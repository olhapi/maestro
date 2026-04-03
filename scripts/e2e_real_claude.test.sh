#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT_UNDER_TEST="$ROOT_DIR/scripts/e2e_real_claude.sh"
APPROVALS_SCRIPT_UNDER_TEST="$ROOT_DIR/scripts/e2e_real_claude_approvals.sh"

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
interrupt_classification=""
interrupt_tool_name=""
interrupt_decision=""
interrupt_note=""

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
    --interrupt-classification)
      interrupt_classification="$2"
      shift 2
      ;;
    --interrupt-tool-name)
      interrupt_tool_name="$2"
      shift 2
      ;;
    --interrupt-decision)
      interrupt_decision="$2"
      shift 2
      ;;
    --interrupt-note)
      interrupt_note="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

state_path() {
  printf '%s/%s.%s' "$FAKE_STATE_DIR" "$1" "$2"
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
    CL-4)
      printf 'claude-session-4'
      ;;
    *)
      printf 'claude-session-x'
      ;;
  esac
}

infer_issue_identifier() {
  local issue_three_title
  issue_three_title="$(read_state CL-3 title)"
  case "$(basename "$evidence_prefix")" in
    launch-1)
      printf 'CL-1'
      ;;
    launch-2)
      printf 'CL-2'
      ;;
    launch-3)
      if [[ "$issue_three_title" == "Claude approval edit timeout" ]]; then
        printf 'CL-3'
      else
        printf 'CL-2'
      fi
      ;;
    launch-4)
      if [[ "$issue_three_title" == "Claude approval edit timeout" ]]; then
        printf 'CL-4'
      else
        printf 'CL-3'
      fi
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
    command-final)
      printf 'CL-1'
      ;;
    write-final)
      printf 'CL-2'
      ;;
    edit-final)
      printf 'CL-3'
      ;;
    protected-final)
      printf 'CL-4'
      ;;
    *)
      printf 'CL-1'
      ;;
  esac
}

stream_marker_for_issue() {
  case "$(read_state "$1" title)" in
    "Claude approval command allow")
      printf 'STREAM:%s:command-approval' "$1"
      ;;
    "Claude approval write deny")
      printf 'STREAM:%s:file-write-approval' "$1"
      ;;
    "Claude approval edit timeout")
      printf 'STREAM:%s:file-edit-timeout' "$1"
      ;;
    "Claude approval protected write allow")
      printf 'STREAM:%s:protected-write-approval' "$1"
      ;;
    "Claude lifecycle success")
      printf 'STREAM:%s:success-live' "$1"
      ;;
    "Claude lifecycle resume")
      printf 'STREAM:%s:resume-live' "$1"
      ;;
    "Claude lifecycle interrupt")
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
interrupt_requested="false"
interrupt_pending_count="0"
interrupt_id=""
interrupt_kind=""
interrupt_source=""
interrupt_response_status=""
interrupt_cleared="false"
runtime_event_kinds=""

if [[ -n "$interrupt_classification" || -n "$interrupt_tool_name" || -n "$interrupt_decision" || -n "$interrupt_note" ]]; then
  interrupt_requested="true"
  interrupt_id="$(read_state "$issue_identifier" interrupt.id)"
  interrupt_kind="approval"
  interrupt_source="claude_permission_prompt"
  interrupt_pending_count="1"
  if [[ -n "$interrupt_decision" ]]; then
    printf '%s' "$interrupt_decision" >"$(state_path "$issue_identifier" interrupt.response_decision)"
    printf '%s' "$interrupt_note" >"$(state_path "$issue_identifier" interrupt.response_note)"
    interrupt_response_status="accepted"
    deadline=$((SECONDS + 5))
    while (( SECONDS < deadline )); do
      if [[ "$(read_state "$issue_identifier" interrupt.cleared)" == "1" ]]; then
        interrupt_cleared="true"
        break
      fi
      sleep 0.05
    done
  fi
fi

if [[ "$mode" == "final" ]]; then
  dashboard_source="persisted"
  execution_active="false"
  execution_session_source="persisted"
  case "$(read_state "$issue_identifier" title)" in
    "Claude approval command allow"|"Claude approval write deny"|"Claude approval protected write allow"|"Claude lifecycle success"|"Claude lifecycle resume")
      dashboard_status="completed"
      dashboard_stop_reason="end_turn"
      execution_stop_reason="end_turn"
      ;;
    "Claude approval edit timeout")
      dashboard_status="paused"
      dashboard_stop_reason="retry_limit_reached"
      execution_failure_class="retry_limit_reached"
      ;;
    "Claude lifecycle interrupt")
      dashboard_status="interrupted"
      dashboard_stop_reason="run_interrupted"
      execution_failure_class="run_interrupted"
      execution_stop_reason="run_interrupted"
      ;;
  esac
fi

for path in "$FAKE_STATE_DIR/$issue_identifier.event."*; do
  [[ -e "$path" ]] || continue
  kind="${path##*.event.}"
  if [[ -z "$runtime_event_kinds" ]]; then
    runtime_event_kinds="$kind"
  else
    runtime_event_kinds="$runtime_event_kinds,$kind"
  fi
done

mkdir -p "$(dirname "$evidence_prefix")"
cp "$mcp_config" "$evidence_prefix.mcp.json"
cp "$settings_path" "$evidence_prefix.settings.json"
cat >"$evidence_prefix.summary.txt" <<PROBE_SUMMARY
expected_tools_present=true
tool_call_get_issue_execution=ok
tool_call_server_info=ok
tool_call_list_issues=ok
tool_call_get_runtime_snapshot=ok
tool_call_list_runtime_events=ok
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
expected_tools_present=true
interrupt_cleared=$interrupt_cleared
interrupt_classification=$interrupt_classification
interrupt_id=$interrupt_id
interrupt_kind=$interrupt_kind
interrupt_pending_count=$interrupt_pending_count
interrupt_requested=$interrupt_requested
interrupt_response_decision=$interrupt_decision
interrupt_response_status=$interrupt_response_status
interrupt_source=$interrupt_source
interrupt_tool_name=$interrupt_tool_name
issue_identifier=$issue_identifier
mode=$mode
strict_mcp_config=$strict_mcp_config
permission_mode=$permission_mode
allowed_tools=$allowed_tools
permission_prompt_tool=${permission_prompt_tool:-<none>}
runtime_event_count=$(printf '%s\n' "$runtime_event_kinds" | tr ',' '\n' | sed '/^$/d' | wc -l | tr -d ' ')
runtime_event_kinds=$runtime_event_kinds
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

issue_title_value() {
  read_state "$1" title
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
    CL-4)
      printf 'claude-session-4'
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

issue_permission_profile() {
  local value
  value="$(read_state "$1" permission_profile)"
  if [[ -n "$value" ]]; then
    printf '%s' "$value"
    return 0
  fi
  printf 'default'
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

write_event_error() {
  local issue_id="$1"
  local kind="$2"
  local error_text="$3"
  write_state "$issue_id" "event_error.$kind" "$error_text"
}

approval_interrupt_id() {
  printf '%s-approval-1' "$1"
}

approval_workspace_dir() {
  printf '%s/workspaces/real-claude-approval-e2e-project/%s' "$FAKE_HARNESS_ROOT" "$1"
}

ensure_approval_workspace() {
  local issue_id="$1"
  local workspace_dir
  workspace_dir="$(approval_workspace_dir "$issue_id")"
  mkdir -p "$workspace_dir/.git"
  printf '%s\n' '# Maestro E2E Workspace' >"$workspace_dir/README.md"
  printf '%s\n' 'before' >"$workspace_dir/approval-edit-target.txt"
}

write_interrupt_state() {
  local issue_id="$1"
  local classification="$2"
  local tool_name="$3"
  write_state "$issue_id" interrupt.id "$(approval_interrupt_id "$issue_id")"
  write_state "$issue_id" interrupt.classification "$classification"
  write_state "$issue_id" interrupt.tool_name "$tool_name"
}

clear_interrupt_state() {
  local issue_id="$1"
  write_state "$issue_id" interrupt.cleared 1
}

wait_for_interrupt_response() {
  local issue_id="$1"
  local timeout_seconds="${2:-5}"
  local deadline
  deadline=$((SECONDS + timeout_seconds))
  while (( SECONDS < deadline )); do
    local decision
    decision="$(read_state "$issue_id" interrupt.response_decision)"
    if [[ -n "$decision" ]]; then
      printf '%s' "$decision"
      return 0
    fi
    sleep 0.05
  done
  return 1
}

invoke_workflow() {
  local issue_id="$1"
  local workflow_command="$2"
  local settings_path="$3"
  local mcp_config_path="$4"
  local resume_token="${5:-}"
  local permission_profile permission_flags
  if [[ -z "$workflow_command" ]]; then
    return 0
  fi
  permission_profile="$(issue_permission_profile "$issue_id")"
  if [[ "$permission_profile" == "full-access" ]]; then
    permission_flags="--allowed-tools 'Bash,Edit,Write,MultiEdit'"
  else
    permission_flags="--permission-prompt-tool 'mcp__maestro__approval_prompt'"
  fi
  if [[ -n "$resume_token" ]]; then
    PATH="$PATH" bash -c "printf 'runtime prompt\n' | $workflow_command -r '$resume_token' -p --verbose --output-format=stream-json --include-partial-messages --permission-mode default --settings '$settings_path' $permission_flags --mcp-config '$mcp_config_path' --strict-mcp-config"
    return 0
  fi
  PATH="$PATH" bash -c "printf 'runtime prompt\n' | $workflow_command -p --verbose --output-format=stream-json --include-partial-messages --permission-mode default --settings '$settings_path' $permission_flags --mcp-config '$mcp_config_path' --strict-mcp-config"
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
  invoke_workflow "$issue_id" "$workflow_command" "$settings_path" "$mcp_config_path"
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
    invoke_workflow "$issue_id" "$workflow_command" "$settings_path" "$mcp_config_path" "$session_id"
    wait_for_gate "$gate_path"
    mkdir -p "$(dirname "$artifact_path")"
    printf 'maestro claude resume e2e ok\n' >"$artifact_path"
    set_snapshot "$issue_id" run_completed end_turn "" "$session_id"
    write_state "$issue_id" state done
    return 0
  fi
  trap "printf '1' >\"$interrupted_marker\"; increment_event \"$issue_id\" run_interrupted; increment_event \"$issue_id\" retry_scheduled; set_snapshot \"$issue_id\" run_interrupted run_interrupted run_interrupted \"$session_id\"; exit 0" TERM INT
  invoke_workflow "$issue_id" "$workflow_command" "$settings_path" "$mcp_config_path"
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
  invoke_workflow "$issue_id" "$workflow_command" "$settings_path" "$mcp_config_path"
  while [[ ! -f "$stop_requested_path" ]]; do
    sleep 0.05
  done
  increment_event "$issue_id" run_stopped
  increment_event "$issue_id" run_interrupted
  set_snapshot "$issue_id" run_interrupted run_interrupted run_interrupted "$session_id"
}

run_command_approval_scenario() {
  local workflow_command="$1"
  local settings_path="$2"
  local mcp_config_path="$3"
  local issue_id="CL-1"
  local session_id workspace_dir artifact_path decision
  session_id="$(session_id_for_issue "$issue_id")"
  wait_for_issue_state "$issue_id" ready
  ensure_approval_workspace "$issue_id"
  workspace_dir="$(approval_workspace_dir "$issue_id")"
  artifact_path="$workspace_dir/command-approval.txt"
  invoke_workflow "$issue_id" "$workflow_command" "$settings_path" "$mcp_config_path"
  write_interrupt_state "$issue_id" command Bash
  decision="$(wait_for_interrupt_response "$issue_id" 5)"
  if [[ "$decision" == "allow" ]]; then
    printf '%s\n' 'maestro claude command approval ok' >"$artifact_path"
  fi
  clear_interrupt_state "$issue_id"
  increment_event "$issue_id" run_completed
  set_snapshot "$issue_id" run_completed end_turn "" "$session_id"
  write_state "$issue_id" state done
}

run_write_approval_scenario() {
  local workflow_command="$1"
  local settings_path="$2"
  local mcp_config_path="$3"
  local issue_id="CL-2"
  local session_id workspace_dir artifact_path decision
  session_id="$(session_id_for_issue "$issue_id")"
  wait_for_issue_state "$issue_id" ready
  ensure_approval_workspace "$issue_id"
  workspace_dir="$(approval_workspace_dir "$issue_id")"
  artifact_path="$workspace_dir/write-denied.txt"
  invoke_workflow "$issue_id" "$workflow_command" "$settings_path" "$mcp_config_path"
  write_interrupt_state "$issue_id" file_write Write
  decision="$(wait_for_interrupt_response "$issue_id" 5)"
  if [[ "$decision" == "allow" ]]; then
    printf '%s\n' 'maestro claude write approval ok' >"$artifact_path"
  else
    rm -f "$artifact_path"
  fi
  clear_interrupt_state "$issue_id"
  increment_event "$issue_id" run_completed
  set_snapshot "$issue_id" run_completed end_turn "" "$session_id"
  write_state "$issue_id" state done
}

run_edit_timeout_scenario() {
  local workflow_command="$1"
  local settings_path="$2"
  local mcp_config_path="$3"
  local issue_id="CL-3"
  local session_id decision
  session_id="$(session_id_for_issue "$issue_id")"
  wait_for_issue_state "$issue_id" ready
  ensure_approval_workspace "$issue_id"
  invoke_workflow "$issue_id" "$workflow_command" "$settings_path" "$mcp_config_path"
  write_interrupt_state "$issue_id" file_edit Edit
  if decision="$(wait_for_interrupt_response "$issue_id" 1)"; then
    if [[ "$decision" == "allow" ]]; then
      printf '%s\n' 'after' >"$(approval_workspace_dir "$issue_id")/approval-edit-target.txt"
      clear_interrupt_state "$issue_id"
      increment_event "$issue_id" run_completed
      set_snapshot "$issue_id" run_completed end_turn "" "$session_id"
      write_state "$issue_id" state done
      return 0
    fi
  fi
  increment_event "$issue_id" run_failed
  write_event_error "$issue_id" run_failed turn_timeout
  set_snapshot "$issue_id" retry_paused retry_limit_reached turn_timeout "$session_id"
  write_state "$issue_id" state in_progress
}

run_protected_write_approval_scenario() {
  local workflow_command="$1"
  local settings_path="$2"
  local mcp_config_path="$3"
  local issue_id="CL-4"
  local session_id workspace_dir artifact_path decision
  session_id="$(session_id_for_issue "$issue_id")"
  wait_for_issue_state "$issue_id" ready
  ensure_approval_workspace "$issue_id"
  workspace_dir="$(approval_workspace_dir "$issue_id")"
  artifact_path="$workspace_dir/.git/maestro-protected.txt"
  invoke_workflow "$issue_id" "$workflow_command" "$settings_path" "$mcp_config_path"
  write_interrupt_state "$issue_id" protected_directory_write Write
  decision="$(wait_for_interrupt_response "$issue_id" 5)"
  if [[ "$decision" == "allow" ]]; then
    printf '%s\n' 'maestro claude protected approval ok' >"$artifact_path"
  fi
  clear_interrupt_state "$issue_id"
  increment_event "$issue_id" run_completed
  set_snapshot "$issue_id" run_completed end_turn "" "$session_id"
  write_state "$issue_id" state done
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
    if [[ "$(issue_title_value CL-1)" == "Claude approval command allow" ]]; then
      run_command_approval_scenario "$workflow_command" "$settings_path" "$mcp_config_path"
      run_write_approval_scenario "$workflow_command" "$settings_path" "$mcp_config_path"
      run_edit_timeout_scenario "$workflow_command" "$settings_path" "$mcp_config_path"
      run_protected_write_approval_scenario "$workflow_command" "$settings_path" "$mcp_config_path"
      printf 'mock orchestrator completed approval scenarios\n'
    else
      run_success_scenario "$workflow_command" "$settings_path" "$mcp_config_path"
      run_resume_scenario "$workflow_command" "$settings_path" "$mcp_config_path"
      run_interrupt_scenario "$workflow_command" "$settings_path" "$mcp_config_path"
      printf 'mock orchestrator completed lifecycle scenarios\n'
    fi
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

state_path() {
  printf '%s/%s.%s' "$FAKE_STATE_DIR" "$1" "$2"
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
  if [[ "$query" == *"SELECT COALESCE(error, '') FROM runtime_events"* ]]; then
    if [[ -f "$(state_path "$identifier" "event_error.$kind")" ]]; then
      cat "$(state_path "$identifier" "event_error.$kind")"
    fi
    exit 0
  fi
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

test_approval_run_covers_each_supported_claude_approval_class() {
  local tmp_dir bin_dir harness_root approval_workspace_root
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-real-claude-test-approvals.XXXXXX")"
  bin_dir="$tmp_dir/bin"
  harness_root="$tmp_dir/harness"
  approval_workspace_root="$harness_root/workspaces/real-claude-approval-e2e-project"

  mkdir -p "$tmp_dir/state"
  : >"$tmp_dir/tool.log"
  : >"$tmp_dir/maestro.log"
  : >"$tmp_dir/probe.log"
  write_mock_toolchain "$bin_dir"

  PATH="$bin_dir:$PATH" \
  MOCK_TOOL_LOG="$tmp_dir/tool.log" \
  FAKE_MAESTRO_LOG="$tmp_dir/maestro.log" \
  FAKE_PROBE_LOG="$tmp_dir/probe.log" \
  FAKE_STATE_DIR="$tmp_dir/state" \
  FAKE_HARNESS_ROOT="$harness_root" \
  E2E_ROOT="$harness_root" \
  E2E_KEEP_HARNESS=1 \
  bash "$APPROVALS_SCRIPT_UNDER_TEST" >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"

  assert_exists "$harness_root/claude-support/launch-1.summary.txt"
  assert_exists "$harness_root/claude-support/launch-2.summary.txt"
  assert_exists "$harness_root/claude-support/launch-3.summary.txt"
  assert_exists "$harness_root/claude-support/launch-4.summary.txt"
  assert_exists "$harness_root/claude-support/command-live.summary.txt"
  assert_exists "$harness_root/claude-support/write-live.summary.txt"
  assert_exists "$harness_root/claude-support/edit-live.summary.txt"
  assert_exists "$harness_root/claude-support/protected-live.summary.txt"
  assert_exists "$harness_root/claude-support/command-final.summary.txt"
  assert_exists "$harness_root/claude-support/write-final.summary.txt"
  assert_exists "$harness_root/claude-support/edit-final.summary.txt"
  assert_exists "$harness_root/claude-support/protected-final.summary.txt"

  assert_contains "$harness_root/claude-support/launch-1.summary.txt" "permission_prompt_tool=mcp__maestro__approval_prompt"
  assert_contains "$harness_root/claude-support/launch-1.summary.txt" "allowed_tools="
  assert_contains "$harness_root/claude-support/launch-1.args.txt" "--permission-prompt-tool"
  assert_contains "$harness_root/claude-support/launch-1.args.txt" "mcp__maestro__approval_prompt"
  if grep -Fq -- "--allowed-tools" "$harness_root/claude-support/launch-1.args.txt"; then
    fail "expected approval launch to route through the Maestro permission prompt instead of allowed-tools"
  fi

  assert_contains "$harness_root/claude-support/command-live.summary.txt" "interrupt_classification=command"
  assert_contains "$harness_root/claude-support/command-live.summary.txt" "interrupt_tool_name=Bash"
  assert_contains "$harness_root/claude-support/command-live.summary.txt" "interrupt_response_decision=allow"
  assert_contains "$harness_root/claude-support/command-live.summary.txt" "interrupt_response_status=accepted"
  assert_contains "$harness_root/claude-support/write-live.summary.txt" "interrupt_classification=file_write"
  assert_contains "$harness_root/claude-support/write-live.summary.txt" "interrupt_tool_name=Write"
  assert_contains "$harness_root/claude-support/write-live.summary.txt" "interrupt_response_decision=deny"
  assert_contains "$harness_root/claude-support/write-live.summary.txt" "interrupt_response_status=accepted"
  assert_contains "$harness_root/claude-support/edit-live.summary.txt" "interrupt_classification=file_edit"
  assert_contains "$harness_root/claude-support/edit-live.summary.txt" "interrupt_tool_name=Edit"
  assert_contains "$harness_root/claude-support/edit-live.summary.txt" "interrupt_response_status="
  assert_contains "$harness_root/claude-support/edit-live.summary.txt" "interrupt_cleared=false"
  assert_contains "$harness_root/claude-support/protected-live.summary.txt" "interrupt_classification=protected_directory_write"
  assert_contains "$harness_root/claude-support/protected-live.summary.txt" "interrupt_tool_name=Write"
  assert_contains "$harness_root/claude-support/protected-live.summary.txt" "interrupt_response_decision=allow"
  assert_contains "$harness_root/claude-support/protected-live.summary.txt" "interrupt_response_status=accepted"

  assert_contains "$harness_root/claude-support/command-final.summary.txt" "dashboard_session_status=completed"
  assert_contains "$harness_root/claude-support/command-final.summary.txt" "runtime_event_kinds=run_completed"
  assert_contains "$harness_root/claude-support/write-final.summary.txt" "dashboard_session_status=completed"
  assert_contains "$harness_root/claude-support/write-final.summary.txt" "runtime_event_kinds=run_completed"
  assert_contains "$harness_root/claude-support/edit-final.summary.txt" "dashboard_session_status=paused"
  assert_contains "$harness_root/claude-support/edit-final.summary.txt" "dashboard_session_stop_reason=retry_limit_reached"
  assert_contains "$harness_root/claude-support/edit-final.summary.txt" "runtime_event_kinds=run_failed"
  assert_contains "$harness_root/claude-support/protected-final.summary.txt" "dashboard_session_status=completed"
  assert_contains "$harness_root/claude-support/protected-final.summary.txt" "runtime_event_kinds=run_completed"

  assert_exists "$approval_workspace_root/CL-1/command-approval.txt"
  [[ "$(cat "$approval_workspace_root/CL-1/command-approval.txt")" == "maestro claude command approval ok" ]] || fail "expected command approval artifact content"
  [[ ! -e "$approval_workspace_root/CL-2/write-denied.txt" ]] || fail "expected denied write artifact to stay absent"
  [[ "$(cat "$approval_workspace_root/CL-3/approval-edit-target.txt")" == "before" ]] || fail "expected timed out edit target to remain unchanged"
  assert_exists "$approval_workspace_root/CL-4/.git/maestro-protected.txt"
  [[ "$(cat "$approval_workspace_root/CL-4/.git/maestro-protected.txt")" == "maestro claude protected approval ok" ]] || fail "expected protected approval artifact content"

  assert_contains "$tmp_dir/stdout.txt" "Real Claude approval bridge e2e flow completed successfully."
  assert_contains "$tmp_dir/stdout.txt" "command allow: CL-1 -> $approval_workspace_root/CL-1/command-approval.txt"
  assert_contains "$tmp_dir/stdout.txt" "write deny: CL-2 -> denied without file creation"
  assert_contains "$tmp_dir/stdout.txt" "edit timeout: CL-3 -> timeout recorded"
  assert_contains "$tmp_dir/stdout.txt" "protected allow: CL-4 -> $approval_workspace_root/CL-4/.git/maestro-protected.txt"
  assert_contains "$tmp_dir/probe.log" "probe --mode live --issue-identifier CL-1"
  assert_contains "$tmp_dir/probe.log" "probe --mode live --issue-identifier CL-2"
  assert_contains "$tmp_dir/probe.log" "probe --mode live --issue-identifier CL-3"
  assert_contains "$tmp_dir/probe.log" "probe --mode live --issue-identifier CL-4"
  assert_contains "$tmp_dir/probe.log" "--interrupt-decision deny"
  assert_contains "$tmp_dir/probe.log" "probe --mode final --issue-identifier CL-4"
  assert_in_order "$tmp_dir/maestro.log" "maestro --json verify" "maestro project create"
  [[ ! -s "$tmp_dir/stderr.txt" ]] || fail "expected approval run to avoid stderr output"
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
  test_approval_run_covers_each_supported_claude_approval_class
  test_timeout_failure_prints_issue_and_path_diagnostics
  test_override_command_is_used_for_preflight_requirement
  test_override_command_with_env_assignment_is_used_for_preflight_requirement
  test_override_command_validation_does_not_execute_command_substitutions
}

main "$@"
