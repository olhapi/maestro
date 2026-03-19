#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=./lib/npm_safe_env.sh
. "$ROOT_DIR/scripts/lib/npm_safe_env.sh"
ROOT_PACKAGE_TEMPLATE="$ROOT_DIR/packaging/npm/root.package.json.tmpl"
ROOT_PACKAGE_SOURCE_DIR="$ROOT_DIR/packaging/npm/root"
FRONTEND_APP_DIR="$ROOT_DIR/apps/frontend"
FRONTEND_DIST_DIR="$ROOT_DIR/internal/dashboardui/dist"
OUT_ROOT="${OUT_ROOT:-$ROOT_DIR/dist}"
STAGE_DIR="${STAGE_DIR:-$OUT_ROOT/npm-package}"
PACK_DIR="${PACK_DIR:-$OUT_ROOT/npm}"
ROOT_PACKAGE_NAME="@olhapi/maestro"

usage() {
  cat <<'EOF'
Usage:
  scripts/package_npm_release.sh <version>
  scripts/package_npm_release.sh print-current-target
  scripts/package_npm_release.sh pack-current <version>
  scripts/package_npm_release.sh pack-root <version>
  scripts/package_npm_release.sh pack-leaf <version> <target>

Builds and packs Maestro npm artifacts into dist/npm-package and dist/npm.
The default form packs the root package plus the current host leaf package.

Examples:
  scripts/package_npm_release.sh print-current-target
  scripts/package_npm_release.sh v1.2.3
  scripts/package_npm_release.sh pack-root v1.2.3
  scripts/package_npm_release.sh pack-leaf v1.2.3 linux-x64-gnu
EOF
}

escape_sed_replacement() {
  printf '%s' "$1" | sed -e 's/[&|]/\\&/g'
}

tarball_filename() {
  local package_name="$1"
  local version="$2"
  printf '%s-%s.tgz\n' "${package_name#@}" "$version" | tr '/' '-'
}

host_has_glibc() {
  if command -v getconf >/dev/null 2>&1 && getconf GNU_LIBC_VERSION >/dev/null 2>&1; then
    return 0
  fi
  if command -v ldd >/dev/null 2>&1 && ldd --version 2>&1 | grep -qi "glibc\|gnu libc"; then
    return 0
  fi
  return 1
}

detect_host_target() {
  local uname_s
  local uname_m
  uname_s="$(uname -s)"
  uname_m="$(uname -m)"

  case "$uname_s" in
    Darwin)
      case "$uname_m" in
        arm64) echo "darwin-arm64" ;;
        x86_64) echo "darwin-x64" ;;
        *)
          echo "unsupported Darwin architecture: $uname_m" >&2
          exit 1
          ;;
      esac
      ;;
    Linux)
      if ! host_has_glibc; then
        echo "pack-current supports Linux glibc hosts only; build a specific leaf target on a native glibc runner instead" >&2
        exit 1
      fi
      case "$uname_m" in
        x86_64|amd64) echo "linux-x64-gnu" ;;
        arm64|aarch64) echo "linux-arm64-gnu" ;;
        *)
          echo "unsupported Linux architecture: $uname_m" >&2
          exit 1
          ;;
      esac
      ;;
    MINGW*|MSYS*|CYGWIN*)
      case "$uname_m" in
        x86_64|amd64) echo "win32-x64" ;;
        *)
          echo "unsupported Windows architecture: $uname_m" >&2
          exit 1
          ;;
      esac
      ;;
    *)
      echo "unsupported host platform: $uname_s/$uname_m" >&2
      exit 1
      ;;
  esac
}

configure_target() {
  local target="$1"
  case "$target" in
    darwin-arm64)
      GOOS="darwin"
      GOARCH="arm64"
      PACKAGE_NAME="@olhapi/maestro-darwin-arm64"
      PACKAGE_LABEL="macOS arm64"
      PACKAGE_OS="darwin"
      PACKAGE_CPU="arm64"
      PACKAGE_LIBC=""
      BINARY_NAME="maestro"
      ;;
    darwin-x64)
      GOOS="darwin"
      GOARCH="amd64"
      PACKAGE_NAME="@olhapi/maestro-darwin-x64"
      PACKAGE_LABEL="macOS x64"
      PACKAGE_OS="darwin"
      PACKAGE_CPU="x64"
      PACKAGE_LIBC=""
      BINARY_NAME="maestro"
      ;;
    linux-x64-gnu)
      GOOS="linux"
      GOARCH="amd64"
      PACKAGE_NAME="@olhapi/maestro-linux-x64-gnu"
      PACKAGE_LABEL="Linux x64 (glibc)"
      PACKAGE_OS="linux"
      PACKAGE_CPU="x64"
      PACKAGE_LIBC="glibc"
      BINARY_NAME="maestro"
      ;;
    linux-arm64-gnu)
      GOOS="linux"
      GOARCH="arm64"
      PACKAGE_NAME="@olhapi/maestro-linux-arm64-gnu"
      PACKAGE_LABEL="Linux arm64 (glibc)"
      PACKAGE_OS="linux"
      PACKAGE_CPU="arm64"
      PACKAGE_LIBC="glibc"
      BINARY_NAME="maestro"
      ;;
    win32-x64)
      GOOS="windows"
      GOARCH="amd64"
      PACKAGE_NAME="@olhapi/maestro-win32-x64"
      PACKAGE_LABEL="Windows x64"
      PACKAGE_OS="win32"
      PACKAGE_CPU="x64"
      PACKAGE_LIBC=""
      BINARY_NAME="maestro.exe"
      ;;
    *)
      echo "unsupported target: $target" >&2
      exit 1
      ;;
  esac
}

pack_package_dir() {
  local package_dir="$1"
  local package_name="$2"
  local version="$3"
  local expected_tarball="$PACK_DIR/$(tarball_filename "$package_name" "$version")"

  rm -f "$expected_tarball"
  local pack_output
  pack_output="$(
    cd "$package_dir"
    run_clean_npm pack --pack-destination "$PACK_DIR" --json
  )"
  node -e 'const data = JSON.parse(process.argv[1]); process.stdout.write(data[0].filename);' "$pack_output"
}

run_pnpm() {
  if command -v pnpm >/dev/null 2>&1; then
    pnpm "$@"
    return
  fi
  if command -v corepack >/dev/null 2>&1; then
    corepack pnpm "$@"
    return
  fi

  echo "missing required command: pnpm (or corepack)" >&2
  exit 1
}

ensure_frontend_dependencies() {
  if [[ -d "$ROOT_DIR/node_modules/.pnpm" && -d "$FRONTEND_APP_DIR/node_modules" ]]; then
    return
  fi

  echo "Installing frontend workspace dependencies"
  (
    cd "$ROOT_DIR"
    run_pnpm install --frozen-lockfile
  )
}

ensure_dashboard_dist() {
  if ! command -v node >/dev/null 2>&1; then
    echo "missing required command: node" >&2
    exit 1
  fi

  ensure_frontend_dependencies

  echo "Building embedded dashboard bundle"
  (
    cd "$FRONTEND_APP_DIR"
    run_pnpm build
  )

  if [[ ! -f "$FRONTEND_DIST_DIR/index.html" || ! -f "$FRONTEND_DIST_DIR/assets/index.js" ]]; then
    echo "frontend build did not produce expected dashboard assets in $FRONTEND_DIST_DIR" >&2
    exit 1
  fi
}

render_root_package_json() {
  local version="$1"
  sed "s/__VERSION__/$(escape_sed_replacement "$version")/g" "$ROOT_PACKAGE_TEMPLATE"
}

render_leaf_package_json() {
  local version="$1"
  cat <<EOF
{
  "name": "$PACKAGE_NAME",
  "version": "$version",
  "description": "Maestro CLI binary for $PACKAGE_LABEL",
  "license": "MIT",
  "files": [
    "lib/$BINARY_NAME",
    "LICENSE",
    "README.md"
  ],
  "os": [
    "$PACKAGE_OS"
  ],
  "cpu": [
    "$PACKAGE_CPU"
  ],$(if [[ -n "$PACKAGE_LIBC" ]]; then cat <<LIBC
  "libc": [
    "$PACKAGE_LIBC"
  ],
LIBC
fi)
  "repository": {
    "type": "git",
    "url": "git+https://github.com/olhapi/maestro.git"
  },
  "homepage": "https://github.com/olhapi/maestro",
  "bugs": {
    "url": "https://github.com/olhapi/maestro/issues"
  },
  "keywords": [
    "maestro",
    "cli",
    "go",
    "mcp",
    "orchestration"
  ],
  "publishConfig": {
    "access": "public"
  }
}
EOF
}

pack_root_package() {
  local version="$1"
  local stage_dir="$STAGE_DIR/root"

  if ! command -v npm >/dev/null 2>&1; then
    echo "missing required command: npm" >&2
    exit 1
  fi
  if [[ ! -f "$ROOT_PACKAGE_TEMPLATE" ]]; then
    echo "missing root package template: $ROOT_PACKAGE_TEMPLATE" >&2
    exit 1
  fi

  rm -rf "$stage_dir"
  mkdir -p "$stage_dir/bin" "$stage_dir/lib" "$PACK_DIR"
  cp "$ROOT_DIR/LICENSE" "$stage_dir/LICENSE"
  cp "$ROOT_DIR/README.md" "$stage_dir/README.md"
  cp "$ROOT_PACKAGE_SOURCE_DIR/bin/maestro.js" "$stage_dir/bin/maestro.js"
  cp "$ROOT_PACKAGE_SOURCE_DIR/lib/get-exe-path.js" "$stage_dir/lib/get-exe-path.js"
  render_root_package_json "$version" >"$stage_dir/package.json"

  local filename
  filename="$(pack_package_dir "$stage_dir" "$ROOT_PACKAGE_NAME" "$version")"
  echo "Packed root package: $PACK_DIR/$filename"
}

pack_leaf_package() {
  local version="$1"
  local target="$2"
  local stage_dir
  local bin_path

  if ! command -v go >/dev/null 2>&1; then
    echo "missing required command: go" >&2
    exit 1
  fi
  if ! command -v npm >/dev/null 2>&1; then
    echo "missing required command: npm" >&2
    exit 1
  fi

  configure_target "$target"
  stage_dir="$STAGE_DIR/${PACKAGE_NAME#@}"
  bin_path="$stage_dir/lib/$BINARY_NAME"

  rm -rf "$stage_dir"
  mkdir -p "$stage_dir/lib" "$PACK_DIR"

  ensure_dashboard_dist

  echo "Building maestro $version for $GOOS/$GOARCH"
  (
    cd "$ROOT_DIR"
    CGO_ENABLED=1 GOOS="$GOOS" GOARCH="$GOARCH" \
      go build -ldflags "-X main.version=$version" -o "$bin_path" ./cmd/maestro
  )
  chmod 755 "$bin_path"

  render_leaf_package_json "$version" >"$stage_dir/package.json"
  cp "$ROOT_DIR/LICENSE" "$stage_dir/LICENSE"
  cp "$ROOT_DIR/README.md" "$stage_dir/README.md"

  local filename
  filename="$(pack_package_dir "$stage_dir" "$PACKAGE_NAME" "$version")"
  echo "Packed leaf package: $PACK_DIR/$filename"
}

if [[ $# -eq 1 && "$1" == "print-current-target" ]]; then
  detect_host_target
  exit 0
fi

if [[ $# -lt 1 || $# -gt 3 ]]; then
  usage >&2
  exit 1
fi

COMMAND="$1"
if [[ $# -eq 1 ]]; then
  set -- "pack-current" "$1"
  COMMAND="pack-current"
elif [[ "$COMMAND" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  set -- "pack-current" "$@"
  COMMAND="pack-current"
fi

case "$COMMAND" in
  pack-current)
    if [[ $# -ne 2 ]]; then
      usage >&2
      exit 1
    fi
    RAW_VERSION="$2"
    ;;
  pack-root)
    if [[ $# -ne 2 ]]; then
      usage >&2
      exit 1
    fi
    RAW_VERSION="$2"
    ;;
  pack-leaf)
    if [[ $# -ne 3 ]]; then
      usage >&2
      exit 1
    fi
    RAW_VERSION="$2"
    TARGET="$3"
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac

if [[ ! "$RAW_VERSION" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "invalid version: $RAW_VERSION" >&2
  exit 1
fi
VERSION="${RAW_VERSION#v}"

case "$COMMAND" in
  pack-current)
    TARGET="$(detect_host_target)"
    pack_leaf_package "$VERSION" "$TARGET"
    pack_root_package "$VERSION"
    ;;
  pack-root)
    pack_root_package "$VERSION"
    ;;
  pack-leaf)
    pack_leaf_package "$VERSION" "$TARGET"
    ;;
esac
