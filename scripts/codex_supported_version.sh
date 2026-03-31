#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
METADATA_FILE="$ROOT/internal/codexschema/metadata.go"
SUPPORTED_VERSION="$(sed -n 's/^[[:space:]]*SupportedVersion = "\(.*\)"/\1/p' "$METADATA_FILE")"

if [[ -z "$SUPPORTED_VERSION" ]]; then
  echo "failed to determine supported Codex version from $METADATA_FILE" >&2
  exit 1
fi

printf '%s\n' "$SUPPORTED_VERSION"
