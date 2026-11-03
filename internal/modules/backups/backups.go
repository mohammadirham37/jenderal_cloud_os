// Package backups creates encrypted backups (age, X25519 recipient) of the
// platform database, configuration, workspace metadata, workspace files and
// project databases; applies retention; copies backups to a second location;
// and performs authorised restores. The age private key is never stored by
// the platform: it is supplied for each restore and only kept (encrypted)
// in the restore job until it finishes.
package backups

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itk-cloud/itk-cloud/internal/modules/agent"
	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/modules/databases"
	"github.com/itk-cloud/itk-cloud/internal/modules/notifications"
	"github.com/itk-cloud/itk-cloud/internal/modules/settings"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/config"
	"github.com/itk-cloud/itk-cloud/internal/platform/crypto"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
	"github.com/itk-cloud/itk-cloud/internal/platform/jobs"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// Job kinds.
const (
	JobRun     = "backup.run"
	JobRestore = "backup.restore"
)

// Kinds of backups.
var Kinds = []string{"platform_db", "config", "workspace_metadata", "workspace_files", "project_databases"}

// Service implements backups.
type Service struct {
	Pool     *pgxpool.Pool
	Cfg      *config.Config
	Settings *settings.Store
	Agent    *agent.Agent
	Box      *crypto.SecretBox
	DB       *databases.Service
}

// Job is a backup job record.
type Job struct {
	ID             uuid.UUID  `json:"id"`
	Kind           string     `json:"kind"`
	Status         string     `json:"status"`
	Trigger        string     `json:"trigger"`
	FileName       *string    `json:"file_name"`
	SizeBytes      *int64     `json:"size_bytes"`
	ChecksumSHA256 *string    `json:"checksum_sha256"`
	Encrypted      bool       `json:"encrypted"`
	Error          *string    `json:"error"`
	TriggeredBy    *string    `json:"triggered_by_username"`
	StartedAt      *time.Time `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at"`
	ExpiresAt      *time.Time `json:"expires_at"`
	RestoreStatus  *string    `json:"restore_status"`
	RestoredAt     *time.Time `json:"restored_at"`
	RestoreError   *string    `json:"restore_error"`
	CreatedAt      time.Time  `json:"created_at"`
}

const selectJob = `SELECT b.id, b.kind, b.status, b.trigger, b.file_name, b.size_bytes, b.checksum_sha256, b.encrypted, b.error, u.username,
	b.started_at, b.finished_at, b.expires_at, b.restore_status, b.restored_at, b.restore_error, b.created_at
	FROM backup_jobs b LEFT JOIN users u ON u.id=b.triggered_by`

func scan(row pgx.Row) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.Kind, &j.Status, &j.Trigger, &j.FileName, &j.SizeBytes, &j.ChecksumSHA256, &j.Encrypted, &j.Error, &j.TriggeredBy,
		&j.StartedAt, &j.FinishedAt, &j.ExpiresAt, &j.RestoreStatus, &j.RestoredAt, &j.RestoreError, &j.CreatedAt)
	return j, err
}

// Enqueue creates backup job rows for kind ("full" expands to all kinds).
func (s *Service) Enqueue(ctx context.Context, tx pgx.Tx, kind, trigger string, by *uuid.UUID, idemPrefix string) ([]uuid.UUID, error) {
	kinds := []string{kind}
	if kind == "full" {
		kinds = Kinds
		if trigger == "scheduled" && !s.Settings.Bool(ctx, "backup.include_workspace_files") {
			kinds = []string{"platform_db", "config", "workspace_metadata", "project_databases"}
		}
	}
	ids := []uuid.UUID{}
	for _, k := range kinds {
		if k == "project_databases" && !s.Cfg.ProjectDatabasesEnabled() {
			continue
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO backup_jobs (kind, trigger, triggered_by) VALUES ($1,$2,$3) RETURNING id`, k, trigger, by).Scan(&id); err != nil {
			return nil, err
		}
		key := ""
		if idemPrefix != "" {
			key = idemPrefix + ":" + k
		}
		if _, err := jobs.Enqueue(ctx, tx, JobRun, map[string]any{"backup_id": id}, jobs.Options{IdempotencyKey: key,
			ResourceType: "backup", ResourceID: id.String(), CreatedBy: by, MaxAttempts: 3}); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// ScheduleDaily enqueues the daily scheduled backup once per day at the
// configured hour.
func (s *Service) ScheduleDaily(ctx context.Context, now time.Time) error {
	if !s.Settings.Bool(ctx, "backup.schedule_enabled") || s.Cfg.BackupAgeRecipient == "" {
		return nil
	}
	if now.UTC().Hour() != s.Settings.Int(ctx, "backup.schedule_hour_utc") {
		return nil
	}
	day := now.UTC().Format("2006-01-02")
	var exists bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM background_jobs WHERE idempotency_key LIKE $1)`, "backup.scheduled:"+day+":%").Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	return db.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		_, err := s.Enqueue(ctx, tx, "full", "scheduled", nil, "backup.scheduled:"+day)
		return err
	})
}

// RegisterJobs registers backup jobs.
func (s *Service) RegisterJobs(r *jobs.Runner) {
	r.Register(JobRun, s.jobRun)
	r.Register(JobRestore, s.jobRestore)
}

type runPayload struct {
	BackupID uuid.UUID `json:"backup_id"`
}

func (s *Service) recipient() (age.Recipient, error) {
	if s.Cfg.BackupAgeRecipient == "" {
		return nil, jobs.Permanent(errors.New("BACKUP_AGE_RECIPIENT is not configured; unencrypted backups are not allowed"))
	}
	r, err := age.ParseX25519Recipient(s.Cfg.BackupAgeRecipient)
	if err != nil {
		return nil, jobs.Permanent(fmt.Errorf("invalid BACKUP_AGE_RECIPIENT: %w", err))
	}
	return r, nil
}

// RunNow creates and executes a backup synchronously (used by itkctl for
// pre-upgrade backups). It records the result in backup_jobs.
func (s *Service) RunNow(ctx context.Context, kind, trigger string) (Job, error) {
	var id uuid.UUID
	if err := s.Pool.QueryRow(ctx, `INSERT INTO backup_jobs (kind, trigger) VALUES ($1,$2) RETURNING id`, kind, trigger).Scan(&id); err != nil {
		return Job{}, err
	}
	payload, _ := json.Marshal(runPayload{BackupID: id})
	err := s.jobRun(ctx, jobs.Job{ID: uuid.New(), Kind: JobRun, Payload: payload, Attempts: 1, MaxAttempts: 1})
	b, serr := scan(s.Pool.QueryRow(ctx, selectJob+" WHERE b.id=$1", id))
	if serr != nil {
		return Job{}, serr
	}
	return b, err
}

func (s *Service) jobRun(ctx context.Context, j jobs.Job) error {
	var p runPayload
	if err := j.Decode(&p); err != nil {
		return jobs.Permanent(err)
	}
	b, err := scan(s.Pool.QueryRow(ctx, selectJob+" WHERE b.id=$1", p.BackupID))
	if err != nil {
		return jobs.Permanent(err)
	}
	if b.Status == "succeeded" {
		return nil
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE backup_jobs SET status='running', started_at=now(), error=NULL WHERE id=$1`, b.ID); err != nil {
		return err
	}
	ts := time.Now().UTC().Format("20060102T150405Z")
	name := fmt.Sprintf("itk-%s-%s-%s", strings.ReplaceAll(b.Kind, "_", "-"), ts, b.ID.String()[:8])
	var res agentproto.BackupResult
	switch b.Kind {
	case "platform_db":
		res, err = s.dumpPlatformDB(ctx, name+".dump.age")
	case "config":
		res, err = s.archiveConfig(ctx, name+".tar.gz.age")
	case "workspace_metadata":
		res, err = s.exportMetadata(ctx, name+".json.age")
	case "workspace_files":
		res, err = s.workspaceFiles(ctx, name+".tar.gz.age")
	case "project_databases":
		res, err = s.dumpProjectDBs(ctx, name+".tar.age")
	default:
		err = jobs.Permanent(fmt.Errorf("unknown backup kind %q", b.Kind))
	}
	if err != nil {
		msg := err.Error()
		if len(msg) > 500 {
			msg = msg[:500]
		}
		_, _ = s.Pool.Exec(ctx, `UPDATE backup_jobs SET status='failed', error=$2, finished_at=now() WHERE id=$1`, b.ID, msg)
		if j.Attempts >= j.MaxAttempts || isPermanent(err) {
			_ = notifications.NotifyPermission(ctx, s.Pool, "backup.manage", notifications.Message{Type: notifications.TypeBackup, Severity: "critical",
				Title: "Backup failed: " + b.Kind, Body: msg, Link: "/admin/backups"})
		}
		return err
	}
	if s.Cfg.BackupCopyDir != "" && b.Kind != "workspace_files" {
		if err := copyFile(filepath.Join(s.Cfg.BackupRoot, res.FileName), filepath.Join(s.Cfg.BackupCopyDir, res.FileName)); err != nil {
			msg := "backup created but copy to BACKUP_COPY_DIR failed: " + err.Error()
			_, _ = s.Pool.Exec(ctx, `UPDATE backup_jobs SET error=$2 WHERE id=$1`, b.ID, msg)
		}
	}
	days := s.Cfg.BackupRetentionDays
	_, err = s.Pool.Exec(ctx, `UPDATE backup_jobs SET status='succeeded', file_name=$2, size_bytes=$3, checksum_sha256=$4, encrypted=true,
		finished_at=now(), expires_at=now() + make_interval(days => $5) WHERE id=$1`, b.ID, res.FileName, res.Size, res.SHA256, days)
	if err == nil {
		_ = audit.Record(ctx, s.Pool, audit.Entry{Action: "backup.completed", ResourceType: "backup", ResourceID: b.ID.String(), ActorUsername: "system:worker",
			Metadata: map[string]any{"kind": b.Kind, "file": res.FileName, "size": res.Size}})
	}
	return err
}

func isPermanent(err error) bool {
	return strings.Contains(fmt.Sprintf("%T", errors.Unwrap(err)), "permanent") || strings.Contains(err.Error(), "not configured")
}

// encryptedWriter creates dst (0600) and returns an age writer plus a
// finish function that closes everything and reports size/checksum.
func (s *Service) encryptedWriter(fileName string) (io.Writer, func() (agentproto.BackupResult, error), func(), error) {
	rcpt, err := s.recipient()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := os.MkdirAll(s.Cfg.BackupRoot, 0o770); err != nil {
		return nil, nil, nil, err
	}
	dst := filepath.Join(s.Cfg.BackupRoot, fileName)
	tmp := dst + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, nil, err
	}
	h := sha256.New()
	cw := &countWriter{w: io.MultiWriter(f, h)}
	enc, err := age.Encrypt(cw, rcpt)
	if err != nil {
		f.Close()
		os.Remove(tmp)
		return nil, nil, nil, err
	}
	abort := func() { f.Close(); os.Remove(tmp) }
	finish := func() (agentproto.BackupResult, error) {
		if err := enc.Close(); err != nil {
			abort()
			return agentproto.BackupResult{}, err
		}
		if err := f.Sync(); err != nil {
			abort()
			return agentproto.BackupResult{}, err
		}
		if err := f.Close(); err != nil {
			os.Remove(tmp)
			return agentproto.BackupResult{}, err
		}
		if err := os.Rename(tmp, dst); err != nil {
			return agentproto.BackupResult{}, err
		}
		return agentproto.BackupResult{FileName: fileName, Size: cw.n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
	}
	return enc, finish, abort, nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// pgEnv converts a connection URL into libpq environment variables so that
// credentials never appear on the command line.
func pgEnv(url string) ([]string, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cc := cfg.ConnConfig
	env := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LC_ALL=C",
		"PGHOST=" + cc.Host, fmt.Sprintf("PGPORT=%d", cc.Port), "PGUSER=" + cc.User, "PGDATABASE=" + cc.Database,
		"PGCONNECT_TIMEOUT=10"}
	if cc.Password != "" {
		env = append(env, "PGPASSWORD="+cc.Password)
	}
	if v, ok := cc.RuntimeParams["sslmode"]; ok {
		env = append(env, "PGSSLMODE="+v)
	} else if cc.TLSConfig == nil {
		env = append(env, "PGSSLMODE=disable")
	}
	return env, nil
}

func pgTool(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	for _, dir := range []string{"/usr/lib/postgresql/17/bin", "/usr/lib/postgresql/16/bin", "/usr/lib/postgresql/18/bin"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", jobs.Permanent(fmt.Errorf("%s not found; install postgresql-client", name))
}

func (s *Service) dumpPlatformDB(ctx context.Context, fileName string) (agentproto.BackupResult, error) {
	tool, err := pgTool("pg_dump")
	if err != nil {
		return agentproto.BackupResult{}, err
	}
	env, err := pgEnv(s.Cfg.DatabaseURL)
	if err != nil {
		return agentproto.BackupResult{}, jobs.Permanent(err)
	}
	w, finish, abort, err := s.encryptedWriter(fileName)
	if err != nil {
		return agentproto.BackupResult{}, err
	}
	cmd := exec.CommandContext(ctx, tool, "--format=custom", "--no-owner", "--no-privileges", "--compress=6")
	cmd.Env = env
	cmd.Stdout = w
	var stderr strings.Builder
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
	if err := cmd.Run(); err != nil {
		abort()
		return agentproto.BackupResult{}, fmt.Errorf("pg_dump failed: %v: %s", err, stderr.String())
	}
	return finish()
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	q := p
	if len(q) > l.n {
		q = q[:l.n]
	}
	l.n -= len(q)
	_, _ = l.w.Write(q)
	return len(p), nil
}

func (s *Service) archiveConfig(_ context.Context, fileName string) (agentproto.BackupResult, error) {
	if _, err := os.Stat(s.Cfg.ConfigDir); err != nil {
		return agentproto.BackupResult{}, jobs.Permanent(fmt.Errorf("config directory %s is not accessible", s.Cfg.ConfigDir))
	}
	w, finish, abort, err := s.encryptedWriter(fileName)
	if err != nil {
		return agentproto.BackupResult{}, err
	}
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	skipped := []string{}
	err = filepath.WalkDir(s.Cfg.ConfigDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(s.Cfg.ConfigDir, p)
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return nil
			}
			hdr, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			hdr.Name = filepath.ToSlash(filepath.Join("config", rel)) + "/"
			return tw.WriteHeader(hdr)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		// Open before writing the header so an unreadable file (e.g. the
		// root-only agent.env when running as the service user) is skipped
		// cleanly instead of corrupting the archive.
		f, err := os.Open(p)
		if err != nil {
			skipped = append(skipped, rel)
			return nil
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(filepath.Join("config", rel))
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err = io.CopyN(tw, f, info.Size())
		return err
	})
	if err == nil && len(skipped) > 0 {
		note := strings.Join(skipped, "\n") + "\n"
		if err = tw.WriteHeader(&tar.Header{Name: "config/SKIPPED-UNREADABLE.txt", Mode: 0o600, Size: int64(len(note)), ModTime: time.Now()}); err == nil {
			_, err = tw.Write([]byte(note))
		}
	}
	if err == nil {
		err = tw.Close()
	}
	if err == nil {
		err = gz.Close()
	}
	if err != nil {
		abort()
		return agentproto.BackupResult{}, err
	}
	return finish()
}

func (s *Service) exportMetadata(ctx context.Context, fileName string) (agentproto.BackupResult, error) {
	tables := []string{"resource_quotas", "container_images", "workspace_templates", "projects", "project_members", "workspaces",
		"workspace_members", "containers", "gpu_devices", "gpu_allocations", "databases"}
	out := map[string]json.RawMessage{"exported_at": json.RawMessage(fmt.Sprintf("%q", time.Now().UTC().Format(time.RFC3339)))}
	for _, t := range tables {
		var raw []byte
		// Table names come from the fixed list above.
		q := "SELECT coalesce(json_agg(t), '[]'::json) FROM " + pgx.Identifier{t}.Sanitize() + " t"
		if t == "databases" {
			q = "SELECT coalesce(json_agg(t), '[]'::json) FROM (SELECT id, project_id, display_name, db_name, role_name, status, created_at FROM databases) t"
		}
		if err := s.Pool.QueryRow(ctx, q).Scan(&raw); err != nil {
			return agentproto.BackupResult{}, err
		}
		out[t] = raw
	}
	w, finish, abort, err := s.encryptedWriter(fileName)
	if err != nil {
		return agentproto.BackupResult{}, err
	}
	if err := json.NewEncoder(w).Encode(out); err != nil {
		abort()
		return agentproto.BackupResult{}, err
	}
	return finish()
}

func (s *Service) workspaceFiles(ctx context.Context, fileName string) (agentproto.BackupResult, error) {
	if s.Cfg.BackupAgeRecipient == "" {
		return agentproto.BackupResult{}, jobs.Permanent(errors.New("BACKUP_AGE_RECIPIENT is not configured"))
	}
	rows, err := s.Pool.Query(ctx, `SELECT id FROM workspaces WHERE status IN ('ready','running','stopped','suspended')`)
	if err != nil {
		return agentproto.BackupResult{}, err
	}
	ids := []string{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return agentproto.BackupResult{}, err
		}
		ids = append(ids, id.String())
	}
	rows.Close()
	if len(ids) == 0 {
		return agentproto.BackupResult{}, jobs.Permanent(errors.New("there are no workspaces to back up"))
	}
	var res agentproto.BackupResult
	err = s.Agent.Do(authz.WithPrincipal(ctx, authz.System()), agentproto.OpBackupWorkspace, "backup", "backup workspace files",
		agentproto.BackupWorkspace{WorkspaceIDs: ids, FileName: fileName, AgeRecipient: s.Cfg.BackupAgeRecipient}, &res)
	return res, err
}

func (s *Service) dumpProjectDBs(ctx context.Context, fileName string) (agentproto.BackupResult, error) {
	if !s.Cfg.ProjectDatabasesEnabled() {
		return agentproto.BackupResult{}, jobs.Permanent(errors.New("project databases are not configured"))
	}
	tool, err := pgTool("pg_dump")
	if err != nil {
		return agentproto.BackupResult{}, err
	}
	env, err := pgEnv(s.Cfg.ProjectDBAdminURL)
	if err != nil {
		return agentproto.BackupResult{}, jobs.Permanent(err)
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, db_name FROM databases WHERE status='ready' ORDER BY db_name`)
	if err != nil {
		return agentproto.BackupResult{}, err
	}
	type dbi struct {
		id   uuid.UUID
		name string
	}
	var list []dbi
	for rows.Next() {
		var d dbi
		if err := rows.Scan(&d.id, &d.name); err != nil {
			rows.Close()
			return agentproto.BackupResult{}, err
		}
		list = append(list, d)
	}
	rows.Close()
	w, finish, abort, err := s.encryptedWriter(fileName)
	if err != nil {
		return agentproto.BackupResult{}, err
	}
	tw := tar.NewWriter(w)
	tmpDir, err := os.MkdirTemp("", "itk-pgdump-")
	if err != nil {
		abort()
		return agentproto.BackupResult{}, err
	}
	defer os.RemoveAll(tmpDir)
	for _, d := range list {
		// Dump to a private temp file first (tar needs the size), then
		// stream it into the encrypted archive and delete it.
		tmp := filepath.Join(tmpDir, d.name+".dump")
		cmd := exec.CommandContext(ctx, tool, "--format=custom", "--no-owner", "--no-privileges", "--file="+tmp, "--dbname="+d.name)
		cmd.Env = env
		var stderr strings.Builder
		cmd.Stderr = &limitedWriter{w: &stderr, n: 2048}
		status := "succeeded"
		if err := cmd.Run(); err != nil {
			status = "failed"
			_, _ = s.Pool.Exec(ctx, `UPDATE databases SET last_backup_status='failed' WHERE id=$1`, d.id)
			abort()
			return agentproto.BackupResult{}, fmt.Errorf("pg_dump %s failed: %v: %s", d.name, err, stderr.String())
		}
		info, err := os.Stat(tmp)
		if err != nil {
			abort()
			return agentproto.BackupResult{}, err
		}
		if err := tw.WriteHeader(&tar.Header{Name: d.name + ".dump", Mode: 0o600, Size: info.Size(), ModTime: time.Now()}); err != nil {
			abort()
			return agentproto.BackupResult{}, err
		}
		f, err := os.Open(tmp)
		if err != nil {
			abort()
			return agentproto.BackupResult{}, err
		}
		_, err = io.Copy(tw, f)
		f.Close()
		os.Remove(tmp)
		if err != nil {
			abort()
			return agentproto.BackupResult{}, err
		}
		_, _ = s.Pool.Exec(ctx, `UPDATE databases SET last_backup_at=now(), last_backup_status=$2 WHERE id=$1`, d.id, status)
	}
	if err := tw.Close(); err != nil {
		abort()
		return agentproto.BackupResult{}, err
	}
	return finish()
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".partial"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// ApplyRetention deletes expired backup files.
func (s *Service) ApplyRetention(ctx context.Context) (int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, file_name FROM backup_jobs WHERE status='succeeded' AND expires_at < now()`)
	if err != nil {
		return 0, err
	}
	type item struct {
		id   uuid.UUID
		name *string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.name); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, it)
	}
	rows.Close()
	for _, it := range items {
		if it.name != nil && safeName(*it.name) {
			for _, dir := range []string{s.Cfg.BackupRoot, s.Cfg.BackupCopyDir} {
				if dir != "" {
					if err := os.Remove(filepath.Join(dir, *it.name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
						return 0, err
					}
				}
			}
		}
		if _, err := s.Pool.Exec(ctx, `UPDATE backup_jobs SET status='expired' WHERE id=$1`, it.id); err != nil {
			return 0, err
		}
	}
	// Purge audit logs past retention (append-only trigger allows this only
	// with the explicit session flag inside this transaction).
	days := s.Settings.Int(ctx, "audit.retention_days")
	err = db.WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('itk.audit_retention', 'on', true)`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM audit_logs WHERE occurred_at < now() - make_interval(days => $1)`, days)
		return err
	})
	return len(items), err
}

func safeName(n string) bool {
	return n != "" && !strings.ContainsAny(n, "/\\") && !strings.Contains(n, "..")
}

// ---------------------------------------------------------------------------
// Restore
// ---------------------------------------------------------------------------

type restorePayload struct {
	BackupID          uuid.UUID  `json:"backup_id"`
	Mode              string     `json:"mode"` // restore | verify
	WorkspaceID       *uuid.UUID `json:"workspace_id,omitempty"`
	DatabaseID        *uuid.UUID `json:"database_id,omitempty"`
	IdentityEncrypted string     `json:"identity_encrypted,omitempty"`
}

func (s *Service) jobRestore(ctx context.Context, j jobs.Job) error {
	var p restorePayload
	if err := j.Decode(&p); err != nil {
		return jobs.Permanent(err)
	}
	// Always scrub the identity from the job payload when done.
	defer func() {
		_, _ = s.Pool.Exec(context.WithoutCancel(ctx), `UPDATE background_jobs SET payload = payload - 'identity_encrypted' WHERE id=$1 AND status <> 'queued'`, j.ID)
	}()
	b, err := scan(s.Pool.QueryRow(ctx, selectJob+" WHERE b.id=$1", p.BackupID))
	if err != nil {
		return jobs.Permanent(err)
	}
	if p.IdentityEncrypted == "" {
		return jobs.Permanent(errors.New("identity missing"))
	}
	raw, err := base64.StdEncoding.DecodeString(p.IdentityEncrypted)
	if err != nil {
		return jobs.Permanent(err)
	}
	identBytes, err := s.Box.Open(raw, []byte("restore:"+j.ID.String()))
	if err != nil {
		return jobs.Permanent(errors.New("cannot decrypt restore identity"))
	}
	identity := string(identBytes)
	_, _ = s.Pool.Exec(ctx, `UPDATE backup_jobs SET restore_status='running', restore_error=NULL WHERE id=$1`, b.ID)
	var detail string
	switch b.Kind {
	case "workspace_files":
		req := agentproto.RestoreWorkspace{FileName: *b.FileName, AgeIdentity: identity}
		if p.WorkspaceID != nil {
			req.WorkspaceID = p.WorkspaceID.String()
		}
		var out map[string]int
		err = s.Agent.Do(authz.WithPrincipal(ctx, authz.System()), agentproto.OpRestoreWorkspace, b.ID.String(), "restore workspace files", req, &out)
		detail = fmt.Sprintf("%d files restored", out["files_restored"])
	case "project_databases":
		detail, err = s.restoreProjectDB(ctx, &b, identity, p.DatabaseID)
	case "platform_db":
		detail, err = s.verifyPlatformDump(ctx, &b, identity)
	default:
		err = s.verifyDecrypt(&b, identity)
		detail = "archive decrypted and checksum verified"
	}
	if err != nil {
		msg := err.Error()
		if len(msg) > 500 {
			msg = msg[:500]
		}
		_, _ = s.Pool.Exec(ctx, `UPDATE backup_jobs SET restore_status='failed', restore_error=$2 WHERE id=$1`, b.ID, msg)
		return jobs.Permanent(err)
	}
	_, err = s.Pool.Exec(ctx, `UPDATE backup_jobs SET restore_status='succeeded', restored_at=now(), restore_error=NULL WHERE id=$1`, b.ID)
	_ = audit.Record(ctx, s.Pool, audit.Entry{Action: "backup.restore_completed", ResourceType: "backup", ResourceID: b.ID.String(),
		ActorUsername: "system:worker", Metadata: map[string]any{"kind": b.Kind, "mode": p.Mode, "detail": detail}})
	return err
}

// openDecrypted opens and decrypts a backup file after checking its checksum.
func (s *Service) openDecrypted(b *Job, identity string) (io.Reader, io.Closer, error) {
	if b.FileName == nil || !safeName(*b.FileName) {
		return nil, nil, errors.New("backup file name missing")
	}
	path := filepath.Join(s.Cfg.BackupRoot, *b.FileName)
	if b.ChecksumSHA256 != nil {
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, fmt.Errorf("backup file not found: %w", err)
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return nil, nil, err
		}
		if hex.EncodeToString(h.Sum(nil)) != *b.ChecksumSHA256 {
			return nil, nil, errors.New("backup checksum mismatch: the file is corrupted or was modified")
		}
	}
	id, err := age.ParseX25519Identity(strings.TrimSpace(identity))
	if err != nil {
		return nil, nil, errors.New("invalid age identity")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	r, err := age.Decrypt(f, id)
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("decrypt failed (wrong key?): %w", err)
	}
	return r, f, nil
}

func (s *Service) verifyDecrypt(b *Job, identity string) error {
	r, c, err := s.openDecrypted(b, identity)
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = io.Copy(io.Discard, r)
	return err
}

// verifyPlatformDump decrypts the platform dump and validates it with
// pg_restore --list. The live platform database is never overwritten from
// the web UI; see docs/backup-restore.md for the CLI procedure.
func (s *Service) verifyPlatformDump(ctx context.Context, b *Job, identity string) (string, error) {
	tool, err := pgTool("pg_restore")
	if err != nil {
		return "", err
	}
	r, c, err := s.openDecrypted(b, identity)
	if err != nil {
		return "", err
	}
	defer c.Close()
	tmp, err := os.CreateTemp("", "itk-verify-*.dump")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return "", err
	}
	tmp.Close()
	out, err := exec.CommandContext(ctx, tool, "--list", tmp.Name()).Output()
	if err != nil {
		return "", fmt.Errorf("pg_restore --list failed: %w", err)
	}
	tables := strings.Count(string(out), " TABLE DATA ")
	if tables == 0 {
		return "", errors.New("the dump contains no table data")
	}
	return fmt.Sprintf("dump verified: %d tables with data", tables), nil
}

func (s *Service) restoreProjectDB(ctx context.Context, b *Job, identity string, dbID *uuid.UUID) (string, error) {
	if dbID == nil {
		return "", errors.New("database_id is required to restore a project database")
	}
	var name, role, status string
	if err := s.Pool.QueryRow(ctx, `SELECT db_name, role_name, status FROM databases WHERE id=$1`, *dbID).Scan(&name, &role, &status); err != nil {
		return "", errors.New("database not found")
	}
	if status != "ready" {
		return "", errors.New("the target database is not ready")
	}
	tool, err := pgTool("pg_restore")
	if err != nil {
		return "", err
	}
	env, err := pgEnv(s.Cfg.ProjectDBAdminURL)
	if err != nil {
		return "", err
	}
	r, c, err := s.openDecrypted(b, identity)
	if err != nil {
		return "", err
	}
	defer c.Close()
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return "", fmt.Errorf("database %s is not contained in this backup", name)
		}
		if err != nil {
			return "", err
		}
		if hdr.Name != name+".dump" {
			continue
		}
		cmd := exec.CommandContext(ctx, tool, "--clean", "--if-exists", "--no-owner", "--no-privileges", "--role="+role, "--dbname="+name)
		cmd.Env = env
		cmd.Stdin = tr
		var stderr strings.Builder
		cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("pg_restore failed: %v: %s", err, stderr.String())
		}
		return "database " + name + " restored", nil
	}
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

// Handler serves /admin/backups.
type Handler struct{ Svc *Service }

// List handles GET /admin/backups.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if !authz.FromContext(ctx).Can("backup.manage") {
		return httpx.Forbidden("")
	}
	page, err := httpx.ParsePage(r, []string{"created_at"}, "-created_at")
	if err != nil {
		return err
	}
	var total int
	if err := h.Svc.Pool.QueryRow(ctx, `SELECT count(*) FROM backup_jobs`).Scan(&total); err != nil {
		return err
	}
	rows, err := h.Svc.Pool.Query(ctx, selectJob+" ORDER BY b.created_at DESC LIMIT $1 OFFSET $2", page.PerPage, page.Offset())
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scan(rows)
		if err != nil {
			return err
		}
		out = append(out, j)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": out, "meta": page.Meta(total), "config": map[string]any{
		"encryption_configured":   h.Svc.Cfg.BackupAgeRecipient != "",
		"offsite_copy_configured": h.Svc.Cfg.BackupCopyDir != "",
		"retention_days":          h.Svc.Cfg.BackupRetentionDays,
		"schedule_enabled":        h.Svc.Settings.Bool(ctx, "backup.schedule_enabled"),
		"schedule_hour_utc":       h.Svc.Settings.Int(ctx, "backup.schedule_hour_utc"),
		"project_databases":       h.Svc.Cfg.ProjectDatabasesEnabled(),
	}})
	return nil
}

// Create handles POST /admin/backups {kind}.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can("backup.manage") {
		return httpx.Forbidden("")
	}
	var in struct {
		Kind string `json:"kind"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	valid := in.Kind == "full"
	for _, k := range Kinds {
		valid = valid || k == in.Kind
	}
	if !valid {
		return httpx.Validation(map[string]string{"kind": "must be full or one of " + strings.Join(Kinds, ", ")})
	}
	if h.Svc.Cfg.BackupAgeRecipient == "" {
		return httpx.Conflict("Backups require an encryption key: set BACKUP_AGE_RECIPIENT (see docs/backup-restore.md).")
	}
	if in.Kind == "project_databases" && !h.Svc.Cfg.ProjectDatabasesEnabled() {
		return httpx.Validation(map[string]string{"kind": "project databases are not configured"})
	}
	var ids []uuid.UUID
	err := db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		var err error
		ids, err = h.Svc.Enqueue(ctx, tx, in.Kind, "manual", &p.UserID, "")
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "backup.create", ResourceType: "backup", Metadata: map[string]any{"kind": in.Kind, "jobs": ids}})
	})
	if err != nil {
		return err
	}
	return httpx.Accepted(w, map[string]any{"backup_ids": ids})
}

type restoreInput struct {
	Confirm     string     `json:"confirm"`
	AgeIdentity string     `json:"age_identity"`
	Mode        string     `json:"mode"`
	WorkspaceID *uuid.UUID `json:"workspace_id"`
	DatabaseID  *uuid.UUID `json:"database_id"`
	Reason      string     `json:"reason"`
}

// Restore handles POST /admin/backups/{id}/restore.
func (h *Handler) Restore(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := authz.FromContext(ctx)
	if !p.Can("backup.manage") {
		return httpx.Forbidden("")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Backup")
	}
	var in restoreInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	b, err := scan(h.Svc.Pool.QueryRow(ctx, selectJob+" WHERE b.id=$1", id))
	if db.IsNotFound(err) {
		return httpx.NotFound("Backup")
	}
	if err != nil {
		return err
	}
	if b.Status != "succeeded" || b.FileName == nil {
		return httpx.Conflict("Only successful, unexpired backups can be restored.")
	}
	d := map[string]string{}
	if in.Confirm != *b.FileName {
		d["confirm"] = "type the backup file name to confirm"
	}
	if !strings.HasPrefix(strings.TrimSpace(in.AgeIdentity), "AGE-SECRET-KEY-") {
		d["age_identity"] = "the age private key (AGE-SECRET-KEY-...) is required"
	}
	if len(strings.TrimSpace(in.Reason)) < 5 {
		d["reason"] = "a reason is required"
	}
	if in.Mode == "" {
		in.Mode = "restore"
	}
	switch b.Kind {
	case "platform_db", "config", "workspace_metadata":
		in.Mode = "verify" // live restore of these is CLI-only
	case "project_databases":
		if in.Mode == "restore" && in.DatabaseID == nil {
			d["database_id"] = "select the project database to restore"
		}
	}
	if len(d) > 0 {
		return httpx.Validation(d)
	}
	if b.RestoreStatus != nil && (*b.RestoreStatus == "queued" || *b.RestoreStatus == "running") {
		return httpx.Conflict("A restore of this backup is already in progress.")
	}
	err = db.WithTx(ctx, h.Svc.Pool, func(tx pgx.Tx) error {
		jobID := uuid.New()
		enc, err := h.Svc.Box.Seal([]byte(strings.TrimSpace(in.AgeIdentity)), []byte("restore:"+jobID.String()))
		if err != nil {
			return err
		}
		payload := restorePayload{BackupID: b.ID, Mode: in.Mode, WorkspaceID: in.WorkspaceID, DatabaseID: in.DatabaseID,
			IdentityEncrypted: base64.StdEncoding.EncodeToString(enc)}
		body, _ := json.Marshal(payload)
		if _, err := tx.Exec(ctx, `INSERT INTO background_jobs (id, kind, payload, max_attempts, resource_type, resource_id, created_by)
			VALUES ($1,$2,$3,1,'backup',$4,$5)`, jobID, JobRestore, body, b.ID.String(), p.UserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE backup_jobs SET restore_status='queued', restored_by=$2, restore_error=NULL WHERE id=$1`, b.ID, p.UserID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "backup.restore", ResourceType: "backup", ResourceID: b.ID.String(), Reason: in.Reason,
			Metadata: map[string]any{"kind": b.Kind, "mode": in.Mode, "workspace_id": in.WorkspaceID, "database_id": in.DatabaseID}})
	})
	if err != nil {
		return err
	}
	b, err = scan(h.Svc.Pool.QueryRow(ctx, selectJob+" WHERE b.id=$1", id))
	if err != nil {
		return err
	}
	return httpx.Accepted(w, b)
}
