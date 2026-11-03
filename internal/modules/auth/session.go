// Package auth implements login, sessions, CSRF protection, MFA (TOTP) and
// password reset.
package auth

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/modules/rbac"
	"github.com/itk-cloud/itk-cloud/internal/modules/settings"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/crypto"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
)

// CookieName is the session cookie name.
const CookieName = "itk_session"

// CSRFHeader carries the per-session CSRF token on unsafe requests.
const CSRFHeader = "X-CSRF-Token"

// Sessions manages server-side sessions. Only a SHA-256 hash of the session
// token is stored, so a database leak does not expose usable cookies.
type Sessions struct {
	Pool     *pgxpool.Pool
	Settings *settings.Store
	TTL      time.Duration
	IdleTTL  time.Duration
	Secure   bool
	// BaseOrigin is the expected Origin for browser requests (scheme://host).
	BaseOrigin string
}

// Session is a stored session.
type Session struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	CSRFToken   string
	MFAVerified bool
	ExpiresAt   time.Time
	LastSeenAt  time.Time
}

// Create starts a new session and returns the raw token.
func (s *Sessions) Create(ctx context.Context, q db.Querier, userID uuid.UUID, mfaVerified bool, ip, ua string) (string, Session, error) {
	token := crypto.RandomToken(32)
	sess := Session{UserID: userID, CSRFToken: crypto.RandomToken(24), MFAVerified: mfaVerified, ExpiresAt: time.Now().Add(s.TTL)}
	if !mfaVerified {
		// A session awaiting MFA is short-lived.
		sess.ExpiresAt = time.Now().Add(10 * time.Minute)
	}
	if len(ua) > 300 {
		ua = ua[:300]
	}
	err := q.QueryRow(ctx, `INSERT INTO sessions (user_id, token_hash, csrf_token, mfa_verified, ip, user_agent, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, last_seen_at`,
		userID, crypto.HashToken(token), sess.CSRFToken, mfaVerified, ip, ua, sess.ExpiresAt).Scan(&sess.ID, &sess.LastSeenAt)
	return token, sess, err
}

// Lookup returns an active session for the raw token.
func (s *Sessions) Lookup(ctx context.Context, token string) (*Session, error) {
	if token == "" || len(token) > 100 {
		return nil, nil
	}
	var sess Session
	err := s.Pool.QueryRow(ctx, `SELECT id, user_id, csrf_token, mfa_verified, expires_at, last_seen_at FROM sessions
		WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at > now()`, crypto.HashToken(token)).
		Scan(&sess.ID, &sess.UserID, &sess.CSRFToken, &sess.MFAVerified, &sess.ExpiresAt, &sess.LastSeenAt)
	if db.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if time.Since(sess.LastSeenAt) > s.IdleTTL {
		_ = s.Revoke(ctx, s.Pool, sess.ID, "idle_timeout")
		return nil, nil
	}
	if time.Since(sess.LastSeenAt) > time.Minute {
		_, _ = s.Pool.Exec(ctx, `UPDATE sessions SET last_seen_at=now() WHERE id=$1`, sess.ID)
	}
	return &sess, nil
}

// Revoke ends a session.
func (s *Sessions) Revoke(ctx context.Context, q db.Querier, id uuid.UUID, _ string) error {
	_, err := q.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE id=$1 AND revoked_at IS NULL`, id)
	return err
}

// RevokeAllForUser ends every session of a user except keep (may be uuid.Nil).
func (s *Sessions) RevokeAllForUser(ctx context.Context, q db.Querier, userID, keep uuid.UUID) error {
	_, err := q.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND id<>$2 AND revoked_at IS NULL`, userID, keep)
	return err
}

// SetCookie writes the session cookie.
func (s *Sessions) SetCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie removes the session cookie.
func (s *Sessions) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteLaxMode})
}

// LoadPrincipal builds the principal for a session.
func (s *Sessions) LoadPrincipal(ctx context.Context, sess *Session, ip, ua string) (*authz.Principal, error) {
	var p authz.Principal
	var status string
	err := s.Pool.QueryRow(ctx, `SELECT u.id, u.username, u.full_name, u.email, u.status, u.must_change_password,
		EXISTS(SELECT 1 FROM mfa_methods m WHERE m.user_id=u.id AND m.confirmed_at IS NOT NULL)
		FROM users u WHERE u.id=$1`, sess.UserID).
		Scan(&p.UserID, &p.Username, &p.FullName, &p.Email, &status, &p.MustChangePassword, &p.MFAEnrolled)
	if db.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if status != "active" {
		_ = s.Revoke(ctx, s.Pool, sess.ID, "user_inactive")
		return nil, nil
	}
	access, err := rbac.LoadUserAccess(ctx, s.Pool, p.UserID)
	if err != nil {
		return nil, err
	}
	p.Roles = access.Roles
	p.MaxRank = access.MaxRank
	p.Permissions = access.Permissions
	p.RequiresMFA = access.RequiresMFA && s.Settings.Bool(ctx, "security.require_mfa_for_admins")
	// A user who enrolled in MFA must always complete it, even if not required.
	p.RequiresMFA = p.RequiresMFA || p.MFAEnrolled
	p.SessionID = sess.ID
	p.MFAVerified = sess.MFAVerified
	p.IP, p.UserAgent = ip, ua
	return &p, nil
}

type sessionKey struct{}

// SessionFrom returns the session attached to the request context.
func SessionFrom(ctx context.Context) *Session {
	s, _ := ctx.Value(sessionKey{}).(*Session)
	return s
}

// Authenticate resolves the session cookie into a principal (if any).
func (s *Sessions) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := audit.WithUserAgent(r.Context(), r.UserAgent())
		c, err := r.Cookie(CookieName)
		if err == nil && c.Value != "" {
			sess, err := s.Lookup(ctx, c.Value)
			if err != nil {
				httpx.WriteError(w, r, err)
				return
			}
			if sess != nil {
				p, err := s.LoadPrincipal(ctx, sess, httpx.ClientIP(ctx), r.UserAgent())
				if err != nil {
					httpx.WriteError(w, r, err)
					return
				}
				if p != nil {
					ctx = authz.WithPrincipal(ctx, p)
					ctx = context.WithValue(ctx, sessionKey{}, sess)
				}
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAuth rejects unauthenticated requests. Sessions that still need MFA
// or a password change may only reach the allow-listed auth endpoints.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := authz.FromContext(r.Context())
		if p == nil {
			httpx.WriteError(w, r, httpx.Unauthorized(""))
			return
		}
		path := r.URL.Path
		if p.RequiresMFA && !p.MFAVerified && !mfaPendingAllowed(path) {
			httpx.WriteError(w, r, httpx.NewError(http.StatusForbidden, "mfa_required", "Multi-factor authentication is required."))
			return
		}
		if p.MustChangePassword && p.MFAVerifiedOrNotRequired() && !passwordChangeAllowed(path) {
			httpx.WriteError(w, r, httpx.NewError(http.StatusForbidden, "password_change_required", "You must change your password before continuing."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func mfaPendingAllowed(path string) bool {
	switch path {
	case "/api/v1/auth/me", "/api/v1/auth/logout", "/api/v1/auth/mfa/verify", "/api/v1/auth/mfa/setup", "/api/v1/auth/mfa/enable":
		return true
	}
	return false
}

func passwordChangeAllowed(path string) bool {
	return mfaPendingAllowed(path) || path == "/api/v1/auth/change-password"
}

// CSRF enforces same-origin requests and, for authenticated sessions, the
// per-session CSRF token on all state-changing methods.
func (s *Sessions) CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		// Workspace app proxy requests come from untrusted apps on the
		// separate proxy origin; they are protected by the SameSite=Lax
		// session cookie and are only served on that origin.
		if strings.HasPrefix(r.URL.Path, "/api/v1/workspaces/") && strings.Contains(r.URL.Path, "/proxy/") {
			next.ServeHTTP(w, r)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !s.originAllowed(origin) {
			httpx.WriteError(w, r, httpx.NewError(http.StatusForbidden, "csrf_failed", "Cross-origin request rejected."))
			return
		}
		if sess := SessionFrom(r.Context()); sess != nil {
			got := r.Header.Get(CSRFHeader)
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRFToken)) != 1 {
				httpx.WriteError(w, r, httpx.NewError(http.StatusForbidden, "csrf_failed", "Missing or invalid CSRF token."))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Sessions) originAllowed(origin string) bool {
	if s.BaseOrigin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Scheme+"://"+u.Host, s.BaseOrigin)
}
