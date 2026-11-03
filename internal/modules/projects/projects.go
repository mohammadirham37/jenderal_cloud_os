// Package projects manages projects, their members and project-scoped
// resources. Access is based on project membership roles:
//
//	owner      — full control, cannot be removed
//	maintainer — manage members (except owner), databases, operate workspaces
//	member     — create workspaces in the project, view resources
//	viewer     — read-only
package projects

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/modules/notifications"
	"github.com/itk-cloud/itk-cloud/internal/modules/resources"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
)

// Project is the API representation.
type Project struct {
	ID             uuid.UUID  `json:"id"`
	Slug           string     `json:"slug"`
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	OwnerID        uuid.UUID  `json:"owner_id"`
	OwnerUsername  string     `json:"owner_username"`
	QuotaID        *uuid.UUID `json:"quota_id"`
	QuotaKey       *string    `json:"quota_key"`
	Status         string     `json:"status"`
	MemberCount    int        `json:"member_count"`
	WorkspaceCount int        `json:"workspace_count"`
	DatabaseCount  int        `json:"database_count"`
	MyRole         *string    `json:"my_role"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

const selectProject = `SELECT p.id, p.slug, p.name, p.description, p.owner_id, u.username, p.quota_id, q.key, p.status,
	(SELECT count(*) FROM project_members m WHERE m.project_id=p.id),
	(SELECT count(*) FROM workspaces w WHERE w.project_id=p.id AND w.status <> 'deleted'),
	(SELECT count(*) FROM databases d WHERE d.project_id=p.id AND d.status <> 'deleted'),
	(SELECT role FROM project_members m WHERE m.project_id=p.id AND m.user_id=$1),
	p.created_at, p.updated_at
	FROM projects p JOIN users u ON u.id=p.owner_id LEFT JOIN resource_quotas q ON q.id=p.quota_id`

func scan(row pgx.Row) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.OwnerID, &p.OwnerUsername, &p.QuotaID, &p.QuotaKey, &p.Status,
		&p.MemberCount, &p.WorkspaceCount, &p.DatabaseCount, &p.MyRole, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// Role ranks.
var roleRank = map[string]int{"viewer": 1, "member": 2, "maintainer": 3, "owner": 4}

// Access describes a principal's access to a project.
type Access struct {
	Role      string // membership role or ""
	ViewAll   bool
	ManageAll bool
}

// AtLeast reports whether the access satisfies a role.
func (a Access) AtLeast(role string) bool {
	if a.ManageAll {
		return true
	}
	if role == "viewer" && a.ViewAll {
		return true
	}
	return roleRank[a.Role] >= roleRank[role]
}

// Authorize loads a project and checks the minimum role (404 without access).
func Authorize(ctx context.Context, q db.Querier, id uuid.UUID, role string) (*Project, Access, error) {
	p := authz.FromContext(ctx)
	pr, err := scan(q.QueryRow(ctx, selectProject+" WHERE p.id=$2", p.UserID, id))
	if db.IsNotFound(err) {
		return nil, Access{}, httpx.NotFound("Project")
	}
	if err != nil {
		return nil, Access{}, err
	}
	a := Access{ViewAll: p.Can("projects.view_all"), ManageAll: p.Can("projects.manage_all")}
	if pr.MyRole != nil {
		a.Role = *pr.MyRole
	}
	if !a.AtLeast("viewer") {
		return nil, a, httpx.NotFound("Project")
	}
	if !a.AtLeast(role) {
		return nil, a, httpx.Forbidden("This action requires the project role " + role + ".")
	}
	return &pr, a, nil
}

// Handler serves project endpoints.
type Handler struct {
	Pool      *pgxpool.Pool
	Resources *resources.Service
}

// List handles GET /projects.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	page, err := httpx.ParsePage(r, []string{"name", "created_at", "updated_at"}, "name")
	if err != nil {
		return err
	}
	args := []any{p.UserID}
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	where := []string{"$1::uuid IS NOT NULL"}
	if !p.CanAny("projects.view_all", "projects.manage_all") || r.URL.Query().Get("mine") == "true" {
		where = append(where, "EXISTS (SELECT 1 FROM project_members m WHERE m.project_id=p.id AND m.user_id=$1)")
	}
	if s := r.URL.Query().Get("status"); s != "" {
		where = append(where, "p.status="+arg(s))
	}
	if page.Search != "" {
		pat := arg(db.LikePattern(page.Search))
		where = append(where, "(p.name ILIKE "+pat+" OR p.slug ILIKE "+pat+")")
	}
	cond := strings.Join(where, " AND ")
	var total int
	if err := h.Pool.QueryRow(ctx, "SELECT count(*) FROM projects p WHERE "+cond, args...).Scan(&total); err != nil {
		return err
	}
	order := page.OrderBy(map[string]string{"name": "p.name", "created_at": "p.created_at", "updated_at": "p.updated_at"})
	lim, off := arg(page.PerPage), arg(page.Offset())
	rows, err := h.Pool.Query(ctx, selectProject+" WHERE "+cond+" ORDER BY "+order+", p.id LIMIT "+lim+" OFFSET "+off, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		pr, err := scan(rows)
		if err != nil {
			return err
		}
		out = append(out, pr)
	}
	return httpx.List(w, out, page.Meta(total))
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,48}[a-z0-9]$`)

type input struct {
	Name        *string `json:"name"`
	Slug        *string `json:"slug"`
	Description *string `json:"description"`
	QuotaKey    *string `json:"quota_key"`
	Status      *string `json:"status"`
}

// Create handles POST /projects.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can("projects.create") {
		return httpx.Forbidden("")
	}
	var in input
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	d := map[string]string{}
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" || len(*in.Name) > 120 {
		d["name"] = "is required (max 120 characters)"
	}
	slug := ""
	autoSlug := false
	if in.Slug != nil && *in.Slug != "" {
		slug = strings.ToLower(*in.Slug)
	} else if in.Name != nil {
		autoSlug = true
		slug = strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(*in.Name), "-"), "-")
		if len(slug) > 43 {
			slug = strings.Trim(slug[:43], "-")
		}
		for len(slug) < 3 {
			slug += "-p"
			slug = strings.TrimLeft(slug, "-")
		}
	}
	if !slugRe.MatchString(slug) {
		d["slug"] = "must be 3-50 lowercase letters, digits or '-'"
	}
	if in.Description != nil && len(*in.Description) > 2000 {
		d["description"] = "must be at most 2000 characters"
	}
	if in.QuotaKey != nil && !p.Can("quotas.manage") {
		d["quota_key"] = "only administrators can assign a project quota"
	}
	if len(d) > 0 {
		return httpx.Validation(d)
	}
	desc := ""
	if in.Description != nil {
		desc = *in.Description
	}
	var id uuid.UUID
	err := db.WithTx(ctx, h.Pool, func(tx pgx.Tx) error {
		var quotaID *uuid.UUID
		if in.QuotaKey != nil && *in.QuotaKey != "" {
			var qid uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT id FROM resource_quotas WHERE key=$1`, *in.QuotaKey).Scan(&qid); err != nil {
				return httpx.Validation(map[string]string{"quota_key": "unknown quota package"})
			}
			quotaID = &qid
		}
		if autoSlug {
			// Many students create projects with the same course name: make a
			// generated slug unique instead of rejecting the request.
			var taken bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE slug=$1)`, slug).Scan(&taken); err != nil {
				return err
			}
			if taken {
				slug = slug + "-" + strings.ReplaceAll(uuid.NewString()[:6], "-", "")
			}
		}
		err := tx.QueryRow(ctx, `INSERT INTO projects (slug, name, description, owner_id, quota_id) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
			slug, strings.TrimSpace(*in.Name), desc, p.UserID, quotaID).Scan(&id)
		if db.IsUniqueViolation(err, "") {
			return httpx.Validation(map[string]string{"slug": "is already taken"})
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO project_members (project_id, user_id, role, added_by) VALUES ($1,$2,'owner',$2)`, id, p.UserID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "project.create", ResourceType: "project", ResourceID: id.String(),
			Metadata: map[string]any{"slug": slug, "name": *in.Name}})
	})
	if err != nil {
		return err
	}
	pr, err := scan(h.Pool.QueryRow(ctx, selectProject+" WHERE p.id=$2", p.UserID, id))
	if err != nil {
		return err
	}
	return httpx.Created(w, pr)
}

// Member is a project member.
type Member struct {
	UserID    uuid.UUID `json:"user_id"`
	Username  string    `json:"username"`
	FullName  string    `json:"full_name"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *Handler) members(ctx context.Context, id uuid.UUID) ([]Member, error) {
	rows, err := h.Pool.Query(ctx, `SELECT m.user_id, u.username, u.full_name, m.role, m.created_at FROM project_members m
		JOIN users u ON u.id=m.user_id WHERE m.project_id=$1 ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'maintainer' THEN 1 WHEN 'member' THEN 2 ELSE 3 END, u.username`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Username, &m.FullName, &m.Role, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func parseID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return id, httpx.NotFound("Project")
	}
	return id, nil
}

// GetOne handles GET /projects/{id}.
func (h *Handler) GetOne(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id, err := parseID(r)
	if err != nil {
		return err
	}
	pr, a, err := Authorize(ctx, h.Pool, id, "viewer")
	if err != nil {
		return err
	}
	members, err := h.members(ctx, id)
	if err != nil {
		return err
	}
	usage, err := h.Resources.ProjectUsage(ctx, h.Pool, id)
	if err != nil {
		return err
	}
	quota, err := h.Resources.ProjectQuota(ctx, h.Pool, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, map[string]any{"project": pr, "members": members, "usage": usage, "quota": quota,
		"can_manage": a.AtLeast("maintainer"), "is_owner": a.Role == "owner" || a.ManageAll})
}

// Update handles PATCH /projects/{id}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	_, a, err := Authorize(ctx, h.Pool, id, "maintainer")
	if err != nil {
		return err
	}
	var in input
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	d := map[string]string{}
	if in.Slug != nil {
		d["slug"] = "cannot be changed"
	}
	if in.Name != nil && (strings.TrimSpace(*in.Name) == "" || len(*in.Name) > 120) {
		d["name"] = "must be 1-120 characters"
	}
	if in.Description != nil && len(*in.Description) > 2000 {
		d["description"] = "must be at most 2000 characters"
	}
	if in.Status != nil && *in.Status != "active" && *in.Status != "archived" {
		d["status"] = "must be active or archived"
	}
	if in.Status != nil && a.Role != "owner" && !a.ManageAll {
		d["status"] = "only the owner can archive the project"
	}
	if in.QuotaKey != nil && !p.Can("quotas.manage") {
		d["quota_key"] = "only administrators can assign a project quota"
	}
	if len(d) > 0 {
		return httpx.Validation(d)
	}
	err = db.WithTx(ctx, h.Pool, func(tx pgx.Tx) error {
		var quotaID *uuid.UUID
		clear := false
		if in.QuotaKey != nil {
			if *in.QuotaKey == "" {
				clear = true
			} else {
				var qid uuid.UUID
				if err := tx.QueryRow(ctx, `SELECT id FROM resource_quotas WHERE key=$1`, *in.QuotaKey).Scan(&qid); err != nil {
					return httpx.Validation(map[string]string{"quota_key": "unknown quota package"})
				}
				quotaID = &qid
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE projects SET name=coalesce($2,name), description=coalesce($3,description), status=coalesce($4,status),
			quota_id=CASE WHEN $6 THEN NULL ELSE coalesce($5,quota_id) END, updated_at=now() WHERE id=$1`,
			id, in.Name, in.Description, in.Status, quotaID, clear); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "project.update", ResourceType: "project", ResourceID: id.String(), Metadata: map[string]any{"input": in}})
	})
	if err != nil {
		return err
	}
	return h.GetOne(w, r)
}

type memberInput struct {
	UserID   *uuid.UUID `json:"user_id"`
	Username *string    `json:"username"`
	Role     string     `json:"role"`
}

// AddMember handles POST /projects/{id}/members (adds or changes a role).
func (h *Handler) AddMember(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	pr, a, err := Authorize(ctx, h.Pool, id, "maintainer")
	if err != nil {
		return err
	}
	var in memberInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if in.Role != "maintainer" && in.Role != "member" && in.Role != "viewer" {
		return httpx.Validation(map[string]string{"role": "must be maintainer, member or viewer"})
	}
	if in.Role == "maintainer" && a.Role != "owner" && !a.ManageAll {
		return httpx.Forbidden("Only the project owner can add maintainers.")
	}
	err = db.WithTx(ctx, h.Pool, func(tx pgx.Tx) error {
		var uid uuid.UUID
		var status string
		var err error
		switch {
		case in.UserID != nil:
			err = tx.QueryRow(ctx, `SELECT id, status FROM users WHERE id=$1`, *in.UserID).Scan(&uid, &status)
		case in.Username != nil:
			err = tx.QueryRow(ctx, `SELECT id, status FROM users WHERE lower(username)=lower($1)`, *in.Username).Scan(&uid, &status)
		default:
			return httpx.Validation(map[string]string{"user_id": "user_id or username is required"})
		}
		if db.IsNotFound(err) {
			return httpx.Validation(map[string]string{"user_id": "user not found"})
		}
		if err != nil {
			return err
		}
		if status != "active" {
			return httpx.Validation(map[string]string{"user_id": "user is suspended"})
		}
		if uid == pr.OwnerID {
			return httpx.Validation(map[string]string{"user_id": "the owner's role cannot be changed"})
		}
		var cur *string
		_ = tx.QueryRow(ctx, `SELECT role FROM project_members WHERE project_id=$1 AND user_id=$2`, id, uid).Scan(&cur)
		if cur != nil && *cur == "maintainer" && a.Role != "owner" && !a.ManageAll {
			return httpx.Forbidden("Only the project owner can change a maintainer's role.")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO project_members (project_id, user_id, role, added_by) VALUES ($1,$2,$3,$4)
			ON CONFLICT (project_id, user_id) DO UPDATE SET role=EXCLUDED.role`, id, uid, in.Role, p.UserID); err != nil {
			return err
		}
		if cur == nil {
			_ = notifications.Notify(ctx, tx, uid, notifications.Message{Type: notifications.TypeSystem, Title: "Added to project " + pr.Name,
				Body: "You were added as " + in.Role + ".", Link: "/projects/" + id.String()})
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "project.member_add", ResourceType: "project", ResourceID: id.String(),
			Metadata: map[string]any{"user_id": uid, "role": in.Role, "previous_role": cur}})
	})
	if err != nil {
		return err
	}
	members, err := h.members(ctx, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, members)
}

// RemoveMember handles DELETE /projects/{id}/members/{userId}. Members may
// remove themselves.
func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	uid, err := uuid.Parse(r.PathValue("userId"))
	if err != nil {
		return httpx.NotFound("Member")
	}
	need := "maintainer"
	if uid == p.UserID {
		need = "viewer"
	}
	pr, a, err := Authorize(ctx, h.Pool, id, need)
	if err != nil {
		return err
	}
	if uid == pr.OwnerID {
		return httpx.Conflict("The project owner cannot be removed.")
	}
	err = db.WithTx(ctx, h.Pool, func(tx pgx.Tx) error {
		var role string
		if err := tx.QueryRow(ctx, `SELECT role FROM project_members WHERE project_id=$1 AND user_id=$2 FOR UPDATE`, id, uid).Scan(&role); err != nil {
			if db.IsNotFound(err) {
				return httpx.NotFound("Member")
			}
			return err
		}
		if role == "maintainer" && uid != p.UserID && a.Role != "owner" && !a.ManageAll {
			return httpx.Forbidden("Only the project owner can remove maintainers.")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM project_members WHERE project_id=$1 AND user_id=$2`, id, uid); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "project.member_remove", ResourceType: "project", ResourceID: id.String(),
			Metadata: map[string]any{"user_id": uid, "role": role}})
	})
	if err != nil {
		return err
	}
	return httpx.NoContent(w)
}

// Activity handles GET /projects/{id}/activity: audit entries for the
// project and its workspaces/databases.
func (h *Handler) Activity(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id, err := parseID(r)
	if err != nil {
		return err
	}
	if _, _, err := Authorize(ctx, h.Pool, id, "viewer"); err != nil {
		return err
	}
	rows, err := h.Pool.Query(ctx, `SELECT a.id, a.occurred_at, a.actor_username, a.action, a.resource_type, a.resource_id, a.outcome
		FROM audit_logs a WHERE (a.resource_type='project' AND a.resource_id=$1)
		OR (a.resource_type='workspace' AND a.resource_id IN (SELECT id::text FROM workspaces WHERE project_id=$2))
		OR (a.resource_type='database' AND a.resource_id IN (SELECT id::text FROM databases WHERE project_id=$2))
		ORDER BY a.occurred_at DESC LIMIT 100`, id.String(), id)
	if err != nil {
		return err
	}
	defer rows.Close()
	type item struct {
		ID           int64     `json:"id"`
		OccurredAt   time.Time `json:"occurred_at"`
		Actor        *string   `json:"actor_username"`
		Action       string    `json:"action"`
		ResourceType string    `json:"resource_type"`
		ResourceID   string    `json:"resource_id"`
		Outcome      string    `json:"outcome"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.OccurredAt, &it.Actor, &it.Action, &it.ResourceType, &it.ResourceID, &it.Outcome); err != nil {
			return err
		}
		out = append(out, it)
	}
	return httpx.OK(w, out)
}

// Delete handles DELETE /projects/{id} (owner or projects.manage_all; the
// project must have no live workspaces or databases).
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id, err := parseID(r)
	if err != nil {
		return err
	}
	pr, a, err := Authorize(ctx, h.Pool, id, "viewer")
	if err != nil {
		return err
	}
	if a.Role != "owner" && !a.ManageAll {
		return httpx.Forbidden("Only the project owner can delete the project.")
	}
	if pr.WorkspaceCount > 0 || pr.DatabaseCount > 0 {
		return httpx.Conflict("Delete or move the project's workspaces and databases first.")
	}
	err = db.WithTx(ctx, h.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE workspaces SET project_id=NULL WHERE project_id=$1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM databases WHERE project_id=$1 AND status='deleted'`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM projects WHERE id=$1`, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "project.delete", ResourceType: "project", ResourceID: id.String(), Metadata: map[string]any{"slug": pr.Slug}})
	})
	if err != nil {
		return err
	}
	return httpx.NoContent(w)
}
