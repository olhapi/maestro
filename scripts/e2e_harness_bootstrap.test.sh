#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

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

assert_in_order() {
  local file="$1"
  shift
  local previous_line=0
  local pattern
  for pattern in "$@"; do
    local line
    line="$(grep -nF -- "$pattern" "$file" | head -n 1 | cut -d: -f1 || true)"
    if [[ -z "$line" ]]; then
      fail "missing ordered pattern '$pattern' in $file"
    fi
    if (( line <= previous_line )); then
      fail "pattern '$pattern' appeared out of order in $file"
    fi
    previous_line="$line"
  done
}

make_mock_toolchain() {
  local bin_dir="$1"
  mkdir -p "$bin_dir"

  cat >"$bin_dir/codex" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'codex %s\n' "$*" >>"$LOG_FILE"
exit 0
EOF

  cat >"$bin_dir/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'git %s\n' "$*" >>"$LOG_FILE"
exit 0
EOF

  cat >"$bin_dir/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'go %s\n' "$*" >>"$LOG_FILE"
case "$1" in
  build)
    if [[ -f "$MOCK_ROOT_DIR/internal/dashboardui/dist/.e2e-bootstrap-sentinel" ]]; then
      printf 'dashboard bootstrap sentinel present before go build\n' >>"$LOG_FILE"
      exit 17
    fi
    printf 'dashboard bootstrap sentinel missing before go build\n' >>"$LOG_FILE"
    exit 99
    ;;
  *)
    exit 0
    ;;
esac
EOF

  cat >"$bin_dir/python3" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'python3 %s\n' "$*" >>"$LOG_FILE"
exit 0
EOF

  cat >"$bin_dir/sqlite3" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'sqlite3 %s\n' "$*" >>"$LOG_FILE"
exit 0
EOF

  cat >"$bin_dir/ensure-dashboard-dist" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'ensure-dashboard\n' >>"$LOG_FILE"
mkdir -p "$MOCK_ROOT_DIR/internal/dashboardui/dist"
: >"$MOCK_ROOT_DIR/internal/dashboardui/dist/.e2e-bootstrap-sentinel"
EOF

  chmod +x \
    "$bin_dir/codex" \
    "$bin_dir/git" \
    "$bin_dir/go" \
    "$bin_dir/python3" \
    "$bin_dir/sqlite3" \
    "$bin_dir/ensure-dashboard-dist"
}

cleanup_sentinel() {
  rm -f "$ROOT_DIR/internal/dashboardui/dist/.e2e-bootstrap-sentinel"
}

test_scripts_bootstrap_dashboard_dist_before_build() {
  local scripts=(
    "$ROOT_DIR/scripts/e2e_retry_safety.sh"
    "$ROOT_DIR/scripts/e2e_real_codex.sh"
    "$ROOT_DIR/scripts/e2e_real_codex_phases.sh"
    "$ROOT_DIR/scripts/e2e_real_codex_issue_images.sh"
  )

  local script
  for script in "${scripts[@]}"; do
    local tmp_dir bin_dir log_file stdout_file stderr_file
    tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-harness-bootstrap.XXXXXX")"
    bin_dir="$tmp_dir/bin"
    log_file="$tmp_dir/log.txt"
    stdout_file="$tmp_dir/stdout.txt"
    stderr_file="$tmp_dir/stderr.txt"

    make_mock_toolchain "$bin_dir"
    : >"$log_file"
    cleanup_sentinel

    if PATH="$bin_dir:$PATH" \
      LOG_FILE="$log_file" \
      MOCK_ROOT_DIR="$ROOT_DIR" \
      MAESTRO_ENSURE_DASHBOARD_DIST_BIN="$bin_dir/ensure-dashboard-dist" \
      E2E_ROOT="$tmp_dir/harness" \
      bash "$script" >"$stdout_file" 2>"$stderr_file"; then
      cleanup_sentinel
      fail "expected $(basename "$script") to stop after the mocked go build"
    fi

    assert_in_order "$log_file" "ensure-dashboard" "dashboard bootstrap sentinel present before go build"
    assert_contains "$log_file" "go build -o "
    cleanup_sentinel
  done
}

main() {
  test_scripts_bootstrap_dashboard_dist_before_build
}

main "$@"
