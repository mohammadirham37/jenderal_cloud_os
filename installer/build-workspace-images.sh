#!/usr/bin/env bash
# Build the ITK Cloud workspace images on this server.
# Usage: sudo installer/build-workspace-images.sh [image ...]
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIR="$ROOT/deployments/docker/workspace-images"
TAG="${ITK_IMAGE_TAG:-1.0}"
ALL=(ubuntu-dev go-dev php-laravel node-dev python-dev python-datascience jupyterlab ml-cuda cpp-dev java-dev web-dev)
IMAGES=("$@")
[ ${#IMAGES[@]} -eq 0 ] && IMAGES=("${ALL[@]}")
command -v docker >/dev/null || { echo "docker is not installed" >&2; exit 1; }
failed=()
for name in "${IMAGES[@]}"; do
  if [ ! -f "$DIR/$name/Dockerfile" ]; then
    echo "unknown image: $name" >&2; failed+=("$name"); continue
  fi
  echo "==> building itk-cloud/$name:$TAG"
  if ! docker build --pull -t "itk-cloud/$name:$TAG" -f "$DIR/$name/Dockerfile" "$DIR"; then
    failed+=("$name")
  fi
done
if [ ${#failed[@]} -gt 0 ]; then
  echo "failed to build: ${failed[*]}" >&2
  exit 1
fi
echo "all requested images built"
