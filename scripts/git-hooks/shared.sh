#!/bin/sh
set -eu

repo_root() {
  git rev-parse --show-toplevel
}

ensure_go_cache_env() {
  ROOT=$(repo_root)

  if [ -z "${GOCACHE:-}" ]; then
    GOCACHE="$ROOT/.maestro/tmp/go-build"
    export GOCACHE
  fi
  if [ -z "${GOMODCACHE:-}" ]; then
    GOMODCACHE="$ROOT/.maestro/tmp/go-mod"
    export GOMODCACHE
  fi

  mkdir -p "$GOCACHE" "$GOMODCACHE"
}

ensure_node_cache_env() {
  ROOT=$(repo_root)

  if [ -z "${npm_config_cache:-}" ]; then
    npm_config_cache="$ROOT/.maestro/tmp/npm-cache"
    export npm_config_cache
  fi
  if [ -z "${NPM_CONFIG_CACHE:-}" ]; then
    NPM_CONFIG_CACHE="$npm_config_cache"
    export NPM_CONFIG_CACHE
  fi

  mkdir -p "$npm_config_cache"
}

log_step() {
  printf '%s\n' "git-hook: $*"
}

run_step() {
  log_step "$*"
  ensure_go_cache_env
  ensure_node_cache_env
  "$@"
}
