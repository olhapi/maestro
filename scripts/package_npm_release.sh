#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=./lib/npm_safe_env.sh
. "$ROOT_DIR/scripts/lib/npm_safe_env.sh"
ROOT_PACKAGE_TEMPLATE="$ROOT_DIR/packaging/npm/root.package.json.tmpl"
ROOT_PACKAGE_SOURCE_DIR="$ROOT_DIR/packaging/npm/root"
ENSURE_DASHBOARD_DIST_BIN="${MAESTRO_ENSURE_DASHBOARD_DIST_BIN:-$ROOT_DIR/scripts/ensure_dashboard_dist.sh}"
OUT_ROOT="${OUT_ROOT:-$ROOT_DIR/dist}"
STAGE_DIR="${STAGE_DIR:-$OUT_ROOT/npm-package}"
PACK_DIR="${PACK_DIR:-$OUT_ROOT/npm}"
ROOT_PACKAGE_NAME="@olhapi/maestro"
SKILLS_SOURCE_DIR="$ROOT_DIR/skills/maestro"

usage() {
  cat <<'EOF'
Usage:
  scripts/package_npm_release.sh <version>
  scripts/package_npm_release.sh pack-root <version>

Builds and packs the Maestro launcher npm artifact into dist/npm-package and dist/npm.

Examples:
  scripts/package_npm_release.sh v1.2.3
  scripts/package_npm_release.sh pack-root v1.2.3
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

render_root_package_json() {
  local version="$1"
  sed "s/__VERSION__/$(escape_sed_replacement "$version")/g" "$ROOT_PACKAGE_TEMPLATE"
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
  mkdir -p "$stage_dir/bin" "$stage_dir/lib" "$stage_dir/share/skills" "$PACK_DIR"
  cp "$ROOT_DIR/LICENSE" "$stage_dir/LICENSE"
  cp "$ROOT_DIR/README.md" "$stage_dir/README.md"
  cp -R "$SKILLS_SOURCE_DIR" "$stage_dir/share/skills/maestro"
  cp "$ROOT_PACKAGE_SOURCE_DIR/bin/maestro" "$stage_dir/bin/maestro"
  cp "$ROOT_PACKAGE_SOURCE_DIR/bin/maestro.cmd" "$stage_dir/bin/maestro.cmd"
  cp "$ROOT_PACKAGE_SOURCE_DIR/bin/maestro.ps1" "$stage_dir/bin/maestro.ps1"
  cp "$ROOT_PACKAGE_SOURCE_DIR/bin/maestro.js" "$stage_dir/bin/maestro.js"
  cp "$ROOT_PACKAGE_SOURCE_DIR/lib/browser.js" "$stage_dir/lib/browser.js"
  cp "$ROOT_PACKAGE_SOURCE_DIR/lib/cli.js" "$stage_dir/lib/cli.js"
  cp "$ROOT_PACKAGE_SOURCE_DIR/lib/docker-plan.js" "$stage_dir/lib/docker-plan.js"
  cp "$ROOT_PACKAGE_SOURCE_DIR/lib/install-skills.js" "$stage_dir/lib/install-skills.js"
  cp "$ROOT_PACKAGE_SOURCE_DIR/lib/runtime-state.js" "$stage_dir/lib/runtime-state.js"
  chmod 755 "$stage_dir/bin/maestro" "$stage_dir/bin/maestro.cmd" "$stage_dir/bin/maestro.ps1" "$stage_dir/bin/maestro.js"
  render_root_package_json "$version" >"$stage_dir/package.json"

  local filename
  filename="$(pack_package_dir "$stage_dir" "$ROOT_PACKAGE_NAME" "$version")"
  echo "Packed root package: $PACK_DIR/$filename"
}

if [[ $# -lt 1 || $# -gt 3 ]]; then
  usage >&2
  exit 1
fi

COMMAND="$1"
if [[ $# -eq 1 ]]; then
  set -- "pack-root" "$1"
  COMMAND="pack-root"
elif [[ "$COMMAND" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  set -- "pack-root" "$@"
  COMMAND="pack-root"
fi

case "$COMMAND" in
  pack-root)
    if [[ $# -ne 2 ]]; then
      usage >&2
      exit 1
    fi
    RAW_VERSION="$2"
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
  pack-root)
    "$ENSURE_DASHBOARD_DIST_BIN"
    pack_root_package "$VERSION"
    ;;
esac
