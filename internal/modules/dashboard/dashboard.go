// Package dashboard aggregates the data shown on the user dashboard.
package dashboard

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itk-cloud/itk-cloud/internal/modules/resources"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/config"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
)

// Handler serves GET /dashboard and GET /config.
type Handler struct {
	Pool         *pgxpool.Pool
	Cfg          *config.Config
	Resources    *resources.Service
	Version      string
	PlatformName func() string
}

type activity struct {
	ID           int64     `json:"id"`
	OccurredAt   time.Time `json:"occurred_at"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	Outcome      string    `json:"outcome"`
}

// Summary handles GET /dashboard.
func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	quota, err := h.Resources.UserQuota(ctx, h.Pool, p.UserID)
	if err != nil {
		return err
	}
	usage, err := h.Resources.UserUsage(ctx, h.Pool, p.UserID)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	rows, err := h.Pool.Query(ctx, `SELECT status, count(*) FROM workspaces WHERE owner_id=$1 AND status <> 'deleted' GROUP BY status`, p.UserID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			rows.Close()
			return err
		}
		counts[s] = n
	}
	rows.Close()
	acts := []activity{}
	rows, err = h.Pool.Query(ctx, `SELECT id, occurred_at, action, resource_type, resource_id, outcome FROM audit_logs WHERE actor_id=$1
		ORDER BY occurred_at DESC LIMIT 10`, p.UserID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var a activity
		if err := rows.Scan(&a.ID, &a.OccurredAt, &a.Action, &a.ResourceType, &a.ResourceID, &a.Outcome); err != nil {
			rows.Close()
			return err
		}
		acts = append(acts, a)
	}
	rows.Close()
	var pendingGPU, activeGPU, projects int
	_ = h.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM gpu_requests WHERE requester_id=$1 AND status='pending'),
		(SELECT count(*) FROM gpu_allocations WHERE user_id=$1 AND status='active'),
		(SELECT count(*) FROM project_members WHERE user_id=$1)`, p.UserID).Scan(&pendingGPU, &activeGPU, &projects)
	resp := map[string]any{
		"quota": quota, "usage": usage, "workspaces_by_status": counts, "recent_activity": acts,
		"gpu": map[string]int{"pending_requests": pendingGPU, "active_allocations": activeGPU}, "projects": projects,
	}
	if p.Can("monitoring.view_host") {
		var users, runningWS, runningContainers, pendingApprovals, firingAlerts int
		_ = h.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM users WHERE status='active'),
			(SELECT count(*) FROM workspaces WHERE status='running'),
			(SELECT count(*) FROM containers WHERE status='running' AND removed_at IS NULL),
			(SELECT count(*) FROM gpu_requests WHERE status='pending'),
			(SELECT count(*) FROM alerts WHERE status='firing')`).Scan(&users, &runningWS, &runningContainers, &pendingApprovals, &firingAlerts)
		resp["platform"] = map[string]int{"active_users": users, "running_workspaces": runningWS, "running_containers": runningContainers,
			"pending_gpu_requests": pendingApprovals, "firing_alerts": firingAlerts}
	}
	return httpx.OK(w, resp)
}

// PublicConfig handles GET /config (unauthenticated): non-sensitive UI
// configuration.
func (h *Handler) PublicConfig(w http.ResponseWriter, r *http.Request) error {
	name := "ITK Cloud"
	if h.PlatformName != nil {
		name = h.PlatformName()
	}
	return httpx.OK(w, map[string]any{
		"platform_name":        name,
		"version":              h.Version,
		"app_proxy_origin":     h.Cfg.AppProxyOrigin,
		"upload_max_bytes":     h.Cfg.UploadMaxBytes,
		"editor_max_bytes":     h.Cfg.EditorMaxBytes,
		"gpu_enabled":          h.Cfg.GPUEnabled,
		"native_workspaces":    h.Cfg.NativeWorkspaces,
		"project_databases":    h.Cfg.ProjectDatabasesEnabled(),
		"email_enabled":        h.Cfg.SMTPEnabled(),
		"terminal_ttl_minutes": int(h.Cfg.TerminalSessionTTL.Minutes()),
	})
}
