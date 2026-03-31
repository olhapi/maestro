#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=/dev/null
. "$SCRIPT_DIR/shared.sh"

ROOT=$(repo_root)
cd "$ROOT"

TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/maestro-pre-push-package.XXXXXX")
cleanup() {
  rm -rf "$TMP_ROOT"
}
trap cleanup EXIT INT TERM

export OUT_ROOT="$TMP_ROOT"
export STAGE_DIR="$OUT_ROOT/npm-package"
export PACK_DIR="$OUT_ROOT/npm"

HOST_TARGET="$(./scripts/package_npm_release.sh print-current-target)"

run_step ./scripts/package_npm_release.sh 0.0.0-pre-push
run_step ./scripts/smoke_npm_package.sh 0.0.0-pre-push "$HOST_TARGET"
