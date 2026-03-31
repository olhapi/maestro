#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=./lib/npm_safe_env.sh
. "$ROOT_DIR/scripts/lib/npm_safe_env.sh"
PACK_DIR="${PACK_DIR:-$ROOT_DIR/dist/npm}"
ROOT_PACKAGE_NAME="@olhapi/maestro"

usage() {
  cat <<'EOF'
Usage: scripts/smoke_npm_package.sh <version> <target>

Installs the packed root package and the selected leaf package into a temporary
project, then verifies the npm-installed CLI launches, serves the dashboard,
and preserves exit codes.

Examples:
  scripts/smoke_npm_package.sh v1.2.3 darwin-arm64
  scripts/smoke_npm_package.sh 1.2.3 linux-x64-gnu
EOF
}

if [[ $# -ne 2 ]]; then
  usage >&2
  exit 1
fi

RAW_VERSION="$1"
VERSION="${RAW_VERSION#v}"
TARGET="$2"

tarball_filename() {
  local package_name="$1"
  printf '%s-%s.tgz\n' "${package_name#@}" "$VERSION" | tr '/' '-'
}

leaf_package_name() {
  case "$1" in
    darwin-arm64) echo "@olhapi/maestro-darwin-arm64" ;;
    darwin-x64) echo "@olhapi/maestro-darwin-x64" ;;
    linux-x64-gnu) echo "@olhapi/maestro-linux-x64-gnu" ;;
    linux-arm64-gnu) echo "@olhapi/maestro-linux-arm64-gnu" ;;
    win32-x64) echo "@olhapi/maestro-win32-x64" ;;
    *)
      echo "unsupported target: $1" >&2
      exit 1
      ;;
  esac
}

ROOT_TARBALL="$PACK_DIR/$(tarball_filename "$ROOT_PACKAGE_NAME")"
LEAF_PACKAGE_NAME="$(leaf_package_name "$TARGET")"
LEAF_TARBALL="$PACK_DIR/$(tarball_filename "$LEAF_PACKAGE_NAME")"

if [[ ! -f "$ROOT_TARBALL" ]]; then
  echo "missing root tarball: $ROOT_TARBALL" >&2
  exit 1
fi
if [[ ! -f "$LEAF_TARBALL" ]]; then
  echo "missing leaf tarball: $LEAF_TARBALL" >&2
  exit 1
fi

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

echo "Smoke testing $ROOT_PACKAGE_NAME with $LEAF_PACKAGE_NAME"
(
  cd "$TMP_DIR"
  run_clean_npm init -y >/dev/null 2>&1
  run_clean_npm install --no-package-lock "$LEAF_TARBALL" "$ROOT_TARBALL" >/dev/null

  VERSION_OUTPUT="$(run_clean_npx --no-install maestro version)"
  if [[ "$VERSION_OUTPUT" != "maestro $VERSION" ]]; then
    echo "unexpected version output: $VERSION_OUTPUT" >&2
    exit 1
  fi

  run_clean_npx --no-install maestro --help >/dev/null

  set +e
  run_clean_npx --no-install maestro does-not-exist >/dev/null 2>&1
  STATUS=$?
  set -e
  if [[ "$STATUS" -eq 0 ]]; then
    echo "expected npm-installed maestro to preserve a non-zero exit code" >&2
    exit 1
  fi

  node "$ROOT_DIR/scripts/smoke_installed_dashboard.mjs" "$TMP_DIR"
)

echo "Smoke test passed for $TARGET"
