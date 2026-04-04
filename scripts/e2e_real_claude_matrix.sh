#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODE="${1:-release-gate}"

usage() {
  cat <<'EOF'
Usage: scripts/e2e_real_claude_matrix.sh [release-gate|matrix]

Modes:
  release-gate  Run the required local Claude pre-release gate:
                lifecycle + permission-profile suites.
  matrix        Run the full Claude validation matrix:
                release gate + approval/alert suite.
EOF
}

MATRIX_ROOT=""
MANIFEST_PATH=""
MODE_LABEL=""
trap_status() {
  local status="$1"
  if [[ "$status" -ne 0 && -n "$MATRIX_ROOT" ]]; then
    echo "Real Claude validation $MODE_LABEL failed." >&2
    echo "Matrix root: $MATRIX_ROOT" >&2
    if [[ -n "$MANIFEST_PATH" ]]; then
      echo "Validation manifest: $MANIFEST_PATH" >&2
    fi
  fi
}
trap 'trap_status $?' EXIT

declare -a SUITE_NAMES=()
declare -a SUITE_SCRIPTS=()
declare -a SUITE_ISSUES=()
declare -a SUITE_LAUNCHES=()
declare -a SUITE_RELEASE_REQUIRED=()

case "$MODE" in
  release-gate|release)
    MODE_LABEL="release-gate"
    SUITE_NAMES=("lifecycle" "profiles")
    SUITE_SCRIPTS=(
      "$ROOT_DIR/scripts/e2e_real_claude.sh"
      "$ROOT_DIR/scripts/e2e_real_claude_profiles.sh"
    )
    SUITE_ISSUES=("4" "3")
    SUITE_LAUNCHES=("5" "5")
    SUITE_RELEASE_REQUIRED=("true" "true")
    ;;
  matrix|full)
    MODE_LABEL="matrix"
    SUITE_NAMES=("lifecycle" "profiles" "approvals")
    SUITE_SCRIPTS=(
      "$ROOT_DIR/scripts/e2e_real_claude.sh"
      "$ROOT_DIR/scripts/e2e_real_claude_profiles.sh"
      "$ROOT_DIR/scripts/e2e_real_claude_approvals.sh"
    )
    SUITE_ISSUES=("4" "3" "5")
    SUITE_LAUNCHES=("5" "5" "5")
    SUITE_RELEASE_REQUIRED=("true" "true" "false")
    ;;
  -h|--help|help)
    usage
    exit 0
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac

if [[ -n "${E2E_ROOT:-}" ]]; then
  MATRIX_ROOT="$E2E_ROOT"
  mkdir -p "$MATRIX_ROOT"
else
  MATRIX_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/maestro-real-claude-${MODE_LABEL}.XXXXXX")"
fi
MANIFEST_PATH="$MATRIX_ROOT/validation-manifest.txt"

{
  printf 'mode=%s\n' "$MODE_LABEL"
  printf 'matrix_root=%s\n' "$MATRIX_ROOT"
  printf 'suite_count=%s\n' "${#SUITE_NAMES[@]}"
  printf 'release_gate_issues=7\n'
  printf 'release_gate_claude_launches=10\n'
  printf 'full_matrix_issues=12\n'
  printf 'full_matrix_claude_launches=15\n'
} >"$MANIFEST_PATH"

run_suite() {
  local index="$1"
  local suite_name="$2"
  local suite_script="$3"
  local suite_issues="$4"
  local suite_launches="$5"
  local release_required="$6"
  local suite_root="$MATRIX_ROOT/$suite_name"
  local suite_state_dir=""
  local -a env_args=("E2E_ROOT=$suite_root")

  mkdir -p "$suite_root"
  {
    printf 'suite_%s_name=%s\n' "$index" "$suite_name"
    printf 'suite_%s_script=%s\n' "$index" "$suite_script"
    printf 'suite_%s_harness_root=%s\n' "$index" "$suite_root"
    printf 'suite_%s_issue_count=%s\n' "$index" "$suite_issues"
    printf 'suite_%s_claude_launches=%s\n' "$index" "$suite_launches"
    printf 'suite_%s_required_for_release=%s\n' "$index" "$release_required"
  } >>"$MANIFEST_PATH"

  if [[ -n "${FAKE_HARNESS_ROOT:-}" ]]; then
    env_args+=("FAKE_HARNESS_ROOT=$suite_root")
  fi
  if [[ -n "${FAKE_STATE_DIR:-}" ]]; then
    suite_state_dir="$MATRIX_ROOT/.fake-state/$suite_name"
    mkdir -p "$suite_state_dir"
    env_args+=("FAKE_STATE_DIR=$suite_state_dir")
  fi

  echo "Running ${suite_name} suite (${suite_issues} issues / ${suite_launches} Claude launches)"
  if env "${env_args[@]}" bash "$suite_script"; then
    printf 'suite_%s_status=passed\n' "$index" >>"$MANIFEST_PATH"
  else
    printf 'suite_%s_status=failed\n' "$index" >>"$MANIFEST_PATH"
    return 1
  fi
}

echo "Real Claude validation mode: $MODE_LABEL"
echo "Matrix root: $MATRIX_ROOT"
echo "Validation manifest: $MANIFEST_PATH"

for ((i = 0; i < ${#SUITE_NAMES[@]}; i++)); do
  run_suite \
    "$((i + 1))" \
    "${SUITE_NAMES[i]}" \
    "${SUITE_SCRIPTS[i]}" \
    "${SUITE_ISSUES[i]}" \
    "${SUITE_LAUNCHES[i]}" \
    "${SUITE_RELEASE_REQUIRED[i]}"
done

echo "Real Claude $MODE_LABEL validation completed successfully."
echo "Retained harness roots:"
for ((i = 0; i < ${#SUITE_NAMES[@]}; i++)); do
  echo "  ${SUITE_NAMES[i]}: $MATRIX_ROOT/${SUITE_NAMES[i]}"
done
