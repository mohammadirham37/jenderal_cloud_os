# 0001 — Go modular monolith for API and worker

- Status: Accepted
- Date: 2026-11-02

## Context and problem statement

ITK Cloud runs on a single lab server and is maintained by a small team. The specification
(§1 rule 6, §7) asks for a modular monolith in Go with clearly separated modules (auth, users,
rbac, projects, workspaces, terminals, files, containers, images, databases, gpu, resources,
monitoring, notifications, audit, backups, settings, agent) and a separate privileged host
agent. How should the backend be structured and deployed?

## Decision drivers

- One server, no orchestration; simple deployment and operations.
- Strong consistency between domain changes, audit entries and background jobs.
- Clear module boundaries without network hops.
- Go's standard library is sufficient for HTTP (method-aware `ServeMux` patterns), JSON and
  structured logging (`log/slog`).

## Considered options

1. Microservices per domain area.
2. One monolithic binary handling HTTP, jobs and host access.
3. A modular monolith shared by two entry points (API and worker), plus a separate host agent.

## Decision outcome

Chosen option 3. `internal/modules/<module>` packages hold handlers, services and SQL for
each domain; `internal/platform/*` provides shared infrastructure (config, db, jobs, httpx,
authz, crypto, logging, redisx, idempotency); `internal/app` wires them into the API
(`apps/api`) and the worker (`apps/worker`), which share the same services
(`app.NewServices`). Host operations live in a separate binary (`apps/agent`,
[ADR 0003](0003-host-agent-unix-socket-hmac.md)). Handlers use the standard library router and
small helpers instead of a web framework; persistence uses `pgx` with hand-written SQL.

### Consequences

- Good: a single transaction can change domain rows, write the audit entry and enqueue the
  job; modules call each other directly; two small binaries to deploy.
- Good: API and worker scale independently in the future (the worker is already stateless
  apart from the database).
- Bad: module boundaries are enforced by convention and review, not by process isolation.
- Bad: hand-written SQL requires care (parameterisation and allowlisted sort columns are
  mandatory conventions).

## More information

[architecture.md §5](../architecture.md#5-modular-monolith), `internal/app/api.go`, `internal/app/worker.go`.
