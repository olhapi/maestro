#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT_UNDER_TEST="$ROOT_DIR/scripts/check_coverage.sh"

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

assert_count() {
  local file="$1"
  local pattern="$2"
  local expected="$3"
  local actual
  actual="$(grep -Fc -- "$pattern" "$file" || true)"
  if [[ "$actual" != "$expected" ]]; then
    fail "expected '$pattern' to appear $expected times in $file, got $actual"
  fi
}

make_mock_toolchain() {
  local bin_dir="$1"
  mkdir -p "$bin_dir"

  cat >"$bin_dir/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'go %s\n' "$*" >>"$LOG_FILE"
if [[ "${1:-}" == "test" ]]; then
  profile=""
  pkg=""
  while (($#)); do
    case "$1" in
      -coverprofile=*)
        profile="${1#-coverprofile=}"
        shift
        ;;
      ./*)
        pkg="$1"
        shift
        ;;
      *)
        shift
        ;;
    esac
  done
  if [[ -z "$profile" || -z "$pkg" ]]; then
    exit 41
  fi
  printf 'mode: set\n%s/file.go:1.1,1.2 1 1\n' "$pkg" >"$profile"
  exit 0
fi
if [[ "${1:-}" == "tool" && "${2:-}" == "cover" ]]; then
  printf 'total: (statements) 95.0%%\n'
  exit 0
fi
exit 42
EOF

  chmod +x "$bin_dir/go"
}

test_runs_under_system_bash_without_associative_arrays() {
  local tmp_dir repo_dir bin_dir log_file
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/check-coverage.XXXXXX")"
  repo_dir="$tmp_dir/repo"
  bin_dir="$tmp_dir/bin"
  log_file="$tmp_dir/log.txt"

  mkdir -p \
    "$repo_dir/scripts" \
    "$repo_dir/cmd/app" \
    "$repo_dir/internal/keep" \
    "$repo_dir/internal/testutil/skip" \
    "$repo_dir/internal/agentruntime/fake" \
    "$repo_dir/pkg/lib" \
    "$repo_dir/skills"
  printf 'package main\n' >"$repo_dir/cmd/app/main.go"
  printf 'package main\n' >"$repo_dir/cmd/app/extra.go"
  printf 'package keep\n' >"$repo_dir/internal/keep/worker.go"
  printf 'package skip\n' >"$repo_dir/internal/testutil/skip/ignored.go"
  printf 'package fake\n' >"$repo_dir/internal/agentruntime/fake/ignored.go"
  printf 'package lib\n' >"$repo_dir/pkg/lib/lib.go"
  cat >"$repo_dir/scripts/ensure_dashboard_dist.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'ensure-dashboard\n' >>"$LOG_FILE"
EOF
  chmod +x "$repo_dir/scripts/ensure_dashboard_dist.sh"

  : >"$log_file"
  make_mock_toolchain "$bin_dir"

  PATH="$bin_dir:/usr/bin:/bin" \
  LOG_FILE="$log_file" \
  MAESTRO_ROOT_DIR="$repo_dir" \
  /bin/bash "$SCRIPT_UNDER_TEST" >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"

  assert_contains "$log_file" "ensure-dashboard"
  assert_contains "$tmp_dir/stdout.txt" "Checking coverage for 3 packages at 90% threshold"
  assert_count "$log_file" "go test -coverprofile=" "3"
  assert_contains "$log_file" "go test -coverprofile="
  assert_contains "$log_file" "./cmd/app"
  assert_contains "$log_file" "./internal/keep"
  assert_contains "$log_file" "./pkg/lib"
  assert_not_contains "$log_file" "./internal/testutil/skip"
  assert_not_contains "$log_file" "./internal/agentruntime/fake"
  assert_count "$log_file" "./cmd/app" "1"
}

main() {
  test_runs_under_system_bash_without_associative_arrays
}

main "$@"
