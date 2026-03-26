#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT_UNDER_TEST="$ROOT_DIR/scripts/package_npm_release.sh"

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

  cat >"$bin_dir/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'go %s\n' "$*" >>"$LOG_FILE"
output=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o)
      output="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
[[ -n "$output" ]] || exit 1
mkdir -p "$(dirname "$output")"
: >"$output"
chmod 755 "$output"
EOF

  cat >"$bin_dir/npm" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'npm %s\n' "$*" >>"$LOG_FILE"
pack_dir=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --pack-destination)
      pack_dir="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
[[ -n "$pack_dir" ]] || exit 1
mkdir -p "$pack_dir"
touch "$pack_dir/mock-package.tgz"
printf '[{"filename":"mock-package.tgz"}]'
EOF

  cat >"$bin_dir/ensure-dashboard" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'ensure-dashboard\n' >>"$LOG_FILE"
exit 0
EOF

  chmod +x "$bin_dir/go" "$bin_dir/npm" "$bin_dir/ensure-dashboard"
}

test_pack_leaf_uses_shared_dashboard_helper() {
  local tmp_dir bin_dir log_file
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/package-npm-release-test-leaf.XXXXXX")"
  bin_dir="$tmp_dir/bin"
  log_file="$tmp_dir/log.txt"
  make_mock_bin "$bin_dir"
  : >"$log_file"

  PATH="$bin_dir:$PATH" \
  LOG_FILE="$log_file" \
  STAGE_DIR="$tmp_dir/stage" \
  PACK_DIR="$tmp_dir/pack" \
  MAESTRO_ENSURE_DASHBOARD_DIST_BIN="$bin_dir/ensure-dashboard" \
  bash "$SCRIPT_UNDER_TEST" pack-leaf 1.2.3 darwin-arm64 >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"

  assert_in_order "$log_file" "ensure-dashboard" "go build -ldflags -X main.version=1.2.3 -o $tmp_dir/stage/olhapi/maestro-darwin-arm64/lib/maestro ./cmd/maestro" "npm pack --pack-destination $tmp_dir/pack --json"
  assert_contains "$tmp_dir/stdout.txt" "Packed leaf package: $tmp_dir/pack/mock-package.tgz"
}

test_pack_root_skips_dashboard_helper() {
  local tmp_dir bin_dir log_file
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/package-npm-release-test-root.XXXXXX")"
  bin_dir="$tmp_dir/bin"
  log_file="$tmp_dir/log.txt"
  make_mock_bin "$bin_dir"
  : >"$log_file"

  PATH="$bin_dir:$PATH" \
  LOG_FILE="$log_file" \
  STAGE_DIR="$tmp_dir/stage" \
  PACK_DIR="$tmp_dir/pack" \
  MAESTRO_ENSURE_DASHBOARD_DIST_BIN="$bin_dir/ensure-dashboard" \
  bash "$SCRIPT_UNDER_TEST" pack-root 1.2.3 >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"

  assert_not_contains "$log_file" "ensure-dashboard"
  assert_contains "$log_file" "npm pack --pack-destination $tmp_dir/pack --json"
}

main() {
  test_pack_leaf_uses_shared_dashboard_helper
  test_pack_root_skips_dashboard_helper
}

main "$@"
