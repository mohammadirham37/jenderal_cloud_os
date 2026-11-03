// Package gpu manages the GPU inventory (discovered by the host agent),
// access requests, approvals and allocations. Allocations are exclusive by
// default; shared allocations are only possible when enabled in settings and
// on devices whose policy is "shared" — VRAM is never partitioned by the
// platform. MIG is not assumed.
package gpu

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itk-cloud/itk-cloud/internal/modules/agent"
	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/modules/notifications"
	"github.com/itk-cloud/itk-cloud/internal/modules/resources"
	"github.com/itk-cloud/itk-cloud/internal/modules/settings"
	ws "github.com/itk-cloud/itk-cloud/internal/modules/workspaces"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/config"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// Device is a GPU device.
type Device struct {
	ID                uuid.UUID `json:"id"`
	UUID              string    `json:"uuid"`
	Index             int       `json:"index"`
	Name              string    `json:"name"`
	DriverVersion     *string   `json:"driver_version"`
	CUDAVersion       *string   `json:"cuda_version"`
	MemoryTotalBytes  *int64    `json:"memory_total_bytes"`
	MemoryUsedBytes   *int64    `json:"memory_used_bytes"`
	UtilizationPct    *int      `json:"utilization_pct"`
	TemperatureC      *int      `json:"temperature_c"`
	MIGMode           *string   `json:"mig_mode"`
	AllocationPolicy  string    `json:"allocation_policy"`
	MaxShared         int       `json:"max_shared"`
	Status            string    `json:"status"`
	ActiveAllocations int       `json:"active_allocations"`
	LastSeenAt        time.Time `json:"last_seen_at"`
}

const selectDevice = `SELECT d.id, d.uuid, d.index, d.name, d.driver_version, d.cuda_version, d.memory_total_bytes, d.memory_used_bytes,
	d.utilization_pct, d.temperature_c, d.mig_mode, d.allocation_policy, d.max_shared, d.status,
	(SELECT count(*) FROM gpu_allocations a WHERE a.gpu_device_id=d.id AND a.status='active'), d.last_seen_at FROM gpu_devices d`

func scanDevice(row pgx.Row) (Device, error) {
	var d Device
	err := row.Scan(&d.ID, &d.UUID, &d.Index, &d.Name, &d.DriverVersion, &d.CUDAVersion, &d.MemoryTotalBytes, &d.MemoryUsedBytes,
		&d.UtilizationPct, &d.TemperatureC, &d.MIGMode, &d.AllocationPolicy, &d.MaxShared, &d.Status, &d.ActiveAllocations, &d.LastSeenAt)
	return d, err
}

// Service implements GPU management.
type Service struct {
	Pool      *pgxpool.Pool
	Cfg       *config.Config
	Settings  *settings.Store
	Resources *resources.Service
	Agent     *agent.Agent
}

// Sync refreshes the inventory from the agent (periodic, worker) and returns
// the raw inventory for metrics sampling.
func (s *Service) Sync(ctx context.Context) (*agentproto.GPUInventory, error) {
	var inv agentproto.GPUInventory
	if err := s.Agent.Do(ctx, agentproto.OpGPUInventory, "gpu", "inventory sync", nil, &inv); err != nil {
		return nil, err
	}
	seen := []string{}
	for _, g := range inv.GPUs {
		seen = append(seen, g.UUID)
		var mig *string
		if g.MIGMode != "" {
			mig = &g.MIGMode
		}
		var cuda *string
		if g.CUDAVersion != "" {
			cuda = &g.CUDAVersion
		}
		if _, err := s.Pool.Exec(ctx, `INSERT INTO gpu_devices (uuid, index, name, driver_version, cuda_version, memory_total_bytes, memory_used_bytes,
			utilization_pct, temperature_c, mig_mode, status, last_seen_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'available',now())
			ON CONFLICT (uuid) DO UPDATE SET index=$2, name=$3, driver_version=$4, cuda_version=$5, memory_total_bytes=$6, memory_used_bytes=$7,
			utilization_pct=$8, temperature_c=$9, mig_mode=$10,
			status=CASE WHEN gpu_devices.status='disabled' THEN 'disabled' ELSE 'available' END, last_seen_at=now(), updated_at=now()`,
			g.UUID, g.Index, g.Name, g.DriverVersion, cuda, g.MemoryTotal, g.MemoryUsed, g.Utilization, g.Temperature, mig); err != nil {
			return nil, err
		}
	}
	if inv.Error == "" {
		if _, err := s.Pool.Exec(ctx, `UPDATE gpu_devices SET status='missing', updated_at=now() WHERE NOT (uuid = ANY($1)) AND status='available'`, seen); err != nil {
			return nil, err
		}
	}
	return &inv, nil
}

// Handler serves /gpu endpoints.
type Handler struct{ Svc *Service }

// Devices handles GET /gpu/devices (inventory + live processes when allowed).
func (h *Handler) Devices(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.CanAny("gpu.view", "gpu.manage", "gpu.approve") {
		return httpx.Forbidden("")
	}
	rows, err := h.Svc.Pool.Query(ctx, selectDevice+" ORDER BY d.index")
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return err
		}
		out = append(out, d)
	}
	resp := map[string]any{"devices": out, "gpu_enabled": h.Svc.Cfg.GPUEnabled}
	caps := h.Svc.Agent.Capabilities(ctx)
	resp["nvidia_driver"] = caps.NVIDIA
	resp["nvidia_container_runtime"] = caps.NVIDIARuntime
	if p.CanAny("gpu.manage", "gpu.approve", "monitoring.view_host") {
		var inv agentproto.GPUInventory
		actx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := h.Svc.Agent.Do(actx, agentproto.OpGPUInventory, "gpu", "view gpu processes", nil, &inv); err == nil {
			procs := map[string][]agentproto.GPUProcess{}
			for _, g := range inv.GPUs {
				procs[g.UUID] = g.Processes
			}
			resp["processes"] = procs
			if inv.Error != "" {
				resp["inventory_error"] = inv.Error
			}
		} else {
			resp["inventory_error"] = "GPU information is currently unavailable from the host agent."
		}
	}
	return httpx.OK(w, resp)
}

type deviceInput struct {
	AllocationPolicy *string `json:"allocation_policy"`
	MaxShared        *int    `json:"max_shared"`
	Disabled         *bool   `json:"disabled"`
}

// UpdateDevice handles PATCH /gpu/devices/{id} (gpu.manage).
func (h *Handler) UpdateDevice(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).Can("gpu.manage") {
		return httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("GPU")
	}
	var in deviceInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if in.AllocationPolicy != nil && *in.AllocationPolicy != "exclusive" && *in.AllocationPolicy != "shared" && *in.AllocationPolicy != "disabled" {
		return httpx.Validation(map[string]string{"allocation_policy": "must be exclusive, shared or disabled"})
	}
	if in.MaxShared != nil && (*in.MaxShared < 1 || *in.MaxShared > 16) {
		return httpx.Validation(map[string]string{"max_shared": "must be between 1 and 16"})
	}
	var status *string
	if in.Disabled != nil {
		s := "available"
		if *in.Disabled {
			s = "disabled"
		}
		status = &s
	}
	err = db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE gpu_devices SET allocation_policy=coalesce($2,allocation_policy), max_shared=coalesce($3,max_shared),
			status=coalesce($4,status), updated_at=now() WHERE id=$1`, id, in.AllocationPolicy, in.MaxShared, status)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("GPU")
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "gpu.device_update", ResourceType: "gpu_device", ResourceID: id.String(), Metadata: map[string]any{"input": in}})
	})
	if err != nil {
		return err
	}
	d, err := scanDevice(h.Svc.Pool.QueryRow(ctx, selectDevice+" WHERE d.id=$1", id))
	if err != nil {
		return err
	}
	return httpx.OK(w, d)
}

// Request is a GPU request.
type Request struct {
	ID            uuid.UUID  `json:"id"`
	RequesterID   uuid.UUID  `json:"requester_id"`
	Requester     string     `json:"requester_username"`
	WorkspaceID   uuid.UUID  `json:"workspace_id"`
	WorkspaceName string     `json:"workspace_name"`
	GPUDeviceID   *uuid.UUID `json:"gpu_device_id"`
	GPUName       *string    `json:"gpu_name"`
	Mode          string     `json:"mode"`
	DurationHours int        `json:"duration_hours"`
	Reason        string     `json:"reason"`
	Status        string     `json:"status"`
	ReviewedBy    *string    `json:"reviewed_by_username"`
	ReviewNote    *string    `json:"review_note"`
	ReviewedAt    *time.Time `json:"reviewed_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

const selectRequest = `SELECT r.id, r.requester_id, u.username, r.workspace_id, w.name, r.gpu_device_id, d.name, r.mode, r.duration_hours,
	r.reason, r.status, rv.username, r.review_note, r.reviewed_at, r.created_at FROM gpu_requests r JOIN users u ON u.id=r.requester_id
	JOIN workspaces w ON w.id=r.workspace_id LEFT JOIN gpu_devices d ON d.id=r.gpu_device_id LEFT JOIN users rv ON rv.id=r.reviewed_by`

func scanRequest(row pgx.Row) (Request, error) {
	var q Request
	err := row.Scan(&q.ID, &q.RequesterID, &q.Requester, &q.WorkspaceID, &q.WorkspaceName, &q.GPUDeviceID, &q.GPUName, &q.Mode,
		&q.DurationHours, &q.Reason, &q.Status, &q.ReviewedBy, &q.ReviewNote, &q.ReviewedAt, &q.CreatedAt)
	return q, err
}

// Requests handles GET /gpu/requests (own; all for approvers).
func (h *Handler) Requests(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.CanAny("gpu.view", "gpu.request", "gpu.approve") {
		return httpx.Forbidden("")
	}
	args := []any{}
	where := []string{"true"}
	if !p.Can("gpu.approve") || r.URL.Query().Get("mine") == "true" {
		args = append(args, p.UserID)
		where = append(where, "r.requester_id=$1")
	}
	if s := r.URL.Query().Get("status"); s != "" {
		args = append(args, s)
		where = append(where, "r.status=$"+strconv.Itoa(len(args)))
	}
	rows, err := h.Svc.Pool.Query(ctx, selectRequest+" WHERE "+strings.Join(where, " AND ")+" ORDER BY r.created_at DESC LIMIT 200", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		q, err := scanRequest(rows)
		if err != nil {
			return err
		}
		out = append(out, q)
	}
	return httpx.OK(w, out)
}

type requestInput struct {
	WorkspaceID   uuid.UUID  `json:"workspace_id"`
	GPUDeviceID   *uuid.UUID `json:"gpu_device_id"`
	Mode          string     `json:"mode"`
	DurationHours int        `json:"duration_hours"`
	Reason        string     `json:"reason"`
}

// CreateRequest handles POST /gpu/requests.
func (h *Handler) CreateRequest(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can("gpu.request") {
		return httpx.Forbidden("")
	}
	if !h.Svc.Cfg.GPUEnabled {
		return httpx.Unavailable("GPU support is not enabled on this server.")
	}
	var in requestInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Mode == "" {
		in.Mode = "exclusive"
	}
	d := map[string]string{}
	maxH := h.Svc.Settings.Int(ctx, "gpu.max_duration_hours")
	if in.DurationHours < 1 || in.DurationHours > maxH {
		d["duration_hours"] = fmt.Sprintf("must be between 1 and %d", maxH)
	}
	if len(in.Reason) < 10 || len(in.Reason) > 2000 {
		d["reason"] = "must be 10-2000 characters"
	}
	if in.Mode != "exclusive" && in.Mode != "shared" {
		d["mode"] = "must be exclusive or shared"
	}
	if in.Mode == "shared" && !h.Svc.Settings.Bool(ctx, "gpu.allow_shared") {
		d["mode"] = "shared GPU allocations are disabled"
	}
	if len(d) > 0 {
		return httpx.Validation(d)
	}
	wsp, _, err := ws.Authorize(ctx, h.Svc.Pool, r, in.WorkspaceID, ws.LevelEdit)
	if err != nil {
		return err
	}
	if wsp.Type != "container" {
		return httpx.Validation(map[string]string{"workspace_id": "GPU access is only available for container workspaces"})
	}
	quota, err := h.Svc.Resources.UserQuota(ctx, h.Svc.Pool, wsp.OwnerID)
	if err != nil {
		return err
	}
	if !quota.GPUAllowed {
		return resources.QuotaError("Your quota package does not include GPU access. Ask an administrator for a package that allows GPUs.")
	}
	var id uuid.UUID
	err = db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		var pending bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gpu_requests WHERE workspace_id=$1 AND status='pending')`, wsp.ID).Scan(&pending); err != nil {
			return err
		}
		if pending {
			return httpx.Conflict("There is already a pending GPU request for this workspace.")
		}
		if in.GPUDeviceID != nil {
			var status string
			if err := tx.QueryRow(ctx, `SELECT status FROM gpu_devices WHERE id=$1`, *in.GPUDeviceID).Scan(&status); err != nil || status != "available" {
				return httpx.Validation(map[string]string{"gpu_device_id": "GPU is not available"})
			}
		}
		if err := tx.QueryRow(ctx, `INSERT INTO gpu_requests (requester_id, workspace_id, gpu_device_id, mode, duration_hours, reason)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, p.UserID, wsp.ID, in.GPUDeviceID, in.Mode, in.DurationHours, in.Reason).Scan(&id); err != nil {
			return err
		}
		_ = notifications.NotifyPermission(ctx, tx, "gpu.approve", notifications.Message{Type: notifications.TypeGPU, Severity: "info",
			Title: "New GPU request from " + p.Username, Body: in.Reason, Link: "/gpu?tab=approvals"})
		return audit.Record(ctx, tx, audit.Entry{Action: "gpu.request", ResourceType: "gpu_request", ResourceID: id.String(),
			Metadata: map[string]any{"workspace_id": wsp.ID, "mode": in.Mode, "duration_hours": in.DurationHours}})
	})
	if err != nil {
		return err
	}
	q, err := scanRequest(h.Svc.Pool.QueryRow(ctx, selectRequest+" WHERE r.id=$1", id))
	if err != nil {
		return err
	}
	return httpx.Created(w, q)
}

// CancelRequest handles POST /gpu/requests/{id}/cancel (requester).
func (h *Handler) CancelRequest(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("GPU request")
	}
	tag, err := h.Svc.Pool.Exec(ctx, `UPDATE gpu_requests SET status='cancelled', updated_at=now() WHERE id=$1 AND requester_id=$2 AND status='pending'`, id, p.UserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.NotFound("Pending GPU request")
	}
	_ = audit.Record(ctx, h.Svc.Pool, audit.Entry{Action: "gpu.request_cancel", ResourceType: "gpu_request", ResourceID: id.String()})
	return httpx.NoContent(w)
}

type reviewInput struct {
	GPUDeviceID   *uuid.UUID `json:"gpu_device_id"`
	Note          string     `json:"note"`
	DurationHours *int       `json:"duration_hours"`
}

// Approve handles POST /gpu/requests/{id}/approve.
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can("gpu.approve") {
		return httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("GPU request")
	}
	var in reviewInput
	if r.ContentLength > 0 {
		if err := httpx.DecodeJSON(r, &in); err != nil {
			return err
		}
	}
	var allocID uuid.UUID
	err = db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		var req Request
		req, err := scanRequest(tx.QueryRow(ctx, selectRequest+" WHERE r.id=$1 FOR UPDATE OF r", id))
		if db.IsNotFound(err) {
			return httpx.NotFound("GPU request")
		}
		if err != nil {
			return err
		}
		if req.Status != "pending" {
			return httpx.Conflict("The request has already been " + req.Status + ".")
		}
		if req.RequesterID == p.UserID && !p.Can("gpu.manage") {
			return httpx.Forbidden("You cannot approve your own GPU request.")
		}
		var wsStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM workspaces WHERE id=$1`, req.WorkspaceID).Scan(&wsStatus); err != nil {
			return err
		}
		if wsStatus == ws.StatusDeleted || wsStatus == ws.StatusDeleting {
			return httpx.Conflict("The workspace no longer exists.")
		}
		dev := in.GPUDeviceID
		if dev == nil {
			dev = req.GPUDeviceID
		}
		deviceID, err := pickDevice(ctx, tx, dev, req.Mode)
		if err != nil {
			return err
		}
		hours := req.DurationHours
		if in.DurationHours != nil {
			if *in.DurationHours < 1 || *in.DurationHours > req.DurationHours {
				return httpx.Validation(map[string]string{"duration_hours": "may only shorten the requested duration"})
			}
			hours = *in.DurationHours
		}
		err = tx.QueryRow(ctx, `INSERT INTO gpu_allocations (request_id, gpu_device_id, workspace_id, user_id, mode, ends_at)
			VALUES ($1,$2,$3,$4,$5, now() + make_interval(hours => $6)) RETURNING id`,
			id, deviceID, req.WorkspaceID, req.RequesterID, req.Mode, hours).Scan(&allocID)
		if db.IsUniqueViolation(err, "gpu_allocations_exclusive_key") {
			return httpx.Conflict("The GPU is already exclusively allocated.")
		}
		if err != nil {
			return err
		}
		note := strings.TrimSpace(in.Note)
		if _, err := tx.Exec(ctx, `UPDATE gpu_requests SET status='approved', gpu_device_id=$2, reviewed_by=$3, review_note=NULLIF($4,''), reviewed_at=now(), updated_at=now() WHERE id=$1`,
			id, deviceID, p.UserID, note); err != nil {
			return err
		}
		// Apply to a running workspace by restarting its primary container.
		if wsStatus == ws.StatusRunning {
			if _, err := ws.EnqueueWorkspaceJob(ctx, tx, ws.JobRestart, req.WorkspaceID, ws.JobPayload{Reason: "attach approved GPU"}, ""); err != nil {
				return err
			}
		}
		_ = notifications.Notify(ctx, tx, req.RequesterID, notifications.Message{Type: notifications.TypeGPU, Severity: "success",
			Title: "GPU request approved", Body: fmt.Sprintf("GPU allocated to %s for %d hours. %s", req.WorkspaceName, hours, note),
			Link: "/workspaces/" + req.WorkspaceID.String()})
		return audit.Record(ctx, tx, audit.Entry{Action: "gpu.approve", ResourceType: "gpu_request", ResourceID: id.String(),
			Metadata: map[string]any{"allocation_id": allocID, "gpu_device_id": deviceID, "hours": hours}})
	})
	if err != nil {
		return err
	}
	q, err := scanRequest(h.Svc.Pool.QueryRow(ctx, selectRequest+" WHERE r.id=$1", id))
	if err != nil {
		return err
	}
	return httpx.OK(w, q)
}

// pickDevice locks and validates a device for an allocation mode.
func pickDevice(ctx context.Context, tx pgx.Tx, preferred *uuid.UUID, mode string) (uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT d.id, d.allocation_policy, d.max_shared,
		(SELECT count(*) FROM gpu_allocations a WHERE a.gpu_device_id=d.id AND a.status='active'),
		(SELECT count(*) FROM gpu_allocations a WHERE a.gpu_device_id=d.id AND a.status='active' AND a.mode='exclusive')
		FROM gpu_devices d WHERE d.status='available' AND ($1::uuid IS NULL OR d.id=$1) ORDER BY d.index FOR UPDATE OF d`, preferred)
	if err != nil {
		return uuid.Nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var policy string
		var maxShared, active, exclusive int
		if err := rows.Scan(&id, &policy, &maxShared, &active, &exclusive); err != nil {
			return uuid.Nil, err
		}
		switch {
		case policy == "disabled" || exclusive > 0:
			continue
		case mode == "exclusive" && active == 0:
			return id, nil
		case mode == "shared" && policy == "shared" && active < maxShared:
			return id, nil
		}
	}
	if preferred != nil {
		return uuid.Nil, httpx.Conflict("The selected GPU is not available for this allocation mode.")
	}
	return uuid.Nil, httpx.Conflict("No GPU is currently available for this allocation mode.")
}

// Reject handles POST /gpu/requests/{id}/reject.
func (h *Handler) Reject(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can("gpu.approve") {
		return httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("GPU request")
	}
	var in reviewInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	in.Note = strings.TrimSpace(in.Note)
	if len(in.Note) < 3 {
		return httpx.Validation(map[string]string{"note": "a reason is required"})
	}
	err = db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		var requester uuid.UUID
		err := tx.QueryRow(ctx, `UPDATE gpu_requests SET status='rejected', reviewed_by=$2, review_note=$3, reviewed_at=now(), updated_at=now()
			WHERE id=$1 AND status='pending' RETURNING requester_id`, id, p.UserID, in.Note).Scan(&requester)
		if db.IsNotFound(err) {
			return httpx.Conflict("The request is not pending.")
		}
		if err != nil {
			return err
		}
		_ = notifications.Notify(ctx, tx, requester, notifications.Message{Type: notifications.TypeGPU, Severity: "warning",
			Title: "GPU request rejected", Body: in.Note, Link: "/gpu"})
		return audit.Record(ctx, tx, audit.Entry{Action: "gpu.reject", ResourceType: "gpu_request", ResourceID: id.String(), Reason: in.Note})
	})
	if err != nil {
		return err
	}
	q, err := scanRequest(h.Svc.Pool.QueryRow(ctx, selectRequest+" WHERE r.id=$1", id))
	if err != nil {
		return err
	}
	return httpx.OK(w, q)
}

// Allocation is a GPU allocation.
type Allocation struct {
	ID            uuid.UUID  `json:"id"`
	GPUDeviceID   uuid.UUID  `json:"gpu_device_id"`
	GPUName       string     `json:"gpu_name"`
	GPUIndex      int        `json:"gpu_index"`
	WorkspaceID   uuid.UUID  `json:"workspace_id"`
	WorkspaceName string     `json:"workspace_name"`
	UserID        uuid.UUID  `json:"user_id"`
	Username      string     `json:"username"`
	Mode          string     `json:"mode"`
	Status        string     `json:"status"`
	StartsAt      time.Time  `json:"starts_at"`
	EndsAt        time.Time  `json:"ends_at"`
	ReleasedAt    *time.Time `json:"released_at"`
	ReleaseReason *string    `json:"release_reason"`
}

const selectAlloc = `SELECT a.id, a.gpu_device_id, d.name, d.index, a.workspace_id, w.name, a.user_id, u.username, a.mode, a.status, a.starts_at,
	a.ends_at, a.released_at, a.release_reason FROM gpu_allocations a JOIN gpu_devices d ON d.id=a.gpu_device_id
	JOIN workspaces w ON w.id=a.workspace_id JOIN users u ON u.id=a.user_id`

func scanAlloc(row pgx.Row) (Allocation, error) {
	var a Allocation
	err := row.Scan(&a.ID, &a.GPUDeviceID, &a.GPUName, &a.GPUIndex, &a.WorkspaceID, &a.WorkspaceName, &a.UserID, &a.Username, &a.Mode,
		&a.Status, &a.StartsAt, &a.EndsAt, &a.ReleasedAt, &a.ReleaseReason)
	return a, err
}

// Allocations handles GET /gpu/allocations (?status=active|history).
func (h *Handler) Allocations(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.CanAny("gpu.view", "gpu.approve", "gpu.manage") {
		return httpx.Forbidden("")
	}
	args := []any{}
	where := []string{"true"}
	if !p.CanAny("gpu.approve", "gpu.manage", "monitoring.view_host") || r.URL.Query().Get("mine") == "true" {
		args = append(args, p.UserID)
		where = append(where, "(a.user_id=$1 OR w.owner_id=$1)")
	}
	if r.URL.Query().Get("status") == "active" {
		where = append(where, "a.status='active'")
	}
	rows, err := h.Svc.Pool.Query(ctx, selectAlloc+" WHERE "+strings.Join(where, " AND ")+" ORDER BY a.status='active' DESC, a.starts_at DESC LIMIT 200", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []Allocation{}
	for rows.Next() {
		a, err := scanAlloc(rows)
		if err != nil {
			return err
		}
		out = append(out, a)
	}
	return httpx.OK(w, out)
}

// Release handles POST /gpu/allocations/{id}/release (owner or approver).
func (h *Handler) Release(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Allocation")
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength > 0 {
		if err := httpx.DecodeJSON(r, &in); err != nil {
			return err
		}
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "released by user"
	}
	err = db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		a, err := scanAlloc(tx.QueryRow(ctx, selectAlloc+" WHERE a.id=$1 FOR UPDATE OF a", id))
		if db.IsNotFound(err) {
			return httpx.NotFound("Allocation")
		}
		if err != nil {
			return err
		}
		if a.UserID != p.UserID && !p.CanAny("gpu.approve", "gpu.manage") {
			return httpx.NotFound("Allocation")
		}
		if a.Status != "active" {
			return httpx.Conflict("The allocation is not active.")
		}
		return releaseTx(ctx, tx, a, p.UserID, reason)
	})
	if err != nil {
		return err
	}
	a, err := scanAlloc(h.Svc.Pool.QueryRow(ctx, selectAlloc+" WHERE a.id=$1", id))
	if err != nil {
		return err
	}
	return httpx.OK(w, a)
}

func releaseTx(ctx context.Context, tx pgx.Tx, a Allocation, by uuid.UUID, reason string) error {
	var byPtr *uuid.UUID
	if by != uuid.Nil {
		byPtr = &by
	}
	status := "released"
	if strings.HasPrefix(reason, "expired") {
		status = "expired"
	}
	if _, err := tx.Exec(ctx, `UPDATE gpu_allocations SET status=$2, released_at=now(), released_by=$3, release_reason=$4 WHERE id=$1`, a.ID, status, byPtr, reason); err != nil {
		return err
	}
	var wsStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM workspaces WHERE id=$1`, a.WorkspaceID).Scan(&wsStatus); err != nil {
		return err
	}
	if wsStatus == ws.StatusRunning {
		// Restart to detach the GPU from the container.
		if _, err := ws.EnqueueWorkspaceJob(ctx, tx, ws.JobRestart, a.WorkspaceID, ws.JobPayload{Reason: "detach released GPU"}, ""); err != nil {
			return err
		}
	}
	_ = notifications.Notify(ctx, tx, a.UserID, notifications.Message{Type: notifications.TypeGPU, Severity: "info",
		Title: "GPU allocation ended: " + a.GPUName, Body: reason, Link: "/gpu"})
	return audit.Record(ctx, tx, audit.Entry{Action: "gpu.release", ResourceType: "gpu_allocation", ResourceID: a.ID.String(), Reason: reason,
		Metadata: map[string]any{"workspace_id": a.WorkspaceID, "gpu_device_id": a.GPUDeviceID}})
}

// Reconcile expires allocations past their end time and releases
// allocations left behind by workspaces that stopped long ago.
func (s *Service) Reconcile(ctx context.Context) (int, error) {
	ctx = authz.WithPrincipal(ctx, authz.System())
	after := s.Settings.Int(ctx, "gpu.release_after_stopped_minutes")
	rows, err := s.Pool.Query(ctx, selectAlloc+` WHERE a.status='active' AND (a.ends_at < now() OR
		(w.status IN ('stopped','suspended','failed','deleted','ready') AND w.updated_at < now() - make_interval(mins => $1)))`, after)
	if err != nil {
		return 0, err
	}
	var list []Allocation
	for rows.Next() {
		a, err := scanAlloc(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, a)
	}
	rows.Close()
	for _, a := range list {
		reason := "workspace not running; allocation released automatically"
		if a.EndsAt.Before(time.Now()) {
			reason = "expired: allocation time ended"
		}
		if err := db.WithTx(ctx, s.Pool, func(tx pgx.Tx) error { return releaseTx(ctx, tx, a, uuid.Nil, reason) }); err != nil {
			return 0, err
		}
	}
	return len(list), nil
}
