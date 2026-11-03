package db_test

import (
	"context"
	"os"
	"testing"

	"github.com/itk-cloud/itk-cloud/internal/platform/db"
)

func TestMigrateUpIsIdempotent(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for i := 0; i < 2; i++ {
		if err := db.Migrate(ctx, pool); err != nil {
			t.Fatalf("migrate run %d: %v", i, err)
		}
	}
	cur, latest, err := db.MigrationStatus(ctx, pool)
	if err != nil || cur != latest {
		t.Fatalf("status cur=%d latest=%d err=%v", cur, latest, err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workspace_templates`).Scan(&n); err != nil || n != 11 {
		t.Fatalf("expected 11 templates, got %d (%v)", n, err)
	}
	// audit log must be append-only
	if _, err := pool.Exec(ctx, `INSERT INTO audit_logs (action, outcome) VALUES ('test.append', 'success')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE audit_logs SET action='tampered' WHERE action='test.append'`); err == nil {
		t.Fatal("expected update on audit_logs to fail")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_logs WHERE action='test.append'`); err == nil {
		t.Fatal("expected delete on audit_logs to fail")
	}
}
