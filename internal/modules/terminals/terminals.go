// Package terminals manages browser terminal sessions. The browser connects
// a WebSocket to the API (authenticated by the session cookie and Origin
// check); the API re-validates the user, workspace access and terminal
// session on every (re)connect and relays bytes to a PTY created by the host
// agent. Terminal content is never stored.
package terminals

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/itk-cloud/itk-cloud/internal/modules/agent"
	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/modules/auth"
	"github.com/itk-cloud/itk-cloud/internal/modules/resources"
	ws "github.com/itk-cloud/itk-cloud/internal/modules/workspaces"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/config"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
	"github.com/itk-cloud/itk-cloud/internal/platform/logging"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// Session is a terminal session.
type Session struct {
	ID            uuid.UUID  `json:"id"`
	UserID        uuid.UUID  `json:"user_id"`
	WorkspaceID   uuid.UUID  `json:"workspace_id"`
	WorkspaceName string     `json:"workspace_name"`
	ContainerID   *uuid.UUID `json:"container_id"`
	ContainerName *string    `json:"container_name"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	ConnectedAt   *time.Time `json:"connected_at"`
	LastActivity  *time.Time `json:"last_activity_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	EndedAt       *time.Time `json:"ended_at"`
	EndReason     *string    `json:"end_reason"`
	WebSocketPath string     `json:"websocket_path"`
}

const selectSession = `SELECT t.id, t.user_id, t.workspace_id, w.name, t.container_id, c.name, t.status, t.created_at, t.connected_at,
	t.last_activity_at, t.expires_at, t.ended_at, t.end_reason FROM terminal_sessions t JOIN workspaces w ON w.id=t.workspace_id
	LEFT JOIN containers c ON c.id=t.container_id`

func scan(row pgx.Row) (Session, error) {
	var s Session
	err := row.Scan(&s.ID, &s.UserID, &s.WorkspaceID, &s.WorkspaceName, &s.ContainerID, &s.ContainerName, &s.Status, &s.CreatedAt,
		&s.ConnectedAt, &s.LastActivity, &s.ExpiresAt, &s.EndedAt, &s.EndReason)
	s.WebSocketPath = "/api/v1/terminals/" + s.ID.String() + "/ws"
	return s, err
}

// Handler serves terminal endpoints.
type Handler struct {
	Svc *ws.Service
	Cfg *config.Config
}

type createInput struct {
	ContainerID *uuid.UUID `json:"container_id"`
	Cols        int        `json:"cols"`
	Rows        int        `json:"rows"`
}

// Create handles POST /workspaces/{id}/terminals.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can("terminals.open") {
		return httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Workspace")
	}
	wsp, g, err := ws.Authorize(ctx, h.Svc.Pool, r, id, ws.LevelEdit)
	if err != nil {
		return err
	}
	var in createInput
	if r.ContentLength > 0 {
		if err := httpx.DecodeJSON(r, &in); err != nil {
			return err
		}
	}
	if in.Cols == 0 {
		in.Cols, in.Rows = 120, 32
	}
	if in.Cols < 10 || in.Cols > 1000 || in.Rows < 5 || in.Rows > 500 {
		return httpx.Validation(map[string]string{"cols": "invalid terminal size"})
	}
	if wsp.Status != ws.StatusRunning {
		return httpx.Conflict("Start the workspace before opening a terminal.")
	}
	var reason *string
	if g.Emergency {
		rs := strings.TrimSpace(r.Header.Get(ws.EmergencyReasonHeader))
		reason = &rs
	}
	var sid uuid.UUID
	err = db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id=$1 FOR UPDATE`, p.UserID); err != nil {
			return err
		}
		var containerID *uuid.UUID
		if wsp.Type == "container" {
			var cid uuid.UUID
			var err error
			if in.ContainerID != nil {
				err = tx.QueryRow(ctx, `SELECT id FROM containers WHERE id=$1 AND workspace_id=$2 AND removed_at IS NULL AND status='running'`, *in.ContainerID, id).Scan(&cid)
			} else {
				err = tx.QueryRow(ctx, `SELECT id FROM containers WHERE workspace_id=$1 AND removed_at IS NULL AND status='running' ORDER BY is_primary DESC LIMIT 1`, id).Scan(&cid)
			}
			if db.IsNotFound(err) {
				return httpx.Conflict("No running container is available in this workspace.")
			}
			if err != nil {
				return err
			}
			containerID = &cid
		}
		quota, err := h.Svc.Resources.UserQuota(ctx, tx, p.UserID)
		if err != nil {
			return err
		}
		var open int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM terminal_sessions WHERE user_id=$1 AND status IN ('pending','active') AND expires_at > now()`, p.UserID).Scan(&open); err != nil {
			return err
		}
		if open >= quota.MaxTerminalSessions {
			return resources.QuotaError("You have reached the maximum number of open terminal sessions. Close one first.")
		}
		if err := tx.QueryRow(ctx, `INSERT INTO terminal_sessions (user_id, workspace_id, container_id, status, client_ip, expires_at, access_reason, cols, rows)
			VALUES ($1,$2,$3,'pending',$4,$5,$6,$7,$8) RETURNING id`, p.UserID, id, containerID, httpx.ClientIP(ctx),
			time.Now().Add(h.Cfg.TerminalSessionTTL), reason, in.Cols, in.Rows).Scan(&sid); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "terminal.open", ResourceType: "workspace", ResourceID: id.String(),
			Reason: deref(reason), Metadata: map[string]any{"terminal_session_id": sid, "container_id": containerID, "emergency": g.Emergency}})
	})
	if err != nil {
		return err
	}
	s, err := scan(h.Svc.Pool.QueryRow(ctx, selectSession+" WHERE t.id=$1", sid))
	if err != nil {
		return err
	}
	return httpx.Created(w, s)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// List handles GET /terminals (own open sessions).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	rows, err := h.Svc.Pool.Query(ctx, selectSession+` WHERE t.user_id=$1 AND (t.status IN ('pending','active') AND t.expires_at > now()
		OR t.ended_at > now() - interval '1 hour') ORDER BY t.created_at DESC LIMIT 50`, p.UserID)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		s, err := scan(rows)
		if err != nil {
			return err
		}
		out = append(out, s)
	}
	return httpx.OK(w, out)
}

// Close handles DELETE /terminals/{id}.
func (h *Handler) Close(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Terminal session")
	}
	tag, err := h.Svc.Pool.Exec(ctx, `UPDATE terminal_sessions SET status='closed', ended_at=now(), end_reason='closed_by_user'
		WHERE id=$1 AND user_id=$2 AND status IN ('pending','active')`, id, p.UserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.NotFound("Terminal session")
	}
	_ = audit.Record(ctx, h.Svc.Pool, audit.Entry{Action: "terminal.close", ResourceType: "terminal_session", ResourceID: id.String()})
	return httpx.NoContent(w)
}

// WebSocket handles GET /terminals/{id}/ws.
func (h *Handler) WebSocket(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can("terminals.open") {
		return httpx.Forbidden("")
	}
	sid, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Terminal session")
	}
	var (
		wsID        uuid.UUID
		containerID *uuid.UUID
		status      string
		expires     time.Time
		reason      *string
		cols, rows  int
	)
	err = h.Svc.Pool.QueryRow(ctx, `SELECT workspace_id, container_id, status, expires_at, access_reason, cols, rows FROM terminal_sessions
		WHERE id=$1 AND user_id=$2`, sid, p.UserID).Scan(&wsID, &containerID, &status, &expires, &reason, &cols, &rows)
	if db.IsNotFound(err) {
		return httpx.NotFound("Terminal session")
	}
	if err != nil {
		return err
	}
	if status == "closed" || status == "expired" || time.Now().After(expires) {
		return httpx.NewError(http.StatusGone, "terminal_session_ended", "This terminal session has ended. Open a new terminal.")
	}
	// Re-validate workspace access on every (re)connect. Emergency access
	// uses the reason captured when the session was created.
	if reason != nil {
		r.Header.Set(ws.EmergencyReasonHeader, *reason)
	}
	wsp, _, err := ws.Authorize(ctx, h.Svc.Pool, r, wsID, ws.LevelEdit)
	if err != nil {
		return err
	}
	if wsp.Status != ws.StatusRunning {
		return httpx.Conflict("The workspace is not running.")
	}
	attach := agentproto.TerminalAttach{WorkspaceID: wsID.String(), Cols: cols, Rows: rows,
		IdleTimeoutSec: int(h.Cfg.TerminalIdleTimeout.Seconds()), MaxDurationSec: int(time.Until(expires).Seconds())}
	if attach.MaxDurationSec < 60 {
		attach.MaxDurationSec = 60
	}
	if wsp.Type == "container" {
		if containerID == nil {
			return httpx.Conflict("The terminal session has no container.")
		}
		c, err := ws.ScanContainer(h.Svc.Pool.QueryRow(ctx, ws.SelectContainer+" WHERE c.id=$1 AND c.removed_at IS NULL", *containerID))
		if err != nil || c.Status != "running" || c.RuntimeID == nil {
			return httpx.Conflict("The container is not running.")
		}
		tpl, err := ws.GetTemplate(ctx, h.Svc.Pool, c.TemplateID)
		if err != nil {
			return err
		}
		attach.RuntimeID, attach.ContainerID, attach.RunAsUID = *c.RuntimeID, c.ID.String(), tpl.Security.RunAsUID
	} else {
		var nu agentproto.NativeUser
		if err := h.Svc.Agent.Do(ctx, agentproto.OpNativeUserEnsure, wsID.String(), "resolve native terminal user",
			agentproto.NativeUserEnsure{WorkspaceID: wsID.String(), LinuxUser: ws.LinuxUser(wsID)}, &nu); err != nil {
			return agent.MapError(err)
		}
		attach.LinuxUser, attach.RunAsUID = ws.LinuxUser(wsID), nu.UID
		attach.CPUMillis, attach.MemoryBytes, attach.PidsLimit = wsp.CPUMillis, wsp.MemoryBytes, wsp.PidsLimit
	}

	origin, _ := url.Parse(h.Cfg.BaseURL)
	client, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{origin.Host}})
	if err != nil {
		return nil // Accept already wrote the HTTP error
	}
	client.SetReadLimit(64 << 10)
	log := logging.From(ctx)

	actx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	upstream, err := h.Svc.Agent.Client.Terminal(actx, h.Svc.Agent.Call(ctx, agentproto.OpTerminalAttach, sid.String(), "browser terminal", attach))
	if err != nil {
		log.Warn("terminal attach failed", "error", err)
		msg := "Could not open terminal"
		var ae *agentproto.Error
		if errors.As(err, &ae) {
			msg = ae.Message
		} else if errors.Is(err, agentproto.ErrUnavailable) {
			msg = "Host agent unavailable"
		}
		_ = client.Close(websocket.StatusTryAgainLater, truncate(msg, 120))
		return nil
	}
	upstream.SetReadLimit(1 << 20)
	_, _ = h.Svc.Pool.Exec(ctx, `UPDATE terminal_sessions SET status='active', connected_at=now(), last_activity_at=now() WHERE id=$1`, sid)
	ws.Touch(ctx, h.Svc.Pool, wsID)

	endReason := h.relay(actx, cancel, client, upstream, sid, wsID, auth.SessionFrom(ctx).ID, expires)
	_ = upstream.Close(websocket.StatusNormalClosure, "")
	newStatus := "pending"
	if endReason == "expired" {
		newStatus = "expired"
	} else if endReason == "session_closed" || endReason == "shell_exited" || endReason == "login_session_ended" {
		newStatus = "closed"
	}
	_, _ = h.Svc.Pool.Exec(context.WithoutCancel(ctx), `UPDATE terminal_sessions SET status=CASE WHEN status='closed' THEN 'closed' ELSE $2 END,
		ended_at=CASE WHEN $2 IN ('closed','expired') THEN now() ELSE ended_at END,
		end_reason=CASE WHEN $2 IN ('closed','expired') THEN coalesce(end_reason,$3) ELSE end_reason END WHERE id=$1`, sid, newStatus, endReason)
	_ = client.Close(websocket.StatusNormalClosure, endReason)
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// relay pipes frames between browser and agent and periodically re-checks
// that the terminal session, login session and workspace are still valid.
func (h *Handler) relay(ctx context.Context, cancel context.CancelFunc, client, upstream *websocket.Conn, sid, wsID, loginSession uuid.UUID, expires time.Time) string {
	done := make(chan string, 3)
	activity := make(chan struct{}, 1)
	go func() { // browser -> agent
		for {
			typ, data, err := client.Read(ctx)
			if err != nil {
				done <- "client_disconnected"
				return
			}
			if typ == websocket.MessageText {
				var c agentproto.TerminalControl
				if json.Unmarshal(data, &c) != nil || (c.Type != "resize" && c.Type != "ping") {
					continue
				}
				if c.Type == "ping" {
					continue
				}
			}
			select {
			case activity <- struct{}{}:
			default:
			}
			if err := upstream.Write(ctx, typ, data); err != nil {
				done <- "shell_exited"
				return
			}
		}
	}()
	go func() { // agent -> browser
		for {
			typ, data, err := upstream.Read(ctx)
			if err != nil {
				done <- "shell_exited"
				return
			}
			if err := client.Write(ctx, typ, data); err != nil {
				done <- "client_disconnected"
				return
			}
		}
	}()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	deadline := time.NewTimer(time.Until(expires))
	defer deadline.Stop()
	for {
		select {
		case reason := <-done:
			cancel()
			return reason
		case <-deadline.C:
			cancel()
			return "expired"
		case <-activity:
		case <-tick.C:
			var status string
			var revoked bool
			var wsStatus string
			err := h.Svc.Pool.QueryRow(ctx, `SELECT t.status, w.status,
				NOT EXISTS (SELECT 1 FROM sessions s WHERE s.id=$2 AND s.revoked_at IS NULL AND s.expires_at > now())
				FROM terminal_sessions t JOIN workspaces w ON w.id=t.workspace_id WHERE t.id=$1`, sid, loginSession).Scan(&status, &wsStatus, &revoked)
			if err != nil {
				continue
			}
			if status == "closed" {
				cancel()
				return "session_closed"
			}
			if revoked {
				cancel()
				return "login_session_ended"
			}
			if wsStatus != ws.StatusRunning {
				cancel()
				return "workspace_stopped"
			}
			_, _ = h.Svc.Pool.Exec(ctx, `UPDATE terminal_sessions SET last_activity_at=now() WHERE id=$1`, sid)
			ws.Touch(ctx, h.Svc.Pool, wsID)
		}
	}
}

// ExpireSessions marks expired sessions (run periodically by the worker).
func ExpireSessions(ctx context.Context, q db.Querier) error {
	_, err := q.Exec(ctx, `UPDATE terminal_sessions SET status='expired', ended_at=now(), end_reason=coalesce(end_reason,'expired')
		WHERE status IN ('pending','active') AND expires_at < now()`)
	return err
}
