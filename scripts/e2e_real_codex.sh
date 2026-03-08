#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HARNESS_ROOT="${E2E_ROOT:-$(mktemp -d "${TMPDIR:-/tmp}/symphony-real-codex-e2e.XXXXXX")}"
BIN_DIR="$HARNESS_ROOT/bin"
ARTIFACTS_DIR="$HARNESS_ROOT/artifacts"
WORKSPACES_DIR="$HARNESS_ROOT/workspaces"
LOGS_DIR="$HARNESS_ROOT/logs"
DB_PATH="$HARNESS_ROOT/.symphony/symphony.db"
WORKFLOW_PATH="$HARNESS_ROOT/WORKFLOW.md"
ORCH_LOG="$HARNESS_ROOT/orchestrator.log"
SYMPHONY_BIN="$BIN_DIR/symphony"
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

issue_state() {
  "$SYMPHONY_BIN" issue show "$1" --db "$DB_PATH" | awk -F': *' '/^State:/{print $2}' | tr -d '[:space:]'
}

issue_title() {
  "$SYMPHONY_BIN" issue show "$1" --db "$DB_PATH" | awk -F': *' '/^Title:/{sub(/^ +/, "", $2); print $2}'
}

wait_for_done() {
  local issue_id="$1"
  local deadline
  deadline=$((SECONDS + TIMEOUT_SEC))
  while (( SECONDS < deadline )); do
    local state
    state="$(issue_state "$issue_id")"
    if [[ "$state" == "done" ]]; then
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

require_cmd go
require_cmd codex
require_cmd git

mkdir -p "$BIN_DIR" "$ARTIFACTS_DIR" "$WORKSPACES_DIR" "$LOGS_DIR" "$(dirname "$DB_PATH")"

echo "Building Symphony binary into $SYMPHONY_BIN"
go build -o "$SYMPHONY_BIN" ./cmd/symphony

CODEX_COMMAND="${E2E_CODEX_COMMAND:-codex exec --dangerously-bypass-approvals-and-sandbox --skip-git-repo-check --cd . --add-dir $(printf '%q' "$HARNESS_ROOT") -}"
CODEX_COMMAND_YAML="$(yaml_quote "$CODEX_COMMAND")"

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
    git config user.name "Symphony E2E"
    git config user.email "e2e@example.com"
    printf '%s\n' '# Symphony E2E Workspace' > README.md
  timeout_ms: 10000
agent:
  max_concurrent_agents: 2
  max_turns: 1
  max_retry_backoff_ms: 5000
  mode: stdio
codex:
  command: '$CODEX_COMMAND_YAML'
  approval_policy: never
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
  read_timeout_ms: 5000
  turn_timeout_ms: 300000
---
You are running the Symphony real-Codex end-to-end harness.

Complete exactly one issue and then stop.

Issue identifier: {{ issue.identifier }}
Issue title: {{ issue.title }}
Issue description:
{{ issue.description }}

Environment:
- Current directory is an isolated issue workspace.
- Shared artifacts directory: $ARTIFACTS_DIR
- Symphony binary: $SYMPHONY_BIN
- Symphony database: $DB_PATH

Requirements:
1. Create the requested artifact in the shared artifacts directory, not only in the current workspace.
2. The file contents must match the requested text exactly, followed by one trailing newline.
3. Verify the file with shell commands before finishing.
4. Mark the issue done with this command after verification succeeds:
   $SYMPHONY_BIN issue move {{ issue.identifier }} done --db $DB_PATH
5. If the artifact is already correct, just verify it and mark the issue done.
6. Do not open a pull request.
7. Stop after the issue is marked done.
EOF

echo "Creating e2e issues in $DB_PATH"
ISSUE_ONE="$("$SYMPHONY_BIN" issue create "Create first e2e artifact" --desc "Create file artifact-one.txt in the shared artifacts directory with exactly this single line of text: symphony e2e ok 1" --db "$DB_PATH" | sed -E 's/^Created issue ([^:]+): .*$/\1/')"
ISSUE_TWO="$("$SYMPHONY_BIN" issue create "Create second e2e artifact" --desc "Create file artifact-two.txt in the shared artifacts directory with exactly this single line of text: symphony e2e ok 2" --db "$DB_PATH" | sed -E 's/^Created issue ([^:]+): .*$/\1/')"

"$SYMPHONY_BIN" issue move "$ISSUE_ONE" ready --db "$DB_PATH" >/dev/null
"$SYMPHONY_BIN" issue move "$ISSUE_TWO" ready --db "$DB_PATH" >/dev/null

echo "Starting orchestrator"
"$SYMPHONY_BIN" run "$HARNESS_ROOT" \
  --workflow "$WORKFLOW_PATH" \
  --db "$DB_PATH" \
  --logs-root "$LOGS_DIR" \
  --i-understand-that-this-will-be-running-without-the-usual-guardrails \
  >"$ORCH_LOG" 2>&1 &
ORCH_PID="$!"

echo "Waiting for $ISSUE_ONE and $ISSUE_TWO to reach done"
if ! wait_for_done "$ISSUE_ONE"; then
  echo "$ISSUE_ONE did not reach done within ${TIMEOUT_SEC}s" >&2
  echo "$ISSUE_ONE title: $(issue_title "$ISSUE_ONE")" >&2
  tail -n 100 "$ORCH_LOG" >&2 || true
  exit 1
fi
if ! wait_for_done "$ISSUE_TWO"; then
  echo "$ISSUE_TWO did not reach done within ${TIMEOUT_SEC}s" >&2
  echo "$ISSUE_TWO title: $(issue_title "$ISSUE_TWO")" >&2
  tail -n 100 "$ORCH_LOG" >&2 || true
  exit 1
fi

assert_file_content "$ARTIFACTS_DIR/artifact-one.txt" "symphony e2e ok 1"
assert_file_content "$ARTIFACTS_DIR/artifact-two.txt" "symphony e2e ok 2"

echo "Real Codex e2e flow completed successfully."
echo "Verified:"
echo "  $ISSUE_ONE -> $ARTIFACTS_DIR/artifact-one.txt"
echo "  $ISSUE_TWO -> $ARTIFACTS_DIR/artifact-two.txt"
