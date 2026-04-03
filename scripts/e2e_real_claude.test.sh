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

assert_exists() {
  local path="$1"
  [[ -e "$path" ]] || fail "expected path to exist: $path"
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
  if [[ "$(basename "$output")" == "maestro-claude-e2e-probe" ]]; then
    cat >"$output" <<'INNER'
#!/usr/bin/env bash
set -euo pipefail
printf 'probe %s\n' "$*" >>"$FAKE_PROBE_LOG"
if [[ "${FAKE_PROBE_FAIL:-0}" == "1" ]]; then
  exit 1
fi

mode="live"
issue_identifier=""
mcp_config=""
settings_path=""
db_path=""
evidence_prefix=""
allowed_tools=""
permission_prompt_tool="<none>"
permission_mode=""
strict_mcp_config="false"

while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --mode)
      mode="$2"
      shift 2
      ;;
    --issue-identifier)
      issue_identifier="$2"
      shift 2
      ;;
    --mcp-config)
      mcp_config="$2"
      shift 2
      ;;
    --settings)
      settings_path="$2"
      shift 2
      ;;
    --db)
      db_path="$2"
      shift 2
      ;;
    --evidence-prefix)
      evidence_prefix="$2"
      shift 2
      ;;
    --allowed-tools)
      allowed_tools="$2"
      shift 2
      ;;
    --permission-prompt-tool)
      permission_prompt_tool="$2"
      shift 2
      ;;
    --permission-mode)
      permission_mode="$2"
      shift 2
      ;;
    --strict-mcp-config)
      strict_mcp_config="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

session_id_for_issue() {
  case "$1" in
    CL-1)
      printf 'claude-session-1'
      ;;
    CL-2)
      printf 'claude-session-2'
      ;;
    CL-3)
      printf 'claude-session-3'
      ;;
    *)
      printf 'claude-session-x'
      ;;
  esac
}

infer_issue_identifier() {
  case "$(basename "$evidence_prefix")" in
    launch-1)
      printf 'CL-1'
      ;;
    launch-2|launch-3)
      printf 'CL-2'
      ;;
    launch-4)
      printf 'CL-3'
      ;;
    success-final)
      printf 'CL-1'
      ;;
    resume-final)
      printf 'CL-2'
      ;;
    interrupt-final)
      printf 'CL-3'
      ;;
    *)
      printf 'CL-1'
      ;;
  esac
}

stream_marker_for_issue() {
  case "$1" in
    CL-1)
      printf 'STREAM:%s:success-live' "$1"
      ;;
    CL-2)
      printf 'STREAM:%s:resume-live' "$1"
      ;;
    CL-3)
      printf 'STREAM:%s:interrupt-live' "$1"
      ;;
    *)
      printf 'STREAM:%s:live' "$1"
      ;;
  esac
}

if [[ -z "$issue_identifier" ]]; then
  issue_identifier="$(infer_issue_identifier)"
fi
session_id="$(session_id_for_issue "$issue_identifier")"
dashboard_source="live"
dashboard_status="running"
dashboard_stop_reason=""
execution_active="true"
execution_failure_class=""
execution_session_source="live"
execution_stop_reason=""
live_claude_session_seen="true"

if [[ "$mode" == "final" ]]; then
  dashboard_source="persisted"
  execution_active="false"
  execution_session_source="persisted"
  case "$issue_identifier" in
    CL-1|CL-2)
      dashboard_status="completed"
      dashboard_stop_reason="end_turn"
      execution_stop_reason="end_turn"
      ;;
    CL-3)
      dashboard_status="interrupted"
      dashboard_stop_reason="run_interrupted"
      execution_failure_class="run_interrupted"
      execution_stop_reason="run_interrupted"
      ;;
  esac
fi

mkdir -p "$(dirname "$evidence_prefix")"
cp "$mcp_config" "$evidence_prefix.mcp.json"
cp "$settings_path" "$evidence_prefix.settings.json"
cat >"$evidence_prefix.summary.txt" <<PROBE_SUMMARY
expected_tools_present=true
tool_call_get_issue_execution=ok
tool_call_server_info=ok
tool_call_list_issues=ok
tool_call_get_runtime_snapshot=ok
tool_call_list_sessions=ok
daemon_registry_entries_before=1
daemon_registry_entries_after=1
daemon_entry_stable=true
server_db_path=$db_path
daemon_db_path=$db_path
bridge_db_path=$db_path
dashboard_session_source=$dashboard_source
dashboard_session_status=$dashboard_status
dashboard_session_stop_reason=$dashboard_stop_reason
execution_active=$execution_active
execution_failure_class=$execution_failure_class
execution_runtime_name=claude
execution_runtime_provider=claude
execution_runtime_transport=stdio
execution_session_identifier_strategy=provider_session_uuid
execution_session_id=$session_id
execution_session_source=$execution_session_source
execution_stop_reason=$execution_stop_reason
execution_stream_marker=$(stream_marker_for_issue "$issue_identifier")
execution_stream_seen=true
execution_thread_id=$session_id
execution_provider_session_id=$session_id
issue_identifier=$issue_identifier
mode=$mode
strict_mcp_config=$strict_mcp_config
permission_mode=$permission_mode
allowed_tools=$allowed_tools
permission_prompt_tool=${permission_prompt_tool:-<none>}
settings_disable_auto_mode=disable
settings_use_auto_mode_during_plan=false
settings_disable_all_hooks=true
settings_include_git_instructions=false
settings_disable_bypass_permissions_mode=disable
live_claude_session_seen=$live_claude_session_seen
server_store_id=store-test
daemon_store_id=store-test
PROBE_SUMMARY
exit 0
INNER
  else
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

state_path() {
  printf '%s/%s.%s' "$FAKE_STATE_DIR" "$1" "$2"
}

write_state() {
  local entity="$1"
  local key="$2"
  local value="$3"
  mkdir -p "$FAKE_STATE_DIR"
  printf '%s' "$value" >"$(state_path "$entity" "$key")"
}

read_state() {
  local entity="$1"
  local key="$2"
  local path
  path="$(state_path "$entity" "$key")"
  if [[ -f "$path" ]]; then
    cat "$path"
  fi
}

issue_state_value() {
  read_state "$1" state
}

wait_for_issue_state() {
  local issue_id="$1"
  local expected="$2"
  while true; do
    if [[ "$(issue_state_value "$issue_id")" == "$expected" ]]; then
      return 0
    fi
    sleep 0.05
  done
}

wait_for_gate() {
  local path="$1"
  while true; do
    if [[ -f "$path" ]] && [[ "$(tr -d '\r\n' <"$path")" == "go" ]]; then
      return 0
    fi
    sleep 0.05
  done
}

session_id_for_issue() {
  case "$1" in
    CL-1)
      printf 'claude-session-1'
      ;;
    CL-2)
      printf 'claude-session-2'
      ;;
    CL-3)
      printf 'claude-session-3'
      ;;
    *)
      printf 'claude-session-x'
      ;;
  esac
}

snapshot_path() {
  printf '%s/%s.snapshot.%s' "$FAKE_STATE_DIR" "$1" "$2"
}

write_snapshot() {
  local issue_id="$1"
  local key="$2"
  local value="$3"
  printf '%s' "$value" >"$(snapshot_path "$issue_id" "$key")"
}

set_snapshot() {
  local issue_id="$1"
  local run_kind="$2"
  local stop_reason="$3"
  local error_text="$4"
  local session_id="$5"
  write_snapshot "$issue_id" run_kind "$run_kind"
  write_snapshot "$issue_id" stop_reason "$stop_reason"
  write_snapshot "$issue_id" error "$error_text"
  write_snapshot "$issue_id" runtime_provider "claude"
  write_snapshot "$issue_id" runtime_transport "stdio"
  write_snapshot "$issue_id" thread_id "$session_id"
  write_snapshot "$issue_id" session_id "$session_id"
  write_snapshot "$issue_id" "metadata.provider_session_id" "$session_id"
  write_snapshot "$issue_id" "metadata.session_identifier_strategy" "provider_session_uuid"
  write_snapshot "$issue_id" "metadata.claude_stop_reason" "$stop_reason"
}

event_path() {
  printf '%s/%s.event.%s' "$FAKE_STATE_DIR" "$1" "$2"
}

increment_event() {
  local issue_id="$1"
  local kind="$2"
  local count=0
  if [[ -f "$(event_path "$issue_id" "$kind")" ]]; then
    count="$(cat "$(event_path "$issue_id" "$kind")")"
  fi
  printf '%s' "$((count + 1))" >"$(event_path "$issue_id" "$kind")"
}

project_event_path() {
  printf '%s/project.%s.event.%s' "$FAKE_STATE_DIR" "$1" "$2"
}

increment_project_event() {
  local project_id="$1"
  local kind="$2"
  local count=0
  if [[ -f "$(project_event_path "$project_id" "$kind")" ]]; then
    count="$(cat "$(project_event_path "$project_id" "$kind")")"
  fi
  printf '%s' "$((count + 1))" >"$(project_event_path "$project_id" "$kind")"
}

next_issue_id() {
  local counter_path="$FAKE_STATE_DIR/issue.counter"
  local count=0
  if [[ -f "$counter_path" ]]; then
    count="$(cat "$counter_path")"
  fi
  count="$((count + 1))"
  printf '%s' "$count" >"$counter_path"
  printf 'CL-%s' "$count"
}

invoke_workflow() {
  local workflow_command="$1"
  local settings_path="$2"
  local mcp_config_path="$3"
  local resume_token="${4:-}"
  if [[ -z "$workflow_command" ]]; then
    return 0
  fi
  if [[ -n "$resume_token" ]]; then
    PATH="$PATH" bash -c "printf 'runtime prompt\n' | $workflow_command -r '$resume_token' -p --verbose --output-format=stream-json --include-partial-messages --permission-mode default --settings '$settings_path' --allowed-tools 'Bash,Edit,Write,MultiEdit' --mcp-config '$mcp_config_path' --strict-mcp-config"
    return 0
  fi
  PATH="$PATH" bash -c "printf 'runtime prompt\n' | $workflow_command -p --verbose --output-format=stream-json --include-partial-messages --permission-mode default --settings '$settings_path' --allowed-tools 'Bash,Edit,Write,MultiEdit' --mcp-config '$mcp_config_path' --strict-mcp-config"
}

run_success_scenario() {
  local workflow_command="$1"
  local settings_path="$2"
  local mcp_config_path="$3"
  local issue_id="CL-1"
  local gate_path="$FAKE_HARNESS_ROOT/artifacts/$issue_id.success.gate"
  local artifact_path="$FAKE_HARNESS_ROOT/artifacts/$issue_id.success.txt"
  local session_id
  session_id="$(session_id_for_issue "$issue_id")"
  if [[ "$(issue_state_value "$issue_id")" == "done" ]]; then
    return 0
  fi
  wait_for_issue_state "$issue_id" ready
  invoke_workflow "$workflow_command" "$settings_path" "$mcp_config_path"
  wait_for_gate "$gate_path"
  if [[ "${FAKE_RUN_STICKS:-0}" == "1" ]]; then
    printf 'mock orchestrator started but did not finish\n'
    while true; do
      sleep 1
    done
  fi
  mkdir -p "$(dirname "$artifact_path")"
  printf 'maestro claude success e2e ok\n' >"$artifact_path"
  set_snapshot "$issue_id" run_completed end_turn "" "$session_id"
  write_state "$issue_id" state done
}

run_resume_scenario() {
  local workflow_command="$1"
  local settings_path="$2"
  local mcp_config_path="$3"
  local issue_id="CL-2"
  local gate_path="$FAKE_HARNESS_ROOT/artifacts/$issue_id.resume.gate"
  local artifact_path="$FAKE_HARNESS_ROOT/artifacts/$issue_id.resume.txt"
  local interrupted_marker
  interrupted_marker="$(state_path "$issue_id" interrupted)"
  local session_id
  session_id="$(session_id_for_issue "$issue_id")"
  if [[ "$(issue_state_value "$issue_id")" == "done" ]]; then
    return 0
  fi
  wait_for_issue_state "$issue_id" ready
  if [[ -f "$interrupted_marker" ]]; then
    invoke_workflow "$workflow_command" "$settings_path" "$mcp_config_path" "$session_id"
    wait_for_gate "$gate_path"
    mkdir -p "$(dirname "$artifact_path")"
    printf 'maestro claude resume e2e ok\n' >"$artifact_path"
    set_snapshot "$issue_id" run_completed end_turn "" "$session_id"
    write_state "$issue_id" state done
    return 0
  fi
  trap "printf '1' >\"$interrupted_marker\"; increment_event \"$issue_id\" run_interrupted; increment_event \"$issue_id\" retry_scheduled; set_snapshot \"$issue_id\" run_interrupted run_interrupted run_interrupted \"$session_id\"; exit 0" TERM INT
  invoke_workflow "$workflow_command" "$settings_path" "$mcp_config_path"
  while [[ ! -f "$interrupted_marker" ]]; do
    sleep 0.05
  done
}

run_interrupt_scenario() {
  local workflow_command="$1"
  local settings_path="$2"
  local mcp_config_path="$3"
  local issue_id="CL-3"
  local stop_requested_path
  stop_requested_path="$(state_path proj-1 stop_requested)"
  local session_id
  session_id="$(session_id_for_issue "$issue_id")"
  wait_for_issue_state "$issue_id" ready
  invoke_workflow "$workflow_command" "$settings_path" "$mcp_config_path"
  while [[ ! -f "$stop_requested_path" ]]; do
    sleep 0.05
  done
  increment_event "$issue_id" run_stopped
  increment_event "$issue_id" run_interrupted
  set_snapshot "$issue_id" run_interrupted run_interrupted run_interrupted "$session_id"
}

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
      stop)
        project_id="$1"
        write_state "$project_id" stop_requested 1
        increment_project_event "$project_id" project_stop_requested
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
        issue_id="$(next_issue_id)"
        title="$1"
        write_state "$issue_id" state backlog
        write_state "$issue_id" title "$title"
        printf '%s\n' "$issue_id"
        ;;
      update)
        issue_id="$1"
        shift || true
        while [[ "$#" -gt 0 ]]; do
          case "$1" in
            --desc)
              write_state "$issue_id" desc "$2"
              shift 2
              ;;
            --permission-profile)
              write_state "$issue_id" permission_profile "$2"
              shift 2
              ;;
            *)
              shift
              ;;
          esac
        done
        exit 0
        ;;
      move)
        issue_id="$1"
        state="$2"
        write_state "$issue_id" state "$state"
        ;;
      show)
        issue_id="$1"
        title="$(read_state "$issue_id" title)"
        state="$(read_state "$issue_id" state)"
        printf 'Title: %s\n' "$title"
        printf 'State: %s\n' "$state"
        ;;
      *)
        exit 1
        ;;
    esac
    ;;
  run)
    db_path=""
    while [[ "$#" -gt 0 ]]; do
      if [[ "$1" == "--db" ]]; then
        db_path="$2"
        shift 2
        continue
      fi
      shift
    done
    runtime_dir="$FAKE_HARNESS_ROOT/fake-runtime"
    mkdir -p "$runtime_dir"
    mcp_config_path="$runtime_dir/mcp.json"
    settings_path="$runtime_dir/settings.json"
    workflow_command="$(sed -n "s/^    command: '\\(.*\\)'$/\\1/p" "$FAKE_HARNESS_ROOT/WORKFLOW.md" | sed "s/''/'/g" | head -n 1)"
    cat >"$mcp_config_path" <<RUNTIME_MCP
{
  "mcpServers": {
    "maestro": {
      "type": "stdio",
      "command": "maestro",
      "args": ["mcp", "--db", "$db_path"]
    }
  }
}
RUNTIME_MCP
    cat >"$settings_path" <<'RUNTIME_SETTINGS'
{
  "disableAutoMode": "disable",
  "useAutoModeDuringPlan": false,
  "disableAllHooks": true,
  "includeGitInstructions": false,
  "permissions": {
    "disableBypassPermissionsMode": "disable"
  }
}
RUNTIME_SETTINGS
    mkdir -p "$MAESTRO_DAEMON_REGISTRY_DIR"
    cat >"$MAESTRO_DAEMON_REGISTRY_DIR/store-test.json" <<RUNTIME_DAEMON
{"store_id":"store-test","db_path":"$db_path","pid":1234,"started_at":"2026-04-03T00:00:00Z","base_url":"http://127.0.0.1:12345/mcp","bearer_token":"token","version":"test","transport":"http"}
RUNTIME_DAEMON
    run_success_scenario "$workflow_command" "$settings_path" "$mcp_config_path"
    run_resume_scenario "$workflow_command" "$settings_path" "$mcp_config_path"
    run_interrupt_scenario "$workflow_command" "$settings_path" "$mcp_config_path"
    printf 'mock orchestrator completed lifecycle scenarios\n'
    ;;
  *)
    exit 1
    ;;
esac
INNER
  fi
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
  -p)
    if [[ -n "${FAKE_CLAUDE_STDIN_LOG:-}" ]]; then
      cat >"$FAKE_CLAUDE_STDIN_LOG"
    else
      cat >/dev/null
    fi
    sleep "${FAKE_CLAUDE_RUNTIME_SLEEP_SECONDS:-0.2}"
    ;;
esac
EOF

  cat >"$bin_dir/claude-wrapper" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'claude-wrapper %s\n' "$*" >>"$MOCK_TOOL_LOG"
cat >/dev/null || true
sleep "${FAKE_CLAUDE_RUNTIME_SLEEP_SECONDS:-0.2}"
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
query=""
for arg in "$@"; do
  case "$arg" in
    .timeout*)
      ;;
    *)
      query="$arg"
      ;;
  esac
done

snapshot_path() {
  printf '%s/%s.snapshot.%s' "$FAKE_STATE_DIR" "$1" "$2"
}

event_path() {
  printf '%s/%s.event.%s' "$FAKE_STATE_DIR" "$1" "$2"
}

project_event_path() {
  printf '%s/project.%s.event.%s' "$FAKE_STATE_DIR" "$1" "$2"
}

if [[ "$query" == UPDATE\ projects* ]]; then
  exit 0
fi

identifier="$(printf '%s' "$query" | sed -n "s/.*WHERE identifier = '\\([^']*\\)'.*/\\1/p")"
project_id="$(printf '%s' "$query" | sed -n "s/.*WHERE project_id = '\\([^']*\\)'.*/\\1/p")"
kind="$(printf '%s' "$query" | sed -n "s/.*AND kind = '\\([^']*\\)'.*/\\1/p")"

if [[ "$query" == *"FROM runtime_events"* ]]; then
  if [[ -n "$identifier" ]]; then
    if [[ -f "$(event_path "$identifier" "$kind")" ]]; then
      cat "$(event_path "$identifier" "$kind")"
    else
      printf '0'
    fi
    exit 0
  fi
  if [[ -f "$(project_event_path "$project_id" "$kind")" ]]; then
    cat "$(project_event_path "$project_id" "$kind")"
  else
    printf '0'
  fi
  exit 0
fi

if [[ "$query" == *"json_extract(session_json, '$.metadata."* ]]; then
  field="$(printf '%s' "$query" | sed -n "s/.*json_extract(session_json, '\\$.metadata\\.\\([^']*\\)').*/\\1/p")"
  if [[ -f "$(snapshot_path "$identifier" "metadata.$field")" ]]; then
    cat "$(snapshot_path "$identifier" "metadata.$field")"
  fi
  exit 0
fi

if [[ "$query" == *"json_extract(session_json, '$."* ]]; then
  field="$(printf '%s' "$query" | sed -n "s/.*json_extract(session_json, '\\$.\\([^']*\\)').*/\\1/p")"
  if [[ -f "$(snapshot_path "$identifier" "$field")" ]]; then
    cat "$(snapshot_path "$identifier" "$field")"
  fi
  exit 0
fi

if [[ "$query" == *"FROM issue_execution_sessions"* ]]; then
  column="$(printf '%s' "$query" | sed -n "s/SELECT COALESCE(\\([^,]*\\), '').*/\\1/p")"
  if [[ -f "$(snapshot_path "$identifier" "$column")" ]]; then
    cat "$(snapshot_path "$identifier" "$column")"
  fi
  exit 0
fi

exit 0
EOF

  chmod +x "$bin_dir/go" "$bin_dir/claude" "$bin_dir/claude-wrapper" "$bin_dir/git" "$bin_dir/sqlite3"
}

test_successful_run_builds_claude_workflow_and_verifies_first() {
  local tmp_dir bin_dir harness_root stdin_log
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-real-claude-test-success.XXXXXX")"
  bin_dir="$tmp_dir/bin"
  harness_root="$tmp_dir/harness"
  stdin_log="$tmp_dir/claude-stdin.txt"

  mkdir -p "$tmp_dir/state"
  : >"$tmp_dir/tool.log"
  : >"$tmp_dir/maestro.log"
  : >"$tmp_dir/probe.log"
  write_mock_toolchain "$bin_dir"

  PATH="$bin_dir:$PATH" \
  MOCK_TOOL_LOG="$tmp_dir/tool.log" \
  FAKE_MAESTRO_LOG="$tmp_dir/maestro.log" \
  FAKE_PROBE_LOG="$tmp_dir/probe.log" \
  FAKE_CLAUDE_STDIN_LOG="$stdin_log" \
  FAKE_STATE_DIR="$tmp_dir/state" \
  FAKE_HARNESS_ROOT="$harness_root" \
  E2E_ROOT="$harness_root" \
  E2E_KEEP_HARNESS=1 \
  bash "$SCRIPT_UNDER_TEST" >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"

  [[ -f "$harness_root/WORKFLOW.md" ]] || fail "expected WORKFLOW.md to be created"
  [[ -f "$harness_root/artifacts/CL-1.success.txt" ]] || fail "expected success artifact to be created"
  [[ -f "$harness_root/artifacts/CL-2.resume.txt" ]] || fail "expected resume artifact to be created"
  assert_exists "$harness_root/bin/claude-e2e-wrapper"
  assert_exists "$harness_root/claude-support/launch-1.summary.txt"
  assert_exists "$harness_root/claude-support/launch-2.summary.txt"
  assert_exists "$harness_root/claude-support/launch-3.summary.txt"
  assert_exists "$harness_root/claude-support/launch-4.summary.txt"
  assert_exists "$harness_root/claude-support/launch-1.mcp.json"
  assert_exists "$harness_root/claude-support/launch-1.settings.json"
  assert_exists "$harness_root/claude-support/success-final.summary.txt"
  assert_exists "$harness_root/claude-support/resume-final.summary.txt"
  assert_exists "$harness_root/claude-support/interrupt-final.summary.txt"
  assert_contains "$harness_root/WORKFLOW.md" "default: claude"
  assert_contains "$harness_root/WORKFLOW.md" "provider: claude"
  assert_contains "$harness_root/WORKFLOW.md" "command: '$harness_root/bin/claude-e2e-wrapper'"
  assert_contains "$harness_root/bin/claude-e2e-wrapper" "REAL_COMMAND_ARGS=( claude"
  assert_contains "$harness_root/claude-support/launch-1.summary.txt" "daemon_entry_stable=true"
  assert_contains "$harness_root/claude-support/launch-1.summary.txt" "tool_call_get_issue_execution=ok"
  assert_contains "$harness_root/claude-support/launch-1.summary.txt" "issue_identifier=CL-1"
  assert_contains "$harness_root/claude-support/launch-1.summary.txt" "live_claude_session_seen=true"
  assert_contains "$harness_root/claude-support/launch-3.args.txt" "-r"
  assert_contains "$harness_root/claude-support/launch-3.args.txt" "claude-session-2"
  assert_contains "$harness_root/claude-support/interrupt-final.summary.txt" "execution_failure_class=run_interrupted"
  assert_contains "$stdin_log" "runtime prompt"
  assert_contains "$tmp_dir/stdout.txt" "Running maestro verify preflight"
  assert_contains "$tmp_dir/stdout.txt" "Real Claude lifecycle e2e flow completed successfully."
  assert_contains "$tmp_dir/stdout.txt" "success: CL-1 -> $harness_root/artifacts/CL-1.success.txt"
  assert_contains "$tmp_dir/stdout.txt" "resume: CL-2 -> $harness_root/artifacts/CL-2.resume.txt"
  assert_contains "$tmp_dir/stdout.txt" "interrupt: CL-3 -> run_interrupted"
  assert_contains "$tmp_dir/stdout.txt" "claude evidence dir: $harness_root/claude-support"
  assert_contains "$tmp_dir/tool.log" "git init -q"
  assert_contains "$tmp_dir/tool.log" "git commit -q -m test init"
  assert_contains "$tmp_dir/probe.log" "probe --mcp-config"
  assert_contains "$tmp_dir/probe.log" "probe --mode final --issue-identifier CL-1"
  assert_contains "$tmp_dir/probe.log" "probe --mode final --issue-identifier CL-2"
  assert_contains "$tmp_dir/probe.log" "probe --mode final --issue-identifier CL-3"
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
  : >"$tmp_dir/probe.log"
  write_mock_toolchain "$bin_dir"

  if PATH="$bin_dir:$PATH" \
    MOCK_TOOL_LOG="$tmp_dir/tool.log" \
    FAKE_MAESTRO_LOG="$tmp_dir/maestro.log" \
    FAKE_PROBE_LOG="$tmp_dir/probe.log" \
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
  assert_contains "$tmp_dir/stderr.txt" "Daemon registry dir: $harness_root/.maestro-daemons"
  assert_contains "$tmp_dir/stderr.txt" "Claude evidence dir: $harness_root/claude-support"
  assert_contains "$tmp_dir/stderr.txt" "Verify log: $harness_root/verify.log"
  assert_contains "$tmp_dir/stderr.txt" "Orchestrator log: $harness_root/orchestrator.log"
  assert_contains "$tmp_dir/stderr.txt" "Workspaces root: $harness_root/workspaces"
  assert_contains "$tmp_dir/stderr.txt" "Issue: CL-1"
  assert_contains "$tmp_dir/stderr.txt" "Current state: ready"
  assert_contains "$tmp_dir/stderr.txt" "Claude evidence files:"
  assert_contains "$tmp_dir/stderr.txt" "Claude evidence summary:"
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
  : >"$tmp_dir/probe.log"
  write_mock_toolchain "$bin_dir"
  rm -f "$bin_dir/claude"

  PATH="$restricted_path" \
  MOCK_TOOL_LOG="$tmp_dir/tool.log" \
  FAKE_MAESTRO_LOG="$tmp_dir/maestro.log" \
  FAKE_PROBE_LOG="$tmp_dir/probe.log" \
  FAKE_STATE_DIR="$tmp_dir/state" \
  FAKE_HARNESS_ROOT="$harness_root" \
  E2E_ROOT="$harness_root" \
  E2E_KEEP_HARNESS=1 \
  E2E_CLAUDE_COMMAND="claude-wrapper --verbose" \
  bash "$SCRIPT_UNDER_TEST" >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"

  assert_contains "$harness_root/WORKFLOW.md" "command: '$harness_root/bin/claude-e2e-wrapper'"
  assert_contains "$harness_root/bin/claude-e2e-wrapper" "REAL_COMMAND_ARGS=( claude-wrapper --verbose"
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
  : >"$tmp_dir/probe.log"
  write_mock_toolchain "$bin_dir"
  rm -f "$bin_dir/claude"

  PATH="$restricted_path" \
  MOCK_TOOL_LOG="$tmp_dir/tool.log" \
  FAKE_MAESTRO_LOG="$tmp_dir/maestro.log" \
  FAKE_PROBE_LOG="$tmp_dir/probe.log" \
  FAKE_STATE_DIR="$tmp_dir/state" \
  FAKE_HARNESS_ROOT="$harness_root" \
  E2E_ROOT="$harness_root" \
  E2E_KEEP_HARNESS=1 \
  E2E_CLAUDE_COMMAND="CLAUDE_CONFIG_DIR=$tmp_dir/claude-config claude-wrapper --verbose" \
  bash "$SCRIPT_UNDER_TEST" >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"

  assert_contains "$harness_root/WORKFLOW.md" "command: '$harness_root/bin/claude-e2e-wrapper'"
  assert_contains "$harness_root/bin/claude-e2e-wrapper" "REAL_COMMAND_ENV=( CLAUDE_CONFIG_DIR=$tmp_dir/claude-config"
  assert_contains "$harness_root/bin/claude-e2e-wrapper" "REAL_COMMAND_ARGS=( claude-wrapper --verbose"
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
  : >"$tmp_dir/probe.log"
  write_mock_toolchain "$bin_dir"
  rm -f "$bin_dir/claude"

  PATH="$restricted_path" \
  MOCK_TOOL_LOG="$tmp_dir/tool.log" \
  FAKE_MAESTRO_LOG="$tmp_dir/maestro.log" \
  FAKE_PROBE_LOG="$tmp_dir/probe.log" \
  FAKE_STATE_DIR="$tmp_dir/state" \
  FAKE_HARNESS_ROOT="$harness_root" \
  E2E_ROOT="$harness_root" \
  E2E_KEEP_HARNESS=1 \
  E2E_CLAUDE_COMMAND="$override_command" \
  bash "$SCRIPT_UNDER_TEST" >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"

  [[ ! -e "$probe_path" ]] || fail "expected preflight validation to avoid executing command substitutions"
  assert_contains "$harness_root/WORKFLOW.md" "command: '$harness_root/bin/claude-e2e-wrapper'"
  assert_contains "$harness_root/bin/claude-e2e-wrapper" "REAL_COMMAND_ARGS=( claude-wrapper"
  assert_contains "$harness_root/bin/claude-e2e-wrapper" "$probe_path"
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
