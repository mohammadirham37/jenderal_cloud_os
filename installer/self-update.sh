#!/usr/bin/env bash
# Jenderal Cloud OS self-updater. Runs as root in itk-cloud-updater.service,
# which the host agent starts (platform.update) when a super administrator
# clicks "Update" in Administration → System update. Not meant to be run by
# hand; to upgrade manually, extract a release and run installer/upgrade.sh.
#
# Reads   $ITK_UPDATE_DIR/request.json   (written by the agent, root-only)
# Writes  $ITK_UPDATE_DIR/status.json    (queued → downloading → verifying →
#                                         upgrading → succeeded | failed)
# Logs to $ITK_LOG_DIR/update.log        (previous run: update.log.1)
#
# Steps: validate the request (version, repository = the agent's UPDATE_REPO,
# CPU architecture) → download the release tarball and its .sha256 from
# https://github.com/<repo>/releases/download/v<version>/ → verify the
# checksum → extract into $ITK_UPDATE_DIR/releases/<version> → run that
# release's installer/upgrade.sh --yes (backup, migrate, restart, health
# check, automatic rollback) → record the result.
#
# This unit is separate from the itk-cloud-* services, so it keeps running
# while upgrade.sh stops and restarts the agent, API, worker and web.
set -Eeuo pipefail
umask 022

SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG_FILE=/dev/null # everything is captured in the update log below
# shellcheck source=installer/lib.sh
. "$SRC/installer/lib.sh"

REQUEST="$ITK_UPDATE_DIR/request.json"
STATUS="$ITK_UPDATE_DIR/status.json"
UPDATE_LOG="$ITK_LOG_DIR/update.log"
DEFAULT_REPO="mohammadirham37/jenderal_cloud_os"
UA="jenderal-cloud-os-updater"

require_root
ensure_update_dirs
if [ -f "$UPDATE_LOG" ]; then mv -f "$UPDATE_LOG" "$UPDATE_LOG.1"; fi
: >"$UPDATE_LOG"
chmod 0640 "$UPDATE_LOG"
exec >>"$UPDATE_LOG" 2>&1 </dev/null
export NON_INTERACTIVE=1

VERSION=""
FROM_VERSION="$(itk_version "$ITK_PREFIX/current")"
STARTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
STEP="starting"
FAIL_MSG=""
DONE=0

# json_str VALUE -> JSON string literal (control characters removed).
json_str() {
	local s
	s="$(printf '%s' "$1" | tr -d '\000-\010\013-\037')"
	s="${s//\\/\\\\}"
	s="${s//\"/\\\"}"
	s="${s//$'\t'/ }"
	s="${s//$'\n'/\\n}"
	printf '"%s"' "$s"
}

# write_status STATE MESSAGE
write_status() {
	local state=$1 msg=$2 now finished="" tmp
	now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	case "$state" in
	succeeded | failed) finished="$now" ;;
	esac
	tmp="$(mktemp "$ITK_UPDATE_DIR/.status.XXXXXX")"
	{
		printf '{\n'
		printf '  "state": %s,\n' "$(json_str "$state")"
		printf '  "version": %s,\n' "$(json_str "$VERSION")"
		printf '  "from_version": %s,\n' "$(json_str "$FROM_VERSION")"
		printf '  "message": %s,\n' "$(json_str "$msg")"
		printf '  "started_at": %s,\n' "$(json_str "$STARTED_AT")"
		printf '  "updated_at": %s,\n' "$(json_str "$now")"
		printf '  "finished_at": %s\n' "$(json_str "$finished")"
		printf '}\n'
	} >"$tmp"
	chmod 0644 "$tmp"
	mv -f "$tmp" "$STATUS"
	log "[$state] $msg"
}

fail() {
	FAIL_MSG=$1
	die "$1"
}

on_exit() {
	local rc=$?
	rm -f "$REQUEST"
	if [ "$DONE" != 1 ]; then
		if [ -z "$FAIL_MSG" ]; then
			FAIL_MSG="Update failed during step '$STEP' (exit code $rc). See $UPDATE_LOG."
		fi
		write_status failed "$FAIL_MSG" || true
	fi
}
trap on_exit EXIT

# json_field KEY FILE -> string value of a top-level "KEY": "value" line
# (request.json is pretty-printed by the agent, one field per line).
json_field() {
	sed -n 's/^[[:space:]]*"'"$1"'"[[:space:]]*:[[:space:]]*"\([^"\\]*\)"[[:space:]]*,\{0,1\}[[:space:]]*$/\1/p' "$2" | head -n 1
}

host_arch() {
	case "$(uname -m)" in
	x86_64 | amd64) echo amd64 ;;
	aarch64 | arm64) echo arm64 ;;
	*) echo unsupported ;;
	esac
}

# version_gt A B -> true if A sorts after B (sort -V, as upgrade.sh does).
version_gt() {
	[ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -n 1)" = "$1" ]
}

log "Jenderal Cloud OS self-update started ($STARTED_AT)"

# ---------------------------------------------------------------------------
# 1. Validate the request.
# ---------------------------------------------------------------------------
STEP="validate"
[ -f "$REQUEST" ] || fail "No update request found ($REQUEST)."
[ ! -L "$REQUEST" ] || fail "Refusing a symlinked update request."
VERSION="$(json_field version "$REQUEST")"
REPO="$(json_field repo "$REQUEST")"
ARCH="$(json_field arch "$REQUEST")"
REQUESTED_BY="$(json_field requested_by "$REQUEST")"
[[ "$VERSION" =~ ^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})(-[0-9A-Za-z]+(\.[0-9A-Za-z]+){0,4})?$ ]] ||
	{ VERSION=""; fail "Invalid version in the update request."; }
[[ "$REPO" =~ ^[A-Za-z0-9_-][A-Za-z0-9_.-]{0,99}/[A-Za-z0-9_-][A-Za-z0-9_.-]{0,99}$ ]] || fail "Invalid repository in the update request."
ALLOWED_REPO="$(env_get "$ITK_ETC/agent.env" UPDATE_REPO)"
ALLOWED_REPO="${ALLOWED_REPO:-$DEFAULT_REPO}"
[ "$REPO" = "$ALLOWED_REPO" ] || fail "Repository $REPO is not the configured update repository ($ALLOWED_REPO)."
[ "$ARCH" = "$(host_arch)" ] || fail "Architecture $ARCH does not match this server ($(host_arch))."
version_gt "$VERSION" "$FROM_VERSION" || fail "Version $VERSION is not newer than the installed $FROM_VERSION."
log "Request: $FROM_VERSION → $VERSION from github.com/$REPO ($ARCH), requested by ${REQUESTED_BY:-unknown}"

NAME="itk-cloud-$VERSION-linux-$ARCH"
TARBALL="$NAME.tar.gz"
BASE_URL="https://github.com/$REPO/releases/download/v$VERSION"
DL_DIR="$ITK_UPDATE_DIR/downloads"
REL_DIR="$ITK_UPDATE_DIR/releases"

# Remove leftovers of earlier runs.
rm -rf -- "$DL_DIR" "$REL_DIR"
install -d -m 0700 -o root -g root "$DL_DIR" "$REL_DIR"

avail_kb="$(df --output=avail -k "$ITK_UPDATE_DIR" | tail -n 1 | tr -d ' ')"
if [ "${avail_kb:-0}" -lt 1048576 ]; then
	fail "Not enough free disk space for the update (need at least 1 GiB in $ITK_UPDATE_DIR)."
fi

# ---------------------------------------------------------------------------
# 2. Download.
# ---------------------------------------------------------------------------
STEP="download"
write_status downloading "Downloading $TARBALL"
fetch() { # fetch URL OUT
	curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --silent --show-error --location \
		--retry 5 --retry-delay 3 --retry-connrefused --connect-timeout 20 --max-time 1800 \
		--user-agent "$UA" --output "$2" "$1"
}
fetch "$BASE_URL/$TARBALL.sha256" "$DL_DIR/$TARBALL.sha256" || fail "Could not download $TARBALL.sha256 from $BASE_URL."
fetch "$BASE_URL/$TARBALL" "$DL_DIR/$TARBALL" || fail "Could not download $TARBALL from $BASE_URL."
log "Downloaded $(du -h "$DL_DIR/$TARBALL" | cut -f1)"

# ---------------------------------------------------------------------------
# 3. Verify.
# ---------------------------------------------------------------------------
STEP="verify"
write_status verifying "Verifying the SHA-256 checksum"
expected="$(awk 'NR == 1 {print tolower($1)}' "$DL_DIR/$TARBALL.sha256")"
[[ "$expected" =~ ^[0-9a-f]{64}$ ]] || fail "The checksum file is malformed."
actual="$(sha256sum "$DL_DIR/$TARBALL" | awk '{print $1}')"
[ "$actual" = "$expected" ] || fail "Checksum mismatch for $TARBALL (expected $expected, got $actual). The download was discarded."
ok "Checksum verified ($actual)"

# Every archive member must live under $NAME/ (no absolute paths or '..').
tar -tzf "$DL_DIR/$TARBALL" >"$DL_DIR/members.txt" || fail "The release archive is not a valid tar.gz file."
name_re="${NAME//./\\.}"
outside="$(grep -Evc "^$name_re(/|\$)" "$DL_DIR/members.txt" || true)"
dotdot="$(grep -Ec '(^|/)\.\.(/|$)' "$DL_DIR/members.txt" || true)"
if [ "$outside" != 0 ] || [ "$dotdot" != 0 ]; then
	fail "The release archive contains unexpected paths."
fi
tar --no-same-owner -xzf "$DL_DIR/$TARBALL" -C "$REL_DIR"
NEW="$REL_DIR/$NAME"
[ -x "$NEW/installer/upgrade.sh" ] || fail "The release does not contain installer/upgrade.sh."
[ "$(itk_version "$NEW")" = "$VERSION" ] || fail "The release VERSION file does not match $VERSION."
rm -f -- "$DL_DIR/$TARBALL"

# ---------------------------------------------------------------------------
# 4. Upgrade (backup → install side by side → stop → migrate → start →
#    health check; upgrade.sh rolls back binaries automatically on failure).
# ---------------------------------------------------------------------------
STEP="upgrade"
write_status upgrading "Installing $VERSION: backup, migrations and service restart (the console is briefly unavailable)"
rc=0
"$NEW/installer/upgrade.sh" --yes || rc=$?
if [ "$rc" -ne 0 ]; then
	fail "upgrade.sh failed (exit code $rc). If services had already been switched, the previous release ($FROM_VERSION) was restored automatically. See $UPDATE_LOG."
fi
INSTALLED="$(itk_version "$ITK_PREFIX/current")"
[ "$INSTALLED" = "$VERSION" ] || fail "upgrade.sh finished but $ITK_PREFIX/current is $INSTALLED, not $VERSION."

# ---------------------------------------------------------------------------
# 5. Clean up.
# ---------------------------------------------------------------------------
STEP="cleanup"
rm -rf -- "$DL_DIR" "$REL_DIR"
DONE=1
write_status succeeded "Jenderal Cloud OS $VERSION is installed and healthy"
ok "Self-update to $VERSION completed"
