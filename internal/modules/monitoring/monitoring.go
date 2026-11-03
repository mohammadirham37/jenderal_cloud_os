// Package monitoring samples host, container and GPU metrics through the
// host agent, stores history, evaluates alert rules and serves monitoring
// endpoints. Metrics that are not available are reported as unavailable,
// never as invented numbers.
package monitoring

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/itk-cloud/itk-cloud/internal/modules/agent"
	"github.com/itk-cloud/itk-cloud/internal/modules/notifications"
	"github.com/itk-cloud/itk-cloud/internal/modules/settings"
	ws "github.com/itk-cloud/itk-cloud/internal/modules/workspaces"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/config"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
	"github.com/itk-cloud/itk-cloud/internal/platform/jobs"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// WorkerHeartbeatKey is refreshed by the worker; its absence means the
// worker is down.
const WorkerHeartbeatKey = "itk:worker:heartbeat"

// Service implements monitoring.
type Service struct {
	Pool     *pgxpool.Pool
	Redis    *redis.Client
	Cfg      *config.Config
	Settings *settings.Store
	Agent    *agent.Agent
}

// ---------------------------------------------------------------------------
// Sampling (worker)
// ---------------------------------------------------------------------------

// SampleHost collects and stores a host sample.
func (s *Service) SampleHost(ctx context.Context) (*agentproto.HostMetrics, error) {
	var m agentproto.HostMetrics
	if err := s.Agent.Do(ctx, agentproto.OpHostMetrics, "host", "metrics sample", nil, &m); err != nil {
		return nil, err
	}
	var diskUsed, diskTotal int64
	if d := rootDisk(m.Disks, s.Cfg.WorkspaceRoot); d != nil {
		diskUsed, diskTotal = int64(d.Used), int64(d.Total)
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO resource_usage_samples (scope, ref_id, cpu_pct, memory_bytes, memory_total_bytes, disk_used_bytes,
		disk_total_bytes, net_rx_bytes_per_sec, net_tx_bytes_per_sec, load1, collected_at) VALUES ('host','',$1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		m.CPUPercent, int64(m.MemoryUsed), int64(m.MemoryTotal), diskUsed, diskTotal, m.NetRxBps, m.NetTxBps, m.Load1, m.CollectedAt)
	return &m, err
}

// rootDisk picks the filesystem that holds the workspace root (longest
// matching mount point).
func rootDisk(disks []agentproto.DiskUsage, path string) *agentproto.DiskUsage {
	var best *agentproto.DiskUsage
	for i := range disks {
		mp := disks[i].Mountpoint
		if strings.HasPrefix(path, mp) && (best == nil || len(mp) > len(best.Mountpoint)) {
			best = &disks[i]
		}
	}
	return best
}

// SampleContainers stores stats of running containers and per-workspace
// aggregates.
func (s *Service) SampleContainers(ctx context.Context) error {
	rows, err := s.Pool.Query(ctx, `SELECT id, workspace_id, runtime_id FROM containers WHERE status='running' AND runtime_id IS NOT NULL AND removed_at IS NULL`)
	if err != nil {
		return err
	}
	type item struct {
		id, ws uuid.UUID
		rid    string
	}
	byRuntime := map[string]item{}
	ids := []string{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.ws, &it.rid); err != nil {
			rows.Close()
			return err
		}
		byRuntime[it.rid] = it
		ids = append(ids, it.rid)
	}
	rows.Close()
	if len(ids) == 0 {
		return nil
	}
	var stats []agentproto.ContainerStats
	if err := s.Agent.Do(ctx, agentproto.OpContainerStats, "containers", "metrics sample", agentproto.ContainerStatsReq{RuntimeIDs: ids}, &stats); err != nil {
		return err
	}
	now := time.Now().UTC()
	type agg struct {
		cpu float64
		mem int64
	}
	perWS := map[uuid.UUID]*agg{}
	for _, st := range stats {
		it, ok := byRuntime[st.RuntimeID]
		if !ok {
			continue
		}
		if _, err := s.Pool.Exec(ctx, `INSERT INTO resource_usage_samples (scope, ref_id, cpu_pct, memory_bytes, memory_total_bytes, collected_at)
			VALUES ('container',$1,$2,$3,$4,$5)`, it.id.String(), st.CPUPercent, st.MemoryBytes, st.MemoryLimit, now); err != nil {
			return err
		}
		a := perWS[it.ws]
		if a == nil {
			a = &agg{}
			perWS[it.ws] = a
		}
		a.cpu += st.CPUPercent
		a.mem += st.MemoryBytes
	}
	for wsID, a := range perWS {
		if _, err := s.Pool.Exec(ctx, `INSERT INTO resource_usage_samples (scope, ref_id, cpu_pct, memory_bytes, collected_at) VALUES ('workspace',$1,$2,$3,$4)`,
			wsID.String(), a.cpu, a.mem, now); err != nil {
			return err
		}
	}
	// Reconcile containers that stopped outside the platform (crash, OOM).
	running := map[string]bool{}
	for _, st := range stats {
		running[st.RuntimeID] = true
	}
	for rid, it := range byRuntime {
		if running[rid] {
			continue
		}
		var state agentproto.ContainerState
		if err := s.Agent.Do(ctx, agentproto.OpContainerInspect, it.id.String(), "reconcile container state",
			agentproto.ContainerRef{ContainerID: it.id.String(), RuntimeID: rid}, &state); err != nil {
			continue
		}
		if state.Status != "running" {
			reason := fmt.Sprintf("container exited (code %d)", state.ExitCode)
			if state.OOMKilled {
				reason = "container was killed: out of memory"
			}
			if state.Status == "missing" {
				reason = "container no longer exists on the host"
			}
			_, _ = s.Pool.Exec(ctx, `UPDATE containers SET status='stopped', status_reason=$2, updated_at=now() WHERE id=$1 AND status='running'`, it.id, reason)
			_, _ = s.Pool.Exec(ctx, `UPDATE workspaces SET status='stopped', status_reason=$2, updated_at=now() WHERE id=$1 AND status='running'
				AND NOT EXISTS (SELECT 1 FROM containers c WHERE c.workspace_id=$1 AND c.status='running' AND c.removed_at IS NULL)`, it.ws, reason)
		}
	}
	return nil
}

// SampleGPUs stores GPU samples from an inventory.
func (s *Service) SampleGPUs(ctx context.Context, inv *agentproto.GPUInventory) error {
	if inv == nil {
		return nil
	}
	now := time.Now().UTC()
	for _, g := range inv.GPUs {
		var util, temp *float64
		if g.Utilization != nil {
			v := float64(*g.Utilization)
			util = &v
		}
		if g.Temperature != nil {
			v := float64(*g.Temperature)
			temp = &v
		}
		if _, err := s.Pool.Exec(ctx, `INSERT INTO resource_usage_samples (scope, ref_id, gpu_util_pct, gpu_memory_bytes, memory_total_bytes, gpu_temperature_c, collected_at)
			VALUES ('gpu',$1,$2,$3,$4,$5,$6)`, g.UUID, util, g.MemoryUsed, g.MemoryTotal, temp, now); err != nil {
			return err
		}
	}
	return nil
}

// Prune deletes samples older than the retention period.
func (s *Service) Prune(ctx context.Context) error {
	days := s.Settings.Int(ctx, "monitoring.retention_days")
	_, err := s.Pool.Exec(ctx, `DELETE FROM resource_usage_samples WHERE collected_at < now() - make_interval(days => $1)`, days)
	return err
}

// ---------------------------------------------------------------------------
// Alerts
// ---------------------------------------------------------------------------

// Alert is an alert record.
type Alert struct {
	ID         uuid.UUID  `json:"id"`
	Rule       string     `json:"rule"`
	Subject    string     `json:"subject"`
	Severity   string     `json:"severity"`
	Status     string     `json:"status"`
	Message    string     `json:"message"`
	Value      *float64   `json:"value"`
	StartedAt  time.Time  `json:"started_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
	AckBy      *string    `json:"acknowledged_by_username"`
	AckAt      *time.Time `json:"acknowledged_at"`
}

// condition is an evaluated alert rule.
type condition struct {
	rule, subject, severity, message string
	value                            *float64
	firing                           bool
}

func fptr(v float64) *float64 { return &v }

// EvaluateAlerts evaluates every rule and fires/resolves alerts.
func (s *Service) EvaluateAlerts(ctx context.Context, host *agentproto.HostMetrics, hostErr error, inv *agentproto.GPUInventory) error {
	var conds []condition
	diskPct := float64(s.Settings.Int(ctx, "alerts.disk_pct"))
	memPct := float64(s.Settings.Int(ctx, "alerts.memory_pct"))
	cpuPct := float64(s.Settings.Int(ctx, "alerts.cpu_pct"))
	cpuMin := s.Settings.Int(ctx, "alerts.cpu_sustain_minutes")

	// Agent offline.
	agentDown := hostErr != nil && errors.Is(hostErr, agentproto.ErrUnavailable)
	conds = append(conds, condition{rule: "agent_offline", severity: "critical", firing: agentDown,
		message: "The host agent is not responding. Workspace operations are unavailable."})

	if host != nil {
		for _, d := range host.Disks {
			if d.Total == 0 {
				continue
			}
			pct := 100 * float64(d.Used) / float64(d.Total)
			conds = append(conds, condition{rule: "disk_full", subject: d.Mountpoint, severity: "critical", value: fptr(pct),
				firing: pct >= diskPct, message: fmt.Sprintf("Disk %s is %.1f%% full.", d.Mountpoint, pct)})
		}
		if host.MemoryTotal > 0 {
			pct := 100 * float64(host.MemoryUsed) / float64(host.MemoryTotal)
			conds = append(conds, condition{rule: "memory_critical", severity: "critical", value: fptr(pct),
				firing: pct >= memPct, message: fmt.Sprintf("Host memory usage is %.1f%%.", pct)})
		}
		var avg *float64
		var n int
		_ = s.Pool.QueryRow(ctx, `SELECT avg(cpu_pct), count(*) FROM resource_usage_samples WHERE scope='host' AND collected_at > now() - make_interval(mins => $1)`, cpuMin).Scan(&avg, &n)
		if avg != nil && n >= 3 {
			conds = append(conds, condition{rule: "cpu_sustained_high", severity: "warning", value: avg,
				firing: *avg >= cpuPct, message: fmt.Sprintf("Average CPU usage over %d minutes is %.1f%%.", cpuMin, *avg)})
		}
		conds = append(conds, condition{rule: "docker_unavailable", severity: "critical", firing: !host.Docker.Available,
			message: "The container runtime (Docker) is not available."})
	}
	if inv != nil {
		tempMax := s.Settings.Int(ctx, "alerts.gpu_temp_c")
		utilMax := s.Settings.Int(ctx, "alerts.gpu_util_pct")
		for _, g := range inv.GPUs {
			if g.Temperature != nil {
				conds = append(conds, condition{rule: "gpu_temperature", subject: g.UUID, severity: "critical", value: fptr(float64(*g.Temperature)),
					firing: *g.Temperature >= tempMax, message: fmt.Sprintf("GPU %d (%s) temperature is %d°C.", g.Index, g.Name, *g.Temperature)})
			}
			if g.Utilization != nil {
				// Sustained over-utilisation: average of the last 10 minutes.
				var avg *float64
				_ = s.Pool.QueryRow(ctx, `SELECT avg(gpu_util_pct) FROM resource_usage_samples WHERE scope='gpu' AND ref_id=$1 AND collected_at > now() - interval '10 minutes'`, g.UUID).Scan(&avg)
				if avg != nil {
					conds = append(conds, condition{rule: "gpu_overutilized", subject: g.UUID, severity: "warning", value: avg,
						firing: *avg >= float64(utilMax), message: fmt.Sprintf("GPU %d (%s) utilisation averaged %.0f%% over 10 minutes.", g.Index, g.Name, *avg)})
				}
			}
		}
	}
	// Queue backlog and failed jobs.
	if st, err := jobs.QueueStats(ctx, s.Pool); err == nil {
		backlog := s.Settings.Int(ctx, "alerts.queue_backlog")
		conds = append(conds, condition{rule: "queue_backlog", severity: "warning", value: fptr(float64(st.Queued)),
			firing: st.Queued >= backlog, message: fmt.Sprintf("%d background jobs are waiting in the queue.", st.Queued)})
		var recentDead int
		_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM background_jobs WHERE status='dead' AND updated_at > now() - interval '1 hour'`).Scan(&recentDead)
		conds = append(conds, condition{rule: "jobs_failed", severity: "warning", value: fptr(float64(recentDead)),
			firing: recentDead > 0, message: fmt.Sprintf("%d background jobs failed permanently in the last hour.", recentDead)})
	}
	// Backup failures / missing backups.
	if s.Settings.Bool(ctx, "backup.schedule_enabled") {
		var lastStatus *string
		var lastOK *time.Time
		_ = s.Pool.QueryRow(ctx, `SELECT (SELECT status FROM backup_jobs WHERE trigger='scheduled' ORDER BY created_at DESC LIMIT 1),
			(SELECT max(finished_at) FROM backup_jobs WHERE status='succeeded')`).Scan(&lastStatus, &lastOK)
		failed := lastStatus != nil && *lastStatus == "failed"
		conds = append(conds, condition{rule: "backup_failed", severity: "critical", firing: failed, message: "The last scheduled backup failed."})
		var installedAt time.Time
		_ = s.Pool.QueryRow(ctx, `SELECT min(created_at) FROM users`).Scan(&installedAt)
		stale := (lastOK == nil && !installedAt.IsZero() && time.Since(installedAt) > 26*time.Hour) || (lastOK != nil && time.Since(*lastOK) > 26*time.Hour)
		conds = append(conds, condition{rule: "backup_missing", severity: "warning", firing: stale, message: "No successful backup in the last 26 hours."})
	}
	// Worker heartbeat (evaluated by the API side too).
	if s.Redis != nil {
		exists, err := s.Redis.Exists(ctx, WorkerHeartbeatKey).Result()
		if err == nil {
			conds = append(conds, condition{rule: "service_down", subject: "worker", severity: "critical", firing: exists == 0,
				message: "The background worker is not running."})
		}
	}
	for _, c := range conds {
		if err := s.apply(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) apply(ctx context.Context, c condition) error {
	if c.firing {
		tag, err := s.Pool.Exec(ctx, `INSERT INTO alerts (rule, subject, severity, status, message, value) VALUES ($1,$2,$3,'firing',$4,$5)
			ON CONFLICT (rule, subject) WHERE status='firing' DO UPDATE SET message=EXCLUDED.message, value=EXCLUDED.value`,
			c.rule, c.subject, c.severity, c.message, c.value)
		if err != nil {
			return err
		}
		// Notify administrators only when a new alert starts.
		var started time.Time
		if err := s.Pool.QueryRow(ctx, `SELECT started_at FROM alerts WHERE rule=$1 AND subject=$2 AND status='firing'`, c.rule, c.subject).Scan(&started); err == nil &&
			time.Since(started) < 5*time.Second && tag.RowsAffected() == 1 {
			sev := "warning"
			if c.severity == "critical" {
				sev = "critical"
			}
			_ = notifications.NotifyPermission(ctx, s.Pool, "monitoring.view_host", notifications.Message{Type: notifications.TypeAlert, Severity: sev,
				Title: "Alert: " + strings.ReplaceAll(c.rule, "_", " "), Body: c.message, Link: "/monitoring?tab=alerts"})
		}
		return nil
	}
	_, err := s.Pool.Exec(ctx, `UPDATE alerts SET status='resolved', resolved_at=now() WHERE rule=$1 AND subject=$2 AND status='firing'`, c.rule, c.subject)
	return err
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

// Handler serves monitoring endpoints.
type Handler struct{ Svc *Service }

// Host handles GET /monitoring/host: live metrics with fallback to the last
// stored sample (flagged stale).
func (h *Handler) Host(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).Can("monitoring.view_host") {
		return httpx.Forbidden("")
	}
	actx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var m agentproto.HostMetrics
	err := h.Svc.Agent.Do(actx, agentproto.OpHostMetrics, "host", "view host metrics", nil, &m)
	node, _ := h.Svc.Agent.GetNode(ctx)
	if err == nil {
		return httpx.OK(w, map[string]any{"available": true, "live": true, "metrics": m, "node": node})
	}
	var last struct {
		CPU, Load             *float64
		Mem, MemTotal, DU, DT *int64
		Rx, Tx                *float64
		At                    time.Time
	}
	qerr := h.Svc.Pool.QueryRow(ctx, `SELECT cpu_pct, load1, memory_bytes, memory_total_bytes, disk_used_bytes, disk_total_bytes,
		net_rx_bytes_per_sec, net_tx_bytes_per_sec, collected_at FROM resource_usage_samples WHERE scope='host' ORDER BY collected_at DESC LIMIT 1`).
		Scan(&last.CPU, &last.Load, &last.Mem, &last.MemTotal, &last.DU, &last.DT, &last.Rx, &last.Tx, &last.At)
	if qerr != nil {
		return httpx.OK(w, map[string]any{"available": false, "live": false, "error": "Host metrics are unavailable: the agent is not responding and no samples exist.", "node": node})
	}
	return httpx.OK(w, map[string]any{"available": true, "live": false, "error": "The agent is not responding; showing the last stored sample.",
		"last_sample": map[string]any{"cpu_percent": last.CPU, "load1": last.Load, "memory_used": last.Mem, "memory_total": last.MemTotal,
			"disk_used": last.DU, "disk_total": last.DT, "net_rx_bytes_per_sec": last.Rx, "net_tx_bytes_per_sec": last.Tx, "collected_at": last.At}, "node": node})
}

// Point is a history data point.
type Point struct {
	T        time.Time `json:"t"`
	CPU      *float64  `json:"cpu_pct"`
	Mem      *float64  `json:"memory_bytes"`
	MemTotal *float64  `json:"memory_total_bytes"`
	DiskUsed *float64  `json:"disk_used_bytes"`
	Rx       *float64  `json:"net_rx_bytes_per_sec"`
	Tx       *float64  `json:"net_tx_bytes_per_sec"`
	Load     *float64  `json:"load1"`
	GPUUtil  *float64  `json:"gpu_util_pct"`
	GPUMem   *float64  `json:"gpu_memory_bytes"`
	GPUTemp  *float64  `json:"gpu_temperature_c"`
}

var ranges = map[string]struct {
	window time.Duration
	bucket int // seconds
}{
	"1h": {time.Hour, 60}, "6h": {6 * time.Hour, 300}, "24h": {24 * time.Hour, 900}, "7d": {7 * 24 * time.Hour, 3600}, "14d": {14 * 24 * time.Hour, 7200},
}

// History handles GET /monitoring/history?scope=&ref=&range=.
func (h *Handler) History(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	q := r.URL.Query()
	scope := q.Get("scope")
	if scope == "" {
		scope = "host"
	}
	ref := q.Get("ref")
	rg, ok := ranges[q.Get("range")]
	if q.Get("range") == "" {
		rg, ok = ranges["1h"], true
	}
	if !ok {
		return httpx.Validation(map[string]string{"range": "must be 1h, 6h, 24h, 7d or 14d"})
	}
	switch scope {
	case "host", "gpu":
		if !p.CanAny("monitoring.view_host", "gpu.view") || (scope == "host" && !p.Can("monitoring.view_host")) {
			return httpx.Forbidden("")
		}
		if scope == "host" {
			ref = ""
		}
	case "workspace":
		id, err := uuid.Parse(ref)
		if err != nil {
			return httpx.Validation(map[string]string{"ref": "workspace id required"})
		}
		if _, _, err := ws.Authorize(ctx, h.Svc.Pool, r, id, ws.LevelOperate); err != nil {
			if !p.Can("monitoring.view_host") {
				return err
			}
		}
	case "container":
		id, err := uuid.Parse(ref)
		if err != nil {
			return httpx.Validation(map[string]string{"ref": "container id required"})
		}
		var wsID uuid.UUID
		if err := h.Svc.Pool.QueryRow(ctx, `SELECT workspace_id FROM containers WHERE id=$1`, id).Scan(&wsID); err != nil {
			return httpx.NotFound("Container")
		}
		if _, _, err := ws.Authorize(ctx, h.Svc.Pool, r, wsID, ws.LevelOperate); err != nil && !p.Can("monitoring.view_host") {
			return err
		}
	default:
		return httpx.Validation(map[string]string{"scope": "must be host, workspace, container or gpu"})
	}
	rows, err := h.Svc.Pool.Query(ctx, `SELECT to_timestamp(floor(extract(epoch FROM collected_at) / $3) * $3) AS t,
		avg(cpu_pct), avg(memory_bytes), max(memory_total_bytes), avg(disk_used_bytes), avg(net_rx_bytes_per_sec), avg(net_tx_bytes_per_sec),
		avg(load1), avg(gpu_util_pct), avg(gpu_memory_bytes), max(gpu_temperature_c)
		FROM resource_usage_samples WHERE scope=$1 AND ref_id=$2 AND collected_at > now() - $4::interval GROUP BY t ORDER BY t`,
		scope, ref, rg.bucket, strconv.Itoa(int(rg.window.Seconds()))+" seconds")
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []Point{}
	for rows.Next() {
		var pt Point
		if err := rows.Scan(&pt.T, &pt.CPU, &pt.Mem, &pt.MemTotal, &pt.DiskUsed, &pt.Rx, &pt.Tx, &pt.Load, &pt.GPUUtil, &pt.GPUMem, &pt.GPUTemp); err != nil {
			return err
		}
		out = append(out, pt)
	}
	return httpx.OK(w, map[string]any{"scope": scope, "ref": ref, "bucket_seconds": rg.bucket, "points": out})
}

// Workspace handles GET /monitoring/workspaces/{id}.
func (h *Handler) Workspace(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Workspace")
	}
	wsp, _, err := ws.Authorize(ctx, h.Svc.Pool, r, id, ws.LevelOperate)
	if err != nil {
		return err
	}
	cs, err := ws.ListContainers(ctx, h.Svc.Pool, id)
	if err != nil {
		return err
	}
	ids := []string{}
	for _, c := range cs {
		if c.Status == "running" && c.RuntimeID != nil {
			ids = append(ids, *c.RuntimeID)
		}
	}
	resp := map[string]any{"workspace_id": id, "storage_bytes": wsp.StorageBytes, "storage_used_bytes": wsp.StorageUsedBytes,
		"cpu_limit_millis": wsp.CPUMillis, "memory_limit_bytes": wsp.MemoryBytes, "containers": []any{}}
	if len(ids) > 0 {
		var stats []agentproto.ContainerStats
		actx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := h.Svc.Agent.Do(actx, agentproto.OpContainerStats, id.String(), "view workspace metrics", agentproto.ContainerStatsReq{RuntimeIDs: ids}, &stats); err != nil {
			resp["error"] = "Live metrics are unavailable."
		} else {
			byRID := map[string]agentproto.ContainerStats{}
			for _, s := range stats {
				byRID[s.RuntimeID] = s
			}
			list := []map[string]any{}
			for _, c := range cs {
				if c.RuntimeID == nil {
					continue
				}
				if s, ok := byRID[*c.RuntimeID]; ok {
					list = append(list, map[string]any{"container_id": c.ID, "name": c.Name, "cpu_percent": s.CPUPercent, "memory_bytes": s.MemoryBytes,
						"memory_limit": s.MemoryLimit, "pids": s.Pids, "net_rx_bytes": s.NetRxBytes, "net_tx_bytes": s.NetTxBytes})
				}
			}
			resp["containers"] = list
		}
	}
	return httpx.OK(w, resp)
}

// GPU handles GET /monitoring/gpu.
func (h *Handler) GPU(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).CanAny("monitoring.view_host", "gpu.view") {
		return httpx.Forbidden("")
	}
	var inv agentproto.GPUInventory
	actx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := h.Svc.Agent.Do(actx, agentproto.OpGPUInventory, "gpu", "view gpu metrics", nil, &inv); err != nil {
		return httpx.OK(w, map[string]any{"available": false, "error": "GPU metrics are unavailable from the host agent.", "gpus": []any{}})
	}
	if !authz.FromContext(ctx).Can("monitoring.view_host") {
		for i := range inv.GPUs {
			inv.GPUs[i].Processes = nil // process details are admin-only
		}
	}
	return httpx.OK(w, inv)
}

// Alerts handles GET /monitoring/alerts.
func (h *Handler) Alerts(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).Can("monitoring.view_host") {
		return httpx.Forbidden("")
	}
	where := "true"
	if r.URL.Query().Get("status") == "firing" {
		where = "a.status='firing'"
	}
	rows, err := h.Svc.Pool.Query(ctx, `SELECT a.id, a.rule, a.subject, a.severity, a.status, a.message, a.value, a.started_at, a.resolved_at,
		u.username, a.acknowledged_at FROM alerts a LEFT JOIN users u ON u.id=a.acknowledged_by WHERE `+where+` ORDER BY a.status='firing' DESC, a.started_at DESC LIMIT 200`)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		var a Alert
		if err := rows.Scan(&a.ID, &a.Rule, &a.Subject, &a.Severity, &a.Status, &a.Message, &a.Value, &a.StartedAt, &a.ResolvedAt, &a.AckBy, &a.AckAt); err != nil {
			return err
		}
		out = append(out, a)
	}
	return httpx.OK(w, out)
}

// AckAlert handles POST /monitoring/alerts/{id}/ack.
func (h *Handler) AckAlert(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can("monitoring.view_host") {
		return httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Alert")
	}
	tag, err := h.Svc.Pool.Exec(ctx, `UPDATE alerts SET acknowledged_by=$2, acknowledged_at=now() WHERE id=$1 AND acknowledged_at IS NULL`, id, p.UserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.NotFound("Unacknowledged alert")
	}
	return httpx.NoContent(w)
}

// ServiceStatus is the health of a platform component.
type ServiceStatus struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // up | down | degraded | unknown | not_configured
	Detail  string `json:"detail,omitempty"`
	Latency int64  `json:"latency_ms,omitempty"`
}

// Services handles GET /monitoring/services.
func (h *Handler) Services(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).CanAny("monitoring.view_host", "host.operations.view") {
		return httpx.Forbidden("")
	}
	out := []ServiceStatus{{Name: "api", Status: "up"}}
	start := time.Now()
	if err := h.Svc.Pool.Ping(ctx); err != nil {
		out = append(out, ServiceStatus{Name: "postgresql", Status: "down", Detail: "ping failed"})
	} else {
		out = append(out, ServiceStatus{Name: "postgresql", Status: "up", Latency: time.Since(start).Milliseconds()})
	}
	start = time.Now()
	if err := h.Svc.Redis.Ping(ctx).Err(); err != nil {
		out = append(out, ServiceStatus{Name: "redis", Status: "down", Detail: "ping failed"})
	} else {
		out = append(out, ServiceStatus{Name: "redis", Status: "up", Latency: time.Since(start).Milliseconds()})
	}
	if n, err := h.Svc.Redis.Exists(ctx, WorkerHeartbeatKey).Result(); err != nil {
		out = append(out, ServiceStatus{Name: "worker", Status: "unknown"})
	} else if n == 0 {
		out = append(out, ServiceStatus{Name: "worker", Status: "down", Detail: "no heartbeat in the last 90 seconds"})
	} else {
		out = append(out, ServiceStatus{Name: "worker", Status: "up"})
	}
	if node, err := h.Svc.Agent.GetNode(ctx); err != nil {
		out = append(out, ServiceStatus{Name: "agent", Status: "unknown", Detail: "never reported"})
	} else {
		st := ServiceStatus{Name: "agent", Status: "up"}
		switch node.Status {
		case "offline":
			st.Status, st.Detail = "down", deref(node.LastError)
		case "degraded":
			st.Status, st.Detail = "degraded", deref(node.LastError)
		}
		out = append(out, st)
	}
	if h.Svc.Cfg.WebHealthURL != "" {
		hc := &http.Client{Timeout: 3 * time.Second}
		start = time.Now()
		resp, err := hc.Get(h.Svc.Cfg.WebHealthURL)
		if err != nil || resp.StatusCode >= 500 {
			out = append(out, ServiceStatus{Name: "web", Status: "down"})
		} else {
			out = append(out, ServiceStatus{Name: "web", Status: "up", Latency: time.Since(start).Milliseconds()})
		}
		if resp != nil {
			resp.Body.Close()
		}
	} else {
		out = append(out, ServiceStatus{Name: "web", Status: "not_configured", Detail: "WEB_HEALTH_URL not set"})
	}
	stats, _ := jobs.QueueStats(ctx, h.Svc.Pool)
	var lastBackup struct {
		Status *string    `json:"status"`
		At     *time.Time `json:"finished_at"`
		Kind   *string    `json:"kind"`
	}
	_ = h.Svc.Pool.QueryRow(ctx, `SELECT status, coalesce(finished_at, created_at), kind FROM backup_jobs ORDER BY created_at DESC LIMIT 1`).
		Scan(&lastBackup.Status, &lastBackup.At, &lastBackup.Kind)
	var counts map[string]int = map[string]int{}
	rows, err := h.Svc.Pool.Query(ctx, `SELECT status, count(*) FROM containers WHERE removed_at IS NULL GROUP BY status`)
	if err == nil {
		for rows.Next() {
			var s string
			var n int
			if rows.Scan(&s, &n) == nil {
				counts[s] = n
			}
		}
		rows.Close()
	}
	return httpx.OK(w, map[string]any{"services": out, "queue": stats, "last_backup": lastBackup, "containers_by_status": counts,
		"prometheus_url": h.Svc.Cfg.PrometheusURL, "grafana_url": h.Svc.Cfg.GrafanaURL})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// FailedJobs handles GET /monitoring/jobs?status=dead.
func (h *Handler) FailedJobs(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).Can("monitoring.view_host") {
		return httpx.Forbidden("")
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "dead"
	}
	if status != "dead" && status != "queued" && status != "running" {
		return httpx.Validation(map[string]string{"status": "must be dead, queued or running"})
	}
	rows, err := h.Svc.Pool.Query(ctx, `SELECT id, kind, status, attempts, max_attempts, last_error, resource_type, resource_id, created_at, updated_at
		FROM background_jobs WHERE status=$1 ORDER BY updated_at DESC LIMIT 100`, status)
	if err != nil {
		return err
	}
	defer rows.Close()
	type job struct {
		ID           uuid.UUID `json:"id"`
		Kind         string    `json:"kind"`
		Status       string    `json:"status"`
		Attempts     int       `json:"attempts"`
		MaxAttempts  int       `json:"max_attempts"`
		LastError    *string   `json:"last_error"`
		ResourceType *string   `json:"resource_type"`
		ResourceID   *string   `json:"resource_id"`
		CreatedAt    time.Time `json:"created_at"`
		UpdatedAt    time.Time `json:"updated_at"`
	}
	out := []job{}
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.ID, &j.Kind, &j.Status, &j.Attempts, &j.MaxAttempts, &j.LastError, &j.ResourceType, &j.ResourceID, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return err
		}
		out = append(out, j)
	}
	return httpx.OK(w, out)
}

// RetryJob handles POST /monitoring/jobs/{id}/retry (dead jobs only).
func (h *Handler) RetryJob(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).CanAny("settings.manage", "workspaces.manage_all") {
		return httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Job")
	}
	tag, err := h.Svc.Pool.Exec(ctx, `UPDATE background_jobs SET status='queued', attempts=0, run_at=now(), updated_at=now(), finished_at=NULL WHERE id=$1 AND status='dead'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.NotFound("Failed job")
	}
	return httpx.NoContent(w)
}

var _ = db.IsNotFound
