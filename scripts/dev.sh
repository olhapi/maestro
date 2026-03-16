#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_PATH="${REPO_PATH:-}"
BACKEND_PORT="${BACKEND_PORT:-8787}"
FRONTEND_HOST="${FRONTEND_HOST:-127.0.0.1}"
FRONTEND_PORT="${FRONTEND_PORT:-4173}"
FRONTEND_PROXY_HOST="${FRONTEND_PROXY_HOST:-$FRONTEND_HOST}"
BACKEND_HEALTH_URL="${BACKEND_HEALTH_URL:-http://127.0.0.1:${BACKEND_PORT}/health}"
STARTUP_TIMEOUT_SEC="${STARTUP_TIMEOUT_SEC:-20}"

backend_pid=""
frontend_pid=""
managed_backend_pid=""
managed_frontend_pid=""
requested_exit_code=""

stop_process_group() {
  local pid="${1:-}"
  local elapsed=0

  if [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null; then
    return 0
  fi

  kill -TERM -- "-$pid" 2>/dev/null || kill "$pid" 2>/dev/null || true

  while kill -0 "$pid" 2>/dev/null; do
    if (( elapsed >= 5 )); then
      kill -KILL -- "-$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
      break
    fi

    sleep 1
    elapsed=$((elapsed + 1))
  done

  wait "$pid" 2>/dev/null || true
}

cleanup() {
  local exit_code=$?
  trap - EXIT INT TERM

  stop_process_group "$managed_frontend_pid"
  stop_process_group "$managed_backend_pid"

  if [[ -n "$requested_exit_code" ]]; then
    exit "$requested_exit_code"
  fi

  exit "$exit_code"
}

handle_signal() {
  requested_exit_code="$1"
  exit "$requested_exit_code"
}

trap cleanup EXIT
trap 'handle_signal 0' INT
trap 'handle_signal 143' TERM

if [[ -n "$REPO_PATH" ]]; then
  if [[ ! -d "$REPO_PATH" ]]; then
    echo "Repo path does not exist: $REPO_PATH" >&2
    exit 1
  fi
  REPO_PATH="$(cd "$REPO_PATH" && pwd)"
fi

case "$FRONTEND_PROXY_HOST" in
  0.0.0.0|::|[::])
    FRONTEND_PROXY_HOST="127.0.0.1"
    ;;
esac

DB_PATH="${DB_PATH:-$ROOT_DIR/.maestro/maestro.db}"
FRONTEND_DEV_PROXY_URL="http://${FRONTEND_PROXY_HOST}:${FRONTEND_PORT}"

wait_for_backend() {
  local elapsed=0

  until curl --silent --fail --output /dev/null "$BACKEND_HEALTH_URL"; do
    if [[ -n "$backend_pid" ]] && ! kill -0 "$backend_pid" 2>/dev/null; then
      echo "Backend exited before becoming ready." >&2
      return 1
    fi

    if (( elapsed >= STARTUP_TIMEOUT_SEC )); then
      echo "Timed out waiting for backend readiness at $BACKEND_HEALTH_URL." >&2
      return 1
    fi

    sleep 1
    elapsed=$((elapsed + 1))
  done
}

start_process_group() {
  local output_var="$1"
  local workdir="$2"
  shift 2

  (
    cd "$workdir"
    exec /usr/bin/python3 - "$@" <<'PY'
import os
import sys

os.setsid()
os.execvp(sys.argv[1], sys.argv[1:])
PY
  ) &

  printf -v "$output_var" '%s' "$!"
}

listener_pid() {
  local port="$1"

  lsof -tiTCP:"${port}" -sTCP:LISTEN 2>/dev/null | head -n 1
}

listener_command() {
  local pid="$1"

  ps -p "$pid" -o command= 2>/dev/null | sed 's/^[[:space:]]*//'
}

listener_command_with_env() {
  local pid="$1"

  ps eww -p "$pid" -o command= 2>/dev/null | sed 's/^[[:space:]]*//'
}

listener_cwd() {
  local pid="$1"

  lsof -a -p "$pid" -d cwd -Fn 2>/dev/null | sed -n 's/^n//p' | head -n 1
}

describe_listener() {
  local port="$1"
  local label="$2"
  local pid
  local cmd
  local cwd

  pid="$(listener_pid "$port" || true)"
  if [[ -z "$pid" ]]; then
    return 0
  fi

  cmd="$(listener_command "$pid" || true)"
  cwd="$(listener_cwd "$pid" || true)"

  echo "${label} port ${port} is currently in use by PID ${pid}." >&2
  if [[ -n "$cwd" ]]; then
    echo "  cwd: ${cwd}" >&2
  fi
  if [[ -n "$cmd" ]]; then
    echo "  cmd: ${cmd}" >&2
  fi
}

is_reusable_backend_listener() {
  local pid="$1"
  local cmdline
  local cwd

  cmdline="$(listener_command_with_env "$pid" || true)"
  cwd="$(listener_cwd "$pid" || true)"

  if [[ -z "$cmdline" ]] || [[ -z "$cwd" ]]; then
    return 1
  fi

  if [[ "$cwd" != "$ROOT_DIR" ]]; then
    return 1
  fi

  if [[ "$cmdline" != *" run "* ]]; then
    return 1
  fi

  if ! printf '%s\n' "$cmdline" | grep -F -q -- " --db $DB_PATH"; then
    return 1
  fi

  if ! printf '%s\n' "$cmdline" | grep -F -q -- " --port $BACKEND_PORT"; then
    return 1
  fi

  if ! printf '%s\n' "$cmdline" | grep -F -q -- " MAESTRO_UI_DEV_PROXY_URL=$FRONTEND_DEV_PROXY_URL"; then
    return 1
  fi

  if [[ -n "$REPO_PATH" ]] && ! printf '%s\n' "$cmdline" | grep -F -q -- " $REPO_PATH"; then
    return 1
  fi

  return 0
}

wait_for_port_release() {
  local port="$1"
  local label="$2"
  local elapsed=0

  while listener_pid "$port" >/dev/null; do
    if (( elapsed >= STARTUP_TIMEOUT_SEC )); then
      echo "Timed out waiting for ${label} port ${port} to become available." >&2
      describe_listener "$port" "$label"
      return 1
    fi

    sleep 1
    elapsed=$((elapsed + 1))
  done
}

wait_for_port_release "$FRONTEND_PORT" "frontend"

existing_backend_pid="$(listener_pid "$BACKEND_PORT" || true)"
if [[ -n "$existing_backend_pid" ]] && is_reusable_backend_listener "$existing_backend_pid"; then
  backend_pid="$existing_backend_pid"
  echo "Reusing existing backend on http://127.0.0.1:${BACKEND_PORT} (PID ${backend_pid})."
else
  wait_for_port_release "$BACKEND_PORT" "backend"

  backend_cmd=(go run ./cmd/maestro run --db "$DB_PATH" --port "$BACKEND_PORT")
  if [[ -n "$REPO_PATH" ]]; then
    backend_cmd+=( "$REPO_PATH" )
  fi
  start_process_group backend_pid "$ROOT_DIR" env MAESTRO_UI_DEV_PROXY_URL="$FRONTEND_DEV_PROXY_URL" "${backend_cmd[@]}"
  managed_backend_pid="$backend_pid"
fi

wait_for_backend

start_process_group frontend_pid "$ROOT_DIR/apps/frontend" pnpm exec vite --host "$FRONTEND_HOST" --port "$FRONTEND_PORT" --strictPort
managed_frontend_pid="$frontend_pid"

if [[ -n "$REPO_PATH" ]]; then
  echo "Repo:     ${REPO_PATH}"
else
  echo "Repo:     all shared projects"
fi
echo "DB:       ${DB_PATH}"
echo "Dashboard: http://127.0.0.1:${BACKEND_PORT} (API + HMR proxy)"
echo "Vite:      http://${FRONTEND_HOST}:${FRONTEND_PORT} (direct)"
echo "Press Ctrl+C to stop both."

wait_targets=()
if [[ -n "$frontend_pid" ]]; then
  wait_targets+=( "$frontend_pid" )
fi

wait "${wait_targets[@]}"
