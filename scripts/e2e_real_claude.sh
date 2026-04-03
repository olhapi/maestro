#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=./lib/e2e_real_claude_harness.sh
source "$ROOT_DIR/scripts/lib/e2e_real_claude_harness.sh"

trap cleanup EXIT INT TERM

CLAUDE_COMMAND="${E2E_CLAUDE_COMMAND:-claude}"
EXPECTED_ARTIFACT_NAME="claude-artifact.txt"
EXPECTED_ARTIFACT_TEXT="maestro claude e2e ok"

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
You are running the Maestro real-Claude end-to-end harness.

Complete exactly one issue and then stop.

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
1. Create the requested artifact in the shared artifacts directory, not only in the current workspace.
2. The file contents must match the requested text exactly, followed by one trailing newline.
3. Verify the file with shell commands before finishing.
4. Mark the issue done with this command after verification succeeds:
   $MAESTRO_BIN issue move {{ issue.identifier }} done --db $DB_PATH
5. If the artifact is already correct, just verify it and mark the issue done.
6. Do not open a pull request.
7. Stop after the issue is marked done.
EOF

init_harness_repo "$HARNESS_ROOT"
run_claude_verify

PROJECT_ID="$("$MAESTRO_BIN" project create "Real Claude E2E Project" --repo "$HARNESS_ROOT" --db "$DB_PATH" --quiet)"
start_project "$PROJECT_ID"

echo "Creating Claude e2e issue in $DB_PATH"
ISSUE_ID="$("$MAESTRO_BIN" issue create "Create Claude e2e artifact" --project "$PROJECT_ID" --desc "Create file $EXPECTED_ARTIFACT_NAME in the shared artifacts directory with exactly this single line of text: $EXPECTED_ARTIFACT_TEXT" --db "$DB_PATH" --quiet)"
CURRENT_ISSUE="$ISSUE_ID"
set_issue_permission_profile "$ISSUE_ID" full-access
"$MAESTRO_BIN" issue move "$ISSUE_ID" ready --db "$DB_PATH" >/dev/null

start_orchestrator

echo "Waiting for $ISSUE_ID to reach done"
if ! wait_for_done "$ISSUE_ID"; then
  echo "$ISSUE_ID did not reach done within ${TIMEOUT_SEC}s" >&2
  exit 1
fi

assert_file_content "$ARTIFACTS_DIR/$EXPECTED_ARTIFACT_NAME" "$EXPECTED_ARTIFACT_TEXT"
assert_claude_runtime_evidence

echo "Real Claude e2e flow completed successfully."
echo "Verified:"
echo "  $ISSUE_ID -> $ARTIFACTS_DIR/$EXPECTED_ARTIFACT_NAME"
echo "  verify log: $VERIFY_LOG"
echo "  orchestrator log: $ORCH_LOG"
echo "  claude evidence: $CLAUDE_EVIDENCE_SUMMARY"
