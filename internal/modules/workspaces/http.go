package workspaces

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/itk-cloud/itk-cloud/internal/modules/agent"
	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// Handler serves workspace endpoints.
type Handler struct{ Svc *Service }

// visibleFilter returns the SQL condition limiting workspaces to those the
// principal may view, using arg placeholder $n for the user ID.
func visibleFilter(p *authz.Principal, n string) string {
	if p.CanAny("workspaces.view_all", "workspaces.manage_all", "workspaces.access_all") {
		// Still reference the placeholder so the argument list stays valid.
		return "(" + n + "::uuid IS NOT NULL)"
	}
	return `(w.owner_id=` + n + ` OR EXISTS (SELECT 1 FROM workspace_members m WHERE m.workspace_id=w.id AND m.user_id=` + n + `)
		OR EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id=w.project_id AND pm.user_id=` + n + `))`
}

// List handles GET /workspaces.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.CanAny("workspaces.view_own", "workspaces.view_all", "workspaces.manage_all") {
		return httpx.Forbidden("")
	}
	page, err := httpx.ParsePage(r, []string{"name", "created_at", "status", "updated_at"}, "-created_at")
	if err != nil {
		return err
	}
	args := []any{p.UserID}
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	where := []string{"w.status <> 'deleted'", visibleFilter(p, "$1")}
	q := r.URL.Query()
	if page.Search != "" {
		pat := arg(db.LikePattern(page.Search))
		where = append(where, "(w.name ILIKE "+pat+" OR w.slug ILIKE "+pat+" OR u.username ILIKE "+pat+")")
	}
	if s := q.Get("status"); s != "" {
		where = append(where, "w.status = "+arg(s))
	}
	if q.Get("mine") == "true" {
		where = append(where, "w.owner_id = $1")
	}
	if v := q.Get("owner_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return httpx.Validation(map[string]string{"owner_id": "must be a UUID"})
		}
		where = append(where, "w.owner_id = "+arg(id))
	}
	if v := q.Get("project_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return httpx.Validation(map[string]string{"project_id": "must be a UUID"})
		}
		where = append(where, "w.project_id = "+arg(id))
	}
	cond := strings.Join(where, " AND ")
	from := " FROM workspaces w JOIN users u ON u.id=w.owner_id WHERE "
	var total int
	if err := h.Svc.Pool.QueryRow(ctx, "SELECT count(*)"+from+cond, args...).Scan(&total); err != nil {
		return err
	}
	order := page.OrderBy(map[string]string{"name": "w.name", "created_at": "w.created_at", "status": "w.status", "updated_at": "w.updated_at"})
	lim, off := arg(page.PerPage), arg(page.Offset())
	rows, err := h.Svc.Pool.Query(ctx, selectWorkspace+" WHERE "+cond+" ORDER BY "+order+", w.id LIMIT "+lim+" OFFSET "+off, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []Workspace{}
	for rows.Next() {
		ws, err := scanWorkspace(rows)
		if err != nil {
			return err
		}
		out = append(out, ws)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range out {
		g, err := ComputeGrants(ctx, h.Svc.Pool, p, &out[i])
		if err != nil {
			return err
		}
		out[i].Access = g.Highest()
	}
	return httpx.List(w, out, page.Meta(total))
}

func parseID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return id, httpx.NotFound("Workspace")
	}
	return id, nil
}

// Detail is the workspace detail response.
type Detail struct {
	Workspace
	Containers []Container `json:"containers"`
	Template   Template    `json:"template"`
	Members    []Member    `json:"members"`
	Grants     Grants      `json:"grants"`
}

// Member is a workspace member.
type Member struct {
	UserID   uuid.UUID `json:"user_id"`
	Username string    `json:"username"`
	FullName string    `json:"full_name"`
	Role     string    `json:"role"`
}

func listMembers(r *http.Request, q db.Querier, id uuid.UUID) ([]Member, error) {
	rows, err := q.Query(r.Context(), `SELECT m.user_id, u.username, u.full_name, m.role FROM workspace_members m JOIN users u ON u.id=m.user_id
		WHERE m.workspace_id=$1 ORDER BY u.username`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Username, &m.FullName, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetOne handles GET /workspaces/{id}.
func (h *Handler) GetOne(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	ws, g, err := Authorize(r.Context(), h.Svc.Pool, r, id, LevelView)
	if err != nil {
		return err
	}
	cs, err := ListContainers(r.Context(), h.Svc.Pool, id)
	if err != nil {
		return err
	}
	tpl, err := GetTemplate(r.Context(), h.Svc.Pool, ws.TemplateID)
	if err != nil {
		return err
	}
	members, err := listMembers(r, h.Svc.Pool, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, Detail{Workspace: *ws, Containers: cs, Template: tpl, Members: members, Grants: g})
}

// Create handles POST /workspaces.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	var in CreateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	ws, err := h.Svc.Create(r.Context(), in)
	if err != nil {
		return err
	}
	ws.Access = "owner"
	return httpx.Accepted(w, ws)
}

type actionInput struct {
	Reason string `json:"reason"`
}

// Action handles POST /workspaces/{id}/{start|stop|restart|suspend|retry}.
func (h *Handler) Action(action string) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := parseID(r)
		if err != nil {
			return err
		}
		level := LevelOperate
		if action == "suspend" || action == "retry" {
			level = LevelManage
		}
		ctx := r.Context()
		ws, _, err := Authorize(ctx, h.Svc.Pool, r, id, level)
		if err != nil {
			return err
		}
		if action == "suspend" && !authz.FromContext(ctx).Can("workspaces.manage_all") {
			return httpx.Forbidden("Only administrators can suspend workspaces.")
		}
		var in actionInput
		if r.ContentLength > 0 {
			if err := httpx.DecodeJSON(r, &in); err != nil {
				return err
			}
		}
		in.Reason = strings.TrimSpace(in.Reason)
		if len(in.Reason) > 500 {
			return httpx.Validation(map[string]string{"reason": "must be at most 500 characters"})
		}
		if action == "suspend" && len(in.Reason) < 3 {
			return httpx.Validation(map[string]string{"reason": "is required when suspending"})
		}
		if in.Reason == "" {
			in.Reason = "requested by user"
		}
		if err := h.Svc.Transition(ctx, ws, action, in.Reason); err != nil {
			return err
		}
		updated, err := Get(ctx, h.Svc.Pool, id)
		if err != nil {
			return err
		}
		updated.Access = ws.Access
		return httpx.Accepted(w, updated)
	}
}

// Delete handles DELETE /workspaces/{id}.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	p := authz.FromContext(ctx)
	ws, g, err := Authorize(ctx, h.Svc.Pool, r, id, LevelView)
	if err != nil {
		return err
	}
	owner := ws.OwnerID == p.UserID
	if !(owner && p.Can("workspaces.delete")) && !(g.Manage && p.Can("workspaces.manage_all")) {
		return httpx.Forbidden("You cannot delete this workspace.")
	}
	reason := strings.TrimSpace(r.URL.Query().Get("reason"))
	if reason == "" {
		reason = "requested by user"
	}
	if err := h.Svc.Transition(ctx, ws, "delete", reason); err != nil {
		return err
	}
	updated, err := Get(ctx, h.Svc.Pool, id)
	if err != nil {
		return err
	}
	return httpx.Accepted(w, updated)
}

type updateInput struct {
	Name           *string `json:"name"`
	IdleTimeoutMin *int    `json:"idle_timeout_min"`
	CPUMillis      *int    `json:"cpu_millis"`
	MemoryBytes    *int64  `json:"memory_bytes"`
}

// Update handles PATCH /workspaces/{id} (settings). CPU/memory changes apply
// to the primary container on next start.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	ws, _, err := Authorize(ctx, h.Svc.Pool, r, id, LevelManage)
	if err != nil {
		return err
	}
	var in updateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	d := map[string]string{}
	if in.Name != nil {
		*in.Name = strings.TrimSpace(*in.Name)
		if *in.Name == "" || len(*in.Name) > 80 {
			d["name"] = "is required (max 80 characters)"
		}
	}
	if in.IdleTimeoutMin != nil && *in.IdleTimeoutMin != 0 && (*in.IdleTimeoutMin < 5 || *in.IdleTimeoutMin > 10080) {
		d["idle_timeout_min"] = "must be 0 (disabled) or between 5 and 10080"
	}
	tpl, err := GetTemplate(ctx, h.Svc.Pool, ws.TemplateID)
	if err != nil {
		return err
	}
	quota, err := h.Svc.Resources.UserQuota(ctx, h.Svc.Pool, ws.OwnerID)
	if err != nil {
		return err
	}
	if in.CPUMillis != nil && (*in.CPUMillis < tpl.MinCPUMillis || *in.CPUMillis > quota.CPUMillis) {
		d["cpu_millis"] = "must be between the template minimum and the owner's quota"
	}
	if in.MemoryBytes != nil && (*in.MemoryBytes < tpl.MinMemoryBytes || *in.MemoryBytes > quota.MemoryBytes) {
		d["memory_bytes"] = "must be between the template minimum and the owner's quota"
	}
	if len(d) > 0 {
		return httpx.Validation(d)
	}
	if (in.CPUMillis != nil || in.MemoryBytes != nil) && ws.Status == StatusRunning {
		return httpx.Conflict("Stop the workspace before changing its CPU or memory limits.")
	}
	err = db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE workspaces SET name=coalesce($2,name),
			idle_timeout_min=CASE WHEN $3::int IS NULL THEN idle_timeout_min ELSE NULLIF($3,0) END,
			cpu_millis=coalesce($4,cpu_millis), memory_bytes=coalesce($5,memory_bytes), updated_at=now() WHERE id=$1`,
			id, in.Name, in.IdleTimeoutMin, in.CPUMillis, in.MemoryBytes); err != nil {
			return err
		}
		if in.CPUMillis != nil || in.MemoryBytes != nil {
			if _, err := tx.Exec(ctx, `UPDATE workspaces SET status_reason=NULL WHERE id=$1`, id); err != nil {
				return err
			}
			// Container limits are fixed at creation: remove the stopped
			// primary container so it is recreated with the new limits on the
			// next start (workspace data stays on the persistent volume).
			var cid uuid.UUID
			var rid *string
			err := tx.QueryRow(ctx, `SELECT id, runtime_id FROM containers WHERE workspace_id=$1 AND is_primary AND removed_at IS NULL FOR UPDATE`, id).Scan(&cid, &rid)
			if err != nil && !db.IsNotFound(err) {
				return err
			}
			if err == nil {
				if rid != nil {
					if err := h.Svc.Agent.Do(ctx, agentproto.OpContainerRemove, cid.String(), "recreate container with new limits",
						agentproto.ContainerRef{ContainerID: cid.String(), RuntimeID: *rid}, nil); err != nil {
						return agent.MapError(err)
					}
				}
				if _, err := tx.Exec(ctx, `UPDATE containers SET runtime_id=NULL, status='stopped', cpu_millis=coalesce($2,cpu_millis),
					memory_bytes=coalesce($3,memory_bytes), updated_at=now() WHERE id=$1`, cid, in.CPUMillis, in.MemoryBytes); err != nil {
					return err
				}
			}
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "workspace.update", ResourceType: "workspace", ResourceID: id.String(),
			Metadata: map[string]any{"name": in.Name, "idle_timeout_min": in.IdleTimeoutMin, "cpu_millis": in.CPUMillis, "memory_bytes": in.MemoryBytes}})
	})
	if err != nil {
		return err
	}
	return h.GetOne(w, r)
}

type memberInput struct {
	UserID uuid.UUID `json:"user_id"`
	Role   string    `json:"role"`
}

// AddMember handles POST /workspaces/{id}/members.
func (h *Handler) AddMember(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	ws, _, err := Authorize(ctx, h.Svc.Pool, r, id, LevelManage)
	if err != nil {
		return err
	}
	var in memberInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if in.Role != "viewer" && in.Role != "editor" {
		return httpx.Validation(map[string]string{"role": "must be viewer or editor"})
	}
	if in.UserID == ws.OwnerID {
		return httpx.Validation(map[string]string{"user_id": "the owner is already a member"})
	}
	err = db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1`, in.UserID).Scan(&status); err != nil {
			if db.IsNotFound(err) {
				return httpx.Validation(map[string]string{"user_id": "user not found"})
			}
			return err
		}
		if status != "active" {
			return httpx.Validation(map[string]string{"user_id": "user is suspended"})
		}
		if _, err := tx.Exec(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role, added_by) VALUES ($1,$2,$3,$4)
			ON CONFLICT (workspace_id, user_id) DO UPDATE SET role=EXCLUDED.role`, id, in.UserID, in.Role, authz.FromContext(ctx).UserID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "workspace.member_add", ResourceType: "workspace", ResourceID: id.String(),
			Metadata: map[string]any{"user_id": in.UserID, "role": in.Role}})
	})
	if err != nil {
		return err
	}
	members, err := listMembers(r, h.Svc.Pool, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, members)
}

// RemoveMember handles DELETE /workspaces/{id}/members/{userId}.
func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if _, _, err := Authorize(ctx, h.Svc.Pool, r, id, LevelManage); err != nil {
		return err
	}
	uid, err := uuid.Parse(r.PathValue("userId"))
	if err != nil {
		return httpx.NotFound("Member")
	}
	err = db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM workspace_members WHERE workspace_id=$1 AND user_id=$2`, id, uid)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("Member")
		}
		if _, err := tx.Exec(ctx, `UPDATE terminal_sessions SET status='closed', ended_at=now(), end_reason='access_revoked'
			WHERE workspace_id=$1 AND user_id=$2 AND status IN ('pending','active')`, id, uid); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "workspace.member_remove", ResourceType: "workspace", ResourceID: id.String(),
			Metadata: map[string]any{"user_id": uid}})
	})
	if err != nil {
		return err
	}
	return httpx.NoContent(w)
}

// Jobs handles GET /workspaces/{id}/jobs: recent lifecycle jobs.
func (h *Handler) Jobs(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	if _, _, err := Authorize(r.Context(), h.Svc.Pool, r, id, LevelView); err != nil {
		return err
	}
	rows, err := h.Svc.Pool.Query(r.Context(), `SELECT id, kind, status, attempts, last_error, created_at, finished_at FROM background_jobs
		WHERE (resource_type='workspace' AND resource_id=$1) OR (resource_type='container' AND resource_id IN
		(SELECT id::text FROM containers WHERE workspace_id=$2)) ORDER BY created_at DESC LIMIT 20`, id.String(), id)
	if err != nil {
		return err
	}
	defer rows.Close()
	type job struct {
		ID         uuid.UUID `json:"id"`
		Kind       string    `json:"kind"`
		Status     string    `json:"status"`
		Attempts   int       `json:"attempts"`
		LastError  *string   `json:"last_error"`
		CreatedAt  any       `json:"created_at"`
		FinishedAt any       `json:"finished_at"`
	}
	out := []job{}
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.ID, &j.Kind, &j.Status, &j.Attempts, &j.LastError, &j.CreatedAt, &j.FinishedAt); err != nil {
			return err
		}
		out = append(out, j)
	}
	return httpx.OK(w, out)
}
