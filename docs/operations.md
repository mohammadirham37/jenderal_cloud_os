# Operating ITK Cloud

Runbook for administrators of an installed ITK Cloud server: services and logs, monitoring
and alerts, settings, common tasks, troubleshooting and capacity planning.

Related: [installation](installation.md) · [backup & restore](backup-restore.md) ·
[security](security.md) · [workspaces](workspace.md) · [GPU](gpu.md) · [API](api.md).

## 1. Services and logs

| Unit | Process | Runs as | Notes |
|---|---|---|---|
| `itk-cloud-agent.service` | `/opt/itk-cloud/current/bin/itk-cloud-agent` | root | Env `/etc/itk-cloud/agent.env`; socket `/run/itk-cloud/agent.sock`; remounts workspace volumes at start. |
| `itk-cloud-api.service` | `itk-cloud-api` | itk-cloud | `127.0.0.1:8080`; env `/etc/itk-cloud/itk-cloud.env`. |
| `itk-cloud-worker.service` | `itk-cloud-worker` | itk-cloud | Jobs and periodic tasks; refuses to start with pending migrations. |
| `itk-cloud-web.service` | `node build/index.js` | itk-cloud | `127.0.0.1:3000`; env `/etc/itk-cloud/web.env`. |
| `itk-cloud.target` | — | — | Groups the four services. |
| `caddy`, `postgresql`, `redis-server`, `docker` | system packages | — | |

```bash
systemctl status 'itk-cloud-*'
sudo systemctl restart itk-cloud-api                       # one service
sudo systemctl stop itk-cloud-web itk-cloud-worker itk-cloud-api itk-cloud-agent   # stop order
sudo systemctl start itk-cloud-agent itk-cloud-api itk-cloud-worker itk-cloud-web  # start order
journalctl -u itk-cloud-api -f                             # follow
journalctl -u 'itk-cloud-*' --since '1 hour ago' -o cat | jq -c 'select(.level=="ERROR")'
journalctl -u itk-cloud-agent -o cat | jq -c 'select(.msg=="agent operation")'
sudo tail -f /var/log/caddy/itk-cloud-access.log | jq -c '{ts,status,request: .request.uri}'
```

**Log format.** All Go services write one JSON object per line (`log/slog`) to stdout →
journald. Common fields: `time`, `level`, `msg`, `service` (`itk-cloud-api`,
`itk-cloud-worker`, `itk-cloud-agent`). API request lines (`msg: "http request"`) carry
`request_id`, `method`, `path` (no query string), `status`, `bytes`, `duration_ms`, `ip`.
Sensitive attribute names (`password`, `token`, `secret`, `cookie`, `code`, …) are redacted.

**Correlation.** Each API response has an `X-Request-ID` header; error bodies include
`request_id`; the same ID appears in the API log, in `audit_logs.request_id` and as
`request_id` in the agent's operation log. Worker jobs use `job-<job id>` as request ID.

```bash
RID=9f2c1d0e8b7a4c3d2e1f0a9b8c7d6e5f
journalctl -u 'itk-cloud-*' -o cat | grep "$RID"
sudo -u postgres psql itk_cloud -c "SELECT occurred_at, actor_username, action, outcome, reason FROM audit_logs WHERE request_id='$RID'"
```

**Health.**

```bash
curl -s http://127.0.0.1:8080/health/live      # {"status":"ok"}
itkctl health                                  # /health/ready: database, redis, migrations, version
```

The console's *Monitoring → Services* (`GET /api/v1/monitoring/services`) shows api,
postgresql, redis, worker (Redis heartbeat, 90 s TTL), agent (last heartbeat ≤ 2 min,
protocol, Docker), web (`WEB_HEALTH_URL`), queue statistics, last backup and container counts.

## 2. Audit log

*Administration → Audit log* (`audit.view`) lists every administrative and sensitive action
with actor, IP, user agent, outcome (`success`/`failure`/`denied`), reason and request ID.
Filters: actor, action (`workspace.*` wildcards), resource, outcome, time range. Entries are
append-only and kept for `audit.retention_days` (default 365). Review regularly at least:
`workspace.emergency_access`, `host.emergency_stop`, `user.reset_mfa`, `backup.restore`,
`settings.update`, `role.*`, and `auth.login` failures/denials.

## 3. Monitoring and alerts

Built-in monitoring is sampled by the worker every `monitoring.sample_interval_seconds`
(default 30 s) and kept for `monitoring.retention_days` (default 14). Unavailable metrics
are shown as unavailable, never invented.

Alert rules (evaluated each sample; a new firing alert notifies all holders of
`monitoring.view_host`; alerts resolve automatically and can be acknowledged):

| Rule | Severity | Condition | Setting |
|---|---|---|---|
| `agent_offline` | critical | Host agent unreachable | — |
| `disk_full` (per mount point) | critical | Filesystem usage ≥ threshold | `alerts.disk_pct` (90) |
| `memory_critical` | critical | Host memory usage ≥ threshold | `alerts.memory_pct` (90) |
| `cpu_sustained_high` | warning | Average host CPU over the window ≥ threshold (needs ≥ 3 samples) | `alerts.cpu_pct` (90), `alerts.cpu_sustain_minutes` (10) |
| `docker_unavailable` | critical | Docker not reachable by the agent | — |
| `gpu_temperature` (per GPU) | critical | Temperature ≥ threshold | `alerts.gpu_temp_c` (85) |
| `gpu_overutilized` (per GPU) | warning | 10-minute average utilisation ≥ threshold | `alerts.gpu_util_pct` (98) |
| `queue_backlog` | warning | Due queued jobs ≥ threshold | `alerts.queue_backlog` (50) |
| `jobs_failed` | warning | Jobs that went `dead` in the last hour > 0 | — |
| `backup_failed` | critical | Last scheduled backup failed | (when `backup.schedule_enabled`) |
| `backup_missing` | warning | No successful backup for 26 h (once the install is older than 26 h) | (when `backup.schedule_enabled`) |
| `service_down` (`worker`) | critical | Worker heartbeat missing | — |

Note: rules are evaluated by the worker; if the worker itself is down, `service_down` is
visible in *Monitoring → Services* (evaluated live by the API) but no new alerts are raised.
Use the optional Prometheus stack or an external check for independent alerting.

### Optional Prometheus and Grafana

Installed with `installer/install.sh --with-monitoring` (files in `deployments/monitoring`):
Prometheus `127.0.0.1:9090` (scrape interval 30 s), node_exporter `127.0.0.1:9100` (with the
systemd collector), Grafana `127.0.0.1:3001` published by Caddy at `https://<domain>:3443`
(change the Grafana admin password at first login). Rules in
`/etc/prometheus/itk-cloud-rules.yml`: `HostDiskAlmostFull`, `HostMemoryCritical`,
`HostHighCPU`, `ItkCloudServiceFailed` (failed `itk-cloud-*`, PostgreSQL, Redis, Docker, Caddy
units), `NodeExporterDown`. No Alertmanager is configured — alerts are visible in the
Prometheus UI/Grafana; add Alertmanager (email/Telegram) if needed. A DCGM exporter job for
GPUs is prepared but commented out. Set `PROMETHEUS_URL`/`GRAFANA_URL` in the env file to show
links in the console.

## 4. Platform settings

*Administration → Settings* (`settings.manage`, super_admin) or `PATCH /api/v1/admin/settings`.
Values are type- and range-checked; every change is audited with old and new values.

| Key | Default | Range | Meaning |
|---|---|---|---|
| `platform.name` | ITK Cloud | ≤ 60 chars | Display name |
| `platform.organization` | Informatika dan Teknik Komputer | ≤ 120 chars | Organisation |
| `security.require_mfa_for_admins` | true | bool | Enforce MFA for roles with `requires_mfa` |
| `security.login_max_attempts` | 5 | 3–20 | Failed logins before lockout |
| `security.lockout_minutes` | 15 | 1–1440 | Lockout duration |
| `quota.default_package` | basic | existing key | Package for users without one |
| `workspaces.idle_stop_enabled` | true | bool | Idle/max-duration stop |
| `workspaces.max_per_user_hard_limit` | 20 | 1–200 | Absolute workspace limit per user |
| `scheduler.cpu_overcommit_pct` | 200 | 100–1000 | Allocatable CPU as % of host threads |
| `gpu.release_after_stopped_minutes` | 60 | 5–10080 | Release GPUs of non-running workspaces |
| `gpu.max_duration_hours` | 72 | 1–720 | Max GPU allocation duration |
| `gpu.allow_shared` | false | bool | Allow shared GPU allocations |
| `backup.schedule_enabled` | true | bool | Daily backup |
| `backup.schedule_hour_utc` | 18 | 0–23 | Hour of the daily backup (UTC) |
| `backup.include_workspace_files` | false | bool | Include workspace files in scheduled backups |
| `monitoring.sample_interval_seconds` | 30 | 10–600 | Sampling interval (read at worker start) |
| `monitoring.retention_days` | 14 | 1–365 | Metric history |
| `alerts.*` | see §3 | | Alert thresholds |
| `audit.retention_days` | 365 | 30–3650 | Audit log retention |

## 5. Common tasks

### Add users

*Administration → Users → New user* (`users.create`): username (lowercase, 3–32 chars),
email, full name, optional NIM/NIP (`identity_number`), roles (only below your own rank),
optional quota package. Leave the password empty to get a one-time temporary password (shown
once); the user must change it at first login. Hand it over through a trusted channel.
Roles: `student`, `assistant`, `lecturer`, `admin`, `super_admin` (or custom roles created with
`roles.manage`). Users can be found with search and filters (status, role).

### Tune quota packages from the hardware inventory

1. Fill the inventory ([installation.md §2](installation.md#2-hardware-inventory-spec-29)).
2. Decide the concurrency target (e.g. a practicum of N students at once) and size packages
   so that N × package memory ≤ 0.9 × RAM − platform reserve (§7).
3. Edit/create packages in *Administration → Quotas* (`quotas.manage`); set the default with
   `quota.default_package`. Changes apply to new checks immediately; existing workspaces keep
   their CPU/memory/storage until changed.
4. Assign packages to users (`PATCH /users/{id}` `quota_key`) or projects.

### Rotate secrets

| Secret | Procedure | Impact |
|---|---|---|
| `AGENT_SHARED_SECRET` | `itkctl gen-secrets` → set the new value in **both** `/etc/itk-cloud/itk-cloud.env` and `/etc/itk-cloud/agent.env`; restart agent, api, worker | Open terminals disconnect |
| `SESSION_SECRET` | Replace (≥ 32 chars); restart api/worker | None (not used for signing) |
| `ENCRYPTION_KEY` | No in-place re-encryption exists. Only if compromised: set a new key, restart, then force MFA re-enrollment (`DELETE FROM mfa_methods;` as postgres on `itk_cloud`) and revoke sessions (`UPDATE sessions SET revoked_at=now() WHERE revoked_at IS NULL;`); project DB passwords remain valid but can no longer be decrypted (maintainers use *Reset credentials* when needed) | All users re-enroll MFA |
| Platform DB password | `ALTER ROLE itk_cloud PASSWORD '...'`; update `DATABASE_URL` (and `ITK_DB_PASSWORD`); restart api/worker | Brief outage |
| Project DB admin password | `ALTER ROLE itk_projects_admin PASSWORD '...'`; update `PROJECT_DB_ADMIN_URL`; restart | — |
| Redis password | Update `requirepass` in `/etc/redis/itk-cloud.conf` and `REDIS_URL`; restart redis-server, api, worker | Rate-limit counters reset |
| Backup age key | See [backup-restore.md §2](backup-restore.md#2-encryption-keys) | Keep old private key until old backups expire |
| User passwords / MFA | Admin reset link / `reset-mfa` (below) | — |

### Emergency stop

When workloads endanger the server (runaway load, abuse, security incident):
*Administration → Host → Emergency stop* (`host.operations.execute`, super_admin), type
`STOP ALL WORKLOADS` and a reason (≥ 10 chars). All running ITK-managed containers are stopped
(10 s grace), running workspaces become `stopped`, terminal sessions close, every user gets a
critical notification, and the action is audited. Native workspace shells are not killed by
this action — stop them via `systemctl stop itk-native.slice` if needed. Afterwards
investigate (audit log, monitoring history, `journalctl`), then let users start workspaces
again individually. Emergency stop via the API:

```bash
curl -sk -b $JAR -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -d '{"confirm":"STOP ALL WORKLOADS","reason":"runaway jobs saturating memory"}' \
  https://itk-cloud.lab/api/v1/admin/host/emergency-stop
```

If the console itself is unreachable: `sudo docker ps -q --filter label=itk.managed=true | xargs -r sudo docker stop`
(the next metrics run reconciles the workspace states).

### Suspend a user

*Users → Suspend* (`users.suspend`, reason required): sessions are revoked immediately,
running workspaces are suspended, terminals close, and login returns "account suspended".
*Activate* restores login (and clears a lockout); suspended workspaces must be started by an
administrator with `workspaces.manage_all` or deleted.

### Retry failed jobs and workspaces

- *Monitoring → Jobs* (`monitoring.view_host`) lists `dead`, `queued` or `running` jobs with
  attempts and last error; *Retry* (`settings.manage` or `workspaces.manage_all`) re-queues a
  dead job with attempts reset.
- A `failed` workspace has *Retry* (manage access), which schedules provisioning again after
  the cause (missing image, agent down, disk full) is fixed.
- `GET /api/v1/workspaces/{id}/jobs` shows a workspace's recent jobs and errors.

### Rebuild workspace images

```bash
cd /opt/itk-cloud/current && sudo installer/build-workspace-images.sh python-dev
```

Then *Images → Check / pull*. Running containers keep the old image until recreated; a
workspace's primary container is recreated when its CPU/memory is changed or its GPU
assignment changes. New workspaces use the new image. Remove unused old layers with
`docker image prune` (never `docker system prune -a` on a production host without checking).

### Reset MFA, unlock accounts, reset passwords

- Lost authenticator: the user signs in with a recovery code; otherwise an administrator of
  higher rank uses *Users → Reset MFA* (reason required) — the user enrolls again at next
  login.
- The only super administrator lost MFA: on the server create another super administrator
  with `sudo itkctl admin create …`,
  log in with it, reset the old account's MFA, and later suspend the temporary account.
- Locked account (`423`): wait `security.lockout_minutes`, or *Activate* / issue a reset link.
- Password reset without SMTP: *Users → Reset password* (`users.reset_password`) returns a
  one-time link valid for 1 hour (`…/reset-password#token=…`); give it to the user through a
  trusted channel. With SMTP configured, the link is also emailed and users can use *Forgot
  password* themselves.

### Announcements and maintenance

*Notifications → Announcements* (`announcements.manage`): title, body, severity, start/end,
optionally notify every active user. Use them before upgrades and maintenance windows.

## 6. Troubleshooting

| Symptom | Diagnosis | Fix |
|---|---|---|
| **Agent offline** — alert `agent_offline`, 503 "The host agent is unavailable", agent status `offline` | `systemctl status itk-cloud-agent`; `journalctl -u itk-cloud-agent -n 50`; `ls -l /run/itk-cloud/agent.sock` (srw-rw---- root itk-cloud) | Start/restart the agent; fix config errors printed at startup (secret < 32 chars, relative paths, invalid storage driver). |
| Agent reachable but 403 "The host agent refused the operation." and agent log "rejected unauthenticated agent request" | Signature/timestamp mismatch | Make `AGENT_SHARED_SECRET` identical in `itk-cloud.env` and `agent.env`; check clock sync (`timedatectl`, window ±60 s); restart agent, api, worker. |
| Agent `degraded` "protocol version mismatch" | Agent and API from different releases | Upgrade/restart all services from the same release. |
| **Docker unavailable** — alert `docker_unavailable`, agent `degraded` "container runtime unavailable" | `systemctl status docker`; `docker info`; agent log "docker API … older than required 1.44" | Start Docker; upgrade to Docker Engine 25+ (Engine API ≥ 1.44). |
| **Provisioning failed: "image itk-cloud/… is not available on the server"** | Image not built | `sudo installer/build-workspace-images.sh <name>`, *Check / pull*, then *Retry* the workspace. |
| Provisioning failed with `mkfs.ext4 failed` / `mount failed` | Loop devices or disk space | `losetup -f` (free loop device), `lsmod \| grep loop`, `df -h /srv/itk-cloud`; check the agent runs as root with the `loopback` driver. |
| Workspace stuck in `provisioning` with `pending_job` | Agent down; job retrying with backoff (5 s … 30 min) | Fix the agent; the job continues. Check *Monitoring → Jobs*. |
| **Quota errors** (`422 quota_exceeded`) | Message names the limit: workspace/container count, CPU/memory (running), allocated storage, GPU, databases, terminals | `GET /api/v1/me/usage`; stop running workspaces (frees CPU/memory), delete unused workspaces (frees storage and counts), or assign a larger package. |
| `503 insufficient_capacity` on start | Host memory ≥ 90 % allocated or CPU over the overcommit limit | Stop idle workloads; tune `scheduler.cpu_overcommit_pct`; review packages (§7). |
| `507 quota_exceeded` on file write | Workspace volume full | User deletes files; volume size cannot be grown in place — create a new workspace with more storage and move data. |
| **Terminal not connecting** | 409 "Start the workspace…", 410 session ended, close reason in the UI | Start the workspace; open a new terminal; check `max_terminal_sessions`. If every WebSocket fails, users may be using another hostname/IP than `APP_BASE_URL` (Origin check) — use the configured URL. Check Caddy routes `/api/*` to the API. |
| Terminal disconnects after a while | `idle_timeout` (`TERMINAL_IDLE_TIMEOUT_MINUTES`), session TTL, workspace stopped, logout | Expected; reconnecting starts a **new** shell (use `tmux` for long jobs). |
| **App proxy**: 503 "not configured" | `APP_PROXY_ORIGIN` empty | Set it (installer sets `https://<domain>:8443`) and restart the API. |
| App proxy 404 | URL on the console origin, port not in the template, or container not running | Use `https://<domain>:<proxy-port>/api/v1/workspaces/<id>/proxy/<port>/`; add the port to the template; start the workspace. |
| App proxy 502 `app_unreachable` | Nothing listens on that port inside the container | Start the app bound to `0.0.0.0` (e.g. `vite --host 0.0.0.0`); JupyterLab starts automatically; check `docker logs` of the container if it does not answer. |
| App proxy asks to log in / 401 | Browser did not send the session cookie to the proxy origin | Use the same hostname as the console; trust the internal CA for port 8443 too. |
| Login: 503 "Login is temporarily unavailable." | Redis unreachable (login rate limiting fails closed) | `systemctl status redis-server`; check `REDIS_URL` password. |
| `/health/ready` 503 `migrations: pending`; worker exits "database schema is not up to date" | New release without migrations applied | `sudo itkctl migrate`. |
| Jobs not processed, `service_down` worker | Worker stopped/crashing | `journalctl -u itk-cloud-worker`; restart. Stale `running` jobs are re-queued automatically after 35 min. |
| Backups failing | Error text in *Backups*: `BACKUP_AGE_RECIPIENT is not configured`, `pg_dump not found; install postgresql-client`, permission denied on `BACKUP_COPY_DIR`, "there are no workspaces to back up" | Configure the recipient; install `postgresql-client`; add `ReadWritePaths` for the copy dir; ignore the last one. |
| Workspace files missing after reboot | Volumes are remounted by the agent at startup and on demand | `findmnt \| grep /srv/itk-cloud/workspaces`; agent log "remount workspace volume". |
| Disk almost full (`disk_full`) | Workspace volumes (sparse), Docker images/writable layers, backups | `du -sh /srv/itk-cloud/* /var/lib/docker`; prune unused images; lower retention or move backups; see residual risk 4 in [security.md](security.md#6-known-limitations-and-residual-risks). |
| GPU problems | | See [gpu.md §9](gpu.md#9-troubleshooting). |
| Certificate warnings | Client does not trust Caddy's internal CA | Install the root certificate ([installation.md §7](installation.md#7-tls-and-caddy)). |

## 7. Capacity planning

What the platform enforces:

- **Memory is not overcommitted**: starts are refused when running workload memory would
  exceed 90 % of host RAM. The remaining 10 % — plus whatever the workloads do not use — must
  hold the OS, PostgreSQL, Redis, the four ITK services, Docker and the page cache. Measure
  the platform's own footprint on your server (`systemd-cgtop`, `free -h`) and keep enough
  headroom; reduce packages if the host swaps.
- **CPU is overcommitted** up to `scheduler.cpu_overcommit_pct` (200 %) of host threads, since
  interactive workloads rarely use their full limit. Lower it for CPU-heavy courses.
- **Storage is allocated, not reserved**: loopback volumes are sparse, so the sum of
  `storage_bytes` can exceed the physical disk. Either keep total allocations ≤ data disk
  size, or overcommit consciously and watch `disk_full` and `used_storage_bytes`.
- **GPUs**: the number of concurrent exclusive allocations equals the number of GPUs.

Sizing worksheet (fill with your inventory values):

| Quantity | Formula |
|---|---|
| Usable workload RAM | 0.9 × RAM (scheduler limit); plan below it to leave room for the platform |
| Concurrent running workspaces (memory-bound) | usable RAM ÷ average workspace memory limit |
| Concurrent running workspaces (CPU-bound) | threads × overcommit ÷ average CPU limit |
| Data disk for workspaces | Σ package storage of expected users (or the overcommit you accept) + growth |
| Docker images | Σ built image sizes (`docker image ls 'itk-cloud/*'`) + writable layers |
| Backups on the server | retention days × (platform DB + project DBs + config/metadata + workspace file archives when enabled) |

Example only (not a recommendation): with 128 GiB RAM, 32 threads and the seeded `basic`
package (1 core, 1 GiB), memory allows about 0.9 × 128 ≈ 115 GiB of running workloads, i.e.
~100 basic workspaces after reserving platform headroom, while CPU (32 × 2 = 64 cores
allocatable) allows 64 — CPU would be the binding limit at the default overcommit.

Review monthly: peak running workspaces, memory/CPU history (*Monitoring → History* 7d/14d),
`insufficient_capacity` and `quota_exceeded` frequency, disk growth, and backup sizes.
