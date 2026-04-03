#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=./lib/e2e_real_claude_harness.sh
source "$ROOT_DIR/scripts/lib/e2e_real_claude_harness.sh"

trap cleanup EXIT INT TERM

CLAUDE_COMMAND="${E2E_CLAUDE_COMMAND:-claude}"
PROJECT_NAME="Real Claude Approval E2E Project"
PROJECT_WORKSPACE_SLUG="real-claude-approval-e2e-project"
ALERT_PROJECT_NAME="Real Claude Alert E2E Project"
ALERT_REPO_PATH="${HARNESS_ROOT}.external-alert-repo"

command_stream_marker() {
  printf 'STREAM:%s:command-approval' "$1"
}

write_stream_marker() {
  printf 'STREAM:%s:file-write-approval' "$1"
}

edit_stream_marker() {
  printf 'STREAM:%s:file-edit-timeout' "$1"
}

protected_stream_marker() {
  printf 'STREAM:%s:protected-write-approval' "$1"
}

alert_ack_stream_marker() {
  printf 'STREAM:%s:dispatch-alert' "$1"
}

issue_workspace_path() {
  printf '%s/%s/%s' "$WORKSPACES_DIR" "$PROJECT_WORKSPACE_SLUG" "$1"
}

command_artifact_path() {
  printf '%s/command-approval.txt' "$(issue_workspace_path "$1")"
}

write_artifact_path() {
  printf '%s/write-denied.txt' "$(issue_workspace_path "$1")"
}

edit_target_path() {
  printf '%s/approval-edit-target.txt' "$(issue_workspace_path "$1")"
}

protected_artifact_path() {
  printf '%s/.git/maestro-protected.txt' "$(issue_workspace_path "$1")"
}

command_artifact_text() {
  printf 'maestro claude command approval ok'
}

write_artifact_text() {
  printf 'maestro claude write approval ok'
}

protected_artifact_text() {
  printf 'maestro claude protected approval ok'
}

sql_value() {
  local sql="$1"
  sqlite3 -noheader "$DB_PATH" ".timeout 5000" "$sql"
}

issue_execution_value() {
  local identifier="$1"
  local column="$2"
  sql_value "SELECT COALESCE($column, '') FROM issue_execution_sessions WHERE identifier = '$identifier' LIMIT 1;"
}

runtime_event_count() {
  local identifier="$1"
  local kind="$2"
  sql_value "SELECT COUNT(*) FROM runtime_events WHERE identifier = '$identifier' AND kind = '$kind';"
}

latest_runtime_event_error() {
  local identifier="$1"
  local kind="$2"
  sql_value "SELECT COALESCE(error, '') FROM runtime_events WHERE identifier = '$identifier' AND kind = '$kind' ORDER BY seq DESC LIMIT 1;"
}

wait_for_runtime_event() {
  local identifier="$1"
  local kind="$2"
  local deadline
  deadline=$((SECONDS + TIMEOUT_SEC))
  while (( SECONDS < deadline )); do
    if [[ "$(runtime_event_count "$identifier" "$kind")" -ge 1 ]]; then
      return 0
    fi
    sleep "$POLL_SEC"
  done
  return 1
}

wait_for_issue_execution_run_kind() {
  local identifier="$1"
  local expected="$2"
  local deadline
  deadline=$((SECONDS + TIMEOUT_SEC))
  while (( SECONDS < deadline )); do
    if [[ "$(issue_execution_value "$identifier" "run_kind")" == "$expected" ]]; then
      return 0
    fi
    sleep "$POLL_SEC"
  done
  return 1
}

wait_for_issue_state() {
  local identifier="$1"
  local expected_state="$2"
  local deadline
  deadline=$((SECONDS + TIMEOUT_SEC))
  while (( SECONDS < deadline )); do
    if [[ "$(issue_state "$identifier")" == "$expected_state" ]]; then
      return 0
    fi
    sleep "$POLL_SEC"
  done
  return 1
}

wait_for_evidence_line() {
  local path="$1"
  local expected="$2"
  local deadline
  deadline=$((SECONDS + TIMEOUT_SEC))
  while (( SECONDS < deadline )); do
    if [[ -f "$path" ]] && grep -Fqx -- "$expected" "$path"; then
      return 0
    fi
    sleep "$POLL_SEC"
  done
  return 1
}

launch_prefix() {
  printf '%s/launch-%s' "$CLAUDE_EVIDENCE_DIR" "$1"
}

run_interrupt_probe() {
  local issue_id="$1"
  local launch_number="$2"
  local prefix="$3"
  local classification="$4"
  local tool_name="$5"
  local decision="${6:-}"

  local -a args=(
    --mode live
    --issue-identifier "$issue_id"
    --mcp-config "$(launch_prefix "$launch_number").mcp.json"
    --settings "$(launch_prefix "$launch_number").settings.json"
    --db "$DB_PATH"
    --registry-dir "$DAEMON_REGISTRY_DIR"
    --evidence-prefix "$CLAUDE_EVIDENCE_DIR/$prefix"
    --permission-prompt-tool "mcp__maestro__approval_prompt"
    --permission-mode default
    --strict-mcp-config true
    --interrupt-classification "$classification"
    --interrupt-tool-name "$tool_name"
  )
  if [[ -n "$decision" ]]; then
    args+=(--interrupt-decision "$decision")
  fi
  "$CLAUDE_PROBE_BIN" "${args[@]}"
}

run_alert_probe() {
  local issue_id="$1"
  local launch_number="$2"
  local prefix="$3"
  "$CLAUDE_PROBE_BIN" \
    --mode interrupt \
    --issue-identifier "$issue_id" \
    --mcp-config "$(launch_prefix "$launch_number").mcp.json" \
    --settings "$(launch_prefix "$launch_number").settings.json" \
    --db "$DB_PATH" \
    --registry-dir "$DAEMON_REGISTRY_DIR" \
    --evidence-prefix "$CLAUDE_EVIDENCE_DIR/$prefix" \
    --permission-prompt-tool "mcp__maestro__approval_prompt" \
    --permission-mode default \
    --strict-mcp-config true \
    --interrupt-kind alert \
    --interrupt-action acknowledge \
    --interrupt-alert-code project_dispatch_blocked
}

run_final_probe() {
  local issue_id="$1"
  local launch_number="$2"
  local prefix="$3"
  "$CLAUDE_PROBE_BIN" \
    --mode final \
    --issue-identifier "$issue_id" \
    --mcp-config "$(launch_prefix "$launch_number").mcp.json" \
    --settings "$(launch_prefix "$launch_number").settings.json" \
    --db "$DB_PATH" \
    --registry-dir "$DAEMON_REGISTRY_DIR" \
    --evidence-prefix "$CLAUDE_EVIDENCE_DIR/$prefix" \
    --permission-prompt-tool "mcp__maestro__approval_prompt" \
    --permission-mode default \
    --strict-mcp-config true
}

assert_claude_runtime_surface() {
  local path="$1"
  assert_evidence_line "$path" "dashboard_session_runtime_name=claude"
  assert_evidence_line "$path" "dashboard_session_runtime_provider=claude"
  assert_evidence_line "$path" "dashboard_session_runtime_transport=stdio"
  assert_claude_runtime_auth_source_line "$path" "dashboard_session_runtime_auth_source"
  assert_evidence_line "$path" "execution_runtime_name=claude"
  assert_evidence_line "$path" "execution_runtime_provider=claude"
  assert_evidence_line "$path" "execution_runtime_transport=stdio"
  assert_claude_runtime_auth_source_line "$path" "execution_runtime_auth_source"
}

assert_permission_prompt_launch_summary() {
  local path="$1"
  local issue_id="$2"
  assert_evidence_line "$path" "issue_identifier=$issue_id"
  assert_evidence_line "$path" "execution_stream_seen=true"
  assert_evidence_line "$path" "permission_mode=default"
  assert_evidence_line "$path" "permission_prompt_tool=mcp__maestro__approval_prompt"
  assert_evidence_line "$path" "allowed_tools="
  assert_evidence_line "$path" "strict_mcp_config=true"
  assert_evidence_line "$path" "tool_call_list_runtime_events=ok"
  assert_evidence_line "$path" "dashboard_session_source=live"
  assert_evidence_line "$path" "execution_session_source=live"
  assert_claude_runtime_surface "$path"
}

assert_interrupt_summary() {
  local path="$1"
  local classification="$2"
  local tool_name="$3"
  local decision="$4"
  local response_status="$5"
  local cleared="$6"
  assert_evidence_line "$path" "interrupt_requested=true"
  assert_evidence_line "$path" "interrupt_kind=approval"
  assert_evidence_line "$path" "interrupt_pending_count=1"
  assert_evidence_line "$path" "interrupt_source=claude_permission_prompt"
  assert_evidence_line "$path" "interrupt_classification=$classification"
  assert_evidence_line "$path" "interrupt_tool_name=$tool_name"
  assert_evidence_line "$path" "interrupt_response_decision=$decision"
  assert_evidence_line "$path" "interrupt_response_status=$response_status"
  assert_evidence_line "$path" "interrupt_cleared=$cleared"
}

assert_approval_surface_summary() {
  local path="$1"
  assert_claude_runtime_surface "$path"
  assert_evidence_line "$path" "dashboard_session_status=waiting"
  assert_evidence_line "$path" "dashboard_session_pending_interaction_state=approval"
  assert_evidence_line "$path" "execution_pending_interaction_state=approval"
}

assert_alert_summary() {
  local path="$1"
  assert_evidence_line "$path" "interrupt_requested=true"
  assert_evidence_line "$path" "interrupt_kind=alert"
  assert_evidence_line "$path" "interrupt_action=acknowledge"
  assert_evidence_line "$path" "interrupt_alert_code=project_dispatch_blocked"
  assert_evidence_line "$path" "interrupt_response_status=accepted"
  assert_evidence_line "$path" "interrupt_cleared=true"
}

assert_missing_path() {
  local path="$1"
  if [[ -e "$path" ]]; then
    echo "did not expect path to exist: $path" >&2
    return 1
  fi
}

COMMAND_ISSUE=""
WRITE_ISSUE=""
EDIT_ISSUE=""
PROTECTED_ISSUE=""
ALERT_ISSUE=""

cd "$ROOT_DIR"

require_cmd go
require_command_string "Claude" "$CLAUDE_COMMAND"
require_cmd git
require_cmd sqlite3

ensure_harness_dirs
build_maestro
build_claude_probe
prepare_claude_command_wrapper "$CLAUDE_COMMAND"
export PATH="$BIN_DIR:$PATH"
export MAESTRO_DAEMON_REGISTRY_DIR="$DAEMON_REGISTRY_DIR"

CLAUDE_WORKFLOW_COMMAND="$(yaml_quote "$CLAUDE_WRAPPER_BIN")"

cat >"$WORKFLOW_PATH" <<EOF
---
tracker:
  kind: kanban
  active_states:
    - ready
    - in_progress
  terminal_states:
    - done
    - cancelled
polling:
  interval_ms: 1000
workspace:
  root: $WORKSPACES_DIR
hooks:
  after_create: |
    git init -q
    git config user.name "Maestro E2E"
    git config user.email "e2e@example.com"
    printf '%s\n' '# Maestro E2E Workspace' > README.md
    printf '%s\n' 'before' > approval-edit-target.txt
  timeout_ms: 10000
phases:
  review:
    enabled: false
  done:
    enabled: false
orchestrator:
  max_concurrent_agents: 1
  max_turns: 1
  max_automatic_retries: 0
  max_retry_backoff_ms: 5000
  dispatch_mode: parallel
runtime:
  default: claude
  claude:
    provider: claude
    transport: stdio
    command: '$CLAUDE_WORKFLOW_COMMAND'
    approval_policy: never
    turn_timeout_ms: 30000
    read_timeout_ms: 5000
---
You are running the Maestro real-Claude approval bridge end-to-end harness.

Complete only the current issue and then stop.

Issue identifier: {{ issue.identifier }}
Issue title: {{ issue.title }}
Issue description:
{{ issue.description }}

Environment:
- Current directory is an isolated issue workspace.
- Use Maestro MCP tools for state transitions.
- Do not use built-in tools other than the single built-in tool explicitly required by the issue description.
EOF

init_harness_repo "$HARNESS_ROOT"
run_claude_verify

PROJECT_ID="$("$MAESTRO_BIN" project create "$PROJECT_NAME" --repo "$HARNESS_ROOT" --db "$DB_PATH" --quiet)"
start_project "$PROJECT_ID"
mkdir -p "$ALERT_REPO_PATH"
(
  cd "$ALERT_REPO_PATH"
  git init -q
)
ALERT_PROJECT_ID="$("$MAESTRO_BIN" project create "$ALERT_PROJECT_NAME" --repo "$ALERT_REPO_PATH" --db "$DB_PATH" --quiet)"
start_project "$ALERT_PROJECT_ID"

echo "Creating Claude approval e2e issues in $DB_PATH"
COMMAND_ISSUE="$("$MAESTRO_BIN" issue create "Claude approval command allow" --project "$PROJECT_ID" --desc "placeholder" --db "$DB_PATH" --quiet)"
WRITE_ISSUE="$("$MAESTRO_BIN" issue create "Claude approval write deny" --project "$PROJECT_ID" --desc "placeholder" --db "$DB_PATH" --quiet)"
EDIT_ISSUE="$("$MAESTRO_BIN" issue create "Claude approval edit timeout" --project "$PROJECT_ID" --desc "placeholder" --db "$DB_PATH" --quiet)"
PROTECTED_ISSUE="$("$MAESTRO_BIN" issue create "Claude approval protected write allow" --project "$PROJECT_ID" --desc "placeholder" --db "$DB_PATH" --quiet)"
ALERT_ISSUE="$("$MAESTRO_BIN" issue create "Claude shared alert acknowledgement" --project "$ALERT_PROJECT_ID" --desc "placeholder" --db "$DB_PATH" --quiet)"

"$MAESTRO_BIN" issue update "$COMMAND_ISSUE" --desc "$(cat <<EOF
Stream marker: $(command_stream_marker "$COMMAND_ISSUE")
Target file: command-approval.txt
Target text: $(command_artifact_text)

Requirements:
1. Immediately emit exactly: $(command_stream_marker "$COMMAND_ISSUE")
2. Use exactly one Bash tool call with this exact command:
   printf '%s\n' '$(command_artifact_text)' > command-approval.txt
3. Do not use any other built-in tools.
4. After the Bash call succeeds, move the issue to done with the Maestro MCP \`set_issue_state\` tool.
5. Stop after the issue is marked done.
EOF
)" --db "$DB_PATH" >/dev/null

"$MAESTRO_BIN" issue update "$WRITE_ISSUE" --desc "$(cat <<EOF
Stream marker: $(write_stream_marker "$WRITE_ISSUE")
Target file: write-denied.txt
Target text: $(write_artifact_text)

Requirements:
1. Immediately emit exactly: $(write_stream_marker "$WRITE_ISSUE")
2. Use exactly one Write tool call to create write-denied.txt with the target text followed by one trailing newline.
3. Do not use any other built-in tools.
4. If the Write request is denied, do not retry and move the issue to done with the Maestro MCP \`set_issue_state\` tool.
5. If the Write request succeeds, also move the issue to done.
6. Stop after the issue is marked done.
EOF
)" --db "$DB_PATH" >/dev/null

"$MAESTRO_BIN" issue update "$EDIT_ISSUE" --desc "$(cat <<EOF
Stream marker: $(edit_stream_marker "$EDIT_ISSUE")
Target file: approval-edit-target.txt currently contains exactly: before

Requirements:
1. Immediately emit exactly: $(edit_stream_marker "$EDIT_ISSUE")
2. Use exactly one Edit tool call to replace the full line \`before\` with \`after\` in approval-edit-target.txt.
3. Do not use any other built-in tools.
4. If the Edit request stays pending, keep waiting for operator input and do not mark the issue done.
EOF
)" --db "$DB_PATH" >/dev/null

"$MAESTRO_BIN" issue update "$PROTECTED_ISSUE" --desc "$(cat <<EOF
Stream marker: $(protected_stream_marker "$PROTECTED_ISSUE")
Target file: .git/maestro-protected.txt
Target text: $(protected_artifact_text)

Requirements:
1. Immediately emit exactly: $(protected_stream_marker "$PROTECTED_ISSUE")
2. Use exactly one Write tool call to create .git/maestro-protected.txt with the target text followed by one trailing newline.
3. Do not use any other built-in tools.
4. After the Write call succeeds, move the issue to done with the Maestro MCP \`set_issue_state\` tool.
5. Stop after the issue is marked done.
EOF
)" --db "$DB_PATH" >/dev/null

"$MAESTRO_BIN" issue update "$ALERT_ISSUE" --desc "$(cat <<EOF
Stream marker: $(alert_ack_stream_marker "$ALERT_ISSUE")

Requirements:
1. Immediately emit exactly: $(alert_ack_stream_marker "$ALERT_ISSUE")
2. Wait for operator acknowledgement before continuing.
3. Do not change the issue state.
EOF
)" --db "$DB_PATH" >/dev/null

start_orchestrator

echo "Running command approval scenario for $COMMAND_ISSUE"
"$MAESTRO_BIN" issue move "$COMMAND_ISSUE" ready --db "$DB_PATH" >/dev/null
if ! wait_for_evidence_line "$(launch_prefix 1).summary.txt" "issue_identifier=$COMMAND_ISSUE"; then
  echo "command approval scenario never produced launch-1 summary" >&2
  exit 1
fi
assert_permission_prompt_launch_summary "$(launch_prefix 1).summary.txt" "$COMMAND_ISSUE"
run_interrupt_probe "$COMMAND_ISSUE" 1 "command-live" "command" "Bash" "allow"
assert_interrupt_summary "$CLAUDE_EVIDENCE_DIR/command-live.summary.txt" "command" "Bash" "allow" "accepted" "true"
assert_approval_surface_summary "$CLAUDE_EVIDENCE_DIR/command-live.summary.txt"
if ! wait_for_done "$COMMAND_ISSUE"; then
  echo "$COMMAND_ISSUE did not reach done within ${TIMEOUT_SEC}s" >&2
  exit 1
fi
assert_file_content "$(command_artifact_path "$COMMAND_ISSUE")" "$(command_artifact_text)"
run_final_probe "$COMMAND_ISSUE" 1 "command-final"
assert_claude_runtime_surface "$CLAUDE_EVIDENCE_DIR/command-final.summary.txt"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/command-final.summary.txt" "dashboard_session_status=completed"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/command-final.summary.txt" "execution_session_source=persisted"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/command-final.summary.txt" "execution_stop_reason=end_turn"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/command-final.summary.txt" "runtime_event_count=1"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/command-final.summary.txt" "runtime_event_kinds=run_completed"

echo "Running file write denial scenario for $WRITE_ISSUE"
"$MAESTRO_BIN" issue move "$WRITE_ISSUE" ready --db "$DB_PATH" >/dev/null
if ! wait_for_evidence_line "$(launch_prefix 2).summary.txt" "issue_identifier=$WRITE_ISSUE"; then
  echo "file write denial scenario never produced launch-2 summary" >&2
  exit 1
fi
assert_permission_prompt_launch_summary "$(launch_prefix 2).summary.txt" "$WRITE_ISSUE"
run_interrupt_probe "$WRITE_ISSUE" 2 "write-live" "file_write" "Write" "deny"
assert_interrupt_summary "$CLAUDE_EVIDENCE_DIR/write-live.summary.txt" "file_write" "Write" "deny" "accepted" "true"
assert_approval_surface_summary "$CLAUDE_EVIDENCE_DIR/write-live.summary.txt"
if ! wait_for_done "$WRITE_ISSUE"; then
  echo "$WRITE_ISSUE did not reach done within ${TIMEOUT_SEC}s" >&2
  exit 1
fi
assert_missing_path "$(write_artifact_path "$WRITE_ISSUE")"
run_final_probe "$WRITE_ISSUE" 2 "write-final"
assert_claude_runtime_surface "$CLAUDE_EVIDENCE_DIR/write-final.summary.txt"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/write-final.summary.txt" "dashboard_session_status=completed"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/write-final.summary.txt" "execution_session_source=persisted"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/write-final.summary.txt" "execution_stop_reason=end_turn"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/write-final.summary.txt" "runtime_event_count=1"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/write-final.summary.txt" "runtime_event_kinds=run_completed"

echo "Running file edit timeout scenario for $EDIT_ISSUE"
"$MAESTRO_BIN" issue move "$EDIT_ISSUE" ready --db "$DB_PATH" >/dev/null
if ! wait_for_evidence_line "$(launch_prefix 3).summary.txt" "issue_identifier=$EDIT_ISSUE"; then
  echo "file edit timeout scenario never produced launch-3 summary" >&2
  exit 1
fi
assert_permission_prompt_launch_summary "$(launch_prefix 3).summary.txt" "$EDIT_ISSUE"
run_interrupt_probe "$EDIT_ISSUE" 3 "edit-live" "file_edit" "Edit"
assert_interrupt_summary "$CLAUDE_EVIDENCE_DIR/edit-live.summary.txt" "file_edit" "Edit" "" "" "false"
assert_approval_surface_summary "$CLAUDE_EVIDENCE_DIR/edit-live.summary.txt"
if ! wait_for_runtime_event "$EDIT_ISSUE" "run_failed"; then
  echo "file edit timeout scenario never recorded run_failed" >&2
  exit 1
fi
if ! wait_for_issue_execution_run_kind "$EDIT_ISSUE" "retry_paused"; then
  echo "file edit timeout scenario never persisted retry_paused" >&2
  exit 1
fi
TIMEOUT_ERROR="$(latest_runtime_event_error "$EDIT_ISSUE" "run_failed")"
case "$TIMEOUT_ERROR" in
  "context deadline exceeded"|"turn_timeout")
    ;;
  *)
    echo "expected timeout error for $EDIT_ISSUE, got '$TIMEOUT_ERROR'" >&2
    exit 1
    ;;
esac
run_final_probe "$EDIT_ISSUE" 3 "edit-final"
assert_claude_runtime_surface "$CLAUDE_EVIDENCE_DIR/edit-final.summary.txt"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/edit-final.summary.txt" "dashboard_session_status=paused"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/edit-final.summary.txt" "dashboard_session_stop_reason=retry_limit_reached"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/edit-final.summary.txt" "execution_session_source=persisted"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/edit-final.summary.txt" "tool_call_list_runtime_events=ok"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/edit-final.summary.txt" "runtime_event_count=1"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/edit-final.summary.txt" "runtime_event_kinds=run_failed"
if [[ "$(issue_state "$EDIT_ISSUE")" == "done" ]]; then
  echo "did not expect timeout issue $EDIT_ISSUE to be marked done" >&2
  exit 1
fi

echo "Running protected write approval scenario for $PROTECTED_ISSUE"
"$MAESTRO_BIN" issue move "$PROTECTED_ISSUE" ready --db "$DB_PATH" >/dev/null
if ! wait_for_evidence_line "$(launch_prefix 4).summary.txt" "issue_identifier=$PROTECTED_ISSUE"; then
  echo "protected write scenario never produced launch-4 summary" >&2
  exit 1
fi
assert_permission_prompt_launch_summary "$(launch_prefix 4).summary.txt" "$PROTECTED_ISSUE"
run_interrupt_probe "$PROTECTED_ISSUE" 4 "protected-live" "protected_directory_write" "Write" "allow"
assert_interrupt_summary "$CLAUDE_EVIDENCE_DIR/protected-live.summary.txt" "protected_directory_write" "Write" "allow" "accepted" "true"
assert_approval_surface_summary "$CLAUDE_EVIDENCE_DIR/protected-live.summary.txt"
if ! wait_for_done "$PROTECTED_ISSUE"; then
  echo "$PROTECTED_ISSUE did not reach done within ${TIMEOUT_SEC}s" >&2
  exit 1
fi
assert_file_content "$(protected_artifact_path "$PROTECTED_ISSUE")" "$(protected_artifact_text)"
run_final_probe "$PROTECTED_ISSUE" 4 "protected-final"
assert_claude_runtime_surface "$CLAUDE_EVIDENCE_DIR/protected-final.summary.txt"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/protected-final.summary.txt" "dashboard_session_status=completed"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/protected-final.summary.txt" "execution_session_source=persisted"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/protected-final.summary.txt" "execution_stop_reason=end_turn"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/protected-final.summary.txt" "runtime_event_count=1"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/protected-final.summary.txt" "runtime_event_kinds=run_completed"

echo "Running shared alert acknowledgement scenario for $ALERT_ISSUE"
"$MAESTRO_BIN" issue move "$ALERT_ISSUE" ready --db "$DB_PATH" >/dev/null
run_alert_probe "$ALERT_ISSUE" 4 "alert-live"
assert_alert_summary "$CLAUDE_EVIDENCE_DIR/alert-live.summary.txt"

echo "Real Claude approval bridge e2e flow completed successfully."
echo "Verified:"
echo "  command allow: $COMMAND_ISSUE -> $(command_artifact_path "$COMMAND_ISSUE")"
echo "  write deny: $WRITE_ISSUE -> denied without file creation"
echo "  edit timeout: $EDIT_ISSUE -> timeout recorded"
echo "  protected allow: $PROTECTED_ISSUE -> $(protected_artifact_path "$PROTECTED_ISSUE")"
echo "  alert acknowledge: $ALERT_ISSUE -> project_dispatch_blocked acknowledged"
echo "  verify log: $VERIFY_LOG"
echo "  orchestrator log: $ORCH_LOG"
echo "  claude evidence dir: $CLAUDE_EVIDENCE_DIR"
