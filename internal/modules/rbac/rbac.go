// Package rbac loads roles and permissions and manages role definitions.
package rbac

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
)

// Role is a role with its permissions.
type Role struct {
	ID          uuid.UUID `json:"id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	IsSystem    bool      `json:"is_system"`
	RequiresMFA bool      `json:"requires_mfa"`
	Rank        int       `json:"rank"`
	Permissions []string  `json:"permissions"`
	UserCount   int       `json:"user_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Permission describes a permission key.
type Permission struct {
	Key         string `json:"key"`
	Category    string `json:"category"`
	Description string `json:"description"`
}

// UserAccess is the role/permission set for a user.
type UserAccess struct {
	Roles       []string
	MaxRank     int
	RequiresMFA bool
	Permissions map[string]bool
}

// LoadUserAccess returns the roles and effective permissions of a user.
func LoadUserAccess(ctx context.Context, q db.Querier, userID uuid.UUID) (UserAccess, error) {
	ua := UserAccess{Permissions: map[string]bool{}}
	rows, err := q.Query(ctx, `
		SELECT r.key, r.rank, r.requires_mfa, coalesce(array_agg(rp.permission_key) FILTER (WHERE rp.permission_key IS NOT NULL), '{}')
		FROM user_roles ur JOIN roles r ON r.id = ur.role_id
		LEFT JOIN role_permissions rp ON rp.role_id = r.id
		WHERE ur.user_id = $1 GROUP BY r.key, r.rank, r.requires_mfa ORDER BY r.rank DESC`, userID)
	if err != nil {
		return ua, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var rank int
		var mfa bool
		var perms []string
		if err := rows.Scan(&key, &rank, &mfa, &perms); err != nil {
			return ua, err
		}
		ua.Roles = append(ua.Roles, key)
		if rank > ua.MaxRank {
			ua.MaxRank = rank
		}
		ua.RequiresMFA = ua.RequiresMFA || mfa
		for _, p := range perms {
			ua.Permissions[p] = true
		}
	}
	return ua, rows.Err()
}

// CanGrant reports whether actor may grant/revoke a role of the given rank.
// Users may only grant roles strictly below their own rank; holders of
// roles.manage may also grant roles equal to their rank.
func CanGrant(actor *authz.Principal, roleRank int) bool {
	if actor.Can("roles.manage") {
		return roleRank <= actor.MaxRank
	}
	return actor.Can("users.assign_roles") && roleRank < actor.MaxRank
}

// Repo provides role persistence.
type Repo struct{ Pool *pgxpool.Pool }

// List returns all roles with permissions.
func (r *Repo) List(ctx context.Context, q db.Querier) ([]Role, error) {
	rows, err := q.Query(ctx, `
		SELECT r.id, r.key, r.name, r.description, r.is_system, r.requires_mfa, r.rank,
		       coalesce((SELECT array_agg(permission_key ORDER BY permission_key) FROM role_permissions WHERE role_id=r.id), '{}'),
		       (SELECT count(*) FROM user_roles WHERE role_id=r.id), r.created_at, r.updated_at
		FROM roles r ORDER BY r.rank DESC, r.key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		var ro Role
		if err := rows.Scan(&ro.ID, &ro.Key, &ro.Name, &ro.Description, &ro.IsSystem, &ro.RequiresMFA, &ro.Rank,
			&ro.Permissions, &ro.UserCount, &ro.CreatedAt, &ro.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, ro)
	}
	return out, rows.Err()
}

// ByKeys returns roles keyed by key for the given keys.
func (r *Repo) ByKeys(ctx context.Context, q db.Querier, keys []string) (map[string]Role, error) {
	all, err := r.List(ctx, q)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	out := map[string]Role{}
	for _, ro := range all {
		if want[ro.Key] {
			out[ro.Key] = ro
		}
	}
	return out, nil
}

// Permissions returns the permission catalog.
func (r *Repo) Permissions(ctx context.Context) ([]Permission, error) {
	rows, err := r.Pool.Query(ctx, `SELECT key, category, description FROM permissions ORDER BY category, key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Permission{}
	for rows.Next() {
		var p Permission
		if err := rows.Scan(&p.Key, &p.Category, &p.Description); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Handler serves role endpoints.
type Handler struct{ Repo *Repo }

// ListRoles handles GET /roles.
func (h *Handler) ListRoles(w http.ResponseWriter, r *http.Request) error {
	p := authz.FromContext(r.Context())
	if !p.CanAny("roles.view", "users.assign_roles", "users.view") {
		return httpx.Forbidden("")
	}
	roles, err := h.Repo.List(r.Context(), h.Repo.Pool)
	if err != nil {
		return err
	}
	return httpx.OK(w, roles)
}

// ListPermissions handles GET /permissions.
func (h *Handler) ListPermissions(w http.ResponseWriter, r *http.Request) error {
	if !authz.FromContext(r.Context()).CanAny("roles.view", "roles.manage") {
		return httpx.Forbidden("")
	}
	perms, err := h.Repo.Permissions(r.Context())
	if err != nil {
		return err
	}
	return httpx.OK(w, perms)
}

type roleInput struct {
	Key         *string   `json:"key"`
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	RequiresMFA *bool     `json:"requires_mfa"`
	Rank        *int      `json:"rank"`
	Permissions *[]string `json:"permissions"`
}

var roleKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,40}$`)

func (h *Handler) validatePerms(ctx context.Context, tx pgx.Tx, perms []string) error {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM permissions WHERE key = ANY($1)`, perms).Scan(&n); err != nil {
		return err
	}
	uniq := map[string]bool{}
	for _, p := range perms {
		uniq[p] = true
	}
	if n != len(uniq) {
		return httpx.Validation(map[string]string{"permissions": "contains unknown permission keys"})
	}
	return nil
}

// CreateRole handles POST /roles.
func (h *Handler) CreateRole(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can("roles.manage") {
		return httpx.Forbidden("")
	}
	var in roleInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	d := map[string]string{}
	if in.Key == nil || !roleKeyRe.MatchString(*in.Key) {
		d["key"] = "must be lowercase letters, digits or underscore (2-41 chars)"
	}
	if in.Name == nil || len(strings.TrimSpace(*in.Name)) == 0 || len(*in.Name) > 80 {
		d["name"] = "is required (max 80 characters)"
	}
	if in.Rank == nil || *in.Rank < 1 || *in.Rank >= p.MaxRank {
		d["rank"] = "must be between 1 and your own rank (exclusive)"
	}
	if len(d) > 0 {
		return httpx.Validation(d)
	}
	desc := ""
	if in.Description != nil {
		desc = *in.Description
	}
	mfa := in.RequiresMFA != nil && *in.RequiresMFA
	var id uuid.UUID
	err := db.WithTx(ctx, h.Repo.Pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO roles (key, name, description, requires_mfa, rank) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
			*in.Key, strings.TrimSpace(*in.Name), desc, mfa, *in.Rank).Scan(&id)
		if db.IsUniqueViolation(err, "") {
			return httpx.Conflict("A role with this key already exists.")
		}
		if err != nil {
			return err
		}
		if in.Permissions != nil {
			if err := h.validatePerms(ctx, tx, *in.Permissions); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO role_permissions (role_id, permission_key) SELECT $1, unnest($2::text[]) ON CONFLICT DO NOTHING`, id, *in.Permissions); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "role.create", ResourceType: "role", ResourceID: id.String(),
			Metadata: map[string]any{"key": *in.Key, "rank": *in.Rank, "permissions": in.Permissions}})
	})
	if err != nil {
		return err
	}
	roles, err := h.Repo.List(ctx, h.Repo.Pool)
	if err != nil {
		return err
	}
	for _, ro := range roles {
		if ro.ID == id {
			return httpx.Created(w, ro)
		}
	}
	return httpx.Internal(nil)
}

// UpdateRole handles PATCH /roles/{id}.
func (h *Handler) UpdateRole(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can("roles.manage") {
		return httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Role")
	}
	var in roleInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if in.Key != nil {
		return httpx.Validation(map[string]string{"key": "cannot be changed"})
	}
	err = db.WithTx(ctx, h.Repo.Pool, func(tx pgx.Tx) error {
		var key string
		var rank int
		if err := tx.QueryRow(ctx, `SELECT key, rank FROM roles WHERE id=$1 FOR UPDATE`, id).Scan(&key, &rank); err != nil {
			if db.IsNotFound(err) {
				return httpx.NotFound("Role")
			}
			return err
		}
		if key == "super_admin" {
			return httpx.Forbidden("The super administrator role cannot be modified.")
		}
		if rank >= p.MaxRank {
			return httpx.Forbidden("You cannot modify a role at or above your own rank.")
		}
		d := map[string]string{}
		if in.Name != nil && (len(strings.TrimSpace(*in.Name)) == 0 || len(*in.Name) > 80) {
			d["name"] = "is required (max 80 characters)"
		}
		if in.Rank != nil && (*in.Rank < 1 || *in.Rank >= p.MaxRank) {
			d["rank"] = "must be between 1 and your own rank (exclusive)"
		}
		if len(d) > 0 {
			return httpx.Validation(d)
		}
		if _, err := tx.Exec(ctx, `UPDATE roles SET name=coalesce($2,name), description=coalesce($3,description),
			requires_mfa=coalesce($4,requires_mfa), rank=coalesce($5,rank), updated_at=now() WHERE id=$1`,
			id, in.Name, in.Description, in.RequiresMFA, in.Rank); err != nil {
			return err
		}
		if in.Permissions != nil {
			if err := h.validatePerms(ctx, tx, *in.Permissions); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_id=$1`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO role_permissions (role_id, permission_key) SELECT $1, unnest($2::text[]) ON CONFLICT DO NOTHING`, id, *in.Permissions); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "role.update", ResourceType: "role", ResourceID: id.String(),
			Metadata: map[string]any{"key": key, "name": in.Name, "rank": in.Rank, "requires_mfa": in.RequiresMFA, "permissions": in.Permissions}})
	})
	if err != nil {
		return err
	}
	roles, err := h.Repo.List(ctx, h.Repo.Pool)
	if err != nil {
		return err
	}
	for _, ro := range roles {
		if ro.ID == id {
			return httpx.OK(w, ro)
		}
	}
	return httpx.NotFound("Role")
}

// DeleteRole handles DELETE /roles/{id} (custom roles without members only).
func (h *Handler) DeleteRole(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can("roles.manage") {
		return httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Role")
	}
	err = db.WithTx(ctx, h.Repo.Pool, func(tx pgx.Tx) error {
		var key string
		var system bool
		var rank int
		if err := tx.QueryRow(ctx, `SELECT key, is_system, rank FROM roles WHERE id=$1 FOR UPDATE`, id).Scan(&key, &system, &rank); err != nil {
			if db.IsNotFound(err) {
				return httpx.NotFound("Role")
			}
			return err
		}
		if system {
			return httpx.Conflict("Built-in roles cannot be deleted.")
		}
		if rank >= p.MaxRank {
			return httpx.Forbidden("You cannot delete a role at or above your own rank.")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM roles WHERE id=$1`, id); err != nil {
			if db.IsForeignKeyViolation(err) {
				return httpx.Conflict("Remove the role from all users before deleting it.")
			}
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "role.delete", ResourceType: "role", ResourceID: id.String(), Metadata: map[string]any{"key": key}})
	})
	if err != nil {
		return err
	}
	return httpx.NoContent(w)
}
