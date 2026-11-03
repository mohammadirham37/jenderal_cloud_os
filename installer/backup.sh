#!/usr/bin/env bash
# Run an encrypted backup immediately and optionally copy all backup files to
# a remote host with rsync over SSH (for an off-server copy).
#
#   sudo ./installer/backup.sh                       # platform DB + config now
#   sudo ITK_BACKUP_REMOTE=backup@nas:/srv/itk ./installer/backup.sh --sync
#
# Scheduled backups (all kinds) run automatically in the worker; this script
# is for ad-hoc backups and off-site synchronisation (e.g. from cron).
set -Eeuo pipefail
SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=installer/lib.sh
. "$SRC/installer/lib.sh"
require_root

SYNC=0
KINDS=(platform_db config workspace_metadata)
while [ $# -gt 0 ]; do
	case "$1" in
	--sync) SYNC=1; shift ;;
	--sync-only) SYNC=1; KINDS=(); shift ;;
	-h | --help) sed -n '2,10p' "$0"; exit 0 ;;
	*) die "Unknown option $1" ;;
	esac
done

for k in "${KINDS[@]}"; do
	log "Backup: $k"
	run_itkctl backup run --kind "$k" | sed 's/^/  /'
done
echo "  Workspace files and project databases are backed up by the scheduled job or from Administration → Backups."

if [ "$SYNC" = 1 ]; then
	[ -n "${ITK_BACKUP_REMOTE:-}" ] || die "Set ITK_BACKUP_REMOTE=user@host:/path for --sync."
	command -v rsync >/dev/null || apt-get install -y -qq rsync
	log "Synchronising $ITK_DATA/backups to $ITK_BACKUP_REMOTE"
	# Files are already age-encrypted; --delete is NOT used so the remote keeps
	# copies even if local files are lost or deleted by retention.
	rsync -a --partial --chmod=F600,D700 --exclude '*.partial' "$ITK_DATA/backups/" "$ITK_BACKUP_REMOTE/"
	ok "Backups synchronised"
fi
