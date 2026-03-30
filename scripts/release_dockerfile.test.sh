#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCKERFILE="$ROOT_DIR/Dockerfile"

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

main() {
  assert_contains "$DOCKERFILE" "COPY cmd ./cmd"
  assert_contains "$DOCKERFILE" "COPY internal ./internal"
  assert_contains "$DOCKERFILE" "COPY pkg ./pkg"
  assert_contains "$DOCKERFILE" "COPY skills ./skills"
  assert_contains "$DOCKERFILE" "apt-get install -y --no-install-recommends ca-certificates git nodejs npm"
  assert_contains "$DOCKERFILE" "npm install -g \"@openai/codex@\${CODEX_VERSION}\""
}

main "$@"
