package jobs_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/jobs"
)

func TestBackoff(t *testing.T) {
	if jobs.Backoff(1) != 5*time.Second || jobs.Backoff(2) != 20*time.Second {
		t.Fatal("unexpected backoff")
	}
	if jobs.Backoff(50) != 30*time.Minute {
		t.Fatal("backoff must be capped")
	}
}

func TestQueueRetryAndIdempotency(t *testing.T) {
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
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	kind := "test.kind." + time.Now().Format("150405.000000")
	key := "idem-" + kind
	id1, err := jobs.Enqueue(ctx, pool, kind, map[string]int{"n": 1}, jobs.Options{IdempotencyKey: key, MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := jobs.Enqueue(ctx, pool, kind, map[string]int{"n": 2}, jobs.Options{IdempotencyKey: key})
	if err != nil || id1 != id2 {
		t.Fatalf("idempotent enqueue returned %v and %v (%v)", id1, id2, err)
	}

	r := jobs.NewRunner(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	calls := 0
	var dead bool
	r.OnDead = func(context.Context, jobs.Job, error) { dead = true }
	r.Register(kind, func(ctx context.Context, j jobs.Job) error {
		calls++
		return errors.New("boom")
	})
	if ok, err := r.RunOnce(ctx); !ok || err != nil {
		t.Fatalf("first run ok=%v err=%v", ok, err)
	}
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM background_jobs WHERE id=$1`, id1).Scan(&status)
	if status != "queued" {
		t.Fatalf("expected retry queued, got %s", status)
	}
	// make it runnable now
	_, _ = pool.Exec(ctx, `UPDATE background_jobs SET run_at=now() WHERE id=$1`, id1)
	if ok, err := r.RunOnce(ctx); !ok || err != nil {
		t.Fatalf("second run ok=%v err=%v", ok, err)
	}
	_ = pool.QueryRow(ctx, `SELECT status FROM background_jobs WHERE id=$1`, id1).Scan(&status)
	if status != "dead" || !dead || calls != 2 {
		t.Fatalf("expected dead after max attempts; status=%s dead=%v calls=%d", status, dead, calls)
	}
}

func TestPermanentErrorSkipsRetry(t *testing.T) {
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
	kind := "test.perm." + time.Now().Format("150405.000000")
	id, _ := jobs.Enqueue(ctx, pool, kind, nil, jobs.Options{MaxAttempts: 5})
	r := jobs.NewRunner(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.Register(kind, func(context.Context, jobs.Job) error { return jobs.Permanent(errors.New("invalid")) })
	if _, err := r.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM background_jobs WHERE id=$1`, id).Scan(&status)
	if status != "dead" {
		t.Fatalf("expected dead, got %s", status)
	}
}
