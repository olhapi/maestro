#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PACKAGE_TEMPLATE="$ROOT_DIR/packaging/npm/package.json.tmpl"
OUT_ROOT="${OUT_ROOT:-$ROOT_DIR/dist}"
STAGE_DIR="${STAGE_DIR:-$OUT_ROOT/npm-package}"
PACK_DIR="${PACK_DIR:-$OUT_ROOT/npm}"

usage() {
  cat <<'EOF'
Usage: scripts/package_npm_release.sh <version>

Builds the macOS arm64 Symphony binary, stages the npm package in dist/npm-package,
and creates an npm tarball in dist/npm.

Examples:
  scripts/package_npm_release.sh v1.2.3
  scripts/package_npm_release.sh 1.2.3
EOF
}

if [[ $# -ne 1 ]]; then
  usage >&2
  exit 1
fi

RAW_VERSION="$1"
if [[ ! "$RAW_VERSION" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "invalid version: $RAW_VERSION" >&2
  exit 1
fi

VERSION="${RAW_VERSION#v}"
BIN_PATH="$STAGE_DIR/bin/symphony"

if ! command -v go >/dev/null 2>&1; then
  echo "missing required command: go" >&2
  exit 1
fi

if ! command -v npm >/dev/null 2>&1; then
  echo "missing required command: npm" >&2
  exit 1
fi

if [[ ! -f "$PACKAGE_TEMPLATE" ]]; then
  echo "missing package template: $PACKAGE_TEMPLATE" >&2
  exit 1
fi

rm -rf "$STAGE_DIR" "$PACK_DIR"
mkdir -p "$STAGE_DIR/bin" "$PACK_DIR"

echo "Building symphony $VERSION for darwin/arm64"
(
  cd "$ROOT_DIR"
  CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
    go build -ldflags "-X main.version=$VERSION" -o "$BIN_PATH" ./cmd/symphony
)
chmod 755 "$BIN_PATH"

sed "s/__VERSION__/$VERSION/g" "$PACKAGE_TEMPLATE" >"$STAGE_DIR/package.json"
cp "$ROOT_DIR/README.md" "$STAGE_DIR/README.md"

echo "Packing npm package"
(
  cd "$STAGE_DIR"
  npm pack --pack-destination "$PACK_DIR"
)

echo "Staged package: $STAGE_DIR"
echo "Packed tarball:"
find "$PACK_DIR" -maxdepth 1 -name '*.tgz' -print
