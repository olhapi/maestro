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

log_step() {
  printf '%s\n' "git-hook: $*"
}

run_step() {
  log_step "$*"
  ensure_go_cache_env
  "$@"
}
