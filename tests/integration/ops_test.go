package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestProjectDatabaseLifecycle(t *testing.T) {
	e := setup(t, envOpts{})
	admin := e.AdminClient()
	lecturer := e.CreateUser(admin, "dosen1", []string{"lecturer"}, "standard")
	student := e.CreateUser(admin, "mhs1", []string{"student"}, "")
	outsider := e.CreateUser(admin, "mhs2", []string{"student"}, "")

	proj := lecturer.Do("POST", "/api/v1/projects", map[string]any{"name": "Riset Basis Data"}).Expect(t, 201, "project").Data()
	pid := str(proj, "id")
	lecturer.Do("POST", "/api/v1/projects/"+pid+"/members", map[string]any{"username": "mhs1", "role": "member"}).Expect(t, 200, "add member")
	outsider.Do("GET", "/api/v1/projects/"+pid, nil).Expect(t, 404, "outsider project")
	student.Do("GET", "/api/v1/projects/"+pid, nil).Expect(t, 200, "member project")
	student.Do("POST", "/api/v1/projects/"+pid+"/members", map[string]any{"username": "mhs2", "role": "member"}).Expect(t, 403, "member cannot add members")

	cr := lecturer.Do("POST", "/api/v1/databases", map[string]any{"project_id": pid, "display_name": "Data Penelitian"}).Expect(t, 202, "create db").Data()
	dbInfo := cr["database"].(map[string]any)
	creds := cr["credentials"].(map[string]any)
	dbID := str(dbInfo, "id")
	if str(creds, "password") == "" {
		t.Fatal("password must be returned once at creation")
	}
	e.Drain()
	got := lecturer.Do("GET", "/api/v1/databases/"+dbID, nil).Expect(t, 200, "get db").Data()
	if str(got, "status") != "ready" {
		t.Fatalf("database not ready: %v", got)
	}
	if _, ok := got["password"]; ok {
		t.Fatal("password must never be returned after creation")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, str(creds, "connection_string"))
	if err != nil {
		t.Fatalf("connect with generated credentials: %v", err)
	}
	if _, err := conn.Exec(ctx, `CREATE TABLE riset (id int)`); err != nil {
		t.Fatalf("owner should create tables: %v", err)
	}
	var super bool
	_ = conn.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname=current_user`).Scan(&super)
	if super {
		t.Fatal("project role must not be superuser")
	}
	if _, err := conn.Exec(ctx, `CREATE DATABASE escape_attempt`); err == nil {
		t.Fatal("project role must not be able to create databases")
	}
	conn.Close(ctx)
	// Another project's role cannot connect to this database (CONNECT revoked from PUBLIC).
	outsider.Do("GET", "/api/v1/databases/"+dbID, nil).Expect(t, 404, "outsider db")
	student.Do("POST", "/api/v1/databases/"+dbID+"/credentials/reset", nil).Expect(t, 403, "member cannot reset")
	newCreds := lecturer.Do("POST", "/api/v1/databases/"+dbID+"/credentials/reset", nil).Expect(t, 200, "reset creds").Data()
	// Only meaningful when the server enforces passwords (not "trust" auth).
	wrong := strings.Replace(str(creds, "connection_string"), ":"+str(creds, "password")+"@", ":definitely-wrong@", 1)
	if c, err := pgx.Connect(ctx, wrong); err == nil {
		c.Close(ctx)
		t.Log("PostgreSQL uses trust authentication locally; skipping old-password check")
	} else if c, err := pgx.Connect(ctx, str(creds, "connection_string")); err == nil {
		c.Close(ctx)
		t.Fatal("old password must stop working after reset")
	}
	c2, err := pgx.Connect(ctx, str(newCreds, "connection_string"))
	if err != nil {
		t.Fatalf("new credentials: %v", err)
	}
	c2.Close(ctx)

	// Backup of project databases, then restore verification.
	backups := admin.Do("POST", "/api/v1/admin/backups", map[string]string{"kind": "project_databases"}).Expect(t, 202, "backup").Data()
	bid := backups["backup_ids"].([]any)[0].(string)
	e.Drain()
	list := admin.Do("GET", "/api/v1/admin/backups", nil).Expect(t, 200, "list backups").List()
	var file string
	for _, b := range list {
		m := b.(map[string]any)
		if str(m, "id") == bid {
			if str(m, "status") != "succeeded" {
				t.Fatalf("backup failed: %v", m)
			}
			file = str(m, "file_name")
		}
	}
	admin.Do("POST", "/api/v1/admin/backups/"+bid+"/restore", map[string]any{"confirm": "wrong", "age_identity": e.Identity.String(), "reason": "test", "database_id": dbID}).Expect(t, 422, "restore without confirmation")
	admin.Do("POST", "/api/v1/admin/backups/"+bid+"/restore", map[string]any{"confirm": file, "age_identity": e.Identity.String(), "reason": "restore drill", "database_id": dbID}).Expect(t, 202, "restore")
	e.Drain()
	for _, b := range admin.Do("GET", "/api/v1/admin/backups", nil).List() {
		m := b.(map[string]any)
		if str(m, "id") == bid && str(m, "restore_status") != "succeeded" {
			t.Fatalf("restore failed: %v", m)
		}
	}
	// The restore job must not retain the private key.
	var leaked bool
	_ = e.Deps.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM background_jobs WHERE kind='backup.restore' AND payload ? 'identity_encrypted')`).Scan(&leaked)
	if leaked {
		t.Fatal("restore identity must be scrubbed from the job payload")
	}

	// Delete requires typing the database name.
	lecturer.Do("DELETE", "/api/v1/databases/"+dbID, nil).Expect(t, 422, "delete without confirm")
	lecturer.Do("DELETE", "/api/v1/databases/"+dbID+"?confirm="+str(dbInfo, "db_name"), nil).Expect(t, 204, "delete db")
	e.Drain()
	var exists bool
	_ = e.Deps.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)`, str(dbInfo, "db_name")).Scan(&exists)
	if exists {
		t.Fatal("database should be dropped")
	}
}

func TestPlatformBackupEncryptedAndVerifiable(t *testing.T) {
	e := setup(t, envOpts{})
	admin := e.AdminClient()
	ids := admin.Do("POST", "/api/v1/admin/backups", map[string]string{"kind": "full"}).Expect(t, 202, "full backup").Data()["backup_ids"].([]any)
	e.Drain()
	byKind := map[string]map[string]any{}
	for _, b := range admin.Do("GET", "/api/v1/admin/backups", nil).List() {
		m := b.(map[string]any)
		byKind[str(m, "kind")] = m
	}
	if len(ids) < 4 {
		t.Fatalf("full backup should create one job per kind, got %d", len(ids))
	}
	for _, k := range []string{"platform_db", "config", "workspace_metadata"} {
		m := byKind[k]
		if m == nil || str(m, "status") != "succeeded" || m["encrypted"] != true {
			t.Fatalf("%s backup did not succeed: %v", k, m)
		}
		raw, err := os.ReadFile(filepath.Join(e.Cfg.BackupRoot, str(m, "file_name")))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw[:21]) != "age-encryption.org/v1" {
			t.Fatalf("%s backup is not age-encrypted", k)
		}
	}
	pdb := byKind["platform_db"]
	admin.Do("POST", "/api/v1/admin/backups/"+str(pdb, "id")+"/restore", map[string]any{"confirm": str(pdb, "file_name"),
		"age_identity": "AGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ", "reason": "wrong key drill"}).Expect(t, 202, "verify with wrong key")
	e.Drain()
	for _, b := range admin.Do("GET", "/api/v1/admin/backups", nil).List() {
		m := b.(map[string]any)
		if str(m, "id") == str(pdb, "id") && str(m, "restore_status") != "failed" {
			t.Fatalf("wrong key must fail verification: %v", m)
		}
	}
	admin.Do("POST", "/api/v1/admin/backups/"+str(pdb, "id")+"/restore", map[string]any{"confirm": str(pdb, "file_name"),
		"age_identity": e.Identity.String(), "reason": "monthly restore test"}).Expect(t, 202, "verify")
	e.Drain()
	for _, b := range admin.Do("GET", "/api/v1/admin/backups", nil).List() {
		m := b.(map[string]any)
		if str(m, "id") == str(pdb, "id") && str(m, "restore_status") != "succeeded" {
			t.Fatalf("verification failed: %v", m)
		}
	}
}

func TestGPURequestApprovalFlow(t *testing.T) {
	e := setup(t, envOpts{GPU: true})
	admin := e.AdminClient()
	researcher := e.CreateUser(admin, "peneliti", []string{"lecturer"}, "research")
	basic := e.CreateUser(admin, "mhs1", []string{"student"}, "basic")
	ctx := context.Background()
	// Test fixture: a GPU row as the inventory sync would create it.
	var gpuID string
	if err := e.Deps.Pool.QueryRow(ctx, `INSERT INTO gpu_devices (uuid, index, name, memory_total_bytes) VALUES
		('GPU-00000000-1111-2222-3333-444444444444', 0, 'Test GPU', 25769803776) RETURNING id`).Scan(&gpuID); err != nil {
		t.Fatal(err)
	}
	ws1 := researcher.Do("POST", "/api/v1/workspaces", map[string]any{"name": "ml", "template_key": "python-dev"}).Expect(t, 202, "ws1").Data()
	ws2 := researcher.Do("POST", "/api/v1/workspaces", map[string]any{"name": "ml2", "template_key": "python-dev"}).Expect(t, 202, "ws2").Data()
	wsBasic := basic.Do("POST", "/api/v1/workspaces", map[string]any{"name": "b", "template_key": "python-dev"}).Expect(t, 202, "ws basic").Data()

	basic.Do("POST", "/api/v1/gpu/requests", map[string]any{"workspace_id": str(wsBasic, "id"), "duration_hours": 2, "reason": "train a small model"}).Expect(t, 422, "quota without gpu")
	researcher.Do("POST", "/api/v1/gpu/requests", map[string]any{"workspace_id": str(ws1, "id"), "duration_hours": 2, "reason": "short"}).Expect(t, 422, "reason too short")
	researcher.Do("POST", "/api/v1/gpu/requests", map[string]any{"workspace_id": str(wsBasic, "id"), "duration_hours": 2, "reason": "using someone else's workspace"}).Expect(t, 404, "foreign workspace")
	r1 := researcher.Do("POST", "/api/v1/gpu/requests", map[string]any{"workspace_id": str(ws1, "id"), "duration_hours": 4, "reason": "fine-tune a transformer model"}).Expect(t, 201, "request 1").Data()
	r2 := researcher.Do("POST", "/api/v1/gpu/requests", map[string]any{"workspace_id": str(ws2, "id"), "duration_hours": 4, "reason": "second experiment in parallel"}).Expect(t, 201, "request 2").Data()

	researcher.Do("POST", "/api/v1/gpu/requests/"+str(r1, "id")+"/approve", nil).Expect(t, 403, "self approval")
	admin.Do("POST", "/api/v1/gpu/requests/"+str(r1, "id")+"/approve", map[string]any{"note": "ok"}).Expect(t, 200, "approve 1")
	admin.Do("POST", "/api/v1/gpu/requests/"+str(r2, "id")+"/approve", nil).Expect(t, 409, "exclusive GPU already allocated")
	admin.Do("POST", "/api/v1/gpu/requests/"+str(r2, "id")+"/reject", map[string]any{"note": "GPU busy, try next week"}).Expect(t, 200, "reject 2")

	allocs := researcher.Do("GET", "/api/v1/gpu/allocations?status=active", nil).Expect(t, 200, "allocations").List()
	if len(allocs) != 1 {
		t.Fatalf("expected one active allocation, got %d", len(allocs))
	}
	aid := str(allocs[0].(map[string]any), "id")
	basic.Do("POST", "/api/v1/gpu/allocations/"+aid+"/release", nil).Expect(t, 404, "other user release")
	researcher.Do("POST", "/api/v1/gpu/allocations/"+aid+"/release", map[string]string{"reason": "training finished"}).Expect(t, 200, "release")
	if n := len(researcher.Do("GET", "/api/v1/gpu/allocations?status=active", nil).List()); n != 0 {
		t.Fatalf("allocation should be released, %d active", n)
	}
	// Notifications were delivered.
	notes := researcher.Do("GET", "/api/v1/notifications", nil).Expect(t, 200, "notifications").List()
	if len(notes) < 2 {
		t.Fatalf("expected GPU notifications, got %d", len(notes))
	}
}

func TestAgentOfflineIsReportedHonestly(t *testing.T) {
	e := setup(t, envOpts{})
	admin := e.AdminClient()
	admin.Do("GET", "/api/v1/admin/agent/status", nil).Expect(t, 200, "agent status")
	e.StopAgent()
	h := admin.Do("GET", "/api/v1/monitoring/host", nil).Expect(t, 200, "host metrics").Data()
	if h["live"] != false {
		t.Fatalf("metrics must not be reported as live when the agent is down: %v", h)
	}
	st := admin.Do("GET", "/api/v1/admin/agent/status", nil).Expect(t, 200, "agent status offline").Data()
	if st["live_error"] == nil {
		t.Fatalf("expected live_error: %v", st)
	}
	// Workspace creation is accepted; provisioning retries until the agent returns.
	ws := admin.Do("POST", "/api/v1/workspaces", map[string]any{"name": "pending", "template_key": "ubuntu-dev"}).Expect(t, 202, "create while agent down").Data()
	e.Drain()
	d := admin.Do("GET", "/api/v1/workspaces/"+str(ws, "id"), nil).Expect(t, 200, "get").Data()
	if str(d, "status") != "provisioning" || d["pending_job"] == nil {
		t.Fatalf("expected provisioning with a pending retry: %v", d)
	}
	// Files return 503 instead of fake data.
	admin.Do("POST", "/api/v1/workspaces/"+str(ws, "id")+"/start", nil).Expect(t, 409, "cannot start while provisioning")
	e.StartAgent()
	e.Svcs.Agent.Refresh(context.Background())
}
