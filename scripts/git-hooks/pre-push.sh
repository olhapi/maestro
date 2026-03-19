#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=/dev/null
. "$SCRIPT_DIR/shared.sh"

ROOT=$(repo_root)
cd "$ROOT"

# Keep the local pre-push gate host-complete. CI still owns the cross-platform
# package matrix and registry-install smoke.
run_step pnpm verify:pre-push
