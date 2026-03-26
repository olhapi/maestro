#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${MAESTRO_ROOT_DIR:-$(cd "$SCRIPT_DIR/.." && pwd)}"
FRONTEND_APP_DIR="${MAESTRO_FRONTEND_APP_DIR:-$ROOT_DIR/apps/frontend}"
FRONTEND_DIST_DIR="${MAESTRO_FRONTEND_DIST_DIR:-$ROOT_DIR/internal/dashboardui/dist}"
FRONTEND_DIST_STAMP="${MAESTRO_FRONTEND_DIST_STAMP:-$FRONTEND_DIST_DIR/.maestro-dist-stamp}"

FRESHNESS_PATHS=(
  "$ROOT_DIR/.npmrc"
  "$ROOT_DIR/package.json"
  "$ROOT_DIR/pnpm-lock.yaml"
  "$ROOT_DIR/pnpm-workspace.yaml"
  "$ROOT_DIR/turbo.json"
)

dashboard_dist_ready() {
  [[ -f "$FRONTEND_DIST_DIR/index.html" && -f "$FRONTEND_DIST_DIR/assets/index.js" ]]
}

dashboard_dist_fresh() {
  local freshness_path newer_file

  dashboard_dist_ready || return 1
  [[ -f "$FRONTEND_DIST_STAMP" ]] || return 1

  newer_file="$(
    find "$FRONTEND_APP_DIR" \
      -path "$FRONTEND_APP_DIR/node_modules" -prune -o \
      -type f -newer "$FRONTEND_DIST_STAMP" -print -quit
  )"
  if [[ -n "$newer_file" ]]; then
    return 1
  fi

  for freshness_path in "${FRESHNESS_PATHS[@]}"; do
    if [[ -e "$freshness_path" && "$freshness_path" -nt "$FRONTEND_DIST_STAMP" ]]; then
      return 1
    fi
  done

  return 0
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

ensure_node_cache_env() {
  if [[ -z "${npm_config_cache:-}" ]]; then
    npm_config_cache="$ROOT_DIR/.maestro/tmp/npm-cache"
    export npm_config_cache
  fi
  if [[ -z "${NPM_CONFIG_CACHE:-}" ]]; then
    NPM_CONFIG_CACHE="$npm_config_cache"
    export NPM_CONFIG_CACHE
  fi

  mkdir -p "$npm_config_cache"
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

main() {
  if [[ ! -d "$FRONTEND_APP_DIR" ]]; then
    echo "missing frontend app directory: $FRONTEND_APP_DIR" >&2
    exit 1
  fi

  if dashboard_dist_fresh; then
    exit 0
  fi

  if ! command -v node >/dev/null 2>&1; then
    echo "missing required command: node" >&2
    exit 1
  fi

  ensure_node_cache_env
  ensure_frontend_dependencies

  echo "Building embedded dashboard bundle"
  (
    cd "$FRONTEND_APP_DIR"
    run_pnpm build
  )

  mkdir -p "$FRONTEND_DIST_DIR"
  touch "$FRONTEND_DIST_STAMP"

  if ! dashboard_dist_fresh; then
    echo "frontend build did not produce expected dashboard assets in $FRONTEND_DIST_DIR" >&2
    exit 1
  fi
}

main "$@"
