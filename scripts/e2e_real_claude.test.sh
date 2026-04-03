#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT_UNDER_TEST="$ROOT_DIR/scripts/e2e_real_claude.sh"
CODEX_OVERRIDE="npx -y @openai/codex@0.118.0 app-server"

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

run_harness() {
  local tmp_dir="$1"
  shift
  local bin_dir="$tmp_dir/bin"
  local harness_root="$tmp_dir/harness"

  mkdir -p "$tmp_dir/state"
  : >"$tmp_dir/tool.log"
  : >"$tmp_dir/maestro.log"
  write_mock_toolchain "$bin_dir"

  (
    export PATH="$bin_dir:/usr/bin:/bin"
    export MOCK_TOOL_LOG="$tmp_dir/tool.log"
    export FAKE_MAESTRO_LOG="$tmp_dir/maestro.log"
    export FAKE_STATE_DIR="$tmp_dir/state"
    export FAKE_HARNESS_ROOT="$harness_root"
    export E2E_ROOT="$harness_root"
    export E2E_KEEP_HARNESS=1
    while (($# > 0)); do
      export "$1"
      shift
    done
    bash "$SCRIPT_UNDER_TEST" >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"
  )
}

write_mock_toolchain() {
  local bin_dir="$1"
  mkdir -p "$bin_dir"

  cat >"$bin_dir/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'go %s\n' "$*" >>"$MOCK_TOOL_LOG"
if [[ "${1:-}" == "build" && "${2:-}" == "-o" ]]; then
  output="$3"
  mkdir -p "$(dirname "$output")"
  cat >"$output" <<'INNER'
#!/usr/bin/env bash
set -euo pipefail

printf 'maestro %s\n' "$*" >>"$FAKE_MAESTRO_LOG"

json_mode=0
while [[ "${1:-}" == --* ]]; do
  case "${1:-}" in
    --json)
      json_mode=1
      shift
      ;;
    --db|--repo|--log-level|--api-url)
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

command_name="${1:-}"
shift || true

verify_case="${FAKE_VERIFY_SCENARIO:-ok}"

print_verify_json() {
  case "$verify_case" in
    ok)
      printf '{"ok":true,"checks":{"runtime_default":"ok","claude_version_status":"ok","runtime_claude":"ok","claude_auth_source":"OAuth","claude_auth_source_status":"ok","claude_session_status":"ok","claude_session_bare_mode":"ok","claude_session_additional_directories":"ok"},"remediation":{}}\n'
      ;;
    token-auth)
      printf '{"ok":true,"checks":{"runtime_default":"warn","claude_version_status":"ok","runtime_claude":"warn","claude_auth_source":"ANTHROPIC_AUTH_TOKEN","claude_auth_source_status":"warn","claude_session_status":"ok","claude_session_bare_mode":"ok","claude_session_additional_directories":"ok"},"warnings":["claude_auth_source: ANTHROPIC_AUTH_TOKEN"],"remediation":{}}\n'
      ;;
    missing-claude)
      printf '{"ok":false,"checks":{"claude_version_status":"fail","runtime_claude":"fail"},"errors":["claude: unable to locate executable"],"remediation":{"claude":"Install Claude Code or update `runtime.claude.command` in WORKFLOW.md, then re-run `maestro verify`."}}\n'
      return 1
      ;;
    auth-fail)
      printf '{"ok":false,"checks":{"claude_auth_source":"OAuth","claude_auth_source_status":"fail","runtime_claude":"fail"},"errors":["claude_auth_source: OAuth"],"remediation":{"claude_auth_source":"Log in with Claude Code or configure a supported auth source, then re-run `maestro verify`."}}\n'
      return 1
      ;;
    bare)
      printf '{"ok":false,"checks":{"claude_session_status":"fail","claude_session_bare_mode":"fail","runtime_claude":"fail"},"errors":["claude_session_bare_mode: runtime command includes `--bare`"],"remediation":{"claude_session_bare_mode":"Remove `--bare`, `--permission-mode auto`, `--permission-mode bypassPermissions`, `permissions.defaultMode: auto`, or `permissions.defaultMode: bypassPermissions` from the Claude configuration."}}\n'
      return 1
      ;;
    permission-auto)
      printf '{"ok":false,"checks":{"claude_session_status":"fail","claude_session_bare_mode":"fail","runtime_claude":"fail"},"errors":["claude_session_bare_mode: runtime command sets `--permission-mode auto`"],"remediation":{"claude_session_bare_mode":"Remove `--bare`, `--permission-mode auto`, `--permission-mode bypassPermissions`, `permissions.defaultMode: auto`, or `permissions.defaultMode: bypassPermissions` from the Claude configuration."}}\n'
      return 1
      ;;
    permission-bypass)
      printf '{"ok":false,"checks":{"claude_session_status":"fail","claude_session_bare_mode":"fail","runtime_claude":"fail"},"errors":["claude_session_bare_mode: runtime command sets `--permission-mode bypassPermissions`"],"remediation":{"claude_session_bare_mode":"Remove `--bare`, `--permission-mode auto`, `--permission-mode bypassPermissions`, `permissions.defaultMode: auto`, or `permissions.defaultMode: bypassPermissions` from the Claude configuration."}}\n'
      return 1
      ;;
    add-dir)
      printf '{"ok":false,"checks":{"claude_session_status":"fail","claude_session_additional_directories":"fail","claude_additional_directories":"../docs","runtime_claude":"fail"},"errors":["claude_session_additional_directories: ../docs"],"remediation":{"claude_session_additional_directories":"Remove `additionalDirectories` or `--add-dir` from Claude configuration so the session stays scoped to the Maestro workspace."}}\n'
      return 1
      ;;
    *)
      printf '{"ok":false,"checks":{"runtime_claude":"fail"}}\n'
      return 1
      ;;
  esac
}

print_doctor_text() {
  if [[ "$verify_case" != "ok" && "$verify_case" != "token-auth" ]]; then
    case "$verify_case" in
      auth-fail)
        cat <<'DOCTOR_AUTH'
Doctor
======
claude_auth_source: OAuth
claude_auth_source_status: fail
runtime_claude: fail
Errors:
- claude_auth_source: OAuth
Remediation:
- claude_auth_source: Log in with Claude Code or configure a supported auth source, then re-run `maestro verify`.
DOCTOR_AUTH
        ;;
      *)
        cat <<'DOCTOR_FAIL'
Doctor
======
runtime_claude: fail
DOCTOR_FAIL
        ;;
    esac
    return 1
  fi

  cat <<DOCTOR_OK
Doctor
======
claude_auth_source: $( [[ "$verify_case" == "token-auth" ]] && printf 'ANTHROPIC_AUTH_TOKEN' || printf 'OAuth' )
claude_auth_source_status: $( [[ "$verify_case" == "token-auth" ]] && printf 'warn' || printf 'ok' )
claude_session_additional_directories: ok
claude_session_bare_mode: ok
claude_session_status: ok
claude_version_status: ok
runtime_claude: $( [[ "$verify_case" == "token-auth" ]] && printf 'warn' || printf 'ok' )
DOCTOR_OK
}

case "$command_name" in
  workflow)
    subcommand="${1:-}"
    shift || true
    case "$subcommand" in
      init)
        repo_path="${1:-}"
        mkdir -p "$repo_path"
        cat >"$repo_path/WORKFLOW.md" <<'WORKFLOW_DOC'
---
tracker:
  kind: kanban
runtime:
  default: codex-appserver
  codex-appserver:
    provider: codex
    transport: app_server
    command: npx -y @openai/codex@0.118.0 app-server
    expected_version: 0.118.0
    approval_policy: never
    initial_collaboration_mode: default
    turn_timeout_ms: 1800000
    read_timeout_ms: 10000
    stall_timeout_ms: 300000
  claude:
    provider: claude
    transport: stdio
    command: claude
    approval_policy: never
    turn_timeout_ms: 300000
    read_timeout_ms: 5000
---
Issue {{ issue.identifier }}
WORKFLOW_DOC
        printf 'Initialized %s/WORKFLOW.md\n\n' "$repo_path"
        cat <<'WORKFLOW_INIT_OUTPUT'
Verification
============
claude_auth_source: OAuth
claude_auth_source_status: ok
claude_session_additional_directories: ok
claude_session_bare_mode: ok
claude_session_status: ok
claude_version_status: ok
runtime_claude: ok
runtime_default: ok
WORKFLOW_INIT_OUTPUT
        ;;
      *)
        exit 1
        ;;
    esac
    ;;
  spec-check)
    if [[ "${FAKE_SPEC_CHECK_FAIL:-0}" == "1" ]]; then
      cat <<'SPEC_FAIL'
Spec Check
==========
workflow_load: ok
workflow_prompt_render: ok
workflow_version: fail
SPEC_FAIL
      exit 1
    fi
    cat <<'SPEC_OK'
Spec Check
==========
workflow_load: ok
workflow_prompt_render: ok
workflow_version: ok
SPEC_OK
    ;;
  verify)
    print_verify_json
    ;;
  doctor)
    print_doctor_text
    ;;
  project)
    subcommand="${1:-}"
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
    subcommand="${1:-}"
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

  cat >"$bin_dir/npx" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'npx %s\n' "$*" >>"$MOCK_TOOL_LOG"
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

  chmod +x "$bin_dir/go" "$bin_dir/claude" "$bin_dir/claude-wrapper" "$bin_dir/npx" "$bin_dir/git" "$bin_dir/sqlite3"
}

test_successful_run_bootstraps_and_checks_claude_preflight() {
  local tmp_dir harness_root
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-real-claude-test-success.XXXXXX")"
  harness_root="$tmp_dir/harness"

  run_harness "$tmp_dir" "E2E_CODEX_COMMAND=$CODEX_OVERRIDE"

  [[ -f "$harness_root/WORKFLOW.md" ]] || fail "expected WORKFLOW.md to be created"
  [[ -f "$harness_root/artifacts/claude-artifact.txt" ]] || fail "expected claude artifact to be created"
  assert_contains "$harness_root/WORKFLOW.md" "default: claude"
  assert_contains "$harness_root/WORKFLOW.md" "provider: claude"
  assert_contains "$harness_root/WORKFLOW.md" "command: 'claude'"
  assert_contains "$tmp_dir/stdout.txt" "Running maestro workflow init bootstrap"
  assert_contains "$tmp_dir/stdout.txt" "Running maestro spec-check"
  assert_contains "$tmp_dir/stdout.txt" "Running maestro verify preflight"
  assert_contains "$tmp_dir/stdout.txt" "Running maestro doctor preflight"
  assert_contains "$tmp_dir/stdout.txt" "Real Claude e2e flow completed successfully."
  assert_contains "$tmp_dir/stdout.txt" "CL-1 -> $harness_root/artifacts/claude-artifact.txt"
  assert_contains "$harness_root/workflow-init.log" "claude_version_status: ok"
  assert_contains "$harness_root/workflow-init.log" "claude_auth_source_status: ok"
  assert_contains "$harness_root/spec-check.log" "workflow_version: ok"
  assert_contains "$harness_root/verify.log" '"claude_session_bare_mode":"ok"'
  assert_contains "$harness_root/doctor.log" "claude_session_additional_directories: ok"
  assert_contains "$tmp_dir/tool.log" "git init -q"
  assert_contains "$tmp_dir/tool.log" "git commit -q -m test init"
  assert_in_order "$tmp_dir/maestro.log" "maestro --db $harness_root/.maestro/maestro.db workflow init $harness_root --defaults --runtime-command $CODEX_OVERRIDE" "maestro spec-check --repo $harness_root"
  assert_in_order "$tmp_dir/maestro.log" "maestro spec-check --repo $harness_root" "maestro --json verify --repo $harness_root --db $harness_root/.maestro/maestro.db"
  assert_in_order "$tmp_dir/maestro.log" "maestro --json verify --repo $harness_root --db $harness_root/.maestro/maestro.db" "maestro doctor --repo $harness_root --db $harness_root/.maestro/maestro.db"
  assert_in_order "$tmp_dir/maestro.log" "maestro doctor --repo $harness_root --db $harness_root/.maestro/maestro.db" "maestro project create"
  [[ ! -s "$tmp_dir/stderr.txt" ]] || fail "expected successful run to avoid stderr output"
}

test_verify_failures_print_actionable_claude_remediation() {
  local scenarios tmp_dir
  scenarios=$'missing-claude|Install Claude Code or update `runtime.claude.command` in WORKFLOW.md, then re-run `maestro verify`.\nauth-fail|Log in with Claude Code or configure a supported auth source, then re-run `maestro verify`.\nbare|runtime command includes `--bare`\npermission-auto|runtime command sets `--permission-mode auto`\npermission-bypass|runtime command sets `--permission-mode bypassPermissions`\nadd-dir|Remove `additionalDirectories` or `--add-dir` from Claude configuration so the session stays scoped to the Maestro workspace.'

  while IFS='|' read -r scenario expected; do
    [[ -n "$scenario" ]] || continue
    tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-real-claude-test-fail-${scenario}.XXXXXX")"
    if run_harness "$tmp_dir" "E2E_CODEX_COMMAND=$CODEX_OVERRIDE" "FAKE_VERIFY_SCENARIO=$scenario"; then
      fail "expected harness failure for verify scenario $scenario"
    fi
    assert_contains "$tmp_dir/stderr.txt" "maestro verify failed for the Claude harness"
    assert_contains "$tmp_dir/stderr.txt" "Verify log: $tmp_dir/harness/verify.log"
    assert_contains "$tmp_dir/stderr.txt" "Last verify output:"
    assert_contains "$tmp_dir/stderr.txt" "$expected"
  done <<<"$scenarios"
}

test_timeout_failure_prints_issue_and_path_diagnostics() {
  local tmp_dir harness_root
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-real-claude-test-timeout.XXXXXX")"
  harness_root="$tmp_dir/harness"

  if run_harness "$tmp_dir" "E2E_CODEX_COMMAND=$CODEX_OVERRIDE" "FAKE_RUN_STICKS=1" "E2E_TIMEOUT_SEC=1" "E2E_POLL_SEC=0.1"; then
    fail "expected the harness to fail when the issue never reaches done"
  fi

  assert_contains "$tmp_dir/stderr.txt" "CL-1 did not reach done within 1s"
  assert_contains "$tmp_dir/stderr.txt" "Harness root: $harness_root"
  assert_contains "$tmp_dir/stderr.txt" "Workflow init log: $harness_root/workflow-init.log"
  assert_contains "$tmp_dir/stderr.txt" "Spec-check log: $harness_root/spec-check.log"
  assert_contains "$tmp_dir/stderr.txt" "Verify log: $harness_root/verify.log"
  assert_contains "$tmp_dir/stderr.txt" "Doctor log: $harness_root/doctor.log"
  assert_contains "$tmp_dir/stderr.txt" "Orchestrator log: $harness_root/orchestrator.log"
  assert_contains "$tmp_dir/stderr.txt" "Workspaces root: $harness_root/workspaces"
  assert_contains "$tmp_dir/stderr.txt" "Issue: CL-1"
  assert_contains "$tmp_dir/stderr.txt" "Current state: ready"
  assert_contains "$tmp_dir/stderr.txt" "mock orchestrator started but did not finish"
}

test_override_command_is_used_for_preflight_requirement() {
  local tmp_dir harness_root
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-real-claude-test-override.XXXXXX")"
  harness_root="$tmp_dir/harness"

  run_harness "$tmp_dir" "E2E_CODEX_COMMAND=$CODEX_OVERRIDE" "E2E_CLAUDE_COMMAND=claude-wrapper --verbose"

  assert_contains "$harness_root/WORKFLOW.md" "command: 'claude-wrapper --verbose'"
  [[ ! -s "$tmp_dir/stderr.txt" ]] || fail "expected override command run to avoid stderr output"
}

test_override_command_with_env_assignment_is_used_for_preflight_requirement() {
  local tmp_dir harness_root override
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-real-claude-test-override-env.XXXXXX")"
  harness_root="$tmp_dir/harness"
  override="CLAUDE_CONFIG_DIR=$tmp_dir/claude-config claude-wrapper --verbose"

  run_harness "$tmp_dir" "E2E_CODEX_COMMAND=$CODEX_OVERRIDE" "E2E_CLAUDE_COMMAND=$override"

  assert_contains "$harness_root/WORKFLOW.md" "command: '$override'"
  [[ ! -s "$tmp_dir/stderr.txt" ]] || fail "expected env-assignment override run to avoid stderr output"
}

test_override_command_validation_does_not_execute_command_substitutions() {
  local tmp_dir probe_path override
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-real-claude-test-no-eval.XXXXXX")"
  probe_path="$tmp_dir/preflight-side-effect.txt"
  override="claude-wrapper \$(touch $probe_path) --verbose"
  mkdir -p "$tmp_dir/bin"
  cat >"$tmp_dir/bin/claude-wrapper" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
  chmod +x "$tmp_dir/bin/claude-wrapper"

  (
    export PATH="$tmp_dir/bin:/usr/bin:/bin"
    export ROOT_DIR
    source "$ROOT_DIR/scripts/lib/e2e_real_claude_harness.sh"
    require_command_string "Claude" "$override"
  ) >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"

  [[ ! -e "$probe_path" ]] || fail "expected preflight validation to avoid executing command substitutions"
  [[ ! -s "$tmp_dir/stderr.txt" ]] || fail "expected command-substitution override run to avoid stderr output"
}

main() {
  test_successful_run_bootstraps_and_checks_claude_preflight
  test_verify_failures_print_actionable_claude_remediation
  test_timeout_failure_prints_issue_and_path_diagnostics
  test_override_command_is_used_for_preflight_requirement
  test_override_command_with_env_assignment_is_used_for_preflight_requirement
  test_override_command_validation_does_not_execute_command_substitutions
}

main "$@"
