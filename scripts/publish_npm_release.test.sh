#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT_UNDER_TEST="$ROOT_DIR/scripts/publish_npm_release.sh"

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

assert_not_contains() {
  local file="$1"
  local pattern="$2"
  if grep -Fq -- "$pattern" "$file"; then
    fail "did not expect to find '$pattern' in $file"
  fi
}

write_mock_commands() {
  local bin_dir="$1"
  mkdir -p "$bin_dir"

  cat >"$bin_dir/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'git %s\n' "$*" >>"$MOCK_LOG"
case "$1" in
  status)
    [[ "${MOCK_GIT_STATUS_OUTPUT:-}" == "" ]] || printf '%s' "$MOCK_GIT_STATUS_OUTPUT"
    exit 0
    ;;
  rev-parse)
    if [[ "$2" == "--abbrev-ref" ]]; then
      printf 'main\n'
      exit 0
    fi
    if [[ "$2" == "HEAD" || "$2" == "origin/main" ]]; then
      printf '%s\n' "$MOCK_HEAD_SHA"
      exit 0
    fi
    if [[ "$2" == "-q" && "$3" == "--verify" ]]; then
      if [[ "$4" == refs/tags/* && -n "${MOCK_LOCAL_TAG_SHA:-}" ]]; then
        printf '%s\n' "$MOCK_LOCAL_TAG_SHA"
        exit 0
      fi
      exit 1
    fi
    ;;
  rev-list)
    if [[ "$2" == "-n" && "$3" == "1" && -n "${MOCK_LOCAL_TAG_SHA:-}" ]]; then
      printf '%s\n' "$MOCK_LOCAL_TAG_SHA"
      exit 0
    fi
    ;;
  ls-remote)
    if [[ -n "${MOCK_REMOTE_TAG_SHA:-}" ]]; then
      printf '%s\trefs/tags/%s^{}\n' "$MOCK_REMOTE_TAG_SHA" "${MOCK_VERSION_TAG}"
      printf '%s\trefs/tags/%s\n' "${MOCK_REMOTE_TAG_OBJECT_SHA:-$MOCK_REMOTE_TAG_SHA}" "${MOCK_VERSION_TAG}"
      exit 0
    fi
    exit 2
    ;;
  fetch|pull|tag|push)
    exit 0
    ;;
esac
printf 'unexpected git invocation: %s\n' "$*" >&2
exit 1
EOF

  cat >"$bin_dir/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'gh %s\n' "$*" >>"$MOCK_LOG"
if [[ "$1" == "run" && "$2" == "list" ]]; then
  cat "$MOCK_RUN_LIST_JSON"
  exit 0
fi
if [[ "$1" == "run" && "$2" == "view" ]]; then
  cat "$MOCK_RUN_VIEW_JSON"
  exit 0
fi
if [[ "$1" == "run" && "$2" == "download" ]]; then
  target_dir=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --dir)
        target_dir="$2"
        shift 2
        ;;
      *)
        shift
        ;;
    esac
  done
  [[ -n "$target_dir" ]] || exit 1
  mkdir -p "$target_dir"
  if [[ -n "${MOCK_ARTIFACT_SOURCE:-}" ]]; then
    cp -R "$MOCK_ARTIFACT_SOURCE"/. "$target_dir"/
  fi
  exit 0
fi
printf 'unexpected gh invocation: %s\n' "$*" >&2
exit 1
EOF

  cat >"$bin_dir/npm" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'npm %s\n' "$*" >>"$MOCK_LOG"
if [[ "$1" == "view" && "$3" == "dist-tags" && "$4" == "--json" ]]; then
  node -e '
    const fs = require("fs");
    const file = process.argv[1];
    const pkg = process.argv[2];
    const tag = process.argv[3];
    const state = fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, "utf8")) : {};
    const version = state[pkg];
    process.stdout.write(JSON.stringify(version ? { [tag]: version } : {}));
  ' "$MOCK_PUBLISHED_STATE_FILE" "$2" "$MOCK_DIST_TAG"
  exit 0
fi
if [[ "$1" == "whoami" ]]; then
  if [[ "${MOCK_NPM_WHOAMI_EXIT_CODE:-0}" != "0" ]]; then
    exit "$MOCK_NPM_WHOAMI_EXIT_CODE"
  fi
  printf '%s\n' "${MOCK_NPM_WHOAMI:-olhapi}"
  exit 0
fi
if [[ "$1" == "publish" ]]; then
  node -e '
    const fs = require("fs");
    const file = process.argv[1];
    const pkg = process.argv[2];
    const version = process.argv[3];
    const state = fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, "utf8")) : {};
    state[pkg] = version;
    fs.writeFileSync(file, JSON.stringify(state));
  ' "$MOCK_PUBLISHED_STATE_FILE" "@olhapi/maestro" "$MOCK_VERSION"
  exit 0
fi
printf 'unexpected npm invocation: %s\n' "$*" >&2
exit 1
EOF

  cat >"$bin_dir/pnpm" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'pnpm %s\n' "$*" >>"$MOCK_LOG"
if [[ "$1" == "verify:pre-push" ]]; then
  exit 0
fi
printf 'unexpected pnpm invocation: %s\n' "$*" >&2
exit 1
EOF

  chmod +x "$bin_dir/git" "$bin_dir/gh" "$bin_dir/npm" "$bin_dir/pnpm"
}

write_run_list_json() {
  local file="$1"
  local run_id="$2"
  local conclusion="$3"
  local head_branch="$4"
  local head_sha="$5"
  cat >"$file" <<EOF
[{"conclusion":"$conclusion","databaseId":$run_id,"headBranch":"$head_branch","headSha":"$head_sha","status":"completed","url":"https://example.com/$run_id"}]
EOF
}

write_success_run_json() {
  local file="$1"
  cat >"$file" <<'EOF'
{"conclusion":"success","databaseId":101,"jobs":[
  {"name":"publish-ghcr","conclusion":"success"},
  {"name":"build-root-package","conclusion":"success"},
  {"name":"registry-install-smoke","conclusion":"success"},
  {"name":"publish-npm","conclusion":"success"}
],"status":"completed","url":"https://example.com/success"}
EOF
}

write_manual_fallback_run_json() {
  local file="$1"
  cat >"$file" <<'EOF'
{"conclusion":"failure","databaseId":202,"jobs":[
  {"name":"publish-ghcr","conclusion":"success"},
  {"name":"build-root-package","conclusion":"success"},
  {"name":"registry-install-smoke","conclusion":"success"},
  {"name":"publish-npm","conclusion":"failure"}
],"status":"completed","url":"https://example.com/fallback"}
EOF
}

write_published_state() {
  local file="$1"
  shift
  node -e '
    const fs = require("fs");
    const file = process.argv[1];
    const data = {};
    for (const entry of process.argv.slice(2)) {
      const separator = entry.lastIndexOf("=");
      data[entry.slice(0, separator)] = entry.slice(separator + 1);
    }
    fs.writeFileSync(file, JSON.stringify(data));
  ' "$file" "$@"
}

create_artifacts() {
  local dir="$1"
  local version="$2"
  mkdir -p "$dir/npm-root-package"
  touch "$dir/npm-root-package/olhapi-maestro-$version.tgz"
}

run_success_path_test() {
  local tmp_dir
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/publish-release-test-success.XXXXXX")"
  trap 'rm -rf "$tmp_dir"' RETURN

  write_mock_commands "$tmp_dir/bin"
  write_run_list_json "$tmp_dir/run-list.json" 101 success "v1.2.3-rc.1" "abc123"
  write_success_run_json "$tmp_dir/run-view.json"
  write_published_state "$tmp_dir/published.json" "@olhapi/maestro=1.2.3-rc.1"

  PATH="$tmp_dir/bin:$PATH" \
  MOCK_LOG="$tmp_dir/log.txt" \
  MOCK_HEAD_SHA="abc123" \
  MOCK_RUN_LIST_JSON="$tmp_dir/run-list.json" \
  MOCK_RUN_VIEW_JSON="$tmp_dir/run-view.json" \
  MOCK_PUBLISHED_STATE_FILE="$tmp_dir/published.json" \
  MOCK_DIST_TAG="next" \
  MOCK_VERSION="1.2.3-rc.1" \
  MOCK_VERSION_TAG="v1.2.3-rc.1" \
  RELEASE_POLL_SEC=0 \
  RELEASE_RUN_LOOKUP_TIMEOUT_SEC=1 \
  RELEASE_REGISTRY_TIMEOUT_SEC=1 \
  "$SCRIPT_UNDER_TEST" "1.2.3-rc.1"

  assert_contains "$tmp_dir/log.txt" "pnpm verify:pre-push"
  assert_contains "$tmp_dir/log.txt" "git tag -a v1.2.3-rc.1 -m Release v1.2.3-rc.1"
  assert_contains "$tmp_dir/log.txt" "git push origin refs/tags/v1.2.3-rc.1"
  assert_not_contains "$tmp_dir/log.txt" "gh run download"
  assert_not_contains "$tmp_dir/log.txt" "npm publish --access public --tag next"
}

run_manual_fallback_test() {
  local tmp_dir
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/publish-release-test-fallback.XXXXXX")"
  trap 'rm -rf "$tmp_dir"' RETURN

  write_mock_commands "$tmp_dir/bin"
  write_run_list_json "$tmp_dir/run-list.json" 202 failure "v1.2.3" "def456"
  write_manual_fallback_run_json "$tmp_dir/run-view.json"
  create_artifacts "$tmp_dir/artifacts" "1.2.3"
  write_published_state "$tmp_dir/published.json"

  PATH="$tmp_dir/bin:$PATH" \
  MOCK_LOG="$tmp_dir/log.txt" \
  MOCK_HEAD_SHA="def456" \
  MOCK_RUN_LIST_JSON="$tmp_dir/run-list.json" \
  MOCK_RUN_VIEW_JSON="$tmp_dir/run-view.json" \
  MOCK_PUBLISHED_STATE_FILE="$tmp_dir/published.json" \
  MOCK_ARTIFACT_SOURCE="$tmp_dir/artifacts" \
  MOCK_DIST_TAG="latest" \
  MOCK_VERSION="1.2.3" \
  MOCK_VERSION_TAG="v1.2.3" \
  RELEASE_POLL_SEC=0 \
  RELEASE_RUN_LOOKUP_TIMEOUT_SEC=1 \
  RELEASE_REGISTRY_TIMEOUT_SEC=1 \
  "$SCRIPT_UNDER_TEST" "1.2.3"

  assert_contains "$tmp_dir/log.txt" "gh run download 202 --repo olhapi/maestro --dir"
  assert_contains "$tmp_dir/log.txt" "npm whoami"
  assert_contains "$tmp_dir/log.txt" "npm publish --access public --tag latest"
}

run_existing_remote_tag_resume_test() {
  local tmp_dir
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/publish-release-test-resume.XXXXXX")"
  trap 'rm -rf "$tmp_dir"' RETURN

  write_mock_commands "$tmp_dir/bin"
  write_run_list_json "$tmp_dir/run-list.json" 404 failure "v1.2.3-rc.4" "tag404"
  write_manual_fallback_run_json "$tmp_dir/run-view.json"
  create_artifacts "$tmp_dir/artifacts" "1.2.3-rc.4"
  write_published_state "$tmp_dir/published.json"

  PATH="$tmp_dir/bin:$PATH" \
  MOCK_LOG="$tmp_dir/log.txt" \
  MOCK_HEAD_SHA="head999" \
  MOCK_LOCAL_TAG_SHA="tag404" \
  MOCK_REMOTE_TAG_SHA="tag404" \
  MOCK_REMOTE_TAG_OBJECT_SHA="obj404" \
  MOCK_RUN_LIST_JSON="$tmp_dir/run-list.json" \
  MOCK_RUN_VIEW_JSON="$tmp_dir/run-view.json" \
  MOCK_PUBLISHED_STATE_FILE="$tmp_dir/published.json" \
  MOCK_ARTIFACT_SOURCE="$tmp_dir/artifacts" \
  MOCK_DIST_TAG="next" \
  MOCK_VERSION="1.2.3-rc.4" \
  MOCK_VERSION_TAG="v1.2.3-rc.4" \
  RELEASE_POLL_SEC=0 \
  RELEASE_RUN_LOOKUP_TIMEOUT_SEC=1 \
  RELEASE_REGISTRY_TIMEOUT_SEC=1 \
  "$SCRIPT_UNDER_TEST" "1.2.3-rc.4"

  assert_not_contains "$tmp_dir/log.txt" "pnpm verify:pre-push"
  assert_not_contains "$tmp_dir/log.txt" "git push origin refs/tags/v1.2.3-rc.4"
  assert_contains "$tmp_dir/log.txt" "gh run download 404 --repo olhapi/maestro --dir"
}

run_stale_local_tag_guard_test() {
  local tmp_dir
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/publish-release-test-stale-tag.XXXXXX")"
  trap 'rm -rf "$tmp_dir"' RETURN

  write_mock_commands "$tmp_dir/bin"
  write_run_list_json "$tmp_dir/run-list.json" 606 success "v1.2.3-rc.6" "head606"
  write_success_run_json "$tmp_dir/run-view.json"

  if PATH="$tmp_dir/bin:$PATH" \
    MOCK_LOG="$tmp_dir/log.txt" \
    MOCK_HEAD_SHA="head606" \
    MOCK_LOCAL_TAG_SHA="tag606" \
    MOCK_RUN_LIST_JSON="$tmp_dir/run-list.json" \
    MOCK_RUN_VIEW_JSON="$tmp_dir/run-view.json" \
    MOCK_DIST_TAG="next" \
    MOCK_VERSION="1.2.3-rc.6" \
    MOCK_VERSION_TAG="v1.2.3-rc.6" \
    RELEASE_POLL_SEC=0 \
    RELEASE_RUN_LOOKUP_TIMEOUT_SEC=1 \
    RELEASE_REGISTRY_TIMEOUT_SEC=1 \
    "$SCRIPT_UNDER_TEST" "1.2.3-rc.6"; then
    fail "stale local tag test should have failed"
  fi

  assert_not_contains "$tmp_dir/log.txt" "pnpm verify:pre-push"
  assert_not_contains "$tmp_dir/log.txt" "git push origin refs/tags/v1.2.3-rc.6"
}

run_dirty_worktree_guard_test() {
  local tmp_dir
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/publish-release-test-dirty.XXXXXX")"
  trap 'rm -rf "$tmp_dir"' RETURN

  write_mock_commands "$tmp_dir/bin"
  write_run_list_json "$tmp_dir/run-list.json" 303 success "v9.9.9-rc.1" "ghi789"
  write_success_run_json "$tmp_dir/run-view.json"

  if PATH="$tmp_dir/bin:$PATH" \
    MOCK_LOG="$tmp_dir/log.txt" \
    MOCK_HEAD_SHA="ghi789" \
    MOCK_GIT_STATUS_OUTPUT=" M package.json" \
    MOCK_RUN_LIST_JSON="$tmp_dir/run-list.json" \
    MOCK_RUN_VIEW_JSON="$tmp_dir/run-view.json" \
    MOCK_DIST_TAG="next" \
    MOCK_VERSION="9.9.9-rc.1" \
    MOCK_VERSION_TAG="v9.9.9-rc.1" \
    RELEASE_POLL_SEC=0 \
    RELEASE_RUN_LOOKUP_TIMEOUT_SEC=1 \
    RELEASE_REGISTRY_TIMEOUT_SEC=1 \
    "$SCRIPT_UNDER_TEST" "9.9.9-rc.1"; then
    fail "dirty worktree test should have failed"
  fi

  assert_not_contains "$tmp_dir/log.txt" "pnpm verify:pre-push"
}

run_success_path_test
run_manual_fallback_test
run_existing_remote_tag_resume_test
run_stale_local_tag_guard_test
run_dirty_worktree_guard_test
