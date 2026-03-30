#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODULE_PATH="$(awk '/^module / {print $2; exit}' "$ROOT_DIR/go.mod")"

"$ROOT_DIR/scripts/ensure_dashboard_dist.sh"

declare -A package_dirs=()
while IFS= read -r file; do
  dir="$(dirname "$file")"
  rel="${dir#"$ROOT_DIR"/}"
  case "$rel" in
    cmd/maestro-fake-appserver|\
    internal/testutil/*|\
    internal/agentruntime/contracttest|\
    internal/agentruntime/fake|\
    internal/agentruntime/testadapter|\
    internal/appserver/protocol/gen)
      continue
      ;;
  esac
  package_dirs["$rel"]=1
done < <(
  find "$ROOT_DIR/cmd" "$ROOT_DIR/internal" "$ROOT_DIR/pkg" "$ROOT_DIR/skills" \
    -type f -name '*.go' ! -name '*_test.go' | sort -u
)

packages=()
for rel in "${!package_dirs[@]}"; do
  packages+=("$rel")
done
IFS=$'\n' packages=($(printf '%s\n' "${packages[@]}" | sort))
unset IFS

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

printf '%-28s %10s %10s\n' "PACKAGE" "COVERAGE" "THRESHOLD"

status=0
threshold="90"
for rel in "${packages[@]}"; do
  pkg="$MODULE_PATH/$rel"
  profile="$tmpdir/$(echo "$rel" | tr '/.' '_').cover"

  go test -coverprofile="$profile" "./$rel" >/dev/null
  coverage="$(go tool cover -func="$profile" | awk '/^total:/ {gsub("%","",$3); print $3}')"
  printf '%-28s %9s%% %9s%%\n' "./$rel" "$coverage" "$threshold"

  if ! awk -v actual="$coverage" -v threshold="$threshold" 'BEGIN { exit !(actual + 0 >= threshold + 0) }'; then
    status=1
  fi
done

exit "$status"
