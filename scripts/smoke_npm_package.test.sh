#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT_UNDER_TEST="$ROOT_DIR/scripts/smoke_npm_package.sh"

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

make_tarball() {
  local dir="$1"
  local name="$2"
  mkdir -p "$dir"
  : >"$dir/$name"
}

write_mock_commands() {
  local bin_dir="$1"
  mkdir -p "$bin_dir"

  cat >"$bin_dir/node" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'node %s\n' "$*" >>"$LOG_FILE"
exit 0
EOF

  cat >"$bin_dir/npm" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'npm %s\n' "$*" >>"$LOG_FILE"
if [[ "$1" == "init" || "$1" == "install" ]]; then
  exit 0
fi
exit 0
EOF

  cat >"$bin_dir/npx" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'npx %s\n' "$*" >>"$LOG_FILE"
if [[ "$*" == *"maestro version"* ]]; then
  printf 'maestro 0.0.0-ci\n'
  exit 0
fi
if [[ "$*" == *"maestro --help"* ]]; then
  exit 0
fi
if [[ "$*" == *"maestro does-not-exist"* ]]; then
  exit 1
fi
exit 0
EOF

  chmod +x "$bin_dir/node" "$bin_dir/npm" "$bin_dir/npx"
}

test_smoke_package_runs_with_a_root_tarball() {
  local tmp_dir
  tmp_dir="$(mktemp -d)"
  local pack_dir="$tmp_dir/dist/npm"
  local bin_dir="$tmp_dir/bin"
  local output="$tmp_dir/output.log"
  local log_file="$tmp_dir/log.txt"

  make_tarball "$pack_dir" "olhapi-maestro-0.0.0-ci.tgz"
  write_mock_commands "$bin_dir"
  : >"$log_file"

  if ! PATH="$bin_dir:$PATH" \
    MAESTRO_NODE_BIN="$bin_dir/node" \
    PACK_DIR="$pack_dir" \
    MAESTRO_SMOKE_IMAGE="maestro-smoke:test" \
    LOG_FILE="$log_file" \
    "$SCRIPT_UNDER_TEST" 0.0.0-ci >"$output" 2>&1; then
    cat "$output" >&2
    fail "expected smoke script to succeed with the launcher tarball"
  fi

  assert_contains "$output" "Smoke test passed"
}

test_smoke_package_runs_with_a_root_tarball
