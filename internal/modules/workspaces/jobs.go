package workspaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/modules/notifications"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
	"github.com/itk-cloud/itk-cloud/internal/platform/jobs"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// jobContext attaches a system principal (acting for the requester) and a
// correlation ID derived from the job ID.
func jobContext(ctx context.Context, j jobs.Job, p JobPayload) context.Context {
	pr := authz.System()
	pr.Username = "system:worker"
	if p.ActorID != nil {
		pr.UserID = *p.ActorID
	}
	ctx = authz.WithPrincipal(ctx, pr)
	return httpx.WithRequestID(ctx, "job-"+j.ID.String())
}

// RegisterJobs registers workspace and container job handlers.
func (s *Service) RegisterJobs(r *jobs.Runner) {
	wrap := func(fn func(context.Context, JobPayload) error) jobs.Handler {
		return func(ctx context.Context, j jobs.Job) error {
			var p JobPayload
			if err := j.Decode(&p); err != nil {
				return jobs.Permanent(err)
			}
			return fn(jobContext(ctx, j, p), p)
		}
	}
	r.Register(JobProvision, wrap(s.jobProvision))
	r.Register(JobStart, wrap(s.jobStart))
	r.Register(JobStop, wrap(s.jobStop))
	r.Register(JobRestart, wrap(s.jobRestart))
	r.Register(JobDelete, wrap(s.jobDelete))
	r.Register(JobContainerCreate, wrap(s.jobContainerCreate))
	r.Register(JobContainerStart, wrap(s.jobContainerStart))
	r.Register(JobContainerStop, wrap(s.jobContainerStop))
	r.Register(JobContainerRestart, wrap(s.jobContainerRestart))
	r.Register(JobContainerRemove, wrap(s.jobContainerRemove))
}

// OnDead marks resources whose lifecycle job permanently failed.
func (s *Service) OnDead(ctx context.Context, j jobs.Job, jobErr error) {
	var p JobPayload
	if err := j.Decode(&p); err != nil {
		return
	}
	msg := userMessage(jobErr)
	switch j.Kind {
	case JobProvision:
		_, _ = s.Pool.Exec(ctx, `UPDATE workspaces SET status='failed', status_reason=$2, updated_at=now() WHERE id=$1 AND status IN ('requested','provisioning')`, p.WorkspaceID, msg)
		s.notifyOwner(ctx, p.WorkspaceID, notifications.Message{Type: notifications.TypeWorkspace, Severity: "critical",
			Title: "Workspace provisioning failed", Body: msg})
	case JobDelete:
		_, _ = s.Pool.Exec(ctx, `UPDATE workspaces SET status='failed', status_reason=$2, updated_at=now() WHERE id=$1 AND status='deleting'`, p.WorkspaceID, "Deletion failed: "+msg)
	case JobStart, JobStop, JobRestart:
		_, _ = s.Pool.Exec(ctx, `UPDATE workspaces SET status_reason=$2, updated_at=now() WHERE id=$1`, p.WorkspaceID, "Last operation failed: "+msg)
		s.notifyOwner(ctx, p.WorkspaceID, notifications.Message{Type: notifications.TypeWorkspace, Severity: "warning",
			Title: "Workspace operation failed", Body: msg})
	default:
		if p.ContainerID != nil {
			_, _ = s.Pool.Exec(ctx, `UPDATE containers SET status=CASE WHEN status IN ('starting','stopping','removing') THEN 'failed' ELSE status END,
				status_reason=$2, updated_at=now() WHERE id=$1`, *p.ContainerID, msg)
		}
	}
	_ = audit.Record(ctx, s.Pool, audit.Entry{Action: j.Kind, Outcome: audit.Failure, ResourceType: "workspace",
		ResourceID: p.WorkspaceID.String(), ActorUsername: "system:worker", Reason: msg})
}

func userMessage(err error) string {
	var ae *agentproto.Error
	if errors.As(err, &ae) {
		return ae.Message
	}
	if errors.Is(err, agentproto.ErrUnavailable) {
		return "The host agent is unavailable."
	}
	msg := err.Error()
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}

func (s *Service) notifyOwner(ctx context.Context, wsID uuid.UUID, m notifications.Message) {
	var owner uuid.UUID
	var name string
	if err := s.Pool.QueryRow(ctx, `SELECT owner_id, name FROM workspaces WHERE id=$1`, wsID).Scan(&owner, &name); err != nil {
		return
	}
	m.Title = m.Title + ": " + name
	if m.Link == "" {
		m.Link = "/workspaces/" + wsID.String()
	}
	_ = notifications.Notify(ctx, s.Pool, owner, m)
}

func (s *Service) setStatus(ctx context.Context, id uuid.UUID, status string, reason *string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE workspaces SET status=$2, status_reason=$3, updated_at=now() WHERE id=$1`, id, status, reason)
	return err
}

func (s *Service) jobProvision(ctx context.Context, p JobPayload) error {
	w, err := Get(ctx, s.Pool, p.WorkspaceID)
	if err != nil {
		if db.IsNotFound(err) {
			return jobs.Permanent(err)
		}
		return err
	}
	switch w.Status {
	case StatusDeleting, StatusDeleted:
		return nil
	case StatusRequested, StatusFailed, StatusProvisioning:
	default:
		return nil // already provisioned
	}
	if err := s.setStatus(ctx, w.ID, StatusProvisioning, nil); err != nil {
		return err
	}
	tpl, err := GetTemplate(ctx, s.Pool, w.TemplateID)
	if err != nil {
		return err
	}
	ownerUID := tpl.Security.RunAsUID
	prov := agentproto.WorkspaceProvision{WorkspaceID: w.ID.String(), StorageBytes: w.StorageBytes, OwnerUID: ownerUID}
	if w.Type == "native" {
		var nu agentproto.NativeUser
		if err := s.Agent.Do(ctx, agentproto.OpNativeUserEnsure, w.ID.String(), "provision native workspace",
			agentproto.NativeUserEnsure{WorkspaceID: w.ID.String(), LinuxUser: LinuxUser(w.ID)}, &nu); err != nil {
			return err
		}
		prov.OwnerUID, prov.Native, prov.LinuxUser = nu.UID, true, LinuxUser(w.ID)
	}
	var usage agentproto.WorkspaceUsage
	if err := s.Agent.Do(ctx, agentproto.OpWorkspaceProvision, w.ID.String(), "provision workspace storage", prov, &usage); err != nil {
		return err
	}
	if w.Type == "container" {
		var ii agentproto.ImageInfo
		if err := s.Agent.Do(ctx, agentproto.OpImageEnsure, tpl.ImageID.String(), "ensure workspace image",
			agentproto.ImageEnsure{Reference: tpl.ImageReference, Pull: true}, &ii); err != nil {
			var ae *agentproto.Error
			if errors.As(err, &ae) && ae.Code == agentproto.ErrCodeNotFound {
				return jobs.Permanent(fmt.Errorf("image %s is not available on the server; an administrator must build or pull it", tpl.ImageReference))
			}
			return err
		}
		_, _ = s.Pool.Exec(ctx, `UPDATE container_images SET pulled_at=coalesce(pulled_at, now()), size_bytes=$2, updated_at=now() WHERE id=$1`, tpl.ImageID, ii.SizeBytes)
		cs, err := ListContainers(ctx, s.Pool, w.ID)
		if err != nil {
			return err
		}
		for _, c := range cs {
			if c.RuntimeID == nil {
				if err := s.createContainer(ctx, &w, &c, nil); err != nil {
					return err
				}
			}
		}
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE workspaces SET status='ready', status_reason=NULL, storage_used_bytes=$2, updated_at=now() WHERE id=$1 AND status='provisioning'`,
		w.ID, usage.UsedBytes); err != nil {
		return err
	}
	_ = audit.Record(ctx, s.Pool, audit.Entry{Action: "workspace.provisioned", ResourceType: "workspace", ResourceID: w.ID.String(), ActorUsername: "system:worker"})
	s.notifyOwner(ctx, w.ID, notifications.Message{Type: notifications.TypeWorkspace, Severity: "success", Title: "Workspace ready",
		Body: "Your workspace has been provisioned and can be started."})
	return nil
}

// gpuDevices returns the GPU UUIDs of active allocations for a workspace.
func (s *Service) gpuDevices(ctx context.Context, wsID uuid.UUID) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT d.uuid FROM gpu_allocations a JOIN gpu_devices d ON d.id=a.gpu_device_id
		WHERE a.workspace_id=$1 AND a.status='active' AND a.ends_at > now() AND d.status='available' ORDER BY d.index`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Service) createContainer(ctx context.Context, w *Workspace, c *Container, gpus []string) error {
	tpl, err := GetTemplate(ctx, s.Pool, c.TemplateID)
	if err != nil {
		return err
	}
	ports := make([]agentproto.PortSpec, 0, len(c.Ports))
	for _, p := range c.Ports {
		ports = append(ports, agentproto.PortSpec{Port: p.Port, Name: p.Name})
	}
	env := map[string]string{}
	for k, v := range tpl.Env {
		env[k] = v
	}
	// Base path for apps served through the authenticated proxy.
	env["ITK_PROXY_BASE"] = "/api/v1/workspaces/" + w.ID.String() + "/proxy/"
	env["ITK_WORKSPACE_NAME"] = w.Slug
	env["ITK_APP_TOKEN"] = AppToken(s.Cfg.SessionSecret, c.ID)
	if s.Cfg.AppProxyOrigin != "" {
		env["ITK_APP_ORIGIN"] = s.Cfg.AppProxyOrigin
	}
	if gpus == nil {
		gpus = []string{}
	}
	req := agentproto.ContainerCreate{
		WorkspaceID: w.ID.String(), ContainerID: c.ID.String(), OwnerID: w.OwnerID.String(), Name: c.Name,
		Image: tpl.ImageReference, CPUMillis: c.CPUMillis, MemoryBytes: c.MemoryBytes, PidsLimit: c.PidsLimit,
		Env: env, Command: tpl.Command, Ports: ports, GPUDevices: gpus,
		RunAsUID: tpl.Security.RunAsUID, RunAsGID: tpl.Security.RunAsGID, ReadOnlyRoot: tpl.Security.ReadOnlyRootfs,
	}
	var st agentproto.ContainerState
	if err := s.Agent.Do(ctx, agentproto.OpContainerCreate, c.ID.String(), "create workspace container", req, &st); err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `UPDATE containers SET runtime_id=$2, status='stopped', status_reason=NULL, gpu_device_ids=$3, updated_at=now() WHERE id=$1`,
		c.ID, st.RuntimeID, gpus)
	c.RuntimeID = &st.RuntimeID
	c.GPUDeviceIDs = gpus
	return err
}

func (s *Service) startContainer(ctx context.Context, w *Workspace, c *Container) error {
	if c.IsPrimary {
		gpus, err := s.gpuDevices(ctx, w.ID)
		if err != nil {
			return err
		}
		cur := slices.Clone(c.GPUDeviceIDs)
		slices.Sort(cur)
		want := slices.Clone(gpus)
		slices.Sort(want)
		if c.RuntimeID != nil && !slices.Equal(cur, want) {
			// GPU devices can only be set at creation: recreate the container
			// (workspace data lives on the persistent volume).
			if err := s.Agent.Do(ctx, agentproto.OpContainerRemove, c.ID.String(), "recreate container to change GPU devices",
				agentproto.ContainerRef{ContainerID: c.ID.String(), RuntimeID: *c.RuntimeID}, nil); err != nil {
				return err
			}
			c.RuntimeID = nil
		}
		if c.RuntimeID == nil {
			if err := s.createContainer(ctx, w, c, gpus); err != nil {
				return err
			}
		}
	} else if c.RuntimeID == nil {
		if err := s.createContainer(ctx, w, c, nil); err != nil {
			return err
		}
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE containers SET status='starting', updated_at=now() WHERE id=$1`, c.ID); err != nil {
		return err
	}
	var st agentproto.ContainerState
	err := s.Agent.Do(ctx, agentproto.OpContainerStart, c.ID.String(), "start container",
		agentproto.ContainerRef{ContainerID: c.ID.String(), RuntimeID: *c.RuntimeID}, &st)
	if err != nil {
		var ae *agentproto.Error
		if errors.As(err, &ae) && ae.Code == agentproto.ErrCodeNotFound {
			// Runtime container vanished: recreate on next attempt.
			_, _ = s.Pool.Exec(ctx, `UPDATE containers SET runtime_id=NULL, status='stopped', updated_at=now() WHERE id=$1`, c.ID)
		} else {
			_, _ = s.Pool.Exec(ctx, `UPDATE containers SET status='stopped', status_reason=$2, updated_at=now() WHERE id=$1`, c.ID, userMessage(err))
		}
		return err
	}
	if st.Status != "running" {
		msg := fmt.Sprintf("container exited immediately (exit code %d)", st.ExitCode)
		if st.OOMKilled {
			msg = "container was killed: out of memory"
		}
		_, _ = s.Pool.Exec(ctx, `UPDATE containers SET status='failed', status_reason=$2, updated_at=now() WHERE id=$1`, c.ID, msg)
		return jobs.Permanent(errors.New(msg))
	}
	ports := c.Ports
	for i := range ports {
		ports[i].HostPort = st.HostPorts[ports[i].Port]
	}
	_, err = s.Pool.Exec(ctx, `UPDATE containers SET status='running', status_reason=NULL, started_at=coalesce($2, now()), ports=$3, updated_at=now() WHERE id=$1`,
		c.ID, st.StartedAt, mustJSON(ports))
	return err
}

func (s *Service) jobStart(ctx context.Context, p JobPayload) error {
	w, err := Get(ctx, s.Pool, p.WorkspaceID)
	if err != nil {
		return jobs.Permanent(err)
	}
	if !CanTransition(w.Status, StatusRunning) && w.Status != StatusRunning {
		return nil
	}
	if w.Type == "container" {
		cs, err := ListContainers(ctx, s.Pool, w.ID)
		if err != nil {
			return err
		}
		for i := range cs {
			if cs[i].Status == "running" {
				continue
			}
			if err := s.startContainer(ctx, &w, &cs[i]); err != nil {
				return err
			}
		}
	} else {
		if err := s.Agent.Do(ctx, agentproto.OpWorkspaceProvision, w.ID.String(), "ensure native workspace storage",
			agentproto.WorkspaceProvision{WorkspaceID: w.ID.String(), StorageBytes: w.StorageBytes, OwnerUID: s.nativeUID(ctx, &w), Native: true, LinuxUser: LinuxUser(w.ID)}, nil); err != nil {
			return err
		}
	}
	_, err = s.Pool.Exec(ctx, `UPDATE workspaces SET status='running', status_reason=NULL, started_at=now(), last_activity_at=now(), updated_at=now() WHERE id=$1`, w.ID)
	if err == nil {
		_ = audit.Record(ctx, s.Pool, audit.Entry{Action: "workspace.started", ResourceType: "workspace", ResourceID: w.ID.String(), ActorUsername: "system:worker", Reason: p.Reason})
	}
	return err
}

func (s *Service) nativeUID(ctx context.Context, w *Workspace) int {
	var nu agentproto.NativeUser
	if err := s.Agent.Do(ctx, agentproto.OpNativeUserEnsure, w.ID.String(), "resolve native user",
		agentproto.NativeUserEnsure{WorkspaceID: w.ID.String(), LinuxUser: LinuxUser(w.ID)}, &nu); err != nil {
		return 1000
	}
	return nu.UID
}

func (s *Service) stopContainer(ctx context.Context, c *Container) error {
	if c.RuntimeID == nil {
		_, err := s.Pool.Exec(ctx, `UPDATE containers SET status='stopped', updated_at=now() WHERE id=$1`, c.ID)
		return err
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE containers SET status='stopping', updated_at=now() WHERE id=$1`, c.ID); err != nil {
		return err
	}
	var st agentproto.ContainerState
	err := s.Agent.Do(ctx, agentproto.OpContainerStop, c.ID.String(), "stop container",
		agentproto.ContainerRef{ContainerID: c.ID.String(), RuntimeID: *c.RuntimeID, TimeoutSec: 15}, &st)
	if err != nil {
		var ae *agentproto.Error
		if !(errors.As(err, &ae) && ae.Code == agentproto.ErrCodeNotFound) {
			return err
		}
	}
	_, err = s.Pool.Exec(ctx, `UPDATE containers SET status='stopped', status_reason=NULL, updated_at=now() WHERE id=$1`, c.ID)
	return err
}

func (s *Service) jobStop(ctx context.Context, p JobPayload) error {
	w, err := Get(ctx, s.Pool, p.WorkspaceID)
	if err != nil {
		return jobs.Permanent(err)
	}
	target := StatusStopped
	if p.Suspend {
		target = StatusSuspended
	}
	if w.Status != StatusRunning && w.Status != target {
		return nil
	}
	cs, err := ListContainers(ctx, s.Pool, w.ID)
	if err != nil {
		return err
	}
	for i := range cs {
		if cs[i].Status == "stopped" || cs[i].Status == "created" {
			continue
		}
		if err := s.stopContainer(ctx, &cs[i]); err != nil {
			return err
		}
	}
	var reason *string
	if p.Reason != "" && p.Reason != "requested by user" {
		reason = &p.Reason
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE workspaces SET status=$2, status_reason=$3, updated_at=now() WHERE id=$1`, w.ID, target, reason); err != nil {
		return err
	}
	_, _ = s.Pool.Exec(ctx, `UPDATE terminal_sessions SET status='closed', ended_at=now(), end_reason='workspace_stopped'
		WHERE workspace_id=$1 AND status IN ('pending','active')`, w.ID)
	_ = audit.Record(ctx, s.Pool, audit.Entry{Action: "workspace.stopped", ResourceType: "workspace", ResourceID: w.ID.String(),
		ActorUsername: "system:worker", Reason: p.Reason, Metadata: map[string]any{"suspended": p.Suspend}})
	if p.Suspend || (reason != nil && *reason != "") {
		title := "Workspace stopped"
		if p.Suspend {
			title = "Workspace suspended"
		}
		s.notifyOwner(ctx, w.ID, notifications.Message{Type: notifications.TypeWorkspace, Severity: "warning", Title: title, Body: p.Reason})
	}
	return nil
}

func (s *Service) jobRestart(ctx context.Context, p JobPayload) error {
	w, err := Get(ctx, s.Pool, p.WorkspaceID)
	if err != nil {
		return jobs.Permanent(err)
	}
	if w.Status != StatusRunning {
		return nil
	}
	cs, err := ListContainers(ctx, s.Pool, w.ID)
	if err != nil {
		return err
	}
	for i := range cs {
		c := &cs[i]
		if c.IsPrimary {
			// Stop + start so GPU device changes are applied.
			if err := s.stopContainer(ctx, c); err != nil {
				return err
			}
			c.Status = "stopped"
			if err := s.startContainer(ctx, &w, c); err != nil {
				return err
			}
			continue
		}
		if c.Status != "running" || c.RuntimeID == nil {
			continue
		}
		var st agentproto.ContainerState
		if err := s.Agent.Do(ctx, agentproto.OpContainerRestart, c.ID.String(), "restart container",
			agentproto.ContainerRef{ContainerID: c.ID.String(), RuntimeID: *c.RuntimeID, TimeoutSec: 15}, &st); err != nil {
			return err
		}
	}
	_, err = s.Pool.Exec(ctx, `UPDATE workspaces SET started_at=now(), last_activity_at=now(), updated_at=now() WHERE id=$1`, w.ID)
	return err
}

func (s *Service) jobDelete(ctx context.Context, p JobPayload) error {
	w, err := Get(ctx, s.Pool, p.WorkspaceID)
	if err != nil {
		return jobs.Permanent(err)
	}
	if w.Status == StatusDeleted {
		return nil
	}
	if w.Status != StatusDeleting {
		return jobs.Permanent(fmt.Errorf("workspace is %s, not deleting", w.Status))
	}
	ref := agentproto.WorkspaceRef{WorkspaceID: w.ID.String()}
	if w.Type == "native" {
		ref.Native, ref.LinuxUser = true, LinuxUser(w.ID)
	}
	if err := s.Agent.Do(ctx, agentproto.OpWorkspaceDestroy, w.ID.String(), "delete workspace", ref, nil); err != nil {
		return err
	}
	err = db.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE containers SET status='removed', removed_at=now(), updated_at=now() WHERE workspace_id=$1 AND removed_at IS NULL`, w.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE gpu_allocations SET status='released', released_at=now(), release_reason='workspace deleted' WHERE workspace_id=$1 AND status='active'`, w.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE gpu_requests SET status='cancelled', updated_at=now() WHERE workspace_id=$1 AND status='pending'`, w.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE terminal_sessions SET status='closed', ended_at=now(), end_reason='workspace_deleted' WHERE workspace_id=$1 AND status IN ('pending','active')`, w.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE workspaces SET status='deleted', deleted_at=now(), storage_used_bytes=NULL, updated_at=now() WHERE id=$1`, w.ID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "workspace.deleted", ResourceType: "workspace", ResourceID: w.ID.String(), ActorUsername: "system:worker", Reason: p.Reason})
	})
	if err != nil {
		return err
	}
	s.notifyOwner(ctx, w.ID, notifications.Message{Type: notifications.TypeWorkspace, Severity: "info", Title: "Workspace deleted", Link: "/workspaces"})
	return nil
}

func (s *Service) loadContainerJob(ctx context.Context, p JobPayload) (*Workspace, *Container, error) {
	if p.ContainerID == nil {
		return nil, nil, jobs.Permanent(errors.New("container_id missing"))
	}
	w, err := Get(ctx, s.Pool, p.WorkspaceID)
	if err != nil {
		return nil, nil, jobs.Permanent(err)
	}
	c, err := ScanContainer(s.Pool.QueryRow(ctx, SelectContainer+" WHERE c.id=$1", *p.ContainerID))
	if err != nil {
		return nil, nil, jobs.Permanent(err)
	}
	return &w, &c, nil
}

func (s *Service) jobContainerCreate(ctx context.Context, p JobPayload) error {
	w, c, err := s.loadContainerJob(ctx, p)
	if err != nil {
		return err
	}
	if c.RuntimeID != nil || c.Status == "removed" {
		return nil
	}
	tpl, err := GetTemplate(ctx, s.Pool, c.TemplateID)
	if err != nil {
		return err
	}
	if err := s.Agent.Do(ctx, agentproto.OpImageEnsure, tpl.ImageID.String(), "ensure container image",
		agentproto.ImageEnsure{Reference: tpl.ImageReference, Pull: true}, nil); err != nil {
		var ae *agentproto.Error
		if errors.As(err, &ae) && ae.Code == agentproto.ErrCodeNotFound {
			return jobs.Permanent(fmt.Errorf("image %s is not available on the server", tpl.ImageReference))
		}
		return err
	}
	if err := s.createContainer(ctx, w, c, nil); err != nil {
		return err
	}
	if w.Status == StatusRunning {
		return s.startContainer(ctx, w, c)
	}
	return nil
}

func (s *Service) jobContainerStart(ctx context.Context, p JobPayload) error {
	w, c, err := s.loadContainerJob(ctx, p)
	if err != nil {
		return err
	}
	if c.Status == "running" || c.Status == "removed" {
		return nil
	}
	if err := s.startContainer(ctx, w, c); err != nil {
		return err
	}
	if w.Status == StatusReady || w.Status == StatusStopped {
		_, err = s.Pool.Exec(ctx, `UPDATE workspaces SET status='running', started_at=now(), last_activity_at=now(), updated_at=now() WHERE id=$1`, w.ID)
	}
	return err
}

func (s *Service) jobContainerStop(ctx context.Context, p JobPayload) error {
	w, c, err := s.loadContainerJob(ctx, p)
	if err != nil {
		return err
	}
	if c.Status == "removed" {
		return nil
	}
	if err := s.stopContainer(ctx, c); err != nil {
		return err
	}
	// Stopping the last running container stops the workspace.
	var running int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM containers WHERE workspace_id=$1 AND removed_at IS NULL AND status IN ('running','starting')`, w.ID).Scan(&running); err != nil {
		return err
	}
	if running == 0 && w.Status == StatusRunning {
		_, err = s.Pool.Exec(ctx, `UPDATE workspaces SET status='stopped', updated_at=now() WHERE id=$1`, w.ID)
	}
	return err
}

func (s *Service) jobContainerRestart(ctx context.Context, p JobPayload) error {
	_, c, err := s.loadContainerJob(ctx, p)
	if err != nil {
		return err
	}
	if c.RuntimeID == nil || c.Status != "running" {
		return nil
	}
	var st agentproto.ContainerState
	if err := s.Agent.Do(ctx, agentproto.OpContainerRestart, c.ID.String(), "restart container",
		agentproto.ContainerRef{ContainerID: c.ID.String(), RuntimeID: *c.RuntimeID, TimeoutSec: 15}, &st); err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `UPDATE containers SET started_at=coalesce($2, now()), updated_at=now() WHERE id=$1`, c.ID, st.StartedAt)
	return err
}

func (s *Service) jobContainerRemove(ctx context.Context, p JobPayload) error {
	_, c, err := s.loadContainerJob(ctx, p)
	if err != nil {
		return err
	}
	if c.Status == "removed" {
		return nil
	}
	if c.RuntimeID != nil {
		if err := s.Agent.Do(ctx, agentproto.OpContainerRemove, c.ID.String(), "remove container",
			agentproto.ContainerRef{ContainerID: c.ID.String(), RuntimeID: *c.RuntimeID}, nil); err != nil {
			return err
		}
	}
	_, err = s.Pool.Exec(ctx, `UPDATE containers SET status='removed', removed_at=now(), updated_at=now() WHERE id=$1`, c.ID)
	return err
}

// RefreshUsage updates storage usage of provisioned workspaces and warns
// owners above 90% of their allocation (at most once per day).
func (s *Service) RefreshUsage(ctx context.Context) error {
	rows, err := s.Pool.Query(ctx, `SELECT id, owner_id, name, storage_bytes FROM workspaces WHERE status IN ('ready','running','stopped','suspended')`)
	if err != nil {
		return err
	}
	type ws struct {
		id, owner uuid.UUID
		name      string
		limit     int64
	}
	var list []ws
	for rows.Next() {
		var w ws
		if err := rows.Scan(&w.id, &w.owner, &w.name, &w.limit); err != nil {
			rows.Close()
			return err
		}
		list = append(list, w)
	}
	rows.Close()
	ctx = authz.WithPrincipal(ctx, authz.System())
	for _, w := range list {
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
		var u agentproto.WorkspaceUsage
		err := s.Agent.Do(cctx, agentproto.OpWorkspaceUsage, w.id.String(), "refresh storage usage", agentproto.WorkspaceRef{WorkspaceID: w.id.String()}, &u)
		cancel()
		if err != nil {
			if errors.Is(err, agentproto.ErrUnavailable) {
				return err
			}
			continue
		}
		_, _ = s.Pool.Exec(ctx, `UPDATE workspaces SET storage_used_bytes=$2 WHERE id=$1`, w.id, u.UsedBytes)
		if w.limit > 0 && float64(u.UsedBytes) >= 0.9*float64(w.limit) {
			var recent bool
			_ = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notifications WHERE user_id=$1 AND type='quota' AND link=$2 AND created_at > now() - interval '24 hours')`,
				w.owner, "/workspaces/"+w.id.String()).Scan(&recent)
			if !recent {
				_ = notifications.Notify(ctx, s.Pool, w.owner, notifications.Message{Type: notifications.TypeQuota, Severity: "warning",
					Title: "Workspace storage almost full: " + w.name,
					Body:  fmt.Sprintf("%.0f%% of the storage quota is in use.", 100*float64(u.UsedBytes)/float64(w.limit)),
					Link:  "/workspaces/" + w.id.String()})
			}
		}
	}
	return nil
}

// EnforceIdleAndDuration stops workspaces that exceeded their idle timeout or
// maximum duration.
func (s *Service) EnforceIdleAndDuration(ctx context.Context) (int, error) {
	if !s.Settings.Bool(ctx, "workspaces.idle_stop_enabled") {
		return 0, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT w.id,
		CASE WHEN w.max_duration_min IS NOT NULL AND w.started_at < now() - make_interval(mins => w.max_duration_min) THEN 'maximum duration reached'
		     ELSE 'idle timeout reached' END
		FROM workspaces w WHERE w.status='running' AND (
		  (w.idle_timeout_min IS NOT NULL AND coalesce(w.last_activity_at, w.started_at) < now() - make_interval(mins => w.idle_timeout_min)
		     AND NOT EXISTS (SELECT 1 FROM terminal_sessions t WHERE t.workspace_id=w.id AND t.status='active' AND t.last_activity_at > now() - make_interval(mins => w.idle_timeout_min)))
		  OR (w.max_duration_min IS NOT NULL AND w.started_at < now() - make_interval(mins => w.max_duration_min)))
		AND NOT EXISTS (SELECT 1 FROM background_jobs j WHERE j.resource_type='workspace' AND j.resource_id=w.id::text AND j.status IN ('queued','running'))`)
	if err != nil {
		return 0, err
	}
	type item struct {
		id     uuid.UUID
		reason string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.reason); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, it)
	}
	rows.Close()
	ctx = authz.WithPrincipal(ctx, authz.System())
	for _, it := range items {
		err := db.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
			_, err := EnqueueWorkspaceJob(ctx, tx, JobStop, it.id, JobPayload{Reason: "Automatically stopped: " + it.reason}, "")
			return err
		})
		if err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

// Touch records user activity on a workspace (terminal, files, proxy).
func Touch(ctx context.Context, q db.Querier, id uuid.UUID) {
	_, _ = q.Exec(ctx, `UPDATE workspaces SET last_activity_at=now() WHERE id=$1 AND (last_activity_at IS NULL OR last_activity_at < now() - interval '30 seconds')`, id)
}
