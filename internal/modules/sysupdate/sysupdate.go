// Package sysupdate offers platform self-updates from GitHub releases.
//
// The API checks the latest release of UPDATE_REPO, compares it with the
// running version and, on request of a holder of system.update, asks the
// host agent (platform.update) to start the root updater unit. The client
// only chooses *whether* to update to the release the server found; it can
// never supply a URL, repository or arbitrary version. The agent verifies the
// repository against its own root-only configuration.
package sysupdate

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/itk-cloud/itk-cloud/internal/modules/agent"
	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/modules/notifications"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// Permissions.
const (
	PermUpdate = "system.update"        // start updates (super_admin)
	PermView   = "host.operations.view" // view update status
)

// AgentCaller is the subset of the agent facade used here.
type AgentCaller interface {
	Do(ctx context.Context, op, resourceID, reason string, payload, out any) error
}

// Service checks for and starts platform updates.
type Service struct {
	Checker   *Checker
	Agent     AgentCaller
	Installed string // running version (ldflags)
	Enabled   bool   // UPDATE_CHECK_ENABLED
}

// New builds the service.
func New(repo string, enabled bool, installed string, ag AgentCaller) *Service {
	return &Service{Checker: &Checker{Repo: repo, Arch: runtime.GOARCH}, Agent: ag, Installed: installed, Enabled: enabled}
}

// Job is the updater state reported by the agent.
type Job = agentproto.UpdateStatus

// Status is the GET /admin/system/update response.
type Status struct {
	InstalledVersion string     `json:"installed_version"`
	Enabled          bool       `json:"enabled"`
	Repo             string     `json:"repo"`
	Latest           *Release   `json:"latest"`
	CheckedAt        *time.Time `json:"checked_at"`
	CheckError       string     `json:"check_error"`
	UpdateAvailable  bool       `json:"update_available"`
	CanUpdate        bool       `json:"can_update"`
	Job              *Job       `json:"job"`
	JobError         string     `json:"job_error"`
}

// updateAvailable reports whether rel is newer than installed and installable.
func updateAvailable(installed string, rel *Release) bool {
	if rel == nil || !rel.AssetAvailable {
		return false
	}
	c, ok := CompareVersions(rel.Version, installed)
	return ok && c > 0
}

// Status assembles the update status; force re-checks GitHub.
func (s *Service) Status(ctx context.Context, force bool) Status {
	st := Status{InstalledVersion: s.Installed, Enabled: s.Enabled, Repo: s.Checker.Repo}
	if s.Enabled {
		snap := s.Checker.Latest(ctx, force)
		st.Latest, st.CheckError = snap.Release, snap.Err
		if !snap.CheckedAt.IsZero() {
			t := snap.CheckedAt.UTC()
			st.CheckedAt = &t
		}
		st.UpdateAvailable = updateAvailable(s.Installed, st.Latest)
	}
	if s.Agent != nil {
		actx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var job Job
		if err := s.Agent.Do(actx, agentproto.OpPlatformUpdateStatus, "platform", "view update status", nil, &job); err != nil {
			st.JobError = "Update status is unavailable (host agent unreachable or too old)."
		} else {
			st.Job = &job
		}
	}
	return st
}

// NotifyNewRelease sends one notification per new release to holders of
// system.update. Called periodically by the worker.
func (s *Service) NotifyNewRelease(ctx context.Context, q db.Querier) error {
	if !s.Enabled {
		return nil
	}
	snap := s.Checker.Latest(ctx, true)
	if snap.Err != "" || !updateAvailable(s.Installed, snap.Release) {
		return nil
	}
	title := fmt.Sprintf("Jenderal Cloud OS v%s is available", snap.Release.Version)
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM notifications WHERE type=$1 AND title=$2)`, notifications.TypeSystem, title).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	return notifications.NotifyPermission(ctx, q, PermUpdate, notifications.Message{
		Type: notifications.TypeSystem, Severity: "info", Title: title, Link: "/admin/updates",
		Body: fmt.Sprintf("Installed version: %s. Review the release notes and update from Administration → System update.", s.Installed),
	})
}

// ConfirmPhrase returns the text that must be typed to start an update.
func ConfirmPhrase(ver string) string { return "v" + ver }

// Handler serves /admin/system/update endpoints.
type Handler struct {
	Svc *Service
	// Audit records an audit entry (audit.Record with the database pool).
	Audit func(ctx context.Context, e audit.Entry) error
}

func (h *Handler) audit(ctx context.Context, e audit.Entry) {
	if h.Audit != nil {
		_ = h.Audit(ctx, e)
	}
}

// Get handles GET /admin/system/update.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	p := authz.FromContext(r.Context())
	if !p.CanAny(PermUpdate, PermView) {
		return httpx.Forbidden("")
	}
	st := h.Svc.Status(r.Context(), false)
	st.CanUpdate = p.Can(PermUpdate)
	return httpx.OK(w, st)
}

// Check handles POST /admin/system/update/check (forces a GitHub check).
func (h *Handler) Check(w http.ResponseWriter, r *http.Request) error {
	p := authz.FromContext(r.Context())
	if !p.CanAny(PermUpdate, PermView) {
		return httpx.Forbidden("")
	}
	if !h.Svc.Enabled {
		return httpx.Conflict("Update checks are disabled (UPDATE_CHECK_ENABLED=false).")
	}
	st := h.Svc.Status(r.Context(), true)
	st.CanUpdate = p.Can(PermUpdate)
	return httpx.OK(w, st)
}

// Start handles POST /admin/system/update/start.
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can(PermUpdate) {
		h.audit(ctx, audit.Entry{Action: "system.update", Outcome: audit.Denied, ResourceType: "platform"})
		return httpx.Forbidden("")
	}
	var in struct {
		Version string `json:"version"`
		Confirm string `json:"confirm"`
		Reason  string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	in.Version = strings.TrimPrefix(strings.TrimSpace(in.Version), "v")
	in.Reason = strings.TrimSpace(in.Reason)
	if !h.Svc.Enabled {
		return httpx.Conflict("Updates are disabled (UPDATE_CHECK_ENABLED=false).")
	}
	snap := h.Svc.Checker.Latest(ctx, false)
	rel := snap.Release
	d := map[string]string{}
	switch {
	case rel == nil:
		return httpx.Conflict("No release information is available. Check for updates first.")
	case in.Version != rel.Version:
		d["version"] = "only the latest release (v" + rel.Version + ") can be installed"
	case !updateAvailable(h.Svc.Installed, rel):
		d["version"] = "v" + rel.Version + " is not newer than the installed version or has no package for this server"
	}
	if in.Confirm != ConfirmPhrase(in.Version) {
		d["confirm"] = "type " + ConfirmPhrase(in.Version) + " to confirm"
	}
	if len(in.Reason) < 10 || len(in.Reason) > 500 {
		d["reason"] = "a reason of 10 to 500 characters is required"
	}
	if len(d) > 0 {
		return httpx.Validation(d)
	}
	actx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var job Job
	err := h.Svc.Agent.Do(actx, agentproto.OpPlatformUpdate, "platform", in.Reason,
		agentproto.PlatformUpdate{Version: rel.Version, Repo: h.Svc.Checker.Repo, Arch: h.Svc.Checker.Arch}, &job)
	outcome := audit.Success
	if err != nil {
		outcome = audit.Failure
	}
	h.audit(ctx, audit.Entry{Action: "system.update", Outcome: outcome, ResourceType: "platform", ResourceID: rel.Version,
		Reason: in.Reason, Metadata: map[string]any{"from_version": h.Svc.Installed, "to_version": rel.Version, "repo": h.Svc.Checker.Repo}})
	if err != nil {
		return agent.MapError(err)
	}
	st := h.Svc.Status(ctx, false)
	if st.Job == nil {
		st.Job, st.JobError = &job, ""
	}
	st.CanUpdate = true
	return httpx.Accepted(w, st)
}
