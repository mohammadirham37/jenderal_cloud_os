package workspaces

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
)

// Access levels, ordered from least to most privileged per capability:
//
//	view    — see workspace metadata and status
//	operate — start/stop/restart, view logs and metrics
//	edit    — terminal, files, proxy (access to user data)
//	manage  — settings, members, delete
type Level int

// Access levels.
const (
	LevelNone Level = iota
	LevelView
	LevelOperate
	LevelEdit
	LevelManage
)

func (l Level) String() string {
	return [...]string{"none", "view", "operate", "edit", "manage"}[l]
}

// EmergencyReasonHeader carries the justification for emergency access to
// another user's workspace data (requires workspaces.access_all).
const EmergencyReasonHeader = "X-Access-Reason"

// Grants describes what a principal may do with a workspace.
type Grants struct {
	View, Operate, Edit, Manage bool
	// Emergency is true when Edit is granted only via workspaces.access_all.
	Emergency bool
}

// Has reports whether the grants include level l.
func (g Grants) Has(l Level) bool {
	switch l {
	case LevelView:
		return g.View
	case LevelOperate:
		return g.Operate
	case LevelEdit:
		return g.Edit
	case LevelManage:
		return g.Manage
	}
	return false
}

// Highest returns the highest level granted (for display).
func (g Grants) Highest() string {
	switch {
	case g.Manage && g.Edit:
		return "owner"
	case g.Edit:
		return "editor"
	case g.Manage || g.Operate:
		return "operator"
	case g.View:
		return "viewer"
	}
	return "none"
}

// ComputeGrants evaluates ownership, workspace membership, project membership
// and global permissions. Private data (Edit) is only available to the owner,
// workspace editors, or via audited emergency access.
func ComputeGrants(ctx context.Context, q db.Querier, p *authz.Principal, w *Workspace) (Grants, error) {
	var g Grants
	if p == nil {
		return g, nil
	}
	if w.OwnerID == p.UserID {
		return Grants{View: true, Operate: true, Edit: true, Manage: true}, nil
	}
	var wsRole, projRole *string
	err := q.QueryRow(ctx, `SELECT
		(SELECT role FROM workspace_members WHERE workspace_id=$1 AND user_id=$2),
		(SELECT role FROM project_members WHERE project_id=$3 AND user_id=$2)`, w.ID, p.UserID, w.ProjectID).Scan(&wsRole, &projRole)
	if err != nil {
		return g, err
	}
	if wsRole != nil {
		g.View = true
		if *wsRole == "editor" {
			g.Operate, g.Edit = true, true
		}
	}
	if projRole != nil {
		g.View = true
		if *projRole == "owner" || *projRole == "maintainer" {
			g.Operate = true
		}
	}
	if p.Can("workspaces.view_all") || p.Can("workspaces.manage_all") {
		g.View = true
	}
	if p.Can("workspaces.manage_all") {
		g.Operate, g.Manage = true, true
	}
	if !g.Edit && p.Can("workspaces.access_all") {
		g.View, g.Edit, g.Emergency = true, true, true
	}
	return g, nil
}

// Authorize loads a workspace and checks that p holds level. It returns
// 404 when the caller cannot even view the workspace (no existence leak) and
// 403 when they can view it but lack the level. Deleted workspaces are
// treated as not found. Emergency edit access requires a reason header and is
// audited.
func Authorize(ctx context.Context, q db.Querier, r *http.Request, id uuid.UUID, level Level) (*Workspace, Grants, error) {
	p := authz.FromContext(ctx)
	w, err := Get(ctx, q, id)
	if db.IsNotFound(err) || (err == nil && w.Status == StatusDeleted) {
		return nil, Grants{}, httpx.NotFound("Workspace")
	}
	if err != nil {
		return nil, Grants{}, err
	}
	g, err := ComputeGrants(ctx, q, p, &w)
	if err != nil {
		return nil, g, err
	}
	if !g.View {
		return nil, g, httpx.NotFound("Workspace")
	}
	if !g.Has(level) {
		return nil, g, httpx.Forbidden("You do not have " + level.String() + " access to this workspace.")
	}
	if level == LevelEdit && g.Emergency {
		reason := ""
		if r != nil {
			reason = strings.TrimSpace(r.Header.Get(EmergencyReasonHeader))
		}
		if len(reason) < 10 {
			return nil, g, httpx.NewError(http.StatusForbidden, "emergency_reason_required",
				"Emergency access to another user's workspace requires a justification (X-Access-Reason header, at least 10 characters).")
		}
		action := "workspace.emergency_access"
		if r != nil {
			action += ":" + r.Method + " " + r.URL.Path
		}
		_ = audit.Record(ctx, q, audit.Entry{Action: "workspace.emergency_access", ResourceType: "workspace", ResourceID: id.String(),
			Reason: reason, Metadata: map[string]any{"owner_id": w.OwnerID, "operation": action}})
	}
	w.Access = g.Highest()
	return &w, g, nil
}
