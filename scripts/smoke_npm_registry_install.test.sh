#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT_UNDER_TEST="$ROOT_DIR/scripts/smoke_npm_registry_install.sh"

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
exit 0
EOF

  cat >"$bin_dir/npm" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
exit 0
EOF

  cat >"$bin_dir/npx" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == *"maestro version"* ]]; then
  printf 'maestro 0.0.0-ci\n'
  exit 0
fi
if [[ "$*" == *"maestro does-not-exist"* ]]; then
  exit 1
fi
exit 0
EOF

  chmod +x "$bin_dir/node" "$bin_dir/npm" "$bin_dir/npx"
}

test_registry_smoke_only_requires_selected_leaf() {
  local tmp_dir
  tmp_dir="$(mktemp -d)"
  local pack_dir="$tmp_dir/dist/npm"
  local bin_dir="$tmp_dir/bin"
  local output="$tmp_dir/output.log"

  make_tarball "$pack_dir" "olhapi-maestro-0.0.0-ci.tgz"
  make_tarball "$pack_dir" "olhapi-maestro-linux-x64-gnu-0.0.0-ci.tgz"
  write_mock_commands "$bin_dir"

  if ! PATH="$bin_dir:$PATH" PACK_DIR="$pack_dir" "$SCRIPT_UNDER_TEST" 0.0.0-ci linux-x64-gnu >"$output" 2>&1; then
    cat "$output" >&2
    fail "expected smoke script to succeed with only the selected leaf tarball"
  fi

  assert_contains "$output" "Registry smoke test passed for linux-x64-gnu"
}

test_registry_smoke_requires_selected_leaf() {
  local tmp_dir
  tmp_dir="$(mktemp -d)"
  local pack_dir="$tmp_dir/dist/npm"
  local output="$tmp_dir/output.log"

  make_tarball "$pack_dir" "olhapi-maestro-0.0.0-ci.tgz"

  if PATH="$PATH" PACK_DIR="$pack_dir" "$SCRIPT_UNDER_TEST" 0.0.0-ci linux-x64-gnu >"$output" 2>&1; then
    cat "$output" >&2
    fail "expected smoke script to fail when the selected leaf tarball is missing"
  fi

  assert_contains "$output" "missing leaf tarball:"
  assert_contains "$output" "olhapi-maestro-linux-x64-gnu-0.0.0-ci.tgz"
}

main() {
  test_registry_smoke_only_requires_selected_leaf
  test_registry_smoke_requires_selected_leaf
}

main "$@"
