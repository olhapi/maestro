#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT_UNDER_TEST="$ROOT_DIR/scripts/ensure_dashboard_dist.sh"

fail() {
  printf 'test: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  local file="$1"
  local pattern="$2"
  if ! grep -Fq -- "$pattern" "$file"; then
    fail "expected to find '$pattern' in $file"
  fi
}

assert_not_contains() {
  local file="$1"
  local pattern="$2"
  if grep -Fq -- "$pattern" "$file"; then
    fail "did not expect to find '$pattern' in $file"
  fi
}

make_mock_toolchain() {
  local bin_dir="$1"
  mkdir -p "$bin_dir"

  cat >"$bin_dir/node" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
exit 0
EOF

  cat >"$bin_dir/pnpm" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'pnpm %s\n' "$*" >>"$LOG_FILE"
case "$1" in
  install)
    mkdir -p "$MOCK_ROOT_DIR/node_modules/.pnpm" "$MOCK_FRONTEND_APP_DIR/node_modules"
    ;;
  build)
    mkdir -p "$MOCK_FRONTEND_DIST_DIR/assets"
    printf '<!doctype html>\n' >"$MOCK_FRONTEND_DIST_DIR/index.html"
    printf 'console.log("ok")\n' >"$MOCK_FRONTEND_DIST_DIR/assets/index.js"
    ;;
  *)
    ;;
esac
EOF

  chmod +x "$bin_dir/node" "$bin_dir/pnpm"
}

test_builds_missing_dist_when_toolchain_is_available() {
  local tmp_dir repo_dir bin_dir log_file
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/ensure-dashboard-dist.XXXXXX")"
  repo_dir="$tmp_dir/repo"
  bin_dir="$tmp_dir/bin"
  log_file="$tmp_dir/log.txt"

  mkdir -p "$repo_dir/apps/frontend" "$repo_dir/internal/dashboardui"
  : >"$log_file"
  make_mock_toolchain "$bin_dir"

  PATH="$bin_dir:$PATH" \
  LOG_FILE="$log_file" \
  MOCK_ROOT_DIR="$repo_dir" \
  MOCK_FRONTEND_APP_DIR="$repo_dir/apps/frontend" \
  MOCK_FRONTEND_DIST_DIR="$repo_dir/internal/dashboardui/dist" \
  MAESTRO_ROOT_DIR="$repo_dir" \
  MAESTRO_FRONTEND_APP_DIR="$repo_dir/apps/frontend" \
  MAESTRO_FRONTEND_DIST_DIR="$repo_dir/internal/dashboardui/dist" \
  bash "$SCRIPT_UNDER_TEST" >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"

  [[ -f "$repo_dir/internal/dashboardui/dist/index.html" ]] || fail "expected index.html to be created"
  [[ -f "$repo_dir/internal/dashboardui/dist/assets/index.js" ]] || fail "expected assets/index.js to be created"
  assert_contains "$log_file" "pnpm install --frozen-lockfile"
  assert_contains "$log_file" "pnpm build"
  assert_contains "$tmp_dir/stdout.txt" "Installing frontend workspace dependencies"
  assert_contains "$tmp_dir/stdout.txt" "Building embedded dashboard bundle"
}

test_skips_toolchain_when_dist_already_exists() {
  local tmp_dir repo_dir log_file
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/ensure-dashboard-dist-ready.XXXXXX")"
  repo_dir="$tmp_dir/repo"
  log_file="$tmp_dir/log.txt"

  mkdir -p "$repo_dir/internal/dashboardui/dist/assets"
  printf '<!doctype html>\n' >"$repo_dir/internal/dashboardui/dist/index.html"
  printf 'console.log("ok")\n' >"$repo_dir/internal/dashboardui/dist/assets/index.js"
  : >"$log_file"

  LOG_FILE="$log_file" \
  MAESTRO_ROOT_DIR="$repo_dir" \
  MAESTRO_FRONTEND_APP_DIR="$repo_dir/apps/frontend" \
  MAESTRO_FRONTEND_DIST_DIR="$repo_dir/internal/dashboardui/dist" \
  bash "$SCRIPT_UNDER_TEST" >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"

  [[ ! -s "$tmp_dir/stdout.txt" ]] || fail "expected no output when dist already exists"
  [[ ! -s "$tmp_dir/stderr.txt" ]] || fail "expected no stderr when dist already exists"
  assert_not_contains "$log_file" "pnpm "
}

test_fails_when_node_is_missing() {
  local tmp_dir repo_dir bin_dir
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/ensure-dashboard-dist-no-node.XXXXXX")"
  repo_dir="$tmp_dir/repo"
  bin_dir="$tmp_dir/bin"

  mkdir -p "$repo_dir/apps/frontend" "$repo_dir/internal/dashboardui"
  mkdir -p "$bin_dir"

  if PATH="$bin_dir:/usr/bin:/bin" \
    MAESTRO_ROOT_DIR="$repo_dir" \
    MAESTRO_FRONTEND_APP_DIR="$repo_dir/apps/frontend" \
    MAESTRO_FRONTEND_DIST_DIR="$repo_dir/internal/dashboardui/dist" \
    bash "$SCRIPT_UNDER_TEST" >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"; then
    fail "expected helper to fail when node is missing"
  fi

  assert_contains "$tmp_dir/stderr.txt" "missing required command: node"
}

test_fails_when_package_manager_is_missing() {
  local tmp_dir repo_dir bin_dir
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/ensure-dashboard-dist-no-pnpm.XXXXXX")"
  repo_dir="$tmp_dir/repo"
  bin_dir="$tmp_dir/bin"

  mkdir -p "$repo_dir/apps/frontend" "$repo_dir/internal/dashboardui" "$bin_dir"
  cat >"$bin_dir/node" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
exit 0
EOF
  chmod +x "$bin_dir/node"

  if PATH="$bin_dir:/usr/bin:/bin" \
    MAESTRO_ROOT_DIR="$repo_dir" \
    MAESTRO_FRONTEND_APP_DIR="$repo_dir/apps/frontend" \
    MAESTRO_FRONTEND_DIST_DIR="$repo_dir/internal/dashboardui/dist" \
    bash "$SCRIPT_UNDER_TEST" >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"; then
    fail "expected helper to fail when pnpm and corepack are missing"
  fi

  assert_contains "$tmp_dir/stdout.txt" "Installing frontend workspace dependencies"
  assert_contains "$tmp_dir/stderr.txt" "missing required command: pnpm (or corepack)"
}

main() {
  test_builds_missing_dist_when_toolchain_is_available
  test_skips_toolchain_when_dist_already_exists
  test_fails_when_node_is_missing
  test_fails_when_package_manager_is_missing
}

main "$@"
