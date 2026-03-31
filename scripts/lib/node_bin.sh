#!/bin/sh

ensure_maestro_node_bin() {
  if [ -n "${MAESTRO_NODE_BIN:-}" ]; then
    export MAESTRO_NODE_BIN
    return 0
  fi

  if ! command -v npx >/dev/null 2>&1; then
    printf 'missing required command: npx (or set MAESTRO_NODE_BIN)\n' >&2
    exit 1
  fi

  _maestro_node_tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/maestro-node24.XXXXXX")"
  if _maestro_node_bin="$(cd "$_maestro_node_tmp_dir" && npx --yes node@24 -p 'process.execPath')"; then
    rm -rf "$_maestro_node_tmp_dir"
    MAESTRO_NODE_BIN="$_maestro_node_bin"
    export MAESTRO_NODE_BIN
    return 0
  fi

  _maestro_node_status=$?
  rm -rf "$_maestro_node_tmp_dir"
  printf 'failed to resolve Node 24 binary; set MAESTRO_NODE_BIN explicitly\n' >&2
  return "$_maestro_node_status"
}
