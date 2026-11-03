// Package databases provisions PostgreSQL databases for projects. Each
// project database gets its own database and login role (no superuser, no
// CREATEDB/CREATEROLE), CONNECT is revoked from PUBLIC, and the generated
// password is encrypted at rest and only shown when created or reset.
//
// Isolation model: separate databases and roles on a shared PostgreSQL
// instance. This isolates data and privileges but not CPU/IO resources; see
// docs/adr/0006-project-database-isolation.md.
package databases

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/modules/notifications"
	"github.com/itk-cloud/itk-cloud/internal/modules/projects"
	"github.com/itk-cloud/itk-cloud/internal/modules/resources"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/config"
	"github.com/itk-cloud/itk-cloud/internal/platform/crypto"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
	"github.com/itk-cloud/itk-cloud/internal/platform/jobs"
)

// Job kinds.
const (
	JobProvision = "database.provision"
	JobDelete    = "database.delete"
)

// Database is the API representation (never includes the password).
type Database struct {
	ID               uuid.UUID  `json:"id"`
	ProjectID        uuid.UUID  `json:"project_id"`
	ProjectName      string     `json:"project_name"`
	CreatedBy        uuid.UUID  `json:"created_by"`
	CreatedByName    string     `json:"created_by_username"`
	DisplayName      string     `json:"display_name"`
	DBName           string     `json:"db_name"`
	RoleName         string     `json:"role_name"`
	Engine           string     `json:"engine"`
	Status           string     `json:"status"`
	StatusReason     *string    `json:"status_reason"`
	SizeBytes        *int64     `json:"size_bytes"`
	ConnectionLimit  int        `json:"connection_limit"`
	LastBackupAt     *time.Time `json:"last_backup_at"`
	LastBackupStatus *string    `json:"last_backup_status"`
	Host             string     `json:"host"`
	Port             int        `json:"port"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

const selectDB = `SELECT d.id, d.project_id, p.name, d.created_by, u.username, d.display_name, d.db_name, d.role_name, d.engine, d.status,
	d.status_reason, d.size_bytes, d.connection_limit, d.last_backup_at, d.last_backup_status, d.created_at, d.updated_at
	FROM databases d JOIN projects p ON p.id=d.project_id JOIN users u ON u.id=d.created_by`

// Service implements database provisioning.
type Service struct {
	Pool      *pgxpool.Pool
	Cfg       *config.Config
	Box       *crypto.SecretBox
	Resources *resources.Service

	once  sync.Once
	admin *pgxpool.Pool
	err   error
}

func (s *Service) scan(row pgx.Row) (Database, error) {
	var d Database
	err := row.Scan(&d.ID, &d.ProjectID, &d.ProjectName, &d.CreatedBy, &d.CreatedByName, &d.DisplayName, &d.DBName, &d.RoleName, &d.Engine,
		&d.Status, &d.StatusReason, &d.SizeBytes, &d.ConnectionLimit, &d.LastBackupAt, &d.LastBackupStatus, &d.CreatedAt, &d.UpdatedAt)
	d.Host, d.Port = s.Cfg.ProjectDBHost, s.Cfg.ProjectDBPort
	return d, err
}

// AdminPool returns the administrative connection to the project database
// server (lazily connected).
func (s *Service) AdminPool(ctx context.Context) (*pgxpool.Pool, error) {
	if !s.Cfg.ProjectDatabasesEnabled() {
		return nil, httpx.Unavailable("Project databases are not configured on this server (PROJECT_DB_ADMIN_URL).")
	}
	s.once.Do(func() { s.admin, s.err = db.Connect(ctx, s.Cfg.ProjectDBAdminURL) })
	return s.admin, s.err
}

var identRe = regexp.MustCompile(`^itk_[a-z0-9_]{3,55}$`)

func ident(name string) (string, error) {
	if !identRe.MatchString(name) {
		return "", errors.New("unsafe identifier")
	}
	return pgx.Identifier{name}.Sanitize(), nil
}

func literal(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Handler serves /databases.
type Handler struct{ Svc *Service }

// List handles GET /databases (?project_id=).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	args := []any{p.UserID}
	where := []string{"d.status <> 'deleted'"}
	if !p.CanAny("databases.manage_all", "projects.view_all", "projects.manage_all") {
		where = append(where, "EXISTS (SELECT 1 FROM project_members m WHERE m.project_id=d.project_id AND m.user_id=$1)")
	} else {
		where = append(where, "$1::uuid IS NOT NULL")
	}
	if v := r.URL.Query().Get("project_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return httpx.Validation(map[string]string{"project_id": "must be a UUID"})
		}
		args = append(args, id)
		where = append(where, "d.project_id=$"+strconv.Itoa(len(args)))
	}
	rows, err := h.Svc.Pool.Query(ctx, selectDB+" WHERE "+strings.Join(where, " AND ")+" ORDER BY p.name, d.display_name", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []Database{}
	for rows.Next() {
		d, err := h.Svc.scan(rows)
		if err != nil {
			return err
		}
		out = append(out, d)
	}
	return httpx.OK(w, map[string]any{"databases": out, "enabled": h.Svc.Cfg.ProjectDatabasesEnabled()})
}

func (h *Handler) load(r *http.Request, role string) (*Database, projects.Access, error) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return nil, projects.Access{}, httpx.NotFound("Database")
	}
	d, err := h.Svc.scan(h.Svc.Pool.QueryRow(ctx, selectDB+" WHERE d.id=$1 AND d.status <> 'deleted'", id))
	if db.IsNotFound(err) {
		return nil, projects.Access{}, httpx.NotFound("Database")
	}
	if err != nil {
		return nil, projects.Access{}, err
	}
	p := authz.FromContext(ctx)
	if p.Can("databases.manage_all") {
		return &d, projects.Access{ManageAll: true}, nil
	}
	_, a, err := projects.Authorize(ctx, h.Svc.Pool, d.ProjectID, role)
	if err != nil {
		if e, ok := err.(*httpx.Error); ok && e.Status == http.StatusNotFound {
			return nil, a, httpx.NotFound("Database")
		}
		return nil, a, err
	}
	return &d, a, nil
}

// GetOne handles GET /databases/{id}.
func (h *Handler) GetOne(w http.ResponseWriter, r *http.Request) error {
	d, _, err := h.load(r, "member")
	if err != nil {
		return err
	}
	return httpx.OK(w, d)
}

type createInput struct {
	ProjectID       uuid.UUID `json:"project_id"`
	DisplayName     string    `json:"display_name"`
	ConnectionLimit *int      `json:"connection_limit"`
}

var nameClean = regexp.MustCompile(`[^a-z0-9_]+`)

// Create handles POST /databases.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.CanAny("databases.create", "databases.manage_all") {
		return httpx.Forbidden("")
	}
	if !h.Svc.Cfg.ProjectDatabasesEnabled() {
		return httpx.Unavailable("Project databases are not configured on this server.")
	}
	var in createInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if in.DisplayName == "" || len(in.DisplayName) > 80 {
		return httpx.Validation(map[string]string{"display_name": "is required (max 80 characters)"})
	}
	limit := 20
	if in.ConnectionLimit != nil {
		if *in.ConnectionLimit < 1 || *in.ConnectionLimit > 200 {
			return httpx.Validation(map[string]string{"connection_limit": "must be between 1 and 200"})
		}
		limit = *in.ConnectionLimit
	}
	pr, _, err := projects.Authorize(ctx, h.Svc.Pool, in.ProjectID, "maintainer")
	if err != nil {
		return err
	}
	if pr.Status != "active" {
		return httpx.Conflict("The project is archived.")
	}
	base := nameClean.ReplaceAllString(strings.ReplaceAll(strings.ToLower(pr.Slug+"_"+in.DisplayName), "-", "_"), "_")
	base = strings.Trim(base, "_")
	if len(base) > 40 {
		base = base[:40]
	}
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	dbName := "itk_" + base + "_" + suffix
	id := uuid.New()
	pw := crypto.RandomPassword(24)
	enc, err := h.Svc.Box.Seal([]byte(pw), []byte("db:"+id.String()))
	if err != nil {
		return err
	}
	err = db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM projects WHERE id=$1 FOR UPDATE`, pr.ID); err != nil {
			return err
		}
		quota, err := h.Svc.Resources.UserQuota(ctx, tx, p.UserID)
		if err != nil {
			return err
		}
		usage, err := h.Svc.Resources.UserUsage(ctx, tx, p.UserID)
		if err != nil {
			return err
		}
		if usage.Databases >= quota.MaxDatabases {
			return resources.QuotaError(fmt.Sprintf("Database limit reached (%d of %d).", usage.Databases, quota.MaxDatabases))
		}
		if pq, err := h.Svc.Resources.ProjectQuota(ctx, tx, pr.ID); err != nil {
			return err
		} else if pq != nil {
			pu, err := h.Svc.Resources.ProjectUsage(ctx, tx, pr.ID)
			if err != nil {
				return err
			}
			if pu.Databases >= pq.MaxDatabases {
				return resources.QuotaError("The project's database quota is exhausted.")
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO databases (id, project_id, created_by, display_name, db_name, role_name, connection_limit, password_encrypted)
			VALUES ($1,$2,$3,$4,$5,$5,$6,$7)`, id, pr.ID, p.UserID, in.DisplayName, dbName, limit, enc); err != nil {
			return err
		}
		if _, err := jobs.Enqueue(ctx, tx, JobProvision, map[string]any{"database_id": id}, jobs.Options{
			IdempotencyKey: JobProvision + ":" + id.String(), ResourceType: "database", ResourceID: id.String(), CreatedBy: &p.UserID}); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "database.create", ResourceType: "database", ResourceID: id.String(),
			Metadata: map[string]any{"project_id": pr.ID, "db_name": dbName}})
	})
	if err != nil {
		return err
	}
	d, err := h.Svc.scan(h.Svc.Pool.QueryRow(ctx, selectDB+" WHERE d.id=$1", id))
	if err != nil {
		return err
	}
	// The password is returned exactly once; it is stored encrypted only so
	// the provisioning job can apply it.
	return httpx.Accepted(w, map[string]any{"database": d, "credentials": h.Svc.credentials(&d, pw)})
}

// ResetCredentials handles POST /databases/{id}/credentials/reset. The new
// password is returned once.
func (h *Handler) ResetCredentials(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	d, _, err := h.load(r, "maintainer")
	if err != nil {
		return err
	}
	if d.Status != "ready" {
		return httpx.Conflict("The database is not ready.")
	}
	pw, err := h.Svc.setPassword(ctx, d)
	if err != nil {
		return err
	}
	_ = audit.Record(ctx, h.Svc.Pool, audit.Entry{Action: "database.credentials_reset", ResourceType: "database", ResourceID: d.ID.String()})
	return httpx.OK(w, h.Svc.credentials(d, pw))
}

// Credentials returned once to the caller.
type Credentials struct {
	Host             string `json:"host"`
	Port             int    `json:"port"`
	Database         string `json:"database"`
	Username         string `json:"username"`
	Password         string `json:"password"`
	ConnectionString string `json:"connection_string"`
}

func (s *Service) credentials(d *Database, pw string) Credentials {
	return Credentials{Host: s.Cfg.ProjectDBHost, Port: s.Cfg.ProjectDBPort, Database: d.DBName, Username: d.RoleName, Password: pw,
		ConnectionString: fmt.Sprintf("postgresql://%s:%s@%s:%d/%s?sslmode=prefer", d.RoleName, pw, s.Cfg.ProjectDBHost, s.Cfg.ProjectDBPort, d.DBName)}
}

func (s *Service) setPassword(ctx context.Context, d *Database) (string, error) {
	admin, err := s.AdminPool(ctx)
	if err != nil {
		return "", err
	}
	role, err := ident(d.RoleName)
	if err != nil {
		return "", err
	}
	pw := crypto.RandomPassword(24)
	if _, err := admin.Exec(ctx, "ALTER ROLE "+role+" WITH PASSWORD "+literal(pw)); err != nil {
		return "", err
	}
	enc, err := s.Box.Seal([]byte(pw), []byte("db:"+d.ID.String()))
	if err != nil {
		return "", err
	}
	_, err = s.Pool.Exec(ctx, `UPDATE databases SET password_encrypted=$2, updated_at=now() WHERE id=$1`, d.ID, enc)
	return pw, err
}

// Delete handles DELETE /databases/{id}.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	d, _, err := h.load(r, "maintainer")
	if err != nil {
		return err
	}
	if !p.CanAny("databases.delete", "databases.manage_all") {
		return httpx.Forbidden("")
	}
	if r.URL.Query().Get("confirm") != d.DBName {
		return httpx.Validation(map[string]string{"confirm": "type the database name to confirm deletion"})
	}
	err = db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE databases SET status='deleting', updated_at=now() WHERE id=$1 AND status IN ('ready','failed')`, d.ID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.Conflict("The database cannot be deleted in its current state.")
		}
		if _, err := jobs.Enqueue(ctx, tx, JobDelete, map[string]any{"database_id": d.ID}, jobs.Options{
			IdempotencyKey: JobDelete + ":" + d.ID.String(), ResourceType: "database", ResourceID: d.ID.String(), CreatedBy: &p.UserID}); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "database.delete", ResourceType: "database", ResourceID: d.ID.String(),
			Metadata: map[string]any{"db_name": d.DBName, "project_id": d.ProjectID}})
	})
	if err != nil {
		return err
	}
	return httpx.NoContent(w)
}

// RegisterJobs registers database jobs.
func (s *Service) RegisterJobs(r *jobs.Runner) {
	r.Register(JobProvision, s.jobProvision)
	r.Register(JobDelete, s.jobDelete)
}

type jobPayload struct {
	DatabaseID uuid.UUID `json:"database_id"`
}

func (s *Service) jobProvision(ctx context.Context, j jobs.Job) error {
	var p jobPayload
	if err := j.Decode(&p); err != nil {
		return jobs.Permanent(err)
	}
	d, err := s.scan(s.Pool.QueryRow(ctx, selectDB+" WHERE d.id=$1", p.DatabaseID))
	if err != nil {
		return jobs.Permanent(err)
	}
	if d.Status != "provisioning" {
		return nil
	}
	admin, err := s.AdminPool(ctx)
	if err != nil {
		return err
	}
	role, err := ident(d.RoleName)
	if err != nil {
		return jobs.Permanent(err)
	}
	dbn, err := ident(d.DBName)
	if err != nil {
		return jobs.Permanent(err)
	}
	var encPW []byte
	if err := s.Pool.QueryRow(ctx, `SELECT password_encrypted FROM databases WHERE id=$1`, d.ID).Scan(&encPW); err != nil {
		return err
	}
	pwb, err := s.Box.Open(encPW, []byte("db:"+d.ID.String()))
	if err != nil {
		return jobs.Permanent(fmt.Errorf("decrypt generated password: %w", err))
	}
	pw := string(pwb)
	var exists bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, d.RoleName).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err := admin.Exec(ctx, "CREATE ROLE "+role+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT "+
			strconv.Itoa(d.ConnectionLimit)+" PASSWORD "+literal(pw)); err != nil {
			return err
		}
	} else if _, err := admin.Exec(ctx, "ALTER ROLE "+role+" WITH PASSWORD "+literal(pw)); err != nil {
		return err
	}
	if err := admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)`, d.DBName).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		// CREATE DATABASE cannot run inside a transaction block.
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbn+" OWNER "+role+" ENCODING 'UTF8'"); err != nil {
			return err
		}
	}
	for _, stmt := range []string{
		"REVOKE ALL ON DATABASE " + dbn + " FROM PUBLIC",
		"GRANT CONNECT, TEMPORARY ON DATABASE " + dbn + " TO " + role,
	} {
		if _, err := admin.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE databases SET status='ready', status_reason=NULL, updated_at=now() WHERE id=$1`, d.ID); err != nil {
		return err
	}
	_ = audit.Record(ctx, s.Pool, audit.Entry{Action: "database.provisioned", ResourceType: "database", ResourceID: d.ID.String(), ActorUsername: "system:worker"})
	_ = notifications.Notify(ctx, s.Pool, d.CreatedBy, notifications.Message{Type: notifications.TypeSystem, Severity: "success",
		Title: "Database ready: " + d.DisplayName, Body: "The database is ready to accept connections.",
		Link: "/databases/" + d.ID.String()})
	return nil
}

func (s *Service) jobDelete(ctx context.Context, j jobs.Job) error {
	var p jobPayload
	if err := j.Decode(&p); err != nil {
		return jobs.Permanent(err)
	}
	d, err := s.scan(s.Pool.QueryRow(ctx, selectDB+" WHERE d.id=$1", p.DatabaseID))
	if err != nil {
		return jobs.Permanent(err)
	}
	if d.Status != "deleting" {
		return nil
	}
	admin, err := s.AdminPool(ctx)
	if err != nil {
		return err
	}
	role, err := ident(d.RoleName)
	if err != nil {
		return jobs.Permanent(err)
	}
	dbn, err := ident(d.DBName)
	if err != nil {
		return jobs.Permanent(err)
	}
	if _, err := admin.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid <> pg_backend_pid()`, d.DBName); err != nil {
		return err
	}
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+dbn); err != nil {
		return err
	}
	if _, err := admin.Exec(ctx, "DROP ROLE IF EXISTS "+role); err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `UPDATE databases SET status='deleted', deleted_at=now(), password_encrypted=NULL, updated_at=now() WHERE id=$1`, d.ID)
	if err == nil {
		_ = audit.Record(ctx, s.Pool, audit.Entry{Action: "database.deleted", ResourceType: "database", ResourceID: d.ID.String(), ActorUsername: "system:worker"})
	}
	return err
}

// OnDead marks a database as failed after its job exhausted retries.
func (s *Service) OnDead(ctx context.Context, j jobs.Job, err error) {
	var p jobPayload
	if j.Decode(&p) != nil {
		return
	}
	msg := err.Error()
	if len(msg) > 300 {
		msg = msg[:300]
	}
	_, _ = s.Pool.Exec(ctx, `UPDATE databases SET status='failed', status_reason=$2, updated_at=now() WHERE id=$1`, p.DatabaseID, msg)
}

// RefreshSizes records database sizes (periodic).
func (s *Service) RefreshSizes(ctx context.Context) error {
	if !s.Cfg.ProjectDatabasesEnabled() {
		return nil
	}
	admin, err := s.AdminPool(ctx)
	if err != nil {
		return err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, db_name FROM databases WHERE status='ready'`)
	if err != nil {
		return err
	}
	type item struct {
		id   uuid.UUID
		name string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.name); err != nil {
			rows.Close()
			return err
		}
		items = append(items, it)
	}
	rows.Close()
	for _, it := range items {
		var size int64
		if err := admin.QueryRow(ctx, `SELECT pg_database_size($1)`, it.name).Scan(&size); err == nil {
			_, _ = s.Pool.Exec(ctx, `UPDATE databases SET size_bytes=$2 WHERE id=$1`, it.id, size)
		}
	}
	return nil
}
