# 0002 — PostgreSQL-backed job queue with `SKIP LOCKED`

- Status: Accepted
- Date: 2026-11-02

## Context and problem statement

Provisioning, lifecycle actions, project databases, backups and restores are slow and touch
the host. They must run in the background, survive restarts, retry on transient errors, never
run twice by accident, and be visible to administrators (spec §7, §10.2: "provisioning and
deletion run as idempotent background jobs"). Where should the queue live?

## Decision drivers

- Exactly-once *enqueue* together with the domain change (no lost or orphaned jobs).
- Durability across worker crashes and restarts.
- Few moving parts on a single server.
- Visibility (failed/dead jobs listed and retryable from the console).

## Considered options

1. Redis-based queue (lists/streams).
2. A dedicated broker (e.g. RabbitMQ/NATS).
3. A `background_jobs` table in PostgreSQL claimed with `SELECT … FOR UPDATE SKIP LOCKED`.

## Decision outcome

Chosen option 3 (`internal/platform/jobs`). Jobs are inserted in the same transaction as the
domain change; workers claim one due job at a time with `FOR UPDATE SKIP LOCKED`, run it with a
30-minute timeout, and record success, a retry (`run_at = now + backoff`, backoff
`5 s × 4^(n-1)` capped at 30 min) or a terminal `dead` state (max attempts or
`jobs.Permanent`). A unique `idempotency_key` deduplicates enqueues; stale `running` jobs are
re-queued after 35 minutes; `OnDead` hooks mark resources failed. Redis is used only for rate
limiting and the worker heartbeat.

### Consequences

- Good: transactional consistency; no extra service; jobs and their errors are queryable
  (`GET /monitoring/jobs`, per-workspace jobs) and retryable.
- Good: per-resource serialisation is a simple SQL check (no new lifecycle action while a job
  is queued/running).
- Bad: polling (1 s) adds latency and some database load; adequate for a single server.
- Bad: handlers must be idempotent because a job may run again after a crash mid-way; this is
  a coding rule (re-read state, skip completed steps; agent operations are idempotent).

## More information

`internal/platform/jobs/jobs.go`, `jobs_test.go` (retry, backoff, idempotency, permanent
errors), [architecture.md §6](../architecture.md#6-background-jobs).
