#!/usr/bin/env bash

ROOT_DIR="${ROOT_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
# shellcheck source=./e2e_real_codex_preflight.sh
source "$ROOT_DIR/scripts/lib/e2e_real_codex_preflight.sh"
HARNESS_LABEL="${HARNESS_LABEL:-maestro-real-claude-e2e}"
HARNESS_ROOT="${E2E_ROOT:-$(mktemp -d "${TMPDIR:-/tmp}/${HARNESS_LABEL}.XXXXXX")}"
BIN_DIR="${BIN_DIR:-$HARNESS_ROOT/bin}"
ARTIFACTS_DIR="${ARTIFACTS_DIR:-$HARNESS_ROOT/artifacts}"
WORKSPACES_DIR="${WORKSPACES_DIR:-$HARNESS_ROOT/workspaces}"
LOGS_DIR="${LOGS_DIR:-$HARNESS_ROOT/logs}"
DB_PATH="${DB_PATH:-$HARNESS_ROOT/.maestro/maestro.db}"
WORKFLOW_PATH="${WORKFLOW_PATH:-$HARNESS_ROOT/WORKFLOW.md}"
VERIFY_LOG="${VERIFY_LOG:-$HARNESS_ROOT/verify.log}"
ORCH_LOG="${ORCH_LOG:-$HARNESS_ROOT/orchestrator.log}"
MAESTRO_BIN="${MAESTRO_BIN:-$BIN_DIR/maestro}"
TIMEOUT_SEC="${E2E_TIMEOUT_SEC:-600}"
POLL_SEC="${E2E_POLL_SEC:-2}"
RUN_PORT="${E2E_PORT:-0}"
KEEP_HARNESS="${E2E_KEEP_HARNESS:-1}"
ORCH_PID="${ORCH_PID:-}"
CURRENT_ISSUE="${CURRENT_ISSUE:-}"

require_command_string() {
  local label="$1"
  local command_string="$2"
  require_command_from_shell_command "$label command" "$command_string"
}

ensure_harness_dirs() {
  mkdir -p "$BIN_DIR" "$ARTIFACTS_DIR" "$WORKSPACES_DIR" "$LOGS_DIR" "$(dirname "$DB_PATH")"
}

build_maestro() {
  echo "Building Maestro binary into $MAESTRO_BIN"
  go build -o "$MAESTRO_BIN" ./cmd/maestro
}

init_harness_repo() {
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

yaml_quote() {
  printf "%s" "$1" | sed "s/'/''/g"
}

issue_state() {
  "$MAESTRO_BIN" issue show "$1" --db "$DB_PATH" 2>/dev/null | awk -F': *' '/^State:/{print $2}' | tr -d '[:space:]'
}

issue_title() {
  "$MAESTRO_BIN" issue show "$1" --db "$DB_PATH" 2>/dev/null | awk -F': *' '/^Title:/{sub(/^ +/, "", $2); print $2}'
}

set_issue_permission_profile() {
  local issue_id="$1"
  local profile="$2"
  "$MAESTRO_BIN" issue update "$issue_id" --permission-profile "$profile" --db "$DB_PATH" >/dev/null
}

wait_for_done() {
  local issue_id="$1"
  local deadline
  CURRENT_ISSUE="$issue_id"
  deadline=$((SECONDS + TIMEOUT_SEC))
  while (( SECONDS < deadline )); do
    local state
    state="$(issue_state "$issue_id")"
    if [[ "$state" == "done" ]]; then
      return 0
    fi
    if [[ "$state" == "cancelled" ]]; then
      return 1
    fi
    sleep "$POLL_SEC"
  done
  return 1
}

assert_file_content() {
  local path="$1"
  local expected="$2"
  if [[ ! -f "$path" ]]; then
    echo "expected artifact missing: $path" >&2
    return 1
  fi
  local actual
  actual="$(cat "$path")"
  if [[ "$actual" != "$expected" ]]; then
    echo "artifact content mismatch for $path" >&2
    echo "expected: $expected" >&2
    echo "actual:   $actual" >&2
    return 1
  fi
}

start_project() {
  local project_id="$1"
  sqlite3 "$DB_PATH" "UPDATE projects SET state = 'running', updated_at = datetime('now') WHERE id = '$project_id';"
}

start_orchestrator() {
  echo "Starting orchestrator"
  : >"$ORCH_LOG"
  "$MAESTRO_BIN" run "$HARNESS_ROOT" \
    --workflow "$WORKFLOW_PATH" \
    --db "$DB_PATH" \
    --logs-root "$LOGS_DIR" \
    --port "$RUN_PORT" \
    --i-understand-that-this-will-be-running-without-the-usual-guardrails \
    >"$ORCH_LOG" 2>&1 &
  ORCH_PID="$!"
}

stop_orchestrator() {
  if [[ -n "$ORCH_PID" ]] && kill -0 "$ORCH_PID" >/dev/null 2>&1; then
    kill "$ORCH_PID" >/dev/null 2>&1 || true
    wait "$ORCH_PID" >/dev/null 2>&1 || true
  fi
  ORCH_PID=""
}

require_verify_check() {
  local label="$1"
  local pattern="$2"
  if ! grep -Eq "$pattern" "$VERIFY_LOG"; then
    echo "verify missing required Claude readiness check: $label" >&2
    return 1
  fi
}

run_claude_verify() {
  echo "Running maestro verify preflight"
  if ! "$MAESTRO_BIN" --json verify --repo "$HARNESS_ROOT" --db "$DB_PATH" >"$VERIFY_LOG" 2>&1; then
    echo "maestro verify failed for the Claude harness" >&2
    return 1
  fi
  require_verify_check "runtime_claude=ok" '"runtime_claude":"ok"'
  require_verify_check 'claude_auth_source_status=ok' '"claude_auth_source_status":"ok"'
  require_verify_check 'claude_auth_source is OAuth or cloud provider' '"claude_auth_source":"(OAuth|cloud provider)"'
  require_verify_check 'claude_session_status=ok' '"claude_session_status":"ok"'
  require_verify_check 'claude_session_bare_mode=ok' '"claude_session_bare_mode":"ok"'
  require_verify_check 'claude_session_additional_directories=ok' '"claude_session_additional_directories":"ok"'
}

print_failure_context() {
  echo "Harness root: $HARNESS_ROOT" >&2
  echo "Workflow path: $WORKFLOW_PATH" >&2
  echo "Database: $DB_PATH" >&2
  echo "Verify log: $VERIFY_LOG" >&2
  echo "Logs root: $LOGS_DIR" >&2
  echo "Orchestrator log: $ORCH_LOG" >&2
  echo "Workspaces root: $WORKSPACES_DIR" >&2
  echo "HTTP port: $RUN_PORT" >&2
  if [[ -n "$CURRENT_ISSUE" ]]; then
    echo "Issue: $CURRENT_ISSUE" >&2
    local title state
    title="$(issue_title "$CURRENT_ISSUE" || true)"
    state="$(issue_state "$CURRENT_ISSUE" || true)"
    if [[ -n "$title" ]]; then
      echo "Issue title: $title" >&2
    fi
    if [[ -n "$state" ]]; then
      echo "Current state: $state" >&2
    fi
  fi
  if [[ -f "$VERIFY_LOG" ]]; then
    echo "Last verify output:" >&2
    tail -n 50 "$VERIFY_LOG" >&2 || true
  fi
  if [[ -f "$ORCH_LOG" ]]; then
    echo "Last orchestrator output:" >&2
    tail -n 100 "$ORCH_LOG" >&2 || true
  fi
}

cleanup() {
  local exit_code="$?"
  stop_orchestrator
  if [[ "$exit_code" -eq 0 && "$KEEP_HARNESS" = "0" ]]; then
    rm -rf "$HARNESS_ROOT"
    return
  fi
  if [[ "$exit_code" -eq 0 ]]; then
    echo "Harness directory: $HARNESS_ROOT"
    return
  fi
  print_failure_context
}
