// Package agentproto defines the versioned request/response protocol between
// the ITK Cloud API/worker and the privileged host agent (itk-cloud-agent).
//
// Transport: HTTP/1.1 over a Unix domain socket with strict filesystem
// permissions. Every request is authenticated with an HMAC-SHA256 signature
// over method, path, timestamp, nonce and a body/operation digest using a
// shared secret, and replayed nonces are rejected.
//
// The agent accepts only the operations declared in this package. There is
// deliberately no generic "exec command" operation.
package agentproto

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

// ProtocolVersion is incremented on breaking changes. The agent rejects
// requests with a different major protocol version.
const ProtocolVersion = 1

// Operation names (the allowlist).
const (
	OpAgentInfo          = "agent.info"
	OpHostMetrics        = "host.metrics"
	OpGPUInventory       = "gpu.inventory"
	OpWorkspaceProvision = "workspace.provision"
	OpWorkspaceDestroy   = "workspace.destroy"
	OpWorkspaceUsage     = "workspace.usage"
	OpImageEnsure        = "image.ensure"
	OpContainerCreate    = "container.create"
	OpContainerStart     = "container.start"
	OpContainerStop      = "container.stop"
	OpContainerRestart   = "container.restart"
	OpContainerRemove    = "container.remove"
	OpContainerInspect   = "container.inspect"
	OpContainerLogs      = "container.logs"
	OpContainerStats     = "container.stats"
	OpContainerList      = "container.list"
	OpFilesList          = "files.list"
	OpFilesStat          = "files.stat"
	OpFilesMkdir         = "files.mkdir"
	OpFilesRename        = "files.rename"
	OpFilesDelete        = "files.delete"
	OpFilesRead          = "files.read"
	OpFilesWrite         = "files.write"
	OpFilesDownload      = "files.download"  // streaming endpoint
	OpFilesUpload        = "files.upload"    // streaming endpoint
	OpTerminalAttach     = "terminal.attach" // websocket endpoint
	OpNativeUserEnsure   = "native.user_ensure"
	OpBackupWorkspace    = "backup.workspace_files"
	OpRestoreWorkspace   = "restore.workspace_files"
	OpEmergencyStopAll   = "emergency.stop_all"
)

// Operations is the allowlist with the payload constructor for each op.
var Operations = map[string]func() Payload{
	OpAgentInfo:          func() Payload { return &Empty{} },
	OpHostMetrics:        func() Payload { return &Empty{} },
	OpGPUInventory:       func() Payload { return &Empty{} },
	OpWorkspaceProvision: func() Payload { return &WorkspaceProvision{} },
	OpWorkspaceDestroy:   func() Payload { return &WorkspaceRef{} },
	OpWorkspaceUsage:     func() Payload { return &WorkspaceRef{} },
	OpImageEnsure:        func() Payload { return &ImageEnsure{} },
	OpContainerCreate:    func() Payload { return &ContainerCreate{} },
	OpContainerStart:     func() Payload { return &ContainerRef{} },
	OpContainerStop:      func() Payload { return &ContainerRef{} },
	OpContainerRestart:   func() Payload { return &ContainerRef{} },
	OpContainerRemove:    func() Payload { return &ContainerRef{} },
	OpContainerInspect:   func() Payload { return &ContainerRef{} },
	OpContainerLogs:      func() Payload { return &ContainerLogs{} },
	OpContainerStats:     func() Payload { return &ContainerStatsReq{} },
	OpContainerList:      func() Payload { return &Empty{} },
	OpFilesList:          func() Payload { return &FilePath{} },
	OpFilesStat:          func() Payload { return &FilePath{} },
	OpFilesMkdir:         func() Payload { return &FilePath{} },
	OpFilesRename:        func() Payload { return &FileRename{} },
	OpFilesDelete:        func() Payload { return &FileDelete{} },
	OpFilesRead:          func() Payload { return &FileRead{} },
	OpFilesWrite:         func() Payload { return &FileWrite{} },
	OpFilesDownload:      func() Payload { return &FilePath{} },
	OpFilesUpload:        func() Payload { return &FileUpload{} },
	OpTerminalAttach:     func() Payload { return &TerminalAttach{} },
	OpNativeUserEnsure:   func() Payload { return &NativeUserEnsure{} },
	OpBackupWorkspace:    func() Payload { return &BackupWorkspace{} },
	OpRestoreWorkspace:   func() Payload { return &RestoreWorkspace{} },
	OpEmergencyStopAll:   func() Payload { return &Empty{} },
}

// Request is the envelope for every agent operation.
type Request struct {
	ProtocolVersion int             `json:"protocol_version"`
	Operation       string          `json:"operation"`
	RequestID       string          `json:"request_id"`
	ActorID         string          `json:"actor_id"`
	ResourceID      string          `json:"resource_id"`
	Reason          string          `json:"reason"`
	Payload         json.RawMessage `json:"payload"`
}

// Validate checks envelope fields.
func (r *Request) Validate() error {
	if r.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("unsupported protocol version %d (agent speaks %d)", r.ProtocolVersion, ProtocolVersion)
	}
	if _, ok := Operations[r.Operation]; !ok {
		return fmt.Errorf("operation %q is not allowed", r.Operation)
	}
	if r.RequestID == "" || len(r.RequestID) > 64 {
		return errors.New("request_id is required")
	}
	if r.ActorID == "" || len(r.ActorID) > 64 {
		return errors.New("actor_id is required")
	}
	if len(r.ResourceID) > 128 || len(r.Reason) > 500 {
		return errors.New("resource_id or reason too long")
	}
	if strings.TrimSpace(r.Reason) == "" {
		return errors.New("reason is required")
	}
	return nil
}

// Response is the envelope returned by the agent.
type Response struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error *Error          `json:"error,omitempty"`
}

// Error codes returned by the agent.
const (
	ErrCodeInvalid     = "invalid_request"
	ErrCodeNotFound    = "not_found"
	ErrCodeConflict    = "conflict"
	ErrCodeUnavailable = "unavailable"
	ErrCodeQuota       = "quota_exceeded"
	ErrCodeInternal    = "internal"
	ErrCodeForbidden   = "forbidden"
	ErrCodeTooLarge    = "too_large"
)

// Error is a structured agent error.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return "agent: " + e.Code + ": " + e.Message }

// IsCode reports whether err is an agent error with the given code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// Payload is implemented by every operation payload.
type Payload interface{ Validate() error }

// ---------------------------------------------------------------------------
// Validation helpers
// ---------------------------------------------------------------------------

var (
	uuidRe      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	runtimeIDRe = regexp.MustCompile(`^[0-9a-f]{12,64}$`)
	imageRefRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:@-]{1,250}$`)
	envKeyRe    = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
	nameRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}[a-z0-9]$`)
	linuxUserRe = regexp.MustCompile(`^itk-[a-z0-9][a-z0-9-]{1,26}$`)
	gpuUUIDRe   = regexp.MustCompile(`^(GPU|MIG)-[0-9a-fA-F-]{8,64}$`)
	backupRe    = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,200}$`)
)

// IsUUID reports whether s is a lowercase canonical UUID.
func IsUUID(s string) bool { return uuidRe.MatchString(s) }

// CleanRelPath validates a workspace-relative path supplied by a user and
// returns its cleaned form ("" for the workspace root). Absolute paths,
// traversal outside the root, NUL bytes and over-long paths are rejected.
// The agent additionally resolves symlinks before touching the filesystem.
func CleanRelPath(p string) (string, error) {
	if len(p) > 4096 || strings.ContainsRune(p, 0) {
		return "", errors.New("invalid path")
	}
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "/")
	c := path.Clean("/" + p)
	if c == "/" {
		return "", nil
	}
	c = strings.TrimPrefix(c, "/")
	for _, seg := range strings.Split(c, "/") {
		if seg == ".." || seg == "" || len(seg) > 255 {
			return "", errors.New("invalid path")
		}
	}
	return c, nil
}

// ---------------------------------------------------------------------------
// Payloads
// ---------------------------------------------------------------------------

// Empty is a payload with no fields.
type Empty struct{}

// Validate implements Payload.
func (Empty) Validate() error { return nil }

// WorkspaceRef identifies a workspace.
type WorkspaceRef struct {
	WorkspaceID string `json:"workspace_id"`
	Native      bool   `json:"native,omitempty"`
	LinuxUser   string `json:"linux_user,omitempty"`
}

// Validate implements Payload.
func (w *WorkspaceRef) Validate() error {
	if !IsUUID(w.WorkspaceID) {
		return errors.New("workspace_id must be a UUID")
	}
	if w.LinuxUser != "" && !linuxUserRe.MatchString(w.LinuxUser) {
		return errors.New("invalid linux_user")
	}
	return nil
}

// WorkspaceProvision creates the workspace storage.
type WorkspaceProvision struct {
	WorkspaceID  string `json:"workspace_id"`
	StorageBytes int64  `json:"storage_bytes"`
	// OwnerUID is the uid that owns the data directory (1000 for container
	// workspaces; the dedicated Linux user for native workspaces).
	OwnerUID  int    `json:"owner_uid"`
	Native    bool   `json:"native"`
	LinuxUser string `json:"linux_user,omitempty"`
}

// Validate implements Payload.
func (w *WorkspaceProvision) Validate() error {
	if !IsUUID(w.WorkspaceID) {
		return errors.New("workspace_id must be a UUID")
	}
	if w.StorageBytes < 64<<20 || w.StorageBytes > 4<<40 {
		return errors.New("storage_bytes out of range")
	}
	if w.OwnerUID < 1000 || w.OwnerUID > 1<<31-2 {
		return errors.New("owner_uid must be a non-system uid")
	}
	if w.Native && !linuxUserRe.MatchString(w.LinuxUser) {
		return errors.New("native workspaces require a valid linux_user")
	}
	return nil
}

// WorkspaceUsage reports storage usage.
type WorkspaceUsage struct {
	UsedBytes  int64 `json:"used_bytes"`
	LimitBytes int64 `json:"limit_bytes"`
	// Enforced is true when the limit is enforced by the filesystem
	// (loopback volume) rather than only measured.
	Enforced bool `json:"enforced"`
}

// ImageEnsure checks for (and optionally pulls) an image.
type ImageEnsure struct {
	Reference string `json:"reference"`
	Pull      bool   `json:"pull"`
}

// Validate implements Payload.
func (i *ImageEnsure) Validate() error {
	if !imageRefRe.MatchString(i.Reference) {
		return errors.New("invalid image reference")
	}
	return nil
}

// ImageInfo describes a local image.
type ImageInfo struct {
	Present   bool   `json:"present"`
	ID        string `json:"id,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

// PortSpec is a container port to publish on the host loopback interface.
type PortSpec struct {
	Port int    `json:"port"`
	Name string `json:"name,omitempty"`
}

// ContainerCreate creates a hardened workspace container.
type ContainerCreate struct {
	WorkspaceID  string            `json:"workspace_id"`
	ContainerID  string            `json:"container_id"` // ITK Cloud container UUID
	OwnerID      string            `json:"owner_id"`
	Name         string            `json:"name"`
	Image        string            `json:"image"`
	CPUMillis    int               `json:"cpu_millis"`
	MemoryBytes  int64             `json:"memory_bytes"`
	PidsLimit    int               `json:"pids_limit"`
	Env          map[string]string `json:"env"`
	Command      []string          `json:"command"`
	Ports        []PortSpec        `json:"ports"`
	GPUDevices   []string          `json:"gpu_devices"` // GPU UUIDs from an approved allocation
	RunAsUID     int               `json:"run_as_uid"`
	RunAsGID     int               `json:"run_as_gid"`
	ReadOnlyRoot bool              `json:"read_only_root"`
}

// Validate implements Payload.
func (c *ContainerCreate) Validate() error {
	switch {
	case !IsUUID(c.WorkspaceID), !IsUUID(c.ContainerID), !IsUUID(c.OwnerID):
		return errors.New("workspace_id, container_id and owner_id must be UUIDs")
	case !nameRe.MatchString(c.Name):
		return errors.New("invalid container name")
	case !imageRefRe.MatchString(c.Image):
		return errors.New("invalid image reference")
	case c.CPUMillis < 100 || c.CPUMillis > 256000:
		return errors.New("cpu_millis out of range")
	case c.MemoryBytes < 64<<20 || c.MemoryBytes > 2<<40:
		return errors.New("memory_bytes out of range")
	case c.PidsLimit < 32 || c.PidsLimit > 32768:
		return errors.New("pids_limit out of range")
	case c.RunAsUID < 1000 || c.RunAsGID < 1000:
		return errors.New("containers must run as a non-root, non-system user")
	case len(c.Env) > 64 || len(c.Command) > 64 || len(c.Ports) > 16 || len(c.GPUDevices) > 16:
		return errors.New("too many env vars, command args, ports or GPUs")
	}
	for k, v := range c.Env {
		if !envKeyRe.MatchString(k) || len(v) > 4096 || strings.ContainsRune(v, 0) {
			return fmt.Errorf("invalid env var %q", k)
		}
	}
	for _, a := range c.Command {
		if len(a) > 4096 || strings.ContainsRune(a, 0) {
			return errors.New("invalid command argument")
		}
	}
	for _, p := range c.Ports {
		if p.Port < 1024 || p.Port > 65535 {
			return errors.New("ports must be unprivileged (1024-65535)")
		}
	}
	for _, g := range c.GPUDevices {
		if !gpuUUIDRe.MatchString(g) {
			return errors.New("invalid GPU device UUID")
		}
	}
	return nil
}

// ContainerRef identifies a managed container.
type ContainerRef struct {
	ContainerID string `json:"container_id"` // ITK Cloud container UUID
	RuntimeID   string `json:"runtime_id"`
	TimeoutSec  int    `json:"timeout_sec,omitempty"`
}

// Validate implements Payload.
func (c *ContainerRef) Validate() error {
	if !IsUUID(c.ContainerID) {
		return errors.New("container_id must be a UUID")
	}
	if c.RuntimeID != "" && !runtimeIDRe.MatchString(c.RuntimeID) {
		return errors.New("invalid runtime_id")
	}
	if c.TimeoutSec < 0 || c.TimeoutSec > 300 {
		return errors.New("timeout_sec out of range")
	}
	return nil
}

// ContainerState is returned by create/start/inspect.
type ContainerState struct {
	RuntimeID  string            `json:"runtime_id"`
	Status     string            `json:"status"` // created|running|paused|restarting|removing|exited|dead|missing
	ExitCode   int               `json:"exit_code"`
	Error      string            `json:"error,omitempty"`
	StartedAt  *time.Time        `json:"started_at,omitempty"`
	FinishedAt *time.Time        `json:"finished_at,omitempty"`
	OOMKilled  bool              `json:"oom_killed"`
	HostPorts  map[int]int       `json:"host_ports,omitempty"` // container port -> host loopback port
	Labels     map[string]string `json:"labels,omitempty"`
}

// ContainerLogs requests recent log lines.
type ContainerLogs struct {
	ContainerRef
	Tail       int  `json:"tail"`
	Timestamps bool `json:"timestamps"`
}

// Validate implements Payload.
func (c *ContainerLogs) Validate() error {
	if err := c.ContainerRef.Validate(); err != nil {
		return err
	}
	if c.Tail < 1 || c.Tail > 5000 {
		return errors.New("tail must be between 1 and 5000")
	}
	return nil
}

// LogsResult carries log text (truncated by the agent if very large).
type LogsResult struct {
	Lines     []string `json:"lines"`
	Truncated bool     `json:"truncated"`
}

// ContainerStatsReq requests stats for a set of containers.
type ContainerStatsReq struct {
	RuntimeIDs []string `json:"runtime_ids"`
}

// Validate implements Payload.
func (c *ContainerStatsReq) Validate() error {
	if len(c.RuntimeIDs) > 500 {
		return errors.New("too many containers")
	}
	for _, id := range c.RuntimeIDs {
		if !runtimeIDRe.MatchString(id) {
			return errors.New("invalid runtime_id")
		}
	}
	return nil
}

// ContainerStats is a point-in-time resource sample.
type ContainerStats struct {
	RuntimeID   string  `json:"runtime_id"`
	CPUPercent  float64 `json:"cpu_percent"` // of one core * 100 (e.g. 150 = 1.5 cores)
	MemoryBytes int64   `json:"memory_bytes"`
	MemoryLimit int64   `json:"memory_limit"`
	Pids        int64   `json:"pids"`
	NetRxBytes  int64   `json:"net_rx_bytes"`
	NetTxBytes  int64   `json:"net_tx_bytes"`
}

// ManagedContainer is returned by container.list for reconciliation.
type ManagedContainer struct {
	RuntimeID   string `json:"runtime_id"`
	ContainerID string `json:"container_id"`
	WorkspaceID string `json:"workspace_id"`
	State       string `json:"state"`
}

// FilePath identifies a path inside a workspace.
type FilePath struct {
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path"`
}

// Validate implements Payload.
func (f *FilePath) Validate() error {
	if !IsUUID(f.WorkspaceID) {
		return errors.New("workspace_id must be a UUID")
	}
	c, err := CleanRelPath(f.Path)
	if err != nil {
		return err
	}
	f.Path = c
	return nil
}

// FileRename renames/moves within a workspace.
type FileRename struct {
	WorkspaceID string `json:"workspace_id"`
	From        string `json:"from"`
	To          string `json:"to"`
}

// Validate implements Payload.
func (f *FileRename) Validate() error {
	if !IsUUID(f.WorkspaceID) {
		return errors.New("workspace_id must be a UUID")
	}
	from, err := CleanRelPath(f.From)
	if err != nil || from == "" {
		return errors.New("invalid source path")
	}
	to, err := CleanRelPath(f.To)
	if err != nil || to == "" {
		return errors.New("invalid destination path")
	}
	f.From, f.To = from, to
	return nil
}

// FileDelete removes a file or directory.
type FileDelete struct {
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path"`
	Recursive   bool   `json:"recursive"`
}

// Validate implements Payload.
func (f *FileDelete) Validate() error {
	if !IsUUID(f.WorkspaceID) {
		return errors.New("workspace_id must be a UUID")
	}
	c, err := CleanRelPath(f.Path)
	if err != nil || c == "" {
		return errors.New("cannot delete the workspace root")
	}
	f.Path = c
	return nil
}

// FileRead reads a small text file for the editor.
type FileRead struct {
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path"`
	MaxBytes    int64  `json:"max_bytes"`
}

// Validate implements Payload.
func (f *FileRead) Validate() error {
	if !IsUUID(f.WorkspaceID) {
		return errors.New("workspace_id must be a UUID")
	}
	c, err := CleanRelPath(f.Path)
	if err != nil || c == "" {
		return errors.New("invalid path")
	}
	f.Path = c
	if f.MaxBytes < 1 || f.MaxBytes > 16<<20 {
		return errors.New("max_bytes out of range")
	}
	return nil
}

// FileWrite writes a small text file from the editor.
type FileWrite struct {
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path"`
	Content     string `json:"content"`
	// ExpectedModTime enables optimistic concurrency (RFC3339Nano, optional).
	ExpectedModTime string `json:"expected_mod_time,omitempty"`
	Create          bool   `json:"create"`
}

// Validate implements Payload.
func (f *FileWrite) Validate() error {
	if !IsUUID(f.WorkspaceID) {
		return errors.New("workspace_id must be a UUID")
	}
	c, err := CleanRelPath(f.Path)
	if err != nil || c == "" {
		return errors.New("invalid path")
	}
	f.Path = c
	if len(f.Content) > 16<<20 {
		return errors.New("content too large")
	}
	return nil
}

// FileUpload describes a streaming upload (body is the file content).
type FileUpload struct {
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path"` // destination file path
	MaxBytes    int64  `json:"max_bytes"`
	Overwrite   bool   `json:"overwrite"`
}

// Validate implements Payload.
func (f *FileUpload) Validate() error {
	if !IsUUID(f.WorkspaceID) {
		return errors.New("workspace_id must be a UUID")
	}
	c, err := CleanRelPath(f.Path)
	if err != nil || c == "" {
		return errors.New("invalid path")
	}
	f.Path = c
	if f.MaxBytes < 1 || f.MaxBytes > 64<<30 {
		return errors.New("max_bytes out of range")
	}
	return nil
}

// FileEntry describes a file in a listing.
type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Type    string    `json:"type"` // file | dir | symlink | other
	Size    int64     `json:"size"`
	Mode    string    `json:"mode"`
	ModTime time.Time `json:"mod_time"`
}

// FileContent is the result of files.read.
type FileContent struct {
	Content string    `json:"content"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	Binary  bool      `json:"binary"`
}

// TerminalAttach opens an interactive shell in a workspace.
type TerminalAttach struct {
	WorkspaceID string `json:"workspace_id"`
	// Exactly one of RuntimeID (container) or LinuxUser (native) is set.
	RuntimeID   string `json:"runtime_id,omitempty"`
	ContainerID string `json:"container_id,omitempty"`
	LinuxUser   string `json:"linux_user,omitempty"`
	RunAsUID    int    `json:"run_as_uid"`
	Cols        int    `json:"cols"`
	Rows        int    `json:"rows"`
	// Native workspace limits applied via a transient systemd scope.
	CPUMillis      int   `json:"cpu_millis,omitempty"`
	MemoryBytes    int64 `json:"memory_bytes,omitempty"`
	PidsLimit      int   `json:"pids_limit,omitempty"`
	IdleTimeoutSec int   `json:"idle_timeout_sec"`
	MaxDurationSec int   `json:"max_duration_sec"`
}

// Validate implements Payload.
func (t *TerminalAttach) Validate() error {
	if !IsUUID(t.WorkspaceID) {
		return errors.New("workspace_id must be a UUID")
	}
	container := t.RuntimeID != ""
	native := t.LinuxUser != ""
	if container == native {
		return errors.New("exactly one of runtime_id or linux_user is required")
	}
	if container && (!runtimeIDRe.MatchString(t.RuntimeID) || !IsUUID(t.ContainerID)) {
		return errors.New("invalid runtime_id or container_id")
	}
	if native && !linuxUserRe.MatchString(t.LinuxUser) {
		return errors.New("invalid linux_user")
	}
	if t.RunAsUID < 1000 {
		return errors.New("terminals never run as root or system users")
	}
	if t.Cols < 10 || t.Cols > 1000 || t.Rows < 5 || t.Rows > 500 {
		return errors.New("invalid terminal size")
	}
	if t.IdleTimeoutSec < 60 || t.MaxDurationSec < 60 {
		return errors.New("idle and max duration timeouts are required")
	}
	return nil
}

// TerminalControl is a JSON control message sent as a WebSocket text frame.
// Binary frames carry raw terminal bytes.
type TerminalControl struct {
	Type string `json:"type"` // resize | ping
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

// NativeUserEnsure creates the dedicated unprivileged Linux account for a
// native workspace (no sudo, no password login).
type NativeUserEnsure struct {
	WorkspaceID string `json:"workspace_id"`
	LinuxUser   string `json:"linux_user"`
}

// Validate implements Payload.
func (n *NativeUserEnsure) Validate() error {
	if !IsUUID(n.WorkspaceID) || !linuxUserRe.MatchString(n.LinuxUser) {
		return errors.New("invalid workspace_id or linux_user")
	}
	return nil
}

// NativeUser is the result of native.user_ensure.
type NativeUser struct {
	UID int `json:"uid"`
	GID int `json:"gid"`
}

// BackupWorkspace archives workspace files into the backup directory,
// encrypted to the given age recipient.
type BackupWorkspace struct {
	WorkspaceIDs []string `json:"workspace_ids"`
	FileName     string   `json:"file_name"`
	AgeRecipient string   `json:"age_recipient"`
}

// Validate implements Payload.
func (b *BackupWorkspace) Validate() error {
	if len(b.WorkspaceIDs) == 0 || len(b.WorkspaceIDs) > 10000 {
		return errors.New("workspace_ids required")
	}
	for _, id := range b.WorkspaceIDs {
		if !IsUUID(id) {
			return errors.New("invalid workspace id")
		}
	}
	if !backupRe.MatchString(b.FileName) || strings.Contains(b.FileName, "..") {
		return errors.New("invalid file_name")
	}
	if !strings.HasPrefix(b.AgeRecipient, "age1") || len(b.AgeRecipient) > 200 {
		return errors.New("an age recipient is required; unencrypted backups are not allowed")
	}
	return nil
}

// RestoreWorkspace restores workspace files from an encrypted archive. The
// age identity is passed in the request and never written to disk.
type RestoreWorkspace struct {
	FileName    string `json:"file_name"`
	AgeIdentity string `json:"age_identity"`
	WorkspaceID string `json:"workspace_id,omitempty"` // restore one workspace only
}

// Validate implements Payload.
func (r *RestoreWorkspace) Validate() error {
	if !backupRe.MatchString(r.FileName) || strings.Contains(r.FileName, "..") {
		return errors.New("invalid file_name")
	}
	if !strings.HasPrefix(r.AgeIdentity, "AGE-SECRET-KEY-") {
		return errors.New("invalid age identity")
	}
	if r.WorkspaceID != "" && !IsUUID(r.WorkspaceID) {
		return errors.New("invalid workspace id")
	}
	return nil
}

// BackupResult describes a created archive.
type BackupResult struct {
	FileName string `json:"file_name"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

// ---------------------------------------------------------------------------
// Results for info/metrics
// ---------------------------------------------------------------------------

// AgentInfo describes the agent and host capabilities.
type AgentInfo struct {
	AgentVersion    string       `json:"agent_version"`
	ProtocolVersion int          `json:"protocol_version"`
	Hostname        string       `json:"hostname"`
	OS              string       `json:"os"`
	Platform        string       `json:"platform"`
	Kernel          string       `json:"kernel"`
	Arch            string       `json:"arch"`
	CPUModel        string       `json:"cpu_model"`
	CPUCores        int          `json:"cpu_cores"`
	CPUThreads      int          `json:"cpu_threads"`
	MemoryTotal     uint64       `json:"memory_total"`
	BootTime        time.Time    `json:"boot_time"`
	Capabilities    Capabilities `json:"capabilities"`
}

// Capabilities lists optional features available on the host.
type Capabilities struct {
	Docker           bool   `json:"docker"`
	DockerVersion    string `json:"docker_version,omitempty"`
	DockerError      string `json:"docker_error,omitempty"`
	NVIDIA           bool   `json:"nvidia"`
	NVIDIARuntime    bool   `json:"nvidia_runtime"`
	NativeWorkspaces bool   `json:"native_workspaces"`
	StorageDriver    string `json:"storage_driver"` // loopback | directory
	StorageEnforced  bool   `json:"storage_enforced"`
	AppArmor         bool   `json:"apparmor"`
	Seccomp          bool   `json:"seccomp"`
}

// HostMetrics is a host metrics snapshot.
type HostMetrics struct {
	CollectedAt   time.Time    `json:"collected_at"`
	CPUPercent    float64      `json:"cpu_percent"`
	PerCore       []float64    `json:"per_core"`
	Load1         float64      `json:"load1"`
	Load5         float64      `json:"load5"`
	Load15        float64      `json:"load15"`
	MemoryTotal   uint64       `json:"memory_total"`
	MemoryUsed    uint64       `json:"memory_used"`
	MemoryAvail   uint64       `json:"memory_available"`
	SwapTotal     uint64       `json:"swap_total"`
	SwapUsed      uint64       `json:"swap_used"`
	Disks         []DiskUsage  `json:"disks"`
	NetRxBps      float64      `json:"net_rx_bytes_per_sec"`
	NetTxBps      float64      `json:"net_tx_bytes_per_sec"`
	UptimeSeconds uint64       `json:"uptime_seconds"`
	Docker        DockerStatus `json:"docker"`
}

// DiskUsage describes a mounted filesystem.
type DiskUsage struct {
	Mountpoint string `json:"mountpoint"`
	Fstype     string `json:"fstype"`
	Total      uint64 `json:"total"`
	Used       uint64 `json:"used"`
	Free       uint64 `json:"free"`
}

// DockerStatus summarises the container runtime.
type DockerStatus struct {
	Available         bool   `json:"available"`
	Version           string `json:"version,omitempty"`
	Error             string `json:"error,omitempty"`
	ContainersRunning int    `json:"containers_running"`
	ContainersStopped int    `json:"containers_stopped"`
	ContainersPaused  int    `json:"containers_paused"`
	Images            int    `json:"images"`
}

// GPUInventory is the result of gpu.inventory.
type GPUInventory struct {
	Available bool        `json:"available"`
	Error     string      `json:"error,omitempty"`
	GPUs      []GPUDevice `json:"gpus"`
}

// GPUDevice describes one NVIDIA GPU. Pointer fields are nil when the
// metric is not reported by the driver ("[N/A]").
type GPUDevice struct {
	Index         int          `json:"index"`
	UUID          string       `json:"uuid"`
	Name          string       `json:"name"`
	DriverVersion string       `json:"driver_version"`
	CUDAVersion   string       `json:"cuda_version,omitempty"`
	MemoryTotal   *int64       `json:"memory_total_bytes"`
	MemoryUsed    *int64       `json:"memory_used_bytes"`
	Utilization   *int         `json:"utilization_pct"`
	Temperature   *int         `json:"temperature_c"`
	PowerDraw     *float64     `json:"power_draw_w"`
	MIGMode       string       `json:"mig_mode,omitempty"`
	Processes     []GPUProcess `json:"processes"`
}

// GPUProcess is a compute process using a GPU.
type GPUProcess struct {
	PID         int    `json:"pid"`
	Name        string `json:"name"`
	MemoryBytes int64  `json:"memory_bytes"`
	ContainerID string `json:"container_id,omitempty"` // ITK container UUID when attributable
}
