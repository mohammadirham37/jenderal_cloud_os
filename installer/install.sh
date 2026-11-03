#!/usr/bin/env bash
# Jenderal Cloud OS installer for Ubuntu Server 24.04 LTS.
#
# Installs Jenderal Cloud OS natively (systemd services; no Docker for the platform
# itself). Docker Engine is installed only as the runtime for user container
# workspaces. Run from an extracted release directory:
#
#   sudo ./installer/install.sh                       # interactive
#   sudo ./installer/install.sh --non-interactive --yes --domain itk-cloud.lab \
#        --admin-user admin --admin-email admin@lab.example --admin-name "Lab Admin"
#
# Re-running the installer is safe: existing secrets, data and configuration
# are kept; binaries and unit files are updated.
set -Eeuo pipefail

SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=installer/lib.sh
. "$SRC/installer/lib.sh"

usage() {
	cat <<'EOF'
Usage: sudo installer/install.sh [options]

Options:
  --domain NAME            Hostname of the console (default: itk-cloud.local)
  --proxy-port PORT        HTTPS port of the separate workspace-app origin (default: 8443)
  --admin-user NAME        First administrator username
  --admin-email EMAIL      First administrator email
  --admin-name "NAME"      First administrator full name
                           (password: prompted, or $ITK_ADMIN_PASSWORD, or generated and shown once)
  --storage-driver D       loopback (default; enforced quotas) | directory (measured only)
  --docker-source S        docker-official (default, docker.com apt repo) | ubuntu (docker.io package)
  --with-gpu / --no-gpu    Configure NVIDIA Container Toolkit (default: auto-detect nvidia-smi)
  --with-monitoring        Install Prometheus, node_exporter and Grafana (localhost only)
  --enable-native          Enable Linux-native workspaces (disabled by default)
  --project-databases / --no-project-databases
                           Provision PostgreSQL databases for projects (default: enabled)
  --ssh-from CIDR          Restrict SSH to this admin network in the firewall (default: allow SSH from anywhere)
  --no-firewall            Do not configure UFW
  --images LIST            Workspace images to build: none | core (default: ubuntu-dev,python-dev) | all | comma list
  --node-tarball FILE      Use a local Node.js tarball (offline install) instead of downloading
  --small-server           Tune for a small VPS (1-2 GB RAM): swap file, smaller PostgreSQL/Redis/
                           connection pools, core images only. Enabled automatically below 3 GB RAM
                           or 20 GB free disk.
  --non-interactive        Never prompt; fail when required values are missing
  --yes                    Answer yes to confirmations
  -h, --help               Show this help
EOF
}

DOMAIN="${ITK_DOMAIN:-}"
PROXY_PORT="${ITK_PROXY_PORT:-8443}"
ADMIN_USER="${ITK_ADMIN_USER:-}"
ADMIN_EMAIL="${ITK_ADMIN_EMAIL:-}"
ADMIN_NAME="${ITK_ADMIN_NAME:-}"
STORAGE_DRIVER="loopback"
DOCKER_SOURCE="docker-official"
WITH_GPU="auto"
WITH_MONITORING=0
ENABLE_NATIVE=0
PROJECT_DBS=1
SSH_FROM=""
CONFIGURE_FIREWALL=1
IMAGES="core"
NODE_TARBALL=""
NON_INTERACTIVE=0
ASSUME_YES=0
SMALL=0

while [ $# -gt 0 ]; do
	case "$1" in
	--domain) DOMAIN=$2; shift 2 ;;
	--proxy-port) PROXY_PORT=$2; shift 2 ;;
	--admin-user) ADMIN_USER=$2; shift 2 ;;
	--admin-email) ADMIN_EMAIL=$2; shift 2 ;;
	--admin-name) ADMIN_NAME=$2; shift 2 ;;
	--storage-driver) STORAGE_DRIVER=$2; shift 2 ;;
	--docker-source) DOCKER_SOURCE=$2; shift 2 ;;
	--with-gpu) WITH_GPU=1; shift ;;
	--no-gpu) WITH_GPU=0; shift ;;
	--with-monitoring) WITH_MONITORING=1; shift ;;
	--enable-native) ENABLE_NATIVE=1; shift ;;
	--project-databases) PROJECT_DBS=1; shift ;;
	--no-project-databases) PROJECT_DBS=0; shift ;;
	--ssh-from) SSH_FROM=$2; shift 2 ;;
	--no-firewall) CONFIGURE_FIREWALL=0; shift ;;
	--images) IMAGES=$2; shift 2 ;;
	--node-tarball) NODE_TARBALL=$2; shift 2 ;;
	--small-server) SMALL=1; shift ;;
	--non-interactive) NON_INTERACTIVE=1; shift ;;
	--yes | -y) ASSUME_YES=1; shift ;;
	-h | --help) usage; exit 0 ;;
	*) die "Unknown option: $1 (see --help)" ;;
	esac
done
export NON_INTERACTIVE ASSUME_YES

trap 'die "Installation failed at line $LINENO. See $LOG_FILE."' ERR

require_root
touch "$LOG_FILE" && chmod 600 "$LOG_FILE"
ITK_VERSION="$(itk_version "$SRC")"
log "Jenderal Cloud OS installer — version $ITK_VERSION"

# ---------------------------------------------------------------------------
# 1. Preflight checks (stop on critical failures)
# ---------------------------------------------------------------------------
log "Checking prerequisites"
[ -r /etc/os-release ] || die "Cannot detect the operating system."
# Read os-release in a subshell so its variables (e.g. VERSION) do not leak.
OS_ID="$(. /etc/os-release && echo "${ID:-}")"
OS_VERSION_ID="$(. /etc/os-release && echo "${VERSION_ID:-}")"
OS_PRETTY="$(. /etc/os-release && echo "${PRETTY_NAME:-unknown}")"
OS_CODENAME="$(. /etc/os-release && echo "${VERSION_CODENAME:-}")"
[ "$OS_ID" = "ubuntu" ] && { [ "$OS_VERSION_ID" = "24.04" ] || [ "$OS_VERSION_ID" = "26.04" ]; } ||
	die "Ubuntu Server 24.04 LTS or 26.04 LTS is required (found $OS_PRETTY)."
ok "OS: $OS_PRETTY"

case "$(uname -m)" in
x86_64) ARCH=amd64; NODE_ARCH=x64 ;;
aarch64) ARCH=arm64; NODE_ARCH=arm64 ;;
*) die "Unsupported architecture $(uname -m) (amd64 or arm64 required)." ;;
esac
ok "Architecture: $ARCH"

for f in bin/itk-cloud-api bin/itk-cloud-worker bin/itk-cloud-agent bin/itkctl web/build/index.js deployments/caddy/Caddyfile.tmpl; do
	[ -e "$SRC/$f" ] || die "Release file missing: $f (run from an extracted Jenderal Cloud OS release; build one with 'make release')."
done
file_arch="$("$SRC/bin/itkctl" version 2>/dev/null || true)"
[ -n "$file_arch" ] || die "bin/itkctl cannot run on this machine (wrong architecture?)."

CPUS=$(nproc)
MEM_KB=$(awk '/MemTotal/ {print $2}' /proc/meminfo)
MEM_GB=$((MEM_KB / 1024 / 1024))
MEM_MB=$((MEM_KB / 1024))
mkdir -p "$ITK_DATA"
FREE_GB=$(df -BG --output=avail "$ITK_DATA" | tail -1 | tr -dc '0-9')
if [ "$SMALL" = 0 ] && { [ "$MEM_MB" -lt 3000 ] || [ "$FREE_GB" -lt 20 ]; }; then
	SMALL=1
	warn "Small server detected (${MEM_MB} MB RAM, ${FREE_GB} GB free disk): enabling small-server mode."
fi
if [ "$SMALL" = 1 ]; then
	[ "$MEM_MB" -ge 900 ] || die "At least 1 GB RAM is required (found ${MEM_MB} MB)."
	[ "$FREE_GB" -ge 10 ] || die "At least 10 GB free disk space is required on $ITK_DATA (found ${FREE_GB} GB)."
	warn "Small-server mode: suitable for trials and a few light workspaces (use 0.5 GB memory per workspace)."
else
	[ "$CPUS" -ge 2 ] || die "At least 2 CPU cores are required (found $CPUS)."
	[ "$MEM_GB" -ge 8 ] || warn "Less than 8 GB RAM: only a few workspaces can run concurrently."
fi
ok "CPU: $CPUS cores, RAM: ${MEM_MB} MB, free disk on $ITK_DATA: ${FREE_GB} GB"

for p in 443 "$PROXY_PORT"; do
	if ss -ltnH "sport = :$p" | grep -q . && ! ss -ltnpH "sport = :$p" | grep -q caddy; then
		die "Port $p is already in use by another program."
	fi
done
ok "Ports 443 and $PROXY_PORT are available"

case "$STORAGE_DRIVER" in loopback | directory) ;; *) die "--storage-driver must be loopback or directory" ;; esac
case "$DOCKER_SOURCE" in docker-official | ubuntu) ;; *) die "--docker-source must be docker-official or ubuntu" ;; esac
[[ "$PROXY_PORT" =~ ^[0-9]+$ ]] && [ "$PROXY_PORT" -gt 1024 ] && [ "$PROXY_PORT" -ne 8080 ] && [ "$PROXY_PORT" -ne 3000 ] ||
	die "--proxy-port must be a free port above 1024 (not 3000/8080)."

if [ "$WITH_GPU" = auto ]; then
	if command -v nvidia-smi >/dev/null 2>&1 && nvidia-smi -L >/dev/null 2>&1; then WITH_GPU=1; else WITH_GPU=0; fi
fi
if [ "$WITH_GPU" = 1 ]; then
	if command -v nvidia-smi >/dev/null 2>&1 && nvidia-smi -L >/dev/null 2>&1; then
		ok "NVIDIA driver detected: $(nvidia-smi --query-gpu=name --format=csv,noheader | paste -sd, -)"
	else
		die "--with-gpu requested but nvidia-smi does not work. Install the NVIDIA driver first (e.g. 'ubuntu-drivers install'), reboot, then re-run."
	fi
else
	ok "GPU support: disabled (no working NVIDIA driver found or --no-gpu)"
fi

ask DOMAIN "Console hostname (DNS name lab clients will use)" "itk-cloud.local"
[[ "$DOMAIN" =~ ^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$ ]] || die "Invalid domain: $DOMAIN"
BASE_URL="https://$DOMAIN"
PROXY_ORIGIN="https://$DOMAIN:$PROXY_PORT"

log "Summary"
cat <<EOF
  Console URL:        $BASE_URL
  Workspace apps:     $PROXY_ORIGIN
  Storage driver:     $STORAGE_DRIVER
  Docker source:      $DOCKER_SOURCE
  GPU support:        $([ "$WITH_GPU" = 1 ] && echo yes || echo no)
  Native workspaces:  $([ "$ENABLE_NATIVE" = 1 ] && echo enabled || echo disabled)
  Project databases:  $([ "$PROJECT_DBS" = 1 ] && echo enabled || echo disabled)
  Monitoring stack:   $([ "$WITH_MONITORING" = 1 ] && echo yes || echo no)
  Small-server mode:  $([ "$SMALL" = 1 ] && echo "yes (swap, reduced memory settings)" || echo no)
  Firewall (UFW):     $([ "$CONFIGURE_FIREWALL" = 1 ] && echo configure || echo skip)
EOF
confirm "Proceed with installation?" || die "Aborted by user."

if [ "$SMALL" = 1 ]; then
	[ "$WITH_MONITORING" = 1 ] && warn "Monitoring stack disabled in small-server mode." && WITH_MONITORING=0
	[ "$IMAGES" = core ] && IMAGES=ubuntu-dev
fi

# ---------------------------------------------------------------------------
# 2. Packages
# ---------------------------------------------------------------------------
export DEBIAN_FRONTEND=noninteractive
if [ "$SMALL" = 1 ] && [ -z "$(swapon --noheadings --show=NAME)" ]; then
	log "Creating a 2 GB swap file (small-server mode)"
	fallocate -l 2G /swapfile.itk-cloud && chmod 600 /swapfile.itk-cloud
	mkswap -q /swapfile.itk-cloud && swapon /swapfile.itk-cloud
	grep -q '^/swapfile.itk-cloud ' /etc/fstab || echo '/swapfile.itk-cloud none swap sw 0 0' >>/etc/fstab
	sysctl -q -w vm.swappiness=20 && echo 'vm.swappiness=20' >/etc/sysctl.d/90-itk-cloud.conf
	ok "Swap enabled"
fi
log "Installing system packages"
apt-get update -qq
apt-get install -y -qq ca-certificates curl gnupg jq uuid-runtime e2fsprogs util-linux acl ufw \
	postgresql postgresql-contrib redis-server caddy >>"$LOG_FILE" 2>&1
ok "PostgreSQL $(psql --version | awk '{print $3}'), Redis $(redis-server --version | awk '{print $3}' | cut -d= -f2), Caddy $(caddy version | awk '{print $1}')"

if ! command -v docker >/dev/null 2>&1; then
	log "Installing Docker Engine ($DOCKER_SOURCE) — runtime for user workspaces only"
	if [ "$DOCKER_SOURCE" = docker-official ]; then
		install -m 0755 -d /etc/apt/keyrings
		curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
		chmod a+r /etc/apt/keyrings/docker.asc
		echo "deb [arch=$ARCH signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu ${OS_CODENAME} stable" \
			>/etc/apt/sources.list.d/docker.list
		apt-get update -qq
		apt-get install -y -qq docker-ce docker-ce-cli containerd.io >>"$LOG_FILE" 2>&1
	else
		apt-get install -y -qq docker.io >>"$LOG_FILE" 2>&1
	fi
fi
if [ ! -f /etc/docker/daemon.json ]; then
	install -m 0644 "$SRC/deployments/config/docker-daemon.json" /etc/docker/daemon.json
	ok "Docker daemon hardened (no-new-privileges, dedicated address pool 10.213.0.0/16, log limits)"
else
	warn "/etc/docker/daemon.json exists; not modified. Compare with deployments/config/docker-daemon.json."
fi
systemctl enable --now docker >/dev/null
systemctl restart docker
DOCKER_API=$(docker version --format '{{.Server.APIVersion}}')
if [ "$(printf '%s\n1.44\n' "$DOCKER_API" | sort -V | head -1)" != "1.44" ]; then
	die "Docker Engine API $DOCKER_API is too old (1.44+ / Docker 25+ required). Use --docker-source docker-official."
fi
ok "Docker Engine $(docker version --format '{{.Server.Version}}') (API $DOCKER_API)"

if [ "$WITH_GPU" = 1 ] && ! command -v nvidia-ctk >/dev/null 2>&1; then
	log "Installing NVIDIA Container Toolkit"
	curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | gpg --dearmor --yes -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
	curl -fsSL https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list |
		sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' \
			>/etc/apt/sources.list.d/nvidia-container-toolkit.list
	apt-get update -qq
	apt-get install -y -qq nvidia-container-toolkit >>"$LOG_FILE" 2>&1
	nvidia-ctk runtime configure --runtime=docker >>"$LOG_FILE" 2>&1
	systemctl restart docker
	ok "NVIDIA Container Toolkit configured"
fi

# Node.js runtime for the web console (pinned, checksum verified).
if [ ! -x "$ITK_PREFIX/node/bin/node" ] || [ "$("$ITK_PREFIX/node/bin/node" --version)" != "$NODE_VERSION" ]; then
	log "Installing Node.js $NODE_VERSION for the web console"
	tmp="$(mktemp -d)"
	name="node-$NODE_VERSION-linux-$NODE_ARCH.tar.xz"
	if [ -n "$NODE_TARBALL" ]; then
		cp "$NODE_TARBALL" "$tmp/$name"
	else
		curl -fsSL "https://nodejs.org/dist/$NODE_VERSION/$name" -o "$tmp/$name"
		curl -fsSL "https://nodejs.org/dist/$NODE_VERSION/SHASUMS256.txt" -o "$tmp/SHASUMS256.txt"
		(cd "$tmp" && grep " $name\$" SHASUMS256.txt | sha256sum -c --quiet -) || die "Node.js checksum verification failed."
	fi
	rm -rf "$ITK_PREFIX/node" && mkdir -p "$ITK_PREFIX/node"
	tar -xJf "$tmp/$name" -C "$ITK_PREFIX/node" --strip-components=1
	rm -rf "$tmp"
	ok "Node.js $("$ITK_PREFIX/node/bin/node" --version)"
fi

# ---------------------------------------------------------------------------
# 3. Users, groups and directories
# ---------------------------------------------------------------------------
log "Creating service accounts and directories"
getent group "$ITK_USER" >/dev/null || groupadd --system "$ITK_USER"
id "$ITK_USER" >/dev/null 2>&1 || useradd --system --gid "$ITK_USER" --home-dir "$ITK_STATE" --shell /usr/sbin/nologin "$ITK_USER"
getent group "$ITK_WS_GROUP" >/dev/null || groupadd "$ITK_WS_GROUP"
install -d -m 0711 -o root -g root "$ITK_DATA" "$ITK_DATA/workspaces"
install -d -m 0770 -o root -g "$ITK_USER" "$ITK_DATA/backups"
install -d -m 0750 -o root -g "$ITK_USER" "$ITK_ETC"
install -d -m 0750 -o "$ITK_USER" -g "$ITK_USER" "$ITK_STATE"
install -d -m 0755 "$ITK_PREFIX" "$ITK_PREFIX/releases"
ok "User $ITK_USER, group $ITK_WS_GROUP, data in $ITK_DATA"

# ---------------------------------------------------------------------------
# 4. Install release files
# ---------------------------------------------------------------------------
log "Installing Jenderal Cloud OS $ITK_VERSION"
REL="$ITK_PREFIX/releases/$ITK_VERSION"
rm -rf "$REL.tmp" && mkdir -p "$REL.tmp"
cp -a "$SRC/bin" "$SRC/web" "$SRC/deployments" "$SRC/installer" "$REL.tmp/"
[ -d "$SRC/docs" ] && cp -a "$SRC/docs" "$REL.tmp/"
cp "$SRC/VERSION" "$REL.tmp/" 2>/dev/null || echo "$ITK_VERSION" >"$REL.tmp/VERSION"
chown -R root:root "$REL.tmp" && chmod -R go-w "$REL.tmp"
rm -rf "$REL" && mv "$REL.tmp" "$REL"
ln -sfn "$REL" "$ITK_PREFIX/current.new" && mv -Tf "$ITK_PREFIX/current.new" "$ITK_PREFIX/current"
ln -sfn "$ITK_PREFIX/current/docs" "$ITK_PREFIX/docs" 2>/dev/null || true
rm -f /usr/local/bin/itkctl
cat >/usr/local/bin/itkctl <<'WRAPPER'
#!/bin/sh
# itkctl wrapper: loads the Jenderal Cloud OS configuration (root only).
set -a
. /etc/itk-cloud/itk-cloud.env
set +a
exec /opt/itk-cloud/current/bin/itkctl "$@"
WRAPPER
chmod 0750 /usr/local/bin/itkctl
ok "Installed to $REL"

# ---------------------------------------------------------------------------
# 5. PostgreSQL and Redis
# ---------------------------------------------------------------------------
ENV_FILE="$ITK_ETC/itk-cloud.env"
touch "$ENV_FILE" && chown root:"$ITK_USER" "$ENV_FILE" && chmod 0640 "$ENV_FILE"

log "Configuring PostgreSQL"
systemctl enable --now postgresql >/dev/null
PG_VER=$(pg_lsclusters -h | awk 'NR==1 {print $1}')
PG_CONF_DIR="/etc/postgresql/$PG_VER/main"
DB_PASS="$(env_get "$ENV_FILE" ITK_DB_PASSWORD)"
[ -n "$DB_PASS" ] || DB_PASS="$(random_password 32)"
sudo -u postgres psql -v ON_ERROR_STOP=1 -q <<SQL
DO \$\$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'itk_cloud') THEN
    CREATE ROLE itk_cloud LOGIN PASSWORD '$DB_PASS';
  ELSE
    ALTER ROLE itk_cloud LOGIN PASSWORD '$DB_PASS';
  END IF;
END \$\$;
SQL
sudo -u postgres psql -tAc "SELECT 1 FROM pg_database WHERE datname='itk_cloud'" | grep -q 1 ||
	sudo -u postgres createdb -O itk_cloud -E UTF8 itk_cloud
sudo -u postgres psql -q -d itk_cloud -c "CREATE EXTENSION IF NOT EXISTS pgcrypto" >/dev/null
env_set "$ENV_FILE" ITK_DB_PASSWORD "$DB_PASS"
POOL=""
[ "$SMALL" = 1 ] && POOL="&pool_max_conns=8"
env_set "$ENV_FILE" DATABASE_URL "postgres://itk_cloud:$DB_PASS@127.0.0.1:5432/itk_cloud?sslmode=disable$POOL"

if [ "$PROJECT_DBS" = 1 ]; then
	PDB_PASS="$(env_get "$ENV_FILE" ITK_PROJECT_DB_PASSWORD)"
	[ -n "$PDB_PASS" ] || PDB_PASS="$(random_password 32)"
	sudo -u postgres psql -v ON_ERROR_STOP=1 -q <<SQL
DO \$\$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'itk_projects_admin') THEN
    CREATE ROLE itk_projects_admin LOGIN CREATEDB CREATEROLE NOSUPERUSER PASSWORD '$PDB_PASS';
  ELSE
    ALTER ROLE itk_projects_admin LOGIN CREATEDB CREATEROLE NOSUPERUSER PASSWORD '$PDB_PASS';
  END IF;
END \$\$;
SQL
	env_set "$ENV_FILE" ITK_PROJECT_DB_PASSWORD "$PDB_PASS"
	env_set "$ENV_FILE" PROJECT_DB_ADMIN_URL "postgres://itk_projects_admin:$PDB_PASS@127.0.0.1:5432/postgres?sslmode=disable"
	# Workspaces reach project databases via host.docker.internal from the
	# dedicated Docker address pool only; scram authentication, project roles only.
	if ! grep -q "itk-cloud project databases" "$PG_CONF_DIR/pg_hba.conf"; then
		cat >>"$PG_CONF_DIR/pg_hba.conf" <<'EOF'
# itk-cloud project databases: workspace containers (Docker pool 10.213.0.0/16)
host    /^itk_    /^itk_    10.213.0.0/16    scram-sha-256
EOF
	fi
	# Docker bridge gateways are created on demand, so PostgreSQL listens on all
	# addresses; pg_hba (above) and UFW (port 5432 only from 10.213.0.0/16)
	# keep it unreachable from the LAN.
	sed -i "s/^#\?listen_addresses.*/listen_addresses = '*'/" "$PG_CONF_DIR/postgresql.conf"
	env_set "$ENV_FILE" PROJECT_DB_HOST "host.docker.internal"
	env_set "$ENV_FILE" PROJECT_DB_PORT "5432"
fi
if [ "$SMALL" = 1 ]; then
	mkdir -p "$PG_CONF_DIR/conf.d"
	cat >"$PG_CONF_DIR/conf.d/90-itk-cloud-small.conf" <<'EOF'
# Jenderal Cloud OS small-server tuning
shared_buffers = 64MB
work_mem = 2MB
maintenance_work_mem = 32MB
effective_cache_size = 256MB
max_connections = 60
EOF
fi
systemctl restart postgresql
ok "PostgreSQL $PG_VER configured (database itk_cloud)"

log "Configuring Redis"
REDIS_PASS="$(env_get "$ENV_FILE" ITK_REDIS_PASSWORD)"
[ -n "$REDIS_PASS" ] || REDIS_PASS="$(random_password 40)"
install -m 0640 -o redis -g redis "$SRC/deployments/config/redis-itk-cloud.conf" /etc/redis/itk-cloud.conf
printf 'requirepass %s\n' "$REDIS_PASS" >>/etc/redis/itk-cloud.conf
[ "$SMALL" = 1 ] && sed -i 's/^maxmemory .*/maxmemory 96mb/' /etc/redis/itk-cloud.conf
grep -q '^include /etc/redis/itk-cloud.conf' /etc/redis/redis.conf || echo 'include /etc/redis/itk-cloud.conf' >>/etc/redis/redis.conf
systemctl enable --now redis-server >/dev/null && systemctl restart redis-server
env_set "$ENV_FILE" ITK_REDIS_PASSWORD "$REDIS_PASS"
env_set "$ENV_FILE" REDIS_URL "redis://:$REDIS_PASS@127.0.0.1:6379/0"
ok "Redis bound to localhost with authentication"

# ---------------------------------------------------------------------------
# 6. Configuration and secrets (generated once, never printed)
# ---------------------------------------------------------------------------
log "Writing configuration"
[ -n "$(env_get "$ENV_FILE" SESSION_SECRET)" ] || env_set "$ENV_FILE" SESSION_SECRET "$(random_secret 48)"
[ -n "$(env_get "$ENV_FILE" ENCRYPTION_KEY)" ] || env_set "$ENV_FILE" ENCRYPTION_KEY "$(random_secret 32)"
[ -n "$(env_get "$ENV_FILE" AGENT_SHARED_SECRET)" ] || env_set "$ENV_FILE" AGENT_SHARED_SECRET "$(random_secret 48)"
set_default() { [ -n "$(env_get "$ENV_FILE" "$1")" ] || env_set "$ENV_FILE" "$1" "$2"; }
env_set "$ENV_FILE" APP_ENV production
env_set "$ENV_FILE" APP_BASE_URL "$BASE_URL"
env_set "$ENV_FILE" APP_PROXY_ORIGIN "$PROXY_ORIGIN"
env_set "$ENV_FILE" COOKIE_SECURE true
set_default APP_LOG_LEVEL info
env_set "$ENV_FILE" API_HOST 127.0.0.1
env_set "$ENV_FILE" API_PORT 8080
env_set "$ENV_FILE" AGENT_SOCKET_PATH /run/itk-cloud/agent.sock
env_set "$ENV_FILE" WORKSPACE_ROOT "$ITK_DATA/workspaces"
env_set "$ENV_FILE" BACKUP_ROOT "$ITK_DATA/backups"
env_set "$ENV_FILE" CONFIG_DIR "$ITK_ETC"
env_set "$ENV_FILE" WORKSPACE_NATIVE_ENABLED "$([ "$ENABLE_NATIVE" = 1 ] && echo true || echo false)"
set_default WORKSPACE_DEFAULT_DRIVER docker
env_set "$ENV_FILE" GPU_ENABLED "$([ "$WITH_GPU" = 1 ] && echo true || echo false)"
env_set "$ENV_FILE" WEB_HEALTH_URL http://127.0.0.1:3000/
set_default UPLOAD_MAX_BYTES 104857600
set_default TERMINAL_SESSION_TTL_MINUTES 60
set_default BACKUP_RETENTION_DAYS 14

KEY_FILE=/root/itk-cloud-backup-private-key.txt
if [ -z "$(env_get "$ENV_FILE" BACKUP_AGE_RECIPIENT)" ]; then
	out="$("$ITK_PREFIX/current/bin/itkctl" backup keygen)"
	env_set "$ENV_FILE" BACKUP_AGE_RECIPIENT "$(printf '%s\n' "$out" | sed -n 's/^BACKUP_AGE_RECIPIENT=//p')"
	umask 077
	printf '%s\n' "$out" | sed -n 's/^# private key: //p' >"$KEY_FILE"
	chmod 0400 "$KEY_FILE"
	BACKUP_KEY_CREATED=1
fi

AGENT_ENV="$ITK_ETC/agent.env"
umask 077
{
	echo "AGENT_SOCKET_PATH=/run/itk-cloud/agent.sock"
	echo "AGENT_SOCKET_GROUP=$ITK_USER"
	echo "AGENT_SHARED_SECRET=$(env_get "$ENV_FILE" AGENT_SHARED_SECRET)"
	echo "AGENT_STORAGE_DRIVER=$STORAGE_DRIVER"
	echo "AGENT_NATIVE_GROUP=$ITK_WS_GROUP"
	echo "WORKSPACE_ROOT=$ITK_DATA/workspaces"
	echo "BACKUP_ROOT=$ITK_DATA/backups"
	echo "WORKSPACE_NATIVE_ENABLED=$([ "$ENABLE_NATIVE" = 1 ] && echo true || echo false)"
	echo "DOCKER_SOCKET=/var/run/docker.sock"
	echo "APP_LOG_LEVEL=info"
} >"$AGENT_ENV"
chown root:root "$AGENT_ENV" && chmod 0600 "$AGENT_ENV"

cat >"$ITK_ETC/web.env" <<EOF
HOST=127.0.0.1
PORT=3000
ORIGIN=$BASE_URL
NODE_ENV=production
BODY_SIZE_LIMIT=1M
EOF
[ "$SMALL" = 1 ] && echo "NODE_OPTIONS=--max-old-space-size=160" >>"$ITK_ETC/web.env"
chown root:"$ITK_USER" "$ITK_ETC/web.env" && chmod 0640 "$ITK_ETC/web.env"
umask 022
ok "Configuration in $ITK_ETC (secrets generated, permissions 0640/0600)"

# ---------------------------------------------------------------------------
# 7. systemd units
# ---------------------------------------------------------------------------
log "Installing systemd units"
for u in itk-cloud-api.service itk-cloud-worker.service itk-cloud-agent.service itk-cloud-web.service itk-cloud.target; do
	install -m 0644 "$SRC/deployments/systemd/$u" "/etc/systemd/system/$u"
done
# Older releases installed a ProtectSystem drop-in; it breaks loop mounts.
rm -f /etc/systemd/system/itk-cloud-agent.service.d/10-protect-etc.conf
systemctl daemon-reload
ok "Units installed"

# ---------------------------------------------------------------------------
# 8. Database migrations and services
# ---------------------------------------------------------------------------
log "Running database migrations"
run_itkctl migrate | sed 's/^/  /'
log "Starting services"
systemctl enable itk-cloud.target "${SERVICES_START_ORDER[@]}" >/dev/null 2>&1
for s in "${SERVICES_START_ORDER[@]}"; do
	systemctl restart "$s"
	sleep 1
done
wait_for "agent socket" 30 test -S /run/itk-cloud/agent.sock || die "The agent did not start (journalctl -u itk-cloud-agent)."
wait_for "API" 60 curl -fsS http://127.0.0.1:8080/health/ready || die "The API is not ready (journalctl -u itk-cloud-api)."
wait_for "web" 30 curl -fsS http://127.0.0.1:3000/ || die "The web console did not start (journalctl -u itk-cloud-web)."
ok "agent, api, worker and web are running"

# ---------------------------------------------------------------------------
# 9. Reverse proxy, TLS and firewall
# ---------------------------------------------------------------------------
log "Configuring Caddy (internal HTTPS)"
sed -e "s/__DOMAIN__/$DOMAIN/g" -e "s/__PROXY_PORT__/$PROXY_PORT/g" "$SRC/deployments/caddy/Caddyfile.tmpl" >/etc/caddy/Caddyfile
if [ "$WITH_MONITORING" = 1 ]; then
	cat >>/etc/caddy/Caddyfile <<EOF

# Grafana (optional monitoring stack)
https://$DOMAIN:3443 {
	tls internal
	reverse_proxy 127.0.0.1:3001
}
EOF
fi
mkdir -p /var/log/caddy && chown caddy:caddy /var/log/caddy
caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile >>"$LOG_FILE" 2>&1 || die "Invalid Caddy configuration."
systemctl enable caddy >/dev/null && systemctl restart caddy
CA_CERT=/var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt
wait_for "caddy CA" 20 test -f "$CA_CERT" || warn "Caddy root certificate not found yet at $CA_CERT."
ok "HTTPS on $BASE_URL and $PROXY_ORIGIN"

if [ "$CONFIGURE_FIREWALL" = 1 ]; then
	log "Configuring firewall (UFW)"
	ufw --force default deny incoming >/dev/null
	ufw --force default allow outgoing >/dev/null
	if [ -n "$SSH_FROM" ]; then ufw allow from "$SSH_FROM" to any port 22 proto tcp >/dev/null; else ufw allow 22/tcp >/dev/null; fi
	ufw allow 80/tcp >/dev/null
	ufw allow 443/tcp >/dev/null
	ufw allow "$PROXY_PORT"/tcp >/dev/null
	[ "$WITH_MONITORING" = 1 ] && ufw allow 3443/tcp >/dev/null
	if [ "$PROJECT_DBS" = 1 ]; then ufw allow from 10.213.0.0/16 to any port 5432 proto tcp >/dev/null; fi
	ufw --force enable >/dev/null
	ok "UFW: allow 22$([ -n "$SSH_FROM" ] && echo " from $SSH_FROM"), 80, 443, $PROXY_PORT; everything else denied"
fi

# ---------------------------------------------------------------------------
# 10. Optional monitoring stack
# ---------------------------------------------------------------------------
if [ "$WITH_MONITORING" = 1 ]; then
	log "Installing Prometheus, node_exporter and Grafana"
	apt-get install -y -qq prometheus prometheus-node-exporter >>"$LOG_FILE" 2>&1
	install -m 0644 "$SRC/deployments/monitoring/prometheus/prometheus.yml" /etc/prometheus/prometheus.yml
	install -m 0644 "$SRC/deployments/monitoring/prometheus/itk-cloud-rules.yml" /etc/prometheus/itk-cloud-rules.yml
	echo 'ARGS="--web.listen-address=127.0.0.1:9090"' >/etc/default/prometheus
	echo 'ARGS="--web.listen-address=127.0.0.1:9100 --collector.systemd"' >/etc/default/prometheus-node-exporter
	if ! command -v grafana-server >/dev/null 2>&1; then
		install -m 0755 -d /etc/apt/keyrings
		curl -fsSL https://apt.grafana.com/gpg.key | gpg --dearmor --yes -o /etc/apt/keyrings/grafana.gpg
		echo "deb [signed-by=/etc/apt/keyrings/grafana.gpg] https://apt.grafana.com stable main" >/etc/apt/sources.list.d/grafana.list
		apt-get update -qq && apt-get install -y -qq grafana >>"$LOG_FILE" 2>&1
	fi
	install -m 0644 "$SRC/deployments/monitoring/grafana/provisioning/datasources/itk-cloud.yml" /etc/grafana/provisioning/datasources/itk-cloud.yml
	install -m 0644 "$SRC/deployments/monitoring/grafana/provisioning/dashboards/itk-cloud.yml" /etc/grafana/provisioning/dashboards/itk-cloud.yml
	install -d -o grafana -g grafana /var/lib/grafana/dashboards/itk-cloud
	install -m 0644 "$SRC/deployments/monitoring/grafana/dashboards/itk-cloud-host.json" /var/lib/grafana/dashboards/itk-cloud/
	sed -i -e 's/^;\?http_addr =.*/http_addr = 127.0.0.1/' -e 's/^;\?http_port =.*/http_port = 3001/' \
		-e "s#^;\?root_url =.*#root_url = https://$DOMAIN:3443/#" /etc/grafana/grafana.ini
	systemctl enable --now prometheus prometheus-node-exporter grafana-server >/dev/null
	systemctl restart prometheus prometheus-node-exporter grafana-server
	env_set "$ENV_FILE" PROMETHEUS_URL "http://127.0.0.1:9090"
	env_set "$ENV_FILE" GRAFANA_URL "https://$DOMAIN:3443"
	systemctl restart itk-cloud-api
	ok "Monitoring stack installed (Grafana: https://$DOMAIN:3443, change the admin password on first login)"
fi

# ---------------------------------------------------------------------------
# 11. Workspace images
# ---------------------------------------------------------------------------
case "$IMAGES" in
none) IMG_LIST=() ;;
core) IMG_LIST=(ubuntu-dev python-dev) ;;
all) IMG_LIST=() ; ALL_IMAGES=1 ;;
*) IFS=, read -r -a IMG_LIST <<<"$IMAGES" ;;
esac
if [ "${ALL_IMAGES:-0}" = 1 ] || [ ${#IMG_LIST[@]} -gt 0 ]; then
	log "Building workspace images (this can take a while)"
	if [ "${ALL_IMAGES:-0}" = 1 ]; then
		"$SRC/installer/build-workspace-images.sh" >>"$LOG_FILE" 2>&1 || warn "Some images failed to build; see $LOG_FILE."
	else
		"$SRC/installer/build-workspace-images.sh" "${IMG_LIST[@]}" >>"$LOG_FILE" 2>&1 || warn "Some images failed to build; see $LOG_FILE."
	fi
	ok "Images: $(docker images --format '{{.Repository}}:{{.Tag}}' | { grep '^itk-cloud/' || true; } | paste -sd' ' -)"
fi

# ---------------------------------------------------------------------------
# 12. First administrator
# ---------------------------------------------------------------------------
HAS_ADMIN=$(sudo -u postgres psql -d itk_cloud -tAc "SELECT count(*) FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE r.key='super_admin'")
if [ "${HAS_ADMIN:-0}" = 0 ]; then
	log "Creating the first super administrator"
	ask ADMIN_USER "Administrator username" "admin"
	ask ADMIN_EMAIL "Administrator email" ""
	ask ADMIN_NAME "Administrator full name" "Jenderal Cloud OS Administrator"
	GENERATED_PW=0
	if [ -z "${ITK_ADMIN_PASSWORD:-}" ]; then
		if [ "$NON_INTERACTIVE" = 1 ]; then
			ITK_ADMIN_PASSWORD="$(random_password 20)Aa1!"
			GENERATED_PW=1
		else
			while :; do
				read -r -s -p "Administrator password (min 12 chars, 3 character classes): " p1; echo
				read -r -s -p "Repeat password: " p2; echo
				[ "$p1" = "$p2" ] && [ ${#p1} -ge 12 ] && break
				warn "Passwords do not match or are too short."
			done
			ITK_ADMIN_PASSWORD="$p1"
		fi
	fi
	printf '%s\n' "$ITK_ADMIN_PASSWORD" | run_itkctl admin create --username "$ADMIN_USER" --email "$ADMIN_EMAIL" --name "$ADMIN_NAME" --password-stdin | sed 's/^/  /'
	[ "$GENERATED_PW" = 1 ] && ADMIN_PW_NOTE="$ITK_ADMIN_PASSWORD"
	unset ITK_ADMIN_PASSWORD p1 p2
else
	ok "A super administrator already exists"
fi

# ---------------------------------------------------------------------------
# 13. Final health check
# ---------------------------------------------------------------------------
log "Final health check"
run_itkctl health | sed 's/^/  /' || die "Health check failed."
curl -fsS --resolve "$DOMAIN:443:127.0.0.1" --cacert "$CA_CERT" "https://$DOMAIN/health/live" >/dev/null 2>&1 &&
	ok "HTTPS endpoint reachable" || warn "HTTPS check via Caddy failed; verify DNS and 'journalctl -u caddy'."
echo "$ITK_VERSION" >"$ITK_STATE/installed-version"

cat <<EOF

${C_BLD}${C_GRN}Jenderal Cloud OS $ITK_VERSION is installed.${C_RST}

  Console:            $BASE_URL
  Workspace apps:     $PROXY_ORIGIN
  Internal CA cert:   $CA_CERT
                      (install it as a trusted root on lab clients; see docs/installation.md)
  Configuration:      $ITK_ETC/itk-cloud.env
  Logs:               journalctl -u 'itk-cloud-*'
EOF
if [ "${BACKUP_KEY_CREATED:-0}" = 1 ]; then
	cat <<EOF

  ${C_YLW}${C_BLD}IMPORTANT — backup encryption key${C_RST}
  The private key needed to RESTORE backups was written to $KEY_FILE.
  Copy it to an offline, secure place (password manager / sealed envelope)
  and then delete it from this server:  shred -u $KEY_FILE
EOF
fi
if [ -n "${ADMIN_PW_NOTE:-}" ]; then
	cat <<EOF

  ${C_YLW}${C_BLD}Generated administrator password (shown once):${C_RST} $ADMIN_PW_NOTE
  Sign in as '$ADMIN_USER', enrol MFA and change the password.
EOF
fi
cat <<EOF

  Next steps: point DNS for $DOMAIN to this server, trust the CA on clients,
  sign in, enrol MFA, review quota packages against the hardware inventory,
  and configure BACKUP_COPY_DIR for off-server backup copies.
EOF
