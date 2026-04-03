#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASIC_SCRIPT_UNDER_TEST="$ROOT_DIR/scripts/e2e_real_codex.sh"
PHASES_SCRIPT_UNDER_TEST="$ROOT_DIR/scripts/e2e_real_codex_phases.sh"
CODEX_OVERRIDE="npx -y @openai/codex@0.118.0 app-server"

fail() {
  printf 'test: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  local file="$1"
  local pattern="$2"
  if ! grep -Fq -- "$pattern" "$file"; then
    fail "expected to find '$pattern' in $file"
  fi
}

link_runtime_tool() {
  local bin_dir="$1"
  local tool="$2"
  local resolved
  resolved="$(command -v "$tool")"
  [[ -n "$resolved" ]] || fail "missing system tool for test runtime: $tool"
  ln -s "$resolved" "$bin_dir/$tool"
}

make_mock_bin() {
  local bin_dir="$1"
  mkdir -p "$bin_dir"

  for tool in awk bash cat chmod dirname env find grep head mkdir rm sed sleep sort tail touch tr; do
    link_runtime_tool "$bin_dir" "$tool"
  done

  cat >"$bin_dir/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'go %s\n' "$*" >>"$MOCK_TOOL_LOG"
if [[ "${1:-}" == "build" && "${2:-}" == "-o" ]]; then
  output="$3"
  mkdir -p "$(dirname "$output")"
  cat >"$output" <<'INNER'
#!/usr/bin/env bash
set -euo pipefail
printf 'maestro %s\n' "$*" >>"$FAKE_MAESTRO_LOG"
command_name="${1:-}"
shift || true
case "$command_name" in
  project)
    if [[ "${1:-}" == "create" ]]; then
      printf 'proj-1\n'
      exit 0
    fi
    ;;
  issue)
    subcommand="${1:-}"
    shift || true
    case "$subcommand" in
      create)
        issue_counter_file="$FAKE_STATE_DIR/issue-counter"
        issue_counter=0
        if [[ -f "$issue_counter_file" ]]; then
          issue_counter="$(cat "$issue_counter_file")"
        fi
        issue_counter=$((issue_counter + 1))
        printf '%s' "$issue_counter" >"$issue_counter_file"
        if [[ "$FAKE_MAESTRO_MODE" == "basic" ]]; then
          issue_id="RC-${issue_counter}"
        else
          if [[ "$issue_counter" -eq 1 ]]; then
            issue_id="RC-REVIEW"
          else
            issue_id="RC-SKIP"
          fi
        fi
        printf 'backlog' >"$FAKE_STATE_DIR/$issue_id.state"
        printf 'implementation' >"$FAKE_STATE_DIR/$issue_id.phase"
        printf '%s\n' "${1:-mock issue}" >"$FAKE_STATE_DIR/$issue_id.title"
        printf 'Created issue %s: %s\n' "$issue_id" "${1:-mock issue}"
        ;;
      update)
        exit 0
        ;;
      move)
        issue_id="${1:-}"
        state="${2:-}"
        printf '%s' "$state" >"$FAKE_STATE_DIR/$issue_id.state"
        if [[ "$FAKE_MAESTRO_MODE" == "phases" && "$state" == "ready" ]]; then
          mkdir -p "$FAKE_HARNESS_ROOT/artifacts"
          printf 'implementation complete for %s\n' "$issue_id" >"$FAKE_HARNESS_ROOT/artifacts/$issue_id.implementation.txt"
          if [[ "$issue_id" == "RC-REVIEW" ]]; then
            printf 'review complete for %s\n' "$issue_id" >"$FAKE_HARNESS_ROOT/artifacts/$issue_id.review.txt"
            printf 'done complete for %s\n' "$issue_id" >"$FAKE_HARNESS_ROOT/artifacts/$issue_id.done.txt"
            printf 'implementation\nreview\ndone\n' >"$FAKE_HARNESS_ROOT/artifacts/$issue_id.phases.log"
          else
            rm -f "$FAKE_HARNESS_ROOT/artifacts/$issue_id.review.txt"
            printf 'done complete for %s\n' "$issue_id" >"$FAKE_HARNESS_ROOT/artifacts/$issue_id.done.txt"
            printf 'implementation\ndone\n' >"$FAKE_HARNESS_ROOT/artifacts/$issue_id.phases.log"
          fi
          printf 'done' >"$FAKE_STATE_DIR/$issue_id.state"
          printf 'complete' >"$FAKE_STATE_DIR/$issue_id.phase"
        elif [[ "$state" == "in_review" ]]; then
          printf 'review' >"$FAKE_STATE_DIR/$issue_id.phase"
        elif [[ "$state" == "done" ]]; then
          printf 'complete' >"$FAKE_STATE_DIR/$issue_id.phase"
        fi
        ;;
      show)
        issue_id="${1:-}"
        title="$(cat "$FAKE_STATE_DIR/$issue_id.title")"
        state="$(cat "$FAKE_STATE_DIR/$issue_id.state")"
        printf 'Title: %s\n' "$title"
        printf 'State: %s\n' "$state"
        if [[ -f "$FAKE_STATE_DIR/$issue_id.phase" ]]; then
          phase="$(cat "$FAKE_STATE_DIR/$issue_id.phase")"
          printf 'Phase: %s\n' "$phase"
        fi
        ;;
      *)
        exit 1
        ;;
    esac
    ;;
  run)
    if [[ "$FAKE_MAESTRO_MODE" == "basic" ]]; then
      mkdir -p "$FAKE_HARNESS_ROOT/artifacts"
      printf 'maestro e2e ok 1\n' >"$FAKE_HARNESS_ROOT/artifacts/artifact-one.txt"
      printf 'maestro e2e ok 2\n' >"$FAKE_HARNESS_ROOT/artifacts/artifact-two.txt"
      printf 'done' >"$FAKE_STATE_DIR/RC-1.state"
      printf 'done' >"$FAKE_STATE_DIR/RC-2.state"
      printf 'complete' >"$FAKE_STATE_DIR/RC-1.phase"
      printf 'complete' >"$FAKE_STATE_DIR/RC-2.phase"
    fi
    ;;
  *)
    exit 1
    ;;
esac
INNER
  chmod +x "$output"
  exit 0
fi
exit 1
EOF

  cat >"$bin_dir/npx" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'npx %s\n' "$*" >>"$MOCK_TOOL_LOG"
exit 0
EOF

  cat >"$bin_dir/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'git %s\n' "$*" >>"$MOCK_TOOL_LOG"
exit 0
EOF

  cat >"$bin_dir/sqlite3" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'sqlite3 %s\n' "$*" >>"$MOCK_TOOL_LOG"
exit 0
EOF

  chmod +x "$bin_dir/go" "$bin_dir/npx" "$bin_dir/git" "$bin_dir/sqlite3"
}

test_basic_harness_honors_command_override_preflight() {
  local tmp_dir bin_dir harness_root
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-real-codex-test-basic.XXXXXX")"
  bin_dir="$tmp_dir/bin"
  harness_root="$tmp_dir/harness"

  mkdir -p "$tmp_dir/state"
  : >"$tmp_dir/tool.log"
  : >"$tmp_dir/maestro.log"
  make_mock_bin "$bin_dir"

  PATH="$bin_dir" \
  MOCK_TOOL_LOG="$tmp_dir/tool.log" \
  FAKE_MAESTRO_LOG="$tmp_dir/maestro.log" \
  FAKE_STATE_DIR="$tmp_dir/state" \
  FAKE_HARNESS_ROOT="$harness_root" \
  FAKE_MAESTRO_MODE=basic \
  E2E_ROOT="$harness_root" \
  E2E_KEEP_HARNESS=1 \
  E2E_CODEX_COMMAND="$CODEX_OVERRIDE" \
  /usr/bin/bash "$BASIC_SCRIPT_UNDER_TEST" >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"

  [[ -f "$harness_root/WORKFLOW.md" ]] || fail "expected WORKFLOW.md to be created"
  [[ -f "$harness_root/artifacts/artifact-one.txt" ]] || fail "expected artifact-one.txt to be created"
  [[ -f "$harness_root/artifacts/artifact-two.txt" ]] || fail "expected artifact-two.txt to be created"
  assert_contains "$harness_root/WORKFLOW.md" "command: '$CODEX_OVERRIDE'"
  assert_contains "$tmp_dir/stdout.txt" "Real Codex e2e flow completed successfully."
  assert_contains "$tmp_dir/stdout.txt" "RC-1 -> $harness_root/artifacts/artifact-one.txt"
  assert_contains "$tmp_dir/stdout.txt" "RC-2 -> $harness_root/artifacts/artifact-two.txt"
  [[ ! -s "$tmp_dir/stderr.txt" ]] || fail "expected successful run to avoid stderr output"
}

test_phase_harness_honors_command_override_preflight() {
  local tmp_dir bin_dir harness_root
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-real-codex-test-phases.XXXXXX")"
  bin_dir="$tmp_dir/bin"
  harness_root="$tmp_dir/harness"

  mkdir -p "$tmp_dir/state"
  : >"$tmp_dir/tool.log"
  : >"$tmp_dir/maestro.log"
  make_mock_bin "$bin_dir"

  PATH="$bin_dir" \
  MOCK_TOOL_LOG="$tmp_dir/tool.log" \
  FAKE_MAESTRO_LOG="$tmp_dir/maestro.log" \
  FAKE_STATE_DIR="$tmp_dir/state" \
  FAKE_HARNESS_ROOT="$harness_root" \
  FAKE_MAESTRO_MODE=phases \
  E2E_ROOT="$harness_root" \
  E2E_KEEP_HARNESS=1 \
  E2E_CODEX_COMMAND="$CODEX_OVERRIDE" \
  /usr/bin/bash "$PHASES_SCRIPT_UNDER_TEST" >"$tmp_dir/stdout.txt" 2>"$tmp_dir/stderr.txt"

  [[ -f "$harness_root/WORKFLOW.md" ]] || fail "expected WORKFLOW.md to be created"
  [[ -f "$harness_root/artifacts/RC-REVIEW.done.txt" ]] || fail "expected RC-REVIEW done artifact to be created"
  [[ -f "$harness_root/artifacts/RC-SKIP.done.txt" ]] || fail "expected RC-SKIP done artifact to be created"
  assert_contains "$harness_root/WORKFLOW.md" "command: '$CODEX_OVERRIDE'"
  assert_contains "$tmp_dir/stdout.txt" "Real Codex phase e2e flow completed successfully."
  assert_contains "$tmp_dir/stdout.txt" "RC-REVIEW -> implementation, review, done, then complete"
  assert_contains "$tmp_dir/stdout.txt" "RC-SKIP -> implementation, done, then complete"
  [[ ! -s "$tmp_dir/stderr.txt" ]] || fail "expected successful run to avoid stderr output"
}

main() {
  test_basic_harness_honors_command_override_preflight
  test_phase_harness_honors_command_override_preflight
}

main "$@"
