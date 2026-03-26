#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${MAESTRO_ROOT_DIR:-$(cd "$SCRIPT_DIR/.." && pwd)}"
FRONTEND_APP_DIR="${MAESTRO_FRONTEND_APP_DIR:-$ROOT_DIR/apps/frontend}"
FRONTEND_DIST_DIR="${MAESTRO_FRONTEND_DIST_DIR:-$ROOT_DIR/internal/dashboardui/dist}"

dashboard_dist_ready() {
  [[ -f "$FRONTEND_DIST_DIR/index.html" && -f "$FRONTEND_DIST_DIR/assets/index.js" ]]
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
  if dashboard_dist_ready; then
    exit 0
  fi

  if ! command -v node >/dev/null 2>&1; then
    echo "missing required command: node" >&2
    exit 1
  fi
  if [[ ! -d "$FRONTEND_APP_DIR" ]]; then
    echo "missing frontend app directory: $FRONTEND_APP_DIR" >&2
    exit 1
  fi

  ensure_node_cache_env
  ensure_frontend_dependencies

  echo "Building embedded dashboard bundle"
  (
    cd "$FRONTEND_APP_DIR"
    run_pnpm build
  )

  if ! dashboard_dist_ready; then
    echo "frontend build did not produce expected dashboard assets in $FRONTEND_DIST_DIR" >&2
    exit 1
  fi
}

main "$@"
