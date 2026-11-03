#!/usr/bin/env bash
# Installs the host tools that are mounted read-only into workspace containers
# at /opt/itk-tools:
#   bin/itk-app-gate   token gate for built-in apps (from this release)
#   code-server/       VS Code in the browser (github.com/coder/code-server)
# Usage: sudo installer/install-workspace-tools.sh [--no-code-server]
# Environment: CODE_SERVER_VERSION (default: latest release).
# Failing to download code-server is not fatal: workspaces still work, only
# the VS Code app is unavailable until this script is re-run.
set -Eeuo pipefail
SRC="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=installer/lib.sh
. "$SRC/installer/lib.sh"

[ "$(id -u)" -eq 0 ] || die "Run as root."
TOOLS="${AGENT_TOOLS_DIR:-$ITK_PREFIX/tools}"
WITH_CODE_SERVER=1
[ "${1:-}" = "--no-code-server" ] && WITH_CODE_SERVER=0

case "$(uname -m)" in
x86_64) ARCH=amd64 ;;
aarch64) ARCH=arm64 ;;
*) die "Unsupported architecture $(uname -m)." ;;
esac

install -d -m 0755 "$TOOLS" "$TOOLS/bin"
if [ -x "$SRC/bin/itk-app-gate" ]; then
	install -m 0755 "$SRC/bin/itk-app-gate" "$TOOLS/bin/itk-app-gate"
	ok "Workspace app gate installed ($TOOLS/bin/itk-app-gate)"
else
	warn "bin/itk-app-gate missing from this release; built-in workspace apps are disabled."
fi

[ "$WITH_CODE_SERVER" = 1 ] || exit 0

version="${CODE_SERVER_VERSION:-latest}"
if [ "$version" = latest ]; then
	tag="$(curl -fsSL --retry 3 -o /dev/null -w '%{url_effective}' https://github.com/coder/code-server/releases/latest 2>/dev/null || true)"
	version="${tag##*/v}"
fi
if ! [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	warn "Could not determine the code-server version; VS Code is unavailable (re-run installer/install-workspace-tools.sh later)."
	exit 0
fi
dest="$TOOLS/code-server-$version"
if [ -x "$dest/bin/code-server" ]; then
	ln -sfn "code-server-$version" "$TOOLS/code-server"
	ok "code-server $version already installed"
	exit 0
fi
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
url="https://github.com/coder/code-server/releases/download/v$version/code-server-$version-linux-$ARCH.tar.gz"
if ! curl -fsSL --retry 3 -o "$tmp/cs.tar.gz" "$url"; then
	warn "Download of code-server $version failed ($url); VS Code is unavailable."
	exit 0
fi
mkdir -p "$tmp/x"
tar -xzf "$tmp/cs.tar.gz" -C "$tmp/x"
src_dir="$(find "$tmp/x" -mindepth 1 -maxdepth 1 -type d | head -n1)"
[ -x "$src_dir/bin/code-server" ] || { warn "Unexpected code-server archive layout; VS Code is unavailable."; exit 0; }
rm -rf "$dest"
mv "$src_dir" "$dest"
chown -R root:root "$dest"
chmod -R go-w "$dest"
ln -sfn "code-server-$version" "$TOOLS/code-server"
# Keep only the two most recent versions.
find "$TOOLS" -mindepth 1 -maxdepth 1 -type d -name 'code-server-*' -printf '%T@ %p\n' | sort -rn | tail -n +3 | cut -d' ' -f2- | xargs -r rm -rf
ok "code-server $version installed ($TOOLS/code-server)"
