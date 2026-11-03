package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/itk-cloud/itk-cloud/internal/modules/agent"
	"github.com/itk-cloud/itk-cloud/internal/modules/backups"
	"github.com/itk-cloud/itk-cloud/internal/modules/databases"
	"github.com/itk-cloud/itk-cloud/internal/modules/images"
	"github.com/itk-cloud/itk-cloud/internal/modules/monitoring"
	"github.com/itk-cloud/itk-cloud/internal/modules/terminals"
	"github.com/itk-cloud/itk-cloud/internal/modules/workspaces"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/jobs"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// Worker runs background jobs and periodic tasks.
type Worker struct {
	D      *Deps
	S      *Services
	Runner *jobs.Runner
}

// NewWorker registers all job handlers.
func NewWorker(d *Deps, s *Services) *Worker {
	r := jobs.NewRunner(d.Pool, d.Log.With("component", "jobs"))
	s.Workspaces.RegisterJobs(r)
	s.Databases.RegisterJobs(r)
	s.Backups.RegisterJobs(r)
	r.OnDead = func(ctx context.Context, j jobs.Job, err error) {
		switch j.Kind {
		case databases.JobProvision, databases.JobDelete:
			s.Databases.OnDead(ctx, j, err)
		case backups.JobRun, backups.JobRestore:
		default:
			s.Workspaces.OnDead(ctx, j, err)
		}
	}
	return &Worker{D: d, S: s, Runner: r}
}

// periodic runs fn every interval (first run after initial delay).
func periodic(ctx context.Context, wg *sync.WaitGroup, log *slog.Logger, name string, initial, every time.Duration, fn func(context.Context) error) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTimer(initial)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				start := time.Now()
				tctx, cancel := context.WithTimeout(ctx, every+time.Minute)
				err := safe(func() error { return fn(authz.WithPrincipal(tctx, authz.System())) })
				cancel()
				if err != nil && ctx.Err() == nil {
					log.Warn("periodic task failed", "task", name, "error", err.Error())
				} else {
					log.Debug("periodic task done", "task", name, "duration_ms", time.Since(start).Milliseconds())
				}
				t.Reset(every)
			}
		}
	}()
}

func safe(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicErr{r}
		}
	}()
	return fn()
}

type panicErr struct{ v any }

func (p panicErr) Error() string { return "panic in periodic task" }

// Run starts job processing and periodic tasks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	log := w.D.Log
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); w.Runner.Run(ctx) }()

	// Heartbeat for service monitoring.
	periodic(ctx, &wg, log, "heartbeat", 0, 30*time.Second, func(ctx context.Context) error {
		return w.D.Redis.Set(ctx, monitoring.WorkerHeartbeatKey, time.Now().UTC().Format(time.RFC3339), 90*time.Second).Err()
	})
	// Agent heartbeat.
	periodic(ctx, &wg, log, "agent_heartbeat", time.Second, 30*time.Second, func(ctx context.Context) error {
		_, err := w.S.Agent.Refresh(ctx)
		return err
	})
	// Metrics sampling + alert evaluation.
	interval := time.Duration(w.D.Settings.Int(ctx, "monitoring.sample_interval_seconds")) * time.Second
	if interval < 10*time.Second {
		interval = 30 * time.Second
	}
	periodic(ctx, &wg, log, "metrics", 5*time.Second, interval, func(ctx context.Context) error {
		host, hostErr := w.S.Monitoring.SampleHost(ctx)
		var inv *agentproto.GPUInventory
		if hostErr == nil {
			if err := w.S.Monitoring.SampleContainers(ctx); err != nil {
				log.Warn("container sampling failed", "error", err.Error())
			}
			if w.D.Cfg.GPUEnabled {
				var err error
				if inv, err = w.S.GPU.Sync(ctx); err == nil {
					_ = w.S.Monitoring.SampleGPUs(ctx, inv)
				}
			}
		}
		return w.S.Monitoring.EvaluateAlerts(ctx, host, hostErr, inv)
	})
	periodic(ctx, &wg, log, "image_sync", 15*time.Second, 10*time.Minute, func(ctx context.Context) error {
		return images.Sync(ctx, w.D.Pool, w.S.Workspaces.Agent)
	})
	periodic(ctx, &wg, log, "workspace_usage", 20*time.Second, 5*time.Minute, w.S.Workspaces.RefreshUsage)
	periodic(ctx, &wg, log, "idle_stop", 30*time.Second, time.Minute, func(ctx context.Context) error {
		n, err := w.S.Workspaces.EnforceIdleAndDuration(ctx)
		if n > 0 {
			log.Info("stopping idle or expired workspaces", "count", n)
		}
		return err
	})
	periodic(ctx, &wg, log, "gpu_reconcile", 45*time.Second, 5*time.Minute, func(ctx context.Context) error {
		if !w.D.Cfg.GPUEnabled {
			return nil
		}
		n, err := w.S.GPU.Reconcile(ctx)
		if n > 0 {
			log.Info("released GPU allocations", "count", n)
		}
		return err
	})
	periodic(ctx, &wg, log, "terminal_expiry", 10*time.Second, time.Minute, func(ctx context.Context) error {
		return terminals.ExpireSessions(ctx, w.D.Pool)
	})
	periodic(ctx, &wg, log, "session_cleanup", time.Minute, time.Hour, func(ctx context.Context) error {
		_, err := w.D.Pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now() - interval '7 days' OR revoked_at < now() - interval '7 days'`)
		if err == nil {
			_, err = w.D.Pool.Exec(ctx, `DELETE FROM password_reset_tokens WHERE expires_at < now() - interval '7 days'`)
		}
		if err == nil {
			_, err = w.D.Pool.Exec(ctx, `DELETE FROM api_idempotency_keys WHERE created_at < now() - interval '2 days'`)
		}
		if err == nil {
			_, err = w.D.Pool.Exec(ctx, `DELETE FROM background_jobs WHERE status IN ('succeeded','cancelled') AND finished_at < now() - interval '30 days'`)
		}
		if err == nil {
			_, err = w.D.Pool.Exec(ctx, `DELETE FROM notifications WHERE read_at < now() - interval '90 days'`)
		}
		return err
	})
	periodic(ctx, &wg, log, "metrics_prune", 2*time.Minute, 6*time.Hour, w.S.Monitoring.Prune)
	periodic(ctx, &wg, log, "database_sizes", 3*time.Minute, 30*time.Minute, w.S.Databases.RefreshSizes)
	periodic(ctx, &wg, log, "backup_schedule", time.Minute, 10*time.Minute, func(ctx context.Context) error {
		return w.S.Backups.ScheduleDaily(ctx, time.Now())
	})
	periodic(ctx, &wg, log, "retention", 5*time.Minute, 6*time.Hour, func(ctx context.Context) error {
		_, err := w.S.Backups.ApplyRetention(ctx)
		return err
	})
	log.Info("worker started", "job_kinds", len(w.Runner.Kinds()), "version", Version)
	<-ctx.Done()
	wg.Wait()
}

var _ = agent.NodeName
var _ = workspaces.JobStart
