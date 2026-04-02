#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
METADATA_FILE="$ROOT/internal/codexschema/metadata.go"
DEFAULT_VERSION="$(sed -n 's/^[[:space:]]*SupportedVersion = "\(.*\)"/\1/p' "$METADATA_FILE")"
if [[ -z "$DEFAULT_VERSION" ]]; then
  echo "failed to determine supported Codex schema version from $METADATA_FILE" >&2
  exit 1
fi
DEFAULT_QUICKTYPE_VERSION="$(sed -n 's/^[[:space:]]*QuicktypeVersion = "\(.*\)"/\1/p' "$METADATA_FILE")"
if [[ -z "$DEFAULT_QUICKTYPE_VERSION" ]]; then
  echo "failed to determine quicktype version from $METADATA_FILE" >&2
  exit 1
fi
VERSION="${CODEX_SCHEMA_VERSION:-$DEFAULT_VERSION}"
QUICKTYPE_VERSION="${QUICKTYPE_VERSION:-$DEFAULT_QUICKTYPE_VERSION}"
SCHEMA_DIR="$ROOT/schemas/codex/$VERSION/json"
GEN_DIR="$ROOT/internal/appserver/protocol/gen"
GEN_FILE="$GEN_DIR/models.go"
CODEX_PACKAGE="@openai/codex@$VERSION"

actual_version="$(npx --yes "$CODEX_PACKAGE" --version | awk '{print $2}')"
if [[ "$actual_version" != "$VERSION" ]]; then
  echo "warning: generating schemas for codex-cli $actual_version while CODEX_SCHEMA_VERSION=$VERSION" >&2
fi

rm -rf "$SCHEMA_DIR"
mkdir -p "$SCHEMA_DIR" "$GEN_DIR"

npx --yes "$CODEX_PACKAGE" app-server generate-json-schema --out "$SCHEMA_DIR"

schema_files=(
  "$SCHEMA_DIR/v1/InitializeParams.json"
  "$SCHEMA_DIR/v1/InitializeResponse.json"
  "$SCHEMA_DIR/v2/ThreadStartParams.json"
  "$SCHEMA_DIR/v2/ThreadStartResponse.json"
  "$SCHEMA_DIR/v2/TurnStartParams.json"
  "$SCHEMA_DIR/v2/TurnStartResponse.json"
  "$SCHEMA_DIR/v2/ThreadStartedNotification.json"
  "$SCHEMA_DIR/v2/TurnStartedNotification.json"
  "$SCHEMA_DIR/v2/TurnCompletedNotification.json"
  "$SCHEMA_DIR/ExecCommandApprovalParams.json"
  "$SCHEMA_DIR/ExecCommandApprovalResponse.json"
  "$SCHEMA_DIR/ApplyPatchApprovalParams.json"
  "$SCHEMA_DIR/ApplyPatchApprovalResponse.json"
  "$SCHEMA_DIR/CommandExecutionRequestApprovalParams.json"
  "$SCHEMA_DIR/CommandExecutionRequestApprovalResponse.json"
  "$SCHEMA_DIR/FileChangeRequestApprovalParams.json"
  "$SCHEMA_DIR/FileChangeRequestApprovalResponse.json"
  "$SCHEMA_DIR/ToolRequestUserInputParams.json"
  "$SCHEMA_DIR/ToolRequestUserInputResponse.json"
  "$SCHEMA_DIR/McpServerElicitationRequestParams.json"
  "$SCHEMA_DIR/McpServerElicitationRequestResponse.json"
  "$SCHEMA_DIR/DynamicToolCallParams.json"
  "$SCHEMA_DIR/DynamicToolCallResponse.json"
)

npx --yes "quicktype@$QUICKTYPE_VERSION" \
  --lang go \
  --src-lang schema \
  --just-types-and-package \
  --package gen \
  --out "$GEN_FILE" \
  "${schema_files[@]}"

gofmt -w "$GEN_FILE"
