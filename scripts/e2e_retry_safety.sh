#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HARNESS_ROOT="${E2E_ROOT:-$(mktemp -d "${TMPDIR:-/tmp}/maestro-retry-safety-e2e.XXXXXX")}"
BIN_DIR="$HARNESS_ROOT/bin"
WORKSPACES_DIR="$HARNESS_ROOT/workspaces"
LOGS_DIR="$HARNESS_ROOT/logs"
REPOS_DIR="$HARNESS_ROOT/repos"
DB_PATH="$HARNESS_ROOT/.maestro/maestro.db"
DAEMON_REGISTRY_DIR="$HARNESS_ROOT/.maestro-daemons"
ORCH_LOG="$HARNESS_ROOT/orchestrator.log"
MAESTRO_BIN="$BIN_DIR/maestro"
FAKE_APPSERVER_BIN="$BIN_DIR/maestro-fake-appserver"
TIMEOUT_SEC="${E2E_TIMEOUT_SEC:-180}"
POLL_SEC="${E2E_POLL_SEC:-1}"
KEEP_HARNESS="${E2E_KEEP_HARNESS:-1}"
HTTP_PORT="${E2E_HTTP_PORT:-0}"
ORCH_PID=""

cd "$ROOT_DIR"
export MAESTRO_DAEMON_REGISTRY_DIR="$DAEMON_REGISTRY_DIR"
ENSURE_DASHBOARD_DIST_BIN="${MAESTRO_ENSURE_DASHBOARD_DIST_BIN:-$ROOT_DIR/scripts/ensure_dashboard_dist.sh}"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

init_git_repo() {
  local repo_path="$1"
  (
    cd "$repo_path"
    unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_COMMON_DIR GIT_PREFIX
    git init -q
    git config user.name "Maestro E2E"
    git config user.email "e2e@example.com"
    git add WORKFLOW.md
    git commit -q -m "test init"
    git branch -M main
  )
}

cleanup() {
  local exit_code="$?"
  if [[ -n "$ORCH_PID" ]] && kill -0 "$ORCH_PID" >/dev/null 2>&1; then
    kill "$ORCH_PID" >/dev/null 2>&1 || true
    wait "$ORCH_PID" >/dev/null 2>&1 || true
  fi
  if [[ "$exit_code" -eq 0 && "$KEEP_HARNESS" = "0" ]]; then
    rm -rf "$HARNESS_ROOT"
  else
    echo "Harness directory: $HARNESS_ROOT"
  fi
}
trap cleanup EXIT INT TERM

yaml_quote() {
  printf "%s" "$1" | sed "s/'/''/g"
}

sql_value() {
  local sql="$1"
  sqlite3 -noheader "$DB_PATH" ".timeout 5000" "$sql"
}

issue_id() {
  sql_value "SELECT id FROM issues WHERE identifier = '$1';"
}

issue_run_count() {
  local id
  id="$(issue_id "$1")"
  sql_value "SELECT COALESCE((SELECT run_count FROM workspaces WHERE issue_id = '$id'), 0);"
}

issue_state() {
  "$MAESTRO_BIN" issue show "$1" --db "$DB_PATH" | awk -F': *' '/^State:/{print $2}' | tr -d '[:space:]'
}

latest_pause_reason() {
  local id
  id="$(issue_id "$1")"
  sql_value "SELECT error FROM runtime_events WHERE issue_id = '$id' AND kind = 'retry_paused' ORDER BY seq DESC LIMIT 1;"
}

latest_snapshot_error() {
  local id
  id="$(issue_id "$1")"
  sql_value "SELECT error FROM issue_execution_sessions WHERE issue_id = '$id' LIMIT 1;"
}

latest_snapshot_kind() {
  local id
  id="$(issue_id "$1")"
  sql_value "SELECT run_kind FROM issue_execution_sessions WHERE issue_id = '$id' LIMIT 1;"
}

pending_retry_count() {
  local id
  id="$(issue_id "$1")"
  sql_value "SELECT COUNT(*) FROM runtime_events WHERE issue_id = '$id' AND kind = 'retry_scheduled' AND seq > COALESCE((SELECT MAX(seq) FROM runtime_events WHERE issue_id = '$id' AND kind = 'retry_paused'), 0);"
}

non_positive_delay_count() {
  local id
  id="$(issue_id "$1")"
  sql_value "SELECT COUNT(*) FROM runtime_events WHERE issue_id = '$id' AND kind = 'retry_scheduled' AND CAST(COALESCE(json_extract(payload_json, '$.delay_ms'), 0) AS INTEGER) <= 0;"
}

wait_for_pause() {
  local identifier="$1"
  local expected_reason="$2"
  local expected_state="$3"
  local deadline
  deadline=$((SECONDS + TIMEOUT_SEC))
  while (( SECONDS < deadline )); do
    local reason
    reason="$(latest_pause_reason "$identifier")"
    if [[ "$reason" == "$expected_reason" && "$(issue_state "$identifier")" == "$expected_state" && "$(pending_retry_count "$identifier")" == "0" ]]; then
      return 0
    fi
    sleep "$POLL_SEC"
  done
  return 1
}

assert_equals() {
  local expected="$1"
  local actual="$2"
  local message="$3"
  if [[ "$expected" != "$actual" ]]; then
    echo "$message: expected '$expected', got '$actual'" >&2
    exit 1
  fi
}

assert_int_le() {
  local threshold="$1"
  local actual="$2"
  local message="$3"
  if (( actual > threshold )); then
    echo "$message: expected <= $threshold, got $actual" >&2
    exit 1
  fi
}

create_project_workflow() {
  local name="$1"
  local scenario="$2"
  local review_enabled="$3"
  local max_automatic_retries="$4"
  local turn_timeout_ms="$5"
  local stall_timeout_ms="$6"
  local repo_path="$REPOS_DIR/$name"
  local workflow_path="$repo_path/WORKFLOW.md"
  local command

  mkdir -p "$repo_path"
  command="$FAKE_APPSERVER_BIN --scenario $scenario"
  cat >"$workflow_path" <<EOF
---
tracker:
  kind: kanban
  active_states:
    - ready
    - in_progress
    - in_review
  terminal_states:
    - done
    - cancelled
polling:
  interval_ms: 100
workspace:
  root: $WORKSPACES_DIR
hooks:
  timeout_ms: 1000
phases:
  review:
    enabled: $review_enabled
  done:
    enabled: false
agent:
  max_concurrent_agents: 1
  max_turns: 1
  max_retry_backoff_ms: 50
  max_automatic_retries: $max_automatic_retries
  mode: app_server
codex:
  command: '$(yaml_quote "$command")'
  approval_policy: never
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
  read_timeout_ms: 1000
  turn_timeout_ms: $turn_timeout_ms
  stall_timeout_ms: $stall_timeout_ms
---
Retry safety harness for {{ issue.identifier }}.
EOF
  init_git_repo "$repo_path"

  "$MAESTRO_BIN" project create "$name" \
    --repo "$repo_path" \
    --workflow "$workflow_path" \
    --db "$DB_PATH" \
    --quiet
}

start_project() {
  local id="$1"
  # Projects are created stopped by default; mark them running so the shared
  # orchestrator can dispatch the harness issues.
  sqlite3 "$DB_PATH" ".timeout 5000" "UPDATE projects SET state = 'running', updated_at = datetime('now') WHERE id = '$id';"
}

require_cmd go
require_cmd sqlite3
require_cmd git

mkdir -p "$BIN_DIR" "$WORKSPACES_DIR" "$LOGS_DIR" "$REPOS_DIR" "$(dirname "$DB_PATH")"

echo "Building Maestro binary into $MAESTRO_BIN"
"$ENSURE_DASHBOARD_DIST_BIN"
go build -o "$MAESTRO_BIN" ./cmd/maestro
echo "Building fake app-server into $FAKE_APPSERVER_BIN"
go build -o "$FAKE_APPSERVER_BIN" ./cmd/maestro-fake-appserver

INPUT_PROJECT_ID="$(create_project_workflow "input-project" "input" "false" "8" "1500" "1500")"
NO_TRANSITION_PROJECT_ID="$(create_project_workflow "no-transition-project" "complete" "false" "8" "1500" "1500")"
STALL_PROJECT_ID="$(create_project_workflow "stall-project" "stall" "false" "8" "1500" "250")"

start_project "$INPUT_PROJECT_ID"
start_project "$NO_TRANSITION_PROJECT_ID"
start_project "$STALL_PROJECT_ID"

INPUT_ISSUE="$("$MAESTRO_BIN" issue create "Input required retry safety" --project "$INPUT_PROJECT_ID" --db "$DB_PATH" --quiet)"
NO_TRANSITION_ISSUE="$("$MAESTRO_BIN" issue create "No transition retry safety" --project "$NO_TRANSITION_PROJECT_ID" --db "$DB_PATH" --quiet)"
STALL_ISSUE="$("$MAESTRO_BIN" issue create "Stall retry safety" --project "$STALL_PROJECT_ID" --db "$DB_PATH" --quiet)"

"$MAESTRO_BIN" issue move "$INPUT_ISSUE" ready --db "$DB_PATH" >/dev/null
"$MAESTRO_BIN" issue move "$NO_TRANSITION_ISSUE" ready --db "$DB_PATH" >/dev/null
"$MAESTRO_BIN" issue move "$STALL_ISSUE" ready --db "$DB_PATH" >/dev/null

echo "Starting shared orchestrator"
"$MAESTRO_BIN" run \
  --db "$DB_PATH" \
  --logs-root "$LOGS_DIR" \
  --port "$HTTP_PORT" \
  --i-understand-that-this-will-be-running-without-the-usual-guardrails \
  >"$ORCH_LOG" 2>&1 &
ORCH_PID="$!"

if ! wait_for_pause "$INPUT_ISSUE" "turn_input_required" "in_progress"; then
  echo "$INPUT_ISSUE did not pause with turn_input_required" >&2
  tail -n 100 "$ORCH_LOG" >&2 || true
  exit 1
fi

if ! wait_for_pause "$NO_TRANSITION_ISSUE" "no_state_transition" "in_progress"; then
  echo "$NO_TRANSITION_ISSUE did not pause with no_state_transition" >&2
  tail -n 100 "$ORCH_LOG" >&2 || true
  exit 1
fi

if ! wait_for_pause "$STALL_ISSUE" "stall_timeout" "in_progress"; then
  echo "$STALL_ISSUE did not pause with stall_timeout" >&2
  tail -n 100 "$ORCH_LOG" >&2 || true
  exit 1
fi

assert_equals "retry_paused" "$(latest_snapshot_kind "$INPUT_ISSUE")" "$INPUT_ISSUE snapshot kind"
assert_equals "turn_input_required" "$(latest_snapshot_error "$INPUT_ISSUE")" "$INPUT_ISSUE snapshot error"
assert_equals "retry_paused" "$(latest_snapshot_kind "$NO_TRANSITION_ISSUE")" "$NO_TRANSITION_ISSUE snapshot kind"
assert_equals "no_state_transition" "$(latest_snapshot_error "$NO_TRANSITION_ISSUE")" "$NO_TRANSITION_ISSUE snapshot error"
assert_equals "retry_paused" "$(latest_snapshot_kind "$STALL_ISSUE")" "$STALL_ISSUE snapshot kind"
assert_equals "stall_timeout" "$(latest_snapshot_error "$STALL_ISSUE")" "$STALL_ISSUE snapshot error"

assert_int_le 1 "$(issue_run_count "$INPUT_ISSUE")" "$INPUT_ISSUE run count"
assert_int_le 1 "$(issue_run_count "$NO_TRANSITION_ISSUE")" "$NO_TRANSITION_ISSUE run count"
assert_int_le 3 "$(issue_run_count "$STALL_ISSUE")" "$STALL_ISSUE run count"

assert_equals "0" "$(pending_retry_count "$INPUT_ISSUE")" "$INPUT_ISSUE pending retry count"
assert_equals "0" "$(pending_retry_count "$NO_TRANSITION_ISSUE")" "$NO_TRANSITION_ISSUE pending retry count"
assert_equals "0" "$(pending_retry_count "$STALL_ISSUE")" "$STALL_ISSUE pending retry count"

assert_equals "0" "$(non_positive_delay_count "$STALL_ISSUE")" "$STALL_ISSUE non-positive retry delay count"

if grep -q "database is locked" "$ORCH_LOG"; then
  echo "detected database lock errors in orchestrator log" >&2
  tail -n 100 "$ORCH_LOG" >&2 || true
  exit 1
fi

echo "Retry safety e2e completed successfully."
echo "Verified:"
echo "  $INPUT_ISSUE -> paused with turn_input_required after $(issue_run_count "$INPUT_ISSUE") run"
echo "  $NO_TRANSITION_ISSUE -> paused with no_state_transition after $(issue_run_count "$NO_TRANSITION_ISSUE") run"
echo "  $STALL_ISSUE -> paused with stall_timeout after $(issue_run_count "$STALL_ISSUE") runs"
