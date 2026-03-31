#!/usr/bin/env bash

set -euo pipefail

MAX_IMAGE_SIZE_BYTES="${MAX_IMAGE_SIZE_BYTES:-230686720}"
DOCKER_PLATFORM="${DOCKER_PLATFORM:-}"

usage() {
  cat <<'EOF'
Usage: scripts/smoke_runtime_image.sh <image-ref>

Builds or pulls the target image if needed, then verifies the published runtime
contract and enforces the image-size budget.

Environment:
  DOCKER_PLATFORM           Optional platform for docker pull/run, e.g. linux/arm64
  MAX_IMAGE_SIZE_BYTES      Maximum allowed image size in bytes (default: 230686720)
EOF
}

if [[ $# -ne 1 ]]; then
  usage >&2
  exit 1
fi

IMAGE_REF="$1"
docker_platform_args=()

if [[ -n "$DOCKER_PLATFORM" ]]; then
  docker_platform_args=(--platform "$DOCKER_PLATFORM")
fi

fail() {
  printf 'smoke: %s\n' "$*" >&2
  exit 1
}

ensure_image_available() {
  if [[ -n "$DOCKER_PLATFORM" ]]; then
    echo "Pulling $IMAGE_REF for $DOCKER_PLATFORM"
    docker pull "${docker_platform_args[@]}" "$IMAGE_REF" >/dev/null
    return
  fi

  if docker image inspect "$IMAGE_REF" >/dev/null 2>&1; then
    return
  fi

  echo "Pulling $IMAGE_REF"
  docker pull "$IMAGE_REF" >/dev/null
}

run_container() {
  docker run --rm "${docker_platform_args[@]}" "$@"
}

ensure_image_available

size_bytes="$(docker image inspect "$IMAGE_REF" --format '{{.Size}}')"
size_mib=$((size_bytes / 1024 / 1024))
echo "Runtime image size for $IMAGE_REF: ${size_bytes} bytes (${size_mib} MiB)"
if (( size_bytes > MAX_IMAGE_SIZE_BYTES )); then
  fail "image size ${size_bytes} exceeds limit ${MAX_IMAGE_SIZE_BYTES}"
fi

maestro_version="$(run_container "$IMAGE_REF" version)"
[[ -n "$maestro_version" ]] || fail "maestro version output was empty"
echo "$maestro_version"

codex_version="$(run_container --entrypoint codex "$IMAGE_REF" --version)"
[[ -n "$codex_version" ]] || fail "codex version output was empty"
echo "$codex_version"

rg_version="$(run_container --entrypoint rg "$IMAGE_REF" --version)"
[[ -n "$rg_version" ]] || fail "rg version output was empty"
echo "$rg_version"

rg_path="$(run_container --entrypoint sh "$IMAGE_REF" -lc 'command -v rg')"
if [[ "$rg_path" != "/usr/bin/rg" ]]; then
  fail "expected rg to resolve to /usr/bin/rg, found ${rg_path:-<empty>}"
fi
echo "rg path: $rg_path"

if ! run_container --entrypoint sh "$IMAGE_REF" -lc 'test ! -e /usr/local/bin/rg'; then
  fail "expected /usr/local/bin/rg to be absent from the runtime image"
fi

git_version="$(run_container --entrypoint git "$IMAGE_REF" --version)"
[[ -n "$git_version" ]] || fail "git version output was empty"
echo "$git_version"

echo "Runtime image smoke test passed"
