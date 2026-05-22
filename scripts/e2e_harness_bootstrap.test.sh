#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

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

assert_in_order() {
  local file="$1"
  shift
  local previous_line=0
  local pattern
  for pattern in "$@"; do
    local line
    line="$(grep -nF -- "$pattern" "$file" | head -n 1 | cut -d: -f1 || true)"
    if [[ -z "$line" ]]; then
      fail "missing ordered pattern '$pattern' in $file"
    fi
    if (( line <= previous_line )); then
      fail "pattern '$pattern' appeared out of order in $file"
    fi
    previous_line="$line"
  done
}

make_mock_toolchain() {
  local bin_dir="$1"
  mkdir -p "$bin_dir"

  cat >"$bin_dir/codex" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'codex %s\n' "$*" >>"$LOG_FILE"
exit 0
EOF

  cat >"$bin_dir/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'git %s\n' "$*" >>"$LOG_FILE"
exit 0
EOF

  cat >"$bin_dir/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'go %s\n' "$*" >>"$LOG_FILE"
case "$1" in
  build)
    if [[ -f "$MOCK_ROOT_DIR/internal/dashboardui/dist/.e2e-bootstrap-sentinel" ]]; then
      printf 'dashboard bootstrap sentinel present before go build\n' >>"$LOG_FILE"
      exit 17
    fi
    printf 'dashboard bootstrap sentinel missing before go build\n' >>"$LOG_FILE"
    exit 99
    ;;
  *)
    exit 0
    ;;
esac
EOF

  cat >"$bin_dir/python3" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'python3 %s\n' "$*" >>"$LOG_FILE"
exit 0
EOF

  cat >"$bin_dir/sqlite3" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'sqlite3 %s\n' "$*" >>"$LOG_FILE"
exit 0
EOF

  cat >"$bin_dir/ensure-dashboard-dist" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'ensure-dashboard\n' >>"$LOG_FILE"
mkdir -p "$MOCK_ROOT_DIR/internal/dashboardui/dist"
: >"$MOCK_ROOT_DIR/internal/dashboardui/dist/.e2e-bootstrap-sentinel"
EOF

  chmod +x \
    "$bin_dir/codex" \
    "$bin_dir/git" \
    "$bin_dir/go" \
    "$bin_dir/python3" \
    "$bin_dir/sqlite3" \
    "$bin_dir/ensure-dashboard-dist"
}

make_project_create_mock_toolchain() {
  local bin_dir="$1"
  mkdir -p "$bin_dir"

  cat >"$bin_dir/codex" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'codex %s\n' "$*" >>"$LOG_FILE"
exit 0
EOF

  cat >"$bin_dir/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'go %s\n' "$*" >>"$LOG_FILE"
if [[ "${1:-}" != "build" ]]; then
  exit 0
fi

output=""
while (($#)); do
  case "$1" in
    -o)
      output="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

if [[ -z "$output" ]]; then
  printf 'missing go build output path\n' >>"$LOG_FILE"
  exit 91
fi

cat >"$output" <<'INNER'
#!/usr/bin/env bash
set -euo pipefail
printf 'maestro %s\n' "$*" >>"$LOG_FILE"
if [[ "${1:-}" == "project" && "${2:-}" == "create" ]]; then
  repo_path=""
  shift 2
  while (($#)); do
    case "$1" in
      --repo)
        repo_path="$2"
        shift 2
        ;;
      *)
        shift
        ;;
    esac
  done
  if [[ -z "$repo_path" ]]; then
    printf 'missing repo path for project create\n' >&2
    exit 41
  fi
  git -C "$repo_path" rev-parse --show-toplevel >/dev/null 2>&1 || exit 42
  git -C "$repo_path" rev-parse HEAD >/dev/null 2>&1 || exit 43
  if [[ "$(git -C "$repo_path" branch --show-current)" != "main" ]]; then
    exit 44
  fi
  printf 'proj_test\n'
  exit 0
fi
exit 45
INNER
chmod +x "$output"
EOF

  cat >"$bin_dir/sqlite3" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'sqlite3 %s\n' "$*" >>"$LOG_FILE"
exit 67
EOF

  cat >"$bin_dir/ensure-dashboard-dist" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'ensure-dashboard\n' >>"$LOG_FILE"
EOF

  chmod +x \
    "$bin_dir/codex" \
    "$bin_dir/go" \
    "$bin_dir/sqlite3" \
    "$bin_dir/ensure-dashboard-dist"
}

make_issue_identifier_mock_toolchain() {
  local bin_dir="$1"
  mkdir -p "$bin_dir"

  cat >"$bin_dir/codex" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'codex %s\n' "$*" >>"$LOG_FILE"
exit 0
EOF

  cat >"$bin_dir/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'go %s\n' "$*" >>"$LOG_FILE"
if [[ "${1:-}" != "build" ]]; then
  exit 0
fi

output=""
while (($#)); do
  case "$1" in
    -o)
      output="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

if [[ -z "$output" ]]; then
  printf 'missing go build output path\n' >>"$LOG_FILE"
  exit 91
fi

cat >"$output" <<'INNER'
#!/usr/bin/env bash
set -euo pipefail
printf 'maestro %s\n' "$*" >>"$LOG_FILE"

mode="${E2E_TEST_MODE:-}"
create_count_file="$MOCK_HARNESS_ROOT/.issue-create-count"
update_count_file="$MOCK_HARNESS_ROOT/.issue-update-count"
move_count_file="$MOCK_HARNESS_ROOT/.issue-move-count"

quiet_requested() {
  local arg
  for arg in "$@"; do
    if [[ "$arg" == "--quiet" ]]; then
      return 0
    fi
  done
  return 1
}

next_count() {
  local path="$1"
  local count
  count="$(cat "$path" 2>/dev/null || printf '0')"
  count=$((count + 1))
  printf '%s\n' "$count" >"$path"
  printf '%s\n' "$count"
}

if [[ "${1:-}" == "project" && "${2:-}" == "create" ]]; then
  printf 'proj_test\n'
  exit 0
fi

if [[ "${1:-}" == "issue" && "${2:-}" == "create" ]]; then
  count="$(next_count "$create_count_file")"
  case "$mode:$count" in
    basic:1) identifier="REAL-1" ;;
    basic:2) identifier="REAL-2" ;;
    phases:1) identifier="PHAS-1" ;;
    phases:2) identifier="PHAS-2" ;;
    *) exit 71 ;;
  esac
  if quiet_requested "$@"; then
    printf '%s\n' "$identifier"
  else
    printf 'Created issue %s\n' "$identifier"
  fi
  exit 0
fi

if [[ "${1:-}" == "issue" && "${2:-}" == "move" ]]; then
  issue_id="${3:-}"
  case "$issue_id" in
    REAL-1|REAL-2) ;;
    *) printf 'unexpected issue identifier for move: %s\n' "$issue_id" >>"$LOG_FILE"; exit 51 ;;
  esac
  count="$(next_count "$move_count_file")"
  if [[ "$count" == "2" ]]; then
    exit 61
  fi
  exit 0
fi

if [[ "${1:-}" == "issue" && "${2:-}" == "update" ]]; then
  issue_id="${3:-}"
  case "$issue_id" in
    PHAS-1|PHAS-2) ;;
    *) printf 'unexpected issue identifier for update: %s\n' "$issue_id" >>"$LOG_FILE"; exit 52 ;;
  esac
  count="$(next_count "$update_count_file")"
  if [[ "$count" == "2" ]]; then
    exit 61
  fi
  exit 0
fi

if [[ "${1:-}" == "project" && "${2:-}" == "start" ]]; then
  exit 0
fi

exit 72
INNER
chmod +x "$output"
EOF

  cat >"$bin_dir/sqlite3" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'sqlite3 %s\n' "$*" >>"$LOG_FILE"
exit 0
EOF

  cat >"$bin_dir/ensure-dashboard-dist" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'ensure-dashboard\n' >>"$LOG_FILE"
EOF

  chmod +x \
    "$bin_dir/codex" \
    "$bin_dir/go" \
    "$bin_dir/sqlite3" \
    "$bin_dir/ensure-dashboard-dist"
}

make_image_harness_mock_toolchain() {
  local bin_dir="$1"
  mkdir -p "$bin_dir"

  cat >"$bin_dir/codex" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'codex %s\n' "$*" >>"$LOG_FILE"
exit 0
EOF

  cat >"$bin_dir/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'go %s\n' "$*" >>"$LOG_FILE"
if [[ "${1:-}" != "build" ]]; then
  exit 0
fi

output=""
while (($#)); do
  case "$1" in
    -o)
      output="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

if [[ -z "$output" ]]; then
  printf 'missing go build output path\n' >>"$LOG_FILE"
  exit 91
fi

cat >"$output" <<'INNER'
#!/usr/bin/env bash
set -euo pipefail
printf 'maestro %s\n' "$*" >>"$LOG_FILE"

if [[ "${1:-}" == "project" && "${2:-}" == "create" ]]; then
  printf 'proj_image\n'
  exit 0
fi

if [[ "${1:-}" == "project" && "${2:-}" == "start" ]]; then
  exit 0
fi

if [[ "${1:-}" == "issue" && "${2:-}" == "create" ]]; then
  printf 'IMAG-1\n'
  exit 0
fi

if [[ "${1:-}" == "issue" && "${2:-}" == "images" && "${3:-}" == "add" ]]; then
  printf 'ast_img\n'
  exit 0
fi

if [[ "${1:-}" == "issue" && "${2:-}" == "update" ]]; then
  exit 0
fi

if [[ "${1:-}" == "issue" && "${2:-}" == "move" ]]; then
  exit 0
fi

if [[ "${1:-}" == "issue" && "${2:-}" == "show" ]]; then
  if [[ -f "$MOCK_HARNESS_ROOT/.image-run-ready" ]]; then
    cat <<'OUT'
Identifier: IMAG-1
Title: Read attached image text
State: done
OUT
  else
    cat <<'OUT'
Identifier: IMAG-1
Title: Read attached image text
State: ready
OUT
  fi
  exit 0
fi

if [[ "${1:-}" == "run" ]]; then
  workspace_path="$MOCK_HARNESS_ROOT/workspaces/image-e2e-project/IMAG-1"
  staged_dir="$workspace_path/.maestro/issue-assets"
  staged_path="$staged_dir/ast_img-uploaded-issue-image.png"
  mkdir -p "$staged_dir" "$MOCK_HARNESS_ROOT/.maestro"
  cp "$MOCK_HARNESS_ROOT/uploaded-issue-image.png" "$staged_path"
  python3 - <<'PY'
import json
import os
import sqlite3

root = os.environ["MOCK_HARNESS_ROOT"]
db_path = os.path.join(root, ".maestro", "maestro.db")
workspace_path = os.path.join(root, "workspaces", "image-e2e-project", "IMAG-1")

conn = sqlite3.connect(db_path)
conn.executescript(
    """
    CREATE TABLE IF NOT EXISTS issues (
        id TEXT PRIMARY KEY,
        identifier TEXT NOT NULL DEFAULT '',
        title TEXT NOT NULL DEFAULT '',
        state TEXT NOT NULL DEFAULT ''
    );
    CREATE TABLE IF NOT EXISTS workspaces (
        issue_id TEXT PRIMARY KEY,
        path TEXT NOT NULL DEFAULT ''
    );
    CREATE TABLE IF NOT EXISTS issue_activity_entries (
        seq INTEGER PRIMARY KEY AUTOINCREMENT,
        issue_id TEXT NOT NULL DEFAULT '',
        identifier TEXT NOT NULL DEFAULT '',
        kind TEXT NOT NULL DEFAULT '',
        phase TEXT NOT NULL DEFAULT '',
        raw_payload_json TEXT NOT NULL DEFAULT '{}',
        attempt INTEGER NOT NULL DEFAULT 0
    );
    DELETE FROM issues;
    DELETE FROM workspaces;
    DELETE FROM issue_activity_entries;
    """
)
conn.execute(
    "INSERT INTO issues (id, identifier, title, state) VALUES (?, ?, ?, ?)",
    ("iss_img", "IMAG-1", "Read attached image text", "done"),
)
conn.execute(
    "INSERT INTO workspaces (issue_id, path) VALUES (?, ?)",
    ("iss_img", workspace_path),
)
conn.execute(
    "INSERT INTO issue_activity_entries (issue_id, identifier, kind, phase, raw_payload_json, attempt) VALUES (?, ?, ?, ?, ?, ?)",
    ("iss_img", "IMAG-1", "agent", "final_answer", json.dumps({"item": {"text": "MAESTRO"}}), 0),
)
conn.commit()
conn.close()
PY
  : >"$MOCK_HARNESS_ROOT/.image-run-ready"
  exit 0
fi

exit 72
INNER
chmod +x "$output"
EOF

  cat >"$bin_dir/ensure-dashboard-dist" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'ensure-dashboard\n' >>"$LOG_FILE"
EOF

  chmod +x \
    "$bin_dir/codex" \
    "$bin_dir/go" \
    "$bin_dir/ensure-dashboard-dist"
}

cleanup_sentinel() {
  rm -f "$ROOT_DIR/internal/dashboardui/dist/.e2e-bootstrap-sentinel"
}

test_scripts_bootstrap_dashboard_dist_before_build() {
  local scripts=(
    "$ROOT_DIR/scripts/e2e_retry_safety.sh"
    "$ROOT_DIR/scripts/e2e_real_codex.sh"
    "$ROOT_DIR/scripts/e2e_real_codex_phases.sh"
    "$ROOT_DIR/scripts/e2e_real_codex_issue_images.sh"
  )

  local script
  for script in "${scripts[@]}"; do
    local tmp_dir bin_dir log_file stdout_file stderr_file
    tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-harness-bootstrap.XXXXXX")"
    bin_dir="$tmp_dir/bin"
    log_file="$tmp_dir/log.txt"
    stdout_file="$tmp_dir/stdout.txt"
    stderr_file="$tmp_dir/stderr.txt"

    make_mock_toolchain "$bin_dir"
    : >"$log_file"
    cleanup_sentinel

    if PATH="$bin_dir:$PATH" \
      LOG_FILE="$log_file" \
      MOCK_ROOT_DIR="$ROOT_DIR" \
      MAESTRO_ENSURE_DASHBOARD_DIST_BIN="$bin_dir/ensure-dashboard-dist" \
      E2E_ROOT="$tmp_dir/harness" \
      bash "$script" >"$stdout_file" 2>"$stderr_file"; then
      cleanup_sentinel
      fail "expected $(basename "$script") to stop after the mocked go build"
    fi

    assert_in_order "$log_file" "ensure-dashboard" "dashboard bootstrap sentinel present before go build"
    assert_contains "$log_file" "go build -o "
    cleanup_sentinel
  done
}

test_scripts_initialize_git_repo_before_project_create() {
  local scripts=(
    "$ROOT_DIR/scripts/e2e_real_codex.sh"
    "$ROOT_DIR/scripts/e2e_real_codex_phases.sh"
    "$ROOT_DIR/scripts/e2e_real_codex_issue_images.sh"
  )

  local script
  for script in "${scripts[@]}"; do
    local tmp_dir bin_dir log_file stdout_file stderr_file harness_root repo_top normalized_harness_root current_branch
    tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-harness-git-bootstrap.XXXXXX")"
    bin_dir="$tmp_dir/bin"
    log_file="$tmp_dir/log.txt"
    stdout_file="$tmp_dir/stdout.txt"
    stderr_file="$tmp_dir/stderr.txt"
    harness_root="$tmp_dir/harness"

    make_project_create_mock_toolchain "$bin_dir"
    : >"$log_file"

    if PATH="$bin_dir:$PATH" \
      LOG_FILE="$log_file" \
      MAESTRO_ENSURE_DASHBOARD_DIST_BIN="$bin_dir/ensure-dashboard-dist" \
      E2E_ROOT="$harness_root" \
      bash "$script" >"$stdout_file" 2>"$stderr_file"; then
      fail "expected $(basename "$script") to stop after the mocked project create path"
    fi

    assert_contains "$log_file" "maestro project create"
    repo_top="$(git -C "$harness_root" rev-parse --show-toplevel 2>/dev/null || true)"
    normalized_harness_root="$(cd "$harness_root" && pwd -P)"
    if [[ -n "$repo_top" ]]; then
      repo_top="$(cd "$repo_top" && pwd -P)"
    fi
    if [[ "$repo_top" != "$normalized_harness_root" ]]; then
      fail "expected $harness_root to be a git repo before project create for $(basename "$script")"
    fi
    if ! git -C "$harness_root" rev-parse HEAD >/dev/null 2>&1; then
      fail "expected $harness_root to have an initial commit for $(basename "$script")"
    fi
    current_branch="$(git -C "$harness_root" branch --show-current)"
    if [[ "$current_branch" != "main" ]]; then
      fail "expected $harness_root to be on main before project create for $(basename "$script"), got '$current_branch'"
    fi
    if ! git -C "$harness_root" ls-files --error-unmatch WORKFLOW.md >/dev/null 2>&1; then
      fail "expected WORKFLOW.md to be tracked in $harness_root for $(basename "$script")"
    fi
  done
}

test_scripts_use_quiet_issue_identifiers() {
  local scripts=(
    "$ROOT_DIR/scripts/e2e_real_codex.sh:basic:REAL-1:REAL-2"
    "$ROOT_DIR/scripts/e2e_real_codex_phases.sh:phases:PHAS-1:PHAS-2"
  )

  local entry
  for entry in "${scripts[@]}"; do
    local script mode first_identifier second_identifier tmp_dir bin_dir log_file stdout_file stderr_file harness_root
    IFS=: read -r script mode first_identifier second_identifier <<<"$entry"
    tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-harness-identifiers.XXXXXX")"
    bin_dir="$tmp_dir/bin"
    log_file="$tmp_dir/log.txt"
    stdout_file="$tmp_dir/stdout.txt"
    stderr_file="$tmp_dir/stderr.txt"
    harness_root="$tmp_dir/harness"

    make_issue_identifier_mock_toolchain "$bin_dir"
    : >"$log_file"

    if PATH="$bin_dir:/usr/bin:/bin" \
      LOG_FILE="$log_file" \
      MOCK_HARNESS_ROOT="$harness_root" \
      E2E_TEST_MODE="$mode" \
      MAESTRO_ENSURE_DASHBOARD_DIST_BIN="$bin_dir/ensure-dashboard-dist" \
      E2E_ROOT="$harness_root" \
      bash "$script" >"$stdout_file" 2>"$stderr_file"; then
      fail "expected $(basename "$script") to stop after the mocked identifier path"
    fi

    assert_contains "$log_file" "maestro issue create"
    case "$mode" in
      basic)
        assert_contains "$log_file" "maestro issue move $first_identifier ready --db"
        assert_contains "$log_file" "maestro issue move $second_identifier ready --db"
        ;;
      phases)
        assert_contains "$log_file" "maestro issue update $first_identifier --desc"
        assert_contains "$log_file" "maestro issue update $second_identifier --desc"
        ;;
    esac
  done
}

test_image_harness_uses_project_workspace_and_issue_assets_dir() {
  local tmp_dir bin_dir log_file stdout_file stderr_file harness_root
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/e2e-harness-images.XXXXXX")"
  bin_dir="$tmp_dir/bin"
  log_file="$tmp_dir/log.txt"
  stdout_file="$tmp_dir/stdout.txt"
  stderr_file="$tmp_dir/stderr.txt"
  harness_root="$tmp_dir/harness"

  make_image_harness_mock_toolchain "$bin_dir"
  : >"$log_file"

  PATH="$bin_dir:/usr/bin:/bin" \
    LOG_FILE="$log_file" \
    MOCK_HARNESS_ROOT="$harness_root" \
    MAESTRO_ENSURE_DASHBOARD_DIST_BIN="$bin_dir/ensure-dashboard-dist" \
    E2E_ROOT="$harness_root" \
    E2E_KEEP_HARNESS=0 \
    bash "$ROOT_DIR/scripts/e2e_real_codex_issue_images.sh" >"$stdout_file" 2>"$stderr_file"

  assert_contains "$log_file" "maestro run $harness_root --workflow $harness_root/WORKFLOW.md --db $harness_root/.maestro/maestro.db --port"
  assert_contains "$log_file" "maestro issue update IMAG-1 --permission-profile full-access --db $harness_root/.maestro/maestro.db --quiet"
  assert_in_order "$log_file" \
    "maestro issue create" \
    "maestro issue images add" \
    "maestro issue update IMAG-1 --permission-profile full-access --db $harness_root/.maestro/maestro.db --quiet" \
    "maestro issue move IMAG-1 ready --db $harness_root/.maestro/maestro.db"
  assert_contains "$stdout_file" "Real Codex app-server image e2e flow completed successfully."
  assert_contains "$stdout_file" "staged image -> $harness_root/workspaces/image-e2e-project/IMAG-1/.maestro/issue-assets/ast_img-uploaded-issue-image.png"
  assert_contains "$stdout_file" "final answer MAESTRO"
}

main() {
  test_scripts_bootstrap_dashboard_dist_before_build
  test_scripts_initialize_git_repo_before_project_create
  test_scripts_use_quiet_issue_identifiers
  test_image_harness_uses_project_workspace_and_issue_assets_dir
}

main "$@"
