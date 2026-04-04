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
DAEMON_REGISTRY_DIR="${DAEMON_REGISTRY_DIR:-$HARNESS_ROOT/.maestro-daemons}"
CLAUDE_EVIDENCE_DIR="${CLAUDE_EVIDENCE_DIR:-$HARNESS_ROOT/claude-support}"
DB_PATH="${DB_PATH:-$HARNESS_ROOT/.maestro/maestro.db}"
WORKFLOW_PATH="${WORKFLOW_PATH:-$HARNESS_ROOT/WORKFLOW.md}"
WORKFLOW_INIT_LOG="${WORKFLOW_INIT_LOG:-$HARNESS_ROOT/workflow-init.log}"
SPEC_CHECK_LOG="${SPEC_CHECK_LOG:-$HARNESS_ROOT/spec-check.log}"
VERIFY_LOG="${VERIFY_LOG:-$HARNESS_ROOT/verify.log}"
DOCTOR_LOG="${DOCTOR_LOG:-$HARNESS_ROOT/doctor.log}"
ORCH_LOG="${ORCH_LOG:-$HARNESS_ROOT/orchestrator.log}"
MAESTRO_BIN="${MAESTRO_BIN:-$BIN_DIR/maestro}"
CLAUDE_PROBE_BIN="${CLAUDE_PROBE_BIN:-$BIN_DIR/maestro-claude-e2e-probe}"
CLAUDE_WRAPPER_BIN="${CLAUDE_WRAPPER_BIN:-$BIN_DIR/claude-e2e-wrapper}"
CLAUDE_EVIDENCE_SUMMARY="${CLAUDE_EVIDENCE_SUMMARY:-$CLAUDE_EVIDENCE_DIR/launch-1.summary.txt}"
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

default_codex_command() {
  if [[ -n "${E2E_CODEX_COMMAND:-}" ]]; then
    printf '%s\n' "$E2E_CODEX_COMMAND"
    return 0
  fi
  if command -v codex >/dev/null 2>&1; then
    printf '%s\n' "codex app-server"
    return 0
  fi
  printf '%s\n' "npx -y @openai/codex@0.118.0 app-server"
}

ensure_harness_dirs() {
  mkdir -p "$BIN_DIR" "$ARTIFACTS_DIR" "$WORKSPACES_DIR" "$LOGS_DIR" "$DAEMON_REGISTRY_DIR" "$CLAUDE_EVIDENCE_DIR" "$(dirname "$DB_PATH")"
}

build_maestro() {
  echo "Building Maestro binary into $MAESTRO_BIN"
  go build -o "$MAESTRO_BIN" ./cmd/maestro
}

build_claude_probe() {
  echo "Building Claude harness probe into $CLAUDE_PROBE_BIN"
  go build -o "$CLAUDE_PROBE_BIN" ./cmd/maestro-claude-e2e-probe
}

init_harness_repo() {
  local repo_path="$1"
  (
    cd "$repo_path"
    unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_COMMON_DIR GIT_PREFIX
    git init -q
    git config user.name "Maestro E2E"
    git config user.email "e2e@example.com"
    sync_harness_codex_schemas "$repo_path"
    git add WORKFLOW.md
    git commit -q -m "test init"
    git branch -M main
  )
}

codex_schema_version() {
  local metadata_file="$ROOT_DIR/internal/codexschema/metadata.go"
  local version

  version="$(sed -n 's/^[[:space:]]*SupportedVersion = "\(.*\)"/\1/p' "$metadata_file")"
  if [[ -z "$version" ]]; then
    echo "failed to determine supported Codex schema version from $metadata_file" >&2
    return 1
  fi
  printf '%s\n' "$version"
}

sync_harness_codex_schemas() {
  local repo_path="$1"
  local version source_dir dest_parent dest_dir

  version="$(codex_schema_version)" || return 1
  source_dir="$ROOT_DIR/schemas/codex/$version/json"
  dest_parent="$repo_path/schemas/codex/$version"
  dest_dir="$dest_parent/json"

  if [[ ! -d "$source_dir" ]]; then
    echo "missing Codex schema source directory: $source_dir" >&2
    return 1
  fi

  mkdir -p "$dest_parent"
  rm -rf "$dest_dir"
  cp -R "$source_dir" "$dest_dir"
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

require_output_contains() {
  local label="$1"
  local log_file="$2"
  local expected="$3"
  if ! grep -Fq -- "$expected" "$log_file"; then
    echo "$label missing expected output: $expected" >&2
    return 1
  fi
}

require_output_matches() {
  local label="$1"
  local log_file="$2"
  local pattern="$3"
  if ! grep -Eq "$pattern" "$log_file"; then
    echo "$label missing expected pattern: $pattern" >&2
    return 1
  fi
}

run_claude_workflow_init() {
  local codex_command="$1"
  echo "Running maestro workflow init bootstrap"
  if ! "$MAESTRO_BIN" --db "$DB_PATH" workflow init "$HARNESS_ROOT" --defaults --runtime-command "$codex_command" >"$WORKFLOW_INIT_LOG" 2>&1; then
    echo "maestro workflow init failed for the Claude harness" >&2
    return 1
  fi
  require_output_contains "workflow init" "$WORKFLOW_INIT_LOG" "Initialized $WORKFLOW_PATH"
  require_output_contains "workflow init" "$WORKFLOW_INIT_LOG" "Verification"
  require_output_contains "workflow init" "$WORKFLOW_INIT_LOG" "claude_version_status: ok"
  require_output_contains "workflow init" "$WORKFLOW_INIT_LOG" "claude_auth_source_status: ok"
  require_output_contains "workflow init" "$WORKFLOW_INIT_LOG" "claude_session_status: ok"
  require_output_contains "workflow init" "$WORKFLOW_INIT_LOG" "claude_session_bare_mode: ok"
  require_output_contains "workflow init" "$WORKFLOW_INIT_LOG" "claude_session_additional_directories: ok"
  require_output_contains "workflow init" "$WORKFLOW_INIT_LOG" "runtime_claude: ok"
}

run_claude_spec_check() {
  echo "Running maestro spec-check"
  if ! "$MAESTRO_BIN" spec-check --repo "$HARNESS_ROOT" >"$SPEC_CHECK_LOG" 2>&1; then
    echo "maestro spec-check failed for the Claude harness" >&2
    return 1
  fi
  require_output_contains "spec-check" "$SPEC_CHECK_LOG" "Spec Check"
  require_output_contains "spec-check" "$SPEC_CHECK_LOG" "config_defaults: ok"
  require_output_contains "spec-check" "$SPEC_CHECK_LOG" "runtime_schema_json: ok"
  require_output_contains "spec-check" "$SPEC_CHECK_LOG" "skill_install: ok"
  require_output_contains "spec-check" "$SPEC_CHECK_LOG" "workflow_load: ok"
  require_output_contains "spec-check" "$SPEC_CHECK_LOG" "workflow_prompt_render: ok"
  require_output_contains "spec-check" "$SPEC_CHECK_LOG" "workflow_version: ok"
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

run_claude_doctor() {
  echo "Running maestro doctor preflight"
  if ! "$MAESTRO_BIN" doctor --repo "$HARNESS_ROOT" --db "$DB_PATH" >"$DOCTOR_LOG" 2>&1; then
    echo "maestro doctor failed for the Claude harness" >&2
    return 1
  fi
  require_output_contains "doctor" "$DOCTOR_LOG" "Doctor"
  require_output_matches "doctor" "$DOCTOR_LOG" 'claude_auth_source: (OAuth|cloud provider)'
  require_output_contains "doctor" "$DOCTOR_LOG" "claude_version_status: ok"
  require_output_contains "doctor" "$DOCTOR_LOG" "claude_auth_source_status: ok"
  require_output_contains "doctor" "$DOCTOR_LOG" "claude_session_status: ok"
  require_output_contains "doctor" "$DOCTOR_LOG" "claude_session_bare_mode: ok"
  require_output_contains "doctor" "$DOCTOR_LOG" "claude_session_additional_directories: ok"
  require_output_contains "doctor" "$DOCTOR_LOG" "runtime_claude: ok"
}

prepare_claude_command_wrapper() {
  local raw_command="$1"
  local -a command_env=()
  local -a command_args=()
  local token=""
  local env_prefix=0

  shell_split_words "$raw_command"
  for token in "${E2E_SHELL_WORDS[@]}"; do
    if [[ "$env_prefix" -eq 0 && "$token" == "env" ]]; then
      env_prefix=1
      continue
    fi
    if [[ "${#command_args[@]}" -eq 0 ]]; then
      if is_shell_assignment "$token"; then
        command_env+=("$token")
        continue
      fi
      if [[ "$env_prefix" -eq 1 && "$token" == "--" ]]; then
        env_prefix=2
        continue
      fi
      if [[ "$env_prefix" -eq 1 && "$token" == -* ]]; then
        continue
      fi
    fi
    command_args+=("$token")
  done

  if [[ "${#command_args[@]}" -eq 0 ]]; then
    echo "unable to determine wrapped Claude command: $raw_command" >&2
    exit 1
  fi

  {
    cat <<EOF
#!/usr/bin/env bash

set -euo pipefail

CLAUDE_EVIDENCE_DIR=$(printf '%q' "$CLAUDE_EVIDENCE_DIR")
CLAUDE_PROBE_BIN=$(printf '%q' "$CLAUDE_PROBE_BIN")
CLAUDE_DB_PATH=$(printf '%q' "$DB_PATH")
CLAUDE_DAEMON_REGISTRY_DIR=$(printf '%q' "$DAEMON_REGISTRY_DIR")
EOF
    printf 'REAL_COMMAND_ENV=('
    if [[ "${#command_env[@]}" -gt 0 ]]; then
      for token in "${command_env[@]}"; do
        printf ' %q' "$token"
      done
    fi
    printf ' )\n'
    printf 'REAL_COMMAND_ARGS=('
    for token in "${command_args[@]}"; do
      printf ' %q' "$token"
    done
    cat <<'EOF'
 )

next_launch_prefix() {
  mkdir -p "$CLAUDE_EVIDENCE_DIR"
  local counter_file="$CLAUDE_EVIDENCE_DIR/launch.counter"
  local counter=0
  if [[ -f "$counter_file" ]]; then
    counter="$(cat "$counter_file")"
  fi
  counter=$((counter + 1))
  printf '%s' "$counter" >"$counter_file"
  printf '%s/launch-%s' "$CLAUDE_EVIDENCE_DIR" "$counter"
}

main() {
  local mcp_config=""
  local settings_path=""
  local allowed_tools=""
  local permission_prompt_tool=""
  local permission_mode=""
  local strict_mcp_config="false"
  local -a runtime_args=("$@")
  local idx=0

  while [[ "$idx" -lt "${#runtime_args[@]}" ]]; do
    local arg="${runtime_args[idx]}"
    case "$arg" in
      --mcp-config)
        if [[ "$idx" -lt $(( ${#runtime_args[@]} - 1 )) ]]; then
          mcp_config="${runtime_args[idx + 1]}"
        fi
        idx=$((idx + 2))
        continue
        ;;
      --settings)
        if [[ "$idx" -lt $(( ${#runtime_args[@]} - 1 )) ]]; then
          settings_path="${runtime_args[idx + 1]}"
        fi
        idx=$((idx + 2))
        continue
        ;;
      --allowed-tools)
        if [[ "$idx" -lt $(( ${#runtime_args[@]} - 1 )) ]]; then
          allowed_tools="${runtime_args[idx + 1]}"
        fi
        idx=$((idx + 2))
        continue
        ;;
      --permission-prompt-tool)
        if [[ "$idx" -lt $(( ${#runtime_args[@]} - 1 )) ]]; then
          permission_prompt_tool="${runtime_args[idx + 1]}"
        fi
        idx=$((idx + 2))
        continue
        ;;
      --permission-mode)
        if [[ "$idx" -lt $(( ${#runtime_args[@]} - 1 )) ]]; then
          permission_mode="${runtime_args[idx + 1]}"
        fi
        idx=$((idx + 2))
        continue
        ;;
      --strict-mcp-config)
        strict_mcp_config="true"
        idx=$((idx + 1))
        continue
        ;;
      *)
        idx=$((idx + 1))
        ;;
    esac
  done

  if [[ -z "$mcp_config" || -z "$settings_path" ]]; then
    if [[ "${#REAL_COMMAND_ENV[@]}" -gt 0 ]]; then
      exec env "${REAL_COMMAND_ENV[@]}" "${REAL_COMMAND_ARGS[@]}" "$@"
    fi
    exec env "${REAL_COMMAND_ARGS[@]}" "$@"
  fi

  local evidence_prefix
  evidence_prefix="$(next_launch_prefix)"
  printf '%s\n' "$@" >"$evidence_prefix.args.txt"

  exec 3<&0
  if [[ "${#REAL_COMMAND_ENV[@]}" -gt 0 ]]; then
    env "${REAL_COMMAND_ENV[@]}" "${REAL_COMMAND_ARGS[@]}" "$@" <&3 &
  else
    env "${REAL_COMMAND_ARGS[@]}" "$@" <&3 &
  fi
  local child_pid="$!"
  exec 3<&-

  if ! "$CLAUDE_PROBE_BIN" \
    --mcp-config "$mcp_config" \
    --settings "$settings_path" \
    --db "$CLAUDE_DB_PATH" \
    --registry-dir "$CLAUDE_DAEMON_REGISTRY_DIR" \
    --evidence-prefix "$evidence_prefix" \
    --allowed-tools "$allowed_tools" \
    --permission-prompt-tool "$permission_prompt_tool" \
    --permission-mode "$permission_mode" \
    --strict-mcp-config "$strict_mcp_config"; then
    kill "$child_pid" >/dev/null 2>&1 || true
    wait "$child_pid" >/dev/null 2>&1 || true
    exit 1
  fi

  wait "$child_pid"
}

main "$@"
EOF
  } >"$CLAUDE_WRAPPER_BIN"
  chmod +x "$CLAUDE_WRAPPER_BIN"
}

assert_evidence_line() {
  local path="$1"
  local expected="$2"
  if ! grep -Fqx -- "$expected" "$path"; then
    echo "expected Claude harness evidence line missing: $expected" >&2
    return 1
  fi
}

assert_evidence_line_matches() {
  local path="$1"
  local pattern="$2"
  if ! grep -Eq -- "$pattern" "$path"; then
    echo "expected Claude harness evidence line matching $pattern in $path" >&2
    return 1
  fi
}

assert_claude_runtime_auth_source_line() {
  local path="$1"
  local key="$2"
  assert_evidence_line_matches "$path" "^${key}=(OAuth|cloud provider)$"
}

assert_claude_runtime_evidence() {
  if [[ ! -f "$CLAUDE_EVIDENCE_SUMMARY" ]]; then
    echo "expected Claude runtime evidence summary missing: $CLAUDE_EVIDENCE_SUMMARY" >&2
    return 1
  fi

  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "expected_tools_present=true"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "tool_call_get_issue_execution=ok"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "tool_call_server_info=ok"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "tool_call_list_issues=ok"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "tool_call_get_runtime_snapshot=ok"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "tool_call_list_sessions=ok"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "daemon_registry_entries_before=1"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "daemon_registry_entries_after=1"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "daemon_entry_stable=true"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "server_db_path=$DB_PATH"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "daemon_db_path=$DB_PATH"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "bridge_db_path=$DB_PATH"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "dashboard_session_runtime_name=claude"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "dashboard_session_runtime_provider=claude"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "dashboard_session_runtime_transport=stdio"
  assert_claude_runtime_auth_source_line "$CLAUDE_EVIDENCE_SUMMARY" "dashboard_session_runtime_auth_source"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "strict_mcp_config=true"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "permission_mode=default"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "allowed_tools=Bash,Edit,Write,MultiEdit"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "execution_runtime_name=claude"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "execution_runtime_provider=claude"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "execution_runtime_transport=stdio"
  assert_claude_runtime_auth_source_line "$CLAUDE_EVIDENCE_SUMMARY" "execution_runtime_auth_source"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "permission_prompt_tool=<none>"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "settings_disable_auto_mode=disable"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "settings_use_auto_mode_during_plan=false"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "settings_disable_all_hooks=true"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "settings_include_git_instructions=false"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "settings_disable_bypass_permissions_mode=disable"
  assert_evidence_line "$CLAUDE_EVIDENCE_SUMMARY" "live_claude_session_seen=true"

  if ! grep -Eq '^server_store_id=.+$' "$CLAUDE_EVIDENCE_SUMMARY"; then
    echo "expected server_store_id in Claude runtime evidence summary" >&2
    return 1
  fi
  if ! grep -Eq '^daemon_store_id=.+$' "$CLAUDE_EVIDENCE_SUMMARY"; then
    echo "expected daemon_store_id in Claude runtime evidence summary" >&2
    return 1
  fi
}

print_failure_context() {
  echo "Harness root: $HARNESS_ROOT" >&2
  echo "Workflow path: $WORKFLOW_PATH" >&2
  echo "Database: $DB_PATH" >&2
  echo "Daemon registry dir: $DAEMON_REGISTRY_DIR" >&2
  echo "Claude evidence dir: $CLAUDE_EVIDENCE_DIR" >&2
  echo "Workflow init log: $WORKFLOW_INIT_LOG" >&2
  echo "Spec-check log: $SPEC_CHECK_LOG" >&2
  echo "Verify log: $VERIFY_LOG" >&2
  echo "Doctor log: $DOCTOR_LOG" >&2
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
  if [[ -f "$DOCTOR_LOG" ]]; then
    echo "Last doctor output:" >&2
    tail -n 50 "$DOCTOR_LOG" >&2 || true
  fi
  if [[ -f "$SPEC_CHECK_LOG" ]]; then
    echo "Last spec-check output:" >&2
    tail -n 50 "$SPEC_CHECK_LOG" >&2 || true
  fi
  if [[ -f "$WORKFLOW_INIT_LOG" ]]; then
    echo "Last workflow init output:" >&2
    tail -n 50 "$WORKFLOW_INIT_LOG" >&2 || true
  fi
  if [[ -f "$ORCH_LOG" ]]; then
    echo "Last orchestrator output:" >&2
    tail -n 100 "$ORCH_LOG" >&2 || true
  fi
  if [[ -d "$CLAUDE_EVIDENCE_DIR" ]]; then
    echo "Claude evidence files:" >&2
    find "$CLAUDE_EVIDENCE_DIR" -maxdepth 1 -type f | sort >&2 || true
  fi
  if [[ -f "$CLAUDE_EVIDENCE_SUMMARY" ]]; then
    echo "Claude evidence summary:" >&2
    cat "$CLAUDE_EVIDENCE_SUMMARY" >&2 || true
  fi
  if [[ -d "$DAEMON_REGISTRY_DIR" ]]; then
    echo "Daemon registry entries:" >&2
    find "$DAEMON_REGISTRY_DIR" -maxdepth 1 -name '*.json' -type f | sort >&2 || true
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
