// Package jobs implements a durable PostgreSQL-backed job queue using
// SELECT ... FOR UPDATE SKIP LOCKED. Jobs are enqueued in the same
// transaction as the domain change that requires them, are retried with
// exponential backoff and can be deduplicated with an idempotency key.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itk-cloud/itk-cloud/internal/platform/db"
)

// Job is a claimed job.
type Job struct {
	ID          uuid.UUID
	Kind        string
	Payload     json.RawMessage
	Attempts    int
	MaxAttempts int
}

// Decode unmarshals the payload.
func (j Job) Decode(v any) error { return json.Unmarshal(j.Payload, v) }

// Options configure Enqueue.
type Options struct {
	IdempotencyKey string
	RunAt          time.Time
	MaxAttempts    int
	ResourceType   string
	ResourceID     string
	CreatedBy      *uuid.UUID
}

// Enqueue inserts a job. With an idempotency key, an existing job with the
// same key is returned instead of creating a duplicate.
func Enqueue(ctx context.Context, q db.Querier, kind string, payload any, o Options) (uuid.UUID, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return uuid.Nil, err
	}
	if o.MaxAttempts == 0 {
		o.MaxAttempts = 5
	}
	runAt := o.RunAt
	if runAt.IsZero() {
		runAt = time.Now()
	}
	var key *string
	if o.IdempotencyKey != "" {
		key = &o.IdempotencyKey
	}
	var id uuid.UUID
	err = q.QueryRow(ctx, `
		INSERT INTO background_jobs (kind, payload, max_attempts, run_at, idempotency_key, resource_type, resource_id, created_by)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6,''), NULLIF($7,''), $8)
		ON CONFLICT (idempotency_key) DO UPDATE SET updated_at = background_jobs.updated_at
		RETURNING id`,
		kind, body, o.MaxAttempts, runAt, key, o.ResourceType, o.ResourceID, o.CreatedBy).Scan(&id)
	return id, err
}

// permanentError marks an error that must not be retried.
type permanentError struct{ err error }

func (p permanentError) Error() string { return p.err.Error() }
func (p permanentError) Unwrap() error { return p.err }

// Permanent wraps err so the job fails without retry.
func Permanent(err error) error { return permanentError{err} }

// Handler processes a job.
type Handler func(ctx context.Context, job Job) error

// Runner claims and executes jobs.
type Runner struct {
	pool        *pgxpool.Pool
	log         *slog.Logger
	handlers    map[string]Handler
	workerID    string
	Concurrency int
	Poll        time.Duration
	JobTimeout  time.Duration
	// OnDead is invoked when a job exhausts its retries (e.g. to mark the
	// related resource as failed).
	OnDead func(ctx context.Context, job Job, err error)
}

// NewRunner creates a runner.
func NewRunner(pool *pgxpool.Pool, log *slog.Logger) *Runner {
	host, _ := os.Hostname()
	return &Runner{
		pool: pool, log: log, handlers: map[string]Handler{},
		workerID:    fmt.Sprintf("%s-%d-%s", host, os.Getpid(), uuid.NewString()[:8]),
		Concurrency: 4, Poll: time.Second, JobTimeout: 30 * time.Minute,
	}
}

// Register adds a handler for kind.
func (r *Runner) Register(kind string, h Handler) { r.handlers[kind] = h }

// Kinds returns registered kinds.
func (r *Runner) Kinds() []string {
	out := make([]string, 0, len(r.handlers))
	for k := range r.handlers {
		out = append(out, k)
	}
	return out
}

// Run processes jobs until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < r.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				worked, err := r.RunOnce(ctx)
				if err != nil && ctx.Err() == nil {
					r.log.Error("job loop error", "error", err)
				}
				if !worked {
					select {
					case <-ctx.Done():
					case <-time.After(r.Poll):
					}
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n, err := r.RequeueStale(ctx, r.JobTimeout+5*time.Minute); err != nil {
					r.log.Error("requeue stale jobs", "error", err)
				} else if n > 0 {
					r.log.Warn("requeued stale jobs", "count", n)
				}
			}
		}
	}()
	wg.Wait()
}

// RunOnce claims and runs at most one job. It reports whether a job ran.
func (r *Runner) RunOnce(ctx context.Context) (bool, error) {
	var j Job
	err := r.pool.QueryRow(ctx, `
		UPDATE background_jobs SET status='running', locked_at=now(), locked_by=$1,
		       attempts=attempts+1, updated_at=now()
		WHERE id = (
			SELECT id FROM background_jobs
			WHERE status='queued' AND run_at <= now() AND kind = ANY($2)
			ORDER BY run_at, created_at
			FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING id, kind, payload, attempts, max_attempts`, r.workerID, r.Kinds()).
		Scan(&j.ID, &j.Kind, &j.Payload, &j.Attempts, &j.MaxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	log := r.log.With("job_id", j.ID, "kind", j.Kind, "attempt", j.Attempts)
	jctx, cancel := context.WithTimeout(ctx, r.JobTimeout)
	runErr := r.safeRun(jctx, j)
	cancel()
	// Persist the outcome with a fresh context so shutdown doesn't lose it.
	sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	if runErr == nil {
		_, err = r.pool.Exec(sctx, `UPDATE background_jobs SET status='succeeded', finished_at=now(), updated_at=now(), last_error=NULL, locked_at=NULL WHERE id=$1`, j.ID)
		log.Info("job succeeded")
		return true, err
	}
	var perm permanentError
	if errors.As(runErr, &perm) || j.Attempts >= j.MaxAttempts {
		_, err = r.pool.Exec(sctx, `UPDATE background_jobs SET status='dead', finished_at=now(), updated_at=now(), last_error=$2, locked_at=NULL WHERE id=$1`, j.ID, truncate(runErr.Error()))
		log.Error("job failed permanently", "error", runErr)
		if r.OnDead != nil {
			r.OnDead(sctx, j, runErr)
		}
		return true, err
	}
	delay := Backoff(j.Attempts)
	_, err = r.pool.Exec(sctx, `UPDATE background_jobs SET status='queued', run_at=now()+$2::interval, updated_at=now(), last_error=$3, locked_at=NULL, locked_by=NULL WHERE id=$1`,
		j.ID, fmt.Sprintf("%d milliseconds", delay.Milliseconds()), truncate(runErr.Error()))
	log.Warn("job failed, will retry", "error", runErr, "retry_in", delay.String())
	return true, err
}

func (r *Runner) safeRun(ctx context.Context, j Job) (err error) {
	h, ok := r.handlers[j.Kind]
	if !ok {
		return Permanent(fmt.Errorf("no handler for job kind %q", j.Kind))
	}
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("panic: %v", v)
		}
	}()
	return h(ctx, j)
}

// RequeueStale returns jobs stuck in running (worker crash) to the queue.
func (r *Runner) RequeueStale(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE background_jobs SET status='queued', locked_at=NULL, locked_by=NULL, updated_at=now(),
		       last_error=coalesce(last_error,'') || ' [requeued after worker timeout]'
		WHERE status='running' AND locked_at < now() - $1::interval`,
		fmt.Sprintf("%d seconds", int(olderThan.Seconds())))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Backoff returns the retry delay for attempt n (1-based): 5s, 20s, 80s ... capped at 30m.
func Backoff(n int) time.Duration {
	d := time.Duration(5*math.Pow(4, float64(n-1))) * time.Second
	if d > 30*time.Minute || d <= 0 {
		d = 30 * time.Minute
	}
	return d
}

func truncate(s string) string {
	if len(s) > 2000 {
		return s[:2000]
	}
	return s
}

// Stats summarises the queue for monitoring.
type Stats struct {
	Queued              int `json:"queued"`
	Running             int `json:"running"`
	Failed24h           int `json:"failed_24h"`
	Dead                int `json:"dead"`
	OldestQueuedSeconds int `json:"oldest_queued_seconds"`
}

// QueueStats returns queue counters.
func QueueStats(ctx context.Context, q db.Querier) (Stats, error) {
	var s Stats
	err := q.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status='queued' AND run_at <= now()),
		       count(*) FILTER (WHERE status='running'),
		       count(*) FILTER (WHERE status='dead' AND updated_at > now() - interval '24 hours'),
		       count(*) FILTER (WHERE status='dead'),
		       coalesce(extract(epoch FROM now() - min(run_at) FILTER (WHERE status='queued' AND run_at <= now()))::int, 0)
		FROM background_jobs`).Scan(&s.Queued, &s.Running, &s.Failed24h, &s.Dead, &s.OldestQueuedSeconds)
	return s, err
}
