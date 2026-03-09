#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=/dev/null
. "$SCRIPT_DIR/shared.sh"

ROOT=$(repo_root)
cd "$ROOT"

run_step make test-cover
run_step make test-race
run_step npm --prefix frontend run lint
run_step npm --prefix frontend run test:ci
