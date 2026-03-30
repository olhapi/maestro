#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=./lib/npm_safe_env.sh
. "$ROOT_DIR/scripts/lib/npm_safe_env.sh"
PACK_DIR="${PACK_DIR:-$ROOT_DIR/dist/npm}"
ROOT_PACKAGE_NAME="@olhapi/maestro"

usage() {
  cat <<'EOF'
Usage: scripts/smoke_install_script.sh <version>

Serves the packed Maestro launcher tarball through a temporary local metadata
endpoint, installs it with the curl installer, and verifies the installed
launcher still drives the Docker-backed dashboard smoke successfully.

Examples:
  scripts/smoke_install_script.sh v1.2.3
  MAESTRO_SMOKE_IMAGE=maestro-smoke:local scripts/smoke_install_script.sh 1.2.3
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

TMP_DIR="$(mktemp -d)"
SERVER_PID=""

cleanup() {
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" >/dev/null 2>&1; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

TARBALL_NAME="$(basename "$ROOT_TARBALL")"
SERVER_PORT_FILE="$TMP_DIR/server-port"
SERVER_LOG="$TMP_DIR/server.log"
INSTALL_ROOT="$TMP_DIR/install-root"
INSTALL_BIN_DIR="$TMP_DIR/install-bin"
PROJECT_DIR="$TMP_DIR/project"
HOME_DIR="$TMP_DIR/home"
mkdir -p "$PROJECT_DIR" "$HOME_DIR"

echo "Starting temporary installer metadata server"
node -e '
const fs = require("node:fs");
const http = require("node:http");

const host = process.argv[1];
const portFile = process.argv[2];
const version = process.argv[3];
const tarballName = process.argv[4];
const tarballPath = process.argv[5];
const metadataPath = "/@olhapi%2Fmaestro";
const tarballURLPath = `/${tarballName}`;

const server = http.createServer((req, res) => {
  if (req.url === metadataPath) {
    const payload = {
      "dist-tags": { latest: version },
      versions: {
        [version]: {
          dist: {
            tarball: `http://${host}:${server.address().port}${tarballURLPath}`,
          },
        },
      },
    };
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify(payload));
    return;
  }

  if (req.url === tarballURLPath) {
    const stream = fs.createReadStream(tarballPath);
    stream.on("error", (error) => {
      res.writeHead(500, { "content-type": "text/plain" });
      res.end(error.message);
    });
    res.writeHead(200, { "content-type": "application/octet-stream" });
    stream.pipe(res);
    return;
  }

  res.writeHead(404, { "content-type": "text/plain" });
  res.end("not found");
});

server.listen(0, host, () => {
  fs.writeFileSync(portFile, String(server.address().port));
});
' "127.0.0.1" "$SERVER_PORT_FILE" "$VERSION" "$TARBALL_NAME" "$ROOT_TARBALL" >"$SERVER_LOG" 2>&1 &
SERVER_PID=$!

if ! node -e '
const fs = require("node:fs");
const file = process.argv[1];
const deadline = Date.now() + 10000;

function attempt() {
  try {
    const value = fs.readFileSync(file, "utf8").trim();
    if (value) {
      process.exit(0);
    }
  } catch {}
  if (Date.now() >= deadline) {
    process.stderr.write(`timed out waiting for ${file}\n`);
    process.exit(1);
  }
  setTimeout(attempt, 100);
}

attempt();
' "$SERVER_PORT_FILE"; then
  if [[ -s "$SERVER_LOG" ]]; then
    cat "$SERVER_LOG" >&2
  fi
  exit 1
fi

SERVER_PORT="$(cat "$SERVER_PORT_FILE")"
METADATA_URL="http://127.0.0.1:${SERVER_PORT}/@olhapi%2Fmaestro"

echo "Smoke testing curl installer with $SMOKE_IMAGE"
MAESTRO_INSTALL_METADATA_URL="$METADATA_URL" \
MAESTRO_INSTALL_ROOT="$INSTALL_ROOT" \
MAESTRO_INSTALL_BIN_DIR="$INSTALL_BIN_DIR" \
"$ROOT_DIR/scripts/install_maestro.sh" "$VERSION" >/dev/null

INSTALL_BIN="$INSTALL_BIN_DIR/maestro"
if [[ ! -x "$INSTALL_BIN" ]]; then
  echo "installer did not create executable launcher: $INSTALL_BIN" >&2
  exit 1
fi

VERSION_OUTPUT="$(MAESTRO_IMAGE="$SMOKE_IMAGE" "$INSTALL_BIN" version)"
if [[ "$VERSION_OUTPUT" != "maestro $VERSION" ]]; then
  echo "unexpected version output: $VERSION_OUTPUT" >&2
  exit 1
fi

MAESTRO_IMAGE="$SMOKE_IMAGE" "$INSTALL_BIN" --help >/dev/null
HOME="$HOME_DIR" "$INSTALL_BIN" install --skills >/dev/null
if [[ ! -f "$HOME_DIR/.agents/skills/maestro/SKILL.md" ]]; then
  echo "installer smoke did not install bundled skills into ~/.agents" >&2
  exit 1
fi

MAESTRO_IMAGE="$SMOKE_IMAGE" MAESTRO_SMOKE_EXE="$INSTALL_BIN" node "$ROOT_DIR/scripts/smoke_installed_dashboard.mjs" "$PROJECT_DIR"

echo "Installer smoke test passed"
