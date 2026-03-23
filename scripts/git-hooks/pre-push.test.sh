#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT_UNDER_TEST="$ROOT_DIR/scripts/git-hooks/pre-push.sh"

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

make_mock_bin() {
  local bin_dir="$1"
  mkdir -p "$bin_dir"

  cat >"$bin_dir/git" <<EOF
#!/usr/bin/env bash
set -euo pipefail
if [[ "\$1" == "rev-parse" && "\$2" == "--show-toplevel" ]]; then
  printf '%s\n' "$ROOT_DIR"
  exit 0
fi
exit 1
EOF
  chmod +x "$bin_dir/git"

  cat >"$bin_dir/uname" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  -s)
    printf 'Darwin\n'
    ;;
  -m)
    printf 'arm64\n'
    ;;
  *)
    exit 1
    ;;
esac
EOF
  chmod +x "$bin_dir/uname"

  cat >"$bin_dir/pnpm" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'pnpm %s\n' "$*" >>"$LOG_FILE"
exit 0
EOF
  chmod +x "$bin_dir/pnpm"

  cat >"$bin_dir/arch" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'arch %s\n' "$*" >>"$LOG_FILE"
if [[ "$1" == "-x86_64" && "${2:-}" == "sh" && "${3:-}" == "-c" ]]; then
  if [[ "${MOCK_X64_READY:-0}" == "1" ]]; then
    exit 0
  fi
  exit 1
fi
if [[ "$1" == "-x86_64" && "${2:-}" == "./scripts/git-hooks/host-package-smoke.sh" ]]; then
  printf 'host-package-smoke called\n' >>"$LOG_FILE"
  exit 0
fi
exit 0
EOF
  chmod +x "$bin_dir/arch"
}

run_case() {
  local name="$1"
  local x64_ready="$2"
  local expect_smoke="$3"

  local tmp_dir bin_dir log_file
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/maestro-pre-push-test.XXXXXX")"
  bin_dir="$tmp_dir/bin"
  log_file="$tmp_dir/log.txt"
  make_mock_bin "$bin_dir"
  : >"$log_file"

  if ! PATH="$bin_dir:$PATH" LOG_FILE="$log_file" MOCK_X64_READY="$x64_ready" sh "$SCRIPT_UNDER_TEST" >"$log_file" 2>&1; then
    fail "pre-push script failed in case '$name'"
  fi

  assert_contains "$log_file" "pnpm verify:pre-push"
  if [[ "$expect_smoke" == "1" ]]; then
    assert_contains "$log_file" "host-package-smoke called"
    assert_not_contains "$log_file" "skipping extra macOS x64 smoke"
  else
    assert_contains "$log_file" "skipping extra macOS x64 smoke; x64 toolchain prerequisites unavailable"
    assert_not_contains "$log_file" "host-package-smoke called"
  fi
}

run_case "skip when prerequisites are missing" 0 0
run_case "run when prerequisites are available" 1 1
