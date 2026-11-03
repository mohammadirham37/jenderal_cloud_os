// Package audit records append-only audit entries for administrative and
// sensitive actions and exposes them to authorised viewers.
package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
	"github.com/itk-cloud/itk-cloud/internal/platform/logging"
)

// Outcomes.
const (
	Success = "success"
	Failure = "failure"
	Denied  = "denied"
)

// Entry is a single audit record.
type Entry struct {
	Action       string
	ResourceType string
	ResourceID   string
	Outcome      string
	Reason       string
	Metadata     map[string]any
	// Actor overrides the principal from context (e.g. failed logins).
	ActorID       *uuid.UUID
	ActorUsername string
}

// Record writes an entry using q (pass a tx to make it atomic with the
// change being audited). Actor, IP, user agent and request ID are taken from
// the context.
func Record(ctx context.Context, q db.Querier, e Entry) error {
	if e.Outcome == "" {
		e.Outcome = Success
	}
	var actorID *uuid.UUID
	actorName := e.ActorUsername
	var ip, ua string
	if p := authz.FromContext(ctx); p != nil {
		if p.UserID != uuid.Nil {
			id := p.UserID
			actorID = &id
		}
		if actorName == "" {
			actorName = p.Username
		}
		ip, ua = p.IP, p.UserAgent
	}
	if e.ActorID != nil {
		actorID = e.ActorID
	}
	if ip == "" {
		ip = httpx.ClientIP(ctx)
	}
	if ua == "" {
		ua = userAgent(ctx)
	}
	meta := e.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_username, action, resource_type, resource_id, outcome, reason, ip, user_agent, request_id, metadata)
		VALUES ($1, NULLIF($2,''), $3, $4, $5, $6, NULLIF($7,''), NULLIF($8,''), NULLIF($9,''), NULLIF($10,''), $11)`,
		actorID, actorName, e.Action, e.ResourceType, e.ResourceID, e.Outcome, e.Reason, ip, truncate(ua, 300), httpx.RequestID(ctx), b)
	if err != nil {
		logging.From(ctx).Error("audit write failed", "action", e.Action, "error", err)
	}
	return err
}

type uaKey struct{}

// WithUserAgent stores the request user agent for audit entries.
func WithUserAgent(ctx context.Context, ua string) context.Context {
	return context.WithValue(ctx, uaKey{}, ua)
}

func userAgent(ctx context.Context) string {
	v, _ := ctx.Value(uaKey{}).(string)
	return v
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Log is an audit row returned by the API.
type Log struct {
	ID            int64          `json:"id"`
	OccurredAt    time.Time      `json:"occurred_at"`
	ActorID       *uuid.UUID     `json:"actor_id"`
	ActorUsername *string        `json:"actor_username"`
	Action        string         `json:"action"`
	ResourceType  string         `json:"resource_type"`
	ResourceID    string         `json:"resource_id"`
	Outcome       string         `json:"outcome"`
	Reason        *string        `json:"reason"`
	IP            *string        `json:"ip"`
	UserAgent     *string        `json:"user_agent"`
	RequestID     *string        `json:"request_id"`
	Metadata      map[string]any `json:"metadata"`
}

// Filter narrows audit queries.
type Filter struct {
	ActorID      *uuid.UUID
	Action       string
	ResourceType string
	ResourceID   string
	Outcome      string
	From, To     *time.Time
}

// Query lists audit logs, newest first.
func Query(ctx context.Context, q db.Querier, f Filter, page httpx.Page) ([]Log, int, error) {
	where := []string{"true"}
	args := []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	if f.ActorID != nil {
		add("actor_id = ?", *f.ActorID)
	}
	if f.Action != "" {
		add("action LIKE ?", strings.ReplaceAll(f.Action, "*", "%"))
	}
	if f.ResourceType != "" {
		add("resource_type = ?", f.ResourceType)
	}
	if f.ResourceID != "" {
		add("resource_id = ?", f.ResourceID)
	}
	if f.Outcome != "" {
		add("outcome = ?", f.Outcome)
	}
	if f.From != nil {
		add("occurred_at >= ?", *f.From)
	}
	if f.To != nil {
		add("occurred_at <= ?", *f.To)
	}
	if page.Search != "" {
		add("(action ILIKE ? OR actor_username ILIKE ? OR resource_id ILIKE ?)", db.LikePattern(page.Search))
	}
	w := strings.Join(where, " AND ")
	var total int
	if err := q.QueryRow(ctx, "SELECT count(*) FROM audit_logs WHERE "+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := page.OrderBy(map[string]string{"occurred_at": "occurred_at", "action": "action"})
	args = append(args, page.PerPage, page.Offset())
	rows, err := q.Query(ctx, `SELECT id, occurred_at, actor_id, actor_username, action, resource_type, resource_id, outcome,
		reason, ip, user_agent, request_id, metadata FROM audit_logs WHERE `+w+` ORDER BY `+order+`, id DESC
		LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Log{}
	for rows.Next() {
		var l Log
		if err := rows.Scan(&l.ID, &l.OccurredAt, &l.ActorID, &l.ActorUsername, &l.Action, &l.ResourceType, &l.ResourceID,
			&l.Outcome, &l.Reason, &l.IP, &l.UserAgent, &l.RequestID, &l.Metadata); err != nil {
			return nil, 0, err
		}
		out = append(out, l)
	}
	return out, total, rows.Err()
}

// Handler serves GET /admin/audit-logs.
type Handler struct{ Pool *pgxpool.Pool }

// List handles the audit log listing endpoint.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	p := authz.FromContext(r.Context())
	if !p.Can("audit.view") {
		return httpx.Forbidden("")
	}
	page, err := httpx.ParsePage(r, []string{"occurred_at", "action"}, "-occurred_at")
	if err != nil {
		return err
	}
	f, err := parseFilter(r)
	if err != nil {
		return err
	}
	logs, total, err := Query(r.Context(), h.Pool, f, page)
	if err != nil {
		return err
	}
	return httpx.List(w, logs, page.Meta(total))
}

func parseFilter(r *http.Request) (Filter, error) {
	q := r.URL.Query()
	var f Filter
	details := map[string]string{}
	if v := q.Get("actor_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			details["actor_id"] = "must be a UUID"
		} else {
			f.ActorID = &id
		}
	}
	f.Action = q.Get("action")
	f.ResourceType = q.Get("resource_type")
	f.ResourceID = q.Get("resource_id")
	if o := q.Get("outcome"); o != "" {
		if o != Success && o != Failure && o != Denied {
			details["outcome"] = "must be success, failure or denied"
		}
		f.Outcome = o
	}
	for key, dst := range map[string]**time.Time{"from": &f.From, "to": &f.To} {
		if v := q.Get(key); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				details[key] = "must be an RFC 3339 timestamp"
			} else {
				*dst = &t
			}
		}
	}
	if len(details) > 0 {
		return f, httpx.Validation(details)
	}
	return f, nil
}
