#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=./lib/e2e_real_claude_harness.sh
source "$ROOT_DIR/scripts/lib/e2e_real_claude_harness.sh"

trap cleanup EXIT INT TERM

CLAUDE_COMMAND="${E2E_CLAUDE_COMMAND:-claude}"
CODEX_COMMAND="$(default_codex_command)"

success_stream_marker() {
  printf 'STREAM:%s:success-live' "$1"
}

resume_stream_marker() {
  printf 'STREAM:%s:resume-live' "$1"
}

interrupt_stream_marker() {
  printf 'STREAM:%s:interrupt-live' "$1"
}

success_gate_path() {
  printf '%s/%s.success.gate' "$ARTIFACTS_DIR" "$1"
}

resume_gate_path() {
  printf '%s/%s.resume.gate' "$ARTIFACTS_DIR" "$1"
}

interrupt_gate_path() {
  printf '%s/%s.interrupt.gate' "$ARTIFACTS_DIR" "$1"
}

success_artifact_path() {
  printf '%s/%s.success.txt' "$ARTIFACTS_DIR" "$1"
}

resume_artifact_path() {
  printf '%s/%s.resume.txt' "$ARTIFACTS_DIR" "$1"
}

success_artifact_text() {
  printf 'maestro claude success e2e ok'
}

resume_artifact_text() {
  printf 'maestro claude resume e2e ok'
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

issue_execution_session_field() {
  local identifier="$1"
  local field="$2"
  sql_value "SELECT COALESCE(json_extract(session_json, '$.$field'), '') FROM issue_execution_sessions WHERE identifier = '$identifier' LIMIT 1;"
}

issue_execution_metadata_field() {
  local identifier="$1"
  local field="$2"
  sql_value "SELECT COALESCE(json_extract(session_json, '$.metadata.$field'), '') FROM issue_execution_sessions WHERE identifier = '$identifier' LIMIT 1;"
}

runtime_event_count() {
  local identifier="$1"
  local kind="$2"
  sql_value "SELECT COUNT(*) FROM runtime_events WHERE identifier = '$identifier' AND kind = '$kind';"
}

project_runtime_event_count() {
  local project_id="$1"
  local kind="$2"
  sql_value "SELECT COUNT(*) FROM runtime_events WHERE project_id = '$project_id' AND kind = '$kind';"
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

wait_for_project_runtime_event() {
  local project_id="$1"
  local kind="$2"
  local deadline
  deadline=$((SECONDS + TIMEOUT_SEC))
  while (( SECONDS < deadline )); do
    if [[ "$(project_runtime_event_count "$project_id" "$kind")" -ge 1 ]]; then
      return 0
    fi
    sleep "$POLL_SEC"
  done
  return 1
}

wait_for_execution_snapshot() {
  local identifier="$1"
  local expected_kind="$2"
  local expected_stop_reason="$3"
  local deadline
  deadline=$((SECONDS + TIMEOUT_SEC))
  while (( SECONDS < deadline )); do
    local run_kind stop_reason
    run_kind="$(issue_execution_value "$identifier" "run_kind")"
    stop_reason="$(issue_execution_value "$identifier" "stop_reason")"
    if [[ "$run_kind" == "$expected_kind" && "$stop_reason" == "$expected_stop_reason" ]]; then
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

update_issue_description() {
  local issue_id="$1"
  local description="$2"
  "$MAESTRO_BIN" issue update "$issue_id" --desc "$description" --db "$DB_PATH" >/dev/null
}

run_post_probe() {
  local issue_id="$1"
  local launch_number="$2"
  local prefix="$3"
  "$CLAUDE_PROBE_BIN" \
    --mode final \
    --issue-identifier "$issue_id" \
    --mcp-config "$CLAUDE_EVIDENCE_DIR/launch-$launch_number.mcp.json" \
    --settings "$CLAUDE_EVIDENCE_DIR/launch-$launch_number.settings.json" \
    --db "$DB_PATH" \
    --registry-dir "$DAEMON_REGISTRY_DIR" \
    --evidence-prefix "$CLAUDE_EVIDENCE_DIR/$prefix" \
    --allowed-tools "Bash,Edit,Write,MultiEdit" \
    --permission-mode default \
    --strict-mcp-config true
}

assert_success_snapshot() {
  local issue_id="$1"
  local expected_session="$2"
  local final_summary="$3"
  if [[ "$(issue_execution_value "$issue_id" "run_kind")" != "run_completed" ]]; then
    echo "expected completed execution snapshot for $issue_id" >&2
    return 1
  fi
  assert_evidence_value "$final_summary" "dashboard_session_status" "completed"
  assert_evidence_value "$final_summary" "dashboard_session_source" "persisted"
  assert_evidence_value "$final_summary" "dashboard_session_stop_reason" "end_turn"
  assert_evidence_value "$final_summary" "execution_session_source" "persisted"
  assert_evidence_value "$final_summary" "execution_runtime_provider" "claude"
  assert_evidence_value "$final_summary" "execution_runtime_transport" "stdio"
  assert_evidence_value "$final_summary" "execution_stop_reason" "end_turn"
  assert_evidence_value "$final_summary" "execution_thread_id" "$expected_session"
  assert_evidence_value "$final_summary" "execution_session_id" "$expected_session"
  assert_evidence_value "$final_summary" "execution_provider_session_id" "$expected_session"
  assert_evidence_value "$final_summary" "execution_session_identifier_strategy" "provider_session_uuid"
  [[ "$(issue_execution_value "$issue_id" "runtime_provider")" == "claude" ]] || {
    echo "expected runtime_provider=claude for $issue_id" >&2
    return 1
  }
  [[ "$(issue_execution_value "$issue_id" "runtime_transport")" == "stdio" ]] || {
    echo "expected runtime_transport=stdio for $issue_id" >&2
    return 1
  }
  [[ "$(issue_execution_session_field "$issue_id" "thread_id")" == "$expected_session" ]] || {
    echo "expected persisted thread_id=$expected_session for $issue_id" >&2
    return 1
  }
  [[ "$(issue_execution_session_field "$issue_id" "session_id")" == "$expected_session" ]] || {
    echo "expected persisted session_id=$expected_session for $issue_id" >&2
    return 1
  }
  [[ "$(issue_execution_metadata_field "$issue_id" "provider_session_id")" == "$expected_session" ]] || {
    echo "expected persisted provider_session_id=$expected_session for $issue_id" >&2
    return 1
  }
  [[ "$(issue_execution_metadata_field "$issue_id" "session_identifier_strategy")" == "provider_session_uuid" ]] || {
    echo "expected persisted session_identifier_strategy=provider_session_uuid for $issue_id" >&2
    return 1
  }
  [[ "$(issue_execution_metadata_field "$issue_id" "claude_stop_reason")" == "end_turn" ]] || {
    echo "expected persisted claude_stop_reason=end_turn for $issue_id" >&2
    return 1
  }
}

CLAUDE_WORKFLOW_COMMAND=""
PROJECT_ID=""
SUCCESS_ISSUE=""
RESUME_ISSUE=""
INTERRUPT_ISSUE=""

cd "$ROOT_DIR"

require_cmd go
require_command_string "Claude" "$CLAUDE_COMMAND"
require_command_string "Codex" "$CODEX_COMMAND"
require_cmd git
require_cmd sqlite3

ensure_harness_dirs
build_maestro
build_claude_probe
prepare_claude_command_wrapper "$CLAUDE_COMMAND"
export PATH="$BIN_DIR:$PATH"
export MAESTRO_DAEMON_REGISTRY_DIR="$DAEMON_REGISTRY_DIR"

CLAUDE_WORKFLOW_COMMAND="$(yaml_quote "$CLAUDE_WRAPPER_BIN")"

run_claude_workflow_init "$CODEX_COMMAND"

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
You are running the Maestro real-Claude lifecycle end-to-end harness.

Complete only the current issue and then stop.

Issue identifier: {{ issue.identifier }}
Issue title: {{ issue.title }}
Issue description:
{{ issue.description }}

Environment:
- Current directory is an isolated issue workspace.
- Shared artifacts directory: $ARTIFACTS_DIR
- Maestro binary: $MAESTRO_BIN
- Maestro database: $DB_PATH

Requirements:
1. Follow the issue description exactly.
2. Emit the requested stream marker exactly before you perform the gated wait or final verification.
3. Verify every requested file from the shell before changing issue state.
4. Stop immediately after the requested state transition succeeds.
EOF

init_harness_repo "$HARNESS_ROOT"
run_claude_spec_check
run_claude_verify
run_claude_doctor

PROJECT_ID="$("$MAESTRO_BIN" project create "Real Claude Lifecycle E2E Project" --repo "$HARNESS_ROOT" --db "$DB_PATH" --quiet)"
start_project "$PROJECT_ID"

echo "Creating Claude lifecycle e2e issues in $DB_PATH"
SUCCESS_ISSUE="$("$MAESTRO_BIN" issue create "Claude lifecycle success" --project "$PROJECT_ID" --desc "placeholder" --db "$DB_PATH" --quiet)"
RESUME_ISSUE="$("$MAESTRO_BIN" issue create "Claude lifecycle resume" --project "$PROJECT_ID" --desc "placeholder" --db "$DB_PATH" --quiet)"
INTERRUPT_ISSUE="$("$MAESTRO_BIN" issue create "Claude lifecycle interrupt" --project "$PROJECT_ID" --desc "placeholder" --db "$DB_PATH" --quiet)"

set_issue_permission_profile "$SUCCESS_ISSUE" full-access
set_issue_permission_profile "$RESUME_ISSUE" full-access
set_issue_permission_profile "$INTERRUPT_ISSUE" full-access

update_issue_description "$SUCCESS_ISSUE" "$(cat <<EOF
Stream marker: $(success_stream_marker "$SUCCESS_ISSUE")
Gate file: $(success_gate_path "$SUCCESS_ISSUE") must contain exactly: go
Artifact file: $(success_artifact_path "$SUCCESS_ISSUE")
Artifact text: $(success_artifact_text)

Requirements:
1. Immediately emit exactly: $(success_stream_marker "$SUCCESS_ISSUE")
2. Wait until the gate file exists with exactly the text go.
3. After the gate file is ready, create the artifact file with the exact artifact text followed by one trailing newline.
4. Verify the gate file and artifact file from the shell.
5. Mark the issue done with:
   $MAESTRO_BIN issue move $SUCCESS_ISSUE done --db $DB_PATH
6. Stop after the issue is marked done.
EOF
)"

update_issue_description "$RESUME_ISSUE" "$(cat <<EOF
Stream marker: $(resume_stream_marker "$RESUME_ISSUE")
Gate file: $(resume_gate_path "$RESUME_ISSUE") must contain exactly: go
Artifact file: $(resume_artifact_path "$RESUME_ISSUE")
Artifact text: $(resume_artifact_text)

Requirements:
1. Immediately emit exactly: $(resume_stream_marker "$RESUME_ISSUE")
2. Wait until the gate file exists with exactly the text go.
3. After the gate file is ready, create the artifact file with the exact artifact text followed by one trailing newline.
4. Verify the gate file and artifact file from the shell.
5. Mark the issue done with:
   $MAESTRO_BIN issue move $RESUME_ISSUE done --db $DB_PATH
6. Stop after the issue is marked done.
EOF
)"

update_issue_description "$INTERRUPT_ISSUE" "$(cat <<EOF
Stream marker: $(interrupt_stream_marker "$INTERRUPT_ISSUE")
Gate file: $(interrupt_gate_path "$INTERRUPT_ISSUE") must contain exactly: go

Requirements:
1. Immediately emit exactly: $(interrupt_stream_marker "$INTERRUPT_ISSUE")
2. Wait until the gate file exists with exactly the text go.
3. Do not change the issue state before the gate file exists.
4. If the gate file never appears, remain waiting.
EOF
)"

start_orchestrator

echo "Running success scenario for $SUCCESS_ISSUE"
"$MAESTRO_BIN" issue move "$SUCCESS_ISSUE" ready --db "$DB_PATH" >/dev/null
if ! wait_for_evidence_line "$CLAUDE_EVIDENCE_DIR/launch-1.summary.txt" "issue_identifier=$SUCCESS_ISSUE"; then
  echo "success scenario never produced launch-1 summary" >&2
  exit 1
fi
if ! wait_for_evidence_line "$CLAUDE_EVIDENCE_DIR/launch-1.summary.txt" "execution_stream_seen=true"; then
  echo "success scenario never exposed the live stream marker" >&2
  exit 1
fi
printf 'go\n' >"$(success_gate_path "$SUCCESS_ISSUE")"
if ! wait_for_done "$SUCCESS_ISSUE"; then
  echo "$SUCCESS_ISSUE did not reach done within ${TIMEOUT_SEC}s" >&2
  exit 1
fi
assert_file_content "$(success_artifact_path "$SUCCESS_ISSUE")" "$(success_artifact_text)"
assert_claude_runtime_evidence
SUCCESS_SESSION_ID="$(evidence_value "$CLAUDE_EVIDENCE_DIR/launch-1.summary.txt" "execution_thread_id")"
run_post_probe "$SUCCESS_ISSUE" 1 "success-final"
assert_success_snapshot "$SUCCESS_ISSUE" "$SUCCESS_SESSION_ID" "$CLAUDE_EVIDENCE_DIR/success-final.summary.txt"

echo "Running resume scenario for $RESUME_ISSUE"
"$MAESTRO_BIN" issue move "$RESUME_ISSUE" ready --db "$DB_PATH" >/dev/null
if ! wait_for_evidence_line "$CLAUDE_EVIDENCE_DIR/launch-2.summary.txt" "issue_identifier=$RESUME_ISSUE"; then
  echo "resume scenario never produced the first live summary" >&2
  exit 1
fi
if ! wait_for_evidence_line "$CLAUDE_EVIDENCE_DIR/launch-2.summary.txt" "execution_stream_seen=true"; then
  echo "resume scenario never exposed the first live stream marker" >&2
  exit 1
fi
RESUME_SESSION_ID="$(evidence_value "$CLAUDE_EVIDENCE_DIR/launch-2.summary.txt" "execution_thread_id")"
stop_orchestrator
start_orchestrator
if ! wait_for_evidence_line "$CLAUDE_EVIDENCE_DIR/launch-3.summary.txt" "issue_identifier=$RESUME_ISSUE"; then
  echo "resume scenario never produced the resumed live summary" >&2
  exit 1
fi
if [[ "$(args_resume_value "$CLAUDE_EVIDENCE_DIR/launch-3.args.txt")" != "$RESUME_SESSION_ID" ]]; then
  echo "expected resumed Claude launch to reuse session $RESUME_SESSION_ID" >&2
  exit 1
fi
printf 'go\n' >"$(resume_gate_path "$RESUME_ISSUE")"
if ! wait_for_done "$RESUME_ISSUE"; then
  echo "$RESUME_ISSUE did not reach done within ${TIMEOUT_SEC}s" >&2
  exit 1
fi
assert_file_content "$(resume_artifact_path "$RESUME_ISSUE")" "$(resume_artifact_text)"
if ! wait_for_runtime_event "$RESUME_ISSUE" "run_interrupted"; then
  echo "resume scenario never recorded run_interrupted" >&2
  exit 1
fi
if ! wait_for_runtime_event "$RESUME_ISSUE" "retry_scheduled"; then
  echo "resume scenario never recorded retry_scheduled" >&2
  exit 1
fi
run_post_probe "$RESUME_ISSUE" 3 "resume-final"
assert_success_snapshot "$RESUME_ISSUE" "$RESUME_SESSION_ID" "$CLAUDE_EVIDENCE_DIR/resume-final.summary.txt"

echo "Running interrupt scenario for $INTERRUPT_ISSUE"
"$MAESTRO_BIN" issue move "$INTERRUPT_ISSUE" ready --db "$DB_PATH" >/dev/null
if ! wait_for_evidence_line "$CLAUDE_EVIDENCE_DIR/launch-4.summary.txt" "issue_identifier=$INTERRUPT_ISSUE"; then
  echo "interrupt scenario never produced the live summary" >&2
  exit 1
fi
if ! wait_for_evidence_line "$CLAUDE_EVIDENCE_DIR/launch-4.summary.txt" "execution_stream_seen=true"; then
  echo "interrupt scenario never exposed the live stream marker" >&2
  exit 1
fi
"$MAESTRO_BIN" project stop "$PROJECT_ID" >/dev/null
if ! wait_for_runtime_event "$INTERRUPT_ISSUE" "run_stopped"; then
  echo "interrupt scenario never recorded run_stopped" >&2
  exit 1
fi
if ! wait_for_project_runtime_event "$PROJECT_ID" "project_stop_requested"; then
  echo "interrupt scenario never recorded project_stop_requested" >&2
  exit 1
fi
if ! wait_for_runtime_event "$INTERRUPT_ISSUE" "run_interrupted"; then
  echo "interrupt scenario never recorded run_interrupted" >&2
  exit 1
fi
if ! wait_for_execution_snapshot "$INTERRUPT_ISSUE" "run_interrupted" "run_interrupted"; then
  echo "interrupt scenario never persisted run_interrupted with stop_reason=run_interrupted" >&2
  exit 1
fi
run_post_probe "$INTERRUPT_ISSUE" 4 "interrupt-final"
assert_evidence_value "$CLAUDE_EVIDENCE_DIR/interrupt-final.summary.txt" "dashboard_session_status" "interrupted"
assert_evidence_value "$CLAUDE_EVIDENCE_DIR/interrupt-final.summary.txt" "dashboard_session_source" "persisted"
assert_evidence_value "$CLAUDE_EVIDENCE_DIR/interrupt-final.summary.txt" "dashboard_session_stop_reason" "run_interrupted"
assert_evidence_value "$CLAUDE_EVIDENCE_DIR/interrupt-final.summary.txt" "execution_failure_class" "run_interrupted"
assert_evidence_value "$CLAUDE_EVIDENCE_DIR/interrupt-final.summary.txt" "execution_stop_reason" "run_interrupted"
assert_evidence_value "$CLAUDE_EVIDENCE_DIR/interrupt-final.summary.txt" "execution_session_source" "persisted"
[[ "$(issue_execution_value "$INTERRUPT_ISSUE" "error")" == "run_interrupted" ]] || {
  echo "expected interrupted snapshot error=run_interrupted for $INTERRUPT_ISSUE" >&2
  exit 1
}
[[ "$(issue_execution_session_field "$INTERRUPT_ISSUE" "thread_id")" != "" ]] || {
  echo "expected interrupted snapshot to retain a persisted thread_id for $INTERRUPT_ISSUE" >&2
  exit 1
}
if [[ -e "$(interrupt_gate_path "$INTERRUPT_ISSUE")" ]]; then
  echo "did not expect interrupt gate file to be created" >&2
  exit 1
fi

echo "Real Claude lifecycle e2e flow completed successfully."
echo "Verified:"
echo "  success: $SUCCESS_ISSUE -> $(success_artifact_path "$SUCCESS_ISSUE")"
echo "  resume: $RESUME_ISSUE -> $(resume_artifact_path "$RESUME_ISSUE")"
echo "  interrupt: $INTERRUPT_ISSUE -> run_interrupted"
echo "  verify log: $VERIFY_LOG"
echo "  orchestrator log: $ORCH_LOG"
echo "  claude evidence dir: $CLAUDE_EVIDENCE_DIR"
