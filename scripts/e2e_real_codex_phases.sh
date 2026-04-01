#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HARNESS_ROOT="${E2E_ROOT:-$(mktemp -d "${TMPDIR:-/tmp}/maestro-real-codex-phases-e2e.XXXXXX")}"
BIN_DIR="$HARNESS_ROOT/bin"
ARTIFACTS_DIR="$HARNESS_ROOT/artifacts"
WORKSPACES_DIR="$HARNESS_ROOT/workspaces"
LOGS_DIR="$HARNESS_ROOT/logs"
DB_PATH="$HARNESS_ROOT/.maestro/maestro.db"
WORKFLOW_PATH="$HARNESS_ROOT/WORKFLOW.md"
ORCH_LOG="$HARNESS_ROOT/orchestrator.log"
MAESTRO_BIN="$BIN_DIR/maestro"
TIMEOUT_SEC="${E2E_TIMEOUT_SEC:-600}"
POLL_SEC="${E2E_POLL_SEC:-2}"
KEEP_HARNESS="${E2E_KEEP_HARNESS:-1}"
ORCH_PID=""

cd "$ROOT_DIR"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

cleanup() {
  local exit_code="$?"
  stop_orchestrator
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

issue_state() {
  "$MAESTRO_BIN" issue show "$1" --db "$DB_PATH" | awk -F': *' '/^State:/{print $2}' | tr -d '[:space:]'
}

issue_phase() {
  "$MAESTRO_BIN" issue show "$1" --db "$DB_PATH" | awk -F': *' '/^Phase:/{print $2}' | tr -d '[:space:]'
}

issue_title() {
  "$MAESTRO_BIN" issue show "$1" --db "$DB_PATH" | awk -F': *' '/^Title:/{sub(/^ +/, "", $2); print $2}'
}

wait_for_state_phase() {
  local issue_id="$1"
  local expected_state="$2"
  local expected_phase="$3"
  local deadline
  deadline=$((SECONDS + TIMEOUT_SEC))
  while (( SECONDS < deadline )); do
    local state phase
    state="$(issue_state "$issue_id")"
    phase="$(issue_phase "$issue_id")"
    if [[ "$state" == "$expected_state" && "$phase" == "$expected_phase" ]]; then
      return 0
    fi
    sleep "$POLL_SEC"
  done
  return 1
}

wait_for_file_content() {
  local path="$1"
  local expected="$2"
  local deadline
  deadline=$((SECONDS + TIMEOUT_SEC))
  while (( SECONDS < deadline )); do
    if [[ -f "$path" ]]; then
      local actual
      actual="$(cat "$path")"
      if [[ "$actual" == "$expected" ]]; then
        return 0
      fi
    fi
    sleep "$POLL_SEC"
  done
  return 1
}

wait_for_path_absent() {
  local path="$1"
  local deadline
  deadline=$((SECONDS + TIMEOUT_SEC))
  while (( SECONDS < deadline )); do
    if [[ ! -e "$path" ]]; then
      return 0
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

assert_file_absent() {
  local path="$1"
  if [[ -e "$path" ]]; then
    echo "expected path to be absent: $path" >&2
    return 1
  fi
}

assert_phase_log() {
  local path="$1"
  local expected="$2"
  assert_file_content "$path" "$expected"
}

start_project() {
  local project_id="$1"
  sqlite3 "$DB_PATH" "UPDATE projects SET state = 'running', updated_at = datetime('now') WHERE id = '$project_id';"
}

stop_orchestrator() {
  if [[ -n "$ORCH_PID" ]] && kill -0 "$ORCH_PID" >/dev/null 2>&1; then
    kill "$ORCH_PID" >/dev/null 2>&1 || true
    wait "$ORCH_PID" >/dev/null 2>&1 || true
  fi
  ORCH_PID=""
}

start_orchestrator() {
  : >"$ORCH_LOG"
  "$MAESTRO_BIN" run "$HARNESS_ROOT" \
    --workflow "$WORKFLOW_PATH" \
    --db "$DB_PATH" \
    --logs-root "$LOGS_DIR" \
    --i-understand-that-this-will-be-running-without-the-usual-guardrails \
    >>"$ORCH_LOG" 2>&1 &
  ORCH_PID="$!"
  sleep 1
}

update_issue_description() {
  local issue_id="$1"
  local description="$2"
  "$MAESTRO_BIN" issue update "$issue_id" --desc "$description" --db "$DB_PATH" >/dev/null
}

require_cmd go
require_cmd codex
require_cmd git
require_cmd sqlite3

mkdir -p "$BIN_DIR" "$ARTIFACTS_DIR" "$WORKSPACES_DIR" "$LOGS_DIR" "$(dirname "$DB_PATH")"

echo "Building Maestro binary into $MAESTRO_BIN"
go build -o "$MAESTRO_BIN" ./cmd/maestro

CODEX_COMMAND="${E2E_CODEX_COMMAND:-codex exec --dangerously-bypass-approvals-and-sandbox --skip-git-repo-check --cd . --add-dir $(printf '%q' "$HARNESS_ROOT") -}"
CODEX_COMMAND_YAML="$(yaml_quote "$CODEX_COMMAND")"

cat >"$WORKFLOW_PATH" <<EOF
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
    enabled: true
    prompt: |
      You are running the review phase for {{ issue.identifier }}.

      Shared artifacts directory: $ARTIFACTS_DIR
      Maestro binary: $MAESTRO_BIN
      Maestro database: $DB_PATH

      Requirements:
      1. Create $ARTIFACTS_DIR/{{ issue.identifier }}.review.txt with exactly this text: review complete for {{ issue.identifier }}
      2. Append exactly one line containing review to $ARTIFACTS_DIR/{{ issue.identifier }}.phases.log
      3. Verify both files from the shell.
      4. Move the issue to done with:
         $MAESTRO_BIN issue move {{ issue.identifier }} done --db $DB_PATH
      5. Stop after moving the issue to done.
  done:
    enabled: true
    prompt: |
      You are running the done phase for {{ issue.identifier }}.

      Shared artifacts directory: $ARTIFACTS_DIR

      Requirements:
      1. Create $ARTIFACTS_DIR/{{ issue.identifier }}.done.txt with exactly this text: done complete for {{ issue.identifier }}
      2. Append exactly one line containing done to $ARTIFACTS_DIR/{{ issue.identifier }}.phases.log
      3. Verify both files from the shell.
      4. Do not change the issue state away from done.
      5. Stop after verification succeeds.
orchestrator:
  max_concurrent_agents: 1
  max_turns: 1
  max_retry_backoff_ms: 5000
  dispatch_mode: parallel
runtime:
  default: codex-stdio
  codex-stdio:
    provider: codex
    transport: stdio
    command: '$CODEX_COMMAND_YAML'
    approval_policy: never
    read_timeout_ms: 5000
    turn_timeout_ms: 300000
---
You are running the Maestro phase end-to-end harness.

Current phase: {{ phase }}
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
2. Perform only the implementation-phase work in this prompt.
3. Verify every file you create with shell commands before changing issue state.
4. Stop immediately after the requested state transition succeeds.
EOF

PROJECT_ID="$("$MAESTRO_BIN" project create "Real Codex E2E Phase Project" --repo "$HARNESS_ROOT" --db "$DB_PATH" --quiet)"
start_project "$PROJECT_ID"

echo "Creating phase e2e issues in $DB_PATH"
ISSUE_REVIEW="$("$MAESTRO_BIN" issue create "Phase flow through review" --project "$PROJECT_ID" --desc "placeholder" --db "$DB_PATH" | sed -E 's/^Created issue ([^:]+): .*$/\1/')"
ISSUE_SKIP="$("$MAESTRO_BIN" issue create "Phase flow skipping review" --project "$PROJECT_ID" --desc "placeholder" --db "$DB_PATH" | sed -E 's/^Created issue ([^:]+): .*$/\1/')"

update_issue_description "$ISSUE_REVIEW" "$(cat <<EOF
Implementation-phase requirements for $ISSUE_REVIEW:
- Create $ARTIFACTS_DIR/$ISSUE_REVIEW.implementation.txt with exactly this text: implementation complete for $ISSUE_REVIEW
- Append exactly one line containing implementation to $ARTIFACTS_DIR/$ISSUE_REVIEW.phases.log
- Verify both files from the shell.
- Move the issue to in_review with:
  $MAESTRO_BIN issue move $ISSUE_REVIEW in_review --db $DB_PATH
- Stop after the issue is in_review.
EOF
)"

update_issue_description "$ISSUE_SKIP" "$(cat <<EOF
Implementation-phase requirements for $ISSUE_SKIP:
- Create $ARTIFACTS_DIR/$ISSUE_SKIP.implementation.txt with exactly this text: implementation complete for $ISSUE_SKIP
- Append exactly one line containing implementation to $ARTIFACTS_DIR/$ISSUE_SKIP.phases.log
- Verify both files from the shell.
- Move the issue directly to done with:
  $MAESTRO_BIN issue move $ISSUE_SKIP done --db $DB_PATH
- Do not use the review phase for this issue.
- Stop after the issue is done.
EOF
)"

echo "Starting orchestrator for review-path issue"
start_orchestrator
"$MAESTRO_BIN" issue move "$ISSUE_REVIEW" ready --db "$DB_PATH" >/dev/null

echo "Waiting for $ISSUE_REVIEW to finish implementation, review, and done"
if ! wait_for_file_content "$ARTIFACTS_DIR/$ISSUE_REVIEW.done.txt" "done complete for $ISSUE_REVIEW"; then
  echo "$ISSUE_REVIEW did not produce the done artifact within ${TIMEOUT_SEC}s" >&2
  echo "$ISSUE_REVIEW title: $(issue_title "$ISSUE_REVIEW")" >&2
  tail -n 150 "$ORCH_LOG" >&2 || true
  exit 1
fi
if ! wait_for_file_content "$ARTIFACTS_DIR/$ISSUE_REVIEW.phases.log" "$(printf 'implementation\nreview\ndone')"; then
  echo "$ISSUE_REVIEW did not produce the final phase log within ${TIMEOUT_SEC}s" >&2
  echo "$ISSUE_REVIEW title: $(issue_title "$ISSUE_REVIEW")" >&2
  tail -n 150 "$ORCH_LOG" >&2 || true
  exit 1
fi
if ! wait_for_state_phase "$ISSUE_REVIEW" "done" "complete"; then
  echo "$ISSUE_REVIEW did not settle at done/complete within ${TIMEOUT_SEC}s" >&2
  echo "$ISSUE_REVIEW title: $(issue_title "$ISSUE_REVIEW")" >&2
  tail -n 150 "$ORCH_LOG" >&2 || true
  exit 1
fi

assert_file_content "$ARTIFACTS_DIR/$ISSUE_REVIEW.implementation.txt" "implementation complete for $ISSUE_REVIEW"
assert_file_content "$ARTIFACTS_DIR/$ISSUE_REVIEW.review.txt" "review complete for $ISSUE_REVIEW"
assert_file_content "$ARTIFACTS_DIR/$ISSUE_REVIEW.done.txt" "done complete for $ISSUE_REVIEW"
assert_phase_log "$ARTIFACTS_DIR/$ISSUE_REVIEW.phases.log" "$(printf 'implementation\nreview\ndone')"

REVIEW_WORKSPACE="$WORKSPACES_DIR/$ISSUE_REVIEW"
stop_orchestrator

echo "Restarting orchestrator to verify complete-phase workspace cleanup"
start_orchestrator
if ! wait_for_path_absent "$REVIEW_WORKSPACE"; then
  echo "workspace for $ISSUE_REVIEW was not cleaned up after restart: $REVIEW_WORKSPACE" >&2
  tail -n 150 "$ORCH_LOG" >&2 || true
  exit 1
fi

"$MAESTRO_BIN" issue move "$ISSUE_SKIP" ready --db "$DB_PATH" >/dev/null

echo "Waiting for $ISSUE_SKIP to finish implementation and done"
if ! wait_for_file_content "$ARTIFACTS_DIR/$ISSUE_SKIP.done.txt" "done complete for $ISSUE_SKIP"; then
  echo "$ISSUE_SKIP did not produce the done artifact within ${TIMEOUT_SEC}s" >&2
  echo "$ISSUE_SKIP title: $(issue_title "$ISSUE_SKIP")" >&2
  tail -n 150 "$ORCH_LOG" >&2 || true
  exit 1
fi
if ! wait_for_file_content "$ARTIFACTS_DIR/$ISSUE_SKIP.phases.log" "$(printf 'implementation\ndone')"; then
  echo "$ISSUE_SKIP did not produce the final phase log within ${TIMEOUT_SEC}s" >&2
  echo "$ISSUE_SKIP title: $(issue_title "$ISSUE_SKIP")" >&2
  tail -n 150 "$ORCH_LOG" >&2 || true
  exit 1
fi
if ! wait_for_state_phase "$ISSUE_SKIP" "done" "complete"; then
  echo "$ISSUE_SKIP did not settle at done/complete within ${TIMEOUT_SEC}s" >&2
  echo "$ISSUE_SKIP title: $(issue_title "$ISSUE_SKIP")" >&2
  tail -n 150 "$ORCH_LOG" >&2 || true
  exit 1
fi

assert_file_content "$ARTIFACTS_DIR/$ISSUE_SKIP.implementation.txt" "implementation complete for $ISSUE_SKIP"
assert_file_absent "$ARTIFACTS_DIR/$ISSUE_SKIP.review.txt"
assert_file_content "$ARTIFACTS_DIR/$ISSUE_SKIP.done.txt" "done complete for $ISSUE_SKIP"
assert_phase_log "$ARTIFACTS_DIR/$ISSUE_SKIP.phases.log" "$(printf 'implementation\ndone')"

SKIP_WORKSPACE="$WORKSPACES_DIR/$ISSUE_SKIP"
stop_orchestrator

echo "Restarting orchestrator to verify cleanup for the skip-review path"
start_orchestrator
if ! wait_for_path_absent "$SKIP_WORKSPACE"; then
  echo "workspace for $ISSUE_SKIP was not cleaned up after restart: $SKIP_WORKSPACE" >&2
  tail -n 150 "$ORCH_LOG" >&2 || true
  exit 1
fi

stop_orchestrator

echo "Real Codex phase e2e flow completed successfully."
echo "Verified:"
echo "  $ISSUE_REVIEW -> implementation, review, done, then complete"
echo "  $ISSUE_SKIP -> implementation, done, then complete"
