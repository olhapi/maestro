#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=/dev/null
. "$SCRIPT_DIR/shared.sh"

ROOT=$(repo_root)
cd "$ROOT"

rosetta_x64_smoke_ready() {
  if ! command -v arch >/dev/null 2>&1; then
    return 1
  fi

  if ! command -v bash >/dev/null 2>&1; then
    return 1
  fi

  arch -x86_64 bash -c '
    [ "$(node -p "process.arch" 2>/dev/null)" = "x64" ] &&
    npm --version >/dev/null 2>&1 &&
    go version >/dev/null 2>&1 &&
    (pnpm --version >/dev/null 2>&1 || corepack pnpm --version >/dev/null 2>&1)
  '
}

# Keep the local pre-push gate host-complete.
# On Apple Silicon macOS, add an x64 Rosetta pass so the disabled CI macOS
# smoke still runs locally when the toolchain can support it.
run_step pnpm verify:pre-push

case "$(uname -s)-$(uname -m)" in
  Darwin-arm64)
    if rosetta_x64_smoke_ready; then
      run_step arch -x86_64 ./scripts/git-hooks/host-package-smoke.sh
    else
      log_step "skipping extra macOS x64 smoke; x64 toolchain prerequisites unavailable"
    fi
    ;;
esac
