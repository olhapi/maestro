#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

"$ROOT_DIR/scripts/ensure_dashboard_dist.sh"

packages=(
  "./cmd/maestro:80"
  "./internal/appserver:80"
  "./internal/dashboardapi:80"
  "./internal/httpserver:80"
  "./internal/kanban:80"
  "./internal/mcp:75"
  "./internal/orchestrator:80"
)

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

printf '%-28s %10s %10s\n' "PACKAGE" "COVERAGE" "THRESHOLD"

status=0
for entry in "${packages[@]}"; do
  pkg="${entry%%:*}"
  threshold="${entry##*:}"
  profile="$tmpdir/$(echo "$pkg" | tr '/.' '_').cover"

  go test -coverprofile="$profile" "$pkg" >/dev/null
  coverage="$(go tool cover -func="$profile" | awk '/^total:/ {gsub("%","",$3); print $3}')"
  printf '%-28s %9s%% %9s%%\n' "$pkg" "$coverage" "$threshold"

  if ! awk -v actual="$coverage" -v threshold="$threshold" 'BEGIN { exit !(actual + 0 >= threshold + 0) }'; then
    status=1
  fi
done

exit "$status"
