// Package users manages user accounts, role assignment, suspension and
// administrator-initiated password/MFA resets.
package users

import (
	"context"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/modules/auth"
	mailer "github.com/itk-cloud/itk-cloud/internal/modules/mail"
	"github.com/itk-cloud/itk-cloud/internal/modules/rbac"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/crypto"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
)

// User is the API representation of an account.
type User struct {
	ID                 uuid.UUID  `json:"id"`
	Username           string     `json:"username"`
	Email              string     `json:"email"`
	FullName           string     `json:"full_name"`
	IdentityNumber     *string    `json:"identity_number"`
	Status             string     `json:"status"`
	SuspendedReason    *string    `json:"suspended_reason"`
	MustChangePassword bool       `json:"must_change_password"`
	Roles              []string   `json:"roles"`
	QuotaID            *uuid.UUID `json:"quota_id"`
	QuotaKey           *string    `json:"quota_key"`
	MFAEnabled         bool       `json:"mfa_enabled"`
	LastLoginAt        *time.Time `json:"last_login_at"`
	LockedUntil        *time.Time `json:"locked_until"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

const selectUser = `SELECT u.id, u.username, u.email, u.full_name, u.identity_number, u.status, u.suspended_reason,
	u.must_change_password,
	coalesce((SELECT array_agg(r.key ORDER BY r.rank DESC) FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id), '{}'),
	u.quota_id, q.key,
	EXISTS(SELECT 1 FROM mfa_methods m WHERE m.user_id=u.id AND m.confirmed_at IS NOT NULL),
	u.last_login_at, u.locked_until, u.created_at, u.updated_at
	FROM users u LEFT JOIN resource_quotas q ON q.id=u.quota_id`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.FullName, &u.IdentityNumber, &u.Status, &u.SuspendedReason,
		&u.MustChangePassword, &u.Roles, &u.QuotaID, &u.QuotaKey, &u.MFAEnabled, &u.LastLoginAt, &u.LockedUntil, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

// Get loads a user by ID.
func Get(ctx context.Context, q db.Querier, id uuid.UUID) (User, error) {
	return scanUser(q.QueryRow(ctx, selectUser+" WHERE u.id=$1", id))
}

// Handler serves /users endpoints.
type Handler struct {
	Pool     *pgxpool.Pool
	Roles    *rbac.Repo
	Sessions *auth.Sessions
	Mail     mailer.Sender
	BaseURL  string
	// OnSuspend runs inside the suspension transaction (e.g. to schedule
	// stopping the user's running workspaces).
	OnSuspend func(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error
}

// List handles GET /users.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).Can("users.view") {
		return httpx.Forbidden("")
	}
	page, err := httpx.ParsePage(r, []string{"username", "full_name", "created_at", "last_login_at", "email"}, "username")
	if err != nil {
		return err
	}
	where := []string{"true"}
	args := []any{}
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	if page.Search != "" {
		p := arg(db.LikePattern(page.Search))
		where = append(where, "(u.username ILIKE "+p+" OR u.full_name ILIKE "+p+" OR u.email ILIKE "+p+" OR u.identity_number ILIKE "+p+")")
	}
	if s := r.URL.Query().Get("status"); s != "" {
		if s != "active" && s != "suspended" {
			return httpx.Validation(map[string]string{"status": "must be active or suspended"})
		}
		where = append(where, "u.status = "+arg(s))
	}
	if role := r.URL.Query().Get("role"); role != "" {
		where = append(where, "EXISTS (SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id AND r.key="+arg(role)+")")
	}
	w2 := strings.Join(where, " AND ")
	var total int
	if err := h.Pool.QueryRow(ctx, "SELECT count(*) FROM users u WHERE "+w2, args...).Scan(&total); err != nil {
		return err
	}
	order := page.OrderBy(map[string]string{"username": "u.username", "full_name": "u.full_name", "created_at": "u.created_at",
		"last_login_at": "u.last_login_at", "email": "u.email"})
	lim, off := arg(page.PerPage), arg(page.Offset())
	rows, err := h.Pool.Query(ctx, selectUser+" WHERE "+w2+" ORDER BY "+order+" NULLS LAST, u.id LIMIT "+lim+" OFFSET "+off, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return httpx.List(w, out, page.Meta(total))
}

// Lookup handles GET /users/lookup?q= returning minimal identity fields for
// member pickers.
func (h *Handler) Lookup(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).CanAny("users.lookup", "users.view") {
		return httpx.Forbidden("")
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 || len(q) > 64 {
		return httpx.Validation(map[string]string{"q": "must be 2-64 characters"})
	}
	rows, err := h.Pool.Query(ctx, `SELECT id, username, full_name FROM users WHERE status='active'
		AND (username ILIKE $1 OR full_name ILIKE $1) ORDER BY username LIMIT 10`, db.LikePattern(q))
	if err != nil {
		return err
	}
	defer rows.Close()
	type item struct {
		ID       uuid.UUID `json:"id"`
		Username string    `json:"username"`
		FullName string    `json:"full_name"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.Username, &it.FullName); err != nil {
			return err
		}
		out = append(out, it)
	}
	return httpx.OK(w, out)
}

// GetOne handles GET /users/{id} (self or users.view).
func (h *Handler) GetOne(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("User")
	}
	if id != p.UserID && !p.Can("users.view") {
		return httpx.NotFound("User")
	}
	u, err := Get(ctx, h.Pool, id)
	if db.IsNotFound(err) {
		return httpx.NotFound("User")
	}
	if err != nil {
		return err
	}
	return httpx.OK(w, u)
}

var usernameRe = regexp.MustCompile(`^[a-z][a-z0-9._-]{2,31}$`)

type createInput struct {
	Username       string   `json:"username"`
	Email          string   `json:"email"`
	FullName       string   `json:"full_name"`
	IdentityNumber *string  `json:"identity_number"`
	Roles          []string `json:"roles"`
	QuotaKey       *string  `json:"quota_key"`
	Password       *string  `json:"password"`
}

func validateEmail(e string) bool {
	if len(e) > 254 {
		return false
	}
	a, err := mail.ParseAddress(e)
	return err == nil && a.Address == e
}

// Create handles POST /users.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can("users.create") {
		return httpx.Forbidden("")
	}
	var in createInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.FullName = strings.TrimSpace(in.FullName)
	d := map[string]string{}
	if !usernameRe.MatchString(in.Username) {
		d["username"] = "must start with a letter and contain 3-32 lowercase letters, digits, '.', '_' or '-'"
	}
	if !validateEmail(in.Email) {
		d["email"] = "must be a valid email address"
	}
	if in.FullName == "" || len(in.FullName) > 200 {
		d["full_name"] = "is required (max 200 characters)"
	}
	if in.IdentityNumber != nil && len(*in.IdentityNumber) > 50 {
		d["identity_number"] = "must be at most 50 characters"
	}
	if len(in.Roles) == 0 {
		d["roles"] = "at least one role is required"
	}
	generated := ""
	password := ""
	if in.Password != nil && *in.Password != "" {
		if msg := crypto.ValidatePasswordStrength(*in.Password); msg != "" {
			d["password"] = msg
		}
		password = *in.Password
	} else {
		generated = crypto.RandomPassword(16)
		password = generated
	}
	if len(d) > 0 {
		return httpx.Validation(d)
	}
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return err
	}
	var id uuid.UUID
	err = db.WithTx(ctx, h.Pool, func(tx pgx.Tx) error {
		roleIDs, err := h.resolveGrantableRoles(ctx, tx, p, in.Roles)
		if err != nil {
			return err
		}
		var quotaID *uuid.UUID
		if in.QuotaKey != nil && *in.QuotaKey != "" {
			var qid uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT id FROM resource_quotas WHERE key=$1`, *in.QuotaKey).Scan(&qid); err != nil {
				if db.IsNotFound(err) {
					return httpx.Validation(map[string]string{"quota_key": "unknown quota package"})
				}
				return err
			}
			quotaID = &qid
		}
		err = tx.QueryRow(ctx, `INSERT INTO users (username, email, full_name, identity_number, password_hash, must_change_password, quota_id, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
			in.Username, in.Email, in.FullName, in.IdentityNumber, hash, true, quotaID, p.UserID).Scan(&id)
		if db.IsUniqueViolation(err, "users_username_key") {
			return httpx.Validation(map[string]string{"username": "is already taken"})
		}
		if db.IsUniqueViolation(err, "users_email_key") {
			return httpx.Validation(map[string]string{"email": "is already registered"})
		}
		if err != nil {
			return err
		}
		for _, rid := range roleIDs {
			if _, err := tx.Exec(ctx, `INSERT INTO user_roles (user_id, role_id, granted_by) VALUES ($1,$2,$3)`, id, rid, p.UserID); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "user.create", ResourceType: "user", ResourceID: id.String(),
			Metadata: map[string]any{"username": in.Username, "roles": in.Roles, "quota": in.QuotaKey, "generated_password": generated != ""}})
	})
	if err != nil {
		return err
	}
	u, err := Get(ctx, h.Pool, id)
	if err != nil {
		return err
	}
	resp := map[string]any{"user": u}
	if generated != "" {
		// Shown exactly once to the creating administrator.
		resp["temporary_password"] = generated
	}
	return httpx.Created(w, resp)
}

// resolveGrantableRoles maps role keys to IDs, enforcing rank rules.
func (h *Handler) resolveGrantableRoles(ctx context.Context, tx pgx.Tx, p *authz.Principal, keys []string) ([]uuid.UUID, error) {
	roles, err := h.Roles.ByKeys(ctx, tx, keys)
	if err != nil {
		return nil, err
	}
	ids := []uuid.UUID{}
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		ro, ok := roles[k]
		if !ok {
			return nil, httpx.Validation(map[string]string{"roles": "unknown role " + strconv.Quote(k)})
		}
		if !rbac.CanGrant(p, ro.Rank) {
			return nil, httpx.Forbidden("You cannot grant the role " + strconv.Quote(k) + ".")
		}
		ids = append(ids, ro.ID)
	}
	return ids, nil
}

// targetRank returns the highest role rank of a user.
func targetRank(ctx context.Context, q db.Querier, id uuid.UUID) (int, error) {
	var rank int
	err := q.QueryRow(ctx, `SELECT coalesce(max(r.rank), 0) FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=$1`, id).Scan(&rank)
	return rank, err
}

// canManage reports whether actor may manage target (strictly lower rank,
// or equal rank for holders of roles.manage, never oneself for sensitive ops).
func canManage(p *authz.Principal, targetRank int) bool {
	if p.Can("roles.manage") {
		return targetRank <= p.MaxRank
	}
	return targetRank < p.MaxRank
}

type updateInput struct {
	Email          *string   `json:"email"`
	FullName       *string   `json:"full_name"`
	IdentityNumber *string   `json:"identity_number"`
	QuotaKey       *string   `json:"quota_key"`
	Roles          *[]string `json:"roles"`
}

// Update handles PATCH /users/{id}. Users may update their own full name;
// other changes require users.update (and users.assign_roles for roles).
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("User")
	}
	var in updateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	self := id == p.UserID
	if !p.Can("users.update") {
		if !self {
			return httpx.Forbidden("")
		}
		if in.Email != nil || in.IdentityNumber != nil || in.QuotaKey != nil || in.Roles != nil {
			return httpx.Forbidden("You may only change your full name.")
		}
	}
	if in.Roles != nil && !p.CanAny("users.assign_roles", "roles.manage") {
		return httpx.Forbidden("You cannot change roles.")
	}
	if in.Roles != nil && self {
		return httpx.Forbidden("You cannot change your own roles.")
	}
	d := map[string]string{}
	if in.Email != nil {
		*in.Email = strings.ToLower(strings.TrimSpace(*in.Email))
		if !validateEmail(*in.Email) {
			d["email"] = "must be a valid email address"
		}
	}
	if in.FullName != nil {
		*in.FullName = strings.TrimSpace(*in.FullName)
		if *in.FullName == "" || len(*in.FullName) > 200 {
			d["full_name"] = "is required (max 200 characters)"
		}
	}
	if in.IdentityNumber != nil && len(*in.IdentityNumber) > 50 {
		d["identity_number"] = "must be at most 50 characters"
	}
	if in.Roles != nil && len(*in.Roles) == 0 {
		d["roles"] = "at least one role is required"
	}
	if len(d) > 0 {
		return httpx.Validation(d)
	}
	err = db.WithTx(ctx, h.Pool, func(tx pgx.Tx) error {
		before, err := Get(ctx, tx, id)
		if db.IsNotFound(err) {
			return httpx.NotFound("User")
		}
		if err != nil {
			return err
		}
		if !self {
			rank, err := targetRank(ctx, tx, id)
			if err != nil {
				return err
			}
			if !canManage(p, rank) {
				return httpx.Forbidden("You cannot modify a user with an equal or higher role.")
			}
		}
		var quotaID *uuid.UUID
		clearQuota := false
		if in.QuotaKey != nil {
			if *in.QuotaKey == "" {
				clearQuota = true
			} else {
				var qid uuid.UUID
				if err := tx.QueryRow(ctx, `SELECT id FROM resource_quotas WHERE key=$1`, *in.QuotaKey).Scan(&qid); err != nil {
					if db.IsNotFound(err) {
						return httpx.Validation(map[string]string{"quota_key": "unknown quota package"})
					}
					return err
				}
				quotaID = &qid
			}
		}
		_, err = tx.Exec(ctx, `UPDATE users SET email=coalesce($2,email), full_name=coalesce($3,full_name),
			identity_number=CASE WHEN $4::text IS NULL THEN identity_number ELSE NULLIF($4,'') END,
			quota_id=CASE WHEN $6 THEN NULL ELSE coalesce($5, quota_id) END, updated_at=now() WHERE id=$1`,
			id, in.Email, in.FullName, in.IdentityNumber, quotaID, clearQuota)
		if db.IsUniqueViolation(err, "users_email_key") {
			return httpx.Validation(map[string]string{"email": "is already registered"})
		}
		if err != nil {
			return err
		}
		if in.Roles != nil {
			newIDs, err := h.resolveGrantableRoles(ctx, tx, p, *in.Roles)
			if err != nil {
				return err
			}
			// Existing roles the actor cannot grant must not be removed either.
			current, err := h.Roles.ByKeys(ctx, tx, before.Roles)
			if err != nil {
				return err
			}
			for k, ro := range current {
				if !rbac.CanGrant(p, ro.Rank) && !contains(*in.Roles, k) {
					return httpx.Forbidden("You cannot remove the role " + strconv.Quote(k) + ".")
				}
			}
			if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1`, id); err != nil {
				return err
			}
			for _, rid := range newIDs {
				if _, err := tx.Exec(ctx, `INSERT INTO user_roles (user_id, role_id, granted_by) VALUES ($1,$2,$3)`, id, rid, p.UserID); err != nil {
					return err
				}
			}
			// Privilege changes take effect on next request; revoke sessions
			// when roles were reduced so stale MFA state cannot linger.
			if err := h.Sessions.RevokeAllForUser(ctx, tx, id, uuid.Nil); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "user.update", ResourceType: "user", ResourceID: id.String(),
			Metadata: map[string]any{"email": in.Email, "full_name": in.FullName, "identity_number": in.IdentityNumber,
				"quota": in.QuotaKey, "roles_before": before.Roles, "roles_after": in.Roles}})
	})
	if err != nil {
		return err
	}
	u, err := Get(ctx, h.Pool, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, u)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

type reasonInput struct {
	Reason string `json:"reason"`
}

// Suspend handles POST /users/{id}/suspend.
func (h *Handler) Suspend(w http.ResponseWriter, r *http.Request) error {
	return h.setStatus(w, r, "suspended")
}

// Activate handles POST /users/{id}/activate.
func (h *Handler) Activate(w http.ResponseWriter, r *http.Request) error {
	return h.setStatus(w, r, "active")
}

func (h *Handler) setStatus(w http.ResponseWriter, r *http.Request, status string) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can("users.suspend") {
		return httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("User")
	}
	if id == p.UserID {
		return httpx.Forbidden("You cannot change your own account status.")
	}
	var in reasonInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if status == "suspended" && (len(in.Reason) < 3 || len(in.Reason) > 500) {
		return httpx.Validation(map[string]string{"reason": "is required (3-500 characters)"})
	}
	err = db.WithTx(ctx, h.Pool, func(tx pgx.Tx) error {
		var cur string
		if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&cur); err != nil {
			if db.IsNotFound(err) {
				return httpx.NotFound("User")
			}
			return err
		}
		rank, err := targetRank(ctx, tx, id)
		if err != nil {
			return err
		}
		if !canManage(p, rank) {
			return httpx.Forbidden("You cannot change the status of a user with an equal or higher role.")
		}
		if cur == status {
			return nil
		}
		reason := &in.Reason
		if status == "active" {
			reason = nil
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET status=$2, suspended_reason=$3, failed_login_count=0, locked_until=NULL, updated_at=now() WHERE id=$1`, id, status, reason); err != nil {
			return err
		}
		action := "user.activate"
		if status == "suspended" {
			action = "user.suspend"
			if err := h.Sessions.RevokeAllForUser(ctx, tx, id, uuid.Nil); err != nil {
				return err
			}
			if h.OnSuspend != nil {
				if err := h.OnSuspend(ctx, tx, id); err != nil {
					return err
				}
			}
		}
		return audit.Record(ctx, tx, audit.Entry{Action: action, ResourceType: "user", ResourceID: id.String(), Reason: in.Reason})
	})
	if err != nil {
		return err
	}
	u, err := Get(ctx, h.Pool, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, u)
}

// ResetPassword handles POST /users/{id}/reset-password. It issues a
// single-use reset link shown once to the administrator (and emailed when
// SMTP is configured). Existing sessions are revoked.
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can("users.reset_password") {
		return httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("User")
	}
	if id == p.UserID {
		return httpx.Forbidden("Use the change password page for your own account.")
	}
	var token, email, fullName string
	err = db.WithTx(ctx, h.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT email, full_name FROM users WHERE id=$1`, id).Scan(&email, &fullName); err != nil {
			if db.IsNotFound(err) {
				return httpx.NotFound("User")
			}
			return err
		}
		rank, err := targetRank(ctx, tx, id)
		if err != nil {
			return err
		}
		if !canManage(p, rank) {
			return httpx.Forbidden("You cannot reset the password of a user with an equal or higher role.")
		}
		token, err = auth.CreateResetToken(ctx, tx, id, &p.UserID)
		if err != nil {
			return err
		}
		if err := h.Sessions.RevokeAllForUser(ctx, tx, id, uuid.Nil); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "user.reset_password", ResourceType: "user", ResourceID: id.String()})
	})
	if err != nil {
		return err
	}
	link := h.BaseURL + "/reset-password#token=" + token
	emailed := false
	if h.Mail.Enabled() {
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		emailed = h.Mail.Send(sctx, email, "Jenderal Cloud OS - Reset kata sandi",
			"Halo "+fullName+",\n\nAdministrator telah meminta reset kata sandi akun Jenderal Cloud OS Anda. Tautan (berlaku 1 jam):\n\n"+link+"\n") == nil
	}
	return httpx.OK(w, map[string]any{"reset_link": link, "expires_in_seconds": int(auth.ResetTokenTTL.Seconds()), "emailed": emailed})
}

// ResetMFA handles POST /users/{id}/reset-mfa (removes the user's MFA so
// they must re-enroll).
func (h *Handler) ResetMFA(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can("users.reset_password") {
		return httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("User")
	}
	if id == p.UserID {
		return httpx.Forbidden("You cannot reset your own MFA.")
	}
	var in reasonInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if len(strings.TrimSpace(in.Reason)) < 3 {
		return httpx.Validation(map[string]string{"reason": "is required"})
	}
	err = db.WithTx(ctx, h.Pool, func(tx pgx.Tx) error {
		rank, err := targetRank(ctx, tx, id)
		if err != nil {
			return err
		}
		if !canManage(p, rank) {
			return httpx.Forbidden("You cannot reset MFA of a user with an equal or higher role.")
		}
		tag, err := tx.Exec(ctx, `DELETE FROM mfa_methods WHERE user_id=$1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.Conflict("The user has no MFA enrolled.")
		}
		if err := h.Sessions.RevokeAllForUser(ctx, tx, id, uuid.Nil); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "user.reset_mfa", ResourceType: "user", ResourceID: id.String(), Reason: in.Reason})
	})
	if err != nil {
		return err
	}
	return httpx.NoContent(w)
}

// CreateAdmin creates a super administrator. Used only by the CLI bootstrap.
func CreateAdmin(ctx context.Context, pool *pgxpool.Pool, username, email, fullName, password string) (uuid.UUID, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	email = strings.ToLower(strings.TrimSpace(email))
	if !usernameRe.MatchString(username) {
		return uuid.Nil, httpx.Validation(map[string]string{"username": "invalid username"})
	}
	if !validateEmail(email) {
		return uuid.Nil, httpx.Validation(map[string]string{"email": "invalid email"})
	}
	if msg := crypto.ValidatePasswordStrength(password); msg != "" {
		return uuid.Nil, httpx.Validation(map[string]string{"password": msg})
	}
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err = db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO users (username, email, full_name, password_hash) VALUES ($1,$2,$3,$4) RETURNING id`,
			username, email, fullName, hash).Scan(&id); err != nil {
			if db.IsUniqueViolation(err, "") {
				return httpx.Conflict("username or email already exists")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) SELECT $1, id FROM roles WHERE key='super_admin'`, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "user.bootstrap_admin", ResourceType: "user", ResourceID: id.String(),
			ActorUsername: "itkctl", Metadata: map[string]any{"username": username}})
	})
	return id, err
}
