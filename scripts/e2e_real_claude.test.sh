#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT_UNDER_TEST="$ROOT_DIR/scripts/e2e_real_claude.sh"

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
  local first="$2"
  local second="$3"
  local first_line second_line
  first_line="$(grep -Fn -- "$first" "$file" | head -n 1 | cut -d: -f1)"
  second_line="$(grep -Fn -- "$second" "$file" | head -n 1 | cut -d: -f1)"
  if [[ -z "$first_line" || -z "$second_line" || "$first_line" -ge "$second_line" ]]; then
    fail "expected '$first' to appear before '$second' in $file"
  fi
}

write_mock_toolchain() {
  local bin_dir="$1"
  mkdir -p "$bin_dir"

  cat >"$bin_dir/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'go %s\n' "$*" >>"$MOCK_TOOL_LOG"
if [[ "$1" == "build" && "$2" == "-o" ]]; then
  output="$3"
  mkdir -p "$(dirname "$output")"
  cat >"$output" <<'INNER'
#!/usr/bin/env bash
set -euo pipefail
printf 'maestro %s\n' "$*" >>"$FAKE_MAESTRO_LOG"
command_name="$1"
shift || true
case "$command_name" in
  --json)
    command_name="$1"
    shift || true
    ;;
esac
case "$command_name" in
  verify)
    if [[ "${FAKE_VERIFY_FAIL:-0}" == "1" ]]; then
      printf '{"ok":false,"checks":{"runtime_claude":"fail","claude_auth_source_status":"fail"}}\n'
      exit 1
    fi
    printf '{"ok":true,"checks":{"runtime_claude":"ok","claude_auth_source":"OAuth","claude_auth_source_status":"ok","claude_session_status":"ok","claude_session_bare_mode":"ok","claude_session_additional_directories":"ok"}}\n'
    ;;
  project)
    subcommand="$1"
    shift || true
    case "$subcommand" in
      create)
        printf 'proj-1\n'
        ;;
      *)
        exit 1
        ;;
    esac
    ;;
  issue)
    subcommand="$1"
    shift || true
    case "$subcommand" in
      create)
        issue_id="CL-1"
        printf 'ready' >"$FAKE_STATE_DIR/$issue_id.state"
        printf 'Create Claude e2e artifact\n' >"$FAKE_STATE_DIR/$issue_id.title"
        printf '%s\n' "$issue_id"
        ;;
      update)
        exit 0
        ;;
      move)
        issue_id="$1"
        state="$2"
        printf '%s' "$state" >"$FAKE_STATE_DIR/$issue_id.state"
        ;;
      show)
        issue_id="$1"
        title="$(cat "$FAKE_STATE_DIR/$issue_id.title")"
        state="$(cat "$FAKE_STATE_DIR/$issue_id.state")"
        printf 'Title: %s\n' "$title"
        printf 'State: %s\n' "$state"
        ;;
      *)
        exit 1
        ;;
    esac
    ;;
  run)
    if [[ "${FAKE_RUN_STICKS:-0}" == "1" ]]; then
      printf 'mock orchestrator started but did not finish\n'
      sleep 2
      exit 0
    fi
    printf 'mock orchestrator completed issue\n'
    printf 'done' >"$FAKE_STATE_DIR/CL-1.state"
    mkdir -p "$FAKE_HARNESS_ROOT/artifacts"
    printf 'maestro claude e2e ok\n' >"$FAKE_HARNESS_ROOT/artifacts/claude-artifact.txt"
    ;;
  *)
    exit 1
    ;;
esac
INNER
  chmod +x "$output"
  exit 0
fi
exit 1
EOF

  cat >"$bin_dir/claude" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'claude %s\n' "$*" >>"$MOCK_TOOL_LOG"
case "${1:-}" in
  --version)
    printf 'claude-cli 1.2.3\n'
    ;;
  auth)
    if [[ "${2:-}" == "status" && "${3:-}" == "--json" ]]; then
      printf '{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","email":"e2e@example.com"}\n'
    fi
    ;;
esac
EOF

  cat >"$bin_dir/claude-wrapper" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'claude-wrapper %s\n' "$*" >>"$MOCK_TOOL_LOG"
exit 0
EOF

  cat >"$bin_dir/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'git %s\n' "$*" >>"$MOCK_TOOL_LOG"
exit 0
EOF

  cat >"$bin_dir/sqlite3" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'sqlite3 %s\n' "$*" >>"$MOCK_TOOL_LOG"
exit 0
EOF

  chmod +x "$bin_dir/go" "$bin_dir/claude" "$bin_dir/claude-wrapper" "$bin_dir/git" "$bin_dir/sqlite3"
}

test_successful_run_builds_claude_workflow_and_verifies_first() {
  local tmp_dir bin_dir harness_root
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-real-claude-test-success.XXXXXX")"
  bin_dir="$tmp_dir/bin"
  harness_root="$tmp_dir/harness"

  mkdir -p "$tmp_dir/state"
  : >"$tmp_dir/tool.log"
  : >"$tmp_dir/maestro.log"
  write_mock_toolchain "$bin_dir"

  PATH="$bin_dir:$PATH" \
  MOCK_TOOL_LOG="$tmp_dir/tool.log" \
  FAKE_MAESTRO_LOG="$tmp_dir/maestro.log" \
  FAKE_STATE_DIR="$tmp_dir/state" \
  FAKE_HARNESS_ROOT="$harness_root" \
  E2E_ROOT="$harness_root" \
  E2E_KEEP_HARNESS=1 \
  bash "$SCRIPT_UNDER_TEST" >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"

  [[ -f "$harness_root/WORKFLOW.md" ]] || fail "expected WORKFLOW.md to be created"
  [[ -f "$harness_root/artifacts/claude-artifact.txt" ]] || fail "expected claude artifact to be created"
  assert_contains "$harness_root/WORKFLOW.md" "default: claude"
  assert_contains "$harness_root/WORKFLOW.md" "provider: claude"
  assert_contains "$harness_root/WORKFLOW.md" "command: 'claude'"
  assert_contains "$tmp_dir/stdout.txt" "Running maestro verify preflight"
  assert_contains "$tmp_dir/stdout.txt" "Real Claude e2e flow completed successfully."
  assert_contains "$tmp_dir/stdout.txt" "CL-1 -> $harness_root/artifacts/claude-artifact.txt"
  assert_contains "$tmp_dir/tool.log" "git init -q"
  assert_contains "$tmp_dir/tool.log" "git commit -q -m test init"
  assert_in_order "$tmp_dir/maestro.log" "maestro --json verify" "maestro project create"
  [[ ! -s "$tmp_dir/stderr.txt" ]] || fail "expected successful run to avoid stderr output"
}

test_timeout_failure_prints_issue_and_path_diagnostics() {
  local tmp_dir bin_dir harness_root
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-real-claude-test-timeout.XXXXXX")"
  bin_dir="$tmp_dir/bin"
  harness_root="$tmp_dir/harness"

  mkdir -p "$tmp_dir/state"
  : >"$tmp_dir/tool.log"
  : >"$tmp_dir/maestro.log"
  write_mock_toolchain "$bin_dir"

  if PATH="$bin_dir:$PATH" \
    MOCK_TOOL_LOG="$tmp_dir/tool.log" \
    FAKE_MAESTRO_LOG="$tmp_dir/maestro.log" \
    FAKE_STATE_DIR="$tmp_dir/state" \
    FAKE_HARNESS_ROOT="$harness_root" \
    FAKE_RUN_STICKS=1 \
    E2E_ROOT="$harness_root" \
    E2E_KEEP_HARNESS=1 \
    E2E_TIMEOUT_SEC=1 \
    E2E_POLL_SEC=0.1 \
    bash "$SCRIPT_UNDER_TEST" >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"; then
    fail "expected the harness to fail when the issue never reaches done"
  fi

  assert_contains "$tmp_dir/stderr.txt" "CL-1 did not reach done within 1s"
  assert_contains "$tmp_dir/stderr.txt" "Harness root: $harness_root"
  assert_contains "$tmp_dir/stderr.txt" "Verify log: $harness_root/verify.log"
  assert_contains "$tmp_dir/stderr.txt" "Orchestrator log: $harness_root/orchestrator.log"
  assert_contains "$tmp_dir/stderr.txt" "Workspaces root: $harness_root/workspaces"
  assert_contains "$tmp_dir/stderr.txt" "Issue: CL-1"
  assert_contains "$tmp_dir/stderr.txt" "Current state: ready"
  assert_contains "$tmp_dir/stderr.txt" "mock orchestrator started but did not finish"
}

test_override_command_is_used_for_preflight_requirement() {
  local tmp_dir bin_dir harness_root restricted_path
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-real-claude-test-override.XXXXXX")"
  bin_dir="$tmp_dir/bin"
  harness_root="$tmp_dir/harness"
  restricted_path="$bin_dir:/usr/bin:/bin"

  mkdir -p "$tmp_dir/state"
  : >"$tmp_dir/tool.log"
  : >"$tmp_dir/maestro.log"
  write_mock_toolchain "$bin_dir"
  rm -f "$bin_dir/claude"

  PATH="$restricted_path" \
  MOCK_TOOL_LOG="$tmp_dir/tool.log" \
  FAKE_MAESTRO_LOG="$tmp_dir/maestro.log" \
  FAKE_STATE_DIR="$tmp_dir/state" \
  FAKE_HARNESS_ROOT="$harness_root" \
  E2E_ROOT="$harness_root" \
  E2E_KEEP_HARNESS=1 \
  E2E_CLAUDE_COMMAND="claude-wrapper --verbose" \
  bash "$SCRIPT_UNDER_TEST" >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"

  assert_contains "$harness_root/WORKFLOW.md" "command: 'claude-wrapper --verbose'"
  [[ ! -s "$tmp_dir/stderr.txt" ]] || fail "expected override command run to avoid stderr output"
}

test_override_command_with_env_assignment_is_used_for_preflight_requirement() {
  local tmp_dir bin_dir harness_root restricted_path
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-real-claude-test-override-env.XXXXXX")"
  bin_dir="$tmp_dir/bin"
  harness_root="$tmp_dir/harness"
  restricted_path="$bin_dir:/usr/bin:/bin"

  mkdir -p "$tmp_dir/state"
  : >"$tmp_dir/tool.log"
  : >"$tmp_dir/maestro.log"
  write_mock_toolchain "$bin_dir"
  rm -f "$bin_dir/claude"

  PATH="$restricted_path" \
  MOCK_TOOL_LOG="$tmp_dir/tool.log" \
  FAKE_MAESTRO_LOG="$tmp_dir/maestro.log" \
  FAKE_STATE_DIR="$tmp_dir/state" \
  FAKE_HARNESS_ROOT="$harness_root" \
  E2E_ROOT="$harness_root" \
  E2E_KEEP_HARNESS=1 \
  E2E_CLAUDE_COMMAND="CLAUDE_CONFIG_DIR=$tmp_dir/claude-config claude-wrapper --verbose" \
  bash "$SCRIPT_UNDER_TEST" >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"

  assert_contains "$harness_root/WORKFLOW.md" "command: 'CLAUDE_CONFIG_DIR=$tmp_dir/claude-config claude-wrapper --verbose'"
  [[ ! -s "$tmp_dir/stderr.txt" ]] || fail "expected env-assignment override run to avoid stderr output"
}

test_override_command_validation_does_not_execute_command_substitutions() {
  local tmp_dir bin_dir harness_root restricted_path probe_path override_command
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-real-claude-test-no-eval.XXXXXX")"
  bin_dir="$tmp_dir/bin"
  harness_root="$tmp_dir/harness"
  restricted_path="$bin_dir:/usr/bin:/bin"
  probe_path="$tmp_dir/preflight-side-effect.txt"
  override_command="claude-wrapper \$(touch $probe_path) --verbose"

  mkdir -p "$tmp_dir/state"
  : >"$tmp_dir/tool.log"
  : >"$tmp_dir/maestro.log"
  write_mock_toolchain "$bin_dir"
  rm -f "$bin_dir/claude"

  PATH="$restricted_path" \
  MOCK_TOOL_LOG="$tmp_dir/tool.log" \
  FAKE_MAESTRO_LOG="$tmp_dir/maestro.log" \
  FAKE_STATE_DIR="$tmp_dir/state" \
  FAKE_HARNESS_ROOT="$harness_root" \
  E2E_ROOT="$harness_root" \
  E2E_KEEP_HARNESS=1 \
  E2E_CLAUDE_COMMAND="$override_command" \
  bash "$SCRIPT_UNDER_TEST" >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"

  [[ ! -e "$probe_path" ]] || fail "expected preflight validation to avoid executing command substitutions"
  assert_contains "$harness_root/WORKFLOW.md" "command: 'claude-wrapper \$(touch $probe_path) --verbose'"
  [[ ! -s "$tmp_dir/stderr.txt" ]] || fail "expected command-substitution override run to avoid stderr output"
}

main() {
  test_successful_run_builds_claude_workflow_and_verifies_first
  test_timeout_failure_prints_issue_and_path_diagnostics
  test_override_command_is_used_for_preflight_requirement
  test_override_command_with_env_assignment_is_used_for_preflight_requirement
  test_override_command_validation_does_not_execute_command_substitutions
}

main "$@"
