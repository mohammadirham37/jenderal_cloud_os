# ITK Cloud security

This document is the threat model and the catalogue of security controls of ITK Cloud, with
references to the code that implements them, followed by known limitations and the
production release checklist (spec §30). It describes what is implemented — not intentions.

Related: [architecture](architecture.md) · [agent protocol](agent-protocol.md) ·
[installation](installation.md) (firewall, TLS) · [backup & restore](backup-restore.md) ·
[operations](operations.md).

## 1. Scope and assets

| Asset | Where | Why it matters |
|---|---|---|
| User workspace data (code, datasets, notebooks) | `WORKSPACE_ROOT/<id>/data` (loopback ext4 volumes) | Confidentiality between users; integrity of research/coursework. |
| Platform database | PostgreSQL `itk_cloud` | Accounts, password hashes, sessions (hashed), encrypted secrets, audit log. |
| Project databases | PostgreSQL databases `itk_*` | Project data; must be isolated per project. |
| Secrets | `/etc/itk-cloud/itk-cloud.env` (0640 root:itk-cloud) | `SESSION_SECRET`, `ENCRYPTION_KEY`, `AGENT_SHARED_SECRET`, DB/SMTP credentials. |
| Backup private key (age identity) | **offline** (password manager / sealed envelope) | Only way to read backups. |
| Host integrity | Ubuntu host, Docker daemon, root agent | Compromise = compromise of all users. |
| Availability | CPU, RAM, disk, GPU | Shared by all lab users. |
| Audit trail | `audit_logs` | Accountability for administrative and emergency actions. |

## 2. Actors

| Actor | Trust | Capabilities |
|---|---|---|
| Anonymous LAN client | untrusted | Reach Caddy (443/8443): login, forgot/reset password, `/api/v1/config`, health. |
| Student / assistant / lecturer | authenticated, untrusted code author | Use own workspaces within quota; run arbitrary code **inside** containers. |
| Workspace code (container) | untrusted | Runs as uid 1000 inside a hardened container; outbound network. |
| Workspace web app (via app proxy) | untrusted | Serves HTML/JS on `APP_PROXY_ORIGIN`. |
| Administrator (`admin`) | trusted for user/workload management, MFA enforced | Manage users of lower rank, workspaces, templates, images, quotas, GPU approvals, monitoring. No settings, backups, emergency stop or emergency data access. |
| Super administrator | highly trusted, MFA enforced | Everything, including settings, backups/restores, emergency stop, emergency access with reason. |
| Host operator (SSH, root) | fully trusted | Out of scope of the application; restricted to administrators by firewall/SSH config. |
| Compromised API/worker process | threat | Runs as `itk-cloud`; can talk to the agent but only through allow-listed operations. |

## 3. Trust boundaries

See [architecture.md §2](architecture.md#2-trust-boundaries) for the diagram. The boundaries
are: LAN ↔ Caddy (TLS), Caddy ↔ API/web (loopback), API/worker ↔ agent (Unix socket + HMAC),
agent ↔ Docker/host (root), host ↔ containers (namespaces, cgroups, AppArmor, seccomp),
console origin ↔ app proxy origin (browser same-origin policy).

## 4. Controls

### 4.1 Authentication

- **Password hashing**: Argon2id, `m = 64 MiB, t = 3, p = 2`, 32-byte key, 16-byte random
  salt, PHC string format (`internal/platform/crypto/password.go`). Hashes with weaker
  parameters are transparently re-hashed at the next successful login. Comparison is
  constant-time.
- **User enumeration resistance**: unknown usernames run a dummy Argon2 verification and
  return the same `401 invalid_credentials` as wrong passwords; forgot-password always
  answers `202`.
- **Password policy**: 12–256 characters and at least three character classes. New users
  get a random 16-character temporary password (shown once) and must change it
  (`must_change_password`).
- **First administrator** is created only with `itkctl admin create` (no default
  credentials are seeded; `migrations/00002` seeds no users).

### 4.2 Sessions

- Opaque 32-byte random token in cookie `itk_session`; only `SHA-256(token)` is stored
  (`sessions.token_hash`), so a database leak does not yield usable cookies
  (`internal/modules/auth/session.go`).
- Cookie flags: `HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` when `COOKIE_SECURE`
  (required to be true in production; defaults to true for `https` base URLs).
- Absolute TTL `SESSION_TTL_HOURS` (12 h), idle timeout `SESSION_IDLE_MINUTES` (120 min),
  10 min for sessions awaiting MFA. Server-side revocation (`revoked_at`).
- Session rotation on MFA enable/verify (anti-fixation). Revocation of all sessions on
  password reset, suspension, role change, admin MFA reset; of all other sessions on
  password change. Suspended users' sessions are rejected immediately.

### 4.3 CSRF (double check)

`Sessions.CSRF` (`internal/modules/auth/session.go`) for every unsafe method:

1. `Origin`, when present, must equal the origin of `APP_BASE_URL`.
2. Authenticated requests must carry `X-CSRF-Token` equal to the random per-session token
   (constant-time compare).

Combined with `SameSite=Lax` cookies. Verified by `TestAuthMFAAndRBAC` (missing token and
foreign origin rejected). The terminal WebSocket additionally checks the `Origin` against the
console host (`websocket.AcceptOptions.OriginPatterns`; cross-site WebSocket rejected in
`TestContainerWorkspaceLifecycle`).

### 4.4 Authorization

- **RBAC with granular permissions** (45 permission keys, `migrations/00002`) assigned to
  roles; principal permissions are loaded per request (`rbac.LoadUserAccess`).
- **Deny by default**: every handler explicitly checks permissions/ownership
  (`internal/platform/authz`); routes outside the table return 404.
- **Role ranks** (super_admin 100, admin 80, lecturer 40, assistant 30, student 10): users
  may grant roles and manage users only of strictly lower rank (`rbac.CanGrant`,
  `users.canManage`); `super_admin` cannot be modified; users cannot change their own roles
  or status. Verified: an admin cannot grant `super_admin` or `admin`, nor suspend/edit the
  super admin.
- **Ownership and no-existence-leak**: workspaces, projects, containers, databases, users and
  GPU allocations return **404** when the caller cannot view them (IDOR/BOLA protection);
  verified by the integration tests (other user → 404 on workspace, files, start, delete,
  terminal, logs, database, project, GPU allocation release).
- **Workspace access levels** (view/operate/edit/manage) separate metadata from private
  data: assistants and administrators with `workspaces.view_all` see metadata but get 403 on
  files ([workspace.md §6](workspace.md#6-members-and-access-levels)).
- **Emergency access**: only `workspaces.access_all` (super_admin), only with
  `X-Access-Reason` (≥ 10 characters), audited per request as `workspace.emergency_access`
  with the reason and owner; terminal sessions store the reason (`terminal_sessions.access_reason`).
  The app proxy refuses emergency access.
- **MFA gate**: `RequireAuth` blocks everything except the MFA/auth endpoints until MFA is
  verified; forced password change is gated the same way.

### 4.5 MFA

TOTP (RFC 6238, SHA-1, 6 digits, 30 s, ±1 step) with **replay protection** — each time step
is accepted once (`mfa_methods.last_used_step`, `crypto.VerifyTOTP`). TOTP secrets are
encrypted at rest with the secret box (AAD `mfa:<user id>`). 10 recovery codes per
enrollment, stored as SHA-256 hashes, single use. MFA is mandatory for roles with
`requires_mfa` (super_admin, admin) while `security.require_mfa_for_admins` is on, and for
anyone who enrolled. Lost device: an administrator of higher rank resets MFA
(`POST /users/{id}/reset-mfa`, reason required, audited).

### 4.6 Lockout and rate limits

- Account lockout after `security.login_max_attempts` (default 5) wrong passwords for
  `security.lockout_minutes` (default 15) → `423`.
- Redis fixed-window limits: login 30/IP/10 min and 10/username/15 min (login fails closed
  with 503 if Redis is down); forgot-password, reset-password, change-password, MFA
  enable/verify limits as in [api.md §8](api.md#8-rate-limits). Verified by
  `TestLoginRateLimit` and the lockout step of `TestAuthMFAAndRBAC`.
- Client IP is taken from the TCP peer; `X-Forwarded-For` is trusted only from loopback.

### 4.7 Audit log

- `audit_logs` records actor, action, resource, outcome (`success|failure|denied`), reason,
  IP, user agent, request ID and metadata, written in the same transaction as the change
  where applicable (`internal/modules/audit/audit.go`).
- **Append-only**: triggers reject `UPDATE`, `DELETE` and `TRUNCATE`; the only exception is
  the worker's retention job, which sets the transaction-local flag `itk.audit_retention=on`
  before deleting entries older than `audit.retention_days` (default 365). Verified:
  `DELETE FROM audit_logs` fails in `TestAuthMFAAndRBAC`.
- Audited: logins (success/failure/denied with reason), logout, password changes/resets,
  MFA changes, user/role/quota/template/image/settings changes, workspace and container
  lifecycle, members, file delete/rename/upload, terminal open/close, emergency access,
  database create/delete/credential reset, GPU request/approve/reject/release, backups and
  restores, emergency stop (including denied attempts).

### 4.8 Secrets at rest

- **Secret box**: AES-256-GCM with a random 96-bit nonce, a version byte and associated data
  binding the ciphertext to its row (`internal/platform/crypto/secretbox.go`). Key:
  `ENCRYPTION_KEY` (32 bytes, base64), supplied from the environment, never stored in the
  database. Used for TOTP secrets, generated project-database passwords (AAD `db:<id>`) and
  the age identity of a queued restore (AAD `restore:<job id>`, scrubbed from the job payload
  when the job ends — verified by `TestProjectDatabaseLifecycle`).
- Password reset tokens and session tokens are stored as SHA-256 hashes.
- Generated database passwords are shown only at creation/reset; `GET /databases/{id}` never
  returns them (verified).
- Secrets are generated with `itkctl gen-secrets` (crypto/rand) and kept in
  `/etc/itk-cloud/itk-cloud.env` (0640 root:itk-cloud). Startup validation rejects missing or
  short secrets (`SESSION_SECRET` and `AGENT_SHARED_SECRET` ≥ 32 characters, `ENCRYPTION_KEY`
  exactly 32 bytes).

### 4.9 Host agent

([agent-protocol.md](agent-protocol.md), [ADR 0003](adr/0003-host-agent-unix-socket-hmac.md))

- Unix socket only (no TCP), mode 0660, group `itk-cloud`; parent directory chgrp'd.
- **HMAC-SHA256** over `method \n path \n timestamp \n nonce \n sha256(body or op header)`
  with `AGENT_SHARED_SECRET`; timestamp window ±60 s; **nonce replay cache** (nonces kept for
  120 s); signatures compared with `hmac.Equal`. Unsigned/badly signed requests → 403
  (verified by `TestAgentRejectsBadAuthAndStreamOpsOnOpsEndpoint`).
- **Allowlist** of 30 operations (`agentproto.Operations`); there is no generic exec.
  Streaming operations (download, upload, terminal) are refused on `/v1/ops` and accepted
  only on their dedicated endpoints.
- **Strict decoding**: envelope and payload decoded with `DisallowUnknownFields`; every
  payload has a `Validate()` (UUID formats, regexes for names/image references/env keys,
  numeric ranges, uid ≥ 1000, port ≥ 1024, GPU UUID format, path cleaning).
- Every request carries `request_id`, `actor_id`, `resource_id` and a mandatory `reason`;
  sensitive operations are always logged at info level.
- **Path confinement**: workspace file operations go through Go's `os.Root`
  (`apps/agent/internal/fsops`), which resolves `..` and symlinks without ever leaving the
  workspace data directory; user paths are additionally cleaned (`CleanRelPath`: no
  absolute paths, no `..`, no NUL, ≤ 4096 bytes). Verified by `TestSymlinkEscapeIsBlocked`
  and the traversal checks of the integration tests. Files are opened `O_NONBLOCK` and must
  be regular files (FIFO/device tricks fail); writes are atomic temp-file + rename; created
  files are chowned to the workspace owner.
- **Container label verification**: the agent only touches containers labelled
  `itk.managed=true` whose `itk.container_id` label matches the requested container
  (`resolveContainer`); other containers on the host are refused (`forbidden`).
- Workspace directories are derived only from validated UUIDs.
- Resource limits: 1 MiB request bodies, 64 KiB op headers, 128 KiB headers, at most
  `AGENT_MAX_CONCURRENT_OPS` (32) concurrent operations, helper commands with fixed
  arguments, minimal `PATH` and 1–2 min timeouts.

### 4.10 Container hardening

Applied by the agent's `containerCreate` (`apps/agent/internal/server/ops.go`), verified with
`docker inspect` in `TestContainerWorkspaceLifecycle`:

| Setting | Value |
|---|---|
| User | `1000:1000` (template uid/gid; values < 1000 are rejected by protocol validation) |
| Capabilities | `CapDrop: ["ALL"]`, nothing added |
| Privileged | `false` |
| `SecurityOpt` | `no-new-privileges:true`, `apparmor=docker-default` (when AppArmor is enabled; profile configurable via `AGENT_APPARMOR_PROFILE`), optional custom seccomp JSON via `AGENT_SECCOMP_PROFILE` (Docker's default seccomp profile otherwise) |
| CPU / memory | `NanoCpus = cpu_millis × 10⁶`; `Memory = MemorySwap = memory_bytes` (no swap) |
| PIDs | `PidsLimit` (512 for workspace containers) |
| Init | `Init: true` (zombie reaping) |
| IPC | `private` |
| `/tmp` | tmpfs `rw,nosuid,nodev,size=AGENT_CONTAINER_TMPFS_SIZE` (512m) |
| Root filesystem | read-only when the template sets `read_only_rootfs`; size-limited with `AGENT_CONTAINER_ROOTFS_SIZE` (overlay2 on XFS `pquota` only) |
| ulimit | `nofile` 8192 |
| Logs | `json-file`, `max-size=10m`, `max-file=3` |
| Restart policy | `no` |
| Network | per-workspace bridge `itk-ws-<id>`; ports published on `127.0.0.1` only |
| Mounts | only the workspace data directory at `/home/itk` (`rprivate`) |
| Extra hosts | `host.docker.internal:host-gateway` (lets workspaces reach host services intended for them, i.e. project databases; firewall and `pg_hba.conf` restrict what is reachable) |
| Environment | `LD_PRELOAD`, `HOME`, `USER`, `ITK_WORKSPACE_ID`, `ITK_CONTAINER_ID` cannot be overridden by templates |
| Images | only from the allowlist (`container_images.is_allowed`) via active templates |

### 4.11 No Docker socket exposure

Only the root agent talks to `/var/run/docker.sock` (minimal client,
[ADR 0010](adr/0010-minimal-docker-engine-client.md)). The API/worker have no Docker access,
the socket is never mounted into containers (asserted by the integration test), and no user
is added to the `docker` group.

### 4.12 Project database isolation

([ADR 0006](adr/0006-project-database-isolation.md)) One database and one login role per
project database: `NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS`, connection
limit, `REVOKE ALL ON DATABASE ... FROM PUBLIC`, `GRANT CONNECT, TEMPORARY` only to its role.
Identifiers are validated (`^itk_[a-z0-9_]{3,55}$`) and quoted. The platform connects with a
`PROJECT_DB_ADMIN_URL` role that has `CREATEDB`/`CREATEROLE` but is not superuser. Verified:
the role is not superuser and cannot create databases.

### 4.13 Backups

Every backup is encrypted with **age** (X25519) to `BACKUP_AGE_RECIPIENT`; unencrypted
backups are refused, and production configuration requires the recipient
([ADR 0009](adr/0009-age-encrypted-backups.md)). The private key never resides on the server
(it is pasted for a restore and kept only encrypted inside the restore job). Backup files
are 0600, written atomically, with a SHA-256 checksum verified before restore. Verified:
backups start with the `age-encryption.org/v1` header; a wrong key fails verification.

### 4.14 Logging hygiene

Structured JSON logs (`log/slog`). The handler's `ReplaceAttr` redacts attributes named
`password`, `token`, `secret`, `authorization`, `cookie`, `private_key`, `otp`, `code`,
`session` (`internal/platform/logging`, `TestRedactsSensitiveKeys`). Request logs contain
method, path (no query string), status, duration, IP and request ID. `pg_dump`/`pg_restore`
receive credentials via environment variables, never on the command line. Terminal content
and file contents are never logged. 5xx errors are logged server-side; clients receive only a
generic message and the request ID.

### 4.15 HTTP and input hardening

- Strict JSON decoding, 1 MiB body limit, 64 KiB header limit, 10 s header read timeout,
  60 s request timeout (non-streaming), panic recovery.
- Security headers on API responses (CSP `default-src 'none'`, `X-Frame-Options: DENY`,
  `nosniff`, `no-referrer`, CORP `same-origin`). The web console uses a nonce-based CSP
  (`apps/web/vite.config.ts`: `script-src 'self'`, `frame-ancestors 'none'`, `object-src 'none'`).
- SQL: parameterised queries everywhere; dynamic `ORDER BY` only from allowlists; search
  terms are escaped for `ILIKE`.
- Server-side validation of every field (regexes for usernames, slugs, keys, image
  references; numeric ranges; enum checks) with field-level 422 errors.
- The editor only opens known text file types up to `EDITOR_MAX_BYTES` and rejects binary
  content; downloads are `Content-Disposition: attachment` with `application/octet-stream`.
- SMTP header injection is rejected (CR/LF in recipient/subject).

## 5. Threat model (STRIDE)

| # | Threat | Category | Mitigation (code) | Residual |
|---|---|---|---|---|
| T1 | Password guessing / credential stuffing | Spoofing | Argon2id, lockout, Redis rate limits, MFA for admins (`auth/handlers.go`) | Lockout can be abused to lock out a known user (DoS) — §6 |
| T2 | Session theft via XSS or network sniffing | Spoofing | HttpOnly+Secure cookie, TLS, CSP, Svelte escaping, hashed tokens, idle/absolute TTL | Malware on the client machine |
| T3 | Session fixation | Spoofing | Rotation on MFA enable/verify; new session per login | — |
| T4 | CSRF from another site or a workspace app | Tampering | Origin check + per-session token; app proxy on a separate origin (`appproxy/proxy.go`) | Requires the Caddy route restriction on the proxy origin (§6) |
| T5 | IDOR/BOLA (access to other users' workspaces/files/DBs) | Info disclosure | Ownership/membership checks, access levels, 404 for no access (`workspaces/access.go`, `projects.Authorize`) | — |
| T6 | Privilege escalation via role assignment | Elevation | Role ranks, `CanGrant`, no self role/status change (`users.go`, `rbac.go`) | Super admins are fully trusted |
| T7 | Path traversal / symlink escape in the file manager | Info disclosure / Tampering | `CleanRelPath` + `os.Root` (`fsops.go`) | — |
| T8 | Arbitrary command execution on the host | Elevation | No exec op; allowlist; strict payload validation; fixed shell inside the user's container/scope | — |
| T9 | Forged or replayed requests to the agent | Spoofing / Tampering | Socket permissions + HMAC + timestamp window + nonce cache (`agentproto/sign.go`) | A process running as `itk-cloud` or root can read the shared secret |
| T10 | Container escape | Elevation | Non-root, no capabilities, no-new-privileges, AppArmor, seccomp, no Docker socket, PIDs limit | Shared kernel; kernel 0-days (§6) |
| T11 | Resource exhaustion (CPU, RAM, PIDs, disk, GPU) | DoS | cgroup limits, quotas, host-capacity check (90 % RAM, CPU overcommit), loopback volume size, idle stop, GPU exclusive allocations, disk alerts | Container writable layer and `/var/lib/docker` growth (§6) |
| T12 | Cross-workspace network access | Info disclosure | One bridge per workspace; ports on loopback only | Outbound traffic is not filtered (§6) |
| T13 | Access to user data by administrators | Info disclosure | View vs edit separation; emergency access only with reason, audited | Super admins and host root can read data |
| T14 | Tampering with the audit trail | Repudiation | Append-only triggers; transaction-bound writes | DB owner/superuser can disable triggers (§6) |
| T15 | Secrets leaked through logs or backups | Info disclosure | slog redaction, env-only DB credentials for tools, age-encrypted backups, private key offline | Config backup contains the env file (encrypted) |
| T16 | Backup theft | Info disclosure | age encryption, 0600 files, separate copy location | Loss of the private key = loss of backups |
| T17 | Malicious workspace web app attacking the console | Tampering | Separate origin, credential stripping, `itk_session` Set-Cookie dropped | Apps can set other cookies on the host name |
| T18 | Project DB role reading other projects | Info disclosure | Separate DB + role, CONNECT revoked from PUBLIC, no superuser | Shared instance: DB/role names are visible in catalogs; no CPU/IO isolation |
| T19 | Unauthorized terminal attach or hijack | Spoofing | Session bound to user; re-authorization on every connect; Origin check; 15 s revalidation | — |
| T20 | Lost admin MFA device | DoS | Recovery codes; reset by a higher-rank admin; `itkctl` for the first admin | If the only super admin loses both, a new admin must be created with `itkctl admin create` on the host |

## 6. Known limitations and residual risks

These are deliberate trade-offs or not-yet-implemented hardening. Operators must understand
them before production.

1. **The agent runs as root.** Loop-mounting volumes, chowning data, creating Linux users and
   using the Docker socket require it. Mitigations: tiny attack surface (Unix socket, HMAC,
   allowlist, strict validation), no network listener, a reduced capability bounding set.
   The unit intentionally does **not** use systemd options that create a private mount
   namespace (`ProtectSystem`, `PrivateTmp`, `ProtectHome`, …) because the agent's loop
   mounts must be visible to Docker in the host namespace. A bug in the agent is a host
   compromise.
2. **Docker group = root equivalent.** Never add users (or the `itk-cloud` service user) to
   the `docker` group; only the root agent uses the socket.
3. **Containers share the host kernel; they are not VMs.** Kernel vulnerabilities can lead to
   escapes. For truly untrusted or high-risk code consider gVisor/Kata or separate VMs
   (not implemented). Keep the kernel patched.
4. **Container writable layer is not quota-limited** unless `AGENT_CONTAINER_ROOTFS_SIZE` is
   set **and** Docker uses overlay2 on XFS mounted with `pquota`. Otherwise a user can fill
   `/var/lib/docker` by writing outside `/home/itk` (e.g. `/var/tmp`). Mitigate with a
   separate partition for `/var/lib/docker`, the `disk_full` alert, and/or read-only root
   filesystems in templates.
5. **The `directory` storage driver does not enforce quotas** (usage is only measured). It is
   intended for development; production must use `loopback`.
6. **Loopback-published app ports.** Workspace web apps are published on `127.0.0.1`
   only. JupyterLab requires a per-container token (`ITK_APP_TOKEN`, derived from
   `SESSION_SECRET` and injected by the app proxy after authorisation), so other local
   processes cannot use it. Other apps started by users (dev servers) have no such
   protection: any process on the host that can reach loopback (e.g. Linux-native
   workspaces, if enabled) could connect to them. Do not enable native workspaces for
   untrusted users.
7. **App-proxy origin isolation** is enforced by the API itself: on the `APP_PROXY_ORIGIN`
   host only `/api/v1/workspaces/*/proxy/*` is served (everything else returns 404), and the
   Caddy site for that origin forwards only those paths as a second layer. Apps can still
   set their own (non-session) cookies for the host name.
8. **Terminal reconnect starts a new shell**; processes started in the previous shell are
   killed with it (use `tmux` inside the workspace, or `nohup`, for long jobs).
9. **Login lockout semantics**: when the limit is reached, the account is locked and the
   failure counter resets to 0, so an attacker gets `login_max_attempts` guesses per lockout
   window (bounded additionally by 10 attempts per username per 15 min). Wrong passwords
   during the lockout do not extend it. Anyone who knows a username can keep that account
   locked (DoS); an administrator can clear it with `activate` or a password reset.
10. **The LAN is not trusted**, but there is no network-level client authentication: anybody
    on the LAN can reach the login page. TLS uses Caddy's internal CA, whose root must be
    distributed to lab clients; users who click through certificate warnings are exposed to
    MITM.
11. **Outbound network from containers is not filtered** (IP masquerading is enabled for
    package downloads). Containers can reach the LAN, the internet and host services bound
    to non-loopback addresses. The host firewall must allow only what workspaces need (e.g.
    PostgreSQL for project databases).
12. **Emergency stop stops containers only.** Shells of Linux-native workspaces are not
    killed by `emergency.stop_all` (their terminal sessions are closed in the database; the
    relay closes them at the next check).
13. **Audit append-only applies to the application.** The platform database role owns the
    table and could, if compromised, disable triggers; ship logs/audit exports off-host for
    stronger guarantees (not implemented).
14. **Project databases share one PostgreSQL instance**: data and privileges are isolated,
    CPU/IO/memory are not; catalog information (database and role names) is visible to all
    roles.
15. **Config backups contain `itk-cloud.env`** (encrypted with age). Whoever holds the backup
    private key can read all platform secrets.
16. The API returns `host_port` (loopback port) in container port listings; this is not a
    secret but reveals local port numbers.
17. Dependency and secret scanning are defined in CI (`.github/workflows/ci.yml`:
    `govulncheck`, `pnpm audit --prod --audit-level high`, gitleaks) but the repository has no
    commits yet, so the pipeline has not run; run `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`
    and `pnpm audit` manually before a release until CI is active.
19. When project databases are enabled the installer sets PostgreSQL
    `listen_addresses = '*'` (Docker bridge gateways are created on demand) and relies on
    `pg_hba.conf` (project roles `/^itk_` only from `10.213.0.0/16`, scram-sha-256) and UFW
    (5432 only from `10.213.0.0/16`). If the firewall is disabled, port 5432 answers on the
    LAN (authentication is still refused by `pg_hba.conf` for other sources).
18. Only TOTP MFA is supported (no WebAuthn/hardware keys).

## 7. Production release checklist (spec §30)

Status as of 2026-11-02. "Verified" means covered by automated tests in this repository;
items requiring the target hardware are open until checked on a staging server.

| Spec §30 item | Status | Evidence / what remains |
|---|---|---|
| All MVP acceptance criteria met | Verified | Integration tests plus the Playwright MVP journey, run on the dev stack and on the staging install. See [implementation-plan.md](implementation-plan.md#mvp-status-spec-24). |
| No sensitive endpoint without authorization | Implemented | Every private route passes `RequireAuth` and handler checks (reviewed for this document; endpoint/permission index in [api.md §10](api.md#10-endpoint-index-and-required-permissions)). Independent review pending. |
| Ownership checks tested | Verified | `TestContainerWorkspaceLifecycle`, `TestProjectDatabaseLifecycle`, `TestGPURequestApprovalFlow` (404/403 for other users). |
| Docker socket not exposed to the web application | Verified | Mount check in `TestContainerWorkspaceLifecycle`; only the agent uses the socket. |
| Host agent only runs allow-listed operations | Verified | `TestDecodePayloadRejectsUnknownOpsAndFields`, `TestAgentRejectsBadAuthAndStreamOpsOnOpsEndpoint`. |
| Resource limits actually applied | Verified | CPU/memory/swap/PIDs/caps verified with `docker inspect`; quotas (`TestQuotaEnforcement`); loopback storage on staging (6 GB write stops at the 5 GiB quota; direct-I/O loop devices prevent memory-cgroup OOM). |
| Workspaces and files isolated | Verified | Traversal/symlink tests (`fsops`, integration), access-level tests. |
| Login rate limit and session security active | Verified | `TestLoginRateLimit`, lockout and session revocation steps in `TestAuthMFAAndRBAC`. |
| Internal HTTPS active | Verified on staging | Caddy internal CA on 443 and the app-proxy port; E2E ran over HTTPS. Distribute the CA to lab clients ([installation.md §7](installation.md#7-tls-and-caddy)). |
| Firewall opens only required ports | Open | Rules documented in [installation.md §8](installation.md#8-firewall-and-ports); apply and verify with an external port scan on staging. |
| Secrets not stored in the repository | Implemented | No secrets in code or migrations; `.gitignore` excludes `.env*` (except `.env.example`), `.dev/`, `*.age`, `*-private-key.txt`, `bin/`; gitleaks is configured in CI. Confirm with the first CI run (the repository has no commits yet). |
| Backups scheduled and restore tested | Partly verified | Schedule implemented; encrypted backup + verify + project DB restore verified by tests. A full disaster-recovery drill on staging is pending ([backup-restore.md §8](backup-restore.md#8-rpo-rto-and-restore-testing-schedule)). |
| Service and host monitoring active | Partly | Built-in monitoring, services status and alerts verified on staging; optional Prometheus/Grafana (`--with-monitoring`) not yet exercised. |
| Installer and upgrade tested on staging | Verified (container staging) | Install on Ubuntu 24.04.5 plus upgrades 0.1.0→0.1.3 with pre-upgrade backups; repeat on the physical lab server. |
| Security test and basic load test passed | Mostly | Automated security regression tests in `tests/security` (IDOR across resource types, session fixation and cookie flags, secret leakage, injection inputs, unauthorized WebSocket, headers, body limits) plus the integration tests. Basic load test passed (`tests/load`: 50 clients, 4 447 req/s, 0 errors, p95 27 ms). A manual security test on the real server (external port scan, container breakout checks) is pending. |
| Installation and recovery documentation available | Done | [installation.md](installation.md), [backup-restore.md](backup-restore.md), [operations.md](operations.md). |
| Administrators understand emergency stop and recovery | Open | Training/drill with the lab administrators; procedures in [operations.md](operations.md#5-common-tasks). |
