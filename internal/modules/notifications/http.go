package notifications

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
)

// Notification is the API representation.
type Notification struct {
	ID        uuid.UUID  `json:"id"`
	Type      string     `json:"type"`
	Severity  string     `json:"severity"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Link      *string    `json:"link"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// Handler serves notification endpoints.
type Handler struct{ Pool *pgxpool.Pool }

// List handles GET /notifications (?unread=true&type=).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	page, err := httpx.ParsePage(r, []string{"created_at"}, "-created_at")
	if err != nil {
		return err
	}
	where := "user_id=$1"
	args := []any{p.UserID}
	if r.URL.Query().Get("unread") == "true" {
		where += " AND read_at IS NULL"
	}
	if t := r.URL.Query().Get("type"); t != "" {
		args = append(args, t)
		where += " AND type=$2"
	}
	var total, unread int
	if err := h.Pool.QueryRow(ctx, "SELECT count(*) FROM notifications WHERE "+where, args...).Scan(&total); err != nil {
		return err
	}
	if err := h.Pool.QueryRow(ctx, "SELECT count(*) FROM notifications WHERE user_id=$1 AND read_at IS NULL", p.UserID).Scan(&unread); err != nil {
		return err
	}
	n := len(args)
	args = append(args, page.PerPage, page.Offset())
	rows, err := h.Pool.Query(ctx, "SELECT id, type, severity, title, body, link, read_at, created_at FROM notifications WHERE "+where+
		" ORDER BY created_at DESC LIMIT $"+itoa(n+1)+" OFFSET $"+itoa(n+2), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var x Notification
		if err := rows.Scan(&x.ID, &x.Type, &x.Severity, &x.Title, &x.Body, &x.Link, &x.ReadAt, &x.CreatedAt); err != nil {
			return err
		}
		out = append(out, x)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": out, "meta": map[string]any{"page": page.Page, "per_page": page.PerPage, "total": total, "unread": unread}})
	return nil
}

func itoa(n int) string { return strconv.Itoa(n) }

// UnreadCount handles GET /notifications/unread-count.
func (h *Handler) UnreadCount(w http.ResponseWriter, r *http.Request) error {
	var n int
	if err := h.Pool.QueryRow(r.Context(), `SELECT count(*) FROM notifications WHERE user_id=$1 AND read_at IS NULL`, authz.FromContext(r.Context()).UserID).Scan(&n); err != nil {
		return err
	}
	return httpx.OK(w, map[string]int{"unread": n})
}

// MarkRead handles POST /notifications/{id}/read.
func (h *Handler) MarkRead(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Notification")
	}
	tag, err := h.Pool.Exec(r.Context(), `UPDATE notifications SET read_at=coalesce(read_at, now()) WHERE id=$1 AND user_id=$2`, id, authz.FromContext(r.Context()).UserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.NotFound("Notification")
	}
	return httpx.NoContent(w)
}

// MarkAllRead handles POST /notifications/read-all.
func (h *Handler) MarkAllRead(w http.ResponseWriter, r *http.Request) error {
	if _, err := h.Pool.Exec(r.Context(), `UPDATE notifications SET read_at=now() WHERE user_id=$1 AND read_at IS NULL`, authz.FromContext(r.Context()).UserID); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

// Announcement is an administrator announcement.
type Announcement struct {
	ID        uuid.UUID  `json:"id"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Severity  string     `json:"severity"`
	StartsAt  time.Time  `json:"starts_at"`
	EndsAt    *time.Time `json:"ends_at"`
	CreatedBy *string    `json:"created_by_username"`
	CreatedAt time.Time  `json:"created_at"`
}

// Announcements handles GET /announcements (active ones; all for managers with ?all=true).
func (h *Handler) Announcements(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	cond := "a.starts_at <= now() AND (a.ends_at IS NULL OR a.ends_at > now())"
	if r.URL.Query().Get("all") == "true" && authz.FromContext(ctx).Can("announcements.manage") {
		cond = "true"
	}
	rows, err := h.Pool.Query(ctx, `SELECT a.id, a.title, a.body, a.severity, a.starts_at, a.ends_at, u.username, a.created_at
		FROM announcements a LEFT JOIN users u ON u.id=a.created_by WHERE `+cond+` ORDER BY a.starts_at DESC LIMIT 50`)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []Announcement{}
	for rows.Next() {
		var a Announcement
		if err := rows.Scan(&a.ID, &a.Title, &a.Body, &a.Severity, &a.StartsAt, &a.EndsAt, &a.CreatedBy, &a.CreatedAt); err != nil {
			return err
		}
		out = append(out, a)
	}
	return httpx.OK(w, out)
}

type announcementInput struct {
	Title    string     `json:"title"`
	Body     string     `json:"body"`
	Severity string     `json:"severity"`
	StartsAt *time.Time `json:"starts_at"`
	EndsAt   *time.Time `json:"ends_at"`
	Notify   bool       `json:"notify"`
}

// CreateAnnouncement handles POST /announcements.
func (h *Handler) CreateAnnouncement(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can("announcements.manage") {
		return httpx.Forbidden("")
	}
	var in announcementInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	in.Title, in.Body = strings.TrimSpace(in.Title), strings.TrimSpace(in.Body)
	d := map[string]string{}
	if in.Title == "" || len(in.Title) > 200 {
		d["title"] = "is required (max 200 characters)"
	}
	if len(in.Body) > 5000 {
		d["body"] = "must be at most 5000 characters"
	}
	if in.Severity == "" {
		in.Severity = "info"
	}
	if in.Severity != "info" && in.Severity != "warning" && in.Severity != "critical" {
		d["severity"] = "must be info, warning or critical"
	}
	starts := time.Now()
	if in.StartsAt != nil {
		starts = *in.StartsAt
	}
	if in.EndsAt != nil && !in.EndsAt.After(starts) {
		d["ends_at"] = "must be after starts_at"
	}
	if len(d) > 0 {
		return httpx.Validation(d)
	}
	var id uuid.UUID
	err := db.WithTx(ctx, h.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO announcements (title, body, severity, starts_at, ends_at, created_by) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
			in.Title, in.Body, in.Severity, starts, in.EndsAt, p.UserID).Scan(&id); err != nil {
			return err
		}
		if in.Notify {
			if err := NotifyAll(ctx, tx, Message{Type: TypeMaintenance, Severity: in.Severity, Title: in.Title, Body: in.Body, Link: "/notifications"}); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "announcement.create", ResourceType: "announcement", ResourceID: id.String(),
			Metadata: map[string]any{"title": in.Title, "notify": in.Notify}})
	})
	if err != nil {
		return err
	}
	return httpx.Created(w, map[string]any{"id": id})
}

// DeleteAnnouncement handles DELETE /announcements/{id}.
func (h *Handler) DeleteAnnouncement(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).Can("announcements.manage") {
		return httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Announcement")
	}
	tag, err := h.Pool.Exec(ctx, `DELETE FROM announcements WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.NotFound("Announcement")
	}
	_ = audit.Record(ctx, h.Pool, audit.Entry{Action: "announcement.delete", ResourceType: "announcement", ResourceID: id.String()})
	return httpx.NoContent(w)
}
