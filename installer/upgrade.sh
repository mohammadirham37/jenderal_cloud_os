#!/usr/bin/env bash
# Upgrade an existing Jenderal Cloud OS installation to the release this script
# belongs to.
#
#   sudo ./installer/upgrade.sh [--yes] [--skip-backup]
#
# Steps: version check → encrypted pre-upgrade backup of the platform
# database → install release side by side → stop services in order →
# switch the 'current' symlink → run migrations → start services → health
# check. If the new release fails its health check, binaries and units are
# rolled back to the previous release automatically. Database migrations are
# NOT rolled back automatically (they may be irreversible); restore the
# pre-upgrade backup with `itkctl restore platform-db` if needed
# (docs/backup-restore.md).
set -Eeuo pipefail
SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=installer/lib.sh
. "$SRC/installer/lib.sh"

ASSUME_YES=0
SKIP_BACKUP=0
while [ $# -gt 0 ]; do
	case "$1" in
	--yes | -y) ASSUME_YES=1; shift ;;
	--skip-backup) SKIP_BACKUP=1; shift ;;
	-h | --help) sed -n '2,15p' "$0"; exit 0 ;;
	*) die "Unknown option $1" ;;
	esac
done
export ASSUME_YES
require_root

[ -L "$ITK_PREFIX/current" ] || die "Jenderal Cloud OS is not installed (no $ITK_PREFIX/current). Use install.sh."
CURRENT_DIR="$(readlink -f "$ITK_PREFIX/current")"
CURRENT="$(itk_version "$CURRENT_DIR")"
TARGET="$(itk_version "$SRC")"
log "Upgrading Jenderal Cloud OS: $CURRENT → $TARGET"
[ "$CURRENT" != "$TARGET" ] || { confirm "Version $TARGET is already installed. Reinstall it?" || exit 0; }
if [ "$(printf '%s\n%s\n' "$CURRENT" "$TARGET" | sort -V | tail -1)" != "$TARGET" ]; then
	warn "Target $TARGET is older than the installed $CURRENT (downgrade)."
	confirm "Downgrades are not supported for database migrations. Continue anyway?" || exit 1
fi
for f in bin/itk-cloud-api bin/itk-cloud-worker bin/itk-cloud-agent bin/itkctl web/build/index.js; do
	[ -e "$SRC/$f" ] || die "Release file missing: $f"
done
confirm "Proceed with the upgrade? Services will be briefly unavailable." || exit 1

# 1. Pre-upgrade backup (encrypted with the configured age recipient).
if [ "$SKIP_BACKUP" = 0 ]; then
	log "Creating pre-upgrade backup of the platform database"
	run_itkctl backup run --kind platform_db --trigger pre_upgrade | sed 's/^/  /' || die "Pre-upgrade backup failed; aborting (nothing changed)."
	run_itkctl backup run --kind config --trigger pre_upgrade | sed 's/^/  /' || warn "Configuration backup failed."
else
	warn "Skipping pre-upgrade backup (--skip-backup)."
fi

# 2. Install the new release next to the old one.
REL="$ITK_PREFIX/releases/$TARGET"
log "Installing release files to $REL"
rm -rf "$REL.tmp" && mkdir -p "$REL.tmp"
cp -a "$SRC/bin" "$SRC/web" "$SRC/deployments" "$SRC/installer" "$REL.tmp/"
[ -d "$SRC/docs" ] && cp -a "$SRC/docs" "$REL.tmp/"
echo "$TARGET" >"$REL.tmp/VERSION"
chown -R root:root "$REL.tmp" && chmod -R go-w "$REL.tmp"
if [ "$REL" != "$CURRENT_DIR" ]; then rm -rf "$REL"; fi
[ "$REL" = "$CURRENT_DIR" ] && rm -rf "$REL.prev" && mv "$REL" "$REL.prev" && CURRENT_DIR="$REL.prev"
mv "$REL.tmp" "$REL"

rollback() {
	warn "Rolling back to $CURRENT"
	for s in "${SERVICES_STOP_ORDER[@]}"; do systemctl stop "$s" || true; done
	ln -sfn "$CURRENT_DIR" "$ITK_PREFIX/current.new" && mv -Tf "$ITK_PREFIX/current.new" "$ITK_PREFIX/current"
	for u in "${UNITS[@]}"; do
		[ -f "$CURRENT_DIR/deployments/systemd/$u" ] && install -m 0644 "$CURRENT_DIR/deployments/systemd/$u" "/etc/systemd/system/$u"
	done
	systemctl daemon-reload
	for s in "${SERVICES_START_ORDER[@]}"; do systemctl start "$s" || true; done
	die "Upgrade failed and binaries were rolled back to $CURRENT. If migrations ran, the database schema may be newer than the old binaries expect: restore the pre-upgrade backup (docs/backup-restore.md) or fix forward."
}

# 3. Stop, switch, migrate, start.
log "Stopping services"
for s in "${SERVICES_STOP_ORDER[@]}"; do systemctl stop "$s" || true; done
ln -sfn "$REL" "$ITK_PREFIX/current.new" && mv -Tf "$ITK_PREFIX/current.new" "$ITK_PREFIX/current"
# Includes itk-cloud-updater.service (refreshed, never stopped: it may be
# the unit running this script).
for u in "${UNITS[@]}"; do
	install -m 0644 "$REL/deployments/systemd/$u" "/etc/systemd/system/$u"
done
# Drop-in from 0.1.0 that put the agent in a private mount namespace (breaks
# workspace volume mounts).
rm -f /etc/systemd/system/itk-cloud-agent.service.d/10-protect-etc.conf
systemctl daemon-reload
ensure_update_dirs

log "Running database migrations"
run_itkctl migrate | sed 's/^/  /' || rollback

# Small-server installs: cap the storage pre-filled for new workspaces at 2 GiB (0.1.9).
if compgen -G "/etc/postgresql/*/main/conf.d/90-itk-cloud-small.conf" >/dev/null; then
	sudo -u postgres psql -q -d itk_cloud -c "INSERT INTO platform_settings (key, value) VALUES ('workspaces.default_storage_cap_mb', '2048') ON CONFLICT (key) DO NOTHING" >/dev/null || true
fi

# --- rebrand: workspace images itk-cloud/<name> -> jenderal/<name> (0.1.6) ---
if command -v docker >/dev/null 2>&1; then
	docker image ls --format '{{.Repository}}:{{.Tag}}' 2>/dev/null | grep '^itk-cloud/' | while read -r old; do
		new="jenderal/${old#itk-cloud/}"
		if docker tag "$old" "$new" && docker rmi "$old" >/dev/null; then
			ok "Re-tagged workspace image $old as $new"
		fi
	done || true
fi
# --- end rebrand ---

# Workspace tools (app gate, code-server) used by built-in workspace apps.
"$SRC/installer/install-workspace-tools.sh" || warn "Workspace tools could not be updated; VS Code may be unavailable."

log "Starting services"
for s in "${SERVICES_START_ORDER[@]}"; do systemctl start "$s" || rollback; sleep 1; done

# 4. Health check.
log "Health check"
wait_for "agent" 30 test -S /run/itk-cloud/agent.sock || rollback
wait_for "api" 60 curl -fsS http://127.0.0.1:8080/health/ready || rollback
wait_for "web" 30 curl -fsS http://127.0.0.1:3000/ || rollback
run_itkctl health | sed 's/^/  /' || rollback
echo "$TARGET" >"$ITK_STATE/installed-version"

# Keep the three most recent releases.
mapfile -t old_releases < <(find "$ITK_PREFIX/releases" -mindepth 1 -maxdepth 1 -type d ! -path "$REL" -printf '%T@ %p\n' | sort -rn | awk 'NR > 2 {print $2}')
for d in "${old_releases[@]}"; do rm -rf -- "$d"; done
ok "Upgrade to $TARGET completed. Previous release kept at $CURRENT_DIR for manual rollback."
