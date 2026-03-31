#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=./lib/node_bin.sh
. "$ROOT_DIR/scripts/lib/node_bin.sh"
# shellcheck source=./lib/npm_safe_env.sh
. "$ROOT_DIR/scripts/lib/npm_safe_env.sh"
PACK_DIR="${PACK_DIR:-$ROOT_DIR/dist/npm}"
ROOT_PACKAGE_NAME="@olhapi/maestro"

usage() {
  cat <<'EOF'
Usage: scripts/smoke_npm_package.sh <version>

Installs the packed Maestro launcher package into a temporary project, then
verifies the npm-installed CLI launches, serves the dashboard through Docker,
and preserves exit codes.

Examples:
  scripts/smoke_npm_package.sh v1.2.3
  MAESTRO_SMOKE_IMAGE=maestro-smoke:local scripts/smoke_npm_package.sh 1.2.3
EOF
}

if [[ $# -ne 1 ]]; then
  usage >&2
  exit 1
fi

RAW_VERSION="$1"
VERSION="${RAW_VERSION#v}"
SMOKE_IMAGE="${MAESTRO_SMOKE_IMAGE:-ghcr.io/olhapi/maestro:${VERSION}}"

tarball_filename() {
  local package_name="$1"
  printf '%s-%s.tgz\n' "${package_name#@}" "$VERSION" | tr '/' '-'
}

ROOT_TARBALL="$PACK_DIR/$(tarball_filename "$ROOT_PACKAGE_NAME")"

if [[ ! -f "$ROOT_TARBALL" ]]; then
  echo "missing root tarball: $ROOT_TARBALL" >&2
  exit 1
fi

ensure_maestro_node_bin

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

echo "Smoke testing $ROOT_PACKAGE_NAME with $SMOKE_IMAGE"
(
  cd "$TMP_DIR"
  run_clean_npm init -y >/dev/null 2>&1
  run_clean_npm install --no-package-lock "$ROOT_TARBALL" >/dev/null

  VERSION_OUTPUT="$(MAESTRO_IMAGE="$SMOKE_IMAGE" run_clean_npx --no-install maestro version)"
  if [[ "$VERSION_OUTPUT" != "maestro $VERSION" ]]; then
    echo "unexpected version output: $VERSION_OUTPUT" >&2
    exit 1
  fi

  MAESTRO_IMAGE="$SMOKE_IMAGE" run_clean_npx --no-install maestro --help >/dev/null

  set +e
  MAESTRO_IMAGE="$SMOKE_IMAGE" run_clean_npx --no-install maestro does-not-exist >/dev/null 2>&1
  STATUS=$?
  set -e
  if [[ "$STATUS" -eq 0 ]]; then
    echo "expected npm-installed maestro to preserve a non-zero exit code" >&2
    exit 1
  fi

  MAESTRO_IMAGE="$SMOKE_IMAGE" node "$ROOT_DIR/scripts/smoke_installed_dashboard.mjs" "$TMP_DIR"
)

echo "Smoke test passed"
