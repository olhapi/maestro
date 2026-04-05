#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=./lib/e2e_real_claude_harness.sh
source "$ROOT_DIR/scripts/lib/e2e_real_claude_harness.sh"

trap cleanup EXIT INT TERM

CLAUDE_COMMAND="${E2E_CLAUDE_COMMAND:-claude}"
PROJECT_NAME="Real Claude Permission Profile E2E Project"
PROJECT_WORKSPACE_SLUG="real-claude-profile-e2e-project"
ALLOWED_TOOLS="Bash,Edit,Write,MultiEdit"
MAESTRO_PROMPT_TOOL="mcp__maestro__approval_prompt"

default_stream_marker() {
  printf 'STREAM:%s:profile-default' "$1"
}

full_access_stream_marker() {
  printf 'STREAM:%s:profile-full-access' "$1"
}

plan_stream_marker() {
  printf 'STREAM:%s:profile-plan' "$1"
}

default_artifact_text() {
  printf 'maestro claude default profile ok'
}

full_access_artifact_text() {
  printf 'maestro claude full access profile ok'
}

plan_artifact_text() {
  printf 'maestro claude plan profile ok'
}

issue_workspace_path() {
  printf '%s/%s/%s' "$WORKSPACES_DIR" "$PROJECT_WORKSPACE_SLUG" "$1"
}

default_artifact_path() {
  printf '%s/default-profile.txt' "$(issue_workspace_path "$1")"
}

full_access_artifact_path() {
  printf '%s/full-access-profile.txt' "$(issue_workspace_path "$1")"
}

plan_artifact_path() {
  printf '%s/plan-profile.txt' "$(issue_workspace_path "$1")"
}

launch_prefix() {
  printf '%s/launch-%s' "$CLAUDE_EVIDENCE_DIR" "$1"
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

evidence_value() {
  local path="$1"
  local key="$2"
  sed -n "s/^${key}=//p" "$path" | head -n 1
}

assert_evidence_value() {
  local path="$1"
  local key="$2"
  local expected="$3"
  local actual
  actual="$(evidence_value "$path" "$key")"
  if [[ "$actual" != "$expected" ]]; then
    echo "unexpected $key in $path: expected '$expected', got '$actual'" >&2
    return 1
  fi
}

args_resume_value() {
  local path="$1"
  awk 'BEGIN { previous="" } previous == "-r" { print; exit } { previous=$0 }' "$path"
}

run_probe() {
  local mode="$1"
  local issue_id="$2"
  local launch_number="$3"
  local prefix="$4"
  local permission_mode="$5"
  local allowed_tools="$6"
  local permission_prompt_tool="$7"
  shift 7

  local -a args=(
    --mode "$mode"
    --issue-identifier "$issue_id"
    --mcp-config "$(launch_prefix "$launch_number").mcp.json"
    --settings "$(launch_prefix "$launch_number").settings.json"
    --db "$DB_PATH"
    --registry-dir "$DAEMON_REGISTRY_DIR"
    --evidence-prefix "$CLAUDE_EVIDENCE_DIR/$prefix"
    --permission-mode "$permission_mode"
    --strict-mcp-config true
  )
  if [[ -n "$allowed_tools" ]]; then
    args+=(--allowed-tools "$allowed_tools")
  fi
  if [[ -n "$permission_prompt_tool" ]]; then
    args+=(--permission-prompt-tool "$permission_prompt_tool")
  fi
  if [[ "$#" -gt 0 ]]; then
    args+=("$@")
  fi
  "$CLAUDE_PROBE_BIN" "${args[@]}"
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

assert_common_launch_summary() {
  local path="$1"
  local issue_id="$2"
  assert_evidence_line "$path" "issue_identifier=$issue_id"
  assert_evidence_line "$path" "daemon_entry_stable=true"
  assert_evidence_line "$path" "strict_mcp_config=true"
  assert_evidence_line "$path" "settings_disable_auto_mode=disable"
  assert_evidence_line "$path" "settings_use_auto_mode_during_plan=false"
  assert_evidence_line "$path" "settings_disable_all_hooks=true"
  assert_evidence_line "$path" "settings_include_git_instructions=false"
  assert_evidence_line "$path" "settings_disable_bypass_permissions_mode=disable"
  assert_evidence_line "$path" "tool_call_get_issue_execution=ok"
  assert_evidence_line "$path" "tool_call_list_runtime_events=ok"
  assert_evidence_line "$path" "live_claude_session_seen=true"
  assert_evidence_line "$path" "dashboard_session_source=live"
  assert_evidence_line "$path" "execution_session_source=live"
  assert_claude_runtime_surface "$path"
}

assert_prompt_launch_summary() {
  local path="$1"
  local issue_id="$2"
  local permission_mode="$3"
  assert_common_launch_summary "$path" "$issue_id"
  assert_evidence_line "$path" "allowed_tools="
  assert_evidence_line "$path" "permission_mode=$permission_mode"
  assert_evidence_line "$path" "permission_prompt_tool=$MAESTRO_PROMPT_TOOL"
  if grep -Fq -- "--allowed-tools" "${path%.summary.txt}.args.txt"; then
    echo "did not expect --allowed-tools in ${path%.summary.txt}.args.txt" >&2
    return 1
  fi
  if ! grep -Fq -- "--permission-prompt-tool" "${path%.summary.txt}.args.txt"; then
    echo "expected --permission-prompt-tool in ${path%.summary.txt}.args.txt" >&2
    return 1
  fi
}

assert_full_access_launch_summary() {
  local path="$1"
  local issue_id="$2"
  assert_common_launch_summary "$path" "$issue_id"
  assert_evidence_line "$path" "allowed_tools=$ALLOWED_TOOLS"
  assert_evidence_line "$path" "permission_mode=default"
  assert_evidence_line "$path" "permission_prompt_tool=<none>"
  if ! grep -Fq -- "--allowed-tools" "${path%.summary.txt}.args.txt"; then
    echo "expected --allowed-tools in ${path%.summary.txt}.args.txt" >&2
    return 1
  fi
  if grep -Fq -- "--permission-prompt-tool" "${path%.summary.txt}.args.txt"; then
    echo "did not expect --permission-prompt-tool in ${path%.summary.txt}.args.txt" >&2
    return 1
  fi
}

assert_approval_surface_summary() {
  local path="$1"
  assert_claude_runtime_surface "$path"
  assert_evidence_line "$path" "dashboard_session_pending_interaction_state=approval"
  assert_evidence_line "$path" "execution_pending_interaction_state=approval"
}

DEFAULT_ISSUE=""
FULL_ACCESS_ISSUE=""
PLAN_ISSUE=""
PLAN_SESSION_ID=""
PLAN_RECORD_ID=""
PLAN_REVISION_NOTE="Add an explicit rollback check and keep the rollout incremental."

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
    turn_timeout_ms: 300000
    read_timeout_ms: 5000
---
You are running the Maestro real-Claude permission-profile end-to-end harness.

Complete only the current issue and then stop.

Issue identifier: {{ issue.identifier }}
Issue title: {{ issue.title }}
Issue description:
{{ issue.description }}

Environment:
- Current directory is an isolated issue workspace.
- Use Maestro MCP tools for issue state transitions.
- Do not use built-in tools other than the single built-in tool explicitly required by the issue description.
EOF

init_harness_repo "$HARNESS_ROOT"
run_claude_verify

PROJECT_ID="$("$MAESTRO_BIN" project create "$PROJECT_NAME" --repo "$HARNESS_ROOT" --db "$DB_PATH" --quiet)"
start_project "$PROJECT_ID"

echo "Creating Claude permission-profile e2e issues in $DB_PATH"
DEFAULT_ISSUE="$("$MAESTRO_BIN" issue create "Claude profile default" --project "$PROJECT_ID" --desc "placeholder" --db "$DB_PATH" --quiet)"
FULL_ACCESS_ISSUE="$("$MAESTRO_BIN" issue create "Claude profile full access" --project "$PROJECT_ID" --desc "placeholder" --db "$DB_PATH" --quiet)"
PLAN_ISSUE="$("$MAESTRO_BIN" issue create "Claude profile plan then full access" --project "$PROJECT_ID" --desc "placeholder" --db "$DB_PATH" --quiet)"

"$MAESTRO_BIN" issue update "$FULL_ACCESS_ISSUE" --permission-profile full-access --db "$DB_PATH" >/dev/null
"$MAESTRO_BIN" issue update "$PLAN_ISSUE" --permission-profile plan-then-full-access --db "$DB_PATH" >/dev/null

"$MAESTRO_BIN" issue update "$DEFAULT_ISSUE" --desc "$(cat <<EOF
Stream marker: $(default_stream_marker "$DEFAULT_ISSUE")
Target file: default-profile.txt
Target text: $(default_artifact_text)

Requirements:
1. Immediately emit exactly: $(default_stream_marker "$DEFAULT_ISSUE")
2. Use exactly one Bash tool call with this exact command:
   printf '%s\n' '$(default_artifact_text)' > default-profile.txt
3. Do not use any other built-in tools.
4. After the Bash call succeeds, move the issue to done with the Maestro MCP \`set_issue_state\` tool.
5. Stop after the issue is marked done.
EOF
)" --db "$DB_PATH" >/dev/null

"$MAESTRO_BIN" issue update "$FULL_ACCESS_ISSUE" --desc "$(cat <<EOF
Stream marker: $(full_access_stream_marker "$FULL_ACCESS_ISSUE")
Target file: full-access-profile.txt
Target text: $(full_access_artifact_text)

Requirements:
1. Immediately emit exactly: $(full_access_stream_marker "$FULL_ACCESS_ISSUE")
2. Use exactly one Bash tool call with this exact command:
   printf '%s\n' '$(full_access_artifact_text)' > full-access-profile.txt
3. Do not use any other built-in tools.
4. After the Bash call succeeds, move the issue to done with the Maestro MCP \`set_issue_state\` tool.
5. Stop after the issue is marked done.
EOF
)" --db "$DB_PATH" >/dev/null

"$MAESTRO_BIN" issue update "$PLAN_ISSUE" --desc "$(cat <<EOF
Plan stream marker: $(plan_stream_marker "$PLAN_ISSUE")
Execution stream marker: $(plan_stream_marker "$PLAN_ISSUE")
Target file: plan-profile.txt
Target text: $(plan_artifact_text)

Requirements:
1. During planning, immediately emit exactly: $(plan_stream_marker "$PLAN_ISSUE")
2. During planning, propose a plan inside a single \`<proposed_plan>\` block and do not execute the file creation yet.
3. If a plan revision note arrives, update the plan to include that note and a rollback check before stopping again for approval.
4. After the plan is approved, emit exactly: $(plan_stream_marker "$PLAN_ISSUE")
5. After approval, use exactly one Bash tool call with this exact command:
   printf '%s\n' '$(plan_artifact_text)' > plan-profile.txt
6. Do not use any other built-in tools.
7. After the Bash call succeeds, move the issue to done with the Maestro MCP \`set_issue_state\` tool.
8. Stop after the issue is marked done.
EOF
)" --db "$DB_PATH" >/dev/null

start_orchestrator

echo "Running default permission-profile scenario for $DEFAULT_ISSUE"
"$MAESTRO_BIN" issue move "$DEFAULT_ISSUE" ready --db "$DB_PATH" >/dev/null
if ! wait_for_evidence_line "$(launch_prefix 1).summary.txt" "issue_identifier=$DEFAULT_ISSUE"; then
  echo "default profile scenario never produced launch-1 summary" >&2
  exit 1
fi
assert_prompt_launch_summary "$(launch_prefix 1).summary.txt" "$DEFAULT_ISSUE" "default"
run_probe live "$DEFAULT_ISSUE" 1 "default-live" "default" "" "$MAESTRO_PROMPT_TOOL" \
  --interrupt-classification command \
  --interrupt-tool-name Bash \
  --interrupt-decision allow
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/default-live.summary.txt" "interrupt_requested=true"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/default-live.summary.txt" "interrupt_response_decision=allow"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/default-live.summary.txt" "interrupt_response_status=accepted"
assert_approval_surface_summary "$CLAUDE_EVIDENCE_DIR/default-live.summary.txt"
if ! wait_for_done "$DEFAULT_ISSUE"; then
  echo "$DEFAULT_ISSUE did not reach done within ${TIMEOUT_SEC}s" >&2
  exit 1
fi
assert_file_content "$(default_artifact_path "$DEFAULT_ISSUE")" "$(default_artifact_text)"
run_probe final "$DEFAULT_ISSUE" 1 "default-final" "default" "" "$MAESTRO_PROMPT_TOOL"
assert_claude_runtime_surface "$CLAUDE_EVIDENCE_DIR/default-final.summary.txt"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/default-final.summary.txt" "dashboard_session_status=completed"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/default-final.summary.txt" "execution_stop_reason=end_turn"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/default-final.summary.txt" "issue_permission_profile=default"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/default-final.summary.txt" "issue_plan_approval_pending=false"

echo "Running full-access permission-profile scenario for $FULL_ACCESS_ISSUE"
"$MAESTRO_BIN" issue move "$FULL_ACCESS_ISSUE" ready --db "$DB_PATH" >/dev/null
if ! wait_for_evidence_line "$(launch_prefix 2).summary.txt" "issue_identifier=$FULL_ACCESS_ISSUE"; then
  echo "full-access profile scenario never produced launch-2 summary" >&2
  exit 1
fi
assert_full_access_launch_summary "$(launch_prefix 2).summary.txt" "$FULL_ACCESS_ISSUE"
if ! wait_for_done "$FULL_ACCESS_ISSUE"; then
  echo "$FULL_ACCESS_ISSUE did not reach done within ${TIMEOUT_SEC}s" >&2
  exit 1
fi
assert_file_content "$(full_access_artifact_path "$FULL_ACCESS_ISSUE")" "$(full_access_artifact_text)"
run_probe final "$FULL_ACCESS_ISSUE" 2 "full-access-final" "default" "$ALLOWED_TOOLS" ""
assert_claude_runtime_surface "$CLAUDE_EVIDENCE_DIR/full-access-final.summary.txt"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/full-access-final.summary.txt" "dashboard_session_status=completed"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/full-access-final.summary.txt" "execution_stop_reason=end_turn"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/full-access-final.summary.txt" "issue_permission_profile=full-access"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/full-access-final.summary.txt" "issue_plan_approval_pending=false"

echo "Running plan-then-full-access permission-profile scenario for $PLAN_ISSUE"
"$MAESTRO_BIN" issue move "$PLAN_ISSUE" ready --db "$DB_PATH" >/dev/null
if ! wait_for_evidence_line "$(launch_prefix 3).summary.txt" "issue_identifier=$PLAN_ISSUE"; then
  echo "plan profile scenario never produced launch-3 summary" >&2
  exit 1
fi
assert_prompt_launch_summary "$(launch_prefix 3).summary.txt" "$PLAN_ISSUE" "plan"
if [[ -n "$(args_resume_value "$(launch_prefix 3).args.txt")" ]]; then
  echo "did not expect the first plan launch to use -r" >&2
  exit 1
fi
run_probe final "$PLAN_ISSUE" 3 "plan-pending-v1" "plan" "" "$MAESTRO_PROMPT_TOOL" \
  --interrupt-approval-type plan_approval \
  --interrupt-plan-status awaiting_approval \
  --interrupt-plan-version 1
assert_approval_surface_summary "$CLAUDE_EVIDENCE_DIR/plan-pending-v1.summary.txt"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-pending-v1.summary.txt" "dashboard_session_status=paused"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-pending-v1.summary.txt" "dashboard_session_stop_reason=plan_approval_pending"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-pending-v1.summary.txt" "execution_stop_reason=plan_approval_pending"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-pending-v1.summary.txt" "issue_permission_profile=plan-then-full-access"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-pending-v1.summary.txt" "issue_plan_approval_pending=true"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-pending-v1.summary.txt" "planning_status=awaiting_approval"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-pending-v1.summary.txt" "planning_version_count=1"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-pending-v1.summary.txt" "planning_current_version_number=1"
PLAN_SESSION_ID="$(evidence_value "$CLAUDE_EVIDENCE_DIR/plan-pending-v1.summary.txt" "execution_thread_id")"
PLAN_RECORD_ID="$(evidence_value "$CLAUDE_EVIDENCE_DIR/plan-pending-v1.summary.txt" "planning_session_id")"
assert_evidence_value "$CLAUDE_EVIDENCE_DIR/plan-pending-v1.summary.txt" "planning_current_version_thread_id" "$PLAN_SESSION_ID"

run_probe final "$PLAN_ISSUE" 3 "plan-revision-requested" "plan" "" "$MAESTRO_PROMPT_TOOL" \
  --interrupt-approval-type plan_approval \
  --interrupt-plan-status awaiting_approval \
  --interrupt-plan-version 1 \
  --interrupt-note "$PLAN_REVISION_NOTE"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-revision-requested.summary.txt" "interrupt_requested=true"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-revision-requested.summary.txt" "interrupt_response_status=accepted"
assert_approval_surface_summary "$CLAUDE_EVIDENCE_DIR/plan-revision-requested.summary.txt"

if ! wait_for_evidence_line "$(launch_prefix 4).summary.txt" "issue_identifier=$PLAN_ISSUE"; then
  echo "plan revision redispatch never produced launch-4 summary" >&2
  exit 1
fi
assert_prompt_launch_summary "$(launch_prefix 4).summary.txt" "$PLAN_ISSUE" "plan"
if [[ "$(args_resume_value "$(launch_prefix 4).args.txt")" != "$PLAN_SESSION_ID" ]]; then
  echo "expected revised plan launch to reuse session $PLAN_SESSION_ID" >&2
  exit 1
fi
run_probe final "$PLAN_ISSUE" 4 "plan-pending-v2" "plan" "" "$MAESTRO_PROMPT_TOOL" \
  --interrupt-approval-type plan_approval \
  --interrupt-plan-status awaiting_approval \
  --interrupt-plan-version 2
assert_approval_surface_summary "$CLAUDE_EVIDENCE_DIR/plan-pending-v2.summary.txt"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-pending-v2.summary.txt" "dashboard_session_status=paused"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-pending-v2.summary.txt" "execution_stop_reason=plan_approval_pending"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-pending-v2.summary.txt" "issue_permission_profile=plan-then-full-access"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-pending-v2.summary.txt" "issue_plan_approval_pending=true"
assert_evidence_value "$CLAUDE_EVIDENCE_DIR/plan-pending-v2.summary.txt" "planning_session_id" "$PLAN_RECORD_ID"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-pending-v2.summary.txt" "planning_status=awaiting_approval"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-pending-v2.summary.txt" "planning_version_count=2"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-pending-v2.summary.txt" "planning_current_version_number=2"
assert_evidence_value "$CLAUDE_EVIDENCE_DIR/plan-pending-v2.summary.txt" "planning_current_version_thread_id" "$PLAN_SESSION_ID"
assert_evidence_value "$CLAUDE_EVIDENCE_DIR/plan-pending-v2.summary.txt" "planning_current_version_revision_note" "$PLAN_REVISION_NOTE"

run_probe final "$PLAN_ISSUE" 4 "plan-approved" "plan" "" "$MAESTRO_PROMPT_TOOL" \
  --interrupt-approval-type plan_approval \
  --interrupt-plan-status awaiting_approval \
  --interrupt-plan-version 2 \
  --interrupt-decision approved
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-approved.summary.txt" "interrupt_requested=true"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-approved.summary.txt" "interrupt_response_decision=approved"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-approved.summary.txt" "interrupt_response_status=accepted"
assert_approval_surface_summary "$CLAUDE_EVIDENCE_DIR/plan-approved.summary.txt"

if ! wait_for_evidence_line "$(launch_prefix 5).summary.txt" "issue_identifier=$PLAN_ISSUE"; then
  echo "post-approval execution never produced launch-5 summary" >&2
  exit 1
fi
assert_full_access_launch_summary "$(launch_prefix 5).summary.txt" "$PLAN_ISSUE"
if [[ "$(args_resume_value "$(launch_prefix 5).args.txt")" != "$PLAN_SESSION_ID" ]]; then
  echo "expected post-approval execution to reuse session $PLAN_SESSION_ID" >&2
  exit 1
fi
if ! wait_for_done "$PLAN_ISSUE"; then
  echo "$PLAN_ISSUE did not reach done within ${TIMEOUT_SEC}s" >&2
  exit 1
fi
assert_file_content "$(plan_artifact_path "$PLAN_ISSUE")" "$(plan_artifact_text)"
run_probe final "$PLAN_ISSUE" 5 "plan-final" "default" "$ALLOWED_TOOLS" ""
assert_claude_runtime_surface "$CLAUDE_EVIDENCE_DIR/plan-final.summary.txt"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-final.summary.txt" "dashboard_session_status=completed"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-final.summary.txt" "execution_stop_reason=end_turn"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-final.summary.txt" "issue_permission_profile=full-access"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-final.summary.txt" "issue_plan_approval_pending=false"
assert_evidence_value "$CLAUDE_EVIDENCE_DIR/plan-final.summary.txt" "planning_session_id" "$PLAN_RECORD_ID"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-final.summary.txt" "planning_status=approved"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-final.summary.txt" "planning_version_count=2"
assert_evidence_line "$CLAUDE_EVIDENCE_DIR/plan-final.summary.txt" "planning_current_version_number=2"
assert_evidence_value "$CLAUDE_EVIDENCE_DIR/plan-final.summary.txt" "execution_thread_id" "$PLAN_SESSION_ID"

echo "Real Claude permission-profile e2e flow completed successfully."
echo "Verified:"
echo "  default: $DEFAULT_ISSUE -> $(default_artifact_path "$DEFAULT_ISSUE")"
echo "  full-access: $FULL_ACCESS_ISSUE -> $(full_access_artifact_path "$FULL_ACCESS_ISSUE")"
echo "  plan-then-full-access: $PLAN_ISSUE -> $(plan_artifact_path "$PLAN_ISSUE")"
echo "  verify log: $VERIFY_LOG"
echo "  orchestrator log: $ORCH_LOG"
echo "  claude evidence dir: $CLAUDE_EVIDENCE_DIR"
