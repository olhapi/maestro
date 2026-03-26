#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT_UNDER_TEST="$ROOT_DIR/scripts/git-hooks/pre-commit.sh"

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
    line="$(grep -nF -- "$pattern" "$file" | head -n 1 | cut -d: -f1)"
    if [[ -z "$line" ]]; then
      fail "missing ordered pattern '$pattern' in $file"
    fi
    if (( line <= previous_line )); then
      fail "pattern '$pattern' appeared out of order in $file"
    fi
    previous_line="$line"
  done
}

make_mock_bin() {
  local bin_dir="$1"
  mkdir -p "$bin_dir"

  cat >"$bin_dir/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == "rev-parse" && "$2" == "--show-toplevel" ]]; then
  printf '%s\n' "$ROOT_DIR"
  exit 0
fi
if [[ "$1" == "diff" && "$2" == "--cached" && "$3" == "--name-only" && "$4" == "--diff-filter=ACMR" ]]; then
  printf '%s' "${MOCK_STAGED:-}"
  exit 0
fi
exit 1
EOF

  cat >"$bin_dir/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'go %s\n' "$*" >>"$LOG_FILE"
exit 0
EOF

  cat >"$bin_dir/make" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'make %s\n' "$*" >>"$LOG_FILE"
exit 0
EOF

  cat >"$bin_dir/ensure-dashboard" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'ensure-dashboard\n' >>"$LOG_FILE"
exit 0
EOF

  chmod +x "$bin_dir/git" "$bin_dir/go" "$bin_dir/make" "$bin_dir/ensure-dashboard"
}

run_case() {
  local name="$1"
  local staged="$2"
  local expected_command="$3"

  local tmp_dir bin_dir log_file
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/maestro-pre-commit-test.XXXXXX")"
  bin_dir="$tmp_dir/bin"
  log_file="$tmp_dir/log.txt"
  make_mock_bin "$bin_dir"
  : >"$log_file"

  if ! PATH="$bin_dir:$PATH" \
    LOG_FILE="$log_file" \
    MOCK_STAGED="$staged" \
    ROOT_DIR="$ROOT_DIR" \
    MAESTRO_ENSURE_DASHBOARD_DIST_BIN="$bin_dir/ensure-dashboard" \
    sh "$SCRIPT_UNDER_TEST" >"$log_file" 2>&1; then
    fail "pre-commit script failed in case '$name'"
  fi

  assert_contains "$log_file" "git-hook: $bin_dir/ensure-dashboard"
  assert_contains "$log_file" "git-hook: $expected_command"
  assert_in_order "$log_file" "git-hook: $bin_dir/ensure-dashboard" "git-hook: $expected_command"
}

main() {
  run_case "targeted go test" $'internal/httpserver/server.go\n' "go test ./internal/httpserver"
  run_case "make test" $'Makefile\n' "make test"
}

main "$@"
