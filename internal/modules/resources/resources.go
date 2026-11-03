// Package resources manages quota packages, computes effective quotas and
// usage, and checks host capacity before scheduling workloads.
package resources

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/modules/settings"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
)

// Quota is a quota package.
type Quota struct {
	ID                      uuid.UUID `json:"id"`
	Key                     string    `json:"key"`
	Name                    string    `json:"name"`
	Description             string    `json:"description"`
	CPUMillis               int       `json:"cpu_millis"`
	MemoryBytes             int64     `json:"memory_bytes"`
	StorageBytes            int64     `json:"storage_bytes"`
	MaxWorkspaces           int       `json:"max_workspaces"`
	MaxContainers           int       `json:"max_containers"`
	MaxDatabases            int       `json:"max_databases"`
	MaxTerminalSessions     int       `json:"max_terminal_sessions"`
	GPUAllowed              bool      `json:"gpu_allowed"`
	MaxWorkspaceDurationMin *int      `json:"max_workspace_duration_min"`
	IdleTimeoutMin          *int      `json:"idle_timeout_min"`
	UserCount               int       `json:"user_count"`
	ProjectCount            int       `json:"project_count"`
}

const selectQuota = `SELECT q.id, q.key, q.name, q.description, q.cpu_millis, q.memory_bytes, q.storage_bytes, q.max_workspaces,
	q.max_containers, q.max_databases, q.max_terminal_sessions, q.gpu_allowed, q.max_workspace_duration_min, q.idle_timeout_min,
	(SELECT count(*) FROM users u WHERE u.quota_id=q.id), (SELECT count(*) FROM projects p WHERE p.quota_id=q.id)
	FROM resource_quotas q`

func scanQuota(row pgx.Row) (Quota, error) {
	var q Quota
	err := row.Scan(&q.ID, &q.Key, &q.Name, &q.Description, &q.CPUMillis, &q.MemoryBytes, &q.StorageBytes, &q.MaxWorkspaces,
		&q.MaxContainers, &q.MaxDatabases, &q.MaxTerminalSessions, &q.GPUAllowed, &q.MaxWorkspaceDurationMin, &q.IdleTimeoutMin,
		&q.UserCount, &q.ProjectCount)
	return q, err
}

// Service computes quotas and usage.
type Service struct {
	Pool     *pgxpool.Pool
	Settings *settings.Store
}

// UserQuota returns the effective quota for a user (explicit package or the
// configured default package).
func (s *Service) UserQuota(ctx context.Context, q db.Querier, userID uuid.UUID) (Quota, error) {
	quota, err := scanQuota(q.QueryRow(ctx, selectQuota+` WHERE q.id = (SELECT quota_id FROM users WHERE id=$1)`, userID))
	if err == nil {
		return quota, nil
	}
	if !db.IsNotFound(err) {
		return quota, err
	}
	def := s.Settings.String(ctx, "quota.default_package")
	quota, err = scanQuota(q.QueryRow(ctx, selectQuota+` WHERE q.key=$1`, def))
	if db.IsNotFound(err) {
		return quota, fmt.Errorf("default quota package %q does not exist", def)
	}
	return quota, err
}

// ProjectQuota returns the project's quota package, if any.
func (s *Service) ProjectQuota(ctx context.Context, q db.Querier, projectID uuid.UUID) (*Quota, error) {
	quota, err := scanQuota(q.QueryRow(ctx, selectQuota+` WHERE q.id = (SELECT quota_id FROM projects WHERE id=$1)`, projectID))
	if db.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &quota, nil
}

// Usage is the aggregate resource consumption of a user or project.
type Usage struct {
	Workspaces       int   `json:"workspaces"`
	Containers       int   `json:"containers"`
	Databases        int   `json:"databases"`
	TerminalSessions int   `json:"terminal_sessions"`
	RunningCPUMillis int   `json:"running_cpu_millis"`
	RunningMemory    int64 `json:"running_memory_bytes"`
	AllocatedStorage int64 `json:"allocated_storage_bytes"`
	UsedStorage      int64 `json:"used_storage_bytes"`
	ActiveGPUs       int   `json:"active_gpu_allocations"`
}

// Live workspace statuses that count toward quotas.
const liveWorkspace = `w.status NOT IN ('deleted','deleting')`

// running resources: running containers plus running native workspaces.
const runningCPUMem = `
	SELECT coalesce(sum(c.cpu_millis),0), coalesce(sum(c.memory_bytes),0) FROM containers c JOIN workspaces w ON w.id=c.workspace_id
	WHERE c.removed_at IS NULL AND c.status IN ('running','starting') AND %[1]s
	UNION ALL
	SELECT coalesce(sum(w.cpu_millis),0), coalesce(sum(w.memory_bytes),0) FROM workspaces w
	WHERE w.type='native' AND w.status='running' AND %[1]s`

// UserUsage returns the usage of a user's own workspaces.
func (s *Service) UserUsage(ctx context.Context, q db.Querier, userID uuid.UUID) (Usage, error) {
	return s.usage(ctx, q, "w.owner_id=$1", "created_by=$1", userID)
}

// ProjectUsage returns the usage of a project's workspaces and databases.
func (s *Service) ProjectUsage(ctx context.Context, q db.Querier, projectID uuid.UUID) (Usage, error) {
	return s.usage(ctx, q, "w.project_id=$1", "project_id=$1", projectID)
}

func (s *Service) usage(ctx context.Context, q db.Querier, wsCond, dbCond string, id uuid.UUID) (Usage, error) {
	var u Usage
	err := q.QueryRow(ctx, `SELECT count(*), coalesce(sum(w.storage_bytes),0), coalesce(sum(w.storage_used_bytes),0)
		FROM workspaces w WHERE `+wsCond+` AND `+liveWorkspace, id).Scan(&u.Workspaces, &u.AllocatedStorage, &u.UsedStorage)
	if err != nil {
		return u, err
	}
	if err := q.QueryRow(ctx, `SELECT count(*) FROM containers c JOIN workspaces w ON w.id=c.workspace_id
		WHERE c.removed_at IS NULL AND `+wsCond+` AND `+liveWorkspace, id).Scan(&u.Containers); err != nil {
		return u, err
	}
	rows, err := q.Query(ctx, fmt.Sprintf(runningCPUMem, wsCond), id)
	if err != nil {
		return u, err
	}
	for rows.Next() {
		var cpu int
		var mem int64
		if err := rows.Scan(&cpu, &mem); err != nil {
			rows.Close()
			return u, err
		}
		u.RunningCPUMillis += cpu
		u.RunningMemory += mem
	}
	rows.Close()
	if err := q.QueryRow(ctx, `SELECT count(*) FROM databases WHERE `+dbCond+` AND status NOT IN ('deleted','deleting')`, id).Scan(&u.Databases); err != nil {
		return u, err
	}
	if err := q.QueryRow(ctx, `SELECT count(*) FROM terminal_sessions t JOIN workspaces w ON w.id=t.workspace_id
		WHERE `+strings.Replace(wsCond, "w.owner_id", "t.user_id", 1)+` AND t.status IN ('pending','active') AND t.expires_at > now()`, id).Scan(&u.TerminalSessions); err != nil {
		return u, err
	}
	err = q.QueryRow(ctx, `SELECT count(*) FROM gpu_allocations a JOIN workspaces w ON w.id=a.workspace_id WHERE `+wsCond+` AND a.status='active'`, id).Scan(&u.ActiveGPUs)
	return u, err
}

// QuotaError builds a 422 quota error.
func QuotaError(msg string) *httpx.Error {
	return httpx.NewError(http.StatusUnprocessableEntity, "quota_exceeded", msg)
}

// CheckRun verifies that starting cpu/mem more stays within the user's (and
// project's) running quota.
func (s *Service) CheckRun(ctx context.Context, q db.Querier, ownerID uuid.UUID, projectID *uuid.UUID, cpu int, mem int64) error {
	quota, err := s.UserQuota(ctx, q, ownerID)
	if err != nil {
		return err
	}
	u, err := s.UserUsage(ctx, q, ownerID)
	if err != nil {
		return err
	}
	if u.RunningCPUMillis+cpu > quota.CPUMillis {
		return QuotaError(fmt.Sprintf("Starting this would exceed your CPU quota (%s of %s cores in use).", cores(u.RunningCPUMillis), cores(quota.CPUMillis)))
	}
	if u.RunningMemory+mem > quota.MemoryBytes {
		return QuotaError(fmt.Sprintf("Starting this would exceed your memory quota (%s of %s in use).", HumanBytes(u.RunningMemory), HumanBytes(quota.MemoryBytes)))
	}
	if projectID != nil {
		pq, err := s.ProjectQuota(ctx, q, *projectID)
		if err != nil {
			return err
		}
		if pq != nil {
			pu, err := s.ProjectUsage(ctx, q, *projectID)
			if err != nil {
				return err
			}
			if pu.RunningCPUMillis+cpu > pq.CPUMillis || pu.RunningMemory+mem > pq.MemoryBytes {
				return QuotaError("Starting this would exceed the project's CPU or memory quota.")
			}
		}
	}
	return nil
}

func cores(m int) string {
	return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.2f", float64(m)/1000), "0"), ".0")
}

// HumanBytes formats bytes using binary units.
func HumanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// Capacity describes host capacity used by the scheduler.
type Capacity struct {
	CPUThreads  int
	MemoryTotal int64
	DiskTotal   int64
	Known       bool
}

// HostCapacity reads the most recent host sample and agent info.
func (s *Service) HostCapacity(ctx context.Context, q db.Querier) Capacity {
	var c Capacity
	var mem, disk *int64
	err := q.QueryRow(ctx, `SELECT memory_total_bytes, disk_total_bytes FROM resource_usage_samples
		WHERE scope='host' AND collected_at > now() - interval '10 minutes' ORDER BY collected_at DESC LIMIT 1`).Scan(&mem, &disk)
	if err != nil || mem == nil {
		return c
	}
	c.MemoryTotal = *mem
	if disk != nil {
		c.DiskTotal = *disk
	}
	var threads *int
	_ = q.QueryRow(ctx, `SELECT (host_info->>'cpu_threads')::int FROM agent_nodes WHERE name='local'`).Scan(&threads)
	if threads != nil {
		c.CPUThreads = *threads
	}
	c.Known = true
	return c
}

// CheckHostCapacity ensures the host can accommodate additional running
// memory (memory is not overcommitted beyond 90% of host RAM) and CPU (up to
// the configured overcommit ratio).
func (s *Service) CheckHostCapacity(ctx context.Context, q db.Querier, cpu int, mem int64) error {
	c := s.HostCapacity(ctx, q)
	if !c.Known {
		return nil // capacity unknown (no recent sample); quota limits still apply
	}
	var runCPU int
	var runMem int64
	rows, err := q.Query(ctx, fmt.Sprintf(runningCPUMem, "true"))
	if err != nil {
		return err
	}
	for rows.Next() {
		var a int
		var b int64
		if err := rows.Scan(&a, &b); err != nil {
			rows.Close()
			return err
		}
		runCPU += a
		runMem += b
	}
	rows.Close()
	if float64(runMem+mem) > 0.9*float64(c.MemoryTotal) {
		return httpx.NewError(http.StatusServiceUnavailable, "insufficient_capacity",
			"The server does not have enough free memory to start this workload. Try again later or ask an administrator.")
	}
	if c.CPUThreads > 0 {
		ratio := s.Settings.Int(ctx, "scheduler.cpu_overcommit_pct")
		if ratio <= 0 {
			ratio = 200
		}
		if runCPU+cpu > c.CPUThreads*1000*ratio/100 {
			return httpx.NewError(http.StatusServiceUnavailable, "insufficient_capacity",
				"The server CPU capacity is fully allocated. Try again later or ask an administrator.")
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// HTTP: quota packages and usage
// ---------------------------------------------------------------------------

// Handler serves /quotas and usage endpoints.
type Handler struct{ Svc *Service }

// ListQuotas handles GET /quotas.
func (h *Handler) ListQuotas(w http.ResponseWriter, r *http.Request) error {
	if !authz.FromContext(r.Context()).CanAny("quotas.view", "quotas.manage") {
		return httpx.Forbidden("")
	}
	rows, err := h.Svc.Pool.Query(r.Context(), selectQuota+" ORDER BY q.cpu_millis, q.key")
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []Quota{}
	for rows.Next() {
		q, err := scanQuota(rows)
		if err != nil {
			return err
		}
		out = append(out, q)
	}
	return httpx.OK(w, out)
}

type quotaInput struct {
	Key                     *string `json:"key"`
	Name                    *string `json:"name"`
	Description             *string `json:"description"`
	CPUMillis               *int    `json:"cpu_millis"`
	MemoryBytes             *int64  `json:"memory_bytes"`
	StorageBytes            *int64  `json:"storage_bytes"`
	MaxWorkspaces           *int    `json:"max_workspaces"`
	MaxContainers           *int    `json:"max_containers"`
	MaxDatabases            *int    `json:"max_databases"`
	MaxTerminalSessions     *int    `json:"max_terminal_sessions"`
	GPUAllowed              *bool   `json:"gpu_allowed"`
	MaxWorkspaceDurationMin *int    `json:"max_workspace_duration_min"`
	IdleTimeoutMin          *int    `json:"idle_timeout_min"`
}

var quotaKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,62}$`)

func (in *quotaInput) validate(create bool) map[string]string {
	d := map[string]string{}
	if create {
		if in.Key == nil || !quotaKeyRe.MatchString(*in.Key) {
			d["key"] = "must be 2-63 lowercase letters, digits, '-' or '_'"
		}
		for k, v := range map[string]bool{"name": in.Name == nil, "cpu_millis": in.CPUMillis == nil, "memory_bytes": in.MemoryBytes == nil,
			"storage_bytes": in.StorageBytes == nil, "max_workspaces": in.MaxWorkspaces == nil, "max_containers": in.MaxContainers == nil,
			"max_databases": in.MaxDatabases == nil, "max_terminal_sessions": in.MaxTerminalSessions == nil} {
			if v {
				d[k] = "is required"
			}
		}
	}
	if in.Name != nil && (strings.TrimSpace(*in.Name) == "" || len(*in.Name) > 80) {
		d["name"] = "must be 1-80 characters"
	}
	if in.CPUMillis != nil && (*in.CPUMillis < 100 || *in.CPUMillis > 256000) {
		d["cpu_millis"] = "must be between 100 and 256000"
	}
	if in.MemoryBytes != nil && (*in.MemoryBytes < 128<<20 || *in.MemoryBytes > 2<<40) {
		d["memory_bytes"] = "must be between 128 MiB and 2 TiB"
	}
	if in.StorageBytes != nil && (*in.StorageBytes < 256<<20 || *in.StorageBytes > 64<<40) {
		d["storage_bytes"] = "must be between 256 MiB and 64 TiB"
	}
	for k, v := range map[string]*int{"max_workspaces": in.MaxWorkspaces, "max_containers": in.MaxContainers,
		"max_databases": in.MaxDatabases, "max_terminal_sessions": in.MaxTerminalSessions} {
		if v != nil && (*v < 0 || *v > 1000) {
			d[k] = "must be between 0 and 1000"
		}
	}
	for k, v := range map[string]*int{"max_workspace_duration_min": in.MaxWorkspaceDurationMin, "idle_timeout_min": in.IdleTimeoutMin} {
		if v != nil && (*v < 0 || *v > 525600) {
			d[k] = "must be between 0 (no limit) and 525600"
		}
	}
	return d
}

func zeroNil(v *int) *int {
	if v != nil && *v == 0 {
		return nil
	}
	return v
}

// CreateQuota handles POST /quotas.
func (h *Handler) CreateQuota(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).Can("quotas.manage") {
		return httpx.Forbidden("")
	}
	var in quotaInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if d := in.validate(true); len(d) > 0 {
		return httpx.Validation(d)
	}
	desc := ""
	if in.Description != nil {
		desc = *in.Description
	}
	gpu := in.GPUAllowed != nil && *in.GPUAllowed
	var id uuid.UUID
	err := db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO resource_quotas (key, name, description, cpu_millis, memory_bytes, storage_bytes, max_workspaces,
			max_containers, max_databases, max_terminal_sessions, gpu_allowed, max_workspace_duration_min, idle_timeout_min)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`,
			*in.Key, strings.TrimSpace(*in.Name), desc, *in.CPUMillis, *in.MemoryBytes, *in.StorageBytes, *in.MaxWorkspaces,
			*in.MaxContainers, *in.MaxDatabases, *in.MaxTerminalSessions, gpu, zeroNil(in.MaxWorkspaceDurationMin), zeroNil(in.IdleTimeoutMin)).Scan(&id)
		if db.IsUniqueViolation(err, "") {
			return httpx.Validation(map[string]string{"key": "already exists"})
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "quota.create", ResourceType: "quota", ResourceID: id.String(), Metadata: map[string]any{"input": in}})
	})
	if err != nil {
		return err
	}
	q, err := scanQuota(h.Svc.Pool.QueryRow(ctx, selectQuota+" WHERE q.id=$1", id))
	if err != nil {
		return err
	}
	return httpx.Created(w, q)
}

// UpdateQuota handles PATCH /quotas/{id}.
func (h *Handler) UpdateQuota(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).Can("quotas.manage") {
		return httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Quota")
	}
	var in quotaInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if in.Key != nil {
		return httpx.Validation(map[string]string{"key": "cannot be changed"})
	}
	if d := in.validate(false); len(d) > 0 {
		return httpx.Validation(d)
	}
	err = db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE resource_quotas SET name=coalesce($2,name), description=coalesce($3,description),
			cpu_millis=coalesce($4,cpu_millis), memory_bytes=coalesce($5,memory_bytes), storage_bytes=coalesce($6,storage_bytes),
			max_workspaces=coalesce($7,max_workspaces), max_containers=coalesce($8,max_containers), max_databases=coalesce($9,max_databases),
			max_terminal_sessions=coalesce($10,max_terminal_sessions), gpu_allowed=coalesce($11,gpu_allowed),
			max_workspace_duration_min=CASE WHEN $12::int IS NULL THEN max_workspace_duration_min ELSE NULLIF($12,0) END,
			idle_timeout_min=CASE WHEN $13::int IS NULL THEN idle_timeout_min ELSE NULLIF($13,0) END, updated_at=now() WHERE id=$1`,
			id, in.Name, in.Description, in.CPUMillis, in.MemoryBytes, in.StorageBytes, in.MaxWorkspaces, in.MaxContainers,
			in.MaxDatabases, in.MaxTerminalSessions, in.GPUAllowed, in.MaxWorkspaceDurationMin, in.IdleTimeoutMin)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("Quota")
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "quota.update", ResourceType: "quota", ResourceID: id.String(), Metadata: map[string]any{"input": in}})
	})
	if err != nil {
		return err
	}
	q, err := scanQuota(h.Svc.Pool.QueryRow(ctx, selectQuota+" WHERE q.id=$1", id))
	if err != nil {
		return err
	}
	return httpx.OK(w, q)
}

// DeleteQuota handles DELETE /quotas/{id} (not the default package).
func (h *Handler) DeleteQuota(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).Can("quotas.manage") {
		return httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Quota")
	}
	err = db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		var key string
		if err := tx.QueryRow(ctx, `SELECT key FROM resource_quotas WHERE id=$1 FOR UPDATE`, id).Scan(&key); err != nil {
			if db.IsNotFound(err) {
				return httpx.NotFound("Quota")
			}
			return err
		}
		if key == h.Svc.Settings.String(ctx, "quota.default_package") {
			return httpx.Conflict("The default quota package cannot be deleted.")
		}
		var inUse int
		if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM users WHERE quota_id=$1) + (SELECT count(*) FROM projects WHERE quota_id=$1)`, id).Scan(&inUse); err != nil {
			return err
		}
		if inUse > 0 {
			return httpx.Conflict("The quota package is assigned to users or projects. Reassign them first.")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM resource_quotas WHERE id=$1`, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "quota.delete", ResourceType: "quota", ResourceID: id.String(), Metadata: map[string]any{"key": key}})
	})
	if err != nil {
		return err
	}
	return httpx.NoContent(w)
}

// MyUsage handles GET /me/usage.
func (h *Handler) MyUsage(w http.ResponseWriter, r *http.Request) error {
	p := authz.FromContext(r.Context())
	return h.usageFor(w, r, p.UserID)
}

// UserUsage handles GET /users/{id}/usage.
func (h *Handler) UserUsage(w http.ResponseWriter, r *http.Request) error {
	p := authz.FromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || (id != p.UserID && !p.Can("users.view")) {
		return httpx.NotFound("User")
	}
	return h.usageFor(w, r, id)
}

func (h *Handler) usageFor(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	ctx := r.Context()
	q, err := h.Svc.UserQuota(ctx, h.Svc.Pool, userID)
	if err != nil {
		return err
	}
	u, err := h.Svc.UserUsage(ctx, h.Svc.Pool, userID)
	if err != nil {
		return err
	}
	return httpx.OK(w, map[string]any{"quota": q, "usage": u})
}
