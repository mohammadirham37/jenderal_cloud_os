# Developing Jenderal Cloud OS

This guide sets up a local development environment (Linux or macOS), runs the stack, and
runs the test suites. Production installation is described in [installation.md](installation.md).

## 1. Prerequisites

| Tool | Version used | Notes |
|---|---|---|
| Go | 1.27.3 toolchain (`go.mod`: `go 1.26.0`) | |
| Node.js | 22 LTS | CI builds the web console with Node 24; both work. |
| pnpm | 9 | `corepack enable` or install pnpm 9. |
| PostgreSQL | 17 locally (CI uses 16) | Server plus client tools **`pg_dump`/`pg_restore` in `PATH`** (backup tests). A role that can `CREATEDB` for integration tests. |
| Redis | any current release | A dedicated DB number for tests (it is flushed). |
| Docker Engine / Docker Desktop | Engine API ≥ 1.44 (tested with Docker 29.9) | **Only** as the runtime for workspace containers. The platform runs as normal processes. |

Key Go dependencies (pinned in `go.mod`): pgx v5.12.0, go-redis v9.23.0, goose v3.29.0,
coder/websocket v1.8.15, creack/pty v1.1.24, gopsutil v4.26.10, filippo.io/age v1.3.2,
golang.org/x/crypto v0.58.0. Web: SvelteKit 3.1, Svelte 5.57, Vite 8.3, Tailwind CSS 4.3,
TypeScript 6.0, @xterm/xterm 6.0.0 (pinned in `apps/web/pnpm-lock.yaml`).

## 2. First-time setup

```bash
git clone <repo> itk_cloud && cd itk_cloud
make setup                       # go mod download + pnpm install --frozen-lockfile (apps/web)
cp .env.example .env
go run ./apps/cli gen-secrets    # paste SESSION_SECRET, ENCRYPTION_KEY, AGENT_SHARED_SECRET into .env
mkdir -p .dev/workspaces .dev/backups .dev/config
go run ./apps/cli backup keygen  # put BACKUP_AGE_RECIPIENT in .env; save the private key line
                                 # (AGE-SECRET-KEY-...) to .dev/backup-identity.txt (0600)
createdb itk_cloud_dev           # or psql -c 'CREATE DATABASE itk_cloud_dev'
```

Edit `.env` for local development (`.env`, `.dev/`, `*.age` and `*-private-key.txt` are in
`.gitignore`; never commit them):

```dotenv
APP_ENV=development
APP_BASE_URL=http://localhost:5173
COOKIE_SECURE=false
DATABASE_URL=postgres://postgres@127.0.0.1:5432/itk_cloud_dev?sslmode=disable
REDIS_URL=redis://127.0.0.1:6379/3
AGENT_SOCKET_PATH=/absolute/path/to/itk_cloud/.dev/agent.sock
AGENT_SOCKET_GROUP=staff                 # macOS; on Linux your primary group
AGENT_STORAGE_DRIVER=directory           # macOS and non-root agents (quota measured, not enforced)
DOCKER_SOCKET=/Users/<you>/.docker/run/docker.sock   # Docker Desktop on macOS; /var/run/docker.sock on Linux
WORKSPACE_ROOT=/absolute/path/to/itk_cloud/.dev/workspaces
BACKUP_ROOT=/absolute/path/to/itk_cloud/.dev/backups
CONFIG_DIR=/absolute/path/to/itk_cloud/.dev/config
# Optional: project databases on the same server
PROJECT_DB_ADMIN_URL=postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable
```

Notes:

- The `loopback` storage driver needs Linux and a root agent (it runs `mkfs.ext4` and
  `mount`). Use `directory` on macOS or when running the agent as your user; ownership changes
  are skipped when the agent is not root.
- Linux-native workspaces require Linux with `useradd` and `systemd-run`; keep
  `WORKSPACE_NATIVE_ENABLED=false` on macOS.
- GPU features need `GPU_ENABLED=true` and `nvidia-smi`; without a GPU the inventory reports
  "unavailable" honestly.

## 3. Running the stack

Four terminals (the Make targets build `bin/` first):

```bash
make dev-agent    # agent: exports .env (set -a; . ./.env) and runs ./bin/itk-cloud-agent
make dev-api      # API on 127.0.0.1:8080, applies migrations (-migrate)
make dev-worker   # worker (refuses to start while migrations are pending: start the API first)
make dev-web      # Vite dev server on http://localhost:5173, proxies /api (incl. WebSocket) and /health to :8080
```

The API, worker and `itkctl` read `.env` from the working directory themselves; the agent
only reads its process environment, which `make dev-agent` exports. Set `ITK_API_URL` to point
the Vite proxy at another API address.

Create the first administrator and log in at <http://localhost:5173>:

```bash
make dev-admin    # ./bin/itkctl admin create --username admin --email admin@itk.local --name "Development Admin"
```

Super administrators must enroll TOTP MFA on first login (any authenticator app).

Build at least the `ubuntu-dev` workspace image (required by the Docker integration test):

```bash
installer/build-workspace-images.sh ubuntu-dev          # add sudo on Linux if your user cannot use Docker
installer/build-workspace-images.sh                     # all 11 images (slow)
```

### Optional: the app proxy in development

The app proxy only answers on `APP_PROXY_ORIGIN`, which must be a different origin than the
console. A simple local setup:

```bash
# .env
APP_PROXY_ORIGIN=http://localhost:5174
# forward :5174 to the API, keeping the Host header (Caddy passes it through by default)
caddy reverse-proxy --from http://localhost:5174 --to 127.0.0.1:8080
```

Use `localhost` (not `127.0.0.1`) so the session cookie set for `localhost:5173` is also sent
to `localhost:5174`.

## 4. Tests

### Go unit and DB-backed tests

```bash
export TEST_DATABASE_URL='postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'
export TEST_REDIS_URL='redis://127.0.0.1:6379/14'
make test          # go test -race -count=1 ./internal/... ./packages/... ./apps/...
```

Tests needing PostgreSQL/Redis skip when the variables are not set. Covered: password hashing,
password policy, secret box, TOTP (RFC vectors, replay), log redaction, Redis limiter, job
queue (retry, backoff, idempotency, permanent errors), migrations idempotency, agent protocol
(path cleaning, signing, strict decoding, validation), agent server end-to-end (auth,
allowlist, file ops, symlink escape, socket mode), `fsops` (CRUD, symlink escape, upload
limit, binary detection, write conflicts), `nvidia-smi` parsing.

### Integration and security tests

```bash
export TEST_PG_ADMIN_URL='postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'  # role with CREATEDB
export TEST_REDIS_URL='redis://127.0.0.1:6379/14'      # dedicated DB, flushed by the tests
export TEST_DOCKER_SOCKET=/var/run/docker.sock          # optional; auto-detected (Docker Desktop path too)
make test-integration PG_BIN=/usr/lib/postgresql/17/bin  # PG_BIN puts pg_dump/pg_restore on PATH
go test -count=1 ./tests/security/...
```

The harness (`tests/testenv`) creates a fresh database per test, builds and starts a real
agent process (directory driver), runs the real API handler and worker against it, and
drains the job queue synchronously. The Docker lifecycle test runs only when Docker is
reachable and `itk-cloud/ubuntu-dev:1.0` exists; otherwise it is skipped.

What the integration tests verify (`tests/integration`): login/MFA/CSRF/RBAC/rank escalation,
suspension and session revocation, password reset, enumeration resistance, lockout, audit
append-only; rate limiting; workspace lifecycle with idempotent create, isolation (404 for
other users), container hardening via `docker inspect`, terminal over WebSocket (uid 1000,
reconnect, hijack and cross-origin rejection), file manager (upload, conflict, editor with
stale-write detection, download, traversal), members, emergency access with reason, stop and
delete; quota enforcement; project database lifecycle (credentials once, non-superuser,
reset, backup + restore, identity scrubbed); encrypted platform backups and verification
(wrong key fails); GPU request/approval/exclusivity/release; honest reporting while the agent
is offline. `tests/security`: IDOR across resource types, session fixation and cookie flags,
secret leakage, inert injection inputs, unauthorized WebSocket, security headers, body limits.

### Web console

```bash
cd apps/web
pnpm run check                        # svelte-kit sync + svelte-check (TypeScript)
pnpm run lint                         # prettier --check + eslint
pnpm exec vitest run --project server # unit tests (node)
pnpm exec vitest run --project client # component tests (Playwright browser, run `pnpm exec playwright install` once)
pnpm run build                        # adapter-node build in apps/web/build
```

### Browser end-to-end tests (Playwright)

The E2E specs live in `tests/e2e` (`*.e2e.ts`) and run against a **running stack**
(the four dev processes above):

```bash
make build-go
make test-e2e                         # links node_modules, runs `pnpm exec playwright test` from apps/web
E2E_BASE_URL=https://itk-cloud.lab make test-e2e   # against another deployment
```

The global setup creates a fresh super administrator with `bin/itkctl` (using `.env` or
`ITK_CLOUD_ENV_FILE`) and enrolls MFA in the browser.

### Other checks

```bash
make lint            # gofmt, go vet, eslint, prettier, svelte-check
make openapi-lint    # npx @redocly/cli lint packages/api-contracts/openapi.yaml
make shellcheck      # installer scripts
make check           # lint + test + test-web + shellcheck
```

CI (`.github/workflows/ci.yml`) runs gofmt, vet, unit and integration tests with PostgreSQL 16
and Redis 7 services, builds the `ubuntu-dev` image, `govulncheck`, web lint/check/unit/build,
`pnpm audit`, shellcheck, the OpenAPI lint and a gitleaks secret scan.

## 5. Repository layout

```text
apps/
  api/        itk-cloud-api entry point
  worker/     itk-cloud-worker entry point
  agent/      itk-cloud-agent (internal/: server, docker, storage, fsops, native, gpu, hostinfo, backup, config)
  cli/        itkctl
  web/        SvelteKit console (see apps/web/CONVENTIONS.md)
internal/
  app/        wiring: dependencies, route table (api.go), worker periodic tasks
  platform/   config, db (+migrations runner), jobs, httpx, authz, crypto, logging, redisx, idempotency
  modules/    auth, users, rbac, projects, workspaces, containers, images, files, terminals, appproxy,
              databases, gpu, resources, monitoring, notifications, audit, backups, settings, agent,
              hostops, dashboard, mail
packages/
  agentproto/     versioned API↔agent protocol (shared by both sides)
  api-contracts/  OpenAPI 3.1 contract
migrations/   goose SQL migrations (embedded into the binaries)
deployments/  systemd units, Caddyfile template, Docker daemon/Redis config, monitoring, workspace images
installer/    install.sh, upgrade.sh, uninstall.sh, backup.sh, build-workspace-images.sh, lib.sh
tests/        integration/, security/, testenv/ (Go harness), e2e/ (Playwright)
docs/         documentation and ADRs
```

## 6. Coding conventions

**Go**

- `gofmt` and `go vet` clean. Errors are wrapped with context; handlers return `error` and
  use `httpx` error constructors (`httpx.Validation`, `httpx.NotFound`, …); internal errors
  are never shown to clients.
- Every handler starts with its authorization check (`authz.FromContext(ctx).Can(...)` or
  a module `Authorize` helper). Deny by default; return 404 when the caller cannot view the
  resource.
- Multi-table changes run in `db.WithTx`; the audit entry and any job are written in the same
  transaction (`audit.Record(ctx, tx, ...)`, `jobs.Enqueue(ctx, tx, ...)`).
- Long or host-touching work goes into an idempotent background job; handlers return 202.
- The API/worker never execute host commands or talk to Docker; host work goes through the
  agent (`internal/modules/agent`), always with a reason. Adding an agent operation follows
  [agent-protocol.md §9](agent-protocol.md#9-adding-a-new-operation-safely).
- Request inputs are decoded with `httpx.DecodeJSON` (strict) into dedicated input structs;
  validate every field server-side and report field errors in `details`.
- SQL is parameterised; dynamic sort columns come from allowlists (`Page.OrderBy`).
- Never log secrets; use attribute names covered by the redaction list or omit the value.
- Settings are added to `settings.Schema` (typed, ranged) and seeded in a migration.

**Migrations**

- New goose files `migrations/000NN_description.sql` with `-- +goose Up` and `-- +goose Down`.
  Never edit a migration that has been applied anywhere; add a new one.
- `itkctl migrate` (or `itk-cloud-api -migrate` in development) applies them; the worker and
  `/health/ready` refuse/report pending migrations.

**API changes**

- Update `packages/api-contracts/openapi.yaml`, the endpoint/permission index in
  [api.md](api.md), the web types in `apps/web/src/lib/api/`, and add tests. `make openapi-lint`
  must report no errors.

**Web**

- Follow `apps/web/CONVENTIONS.md` (Svelte 5 runes, `#lib/...` imports with extensions,
  `api` client, loading/empty/error/success states, confirmation for destructive actions,
  permission-aware UI while relying on backend enforcement, no invented data).

**Documentation**

- Keep docs accurate to the code; update [implementation-plan.md](implementation-plan.md)
  when a task changes status and record significant decisions as ADRs in [docs/adr](adr/README.md).
