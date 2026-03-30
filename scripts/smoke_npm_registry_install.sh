#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=./lib/npm_safe_env.sh
. "$ROOT_DIR/scripts/lib/npm_safe_env.sh"
PACK_DIR="${PACK_DIR:-$ROOT_DIR/dist/npm}"
ROOT_PACKAGE_NAME="@olhapi/maestro"
REGISTRY_PORT="${REGISTRY_PORT:-4873}"
REGISTRY_HOST="${REGISTRY_HOST:-127.0.0.1}"
REGISTRY_URL="http://${REGISTRY_HOST}:${REGISTRY_PORT}/"
VERDACCIO_PACKAGE="${VERDACCIO_PACKAGE:-verdaccio@6}"
REGISTRY_START_TIMEOUT_MS="${REGISTRY_START_TIMEOUT_MS:-60000}"

usage() {
  cat <<'EOF'
Usage: scripts/smoke_npm_registry_install.sh <version>

Publishes the packed Maestro npm packages to a temporary local registry, then
verifies that installing only @olhapi/maestro resolves the launcher package and
still serves the dashboard through Docker.

Examples:
  scripts/smoke_npm_registry_install.sh v1.2.3
  MAESTRO_SMOKE_IMAGE=maestro-smoke:local scripts/smoke_npm_registry_install.sh 1.2.3
EOF
}

if [[ $# -ne 1 ]]; then
  usage >&2
  exit 1
fi

RAW_VERSION="$1"
VERSION="${RAW_VERSION#v}"
PUBLISH_TAG="latest"
SMOKE_IMAGE="${MAESTRO_SMOKE_IMAGE:-ghcr.io/olhapi/maestro:${VERSION}}"

if [[ "$VERSION" == *-* ]]; then
  PUBLISH_TAG="smoke"
fi

tarball_filename() {
  local package_name="$1"
  printf '%s-%s.tgz\n' "${package_name#@}" "$VERSION" | tr '/' '-'
}

ROOT_TARBALL="$PACK_DIR/$(tarball_filename "$ROOT_PACKAGE_NAME")"
if [[ ! -f "$ROOT_TARBALL" ]]; then
  echo "missing root tarball: $ROOT_TARBALL" >&2
  exit 1
fi

TMP_DIR="$(mktemp -d)"
VERDACCIO_LOG="$TMP_DIR/verdaccio.log"
VERDACCIO_CONFIG="$TMP_DIR/verdaccio.yaml"
NPM_CONFIG_FILE="$TMP_DIR/.npmrc"
VERDACCIO_PID=""

cleanup() {
  if [[ -n "$VERDACCIO_PID" ]] && kill -0 "$VERDACCIO_PID" >/dev/null 2>&1; then
    kill "$VERDACCIO_PID" >/dev/null 2>&1 || true
    wait "$VERDACCIO_PID" >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

cat >"$VERDACCIO_CONFIG" <<EOF
storage: $TMP_DIR/storage
max_body_size: 64mb
uplinks: {}
packages:
  '@*/*':
    access: \$all
    publish: \$anonymous
    unpublish: \$anonymous
  '**':
    access: \$all
    publish: \$anonymous
    unpublish: \$anonymous
log:
  - { type: stdout, format: pretty, level: warn }
EOF

cat >"$NPM_CONFIG_FILE" <<EOF
registry=$REGISTRY_URL
@olhapi:registry=$REGISTRY_URL
strict-ssl=false
//${REGISTRY_HOST}:${REGISTRY_PORT}/:_authToken=smoke-token
EOF

echo "Starting temporary npm registry on $REGISTRY_URL"
run_clean_npx --yes "$VERDACCIO_PACKAGE" --version >/dev/null
run_clean_npx --yes "$VERDACCIO_PACKAGE" --config "$VERDACCIO_CONFIG" --listen "${REGISTRY_HOST}:${REGISTRY_PORT}" >"$VERDACCIO_LOG" 2>&1 &
VERDACCIO_PID=$!

if ! node -e '
const http = require("node:http");
const url = process.argv[1];
const deadline = Date.now() + Number(process.argv[2]);
function attempt() {
  const req = http.get(url, (res) => {
    res.resume();
    if (res.statusCode && res.statusCode < 500) {
      process.exit(0);
    }
    retry();
  });
  req.on("error", retry);
}
function retry() {
  if (Date.now() >= deadline) {
    process.stderr.write(`timed out waiting for registry at ${url}\n`);
    process.exit(1);
  }
  setTimeout(attempt, 250);
}
attempt();
' "$REGISTRY_URL" "$REGISTRY_START_TIMEOUT_MS"; then
  if [[ -s "$VERDACCIO_LOG" ]]; then
    cat "$VERDACCIO_LOG" >&2
  fi
  exit 1
fi

export npm_config_userconfig="$NPM_CONFIG_FILE"
export NPM_CONFIG_USERCONFIG="$NPM_CONFIG_FILE"

echo "Publishing root package to temporary registry"
run_clean_npm publish --registry "$REGISTRY_URL" --access public --tag "$PUBLISH_TAG" "$ROOT_TARBALL" >/dev/null

echo "Smoke testing registry-backed install of $ROOT_PACKAGE_NAME"
(
  cd "$TMP_DIR"
  run_clean_npm init -y >/dev/null 2>&1
  run_clean_npm install --registry "$REGISTRY_URL" --no-package-lock "${ROOT_PACKAGE_NAME}@${VERSION}" >/dev/null

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

echo "Registry smoke test passed"
