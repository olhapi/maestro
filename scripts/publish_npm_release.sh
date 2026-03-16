#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO="${RELEASE_GH_REPO:-olhapi/maestro}"
WORKFLOW_FILE="${RELEASE_GH_WORKFLOW:-release-npm.yml}"
POLL_SEC="${RELEASE_POLL_SEC:-15}"
RUN_LOOKUP_TIMEOUT_SEC="${RELEASE_RUN_LOOKUP_TIMEOUT_SEC:-120}"
REGISTRY_TIMEOUT_SEC="${RELEASE_REGISTRY_TIMEOUT_SEC:-120}"
SKIP_MANUAL_FALLBACK="${RELEASE_SKIP_MANUAL_FALLBACK:-0}"

ROOT_PACKAGE="@olhapi/maestro"
LEAF_PACKAGES=(
  "@olhapi/maestro-darwin-arm64"
  "@olhapi/maestro-darwin-x64"
  "@olhapi/maestro-linux-x64-gnu"
  "@olhapi/maestro-linux-arm64-gnu"
  "@olhapi/maestro-win32-x64"
)
LEAF_ARTIFACT_DIRS=(
  "npm-leaf-darwin-arm64"
  "npm-leaf-darwin-x64"
  "npm-leaf-linux-x64-gnu"
  "npm-leaf-linux-arm64-gnu"
  "npm-leaf-win32-x64"
)

usage() {
  cat <<'EOF'
Usage:
  scripts/publish_npm_release.sh [--] <version>

Runs the Maestro npm release flow end-to-end:
  - requires a clean local main branch
  - fetches and fast-forward pulls origin/main
  - runs pnpm verify:pre-push
  - creates and pushes the annotated release tag
  - waits for the GitHub Actions release workflow
  - verifies npm dist-tags when GitHub publish succeeds
  - if artifact jobs succeed but publish is skipped or fails, downloads the
    workflow artifacts and publishes them locally in leaf-first order

Environment:
  RELEASE_POLL_SEC                  Poll interval while waiting for Actions
  RELEASE_RUN_LOOKUP_TIMEOUT_SEC    Max seconds to wait for the run to start
  RELEASE_REGISTRY_TIMEOUT_SEC      Max seconds to wait for npm dist-tags
  RELEASE_SKIP_MANUAL_FALLBACK=1    Disable local artifact publish fallback
  RELEASE_GH_REPO                   Override GitHub repo (default olhapi/maestro)
  RELEASE_GH_WORKFLOW               Override workflow file (default release-npm.yml)
  RELEASE_ARTIFACT_DIR              Directory to download workflow artifacts into
EOF
}

log() {
  printf 'release: %s\n' "$*"
}

fail() {
  printf 'release: %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    fail "missing required command: $cmd"
  fi
}

require_commands() {
  require_cmd git
  require_cmd gh
  require_cmd node
  require_cmd npm
  require_cmd pnpm
}

node_query() {
  local script="$1"
  shift
  node -e "$script" "$@"
}

version_from_json() {
  local json="$1"
  local field="$2"
  node_query '
    const data = JSON.parse(process.argv[1]);
    const value = data[process.argv[2]];
    if (value === undefined || value === null) {
      process.exit(1);
    }
    process.stdout.write(String(value));
  ' "$json" "$field"
}

job_conclusion_from_file() {
  local file="$1"
  local job_name="$2"
  node_query '
    const fs = require("fs");
    const data = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    const jobName = process.argv[2];
    const jobs = data.jobs || [];
    const job = jobs.find((entry) => entry.name === jobName);
    process.stdout.write(job ? String(job.conclusion || job.status || "unknown") : "absent");
  ' "$file" "$job_name"
}

manual_fallback_ready_from_file() {
  local file="$1"
  node_query '
    const fs = require("fs");
    const data = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    const expectedLeafCount = Number(process.argv[2]);
    const expectedSmokeCount = Number(process.argv[3]);
    const jobs = data.jobs || [];
    const rootOk = jobs.some((job) => job.name === "build-root-package" && job.conclusion === "success");
    const leafJobs = jobs.filter((job) => job.name.startsWith("build-leaf-packages"));
    const smokeJobs = jobs.filter((job) => job.name.startsWith("registry-install-smoke"));
    const ok =
      rootOk &&
      leafJobs.length === expectedLeafCount &&
      smokeJobs.length === expectedSmokeCount &&
      leafJobs.every((job) => job.conclusion === "success") &&
      smokeJobs.every((job) => job.conclusion === "success");
    process.stdout.write(ok ? "1" : "0");
  ' "$file" "${#LEAF_PACKAGES[@]}" "${#LEAF_PACKAGES[@]}"
}

validate_version() {
  local raw_version="$1"
  if [[ ! "$raw_version" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
    fail "invalid version: $raw_version"
  fi
}

local_tag_target_sha() {
  git rev-list -n 1 "$TAG"
}

remote_tag_target_sha() {
  local remote_refs fallback_sha=""
  if ! remote_refs="$(git ls-remote --tags origin "refs/tags/$TAG^{}" "refs/tags/$TAG" 2>/dev/null)"; then
    return 1
  fi

  local line sha ref
  while IFS=$'\t' read -r sha ref; do
    case "$ref" in
      "refs/tags/$TAG^{}")
        printf '%s\n' "$sha"
        return 0
        ;;
      "refs/tags/$TAG")
        fallback_sha="$sha"
        ;;
    esac
  done <<<"$remote_refs"

  if [[ -n "$fallback_sha" ]]; then
    printf '%s\n' "$fallback_sha"
    return 0
  fi

  return 1
}

ensure_release_branch_state() {
  local current_branch
  current_branch="$(git rev-parse --abbrev-ref HEAD)"
  if [[ "$current_branch" != "main" ]]; then
    fail "release script must run from main, found: $current_branch"
  fi

  if [[ -n "$(git status --porcelain)" ]]; then
    fail "release script requires a clean worktree"
  fi

  log "fetching origin tags"
  git fetch --tags origin
  log "fast-forwarding local main"
  git pull --ff-only origin main

  local local_head remote_head
  local_head="$(git rev-parse HEAD)"
  remote_head="$(git rev-parse origin/main)"
  if [[ "$local_head" != "$remote_head" ]]; then
    fail "local main is not aligned with origin/main after pull"
  fi
  CURRENT_HEAD_SHA="$local_head"

  local local_tag_sha="" remote_tag_sha=""
  local has_local_tag="0"
  local has_remote_tag="0"

  if git rev-parse -q --verify "refs/tags/$TAG" >/dev/null 2>&1; then
    has_local_tag="1"
    local_tag_sha="$(local_tag_target_sha)"
  fi
  if remote_tag_sha="$(remote_tag_target_sha)"; then
    has_remote_tag="1"
  fi

  if [[ "$has_local_tag" == "1" && "$has_remote_tag" == "1" ]]; then
    if [[ "$local_tag_sha" != "$remote_tag_sha" ]]; then
      fail "local tag $TAG points to $local_tag_sha but origin points to $remote_tag_sha"
    fi
    TAG_TARGET_SHA="$local_tag_sha"
    CREATE_RELEASE_TAG="0"
    PUSH_RELEASE_TAG="0"
    RUN_RELEASE_VERIFICATION="0"
    log "tag already exists on origin: $TAG (commit $TAG_TARGET_SHA); resuming release flow"
    return 0
  fi

  if [[ "$has_local_tag" == "1" ]]; then
    if [[ "$local_tag_sha" != "$CURRENT_HEAD_SHA" ]]; then
      fail "local tag $TAG already exists at $local_tag_sha, but current HEAD is $CURRENT_HEAD_SHA"
    fi
    TAG_TARGET_SHA="$local_tag_sha"
    CREATE_RELEASE_TAG="0"
    PUSH_RELEASE_TAG="1"
    RUN_RELEASE_VERIFICATION="1"
    log "tag already exists locally at HEAD: $TAG; reusing it"
    return 0
  fi

  TAG_TARGET_SHA="$CURRENT_HEAD_SHA"
  CREATE_RELEASE_TAG="1"
  PUSH_RELEASE_TAG="1"
  RUN_RELEASE_VERIFICATION="1"
}

find_release_run() {
  local deadline=$((SECONDS + RUN_LOOKUP_TIMEOUT_SEC))
  while (( SECONDS < deadline )); do
    local run_list_json
    run_list_json="$(
      gh run list \
        --repo "$REPO" \
        --workflow "$WORKFLOW_FILE" \
        --branch "$TAG" \
        --event push \
        --limit 5 \
        --json databaseId,status,conclusion,url,headBranch
    )"
    local run_json
    if run_json="$(
      node_query '
        const runs = JSON.parse(process.argv[1]);
        const tag = process.argv[2];
        if (!Array.isArray(runs) || runs.length === 0) {
          process.exit(1);
        }
        const match = runs.find((run) => run.headBranch === tag);
        if (!match) {
          process.exit(1);
        }
        process.stdout.write(JSON.stringify(match));
      ' "$run_list_json" "$TAG" 2>/dev/null
    )"; then
      RELEASE_RUN_ID="$(version_from_json "$run_json" "databaseId")"
      RELEASE_RUN_URL="$(version_from_json "$run_json" "url")"
      return 0
    fi
    sleep "$POLL_SEC"
  done
  fail "timed out waiting for $WORKFLOW_FILE to start for $TAG"
}

wait_for_release_completion() {
  RELEASE_RUN_JSON_FILE="$(mktemp "${TMPDIR:-/tmp}/maestro-release-run.XXXXXX")"
  while true; do
    gh run view \
      "$RELEASE_RUN_ID" \
      --repo "$REPO" \
      --json databaseId,status,conclusion,url,jobs \
      >"$RELEASE_RUN_JSON_FILE"

    local status
    status="$(node_query 'const fs = require("fs"); const data = JSON.parse(fs.readFileSync(process.argv[1], "utf8")); process.stdout.write(String(data.status));' "$RELEASE_RUN_JSON_FILE")"
    if [[ "$status" == "completed" ]]; then
      return 0
    fi
    sleep "$POLL_SEC"
  done
}

download_release_artifacts() {
  if [[ -n "${RELEASE_ARTIFACT_DIR:-}" ]]; then
    RELEASE_ARTIFACT_DIR="$RELEASE_ARTIFACT_DIR"
    mkdir -p "$RELEASE_ARTIFACT_DIR"
  else
    RELEASE_ARTIFACT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/maestro-release-${TAG}.XXXXXX")"
  fi

  log "downloading workflow artifacts into $RELEASE_ARTIFACT_DIR"
  gh run download "$RELEASE_RUN_ID" --repo "$REPO" --dir "$RELEASE_ARTIFACT_DIR"
}

artifact_path() {
  local artifact_dir="$1"
  local tarball_name="$2"
  printf '%s/%s/%s\n' "$RELEASE_ARTIFACT_DIR" "$artifact_dir" "$tarball_name"
}

publish_tarball_if_needed() {
  local pkg="$1"
  local dist_tag="$2"
  local tarball="$3"
  local published_version
  if published_version="$(package_dist_tag_version "$pkg" "$dist_tag" 2>/dev/null)" && [[ "$published_version" == "$VERSION" ]]; then
    log "skipping $pkg@$VERSION because npm dist-tag $dist_tag already points to it"
    return 0
  fi
  npm publish --access public --tag "$dist_tag" "$tarball"
}

ensure_npm_publish_session() {
  local npm_user
  if ! npm_user="$(npm whoami 2>/dev/null)"; then
    fail "npm authentication is required before local artifact publish fallback; run 'npm login --scope=@olhapi --registry=https://registry.npmjs.org/' and verify with 'npm whoami'"
  fi
  log "using npm publisher account: $npm_user"
}

manual_publish_from_artifacts() {
  local dist_tag="$1"
  ensure_npm_publish_session
  download_release_artifacts

  log "publishing workflow artifacts locally with npm dist-tag $dist_tag"
  log "npm may prompt for browser-based authentication if your session needs write confirmation"

  local index
  for index in "${!LEAF_PACKAGES[@]}"; do
    local pkg="${LEAF_PACKAGES[$index]}"
    local dir="${LEAF_ARTIFACT_DIRS[$index]}"
    local tarball
    tarball="$(artifact_path "$dir" "$(printf '%s-%s.tgz' "${pkg#@}" "$VERSION" | tr '/' '-')")"
    if [[ ! -f "$tarball" ]]; then
      fail "missing leaf tarball: $tarball"
    fi
    publish_tarball_if_needed "$pkg" "$dist_tag" "$tarball"
  done

  local root_tarball
  root_tarball="$(artifact_path "npm-root-package" "$(printf '%s-%s.tgz' "${ROOT_PACKAGE#@}" "$VERSION" | tr '/' '-')")"
  if [[ ! -f "$root_tarball" ]]; then
    fail "missing root tarball: $root_tarball"
  fi

  publish_tarball_if_needed "$ROOT_PACKAGE" "$dist_tag" "$root_tarball"
}

package_dist_tag_version() {
  local pkg="$1"
  local dist_tag="$2"
  local dist_tags_json
  if ! dist_tags_json="$(npm view "$pkg" dist-tags --json 2>/dev/null)"; then
    return 1
  fi
  node_query '
    const data = JSON.parse(process.argv[1]);
    const value = data[process.argv[2]];
    if (typeof value !== "string") {
      process.exit(1);
    }
    process.stdout.write(value);
  ' "$dist_tags_json" "$dist_tag"
}

verify_registry() {
  local dist_tag="$1"
  local deadline=$((SECONDS + REGISTRY_TIMEOUT_SEC))
  local packages=("${LEAF_PACKAGES[@]}" "$ROOT_PACKAGE")
  while (( SECONDS < deadline )); do
    local all_ok="1"
    local pkg
    for pkg in "${packages[@]}"; do
      local actual_version
      if ! actual_version="$(package_dist_tag_version "$pkg" "$dist_tag")"; then
        all_ok="0"
        break
      fi
      if [[ "$actual_version" != "$VERSION" ]]; then
        all_ok="0"
        break
      fi
    done
    if [[ "$all_ok" == "1" ]]; then
      return 0
    fi
    sleep "$POLL_SEC"
  done
  fail "npm dist-tag $dist_tag did not converge to $VERSION before timeout"
}

main() {
  if [[ $# -gt 0 && "$1" == "--" ]]; then
    shift
  fi

  if [[ $# -ne 1 ]]; then
    usage >&2
    exit 1
  fi

  require_commands

  local raw_version="$1"
  validate_version "$raw_version"
  VERSION="${raw_version#v}"
  TAG="v$VERSION"
  CURRENT_HEAD_SHA=""
  TAG_TARGET_SHA=""
  RELEASE_RUN_ID=""
  RELEASE_RUN_URL=""
  RELEASE_RUN_JSON_FILE=""
  RELEASE_ARTIFACT_DIR=""
  CREATE_RELEASE_TAG="0"
  PUSH_RELEASE_TAG="0"
  RUN_RELEASE_VERIFICATION="0"

  local dist_tag="latest"
  if [[ "$VERSION" == *-* ]]; then
    dist_tag="next"
  fi

  ensure_release_branch_state

  if [[ "$RUN_RELEASE_VERIFICATION" == "1" ]]; then
    log "running release verification gate"
    (
      cd "$ROOT_DIR"
      pnpm verify:pre-push
    )
  fi

  if [[ "$CREATE_RELEASE_TAG" == "1" ]]; then
    log "creating release tag $TAG"
    git tag -a "$TAG" -m "Release $TAG"
  fi
  if [[ "$PUSH_RELEASE_TAG" == "1" ]]; then
    log "pushing release tag $TAG"
    git push origin "refs/tags/$TAG"
  fi

  log "waiting for GitHub Actions run"
  find_release_run
  log "watching $RELEASE_RUN_URL"
  wait_for_release_completion

  local run_conclusion publish_conclusion fallback_ready
  run_conclusion="$(node_query 'const fs = require("fs"); const data = JSON.parse(fs.readFileSync(process.argv[1], "utf8")); process.stdout.write(String(data.conclusion || "unknown"));' "$RELEASE_RUN_JSON_FILE")"
  publish_conclusion="$(job_conclusion_from_file "$RELEASE_RUN_JSON_FILE" "publish-npm")"
  fallback_ready="$(manual_fallback_ready_from_file "$RELEASE_RUN_JSON_FILE")"

  if [[ "$run_conclusion" == "success" && "$publish_conclusion" == "success" ]]; then
    log "GitHub publish succeeded, verifying npm dist-tags"
    verify_registry "$dist_tag"
    log "release $TAG is published on npm dist-tag $dist_tag"
    return 0
  fi

  if [[ "$SKIP_MANUAL_FALLBACK" != "1" && "$fallback_ready" == "1" ]]; then
    log "GitHub workflow completed with publish-npm=$publish_conclusion; switching to local artifact publish fallback"
    manual_publish_from_artifacts "$dist_tag"
    verify_registry "$dist_tag"
    log "release $TAG is published on npm dist-tag $dist_tag"
    return 0
  fi

  fail "release workflow did not complete successfully and manual fallback is unavailable: $RELEASE_RUN_URL"
}

main "$@"
