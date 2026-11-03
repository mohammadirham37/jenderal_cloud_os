// Package hostops exposes allow-listed host operations for administrators.
// Currently: emergency stop of all workloads. Every operation requires the
// host.operations.execute permission, an explicit confirmation phrase and a
// reason, and is audited.
package hostops

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itk-cloud/itk-cloud/internal/modules/agent"
	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/modules/notifications"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// ConfirmPhrase must be typed to confirm an emergency stop.
const ConfirmPhrase = "STOP ALL WORKLOADS"

// Handler serves /admin/host endpoints.
type Handler struct {
	Pool  *pgxpool.Pool
	Agent *agent.Agent
}

// EmergencyStop handles POST /admin/host/emergency-stop.
func (h *Handler) EmergencyStop(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can("host.operations.execute") {
		_ = audit.Record(ctx, h.Pool, audit.Entry{Action: "host.emergency_stop", Outcome: audit.Denied})
		return httpx.Forbidden("")
	}
	var in struct {
		Confirm string `json:"confirm"`
		Reason  string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	d := map[string]string{}
	if in.Confirm != ConfirmPhrase {
		d["confirm"] = "type " + ConfirmPhrase + " to confirm"
	}
	if len(strings.TrimSpace(in.Reason)) < 10 {
		d["reason"] = "a reason of at least 10 characters is required"
	}
	if len(d) > 0 {
		return httpx.Validation(d)
	}
	actx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var res map[string]any
	err := h.Agent.Do(actx, agentproto.OpEmergencyStopAll, "host", in.Reason, nil, &res)
	outcome := audit.Success
	if err != nil {
		outcome = audit.Failure
	}
	_ = audit.Record(ctx, h.Pool, audit.Entry{Action: "host.emergency_stop", Outcome: outcome, ResourceType: "host", Reason: in.Reason, Metadata: map[string]any{"result": res}})
	if err != nil {
		return agent.MapError(err)
	}
	if _, err := h.Pool.Exec(ctx, `UPDATE containers SET status='stopped', status_reason='emergency stop', updated_at=now() WHERE status IN ('running','starting') AND removed_at IS NULL`); err != nil {
		return err
	}
	if _, err := h.Pool.Exec(ctx, `UPDATE workspaces SET status='stopped', status_reason='Emergency stop by administrator: ' || $1, updated_at=now() WHERE status='running'`, in.Reason); err != nil {
		return err
	}
	_, _ = h.Pool.Exec(ctx, `UPDATE terminal_sessions SET status='closed', ended_at=now(), end_reason='emergency_stop' WHERE status IN ('pending','active')`)
	_ = notifications.NotifyAll(ctx, h.Pool, notifications.Message{Type: notifications.TypeMaintenance, Severity: "critical",
		Title: "All workloads were stopped by an administrator", Body: in.Reason})
	return httpx.OK(w, res)
}
