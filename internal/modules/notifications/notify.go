// Package notifications stores in-app notifications and administrator
// announcements.
package notifications

import (
	"context"

	"github.com/google/uuid"

	"github.com/itk-cloud/itk-cloud/internal/platform/db"
)

// Notification types.
const (
	TypeSystem      = "system"
	TypeWorkspace   = "workspace"
	TypeGPU         = "gpu"
	TypeQuota       = "quota"
	TypeMaintenance = "maintenance"
	TypeSecurity    = "security"
	TypeBackup      = "backup"
	TypeAlert       = "alert"
)

// Message is a notification to deliver.
type Message struct {
	Type     string
	Severity string // info | success | warning | critical
	Title    string
	Body     string
	Link     string
}

// Notify stores a notification for one user.
func Notify(ctx context.Context, q db.Querier, userID uuid.UUID, m Message) error {
	if m.Severity == "" {
		m.Severity = "info"
	}
	var link *string
	if m.Link != "" {
		link = &m.Link
	}
	_, err := q.Exec(ctx, `INSERT INTO notifications (user_id, type, severity, title, body, link) VALUES ($1,$2,$3,$4,$5,$6)`,
		userID, m.Type, m.Severity, m.Title, m.Body, link)
	return err
}

// NotifyPermission notifies every active user holding a permission.
func NotifyPermission(ctx context.Context, q db.Querier, perm string, m Message) error {
	if m.Severity == "" {
		m.Severity = "info"
	}
	var link *string
	if m.Link != "" {
		link = &m.Link
	}
	_, err := q.Exec(ctx, `INSERT INTO notifications (user_id, type, severity, title, body, link)
		SELECT DISTINCT u.id, $2, $3, $4, $5, $6 FROM users u JOIN user_roles ur ON ur.user_id=u.id
		JOIN role_permissions rp ON rp.role_id=ur.role_id WHERE rp.permission_key=$1 AND u.status='active'`,
		perm, m.Type, m.Severity, m.Title, m.Body, link)
	return err
}

// NotifyAll notifies every active user.
func NotifyAll(ctx context.Context, q db.Querier, m Message) error {
	var link *string
	if m.Link != "" {
		link = &m.Link
	}
	_, err := q.Exec(ctx, `INSERT INTO notifications (user_id, type, severity, title, body, link)
		SELECT id, $1, $2, $3, $4, $5 FROM users WHERE status='active'`, m.Type, m.Severity, m.Title, m.Body, link)
	return err
}
