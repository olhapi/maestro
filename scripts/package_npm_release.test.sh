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

  chmod +x "$bin_dir/npm" "$bin_dir/ensure-dashboard"
}

test_pack_root_includes_launcher_files_and_skills() {
  local tmp_dir bin_dir log_file stage_dir
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/package-npm-release-test-root.XXXXXX")"
  bin_dir="$tmp_dir/bin"
  log_file="$tmp_dir/log.txt"
  stage_dir="$tmp_dir/stage"
  make_mock_bin "$bin_dir"
  : >"$log_file"

  PATH="$bin_dir:$PATH" \
  LOG_FILE="$log_file" \
  STAGE_DIR="$stage_dir" \
  PACK_DIR="$tmp_dir/pack" \
  MAESTRO_ENSURE_DASHBOARD_DIST_BIN="$bin_dir/ensure-dashboard" \
  bash "$SCRIPT_UNDER_TEST" pack-root 1.2.3 >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"

  assert_in_order "$log_file" "ensure-dashboard" "npm pack --pack-destination $tmp_dir/pack --json"
  assert_contains "$tmp_dir/stdout.txt" "Packed root package: $tmp_dir/pack/mock-package.tgz"
  assert_contains "$stage_dir/root/package.json" "\"name\": \"@olhapi/maestro\""
  assert_contains "$stage_dir/root/package.json" "\"version\": \"1.2.3\""

  for required in \
    "$stage_dir/root/bin/maestro" \
    "$stage_dir/root/bin/maestro.cmd" \
    "$stage_dir/root/bin/maestro.ps1" \
    "$stage_dir/root/bin/maestro.js" \
    "$stage_dir/root/lib/cli.js" \
    "$stage_dir/root/lib/docker-plan.js" \
    "$stage_dir/root/lib/install-skills.js" \
    "$stage_dir/root/lib/runtime-state.js" \
    "$stage_dir/root/share/skills/maestro/SKILL.md"; do
    [[ -f "$required" ]] || fail "expected packaged file $required"
  done
}

main() {
  test_pack_root_includes_launcher_files_and_skills
}

main "$@"
