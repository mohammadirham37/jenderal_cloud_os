package workspaces

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itk-cloud/itk-cloud/internal/modules/agent"
	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/modules/images"
	"github.com/itk-cloud/itk-cloud/internal/modules/resources"
	"github.com/itk-cloud/itk-cloud/internal/modules/settings"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/config"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
	"github.com/itk-cloud/itk-cloud/internal/platform/jobs"
)

// Job kinds handled by the worker.
const (
	JobProvision        = "workspace.provision"
	JobStart            = "workspace.start"
	JobStop             = "workspace.stop"
	JobRestart          = "workspace.restart"
	JobDelete           = "workspace.delete"
	JobContainerCreate  = "container.create"
	JobContainerStart   = "container.start"
	JobContainerStop    = "container.stop"
	JobContainerRestart = "container.restart"
	JobContainerRemove  = "container.remove"
	JobUsageRefresh     = "workspace.usage_refresh"
)

// JobPayload is the payload of workspace and container jobs.
type JobPayload struct {
	WorkspaceID uuid.UUID  `json:"workspace_id"`
	ContainerID *uuid.UUID `json:"container_id,omitempty"`
	Reason      string     `json:"reason"`
	ActorID     *uuid.UUID `json:"actor_id,omitempty"`
	Suspend     bool       `json:"suspend,omitempty"`
}

// Service implements workspace use cases.
type Service struct {
	Pool      *pgxpool.Pool
	Cfg       *config.Config
	Settings  *settings.Store
	Resources *resources.Service
	Agent     *agent.Agent
}

// CreateInput is the create workspace request.
type CreateInput struct {
	Name           string     `json:"name"`
	TemplateID     *uuid.UUID `json:"template_id"`
	TemplateKey    *string    `json:"template_key"`
	ProjectID      *uuid.UUID `json:"project_id"`
	Type           string     `json:"type"`
	CPUMillis      *int       `json:"cpu_millis"`
	MemoryBytes    *int64     `json:"memory_bytes"`
	StorageBytes   *int64     `json:"storage_bytes"`
	IdleTimeoutMin *int       `json:"idle_timeout_min"`
	OwnerID        *uuid.UUID `json:"owner_id"` // administrators may create on behalf of a user
}

var slugInvalid = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify derives a workspace slug from a name.
func Slugify(name string) string {
	s := slugInvalid.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, "-")
	if len(s) > 36 {
		s = strings.Trim(s[:36], "-")
	}
	for len(s) < 3 {
		s += "-ws"
		s = strings.TrimLeft(s, "-")
	}
	return s
}

func actorID(ctx context.Context) *uuid.UUID {
	if p := authz.FromContext(ctx); p != nil && p.UserID != uuid.Nil {
		id := p.UserID
		return &id
	}
	return nil
}

// EnqueueWorkspaceJob schedules a lifecycle job for a workspace.
func EnqueueWorkspaceJob(ctx context.Context, tx pgx.Tx, kind string, wsID uuid.UUID, payload JobPayload, idemKey string) (uuid.UUID, error) {
	payload.WorkspaceID = wsID
	if payload.ActorID == nil {
		payload.ActorID = actorID(ctx)
	}
	return jobs.Enqueue(ctx, tx, kind, payload, jobs.Options{
		ResourceType: "workspace", ResourceID: wsID.String(), CreatedBy: payload.ActorID, IdempotencyKey: idemKey, MaxAttempts: 5,
	})
}

// EnqueueContainerJob schedules a container job.
func EnqueueContainerJob(ctx context.Context, tx pgx.Tx, kind string, wsID, containerID uuid.UUID, reason string) (uuid.UUID, error) {
	cid := containerID
	payload := JobPayload{WorkspaceID: wsID, ContainerID: &cid, Reason: reason, ActorID: actorID(ctx)}
	return jobs.Enqueue(ctx, tx, kind, payload, jobs.Options{
		ResourceType: "container", ResourceID: containerID.String(), CreatedBy: payload.ActorID, MaxAttempts: 4,
	})
}

func pendingJob(ctx context.Context, q db.Querier, resourceType string, id uuid.UUID) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM background_jobs WHERE resource_type=$1 AND resource_id=$2 AND status IN ('queued','running'))`,
		resourceType, id.String()).Scan(&exists)
	return exists, err
}

// Create validates and records a new workspace and schedules provisioning.
func (s *Service) Create(ctx context.Context, in CreateInput) (Workspace, error) {
	p := authz.FromContext(ctx)
	if !p.Can("workspaces.create") {
		return Workspace{}, httpx.Forbidden("")
	}
	d := map[string]string{}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 {
		d["name"] = "is required (max 80 characters)"
	}
	if in.Type == "" {
		in.Type = s.Cfg.WorkspaceDefaultDriver
		if in.Type == "docker" {
			in.Type = "container"
		}
	}
	if in.Type != "container" && in.Type != "native" {
		d["type"] = "must be container or native"
	}
	if in.TemplateID == nil && (in.TemplateKey == nil || *in.TemplateKey == "") {
		d["template_id"] = "is required"
	}
	if in.IdleTimeoutMin != nil && (*in.IdleTimeoutMin < 5 || *in.IdleTimeoutMin > 10080) {
		d["idle_timeout_min"] = "must be between 5 and 10080 minutes"
	}
	if len(d) > 0 {
		return Workspace{}, httpx.Validation(d)
	}
	if in.Type == "native" {
		if !s.Cfg.NativeWorkspaces {
			return Workspace{}, httpx.Validation(map[string]string{"type": "Linux-native workspaces are disabled on this server"})
		}
		if !p.Can("workspaces.native") {
			return Workspace{}, httpx.Forbidden("Linux-native workspaces require explicit approval (workspaces.native permission).")
		}
	}
	ownerID := p.UserID
	if in.OwnerID != nil && *in.OwnerID != p.UserID {
		if !p.Can("workspaces.manage_all") {
			return Workspace{}, httpx.Forbidden("Only administrators can create workspaces for other users.")
		}
		ownerID = *in.OwnerID
	}

	var wsID uuid.UUID
	err := db.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		// Serialise quota checks per owner.
		var ownerStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR UPDATE`, ownerID).Scan(&ownerStatus); err != nil {
			if db.IsNotFound(err) {
				return httpx.Validation(map[string]string{"owner_id": "user not found"})
			}
			return err
		}
		if ownerStatus != "active" {
			return httpx.Validation(map[string]string{"owner_id": "user is suspended"})
		}
		var tpl Template
		var err error
		if in.TemplateID != nil {
			tpl, err = GetTemplate(ctx, tx, *in.TemplateID)
		} else {
			tpl, err = scanTemplate(tx.QueryRow(ctx, selectTemplate+" WHERE t.key=$1", *in.TemplateKey))
		}
		if db.IsNotFound(err) {
			return httpx.Validation(map[string]string{"template_id": "template not found"})
		}
		if err != nil {
			return err
		}
		if !tpl.IsActive || !tpl.ImageAllowed {
			return httpx.Validation(map[string]string{"template_id": "template is not available"})
		}
		if in.Type == "container" && !tpl.ImagePresent && s.Agent != nil {
			// Re-check live: the image may have been built since the last sync.
			// When the agent cannot answer, let the provisioning job decide.
			if present, err := images.CheckPresent(ctx, s.Pool, s.Agent, tpl.ImageID, tpl.ImageReference); err == nil && !present {
				return httpx.Validation(map[string]string{"template_id": fmt.Sprintf(
					"The image for %q (%s) is not installed on this server yet. Ask an administrator to build it (installer/build-workspace-images.sh %s) or choose another template.",
					tpl.Name, tpl.ImageReference, strings.TrimSuffix(strings.TrimPrefix(tpl.ImageReference, "jenderal/"), ":1.0"))})
			}
		}
		if in.ProjectID != nil {
			var role *string
			var status string
			err := tx.QueryRow(ctx, `SELECT p.status, (SELECT role FROM project_members WHERE project_id=p.id AND user_id=$2)
				FROM projects p WHERE p.id=$1`, *in.ProjectID, ownerID).Scan(&status, &role)
			if db.IsNotFound(err) {
				return httpx.Validation(map[string]string{"project_id": "project not found"})
			}
			if err != nil {
				return err
			}
			if status != "active" {
				return httpx.Validation(map[string]string{"project_id": "project is archived"})
			}
			if role == nil || *role == "viewer" {
				return httpx.Validation(map[string]string{"project_id": "the owner must be a project member (not viewer)"})
			}
		}
		cpu, mem, storage := tpl.RecCPUMillis, tpl.RecMemoryBytes, tpl.DefaultStorageBytes
		if in.CPUMillis != nil {
			cpu = *in.CPUMillis
		}
		if in.MemoryBytes != nil {
			mem = *in.MemoryBytes
		}
		if in.StorageBytes != nil {
			storage = *in.StorageBytes
		}
		quota, err := s.Resources.UserQuota(ctx, tx, ownerID)
		if err != nil {
			return err
		}
		d := map[string]string{}
		if cpu < tpl.MinCPUMillis || cpu > quota.CPUMillis {
			d["cpu_millis"] = fmt.Sprintf("must be between %d (template minimum) and %d (your quota)", tpl.MinCPUMillis, quota.CPUMillis)
		}
		if mem < tpl.MinMemoryBytes || mem > quota.MemoryBytes {
			d["memory_bytes"] = fmt.Sprintf("must be between %s (template minimum) and %s (your quota)",
				resources.HumanBytes(tpl.MinMemoryBytes), resources.HumanBytes(quota.MemoryBytes))
		}
		if storage < 256<<20 {
			d["storage_bytes"] = "must be at least 256 MiB"
		}
		if len(d) > 0 {
			return httpx.Validation(d)
		}
		if tpl.GPUMode == "required" && !quota.GPUAllowed {
			return resources.QuotaError("This template requires GPU access, which your quota package does not allow.")
		}
		usage, err := s.Resources.UserUsage(ctx, tx, ownerID)
		if err != nil {
			return err
		}
		hard := s.Settings.Int(ctx, "workspaces.max_per_user_hard_limit")
		if usage.Workspaces >= quota.MaxWorkspaces || usage.Workspaces >= hard {
			return resources.QuotaError(fmt.Sprintf("Workspace limit reached (%d of %d).", usage.Workspaces, min(quota.MaxWorkspaces, hard)))
		}
		if in.Type == "container" && usage.Containers >= quota.MaxContainers {
			return resources.QuotaError(fmt.Sprintf("Container limit reached (%d of %d).", usage.Containers, quota.MaxContainers))
		}
		if usage.AllocatedStorage+storage > quota.StorageBytes {
			return resources.QuotaError(fmt.Sprintf("Storage quota exceeded: %s allocated of %s; requested %s.",
				resources.HumanBytes(usage.AllocatedStorage), resources.HumanBytes(quota.StorageBytes), resources.HumanBytes(storage)))
		}
		if in.ProjectID != nil {
			pq, err := s.Resources.ProjectQuota(ctx, tx, *in.ProjectID)
			if err != nil {
				return err
			}
			if pq != nil {
				pu, err := s.Resources.ProjectUsage(ctx, tx, *in.ProjectID)
				if err != nil {
					return err
				}
				if pu.Workspaces >= pq.MaxWorkspaces || pu.AllocatedStorage+storage > pq.StorageBytes {
					return resources.QuotaError("The project's workspace or storage quota would be exceeded.")
				}
			}
		}
		slug := Slugify(in.Name)
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM workspaces WHERE owner_id=$1 AND (slug=$2 OR slug LIKE $2 || '-%') AND deleted_at IS NULL`,
			ownerID, slug).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			slug = fmt.Sprintf("%s-%d", slug, n+1)
		}
		idle := in.IdleTimeoutMin
		if idle == nil {
			idle = quota.IdleTimeoutMin
		}
		err = tx.QueryRow(ctx, `INSERT INTO workspaces (name, slug, owner_id, project_id, template_id, type, status, cpu_millis, memory_bytes,
			storage_bytes, idle_timeout_min, max_duration_min) VALUES ($1,$2,$3,$4,$5,$6,'requested',$7,$8,$9,$10,$11) RETURNING id`,
			in.Name, slug, ownerID, in.ProjectID, tpl.ID, in.Type, cpu, mem, storage, idle, quota.MaxWorkspaceDurationMin).Scan(&wsID)
		if db.IsUniqueViolation(err, "") {
			return httpx.Conflict("A workspace with a similar name already exists; choose another name.")
		}
		if err != nil {
			return err
		}
		if in.Type == "container" {
			ports := tpl.Ports
			if ports == nil {
				ports = []Port{}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO containers (workspace_id, owner_id, template_id, image_id, name, is_primary, cpu_millis, memory_bytes, pids_limit, ports)
				VALUES ($1,$2,$3,$4,'main',true,$5,$6,512,$7)`, wsID, ownerID, tpl.ID, tpl.ImageID, cpu, mem, mustJSON(ports)); err != nil {
				return err
			}
		}
		if _, err := EnqueueWorkspaceJob(ctx, tx, JobProvision, wsID, JobPayload{Reason: "workspace created"}, JobProvision+":"+wsID.String()); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "workspace.create", ResourceType: "workspace", ResourceID: wsID.String(),
			Metadata: map[string]any{"name": in.Name, "template": tpl.Key, "type": in.Type, "owner_id": ownerID, "project_id": in.ProjectID,
				"cpu_millis": cpu, "memory_bytes": mem, "storage_bytes": storage}})
	})
	if err != nil {
		return Workspace{}, err
	}
	return Get(ctx, s.Pool, wsID)
}

// Transition requests a lifecycle action on a workspace. The status change
// itself is applied by the worker job; here the request is validated and
// enqueued.
func (s *Service) Transition(ctx context.Context, w *Workspace, action, reason string) error {
	return db.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM workspaces WHERE id=$1 FOR UPDATE`, w.ID).Scan(&status); err != nil {
			return err
		}
		busy, err := pendingJob(ctx, tx, "workspace", w.ID)
		if err != nil {
			return err
		}
		if busy && action != "delete" {
			return httpx.Conflict("Another operation on this workspace is in progress. Please wait for it to finish.")
		}
		var kind string
		payload := JobPayload{Reason: reason}
		switch action {
		case "start":
			if !CanTransition(status, StatusRunning) {
				return httpx.Conflict(fmt.Sprintf("A workspace in status %q cannot be started.", status))
			}
			if status == StatusSuspended && !authz.FromContext(ctx).Can("workspaces.manage_all") {
				return httpx.Forbidden("This workspace was suspended by an administrator.")
			}
			cpu, mem, err := s.startDemand(ctx, tx, w)
			if err != nil {
				return err
			}
			if err := s.Resources.CheckRun(ctx, tx, w.OwnerID, w.ProjectID, cpu, mem); err != nil {
				return err
			}
			if err := s.Resources.CheckHostCapacity(ctx, tx, cpu, mem); err != nil {
				return err
			}
			kind = JobStart
		case "stop":
			if !CanTransition(status, StatusStopped) {
				return httpx.Conflict(fmt.Sprintf("A workspace in status %q cannot be stopped.", status))
			}
			kind = JobStop
		case "suspend":
			if !CanTransition(status, StatusSuspended) {
				return httpx.Conflict(fmt.Sprintf("A workspace in status %q cannot be suspended.", status))
			}
			kind, payload.Suspend = JobStop, true
		case "restart":
			if status != StatusRunning {
				return httpx.Conflict("Only running workspaces can be restarted.")
			}
			kind = JobRestart
		case "delete":
			if !CanTransition(status, StatusDeleting) {
				if status == StatusDeleting {
					return nil
				}
				return httpx.Conflict(fmt.Sprintf("A workspace in status %q cannot be deleted. Stop it first.", status))
			}
			if busy {
				return httpx.Conflict("Another operation on this workspace is in progress. Please wait for it to finish.")
			}
			if _, err := tx.Exec(ctx, `UPDATE workspaces SET status='deleting', status_reason=$2, updated_at=now() WHERE id=$1`, w.ID, reason); err != nil {
				return err
			}
			kind = JobDelete
		case "retry":
			if status != StatusFailed {
				return httpx.Conflict("Only failed workspaces can be retried.")
			}
			kind = JobProvision
		default:
			return httpx.BadRequest("unknown action")
		}
		idem := ""
		if kind == JobDelete {
			idem = JobDelete + ":" + w.ID.String()
		}
		if kind == JobProvision {
			idem = JobProvision + ":" + w.ID.String() + ":retry:" + uuid.NewString()
		}
		if _, err := EnqueueWorkspaceJob(ctx, tx, kind, w.ID, payload, idem); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "workspace." + action, ResourceType: "workspace", ResourceID: w.ID.String(),
			Reason: reason, Metadata: map[string]any{"from_status": status, "owner_id": w.OwnerID}})
	})
}

// startDemand returns the CPU/memory that starting the workspace adds.
func (s *Service) startDemand(ctx context.Context, q db.Querier, w *Workspace) (int, int64, error) {
	if w.Type == "native" {
		return w.CPUMillis, w.MemoryBytes, nil
	}
	var cpu int
	var mem int64
	err := q.QueryRow(ctx, `SELECT coalesce(sum(cpu_millis),0), coalesce(sum(memory_bytes),0) FROM containers
		WHERE workspace_id=$1 AND removed_at IS NULL AND status NOT IN ('running','starting')`, w.ID).Scan(&cpu, &mem)
	return cpu, mem, err
}

// SuspendAllForUser schedules suspension of all running workspaces of a
// user (used when an account is suspended).
func SuspendAllForUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	rows, err := tx.Query(ctx, `SELECT id FROM workspaces WHERE owner_id=$1 AND status='running'`, userID)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if _, err := EnqueueWorkspaceJob(ctx, tx, JobStop, id, JobPayload{Reason: "owner account suspended", Suspend: true}, ""); err != nil {
			return err
		}
	}
	// Close the user's terminal sessions.
	_, err = tx.Exec(ctx, `UPDATE terminal_sessions SET status='closed', ended_at=now(), end_reason='account_suspended'
		WHERE user_id=$1 AND status IN ('pending','active')`, userID)
	return err
}
