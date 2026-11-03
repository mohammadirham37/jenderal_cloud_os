#!/usr/bin/env bash
# Remove Jenderal Cloud OS services and binaries.
#
#   sudo ./installer/uninstall.sh                  # keep data, config and backups
#   sudo ./installer/uninstall.sh --purge          # also delete workspaces, database and config
#   sudo ./installer/uninstall.sh --purge --purge-backups   # ...and backups
#
# PostgreSQL, Redis, Docker and Caddy packages are left installed. Workspace
# containers managed by Jenderal Cloud OS are stopped and removed only with --purge.
set -Eeuo pipefail
SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=installer/lib.sh
. "$SRC/installer/lib.sh"
require_root

PURGE=0
PURGE_BACKUPS=0
ASSUME_YES=0
while [ $# -gt 0 ]; do
	case "$1" in
	--purge) PURGE=1; shift ;;
	--purge-backups) PURGE_BACKUPS=1; shift ;;
	--yes) ASSUME_YES=1; shift ;;
	-h | --help) sed -n '2,10p' "$0"; exit 0 ;;
	*) die "Unknown option $1" ;;
	esac
done
export ASSUME_YES

if [ "$PURGE" = 1 ]; then
	warn "--purge permanently deletes all workspaces, user files, project databases and the platform database."
	if [ "$ASSUME_YES" != 1 ]; then
		read -r -p "Type DELETE EVERYTHING to continue: " ans
		[ "$ans" = "DELETE EVERYTHING" ] || die "Aborted."
	fi
fi
confirm "Remove Jenderal Cloud OS services and binaries?" || exit 0

log "Stopping and disabling services"
for s in "${SERVICES_STOP_ORDER[@]}"; do systemctl disable --now "$s" >/dev/null 2>&1 || true; done
systemctl disable itk-cloud.target >/dev/null 2>&1 || true
rm -f /etc/systemd/system/itk-cloud-{api,worker,agent,web}.service /etc/systemd/system/itk-cloud.target
rm -rf /etc/systemd/system/itk-cloud-agent.service.d
systemctl daemon-reload
rm -f /usr/local/bin/itkctl
if grep -q "Jenderal Cloud OS reverse proxy" /etc/caddy/Caddyfile 2>/dev/null; then
	mv /etc/caddy/Caddyfile "/etc/caddy/Caddyfile.itk-cloud.$(date +%s).bak"
	printf '# Jenderal Cloud OS was uninstalled.\n' >/etc/caddy/Caddyfile
	systemctl reload caddy || true
fi
ok "Services removed"

if [ "$PURGE" = 1 ]; then
	log "Removing workspace containers and data"
	if command -v docker >/dev/null; then
		docker ps -aq --filter label=itk.managed=true | xargs -r docker rm -f >/dev/null
		docker network ls -q --filter label=itk.managed=true | xargs -r docker network rm >/dev/null || true
	fi
	# Unmount loopback workspace volumes before deleting.
	awk -v root="$ITK_DATA/workspaces/" 'index($2, root) == 1 {print $2}' /proc/mounts | xargs -r -n1 umount -l || true
	rm -rf "$ITK_DATA/workspaces"
	# Native workspace accounts.
	getent passwd | awk -F: '$1 ~ /^itk-[0-9a-f]+$/ {print $1}' | xargs -r -n1 userdel || true
	log "Dropping databases"
	sudo -u postgres psql -tAc "SELECT datname FROM pg_database WHERE datname LIKE 'itk\\_%' ESCAPE '\\'" |
		while read -r d; do [ -n "$d" ] && sudo -u postgres dropdb --if-exists "$d"; done
	sudo -u postgres dropdb --if-exists itk_cloud
	sudo -u postgres psql -tAc "SELECT rolname FROM pg_roles WHERE rolname LIKE 'itk\\_%' ESCAPE '\\'" |
		while read -r r; do [ -n "$r" ] && sudo -u postgres psql -qc "DROP ROLE IF EXISTS \"$r\""; done
	sudo -u postgres psql -qc 'DROP ROLE IF EXISTS itk_cloud' || true
	rm -rf "$ITK_ETC" "$ITK_STATE" /etc/redis/itk-cloud.conf
	sed -i '/^include \/etc\/redis\/itk-cloud.conf/d' /etc/redis/redis.conf 2>/dev/null || true
	if [ "$PURGE_BACKUPS" = 1 ]; then rm -rf "$ITK_DATA/backups"; else warn "Backups kept in $ITK_DATA/backups"; fi
	userdel "$ITK_USER" 2>/dev/null || true
	groupdel "$ITK_WS_GROUP" 2>/dev/null || true
	ok "All Jenderal Cloud OS data removed"
else
	warn "Data kept: $ITK_DATA, $ITK_ETC and the itk_cloud database. Re-run with --purge to delete them."
fi
rm -rf "$ITK_PREFIX"
ok "Jenderal Cloud OS uninstalled"
