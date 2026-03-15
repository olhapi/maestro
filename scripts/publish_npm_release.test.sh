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

assert_in_order() {
  local file="$1"
  shift
  local previous_line=0
  local pattern
  for pattern in "$@"; do
    local line
    line="$(grep -nF -- "$pattern" "$file" | head -n 1 | cut -d: -f1)"
    if [[ -z "$line" ]]; then
      fail "missing ordered pattern '$pattern' in $file"
    fi
    if (( line <= previous_line )); then
      fail "pattern '$pattern' appeared out of order in $file"
    fi
    previous_line="$line"
  done
}

assert_publish_sequence() {
  local file="$1"
  shift
  local expected=("$@")
  local actual=()
  local line
  while IFS= read -r line; do
    actual+=("$line")
  done < <(grep '^npm publish ' -- "$file" || true)
  if [[ "${#actual[@]}" -ne "${#expected[@]}" ]]; then
    fail "expected ${#expected[@]} publish commands, found ${#actual[@]}"
  fi
  local index
  for index in "${!expected[@]}"; do
    if [[ "${actual[$index]}" != *"${expected[$index]}"* ]]; then
      fail "unexpected publish order at index $index: ${actual[$index]}"
    fi
  done
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
    if [[ "$2" == "--porcelain" ]]; then
      printf '%s' "${MOCK_GIT_STATUS_OUTPUT:-}"
      exit 0
    fi
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
      exit 1
    fi
    ;;
  ls-remote)
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
resolve_package_from_tarball() {
  case "$(basename "$1")" in
    "olhapi-maestro-darwin-arm64-$MOCK_VERSION.tgz") printf '@olhapi/maestro-darwin-arm64\n' ;;
    "olhapi-maestro-darwin-x64-$MOCK_VERSION.tgz") printf '@olhapi/maestro-darwin-x64\n' ;;
    "olhapi-maestro-linux-x64-gnu-$MOCK_VERSION.tgz") printf '@olhapi/maestro-linux-x64-gnu\n' ;;
    "olhapi-maestro-linux-arm64-gnu-$MOCK_VERSION.tgz") printf '@olhapi/maestro-linux-arm64-gnu\n' ;;
    "olhapi-maestro-win32-x64-$MOCK_VERSION.tgz") printf '@olhapi/maestro-win32-x64\n' ;;
    "olhapi-maestro-$MOCK_VERSION.tgz") printf '@olhapi/maestro\n' ;;
    *)
      printf 'unexpected tarball: %s\n' "$1" >&2
      exit 1
      ;;
  esac
}
if [[ "$1" == "view" && "$3" == "dist-tags" && "$4" == "--json" ]]; then
  node -e '
    const fs = require("fs");
    const file = process.argv[1];
    const packageName = process.argv[2];
    const distTag = process.argv[3];
    const data = fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, "utf8")) : {};
    const version = data[packageName];
    process.stdout.write(JSON.stringify(version ? { [distTag]: version } : {}));
  ' "$MOCK_PUBLISHED_STATE_FILE" "$2" "$MOCK_DIST_TAG"
  exit 0
fi
if [[ "$1" == "publish" ]]; then
  package_name="$(resolve_package_from_tarball "${@: -1}")"
  node -e '
    const fs = require("fs");
    const file = process.argv[1];
    const packageName = process.argv[2];
    const version = process.argv[3];
    const failOnRepublish = process.argv[4] === "1";
    const data = fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, "utf8")) : {};
    if (failOnRepublish && data[packageName] === version) {
      process.stderr.write(`cannot republish ${packageName}@${version}\n`);
      process.exit(1);
    }
    data[packageName] = version;
    fs.writeFileSync(file, JSON.stringify(data));
  ' "$MOCK_PUBLISHED_STATE_FILE" "$package_name" "$MOCK_VERSION" "${MOCK_FAIL_ON_REPUBLISH:-0}"
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

write_success_run_json() {
  local file="$1"
  cat >"$file" <<'EOF'
{"conclusion":"success","databaseId":101,"jobs":[
  {"name":"build-root-package","conclusion":"success"},
  {"name":"build-leaf-packages (darwin-arm64)","conclusion":"success"},
  {"name":"build-leaf-packages (darwin-x64)","conclusion":"success"},
  {"name":"build-leaf-packages (linux-x64-gnu)","conclusion":"success"},
  {"name":"build-leaf-packages (linux-arm64-gnu)","conclusion":"success"},
  {"name":"build-leaf-packages (win32-x64)","conclusion":"success"},
  {"name":"registry-install-smoke (darwin-arm64)","conclusion":"success"},
  {"name":"registry-install-smoke (darwin-x64)","conclusion":"success"},
  {"name":"registry-install-smoke (linux-x64-gnu)","conclusion":"success"},
  {"name":"registry-install-smoke (linux-arm64-gnu)","conclusion":"success"},
  {"name":"registry-install-smoke (win32-x64)","conclusion":"success"},
  {"name":"publish-npm","conclusion":"success"}
],"status":"completed","url":"https://example.com/success"}
EOF
}

write_manual_fallback_run_json() {
  local file="$1"
  cat >"$file" <<'EOF'
{"conclusion":"failure","databaseId":202,"jobs":[
  {"name":"build-root-package","conclusion":"success"},
  {"name":"build-leaf-packages (darwin-arm64)","conclusion":"success"},
  {"name":"build-leaf-packages (darwin-x64)","conclusion":"success"},
  {"name":"build-leaf-packages (linux-x64-gnu)","conclusion":"success"},
  {"name":"build-leaf-packages (linux-arm64-gnu)","conclusion":"success"},
  {"name":"build-leaf-packages (win32-x64)","conclusion":"success"},
  {"name":"registry-install-smoke (darwin-arm64)","conclusion":"success"},
  {"name":"registry-install-smoke (darwin-x64)","conclusion":"success"},
  {"name":"registry-install-smoke (linux-x64-gnu)","conclusion":"success"},
  {"name":"registry-install-smoke (linux-arm64-gnu)","conclusion":"success"},
  {"name":"registry-install-smoke (win32-x64)","conclusion":"success"},
  {"name":"publish-npm","conclusion":"failure"}
],"status":"completed","url":"https://example.com/fallback"}
EOF
}

write_run_list_json() {
  local file="$1"
  local run_id="$2"
  local conclusion="$3"
  cat >"$file" <<EOF
[{"conclusion":"$conclusion","databaseId":$run_id,"headBranch":"ignored","status":"completed","url":"https://example.com/$run_id"}]
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
  mkdir -p \
    "$dir/npm-leaf-darwin-arm64" \
    "$dir/npm-leaf-darwin-x64" \
    "$dir/npm-leaf-linux-x64-gnu" \
    "$dir/npm-leaf-linux-arm64-gnu" \
    "$dir/npm-leaf-win32-x64" \
    "$dir/npm-root-package"
  touch \
    "$dir/npm-leaf-darwin-arm64/olhapi-maestro-darwin-arm64-$version.tgz" \
    "$dir/npm-leaf-darwin-x64/olhapi-maestro-darwin-x64-$version.tgz" \
    "$dir/npm-leaf-linux-x64-gnu/olhapi-maestro-linux-x64-gnu-$version.tgz" \
    "$dir/npm-leaf-linux-arm64-gnu/olhapi-maestro-linux-arm64-gnu-$version.tgz" \
    "$dir/npm-leaf-win32-x64/olhapi-maestro-win32-x64-$version.tgz" \
    "$dir/npm-root-package/olhapi-maestro-$version.tgz"
}

run_success_path_test() {
  local tmp_dir
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/publish-release-test-success.XXXXXX")"
  trap 'rm -rf "$tmp_dir"' RETURN

  write_mock_commands "$tmp_dir/bin"
  write_run_list_json "$tmp_dir/run-list.json" 101 success
  write_success_run_json "$tmp_dir/run-view.json"
  write_published_state \
    "$tmp_dir/published.json" \
    "@olhapi/maestro-darwin-arm64=1.2.3-rc.1" \
    "@olhapi/maestro-darwin-x64=1.2.3-rc.1" \
    "@olhapi/maestro-linux-x64-gnu=1.2.3-rc.1" \
    "@olhapi/maestro-linux-arm64-gnu=1.2.3-rc.1" \
    "@olhapi/maestro-win32-x64=1.2.3-rc.1" \
    "@olhapi/maestro=1.2.3-rc.1"

  PATH="$tmp_dir/bin:$PATH" \
  MOCK_LOG="$tmp_dir/log.txt" \
  MOCK_HEAD_SHA="abc123" \
  MOCK_RUN_LIST_JSON="$tmp_dir/run-list.json" \
  MOCK_RUN_VIEW_JSON="$tmp_dir/run-view.json" \
  MOCK_PUBLISHED_STATE_FILE="$tmp_dir/published.json" \
  MOCK_DIST_TAG="next" \
  MOCK_VERSION="1.2.3-rc.1" \
  RELEASE_POLL_SEC=0 \
  RELEASE_RUN_LOOKUP_TIMEOUT_SEC=1 \
  RELEASE_REGISTRY_TIMEOUT_SEC=1 \
  "$SCRIPT_UNDER_TEST" "1.2.3-rc.1"

  assert_contains "$tmp_dir/log.txt" "pnpm verify:pre-push"
  assert_contains "$tmp_dir/log.txt" "gh run list --repo olhapi/maestro --workflow release-npm.yml --commit abc123 --event push --limit 1 --json databaseId,status,conclusion,url"
  assert_not_contains "$tmp_dir/log.txt" "--branch"
  assert_contains "$tmp_dir/log.txt" "git tag -a v1.2.3-rc.1 -m Release v1.2.3-rc.1"
  assert_contains "$tmp_dir/log.txt" "git push origin refs/tags/v1.2.3-rc.1"
  assert_not_contains "$tmp_dir/log.txt" "gh run download"
  assert_not_contains "$tmp_dir/log.txt" "npm publish --access public --tag next"
}

run_double_dash_passthrough_test() {
  local tmp_dir
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/publish-release-test-double-dash.XXXXXX")"
  trap 'rm -rf "$tmp_dir"' RETURN

  write_mock_commands "$tmp_dir/bin"
  write_run_list_json "$tmp_dir/run-list.json" 111 success
  write_success_run_json "$tmp_dir/run-view.json"
  write_published_state \
    "$tmp_dir/published.json" \
    "@olhapi/maestro-darwin-arm64=1.2.3-rc.2" \
    "@olhapi/maestro-darwin-x64=1.2.3-rc.2" \
    "@olhapi/maestro-linux-x64-gnu=1.2.3-rc.2" \
    "@olhapi/maestro-linux-arm64-gnu=1.2.3-rc.2" \
    "@olhapi/maestro-win32-x64=1.2.3-rc.2" \
    "@olhapi/maestro=1.2.3-rc.2"

  PATH="$tmp_dir/bin:$PATH" \
  MOCK_LOG="$tmp_dir/log.txt" \
  MOCK_HEAD_SHA="double111" \
  MOCK_RUN_LIST_JSON="$tmp_dir/run-list.json" \
  MOCK_RUN_VIEW_JSON="$tmp_dir/run-view.json" \
  MOCK_PUBLISHED_STATE_FILE="$tmp_dir/published.json" \
  MOCK_DIST_TAG="next" \
  MOCK_VERSION="1.2.3-rc.2" \
  RELEASE_POLL_SEC=0 \
  RELEASE_RUN_LOOKUP_TIMEOUT_SEC=1 \
  RELEASE_REGISTRY_TIMEOUT_SEC=1 \
  "$SCRIPT_UNDER_TEST" "--" "v1.2.3-rc.2"

  assert_contains "$tmp_dir/log.txt" "git tag -a v1.2.3-rc.2 -m Release v1.2.3-rc.2"
  assert_contains "$tmp_dir/log.txt" "git push origin refs/tags/v1.2.3-rc.2"
  assert_not_contains "$tmp_dir/log.txt" "gh run download"
}

run_manual_fallback_test() {
  local tmp_dir
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/publish-release-test-fallback.XXXXXX")"
  trap 'rm -rf "$tmp_dir"' RETURN

  write_mock_commands "$tmp_dir/bin"
  write_run_list_json "$tmp_dir/run-list.json" 202 failure
  write_manual_fallback_run_json "$tmp_dir/run-view.json"
  create_artifacts "$tmp_dir/artifacts" "1.2.3"
  write_published_state \
    "$tmp_dir/published.json" \
    "@olhapi/maestro-darwin-arm64=1.2.3"

  PATH="$tmp_dir/bin:$PATH" \
  MOCK_LOG="$tmp_dir/log.txt" \
  MOCK_HEAD_SHA="def456" \
  MOCK_RUN_LIST_JSON="$tmp_dir/run-list.json" \
  MOCK_RUN_VIEW_JSON="$tmp_dir/run-view.json" \
  MOCK_PUBLISHED_STATE_FILE="$tmp_dir/published.json" \
  MOCK_FAIL_ON_REPUBLISH=1 \
  MOCK_ARTIFACT_SOURCE="$tmp_dir/artifacts" \
  MOCK_DIST_TAG="latest" \
  MOCK_VERSION="1.2.3" \
  RELEASE_POLL_SEC=0 \
  RELEASE_RUN_LOOKUP_TIMEOUT_SEC=1 \
  RELEASE_REGISTRY_TIMEOUT_SEC=1 \
  "$SCRIPT_UNDER_TEST" "1.2.3"

  assert_contains "$tmp_dir/log.txt" "gh run list --repo olhapi/maestro --workflow release-npm.yml --commit def456 --event push --limit 1 --json databaseId,status,conclusion,url"
  assert_contains "$tmp_dir/log.txt" "gh run download 202 --repo olhapi/maestro --dir"
  assert_publish_sequence \
    "$tmp_dir/log.txt" \
    "olhapi-maestro-darwin-x64-1.2.3.tgz" \
    "olhapi-maestro-linux-x64-gnu-1.2.3.tgz" \
    "olhapi-maestro-linux-arm64-gnu-1.2.3.tgz" \
    "olhapi-maestro-win32-x64-1.2.3.tgz" \
    "olhapi-maestro-1.2.3.tgz"
}

run_dirty_worktree_guard_test() {
  local tmp_dir
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/publish-release-test-dirty.XXXXXX")"
  trap 'rm -rf "$tmp_dir"' RETURN

  write_mock_commands "$tmp_dir/bin"
  write_run_list_json "$tmp_dir/run-list.json" 303 success
  write_success_run_json "$tmp_dir/run-view.json"

  if PATH="$tmp_dir/bin:$PATH" \
    MOCK_LOG="$tmp_dir/log.txt" \
    MOCK_HEAD_SHA="ghi789" \
    MOCK_GIT_STATUS_OUTPUT=" M package.json" \
    MOCK_RUN_LIST_JSON="$tmp_dir/run-list.json" \
    MOCK_RUN_VIEW_JSON="$tmp_dir/run-view.json" \
    MOCK_DIST_TAG="next" \
    MOCK_VERSION="9.9.9-rc.1" \
    RELEASE_POLL_SEC=0 \
    RELEASE_RUN_LOOKUP_TIMEOUT_SEC=1 \
    RELEASE_REGISTRY_TIMEOUT_SEC=1 \
    "$SCRIPT_UNDER_TEST" "9.9.9-rc.1"; then
    fail "dirty worktree test should have failed"
  fi

  assert_not_contains "$tmp_dir/log.txt" "pnpm verify:pre-push"
}

run_success_path_test
run_double_dash_passthrough_test
run_manual_fallback_test
run_dirty_worktree_guard_test
