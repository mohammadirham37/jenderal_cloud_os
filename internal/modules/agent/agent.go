// Package agent wraps the host agent client for API modules (filling actor,
// correlation ID and reason on every call) and tracks agent node status.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// NodeName is the agent node identifier for the single-server deployment.
const NodeName = "local"

// Agent is the API-side agent facade.
type Agent struct {
	Client *agentproto.Client
	Pool   *pgxpool.Pool
}

func actor(ctx context.Context) string {
	if p := authz.FromContext(ctx); p != nil && p.UserID != uuid.Nil {
		return p.UserID.String()
	}
	return "system"
}

func (a *Agent) call(ctx context.Context, op, resourceID, reason string, payload any) agentproto.Call {
	return agentproto.Call{Op: op, RequestID: httpx.RequestID(ctx), ActorID: actor(ctx), ResourceID: resourceID, Reason: reason, Payload: payload}
}

// Do performs an operation.
func (a *Agent) Do(ctx context.Context, op, resourceID, reason string, payload, out any) error {
	return a.Client.Do(ctx, a.call(ctx, op, resourceID, reason, payload), out)
}

// Call builds a call for streaming endpoints.
func (a *Agent) Call(ctx context.Context, op, resourceID, reason string, payload any) agentproto.Call {
	return a.call(ctx, op, resourceID, reason, payload)
}

// MapError converts agent errors into safe HTTP errors.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, agentproto.ErrUnavailable) {
		return httpx.Unavailable("The host agent is unavailable. Try again later or contact an administrator.").Wrap(err)
	}
	var ae *agentproto.Error
	if errors.As(err, &ae) {
		switch ae.Code {
		case agentproto.ErrCodeNotFound:
			return httpx.NewError(http.StatusNotFound, "not_found", "The requested file or resource was not found.").Wrap(err)
		case agentproto.ErrCodeConflict:
			return httpx.Conflict(capitalize(ae.Message) + ".").Wrap(err)
		case agentproto.ErrCodeInvalid:
			return httpx.BadRequest(capitalize(ae.Message) + ".").Wrap(err)
		case agentproto.ErrCodeTooLarge:
			return httpx.NewError(http.StatusRequestEntityTooLarge, "payload_too_large", "The file exceeds the size limit.").Wrap(err)
		case agentproto.ErrCodeQuota:
			return httpx.NewError(http.StatusInsufficientStorage, "quota_exceeded", "Workspace storage quota exceeded.").Wrap(err)
		case agentproto.ErrCodeUnavailable:
			return httpx.Unavailable(capitalize(ae.Message) + ".").Wrap(err)
		case agentproto.ErrCodeForbidden:
			return httpx.Forbidden("The host agent refused the operation.").Wrap(err)
		}
	}
	return httpx.Internal(err)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[0] >= 'a' && b[0] <= 'z' {
		b[0] -= 32
	}
	return string(b)
}

// Node is the persisted agent status.
type Node struct {
	Name                    string                  `json:"name"`
	Hostname                *string                 `json:"hostname"`
	AgentVersion            *string                 `json:"agent_version"`
	ProtocolVersion         *int                    `json:"protocol_version"`
	OS                      *string                 `json:"os"`
	Kernel                  *string                 `json:"kernel"`
	Arch                    *string                 `json:"arch"`
	Capabilities            agentproto.Capabilities `json:"capabilities"`
	HostInfo                HostInfo                `json:"host_info"`
	Status                  string                  `json:"status"`
	LastError               *string                 `json:"last_error"`
	LastSeenAt              *time.Time              `json:"last_seen_at"`
	UpdatedAt               time.Time               `json:"updated_at"`
	ExpectedProtocolVersion int                     `json:"expected_protocol_version"`
}

// HostInfo is static host inventory reported by the agent.
type HostInfo struct {
	CPUModel    string    `json:"cpu_model"`
	CPUCores    int       `json:"cpu_cores"`
	CPUThreads  int       `json:"cpu_threads"`
	MemoryTotal uint64    `json:"memory_total"`
	BootTime    time.Time `json:"boot_time"`
}

// Refresh queries agent.info and persists the node status. Returns the info.
func (a *Agent) Refresh(ctx context.Context) (*agentproto.AgentInfo, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var info agentproto.AgentInfo
	err := a.Do(cctx, agentproto.OpAgentInfo, NodeName, "heartbeat", nil, &info)
	if err != nil {
		msg := err.Error()
		_, _ = a.Pool.Exec(ctx, `INSERT INTO agent_nodes (name, status, last_error, updated_at) VALUES ($1,'offline',$2,now())
			ON CONFLICT (name) DO UPDATE SET status='offline', last_error=$2, updated_at=now()`, NodeName, msg)
		return nil, err
	}
	status := "online"
	var lastErr *string
	if info.ProtocolVersion != agentproto.ProtocolVersion {
		status = "degraded"
		s := "protocol version mismatch"
		lastErr = &s
	} else if !info.Capabilities.Docker {
		status = "degraded"
		s := "container runtime unavailable: " + info.Capabilities.DockerError
		lastErr = &s
	}
	caps, _ := json.Marshal(info.Capabilities)
	host, _ := json.Marshal(HostInfo{CPUModel: info.CPUModel, CPUCores: info.CPUCores, CPUThreads: info.CPUThreads,
		MemoryTotal: info.MemoryTotal, BootTime: info.BootTime})
	_, err = a.Pool.Exec(ctx, `INSERT INTO agent_nodes (name, hostname, agent_version, protocol_version, os, kernel, arch, capabilities, host_info, status, last_error, last_seen_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now(),now())
		ON CONFLICT (name) DO UPDATE SET hostname=$2, agent_version=$3, protocol_version=$4, os=$5, kernel=$6, arch=$7,
		capabilities=$8, host_info=$9, status=$10, last_error=$11, last_seen_at=now(), updated_at=now()`,
		NodeName, info.Hostname, info.AgentVersion, info.ProtocolVersion, info.Platform, info.Kernel, info.Arch, caps, host, status, lastErr)
	return &info, err
}

// GetNode returns the persisted node status.
func (a *Agent) GetNode(ctx context.Context) (*Node, error) {
	var n Node
	var caps, host []byte
	err := a.Pool.QueryRow(ctx, `SELECT name, hostname, agent_version, protocol_version, os, kernel, arch, capabilities, host_info, status, last_error, last_seen_at, updated_at
		FROM agent_nodes WHERE name=$1`, NodeName).Scan(&n.Name, &n.Hostname, &n.AgentVersion, &n.ProtocolVersion, &n.OS, &n.Kernel, &n.Arch,
		&caps, &host, &n.Status, &n.LastError, &n.LastSeenAt, &n.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(caps, &n.Capabilities)
	_ = json.Unmarshal(host, &n.HostInfo)
	n.ExpectedProtocolVersion = agentproto.ProtocolVersion
	// A node not seen for 2 minutes is considered offline.
	if n.LastSeenAt == nil || time.Since(*n.LastSeenAt) > 2*time.Minute {
		n.Status = "offline"
	}
	return &n, nil
}

// Capabilities returns cached capabilities (zero value if unknown).
func (a *Agent) Capabilities(ctx context.Context) agentproto.Capabilities {
	n, err := a.GetNode(ctx)
	if err != nil {
		return agentproto.Capabilities{}
	}
	return n.Capabilities
}

// Handler serves agent status endpoints.
type Handler struct{ Agent *Agent }

// Status handles GET /admin/agent/status: live refresh, falling back to the
// last persisted state.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) error {
	p := authz.FromContext(r.Context())
	if !p.CanAny("agent.view", "host.operations.view") {
		return httpx.Forbidden("")
	}
	_, liveErr := h.Agent.Refresh(r.Context())
	n, err := h.Agent.GetNode(r.Context())
	if err != nil {
		return httpx.OK(w, map[string]any{"status": "unknown", "error": "The agent has never reported in.", "expected_protocol_version": agentproto.ProtocolVersion})
	}
	resp := map[string]any{"node": n}
	if liveErr != nil {
		resp["live_error"] = "Agent did not respond to a live check."
	}
	return httpx.OK(w, resp)
}
