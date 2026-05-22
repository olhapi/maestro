#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HARNESS_ROOT="${E2E_ROOT:-$(mktemp -d "${TMPDIR:-/tmp}/maestro-real-codex-images-e2e.XXXXXX")}"
BIN_DIR="$HARNESS_ROOT/bin"
WORKSPACES_DIR="$HARNESS_ROOT/workspaces"
LOGS_DIR="$HARNESS_ROOT/logs"
DB_PATH="$HARNESS_ROOT/.maestro/maestro.db"
WORKFLOW_PATH="$HARNESS_ROOT/WORKFLOW.md"
ORCH_LOG="$HARNESS_ROOT/orchestrator.log"
MAESTRO_BIN="$BIN_DIR/maestro"
IMAGE_FIXTURE="${E2E_IMAGE_FIXTURE:-$ROOT_DIR/testdata/e2e/maestro-text.png}"
EXPECTED_TEXT="${E2E_EXPECTED_TEXT:-MAESTRO}"
TIMEOUT_SEC="${E2E_TIMEOUT_SEC:-600}"
POLL_SEC="${E2E_POLL_SEC:-2}"
RUN_PORT="${E2E_PORT:-$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)}"
KEEP_HARNESS="${E2E_KEEP_HARNESS:-1}"
ORCH_PID=""

cd "$ROOT_DIR"
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

issue_state() {
  "$MAESTRO_BIN" issue show "$1" --db "$DB_PATH" | awk -F': *' '/^State:/{print $2}' | tr -d '[:space:]'
}

issue_title() {
  "$MAESTRO_BIN" issue show "$1" --db "$DB_PATH" | awk -F': *' '/^Title:/{sub(/^ +/, "", $2); print $2}'
}

workspace_path_for_issue() {
  local issue_identifier="$1"
  python3 - "$DB_PATH" "$issue_identifier" <<'PY'
import sqlite3
import sys

db_path, issue_identifier = sys.argv[1:3]
conn = sqlite3.connect(db_path)
row = conn.execute(
    """
    SELECT w.path
    FROM workspaces AS w
    JOIN issues AS i ON i.id = w.issue_id
    WHERE i.identifier = ?
    LIMIT 1
    """,
    (issue_identifier,),
).fetchone()
conn.close()
if not row or not row[0]:
    raise SystemExit(1)
print(row[0])
PY
}

fixture_upload_path() {
  local fixture_path="$1"
  local fixture_name ext
  fixture_name="$(basename "$fixture_path")"
  ext=""
  if [[ "$fixture_name" == *.* && "$fixture_name" != .* ]]; then
    ext=".${fixture_name##*.}"
  fi
  printf '%s\n' "$HARNESS_ROOT/uploaded-issue-image$ext"
}

wait_for_done() {
  local issue_id="$1"
  local deadline
  deadline=$((SECONDS + TIMEOUT_SEC))
  while (( SECONDS < deadline )); do
    if [[ "$(issue_state "$issue_id")" == "done" ]]; then
      return 0
    fi
    sleep "$POLL_SEC"
  done
  return 1
}

start_project() {
  local project_id="$1"
  local deadline
  deadline=$((SECONDS + TIMEOUT_SEC))
  while (( SECONDS < deadline )); do
    if "$MAESTRO_BIN" project start "$project_id" --db "$DB_PATH" --api-url "http://127.0.0.1:$RUN_PORT" --quiet >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_for_final_answer_file() {
  local issue_identifier="$1"
  local output_path="$2"
  local deadline
  deadline=$((SECONDS + TIMEOUT_SEC))
  while (( SECONDS < deadline )); do
    if python3 - "$DB_PATH" "$issue_identifier" "$output_path" <<'PY'
import json
import pathlib
import sqlite3
import sys

db_path, issue_identifier, output_path = sys.argv[1:4]
conn = sqlite3.connect(db_path)
row = conn.execute(
    """
    SELECT raw_payload_json
    FROM issue_activity_entries
    WHERE identifier = ?
      AND kind = 'agent'
      AND phase = 'final_answer'
    ORDER BY attempt DESC, seq DESC
    LIMIT 1
    """,
    (issue_identifier,),
).fetchone()
conn.close()
if not row or not row[0]:
    raise SystemExit(1)
payload = json.loads(row[0])
text = (payload.get("item") or {}).get("text")
if text is None:
    raise SystemExit(1)
pathlib.Path(output_path).write_text(text, encoding="utf-8")
PY
    then
      return 0
    fi
    sleep "$POLL_SEC"
  done
  return 1
}

assert_file_exists() {
  local path="$1"
  if [[ ! -f "$path" ]]; then
    echo "expected file missing: $path" >&2
    return 1
  fi
}

assert_files_identical() {
  local expected="$1"
  local actual="$2"
  local label="$3"
  if ! python3 - "$expected" "$actual" <<'PY'
import pathlib
import sys

expected_path, actual_path = sys.argv[1:3]
expected = pathlib.Path(expected_path).read_bytes()
actual = pathlib.Path(actual_path).read_bytes()
raise SystemExit(0 if expected == actual else 1)
PY
  then
    echo "$label mismatch" >&2
    echo "expected file: $expected" >&2
    echo "actual file:   $actual" >&2
    return 1
  fi
}

text_file_repr() {
  local path="$1"
  python3 - "$path" <<'PY'
import pathlib
import sys

print(repr(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8", errors="replace")))
PY
}

find_staged_image_path() {
  local workspace_path="$1"
  local image_id="$2"
  local stage_dir="$workspace_path/.maestro/issue-assets"
  local match_count=0
  local match_path=""
  local path
  if [[ ! -d "$stage_dir" ]]; then
    echo "expected staging directory missing: $stage_dir" >&2
    return 1
  fi
  while IFS= read -r path; do
    match_path="$path"
    match_count=$((match_count + 1))
  done < <(find "$stage_dir" -type f -name "${image_id}*" | sort)
  if [[ "$match_count" -ne 1 ]]; then
    echo "expected exactly one staged image for $image_id, found $match_count in $stage_dir" >&2
    return 1
  fi
  printf '%s\n' "$match_path"
}

require_cmd go
require_cmd codex
require_cmd git
require_cmd python3

if [[ ! -f "$IMAGE_FIXTURE" ]]; then
  echo "missing image fixture: $IMAGE_FIXTURE" >&2
  exit 1
fi

mkdir -p "$BIN_DIR" "$WORKSPACES_DIR" "$LOGS_DIR" "$(dirname "$DB_PATH")"

echo "Building Maestro binary into $MAESTRO_BIN"
"$ENSURE_DASHBOARD_DIST_BIN"
go build -o "$MAESTRO_BIN" ./cmd/maestro

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
agent:
  max_concurrent_agents: 1
  max_turns: 1
  max_retry_backoff_ms: 5000
  mode: app_server
codex:
  # Pin the app-server model so multimodal behavior stays deterministic across
  # local ChatGPT logins and GitHub Actions API-key runs.
  command: codex app-server -c 'model="gpt-5.3-codex"'
  approval_policy: never
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
  read_timeout_ms: 5000
  turn_timeout_ms: 300000
  stall_timeout_ms: 300000
---
You are running the Maestro real-Codex app-server image harness.

The issue has one attached image. Read that image and determine the exact text shown in it.

Rules:
1. Your final answer must be only the exact text shown in the attached image.
2. Preserve capitalization exactly.
3. Do not add punctuation, explanations, or any other words.
4. Before sending the final answer, mark the issue done with this command:
   $MAESTRO_BIN issue move {{ issue.identifier }} done --db $DB_PATH
5. Do not create any files.
6. Do not mention image paths or the issue identifier in the final answer.

Issue identifier: {{ issue.identifier }}
Issue title: {{ issue.title }}
Issue description:
{{ issue.description }}
EOF

init_git_repo "$HARNESS_ROOT"

PROJECT_ID="$("$MAESTRO_BIN" project create "Image E2E Project" --repo "$HARNESS_ROOT" --db "$DB_PATH" --quiet)"

echo "Creating image e2e issue in $DB_PATH"
ISSUE_IDENTIFIER="$("$MAESTRO_BIN" issue create "Read attached image text" --project "$PROJECT_ID" --desc "Inspect the attached image and answer with only the text shown in it." --db "$DB_PATH" --quiet)"
UPLOAD_IMAGE_PATH="$(fixture_upload_path "$IMAGE_FIXTURE")"
cp "$IMAGE_FIXTURE" "$UPLOAD_IMAGE_PATH"
IMAGE_ID="$("$MAESTRO_BIN" issue images add "$ISSUE_IDENTIFIER" "$UPLOAD_IMAGE_PATH" --db "$DB_PATH" --quiet)"

"$MAESTRO_BIN" issue move "$ISSUE_IDENTIFIER" ready --db "$DB_PATH" >/dev/null

echo "Starting orchestrator in app-server mode"
"$MAESTRO_BIN" run "$HARNESS_ROOT" \
  --workflow "$WORKFLOW_PATH" \
  --db "$DB_PATH" \
  --port "$RUN_PORT" \
  --logs-root "$LOGS_DIR" \
  --i-understand-that-this-will-be-running-without-the-usual-guardrails \
  >"$ORCH_LOG" 2>&1 &
ORCH_PID="$!"

if ! start_project "$PROJECT_ID"; then
  echo "failed to start project $PROJECT_ID through the live API on port $RUN_PORT" >&2
  tail -n 150 "$ORCH_LOG" >&2 || true
  exit 1
fi

echo "Waiting for $ISSUE_IDENTIFIER to reach done"
if ! wait_for_done "$ISSUE_IDENTIFIER"; then
  echo "$ISSUE_IDENTIFIER did not reach done within ${TIMEOUT_SEC}s" >&2
  echo "$ISSUE_IDENTIFIER title: $(issue_title "$ISSUE_IDENTIFIER")" >&2
  tail -n 150 "$ORCH_LOG" >&2 || true
  exit 1
fi

if ! WORKSPACE_PATH="$(workspace_path_for_issue "$ISSUE_IDENTIFIER")"; then
  echo "failed to resolve workspace path for $ISSUE_IDENTIFIER from $DB_PATH" >&2
  exit 1
fi
STAGED_IMAGE_PATH="$(find_staged_image_path "$WORKSPACE_PATH" "$IMAGE_ID")"
assert_file_exists "$STAGED_IMAGE_PATH"
assert_files_identical "$IMAGE_FIXTURE" "$STAGED_IMAGE_PATH" "staged image"

ACTUAL_FINAL_ANSWER_PATH="$HARNESS_ROOT/final-answer.txt"
EXPECTED_FINAL_ANSWER_PATH="$HARNESS_ROOT/expected-final-answer.txt"
if ! wait_for_final_answer_file "$ISSUE_IDENTIFIER" "$ACTUAL_FINAL_ANSWER_PATH"; then
  echo "no persisted final answer payload found for $ISSUE_IDENTIFIER within ${TIMEOUT_SEC}s" >&2
  tail -n 150 "$ORCH_LOG" >&2 || true
  exit 1
fi
printf '%s' "$EXPECTED_TEXT" >"$EXPECTED_FINAL_ANSWER_PATH"
if ! assert_files_identical "$EXPECTED_FINAL_ANSWER_PATH" "$ACTUAL_FINAL_ANSWER_PATH" "final answer"; then
  echo "unexpected final answer for $ISSUE_IDENTIFIER" >&2
  echo "expected: $(text_file_repr "$EXPECTED_FINAL_ANSWER_PATH")" >&2
  echo "actual:   $(text_file_repr "$ACTUAL_FINAL_ANSWER_PATH")" >&2
  tail -n 150 "$ORCH_LOG" >&2 || true
  exit 1
fi
FINAL_ANSWER="$(cat "$ACTUAL_FINAL_ANSWER_PATH")"

echo "Real Codex app-server image e2e flow completed successfully."
echo "Verified:"
echo "  $ISSUE_IDENTIFIER -> final answer $FINAL_ANSWER"
echo "  staged image -> $STAGED_IMAGE_PATH"
