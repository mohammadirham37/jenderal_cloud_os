// Package workspaces implements workspace templates, the workspace
// lifecycle state machine, access policy, members and the background jobs
// that provision, start, stop and delete workspaces through the host agent.
package workspaces

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/itk-cloud/itk-cloud/internal/platform/db"
)

// Workspace statuses (spec §10.2).
const (
	StatusRequested    = "requested"
	StatusProvisioning = "provisioning"
	StatusReady        = "ready"
	StatusRunning      = "running"
	StatusStopped      = "stopped"
	StatusSuspended    = "suspended"
	StatusFailed       = "failed"
	StatusDeleting     = "deleting"
	StatusDeleted      = "deleted"
)

// transitions is the allowed workspace state machine. Beyond the baseline
// diagram, failed→provisioning (retry) and suspended→deleting (cleanup of
// suspended accounts) are permitted; see docs/workspace.md.
var transitions = map[string][]string{
	StatusRequested:    {StatusProvisioning, StatusFailed},
	StatusProvisioning: {StatusReady, StatusFailed},
	StatusReady:        {StatusRunning, StatusDeleting},
	StatusRunning:      {StatusStopped, StatusSuspended},
	StatusStopped:      {StatusRunning, StatusDeleting},
	StatusSuspended:    {StatusRunning, StatusDeleting},
	StatusFailed:       {StatusDeleting, StatusProvisioning},
	StatusDeleting:     {StatusDeleted},
}

// CanTransition reports whether from→to is allowed.
func CanTransition(from, to string) bool {
	for _, t := range transitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// Port describes a template port.
type Port struct {
	Port     int    `json:"port"`
	Name     string `json:"name"`
	Proxy    bool   `json:"proxy,omitempty"`
	HostPort int    `json:"host_port,omitempty"` // loopback port assigned at start (server-side only)
}

// Security is the template security profile.
type Security struct {
	RunAsUID        int      `json:"run_as_uid"`
	RunAsGID        int      `json:"run_as_gid"`
	NoNewPrivileges bool     `json:"no_new_privileges"`
	ReadOnlyRootfs  bool     `json:"read_only_rootfs"`
	CapAdd          []string `json:"cap_add"`
}

// Template is a workspace template.
type Template struct {
	ID                  uuid.UUID         `json:"id"`
	Key                 string            `json:"key"`
	Name                string            `json:"name"`
	Description         string            `json:"description"`
	Category            string            `json:"category"`
	ImageID             uuid.UUID         `json:"image_id"`
	ImageReference      string            `json:"image_reference"`
	ImageAllowed        bool              `json:"image_allowed"`
	ImagePresent        bool              `json:"image_present"`
	RuntimeVersions     map[string]string `json:"runtime_versions"`
	MinCPUMillis        int               `json:"min_cpu_millis"`
	MinMemoryBytes      int64             `json:"min_memory_bytes"`
	RecCPUMillis        int               `json:"rec_cpu_millis"`
	RecMemoryBytes      int64             `json:"rec_memory_bytes"`
	DefaultStorageBytes int64             `json:"default_storage_bytes"`
	Ports               []Port            `json:"ports"`
	GPUMode             string            `json:"gpu_mode"`
	Security            Security          `json:"security"`
	Env                 map[string]string `json:"env"`
	Command             []string          `json:"command"`
	IsActive            bool              `json:"is_active"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
}

const selectTemplate = `SELECT t.id, t.key, t.name, t.description, t.category, t.image_id, i.reference, i.is_allowed, i.pulled_at IS NOT NULL,
	t.runtime_versions, t.min_cpu_millis, t.min_memory_bytes, t.rec_cpu_millis, t.rec_memory_bytes, t.default_storage_bytes,
	t.ports, t.gpu_mode, t.security, t.env, t.command, t.is_active, t.created_at, t.updated_at
	FROM workspace_templates t JOIN container_images i ON i.id=t.image_id`

func scanTemplate(row pgx.Row) (Template, error) {
	var t Template
	var runtime, ports, sec, env, cmd []byte
	err := row.Scan(&t.ID, &t.Key, &t.Name, &t.Description, &t.Category, &t.ImageID, &t.ImageReference, &t.ImageAllowed, &t.ImagePresent,
		&runtime, &t.MinCPUMillis, &t.MinMemoryBytes, &t.RecCPUMillis, &t.RecMemoryBytes, &t.DefaultStorageBytes,
		&ports, &t.GPUMode, &sec, &env, &cmd, &t.IsActive, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return t, err
	}
	for _, x := range []struct {
		b []byte
		v any
	}{{runtime, &t.RuntimeVersions}, {ports, &t.Ports}, {sec, &t.Security}, {env, &t.Env}, {cmd, &t.Command}} {
		if err := json.Unmarshal(x.b, x.v); err != nil {
			return t, fmt.Errorf("decode template %s: %w", t.Key, err)
		}
	}
	if t.Ports == nil {
		t.Ports = []Port{}
	}
	if t.Command == nil {
		t.Command = []string{}
	}
	if t.Env == nil {
		t.Env = map[string]string{}
	}
	if t.Security.RunAsUID < 1000 {
		t.Security.RunAsUID = 1000
	}
	if t.Security.RunAsGID < 1000 {
		t.Security.RunAsGID = t.Security.RunAsUID
	}
	return t, nil
}

// GetTemplate loads a template by ID.
func GetTemplate(ctx context.Context, q db.Querier, id uuid.UUID) (Template, error) {
	return scanTemplate(q.QueryRow(ctx, selectTemplate+" WHERE t.id=$1", id))
}

// Workspace is the API representation.
type Workspace struct {
	ID               uuid.UUID  `json:"id"`
	Name             string     `json:"name"`
	Slug             string     `json:"slug"`
	OwnerID          uuid.UUID  `json:"owner_id"`
	OwnerUsername    string     `json:"owner_username"`
	ProjectID        *uuid.UUID `json:"project_id"`
	ProjectName      *string    `json:"project_name"`
	TemplateID       uuid.UUID  `json:"template_id"`
	TemplateKey      string     `json:"template_key"`
	TemplateName     string     `json:"template_name"`
	Type             string     `json:"type"`
	Status           string     `json:"status"`
	StatusReason     *string    `json:"status_reason"`
	CPUMillis        int        `json:"cpu_millis"`
	MemoryBytes      int64      `json:"memory_bytes"`
	StorageBytes     int64      `json:"storage_bytes"`
	PidsLimit        int        `json:"pids_limit"`
	IdleTimeoutMin   *int       `json:"idle_timeout_min"`
	MaxDurationMin   *int       `json:"max_duration_min"`
	StorageUsedBytes *int64     `json:"storage_used_bytes"`
	LastActivityAt   *time.Time `json:"last_activity_at"`
	StartedAt        *time.Time `json:"started_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	// Access is the caller's effective access level (filled by handlers).
	Access string `json:"access,omitempty"`
	// PendingJob is set while a lifecycle job is queued or running.
	PendingJob *string `json:"pending_job"`
}

const selectWorkspace = `SELECT w.id, w.name, w.slug, w.owner_id, u.username, w.project_id, p.name, w.template_id, t.key, t.name, w.type, w.status,
	w.status_reason, w.cpu_millis, w.memory_bytes, w.storage_bytes, w.pids_limit, w.idle_timeout_min, w.max_duration_min,
	w.storage_used_bytes, w.last_activity_at, w.started_at, w.created_at, w.updated_at,
	(SELECT j.kind FROM background_jobs j WHERE j.resource_type='workspace' AND j.resource_id=w.id::text AND j.status IN ('queued','running')
	 ORDER BY j.created_at DESC LIMIT 1)
	FROM workspaces w JOIN users u ON u.id=w.owner_id JOIN workspace_templates t ON t.id=w.template_id
	LEFT JOIN projects p ON p.id=w.project_id`

func scanWorkspace(row pgx.Row) (Workspace, error) {
	var w Workspace
	err := row.Scan(&w.ID, &w.Name, &w.Slug, &w.OwnerID, &w.OwnerUsername, &w.ProjectID, &w.ProjectName, &w.TemplateID, &w.TemplateKey,
		&w.TemplateName, &w.Type, &w.Status, &w.StatusReason, &w.CPUMillis, &w.MemoryBytes, &w.StorageBytes, &w.PidsLimit,
		&w.IdleTimeoutMin, &w.MaxDurationMin, &w.StorageUsedBytes, &w.LastActivityAt, &w.StartedAt, &w.CreatedAt, &w.UpdatedAt, &w.PendingJob)
	return w, err
}

// Get loads a workspace (including deleted ones).
func Get(ctx context.Context, q db.Querier, id uuid.UUID) (Workspace, error) {
	return scanWorkspace(q.QueryRow(ctx, selectWorkspace+" WHERE w.id=$1", id))
}

// AppToken derives the per-container token that protects web apps inside a
// workspace (e.g. JupyterLab). The app proxy injects it after authorising
// the user, so processes that can reach the loopback port cannot use the app
// without it. It is derived (not stored) from SESSION_SECRET.
func AppToken(secret []byte, containerID uuid.UUID) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("itk-app-token:" + containerID.String()))
	return hex.EncodeToString(mac.Sum(nil))[:40]
}

// LinuxUser returns the dedicated Linux account name for a native workspace.
func LinuxUser(id uuid.UUID) string {
	return "itk-" + id.String()[:8] + id.String()[9:13]
}

// Container is a workspace container.
type Container struct {
	ID            uuid.UUID  `json:"id"`
	WorkspaceID   uuid.UUID  `json:"workspace_id"`
	WorkspaceName string     `json:"workspace_name"`
	OwnerID       uuid.UUID  `json:"owner_id"`
	OwnerUsername string     `json:"owner_username"`
	TemplateID    uuid.UUID  `json:"template_id"`
	TemplateName  string     `json:"template_name"`
	ImageID       uuid.UUID  `json:"image_id"`
	Image         string     `json:"image"`
	Name          string     `json:"name"`
	IsPrimary     bool       `json:"is_primary"`
	RuntimeID     *string    `json:"runtime_id"`
	Status        string     `json:"status"`
	StatusReason  *string    `json:"status_reason"`
	CPUMillis     int        `json:"cpu_millis"`
	MemoryBytes   int64      `json:"memory_bytes"`
	PidsLimit     int        `json:"pids_limit"`
	Ports         []Port     `json:"ports"`
	GPUDeviceIDs  []string   `json:"gpu_device_ids"`
	StartedAt     *time.Time `json:"started_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	PendingJob    *string    `json:"pending_job"`
}

// SelectContainer selects containers with workspace/template/image context.
const SelectContainer = `SELECT c.id, c.workspace_id, w.name, c.owner_id, u.username, c.template_id, t.name, c.image_id, i.reference, c.name,
	c.is_primary, c.runtime_id, c.status, c.status_reason, c.cpu_millis, c.memory_bytes, c.pids_limit, c.ports, c.gpu_device_ids,
	c.started_at, c.created_at, c.updated_at,
	(SELECT j.kind FROM background_jobs j WHERE j.resource_type='container' AND j.resource_id=c.id::text AND j.status IN ('queued','running')
	 ORDER BY j.created_at DESC LIMIT 1)
	FROM containers c JOIN workspaces w ON w.id=c.workspace_id JOIN users u ON u.id=c.owner_id
	JOIN workspace_templates t ON t.id=c.template_id JOIN container_images i ON i.id=c.image_id`

// ScanContainer scans a SelectContainer row.
func ScanContainer(row pgx.Row) (Container, error) {
	var c Container
	var ports []byte
	err := row.Scan(&c.ID, &c.WorkspaceID, &c.WorkspaceName, &c.OwnerID, &c.OwnerUsername, &c.TemplateID, &c.TemplateName, &c.ImageID,
		&c.Image, &c.Name, &c.IsPrimary, &c.RuntimeID, &c.Status, &c.StatusReason, &c.CPUMillis, &c.MemoryBytes, &c.PidsLimit,
		&ports, &c.GPUDeviceIDs, &c.StartedAt, &c.CreatedAt, &c.UpdatedAt, &c.PendingJob)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(ports, &c.Ports); err != nil {
		return c, err
	}
	if c.Ports == nil {
		c.Ports = []Port{}
	}
	if c.GPUDeviceIDs == nil {
		c.GPUDeviceIDs = []string{}
	}
	return c, nil
}

// ListContainers returns live (not removed) containers of a workspace.
func ListContainers(ctx context.Context, q db.Querier, workspaceID uuid.UUID) ([]Container, error) {
	rows, err := q.Query(ctx, SelectContainer+" WHERE c.workspace_id=$1 AND c.removed_at IS NULL ORDER BY c.is_primary DESC, c.created_at", workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Container{}
	for rows.Next() {
		c, err := ScanContainer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
