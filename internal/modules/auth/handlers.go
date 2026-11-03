package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/modules/mail"
	"github.com/itk-cloud/itk-cloud/internal/modules/settings"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/crypto"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
	"github.com/itk-cloud/itk-cloud/internal/platform/logging"
	"github.com/itk-cloud/itk-cloud/internal/platform/redisx"
)

// ResetTokenTTL is the lifetime of password reset links.
const ResetTokenTTL = time.Hour

// Handler implements the /auth endpoints.
type Handler struct {
	Sessions *Sessions
	Settings *settings.Store
	Limiter  *redisx.Limiter
	Box      *crypto.SecretBox
	Mail     mail.Sender
	BaseURL  string
	Issuer   string
}

// MeResponse describes the current user and session state.
type MeResponse struct {
	ID                 uuid.UUID `json:"id"`
	Username           string    `json:"username"`
	FullName           string    `json:"full_name"`
	Email              string    `json:"email"`
	Roles              []string  `json:"roles"`
	Permissions        []string  `json:"permissions"`
	MFAEnrolled        bool      `json:"mfa_enrolled"`
	MFARequired        bool      `json:"mfa_required"`
	MFAVerified        bool      `json:"mfa_verified"`
	MFASetupRequired   bool      `json:"mfa_setup_required"`
	MustChangePassword bool      `json:"must_change_password"`
	CSRFToken          string    `json:"csrf_token"`
	SessionExpiresAt   time.Time `json:"session_expires_at"`
}

func meFrom(p *authz.Principal, s *Session) MeResponse {
	perms := p.PermissionList()
	slices.Sort(perms)
	roles := p.Roles
	if roles == nil {
		roles = []string{}
	}
	return MeResponse{
		ID: p.UserID, Username: p.Username, FullName: p.FullName, Email: p.Email,
		Roles: roles, Permissions: perms, MFAEnrolled: p.MFAEnrolled,
		MFARequired: p.RequiresMFA, MFAVerified: s.MFAVerified,
		MFASetupRequired: p.RequiresMFA && !p.MFAEnrolled, MustChangePassword: p.MustChangePassword,
		CSRFToken: s.CSRFToken, SessionExpiresAt: s.ExpiresAt,
	}
}

// Me handles GET /auth/me.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) error {
	p := authz.FromContext(r.Context())
	return httpx.OK(w, meFrom(p, SessionFrom(r.Context())))
}

type loginInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

var errInvalidCredentials = httpx.NewError(http.StatusUnauthorized, "invalid_credentials", "Invalid username or password.")

// Login handles POST /auth/login.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var in loginInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	ident := strings.ToLower(strings.TrimSpace(in.Username))
	if ident == "" || in.Password == "" || len(ident) > 254 || len(in.Password) > 256 {
		return httpx.Validation(map[string]string{"username": "username and password are required"})
	}
	ip := httpx.ClientIP(ctx)
	// Rate limits fail closed: if Redis is unavailable logins are refused.
	if ok, _, err := h.Limiter.Allow(ctx, "login:ip:"+ip, 30, 10*time.Minute); err != nil {
		return httpx.Unavailable("Login is temporarily unavailable.").Wrap(err)
	} else if !ok {
		_ = audit.Record(ctx, h.Sessions.Pool, audit.Entry{Action: "auth.login", Outcome: audit.Denied, ActorUsername: ident, Reason: "rate_limited_ip"})
		return httpx.TooManyRequests()
	}
	if ok, _, err := h.Limiter.Allow(ctx, "login:user:"+ident, 10, 15*time.Minute); err != nil {
		return httpx.Unavailable("Login is temporarily unavailable.").Wrap(err)
	} else if !ok {
		_ = audit.Record(ctx, h.Sessions.Pool, audit.Entry{Action: "auth.login", Outcome: audit.Denied, ActorUsername: ident, Reason: "rate_limited_user"})
		return httpx.TooManyRequests()
	}

	pool := h.Sessions.Pool
	var (
		userID      uuid.UUID
		username    string
		hash        string
		status      string
		lockedUntil *time.Time
		failedCount int
	)
	err := pool.QueryRow(ctx, `SELECT id, username, password_hash, status, locked_until, failed_login_count FROM users
		WHERE lower(username)=$1 OR lower(email)=$1`, ident).Scan(&userID, &username, &hash, &status, &lockedUntil, &failedCount)
	if db.IsNotFound(err) {
		crypto.VerifyDummy(in.Password)
		_ = audit.Record(ctx, pool, audit.Entry{Action: "auth.login", Outcome: audit.Failure, ActorUsername: ident, Reason: "unknown_user"})
		return errInvalidCredentials
	}
	if err != nil {
		return err
	}
	uid := userID
	if lockedUntil != nil && lockedUntil.After(time.Now()) {
		crypto.VerifyDummy(in.Password)
		_ = audit.Record(ctx, pool, audit.Entry{Action: "auth.login", Outcome: audit.Denied, ActorID: &uid, ActorUsername: username, Reason: "locked"})
		return httpx.NewError(http.StatusLocked, "account_locked", "Too many failed attempts. Try again later.")
	}
	needsRehash, verr := crypto.VerifyPassword(hash, in.Password)
	if verr != nil {
		maxAttempts := h.Settings.Int(ctx, "security.login_max_attempts")
		lockMin := h.Settings.Int(ctx, "security.lockout_minutes")
		_, _ = pool.Exec(ctx, `UPDATE users SET
			locked_until = CASE WHEN failed_login_count + 1 >= $2 THEN now() + make_interval(mins => $3) ELSE locked_until END,
			failed_login_count = CASE WHEN failed_login_count + 1 >= $2 THEN 0 ELSE failed_login_count + 1 END
			WHERE id=$1`, userID, maxAttempts, lockMin)
		_ = audit.Record(ctx, pool, audit.Entry{Action: "auth.login", Outcome: audit.Failure, ActorID: &uid, ActorUsername: username, Reason: "bad_password"})
		return errInvalidCredentials
	}
	if status != "active" {
		_ = audit.Record(ctx, pool, audit.Entry{Action: "auth.login", Outcome: audit.Denied, ActorID: &uid, ActorUsername: username, Reason: "suspended"})
		return httpx.NewError(http.StatusForbidden, "account_suspended", "This account is suspended. Contact an administrator.")
	}
	if needsRehash {
		if nh, err := crypto.HashPassword(in.Password); err == nil {
			_, _ = pool.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, userID, nh)
		}
	}
	_ = h.Limiter.Reset(ctx, "login:user:"+ident)

	var token string
	var sess Session
	err = db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET failed_login_count=0, locked_until=NULL, last_login_at=now() WHERE id=$1`, userID); err != nil {
			return err
		}
		var err error
		// Sessions start unverified; LoadPrincipal decides whether MFA is
		// needed. Users without an MFA requirement are verified immediately.
		token, sess, err = h.Sessions.Create(ctx, tx, userID, false, ip, r.UserAgent())
		return err
	})
	if err != nil {
		return err
	}
	p, err := h.Sessions.LoadPrincipal(ctx, &sess, ip, r.UserAgent())
	if err != nil || p == nil {
		return httpx.Internal(err)
	}
	if !p.RequiresMFA {
		if _, err := pool.Exec(ctx, `UPDATE sessions SET mfa_verified=true, expires_at=$2 WHERE id=$1`, sess.ID, time.Now().Add(h.Sessions.TTL)); err != nil {
			return err
		}
		sess.MFAVerified = true
		sess.ExpiresAt = time.Now().Add(h.Sessions.TTL)
		p.MFAVerified = true
	}
	h.Sessions.SetCookie(w, token, sess.ExpiresAt)
	actx := authz.WithPrincipal(ctx, p)
	_ = audit.Record(actx, pool, audit.Entry{Action: "auth.login", ResourceType: "user", ResourceID: userID.String(),
		Metadata: map[string]any{"mfa_pending": p.RequiresMFA}})
	return httpx.OK(w, meFrom(p, &sess))
}

// Logout handles POST /auth/logout.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if s := SessionFrom(ctx); s != nil {
		if err := h.Sessions.Revoke(ctx, h.Sessions.Pool, s.ID, "logout"); err != nil {
			return err
		}
		_ = audit.Record(ctx, h.Sessions.Pool, audit.Entry{Action: "auth.logout", ResourceType: "session", ResourceID: s.ID.String()})
	}
	h.Sessions.ClearCookie(w)
	return httpx.NoContent(w)
}

// rotate replaces the current session with a new verified one (prevents
// session fixation across privilege changes such as MFA completion).
func (h *Handler) rotate(ctx context.Context, w http.ResponseWriter, r *http.Request, tx pgx.Tx, old *Session, userID uuid.UUID) (Session, error) {
	if err := h.Sessions.Revoke(ctx, tx, old.ID, "rotated"); err != nil {
		return Session{}, err
	}
	token, sess, err := h.Sessions.Create(ctx, tx, userID, true, httpx.ClientIP(ctx), r.UserAgent())
	if err != nil {
		return Session{}, err
	}
	h.Sessions.SetCookie(w, token, sess.ExpiresAt)
	return sess, nil
}

type changePasswordInput struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword handles POST /auth/change-password.
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	var in changePasswordInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if msg := crypto.ValidatePasswordStrength(in.NewPassword); msg != "" {
		return httpx.Validation(map[string]string{"new_password": msg})
	}
	if in.NewPassword == in.CurrentPassword {
		return httpx.Validation(map[string]string{"new_password": "must differ from the current password"})
	}
	if ok, _, err := h.Limiter.Allow(ctx, "chpw:"+p.UserID.String(), 10, 15*time.Minute); err != nil || !ok {
		return httpx.TooManyRequests()
	}
	pool := h.Sessions.Pool
	var hash string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1`, p.UserID).Scan(&hash); err != nil {
		return err
	}
	if _, err := crypto.VerifyPassword(hash, in.CurrentPassword); err != nil {
		_ = audit.Record(ctx, pool, audit.Entry{Action: "auth.password_change", Outcome: audit.Failure, ResourceType: "user", ResourceID: p.UserID.String(), Reason: "bad_current_password"})
		return httpx.Validation(map[string]string{"current_password": "is incorrect"})
	}
	nh, err := crypto.HashPassword(in.NewPassword)
	if err != nil {
		return err
	}
	sess := SessionFrom(ctx)
	err = db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2, must_change_password=false, password_changed_at=now(), updated_at=now() WHERE id=$1`, p.UserID, nh); err != nil {
			return err
		}
		if err := h.Sessions.RevokeAllForUser(ctx, tx, p.UserID, sess.ID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "auth.password_change", ResourceType: "user", ResourceID: p.UserID.String()})
	})
	if err != nil {
		return err
	}
	return httpx.NoContent(w)
}

type forgotInput struct {
	Email string `json:"email"`
}

// ForgotPassword handles POST /auth/forgot-password. The response never
// reveals whether the account exists.
func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var in forgotInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if email == "" || len(email) > 254 || !strings.Contains(email, "@") {
		return httpx.Validation(map[string]string{"email": "must be a valid email address"})
	}
	ip := httpx.ClientIP(ctx)
	if ok, _, err := h.Limiter.Allow(ctx, "forgot:ip:"+ip, 10, time.Hour); err != nil || !ok {
		return httpx.TooManyRequests()
	}
	if ok, _, err := h.Limiter.Allow(ctx, "forgot:email:"+email, 3, time.Hour); err != nil || !ok {
		// Silently accept to avoid account enumeration.
		return httpx.Accepted(w, map[string]any{"email_delivery": h.Mail.Enabled()})
	}
	pool := h.Sessions.Pool
	var userID uuid.UUID
	var fullName string
	err := pool.QueryRow(ctx, `SELECT id, full_name FROM users WHERE lower(email)=$1 AND status='active'`, email).Scan(&userID, &fullName)
	if err == nil {
		uid := userID
		if !h.Mail.Enabled() {
			logging.From(ctx).Warn("password reset requested but SMTP is not configured", "user_id", userID)
			_ = audit.Record(ctx, pool, audit.Entry{Action: "auth.password_reset_request", ActorID: &uid, Outcome: audit.Failure, ResourceType: "user", ResourceID: userID.String(), Reason: "email_disabled"})
		} else {
			token, err := CreateResetToken(ctx, pool, userID, nil)
			if err != nil {
				return err
			}
			link := h.BaseURL + "/reset-password#token=" + token
			body := fmt.Sprintf("Halo %s,\n\nGunakan tautan berikut untuk mengatur ulang kata sandi ITK Cloud Anda (berlaku 1 jam):\n\n%s\n\nAbaikan email ini jika Anda tidak memintanya.\n", fullName, link)
			go func() {
				sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := h.Mail.Send(sctx, email, "ITK Cloud - Reset kata sandi", body); err != nil {
					logging.From(ctx).Error("send reset email failed", "error", err)
				}
			}()
			_ = audit.Record(ctx, pool, audit.Entry{Action: "auth.password_reset_request", ActorID: &uid, ResourceType: "user", ResourceID: userID.String()})
		}
	} else if !db.IsNotFound(err) {
		return err
	}
	return httpx.Accepted(w, map[string]any{"email_delivery": h.Mail.Enabled()})
}

// CreateResetToken issues a single-use reset token (only its hash is stored).
func CreateResetToken(ctx context.Context, q db.Querier, userID uuid.UUID, createdBy *uuid.UUID) (string, error) {
	token := crypto.RandomToken(32)
	// Invalidate older outstanding tokens.
	if _, err := q.Exec(ctx, `UPDATE password_reset_tokens SET used_at=now() WHERE user_id=$1 AND used_at IS NULL`, userID); err != nil {
		return "", err
	}
	_, err := q.Exec(ctx, `INSERT INTO password_reset_tokens (user_id, token_hash, created_by, expires_at) VALUES ($1,$2,$3,$4)`,
		userID, crypto.HashToken(token), createdBy, time.Now().Add(ResetTokenTTL))
	return token, err
}

type resetInput struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// ResetPassword handles POST /auth/reset-password.
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var in resetInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if msg := crypto.ValidatePasswordStrength(in.Password); msg != "" {
		return httpx.Validation(map[string]string{"password": msg})
	}
	if ok, _, err := h.Limiter.Allow(ctx, "reset:ip:"+httpx.ClientIP(ctx), 20, time.Hour); err != nil || !ok {
		return httpx.TooManyRequests()
	}
	invalid := httpx.NewError(http.StatusBadRequest, "invalid_token", "This reset link is invalid or has expired.")
	if in.Token == "" || len(in.Token) > 100 {
		return invalid
	}
	hash, err := crypto.HashPassword(in.Password)
	if err != nil {
		return err
	}
	pool := h.Sessions.Pool
	return db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		var tokenID, userID uuid.UUID
		err := tx.QueryRow(ctx, `SELECT t.id, t.user_id FROM password_reset_tokens t JOIN users u ON u.id=t.user_id
			WHERE t.token_hash=$1 AND t.used_at IS NULL AND t.expires_at > now() AND u.status='active' FOR UPDATE OF t`,
			crypto.HashToken(in.Token)).Scan(&tokenID, &userID)
		if db.IsNotFound(err) {
			return invalid
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE password_reset_tokens SET used_at=now() WHERE id=$1`, tokenID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2, must_change_password=false, password_changed_at=now(),
			failed_login_count=0, locked_until=NULL, updated_at=now() WHERE id=$1`, userID, hash); err != nil {
			return err
		}
		if err := h.Sessions.RevokeAllForUser(ctx, tx, userID, uuid.Nil); err != nil {
			return err
		}
		uid := userID
		if err := audit.Record(ctx, tx, audit.Entry{Action: "auth.password_reset", ActorID: &uid, ResourceType: "user", ResourceID: userID.String()}); err != nil {
			return err
		}
		return httpx.NoContent(w)
	})
}

// ---------------------------------------------------------------------------
// MFA (TOTP)
// ---------------------------------------------------------------------------

// MFASetup handles POST /auth/mfa/setup: creates (or replaces) an unconfirmed
// TOTP secret and returns it once for enrollment.
func (h *Handler) MFASetup(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if p.MFAEnrolled {
		return httpx.Conflict("MFA is already enabled. Disable it first to re-enroll.")
	}
	secret := crypto.NewTOTPSecret()
	enc, err := h.Box.Seal([]byte(secret), []byte("mfa:"+p.UserID.String()))
	if err != nil {
		return err
	}
	_, err = h.Sessions.Pool.Exec(ctx, `INSERT INTO mfa_methods (user_id, type, secret_encrypted) VALUES ($1,'totp',$2)
		ON CONFLICT (user_id, type) DO UPDATE SET secret_encrypted=EXCLUDED.secret_encrypted, confirmed_at=NULL,
		last_used_step=0, recovery_codes='{}', updated_at=now() WHERE mfa_methods.confirmed_at IS NULL`, p.UserID, enc)
	if err != nil {
		return err
	}
	return httpx.OK(w, map[string]string{
		"secret":      secret,
		"otpauth_uri": crypto.TOTPURI(h.Issuer, p.Username, secret),
	})
}

type codeInput struct {
	Code         string `json:"code"`
	RecoveryCode string `json:"recovery_code"`
}

func hashRecovery(code string) string {
	norm := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])
}

// MFAEnable handles POST /auth/mfa/enable: confirms enrollment with a code,
// returns one-time recovery codes and upgrades the session.
func (h *Handler) MFAEnable(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	var in codeInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if ok, _, err := h.Limiter.Allow(ctx, "mfa:"+p.UserID.String(), 10, 10*time.Minute); err != nil || !ok {
		return httpx.TooManyRequests()
	}
	pool := h.Sessions.Pool
	var codes []string
	var me MeResponse
	err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		var id uuid.UUID
		var enc []byte
		var confirmed *time.Time
		err := tx.QueryRow(ctx, `SELECT id, secret_encrypted, confirmed_at FROM mfa_methods WHERE user_id=$1 AND type='totp' FOR UPDATE`, p.UserID).Scan(&id, &enc, &confirmed)
		if db.IsNotFound(err) {
			return httpx.Conflict("Start MFA setup first.")
		}
		if err != nil {
			return err
		}
		if confirmed != nil {
			return httpx.Conflict("MFA is already enabled.")
		}
		secret, err := h.Box.Open(enc, []byte("mfa:"+p.UserID.String()))
		if err != nil {
			return err
		}
		step, ok := crypto.VerifyTOTP(string(secret), in.Code, time.Now(), 0)
		if !ok {
			return httpx.Validation(map[string]string{"code": "is incorrect"})
		}
		hashes := make([]string, 0, 10)
		for i := 0; i < 10; i++ {
			c := strings.ToUpper(crypto.RandomPassword(10))
			c = c[:5] + "-" + c[5:]
			codes = append(codes, c)
			hashes = append(hashes, hashRecovery(c))
		}
		if _, err := tx.Exec(ctx, `UPDATE mfa_methods SET confirmed_at=now(), last_used_step=$2, recovery_codes=$3, updated_at=now() WHERE id=$1`, id, step, hashes); err != nil {
			return err
		}
		sess, err := h.rotate(ctx, w, r, tx, SessionFrom(ctx), p.UserID)
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: "auth.mfa_enable", ResourceType: "user", ResourceID: p.UserID.String()}); err != nil {
			return err
		}
		p.MFAEnrolled, p.MFAVerified = true, true
		me = meFrom(p, &sess)
		return nil
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, map[string]any{"recovery_codes": codes, "me": me})
}

// MFAVerify handles POST /auth/mfa/verify for a session awaiting MFA.
func (h *Handler) MFAVerify(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	sess := SessionFrom(ctx)
	if sess.MFAVerified {
		return httpx.OK(w, meFrom(p, sess))
	}
	var in codeInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if ok, _, err := h.Limiter.Allow(ctx, "mfa:"+p.UserID.String(), 10, 10*time.Minute); err != nil || !ok {
		return httpx.TooManyRequests()
	}
	pool := h.Sessions.Pool
	var me MeResponse
	err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		var id uuid.UUID
		var enc []byte
		var lastStep int64
		var recovery []string
		err := tx.QueryRow(ctx, `SELECT id, secret_encrypted, last_used_step, recovery_codes FROM mfa_methods
			WHERE user_id=$1 AND type='totp' AND confirmed_at IS NOT NULL FOR UPDATE`, p.UserID).Scan(&id, &enc, &lastStep, &recovery)
		if db.IsNotFound(err) {
			return httpx.Conflict("MFA is not enrolled; complete MFA setup.")
		}
		if err != nil {
			return err
		}
		method := "totp"
		switch {
		case in.RecoveryCode != "":
			h := hashRecovery(in.RecoveryCode)
			idx := slices.Index(recovery, h)
			if idx < 0 {
				return errMFAFailed(ctx, tx, p)
			}
			recovery = slices.Delete(recovery, idx, idx+1)
			if _, err := tx.Exec(ctx, `UPDATE mfa_methods SET recovery_codes=$2, updated_at=now() WHERE id=$1`, id, recovery); err != nil {
				return err
			}
			method = "recovery_code"
		default:
			secret, err := h.Box.Open(enc, []byte("mfa:"+p.UserID.String()))
			if err != nil {
				return err
			}
			step, ok := crypto.VerifyTOTP(string(secret), in.Code, time.Now(), lastStep)
			if !ok {
				return errMFAFailed(ctx, tx, p)
			}
			if _, err := tx.Exec(ctx, `UPDATE mfa_methods SET last_used_step=$2, updated_at=now() WHERE id=$1`, id, step); err != nil {
				return err
			}
		}
		newSess, err := h.rotate(ctx, w, r, tx, sess, p.UserID)
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: "auth.mfa_verify", ResourceType: "user", ResourceID: p.UserID.String(),
			Metadata: map[string]any{"method": method, "recovery_codes_left": len(recovery)}}); err != nil {
			return err
		}
		p.MFAVerified = true
		me = meFrom(p, &newSess)
		return nil
	})
	if errors.Is(err, errMFAWrong) {
		_ = audit.Record(ctx, pool, audit.Entry{Action: "auth.mfa_verify", Outcome: audit.Failure, ResourceType: "user", ResourceID: p.UserID.String()})
	}
	if err != nil {
		return err
	}
	return httpx.OK(w, me)
}

// errMFAWrong is returned for an incorrect MFA code; the failure is audited
// after the transaction rolls back.
var errMFAWrong = httpx.Validation(map[string]string{"code": "is incorrect"})

func errMFAFailed(_ context.Context, _ pgx.Tx, _ *authz.Principal) error { return errMFAWrong }

type disableMFAInput struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

// MFADisable handles POST /auth/mfa/disable. Users whose role requires MFA
// cannot disable it themselves.
func (h *Handler) MFADisable(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	var in disableMFAInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	pool := h.Sessions.Pool
	access, err := loadRequiresMFA(ctx, pool, p.UserID)
	if err != nil {
		return err
	}
	if access && h.Settings.Bool(ctx, "security.require_mfa_for_admins") {
		return httpx.Forbidden("Your role requires MFA; it cannot be disabled.")
	}
	var hash string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1`, p.UserID).Scan(&hash); err != nil {
		return err
	}
	if _, err := crypto.VerifyPassword(hash, in.Password); err != nil {
		return httpx.Validation(map[string]string{"password": "is incorrect"})
	}
	return db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		var enc []byte
		var last int64
		if err := tx.QueryRow(ctx, `SELECT secret_encrypted, last_used_step FROM mfa_methods WHERE user_id=$1 AND confirmed_at IS NOT NULL FOR UPDATE`, p.UserID).Scan(&enc, &last); err != nil {
			if db.IsNotFound(err) {
				return httpx.Conflict("MFA is not enabled.")
			}
			return err
		}
		secret, err := h.Box.Open(enc, []byte("mfa:"+p.UserID.String()))
		if err != nil {
			return err
		}
		if _, ok := crypto.VerifyTOTP(string(secret), in.Code, time.Now(), last); !ok {
			return httpx.Validation(map[string]string{"code": "is incorrect"})
		}
		if _, err := tx.Exec(ctx, `DELETE FROM mfa_methods WHERE user_id=$1`, p.UserID); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: "auth.mfa_disable", ResourceType: "user", ResourceID: p.UserID.String()}); err != nil {
			return err
		}
		return httpx.NoContent(w)
	})
}

func loadRequiresMFA(ctx context.Context, q db.Querier, userID uuid.UUID) (bool, error) {
	var req bool
	err := q.QueryRow(ctx, `SELECT coalesce(bool_or(r.requires_mfa), false) FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=$1`, userID).Scan(&req)
	return req, err
}

// SessionView is a session listed to its owner.
type SessionView struct {
	ID         uuid.UUID `json:"id"`
	IP         *string   `json:"ip"`
	UserAgent  *string   `json:"user_agent"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Current    bool      `json:"current"`
}

// ListSessions handles GET /auth/sessions.
func (h *Handler) ListSessions(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	rows, err := h.Sessions.Pool.Query(ctx, `SELECT id, ip, user_agent, created_at, last_seen_at, expires_at FROM sessions
		WHERE user_id=$1 AND revoked_at IS NULL AND expires_at > now() ORDER BY last_seen_at DESC LIMIT 50`, p.UserID)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []SessionView{}
	for rows.Next() {
		var s SessionView
		if err := rows.Scan(&s.ID, &s.IP, &s.UserAgent, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt); err != nil {
			return err
		}
		s.Current = s.ID == p.SessionID
		out = append(out, s)
	}
	return httpx.OK(w, out)
}

// RevokeSession handles DELETE /auth/sessions/{id} (own sessions only).
func (h *Handler) RevokeSession(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Session")
	}
	tag, err := h.Sessions.Pool.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, id, p.UserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.NotFound("Session")
	}
	_ = audit.Record(ctx, h.Sessions.Pool, audit.Entry{Action: "auth.session_revoke", ResourceType: "session", ResourceID: id.String()})
	return httpx.NoContent(w)
}
