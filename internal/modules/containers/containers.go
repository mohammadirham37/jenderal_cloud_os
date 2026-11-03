// Package containers exposes container management for workspace
// containers. Containers always belong to a workspace; access is derived
// from the workspace access policy.
package containers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/itk-cloud/itk-cloud/internal/modules/agent"
	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/modules/resources"
	ws "github.com/itk-cloud/itk-cloud/internal/modules/workspaces"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// Handler serves /containers.
type Handler struct{ Svc *ws.Service }

func (h *Handler) load(ctx context.Context, r *http.Request, level ws.Level) (*ws.Container, *ws.Workspace, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return nil, nil, httpx.NotFound("Container")
	}
	c, err := ws.ScanContainer(h.Svc.Pool.QueryRow(ctx, ws.SelectContainer+" WHERE c.id=$1 AND c.removed_at IS NULL", id))
	if db.IsNotFound(err) {
		return nil, nil, httpx.NotFound("Container")
	}
	if err != nil {
		return nil, nil, err
	}
	p := authz.FromContext(ctx)
	if p.Can("containers.manage_all") && level <= ws.LevelOperate {
		w, err := ws.Get(ctx, h.Svc.Pool, c.WorkspaceID)
		if err != nil {
			return nil, nil, err
		}
		return &c, &w, nil
	}
	w, _, err := ws.Authorize(ctx, h.Svc.Pool, r, c.WorkspaceID, level)
	if err != nil {
		if e, ok := err.(*httpx.Error); ok && e.Status == http.StatusNotFound {
			return nil, nil, httpx.NotFound("Container")
		}
		return nil, nil, err
	}
	return &c, w, nil
}

// List handles GET /containers.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	page, err := httpx.ParsePage(r, []string{"name", "created_at", "status"}, "-created_at")
	if err != nil {
		return err
	}
	args := []any{p.UserID}
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	where := []string{"c.removed_at IS NULL", "w.status <> 'deleted'"}
	if !p.CanAny("containers.manage_all", "workspaces.view_all", "workspaces.manage_all") {
		where = append(where, `(w.owner_id=$1 OR EXISTS (SELECT 1 FROM workspace_members m WHERE m.workspace_id=w.id AND m.user_id=$1)
			OR EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id=w.project_id AND pm.user_id=$1))`)
	} else {
		where = append(where, "$1::uuid IS NOT NULL")
	}
	q := r.URL.Query()
	if s := q.Get("status"); s != "" {
		where = append(where, "c.status="+arg(s))
	}
	if v := q.Get("workspace_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return httpx.Validation(map[string]string{"workspace_id": "must be a UUID"})
		}
		where = append(where, "c.workspace_id="+arg(id))
	}
	if q.Get("mine") == "true" {
		where = append(where, "c.owner_id=$1")
	}
	if page.Search != "" {
		pat := arg(db.LikePattern(page.Search))
		where = append(where, "(c.name ILIKE "+pat+" OR w.name ILIKE "+pat+" OR u.username ILIKE "+pat+" OR i.reference ILIKE "+pat+")")
	}
	cond := strings.Join(where, " AND ")
	var total int
	if err := h.Svc.Pool.QueryRow(ctx, `SELECT count(*) FROM containers c JOIN workspaces w ON w.id=c.workspace_id JOIN users u ON u.id=c.owner_id
		JOIN container_images i ON i.id=c.image_id WHERE `+cond, args...).Scan(&total); err != nil {
		return err
	}
	order := page.OrderBy(map[string]string{"name": "c.name", "created_at": "c.created_at", "status": "c.status"})
	lim, off := arg(page.PerPage), arg(page.Offset())
	rows, err := h.Svc.Pool.Query(ctx, ws.SelectContainer+" WHERE "+cond+" ORDER BY "+order+", c.id LIMIT "+lim+" OFFSET "+off, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []ws.Container{}
	for rows.Next() {
		c, err := ws.ScanContainer(rows)
		if err != nil {
			return err
		}
		out = append(out, c)
	}
	return httpx.List(w, out, page.Meta(total))
}

// Detail is a container with live runtime state.
type Detail struct {
	ws.Container
	Runtime      *agentproto.ContainerState `json:"runtime"`
	RuntimeError string                     `json:"runtime_error,omitempty"`
	Stats        *agentproto.ContainerStats `json:"stats"`
}

// GetOne handles GET /containers/{id}.
func (h *Handler) GetOne(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	c, _, err := h.load(ctx, r, ws.LevelView)
	if err != nil {
		return err
	}
	d := Detail{Container: *c}
	if c.RuntimeID != nil {
		actx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var st agentproto.ContainerState
		if err := h.Svc.Agent.Do(actx, agentproto.OpContainerInspect, c.ID.String(), "view container",
			agentproto.ContainerRef{ContainerID: c.ID.String(), RuntimeID: *c.RuntimeID}, &st); err != nil {
			d.RuntimeError = "Runtime state unavailable"
		} else {
			st.Labels = nil
			d.Runtime = &st
			if st.Status == "running" {
				var stats []agentproto.ContainerStats
				if err := h.Svc.Agent.Do(actx, agentproto.OpContainerStats, c.ID.String(), "view container stats",
					agentproto.ContainerStatsReq{RuntimeIDs: []string{*c.RuntimeID}}, &stats); err == nil && len(stats) == 1 {
					d.Stats = &stats[0]
				}
			}
		}
	}
	return httpx.OK(w, d)
}

// Logs handles GET /containers/{id}/logs?tail=200.
func (h *Handler) Logs(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	c, _, err := h.load(ctx, r, ws.LevelOperate)
	if err != nil {
		return err
	}
	tail := 200
	if v := r.URL.Query().Get("tail"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 5000 {
			return httpx.Validation(map[string]string{"tail": "must be between 1 and 5000"})
		}
		tail = n
	}
	if c.RuntimeID == nil {
		return httpx.OK(w, agentproto.LogsResult{Lines: []string{}})
	}
	var res agentproto.LogsResult
	if err := h.Svc.Agent.Do(ctx, agentproto.OpContainerLogs, c.ID.String(), "view container logs", agentproto.ContainerLogs{
		ContainerRef: agentproto.ContainerRef{ContainerID: c.ID.String(), RuntimeID: *c.RuntimeID}, Tail: tail,
		Timestamps: r.URL.Query().Get("timestamps") == "true"}, &res); err != nil {
		return agent.MapError(err)
	}
	if res.Lines == nil {
		res.Lines = []string{}
	}
	return httpx.OK(w, res)
}

// Action handles POST /containers/{id}/{start|stop|restart}.
func (h *Handler) Action(action string) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		p := authz.FromContext(ctx)
		c, wsp, err := h.load(ctx, r, ws.LevelOperate)
		if err != nil {
			return err
		}
		if !p.CanAny("containers.stop", "containers.manage_all") {
			return httpx.Forbidden("")
		}
		var kind string
		err = db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
			var status string
			if err := tx.QueryRow(ctx, `SELECT status FROM containers WHERE id=$1 FOR UPDATE`, c.ID).Scan(&status); err != nil {
				return err
			}
			var busy bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM background_jobs WHERE status IN ('queued','running') AND
				((resource_type='container' AND resource_id=$1) OR (resource_type='workspace' AND resource_id=$2)))`,
				c.ID.String(), c.WorkspaceID.String()).Scan(&busy); err != nil {
				return err
			}
			if busy {
				return httpx.Conflict("Another operation is in progress for this container or its workspace.")
			}
			switch action {
			case "start":
				if status == "running" {
					return httpx.Conflict("The container is already running.")
				}
				switch wsp.Status {
				case ws.StatusReady, ws.StatusRunning, ws.StatusStopped:
				default:
					return httpx.Conflict(fmt.Sprintf("Containers cannot be started while the workspace is %s.", wsp.Status))
				}
				if err := h.Svc.Resources.CheckRun(ctx, tx, wsp.OwnerID, wsp.ProjectID, c.CPUMillis, c.MemoryBytes); err != nil {
					return err
				}
				if err := h.Svc.Resources.CheckHostCapacity(ctx, tx, c.CPUMillis, c.MemoryBytes); err != nil {
					return err
				}
				kind = ws.JobContainerStart
			case "stop":
				if status != "running" {
					return httpx.Conflict("The container is not running.")
				}
				kind = ws.JobContainerStop
			case "restart":
				if status != "running" {
					return httpx.Conflict("Only running containers can be restarted.")
				}
				kind = ws.JobContainerRestart
			}
			if _, err := ws.EnqueueContainerJob(ctx, tx, kind, c.WorkspaceID, c.ID, "requested by user"); err != nil {
				return err
			}
			return audit.Record(ctx, tx, audit.Entry{Action: "container." + action, ResourceType: "container", ResourceID: c.ID.String(),
				Metadata: map[string]any{"workspace_id": c.WorkspaceID, "owner_id": c.OwnerID}})
		})
		if err != nil {
			return err
		}
		out, err := ws.ScanContainer(h.Svc.Pool.QueryRow(ctx, ws.SelectContainer+" WHERE c.id=$1", c.ID))
		if err != nil {
			return err
		}
		return httpx.Accepted(w, out)
	}
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}[a-z0-9]$`)

type createInput struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
	TemplateID  uuid.UUID `json:"template_id"`
	Name        string    `json:"name"`
	CPUMillis   *int      `json:"cpu_millis"`
	MemoryBytes *int64    `json:"memory_bytes"`
}

// Create handles POST /containers: an additional container in a workspace,
// sharing the workspace volume and network.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can("containers.create") {
		return httpx.Forbidden("")
	}
	var in createInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	if !nameRe.MatchString(in.Name) {
		return httpx.Validation(map[string]string{"name": "must be 2-40 lowercase letters, digits or '-'"})
	}
	wsp, _, err := ws.Authorize(ctx, h.Svc.Pool, r, in.WorkspaceID, ws.LevelEdit)
	if err != nil {
		return err
	}
	if wsp.Type != "container" {
		return httpx.Validation(map[string]string{"workspace_id": "containers can only be added to container workspaces"})
	}
	switch wsp.Status {
	case ws.StatusReady, ws.StatusRunning, ws.StatusStopped:
	default:
		return httpx.Conflict(fmt.Sprintf("Containers cannot be added while the workspace is %s.", wsp.Status))
	}
	tpl, err := ws.GetTemplate(ctx, h.Svc.Pool, in.TemplateID)
	if db.IsNotFound(err) || (err == nil && (!tpl.IsActive || !tpl.ImageAllowed)) {
		return httpx.Validation(map[string]string{"template_id": "template is not available"})
	}
	if err != nil {
		return err
	}
	cpu, mem := tpl.RecCPUMillis, tpl.RecMemoryBytes
	if in.CPUMillis != nil {
		cpu = *in.CPUMillis
	}
	if in.MemoryBytes != nil {
		mem = *in.MemoryBytes
	}
	var id uuid.UUID
	err = db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id=$1 FOR UPDATE`, wsp.OwnerID); err != nil {
			return err
		}
		quota, err := h.Svc.Resources.UserQuota(ctx, tx, wsp.OwnerID)
		if err != nil {
			return err
		}
		if cpu < tpl.MinCPUMillis || cpu > quota.CPUMillis {
			return httpx.Validation(map[string]string{"cpu_millis": fmt.Sprintf("must be between %d and %d", tpl.MinCPUMillis, quota.CPUMillis)})
		}
		if mem < tpl.MinMemoryBytes || mem > quota.MemoryBytes {
			return httpx.Validation(map[string]string{"memory_bytes": "must be between the template minimum and the quota"})
		}
		usage, err := h.Svc.Resources.UserUsage(ctx, tx, wsp.OwnerID)
		if err != nil {
			return err
		}
		if usage.Containers >= quota.MaxContainers {
			return resources.QuotaError(fmt.Sprintf("Container limit reached (%d of %d).", usage.Containers, quota.MaxContainers))
		}
		ports := tpl.Ports
		if ports == nil {
			ports = []ws.Port{}
		}
		pb, _ := jsonMarshal(ports)
		err = tx.QueryRow(ctx, `INSERT INTO containers (workspace_id, owner_id, template_id, image_id, name, is_primary, cpu_millis, memory_bytes, pids_limit, ports)
			VALUES ($1,$2,$3,$4,$5,false,$6,$7,512,$8) RETURNING id`, wsp.ID, wsp.OwnerID, tpl.ID, tpl.ImageID, in.Name, cpu, mem, pb).Scan(&id)
		if db.IsUniqueViolation(err, "") {
			return httpx.Validation(map[string]string{"name": "a container with this name already exists in the workspace"})
		}
		if err != nil {
			return err
		}
		if _, err := ws.EnqueueContainerJob(ctx, tx, ws.JobContainerCreate, wsp.ID, id, "container created"); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "container.create", ResourceType: "container", ResourceID: id.String(),
			Metadata: map[string]any{"workspace_id": wsp.ID, "template": tpl.Key, "cpu_millis": cpu, "memory_bytes": mem}})
	})
	if err != nil {
		return err
	}
	c, err := ws.ScanContainer(h.Svc.Pool.QueryRow(ctx, ws.SelectContainer+" WHERE c.id=$1", id))
	if err != nil {
		return err
	}
	return httpx.Accepted(w, c)
}

// Delete handles DELETE /containers/{id} (additional containers only).
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	c, _, err := h.load(ctx, r, ws.LevelEdit)
	if err != nil {
		return err
	}
	if c.IsPrimary {
		return httpx.Conflict("The primary container is removed together with its workspace.")
	}
	err = db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE containers SET status='removing', updated_at=now() WHERE id=$1`, c.ID); err != nil {
			return err
		}
		if _, err := ws.EnqueueContainerJob(ctx, tx, ws.JobContainerRemove, c.WorkspaceID, c.ID, "deleted by user"); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "container.delete", ResourceType: "container", ResourceID: c.ID.String(),
			Metadata: map[string]any{"workspace_id": c.WorkspaceID}})
	})
	if err != nil {
		return err
	}
	return httpx.NoContent(w)
}
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
