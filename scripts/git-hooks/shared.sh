#!/bin/sh
set -eu

repo_root() {
  git rev-parse --show-toplevel
}

log_step() {
  printf '%s\n' "git-hook: $*"
}

run_step() {
  log_step "$*"
  "$@"
}
