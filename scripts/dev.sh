#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BACKEND_PORT="${BACKEND_PORT:-8787}"
FRONTEND_HOST="${FRONTEND_HOST:-127.0.0.1}"
FRONTEND_PORT="${FRONTEND_PORT:-4173}"
DB_PATH="${DB_PATH:-$ROOT_DIR/.symphony/symphony.db}"
BACKEND_HEALTH_URL="${BACKEND_HEALTH_URL:-http://127.0.0.1:${BACKEND_PORT}/health}"
STARTUP_TIMEOUT_SEC="${STARTUP_TIMEOUT_SEC:-20}"

backend_pid=""
frontend_pid=""

cleanup() {
  local exit_code=$?

  if [[ -n "$frontend_pid" ]] && kill -0 "$frontend_pid" 2>/dev/null; then
    kill "$frontend_pid" 2>/dev/null || true
    wait "$frontend_pid" 2>/dev/null || true
  fi

  if [[ -n "$backend_pid" ]] && kill -0 "$backend_pid" 2>/dev/null; then
    kill "$backend_pid" 2>/dev/null || true
    wait "$backend_pid" 2>/dev/null || true
  fi

  exit "$exit_code"
}

trap cleanup EXIT INT TERM

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

wait_for_port_release() {
  local port="$1"
  local label="$2"
  local elapsed=0

  while lsof -tiTCP:"${port}" -sTCP:LISTEN >/dev/null 2>&1; do
    if (( elapsed >= STARTUP_TIMEOUT_SEC )); then
      echo "Timed out waiting for ${label} port ${port} to become available." >&2
      return 1
    fi

    sleep 1
    elapsed=$((elapsed + 1))
  done
}

wait_for_port_release "$BACKEND_PORT" "backend"
wait_for_port_release "$FRONTEND_PORT" "frontend"

cd "$ROOT_DIR"
go run ./cmd/symphony run . --db "$DB_PATH" --port "$BACKEND_PORT" &
backend_pid=$!

wait_for_backend

cd "$ROOT_DIR/frontend"
npm run dev -- --host "$FRONTEND_HOST" --port "$FRONTEND_PORT" --strictPort &
frontend_pid=$!

echo "Backend:  http://127.0.0.1:${BACKEND_PORT}"
echo "Frontend: http://${FRONTEND_HOST}:${FRONTEND_PORT}"
echo "Press Ctrl+C to stop both."

wait "$backend_pid" "$frontend_pid"
