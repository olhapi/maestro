#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCKERFILE="$ROOT_DIR/Dockerfile"
SUPPORTED_CODEX_VERSION="$("$ROOT_DIR/scripts/codex_supported_version.sh")"
EXPECTED_ALPINE_VERSION="3.20"
EXPECTED_ALPINE_DIGEST="sha256:a4f4213abb84c497377b8544c81b3564f313746700372ec4fe84653e4fb03805"

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

main() {
  local dockerfile_codex_version
  local dockerfile_alpine_version
  local dockerfile_alpine_digest

  dockerfile_codex_version="$(sed -n 's/^ARG CODEX_VERSION=\(.*\)$/\1/p' "$DOCKERFILE")"
  if [[ "$dockerfile_codex_version" != "$SUPPORTED_CODEX_VERSION" ]]; then
    fail "expected Dockerfile CODEX_VERSION=$SUPPORTED_CODEX_VERSION, found ${dockerfile_codex_version:-<empty>}"
  fi

  dockerfile_alpine_version="$(sed -n 's/^ARG ALPINE_VERSION=\(.*\)$/\1/p' "$DOCKERFILE")"
  if [[ "$dockerfile_alpine_version" != "$EXPECTED_ALPINE_VERSION" ]]; then
    fail "expected Dockerfile ALPINE_VERSION=$EXPECTED_ALPINE_VERSION, found ${dockerfile_alpine_version:-<empty>}"
  fi

  dockerfile_alpine_digest="$(sed -n 's/^ARG ALPINE_DIGEST=\(.*\)$/\1/p' "$DOCKERFILE")"
  if [[ "$dockerfile_alpine_digest" != "$EXPECTED_ALPINE_DIGEST" ]]; then
    fail "expected Dockerfile ALPINE_DIGEST=$EXPECTED_ALPINE_DIGEST, found ${dockerfile_alpine_digest:-<empty>}"
  fi

  assert_contains "$DOCKERFILE" "AS maestro-build"
  assert_contains "$DOCKERFILE" "AS codex-fetch"
  assert_contains "$DOCKERFILE" "COPY cmd ./cmd"
  assert_contains "$DOCKERFILE" "COPY internal ./internal"
  assert_contains "$DOCKERFILE" "COPY pkg ./pkg"
  assert_contains "$DOCKERFILE" "COPY skills ./skills"
  assert_contains "$DOCKERFILE" "npm pack \"@openai/codex@\${CODEX_VERSION}-linux-\${openai_arch}\""
  assert_contains "$DOCKERFILE" "FROM alpine:\${ALPINE_VERSION}@\${ALPINE_DIGEST}"
  assert_contains "$DOCKERFILE" "apk add --no-cache ca-certificates git ripgrep"
  assert_contains "$DOCKERFILE" "COPY --from=codex-fetch /out/codex /usr/local/bin/codex"
  assert_not_contains "$DOCKERFILE" "package/vendor/\${codex_triple}/path/rg"
  assert_not_contains "$DOCKERFILE" "COPY --from=codex-fetch /out/rg /usr/local/bin/rg"
  assert_not_contains "$DOCKERFILE" "apt-get update"
  assert_not_contains "$DOCKERFILE" "apt-get install -y --no-install-recommends ca-certificates git"
  assert_not_contains "$DOCKERFILE" "npm install -g \"@openai/codex@\${CODEX_VERSION}\""
}

main "$@"
