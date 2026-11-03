# Installing Jenderal Cloud OS on Ubuntu Server 24.04 LTS

Jenderal Cloud OS is installed **natively**: the API, worker, host agent and web console run as
systemd services on the host, with PostgreSQL, Redis and Caddy from apt. Docker Engine is
installed only as the runtime for **user container workspaces**; the platform itself does not
run in containers ([ADR 0004](adr/0004-native-install-no-docker-for-platform.md)).

Related: [operations](operations.md) · [security](security.md) · [backup & restore](backup-restore.md) ·
[GPU](gpu.md) · [workspaces](workspace.md) · [architecture](architecture.md).

## 1. Prerequisites

### 1.1 Server

| Requirement | Minimum enforced by the installer | Recommendation |
|---|---|---|
| OS | Ubuntu Server **24.04 LTS** (`/etc/os-release` checked) | Fresh, fully updated install |
| Architecture | amd64 or arm64 | amd64 (NVIDIA driver/toolkit support) |
| CPU | 2 cores | Size from the expected concurrent workloads (§2) |
| RAM | 4 GB (warning below 8 GB) | ≥ 32 GB for a lab |
| Free disk on `/srv/itk-cloud` | 20 GB | Dedicated data volume; separate partition for `/var/lib/docker` |
| Ports | 443 and the app-proxy port (default 8443) free | — |
| Network | LAN address, DNS name for the console | Static IP |
| GPU (optional) | Working `nvidia-smi` if `--with-gpu` | Driver installed and verified before running the installer ([gpu.md](gpu.md)) |
| Internet access during install | apt repositories, download.docker.com, nodejs.org (or `--node-tarball`), NVIDIA repo for GPU, Docker Hub base images for workspace images | — |

The installer stops when a critical prerequisite is not met.

### 1.2 Release bundle

The installer runs from an extracted release directory built with `make release`
(`dist/itk-cloud-<VERSION>-linux-<arch>.tar.gz`), which contains `bin/` (`itk-cloud-api`,
`itk-cloud-worker`, `itk-cloud-agent`, `itkctl`), `web/build`, `deployments/`, `installer/`,
`docs/`, `VERSION`.

```bash
make release VERSION=$(cat VERSION) GOARCHS="amd64"    # on a build machine with Go and pnpm
scp dist/itk-cloud-0.1.3-linux-amd64.tar.gz{,.sha256} admin@server:
ssh admin@server
sha256sum -c itk-cloud-0.1.3-linux-amd64.tar.gz.sha256
tar xzf itk-cloud-0.1.3-linux-amd64.tar.gz && cd itk-cloud-0.1.3-linux-amd64
sudo ./installer/install.sh
```

## 2. Hardware inventory (spec §29)

Fill this table **before** production and size quota packages from it — never from the
example packages (Basic/Standard/Research) seeded by the migration.

| Item | Command | Value |
|---|---|---|
| CPU model, cores/threads | `lscpu \| grep -E 'Model name\|^CPU\(s\)\|Thread\|Core'` | |
| RAM total | `free -h` | |
| SSD/HDD type and capacity | `lsblk -o NAME,SIZE,TYPE,ROTA,MODEL` (ROTA=0 → SSD) | |
| Filesystems and mount points | `df -hT; findmnt /srv /var/lib/docker` | |
| NVIDIA GPU model and count | `nvidia-smi -L` | |
| VRAM per GPU | `nvidia-smi --query-gpu=index,name,memory.total --format=csv` | |
| NVIDIA driver version | `nvidia-smi --query-gpu=driver_version --format=csv,noheader` | |
| Compatible CUDA version | `nvidia-smi` (header "CUDA Version") | |
| MIG support/mode | `nvidia-smi --query-gpu=mig.mode.current --format=csv` | |
| Expected active users | (lab administration) | |
| Expected concurrent workloads | (lab administration) | |
| LAN capacity | `ethtool <iface> \| grep Speed` | |
| Backup storage location (other disk/NAS/server) | (see [backup-restore.md](backup-restore.md)) | |

Capacity planning guidance is in [operations.md §7](operations.md#7-capacity-planning).

## 3. What `installer/install.sh` does

```bash
sudo ./installer/install.sh                    # interactive
sudo ./installer/install.sh --non-interactive --yes --domain itk-cloud.lab \
     --admin-user admin --admin-email admin@lab.example --admin-name "Lab Admin"
sudo ./installer/install.sh --help             # all options
```

Options (from `installer/install.sh --help`): `--domain`, `--proxy-port` (default 8443),
`--admin-user/--admin-email/--admin-name` (password prompted, from `ITK_ADMIN_PASSWORD`, or
generated and shown once), `--storage-driver loopback|directory`,
`--docker-source docker-official|ubuntu`, `--with-gpu/--no-gpu` (default: auto-detect),
`--with-monitoring`, `--enable-native`, `--project-databases/--no-project-databases`
(default enabled), `--ssh-from CIDR`, `--no-firewall`, `--images none|core|all|<list>`
(core = `ubuntu-dev,python-dev`), `--node-tarball FILE` (offline Node.js), `--non-interactive`,
`--yes`. Re-running is safe: existing secrets, data and configuration are kept. The install
log is `/var/log/itk-cloud-install.log` (0600).

Flow:

1. **Preflight**: Ubuntu 24.04, amd64/arm64, release files present and runnable, ≥ 2 CPUs,
   ≥ 4 GB RAM, ≥ 20 GB free on `/srv/itk-cloud`, ports 443/proxy port free, valid options,
   GPU auto-detection (`nvidia-smi -L`). Prints a summary and asks for confirmation.
2. **Packages**: `ca-certificates curl gnupg jq uuid-runtime e2fsprogs util-linux acl ufw
   postgresql postgresql-contrib redis-server caddy`.
3. **Docker Engine** (for workspaces only): `docker-ce` from download.docker.com (default)
   or Ubuntu's `docker.io`; installs `deployments/config/docker-daemon.json` if no
   `daemon.json` exists (address pool `10.213.0.0/16` split into /24, `no-new-privileges`,
   `live-restore`, json-file log limits, `userland-proxy: false`); requires Engine API ≥ 1.44.
4. **NVIDIA Container Toolkit** when GPU support is on (NVIDIA apt repo,
   `nvidia-ctk runtime configure --runtime=docker`).
5. **Node.js** runtime for the web console, pinned in `installer/lib.sh` (`NODE_VERSION`),
   SHA-256 verified, unpacked to `/opt/itk-cloud/node`.
6. **Accounts and directories**: system user/group `itk-cloud` (home `/var/lib/itk-cloud`,
   no login shell), group `itk-workspace` (native workspace users),
   `/srv/itk-cloud` and `/srv/itk-cloud/workspaces` (0711 root), `/srv/itk-cloud/backups`
   (0770 root:itk-cloud), `/etc/itk-cloud` (0750 root:itk-cloud), `/var/lib/itk-cloud`
   (0750 itk-cloud).
7. **Release files** to `/opt/itk-cloud/releases/<VERSION>`, symlink
   `/opt/itk-cloud/current`, `/usr/local/bin/itkctl` installed as a root-only wrapper that loads `/etc/itk-cloud/itk-cloud.env` (use `sudo itkctl …`).
8. **PostgreSQL**: role `itk_cloud` with a random password, database `itk_cloud`
   (`pgcrypto`), `DATABASE_URL`. With project databases: role `itk_projects_admin`
   (`CREATEDB CREATEROLE NOSUPERUSER`), `PROJECT_DB_ADMIN_URL`, a `pg_hba.conf` rule
   `host /^itk_ /^itk_ 10.213.0.0/16 scram-sha-256`, `listen_addresses = '*'`,
   `PROJECT_DB_HOST=host.docker.internal`.
9. **Redis**: `/etc/redis/itk-cloud.conf` (bind 127.0.0.1/::1, protected mode,
   random `requirepass`, `maxmemory 512mb`, `allkeys-lru`), `REDIS_URL`.
10. **Configuration and secrets** in `/etc/itk-cloud/itk-cloud.env` (0640 root:itk-cloud):
    `SESSION_SECRET`, `ENCRYPTION_KEY`, `AGENT_SHARED_SECRET` generated once from
    `/dev/urandom` and never printed; production values (`APP_ENV=production`,
    `APP_BASE_URL=https://<domain>`, `APP_PROXY_ORIGIN=https://<domain>:<proxy-port>`,
    `COOKIE_SECURE=true`, paths, feature flags, `WEB_HEALTH_URL=http://127.0.0.1:3000/`).
    `/etc/itk-cloud/agent.env` (0600 root) carries only the agent variables;
    `/etc/itk-cloud/web.env` (0640) sets `HOST=127.0.0.1 PORT=3000 ORIGIN=<base URL>`.
11. **Backup key**: if `BACKUP_AGE_RECIPIENT` is empty, runs `itkctl backup keygen`, stores
    the public recipient in the env file and writes the **private key** to
    `/root/itk-cloud-backup-private-key.txt` (0400) with instructions to move it offline and
    `shred -u` it.
12. **systemd units** `itk-cloud-{agent,api,worker,web}.service` and `itk-cloud.target`
    (from `deployments/systemd`). The agent unit deliberately avoids options that create a
    private mount namespace (`ProtectSystem`, `PrivateTmp`, …): its loop mounts of workspace
    volumes must be visible to Docker in the host namespace.
13. **Migrations** with `itkctl migrate` (as `itk-cloud`), then services start in the order
    agent → api → worker → web and are checked (agent socket, `/health/ready`, web on
    `127.0.0.1:3000`).
14. **Caddy** from `deployments/caddy/Caddyfile.tmpl` (internal TLS, see §7); optional
    Grafana site on `:3443`.
15. **Firewall (UFW)** unless `--no-firewall` (§8).
16. **Optional monitoring** (`--with-monitoring`): Prometheus, node_exporter, Grafana on
    localhost, provisioning from `deployments/monitoring`.
17. **Workspace images** via `installer/build-workspace-images.sh` (§9).
18. **First super administrator** via `itkctl admin create --password-stdin` if none exists.
19. **Final health check**: `itkctl health` and an HTTPS request through Caddy using the
    internal CA; prints the console URL, the CA certificate path and next steps.

## 4. Manual installation outline

If the installer cannot be used, these are the equivalent essential steps (as root):

```bash
apt-get install -y postgresql postgresql-contrib redis-server caddy e2fsprogs ufw
# Docker Engine for workspaces (Docker's apt repo), then:
install -m 0644 deployments/config/docker-daemon.json /etc/docker/daemon.json && systemctl restart docker
groupadd --system itk-cloud && useradd --system --gid itk-cloud --home-dir /var/lib/itk-cloud --shell /usr/sbin/nologin itk-cloud
groupadd itk-workspace
install -d -m 0711 /srv/itk-cloud /srv/itk-cloud/workspaces
install -d -m 0770 -g itk-cloud /srv/itk-cloud/backups
install -d -m 0750 -g itk-cloud /etc/itk-cloud
install -d -m 0750 -o itk-cloud -g itk-cloud /var/lib/itk-cloud
sudo -u postgres createuser -P itk_cloud && sudo -u postgres createdb -O itk_cloud itk_cloud
sudo -u postgres psql -d itk_cloud -c 'CREATE EXTENSION IF NOT EXISTS pgcrypto'
cp .env.example /etc/itk-cloud/itk-cloud.env && chown root:itk-cloud /etc/itk-cloud/itk-cloud.env && chmod 0640 /etc/itk-cloud/itk-cloud.env
itkctl gen-secrets          # paste SESSION_SECRET, ENCRYPTION_KEY, AGENT_SHARED_SECRET into the env file
itkctl backup keygen        # paste BACKUP_AGE_RECIPIENT; store the private key OFFLINE
# edit APP_ENV=production, APP_BASE_URL, APP_PROXY_ORIGIN, COOKIE_SECURE=true, DATABASE_URL, REDIS_URL ...
sudo itkctl migrate
# install deployments/systemd/*.service and itk-cloud.target, write /etc/itk-cloud/agent.env (0600) and web.env
systemctl daemon-reload && systemctl enable --now itk-cloud-agent itk-cloud-api itk-cloud-worker itk-cloud-web
sudo itkctl admin create \
     --username admin --email admin@lab.example --name "Lab Admin"
```

Important: create `/srv/itk-cloud/backups` with group `itk-cloud` **before** the agent's
first start; the agent creates missing directories as root with mode 0700, which the worker
could then not write to.

## 5. Configuration reference

The API, worker and `itkctl` read `ITK_CLOUD_ENV_FILE` (or `.env` in the working directory)
and the process environment (real environment variables win). The agent reads only the
process environment (systemd `EnvironmentFile=/etc/itk-cloud/agent.env`). Invalid values stop
the process with a list of errors.

### API, worker, itkctl (`/etc/itk-cloud/itk-cloud.env`)

| Variable | Default | Notes |
|---|---|---|
| `APP_ENV` | `development` | `development`, `test` or `production`. Production requires an `https` base URL, `COOKIE_SECURE=true` and `BACKUP_AGE_RECIPIENT`; API/worker refuse to run as root. |
| `APP_BASE_URL` | `http://localhost:5173` | Console origin; used for CSRF Origin checks, WebSocket origin, reset links. |
| `APP_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |
| `APP_PROXY_ORIGIN` | empty (proxy disabled) | e.g. `https://itk-cloud.lab:8443`; must be an origin without path and differ from the base URL host. |
| `COOKIE_SECURE` | `true` when base URL is https | Must be true in production. |
| `API_HOST` / `API_PORT` | `127.0.0.1` / `8080` | Keep on loopback. |
| `DATABASE_URL` | — (required) | Platform database. |
| `REDIS_URL` | — (required) | e.g. `redis://:<pass>@127.0.0.1:6379/0`. |
| `SESSION_SECRET` | — (required, ≥ 32 chars) | Validated at startup; session tokens are random and stored hashed, so it is currently not used for signing (reserved). |
| `ENCRYPTION_KEY` | — (required) | 32 random bytes, base64 (std or URL encoding). Encrypts TOTP secrets, project DB passwords, restore identities. **Changing it makes those secrets unreadable.** |
| `SESSION_TTL_HOURS` | `12` | Absolute session lifetime. |
| `SESSION_IDLE_MINUTES` | `120` | Idle timeout. |
| `AGENT_SOCKET_PATH` | `/run/itk-cloud/agent.sock` | Absolute path. |
| `AGENT_SHARED_SECRET` | — (required, ≥ 32 chars) | Must equal the agent's value. |
| `WORKSPACE_ROOT` | `/srv/itk-cloud/workspaces` | Absolute; also used to pick the disk shown in metrics. |
| `WORKSPACE_DEFAULT_DRIVER` | `docker` | `docker` (container workspaces) or `native` (requires `WORKSPACE_NATIVE_ENABLED=true`). |
| `WORKSPACE_NATIVE_ENABLED` | `false` | Linux-native workspaces ([workspace.md §9](workspace.md#9-linux-native-workspaces)). |
| `UPLOAD_MAX_BYTES` | `104857600` (100 MiB) | Per uploaded file. |
| `EDITOR_MAX_BYTES` | `1048576` (1 MiB) | Browser editor read/write limit. |
| `TERMINAL_SESSION_TTL_MINUTES` | `60` | Terminal session lifetime. |
| `TERMINAL_IDLE_TIMEOUT_MINUTES` | `30` | Idle timeout enforced by the agent. |
| `GPU_ENABLED` | `false` | Enables GPU sync, requests and reconcile. |
| `BACKUP_ROOT` | `/srv/itk-cloud/backups` | Absolute. |
| `BACKUP_AGE_RECIPIENT` | empty | `age1...` public key; required in production. |
| `BACKUP_RETENTION_DAYS` | `14` | ≥ 1. |
| `BACKUP_COPY_DIR` | empty | Second location (other disk/NFS) receiving a copy of each backup file except workspace file archives. |
| `PROJECT_DB_ADMIN_URL` | empty (feature off) | Role with `CREATEDB`/`CREATEROLE`, not superuser. |
| `PROJECT_DB_HOST` / `PROJECT_DB_PORT` | `127.0.0.1` / `5432` | Address given to users/workspaces in connection strings (installer: `host.docker.internal`). |
| `SMTP_HOST`, `SMTP_PORT` (587), `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM` | empty | Optional email for password resets (STARTTLS when offered). |
| `CONFIG_DIR` | `/etc/itk-cloud` | Archived by `config` backups (readable files only). |
| `WEB_HEALTH_URL` | empty | Probed for the `web` service status. |
| `PROMETHEUS_URL`, `GRAFANA_URL` | empty | Links shown in the console. |
| `ITK_CLOUD_ENV_FILE` | — | Path of the env file to load. |

The installer also stores `ITK_DB_PASSWORD`, `ITK_PROJECT_DB_PASSWORD` and
`ITK_REDIS_PASSWORD` in the same file for re-runs; the applications ignore them.

### Agent (`/etc/itk-cloud/agent.env`, 0600 root)

| Variable | Default | Notes |
|---|---|---|
| `AGENT_SOCKET_PATH` | `/run/itk-cloud/agent.sock` | |
| `AGENT_SOCKET_GROUP` | `itk-cloud` | Group allowed to connect (socket 0660). |
| `AGENT_SHARED_SECRET` / `AGENT_SHARED_SECRET_FILE` | — (≥ 32 chars) | The file variant overrides the value. |
| `AGENT_STORAGE_DRIVER` | `loopback` | `loopback` (enforced quota) or `directory` (measured only). |
| `AGENT_NATIVE_GROUP` | `itk-workspace` | Primary group of native workspace users. |
| `WORKSPACE_ROOT`, `BACKUP_ROOT` | as above | Must match the API/worker values. |
| `WORKSPACE_NATIVE_ENABLED` | `false` | Must match the API value. |
| `DOCKER_SOCKET` | `/var/run/docker.sock` | |
| `NVIDIA_SMI_PATH` | `nvidia-smi` | |
| `AGENT_CONTAINER_ROOTFS_SIZE` | empty | Docker `storage-opt size` (overlay2 on XFS with `pquota` only). |
| `AGENT_CONTAINER_TMPFS_SIZE` | `512m` | `/tmp` tmpfs size. |
| `AGENT_APPARMOR_PROFILE` | `docker-default` | Applied when AppArmor is enabled. |
| `AGENT_SECCOMP_PROFILE` | empty (Docker default) | Path to a custom seccomp JSON. |
| `AGENT_MAX_CONCURRENT_OPS` | `32` | |
| `APP_LOG_LEVEL` | `info` | `debug` enables debug logs. |

Runtime policy (quotas, alerts, MFA enforcement, backups schedule, GPU limits, …) is in
**platform settings**, changed in the console or via `PATCH /api/v1/admin/settings`
([operations.md §4](operations.md#4-platform-settings)).

## 6. DNS

- Choose the console name before installing; it is baked into `APP_BASE_URL`,
  `APP_PROXY_ORIGIN`, the Caddyfile and the TLS certificates.
- The spec's example `itk-cloud.local` works only where clients resolve it via unicast DNS.
  `.local` is reserved for multicast DNS (RFC 6762); macOS, many Linux desktops and some
  Windows setups send `.local` lookups to mDNS only. Prefer a name under the campus internal
  domain (e.g. `itk-cloud.lab.<campus>.ac.id`) or `.internal`.
- Create an A record on the lab/campus DNS server pointing to the server's LAN address. For
  a quick test, add `<server-ip> itk-cloud.lab` to the client's hosts file.
- The app-proxy origin uses the **same name** with a different port (default 8443), so no
  extra record is needed.

## 7. TLS and Caddy

`deployments/caddy/Caddyfile.tmpl` (rendered to `/etc/caddy/Caddyfile`):

- `https://<domain>` — `tls internal`; `/api/*` and `/health/*` → `127.0.0.1:8080`
  (`flush_interval -1` for streaming/WebSocket), everything else → web on `127.0.0.1:3000`;
  requests to `/api/v1/workspaces/*/proxy...` are answered 404 on this origin; adds HSTS,
  `nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, a restrictive
  `Permissions-Policy`; JSON access log `/var/log/caddy/itk-cloud-access.log`.
- `https://<domain>:<proxy-port>` — **only** `^/api/v1/workspaces/[^/]+/proxy(/.*)?$` is
  forwarded to the API (Host preserved); everything else returns 404. This restriction is a
  security requirement ([security.md §6](security.md#6-known-limitations-and-residual-risks), item 7).
- `http://<domain>` redirects to HTTPS.

**Trusting the internal CA on lab clients.** Caddy's root certificate is
`/var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt`. Distribute only the
root certificate (never the key):

| Client | Command |
|---|---|
| Ubuntu/Debian | `sudo cp root.crt /usr/local/share/ca-certificates/itk-cloud.crt && sudo update-ca-certificates` |
| Windows (admin shell) | `certutil -addstore -f ROOT root.crt` (or deploy via Group Policy) |
| macOS | `sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain root.crt` |
| Firefox | Settings → Certificates → Import (or enterprise policy `Certificates.Install`) |

Publish the certificate fingerprint (`openssl x509 -in root.crt -noout -fingerprint -sha256`)
so users can verify it. Instruct users never to click through certificate warnings.

## 8. Firewall and ports

UFW configuration applied by the installer: default deny incoming, allow outgoing, then:

| Port | Proto | Source | Purpose |
|---|---|---|---|
| 22 | TCP | admin network only (`--ssh-from CIDR`; without it the installer allows any source — restrict it) | SSH for administrators |
| 80 | TCP | LAN | Redirect to HTTPS |
| 443 | TCP | LAN | Console and API (Caddy) |
| 8443 (`--proxy-port`) | TCP | LAN | Workspace app origin (Caddy) |
| 3443 | TCP | LAN/admin network | Grafana (only with `--with-monitoring`) |
| 5432 | TCP | `10.213.0.0/16` only | Project databases from workspace containers (only with project databases) |

Everything else is closed to the LAN. Local-only listeners: API `127.0.0.1:8080`, web
`127.0.0.1:3000`, Redis `127.0.0.1:6379`, Prometheus `127.0.0.1:9090`, node_exporter
`127.0.0.1:9100`, Grafana `127.0.0.1:3001`, workspace app ports `127.0.0.1:<random>`.
PostgreSQL authenticates the platform roles only from localhost; project roles only from the
Docker address pool. Docker's own iptables rules do not expose anything because workspace
ports are published on `127.0.0.1` only.

Verify from another machine on the LAN:

```bash
nmap -Pn -p- itk-cloud.lab        # expect only 22 (if allowed), 80, 443, 8443 (3443)
```

## 9. Workspace images

Workspace images are built on the server from `deployments/docker/workspace-images` and
tagged `itk-cloud/<name>:1.0` (matching the seeded image catalog):

```bash
sudo installer/build-workspace-images.sh                       # all 11 images
sudo installer/build-workspace-images.sh ubuntu-dev python-dev  # selected images
ITK_IMAGE_TAG=1.0 sudo installer/build-workspace-images.sh jupyterlab
```

A template whose image is missing fails provisioning with "image ... is not available on the
server". After building, use *Administration → Images → Check / pull* (or
`POST /api/v1/images/{id}/pull`) to refresh the catalog status. Details in
[workspace.md §8](workspace.md#8-templates-and-images).

## 10. Post-install verification

```bash
systemctl status itk-cloud-agent itk-cloud-api itk-cloud-worker itk-cloud-web caddy postgresql redis-server docker
sudo itkctl migrate status   # up to date
itkctl health                                      # {"status":"ready",...}
curl -s https://itk-cloud.lab/health/ready         # from a client that trusts the CA
ls -l /run/itk-cloud/agent.sock                     # srw-rw---- root itk-cloud
journalctl -u itk-cloud-agent -n 20                 # "agent listening", protocol 1
docker info --format '{{.ServerVersion}} {{.SecurityOptions}}'
docker image ls 'itk-cloud/*'
```

Then in the browser:

1. Sign in as the first administrator; enroll MFA (mandatory) and store the recovery codes.
2. *Administration → Agent status*: online, protocol 1, Docker available,
   `storage_driver=loopback`, `storage_enforced=true`, AppArmor/seccomp true.
3. *Monitoring → Services*: api, postgresql, redis, worker, agent, web all `up`.
4. Create a test student, log in as that user, create an `ubuntu-dev` workspace, start it,
   open a terminal (`id` → uid 1000), upload/download a file, stop and delete it.
5. *Administration → Backups*: run a `full` backup and a **verify** restore of the
   `platform_db` backup with the offline key ([backup-restore.md](backup-restore.md)).
6. Move the backup private key offline and remove `/root/itk-cloud-backup-private-key.txt`
   (`shred -u`).
7. Review quota packages against the hardware inventory (§2) and set `BACKUP_COPY_DIR`.

## 11. Upgrade

```bash
tar xzf itk-cloud-<new>-linux-amd64.tar.gz && cd itk-cloud-<new>-linux-amd64
sudo ./installer/upgrade.sh            # --yes to skip prompts, --skip-backup (not recommended)
```

`installer/upgrade.sh`:

1. Checks that Jenderal Cloud OS is installed (`/opt/itk-cloud/current`), compares the installed and
   target versions (warns on downgrade) and verifies the release files.
2. Creates **pre-upgrade backups** synchronously: `itkctl backup run --kind platform_db
   --trigger pre_upgrade` (aborts the upgrade if it fails) and the `config` backup.
3. Installs the release side by side in `/opt/itk-cloud/releases/<version>`.
4. Stops services in order web → worker → api → agent, switches the `current` symlink,
   installs the new unit files.
5. Runs `itkctl migrate`.
6. Starts agent → api → worker → web and health-checks (agent socket, `/health/ready`, web,
   `itkctl health`).
7. On failure it **rolls back binaries and unit files** to the previous release and
   restarts the services. **Database migrations are not rolled back automatically** (they may
   be irreversible); if the old binaries cannot run against the migrated schema, restore the
   pre-upgrade backup with `itkctl restore platform-db` ([backup-restore.md §6.3](backup-restore.md#63-full-platform-database-restore-cli))
   or fix forward with a newer release.
8. Keeps the three most recent releases for manual rollback.

Before upgrading: read the release notes, make sure the last scheduled backup succeeded and
the backup private key is at hand, and announce a maintenance window
(`POST /api/v1/announcements`).

## 12. Uninstall

```bash
sudo ./installer/uninstall.sh                         # remove services and binaries; keep data, config, backups
sudo ./installer/uninstall.sh --purge                 # also delete workspaces, databases, config (type DELETE EVERYTHING)
sudo ./installer/uninstall.sh --purge --purge-backups # ...and the backups
```

The uninstaller stops and disables the services, removes unit files, `itkctl` and
`/opt/itk-cloud`, and restores an empty Caddyfile (backing up the old one). With `--purge`
it removes managed containers and networks (`label=itk.managed=true`), unmounts and deletes
workspace volumes, deletes native workspace accounts, drops `itk_*` databases/roles and the
platform database, removes `/etc/itk-cloud`, `/var/lib/itk-cloud`, the Redis include, the
`itk-cloud` user and `itk-workspace` group. PostgreSQL, Redis, Docker and Caddy packages stay
installed. Take and copy a final backup off the server before purging.
