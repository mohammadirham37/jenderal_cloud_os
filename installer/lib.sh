#!/usr/bin/env bash
# Shared helpers for the Jenderal Cloud OS installer scripts. Sourced, not executed.
# shellcheck disable=SC2034  # variables are used by the scripts that source this file

ITK_PREFIX="${ITK_PREFIX:-/opt/itk-cloud}"
ITK_ETC="${ITK_ETC:-/etc/itk-cloud}"
ITK_DATA="${ITK_DATA:-/srv/itk-cloud}"
ITK_STATE="${ITK_STATE:-/var/lib/itk-cloud}"
ITK_USER="itk-cloud"
ITK_WS_GROUP="itk-workspace"
NODE_VERSION="${ITK_NODE_VERSION:-v24.22.2}"   # Node.js Active LTS (Krypton), verified against nodejs.org/dist
SERVICES_START_ORDER=(itk-cloud-agent itk-cloud-api itk-cloud-worker itk-cloud-web)
SERVICES_STOP_ORDER=(itk-cloud-web itk-cloud-worker itk-cloud-api itk-cloud-agent)

if [ -t 1 ]; then
	C_RED=$'\e[31m'; C_GRN=$'\e[32m'; C_YLW=$'\e[33m'; C_BLU=$'\e[34m'; C_BLD=$'\e[1m'; C_RST=$'\e[0m'
else
	C_RED=''; C_GRN=''; C_YLW=''; C_BLU=''; C_BLD=''; C_RST=''
fi

LOG_FILE="${LOG_FILE:-/var/log/itk-cloud-install.log}"

log()   { printf '%s\n' "${C_BLU}==>${C_RST} $*"; printf '%s %s\n' "$(date -Is)" "$*" >>"$LOG_FILE" 2>/dev/null || true; }
ok()    { printf '%s\n' "  ${C_GRN}✓${C_RST} $*"; printf '%s OK %s\n' "$(date -Is)" "$*" >>"$LOG_FILE" 2>/dev/null || true; }
warn()  { printf '%s\n' "  ${C_YLW}!${C_RST} $*" >&2; printf '%s WARN %s\n' "$(date -Is)" "$*" >>"$LOG_FILE" 2>/dev/null || true; }
die()   { printf '%s\n' "${C_RED}✗ $*${C_RST}" >&2; printf '%s FATAL %s\n' "$(date -Is)" "$*" >>"$LOG_FILE" 2>/dev/null || true; exit 1; }

require_root() {
	[ "$(id -u)" -eq 0 ] || die "This script must be run as root (use sudo)."
}

# confirm "question" -> returns 0 for yes. Honours ASSUME_YES / NON_INTERACTIVE.
confirm() {
	if [ "${ASSUME_YES:-0}" = 1 ]; then return 0; fi
	if [ "${NON_INTERACTIVE:-0}" = 1 ]; then return 1; fi
	local ans
	read -r -p "$1 [y/N] " ans
	[[ "$ans" =~ ^[Yy]([Ee][Ss])?$ ]]
}

# ask VAR "prompt" "default" — interactive prompt unless the variable is set.
ask() {
	local var=$1 prompt=$2 def=${3:-} cur
	cur="${!var:-}"
	if [ -n "$cur" ]; then return 0; fi
	if [ "${NON_INTERACTIVE:-0}" = 1 ]; then
		[ -n "$def" ] || die "$var is required in non-interactive mode."
		printf -v "$var" '%s' "$def"
		return 0
	fi
	local ans
	read -r -p "$prompt${def:+ [$def]}: " ans
	printf -v "$var" '%s' "${ans:-$def}"
}

# random_secret N -> N random bytes, base64 (no newline).
random_secret() {
	head -c "${1:-32}" /dev/urandom | base64 -w0
}

random_password() { # avoids SIGPIPE-prone pipelines (scripts run with pipefail)
	local n=${1:-32} out=""
	while [ ${#out} -lt "$n" ]; do
		out+="$(head -c 96 /dev/urandom | base64 -w0 | tr -dc 'A-Za-z0-9')"
	done
	printf '%s' "${out:0:n}"
}

# env_get FILE KEY -> value of KEY in an env file (empty if missing).
env_get() {
	local file=$1 key=$2
	[ -f "$file" ] || return 0
	{ grep -E "^${key}=" "$file" || true; } | tail -n1 | cut -d= -f2-
}

# env_set FILE KEY VALUE -> sets/replaces KEY in an env file (no shell expansion).
env_set() {
	local file=$1 key=$2 val=$3 tmp
	tmp="$(mktemp)"
	if [ -f "$file" ]; then grep -vE "^${key}=" "$file" >"$tmp" || true; fi
	printf '%s=%s\n' "$key" "$val" >>"$tmp"
	cat "$tmp" >"$file"
	rm -f "$tmp"
}

wait_for() { # wait_for "description" seconds command...
	local secs=$2; shift 2
	local i
	for ((i = 0; i < secs; i++)); do
		if "$@" >/dev/null 2>&1; then return 0; fi
		sleep 1
	done
	return 1
}

service_running() { systemctl is-active --quiet "$1"; }

itk_version() { cat "$1/VERSION" 2>/dev/null || echo "unknown"; }

run_itkctl() { # runs itkctl as the service user with the production env
	sudo -u "$ITK_USER" env ITK_CLOUD_ENV_FILE="$ITK_ETC/itk-cloud.env" HOME="$ITK_STATE" \
		bash -c "cd '$ITK_STATE' && exec '$ITK_PREFIX/current/bin/itkctl' \"\$@\"" itkctl "$@"
}
