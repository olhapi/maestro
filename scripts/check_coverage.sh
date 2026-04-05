#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

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

# Set COVERAGE_VERBOSE=1 to print the full per-package table.
verbose="${COVERAGE_VERBOSE:-0}"
threshold="90"
if [[ "$verbose" == "1" ]]; then
  printf '%-28s %10s %10s\n' "PACKAGE" "COVERAGE" "THRESHOLD"
else
  printf 'Checking coverage for %d packages at %s%% threshold\n' "${#packages[@]}" "$threshold"
fi

status=0
failed_packages=()
for rel in "${packages[@]}"; do
  profile="$tmpdir/$(echo "$rel" | tr '/.' '_').cover"

  go test -coverprofile="$profile" "./$rel" >/dev/null
  coverage="$(go tool cover -func="$profile" | awk '/^total:/ {gsub("%","",$3); print $3}')"

  if ! awk -v actual="$coverage" -v threshold="$threshold" 'BEGIN { exit !(actual + 0 >= threshold + 0) }'; then
    status=1
    if [[ "$verbose" == "1" ]]; then
      printf '%-28s %9s%% %9s%%\n' "./$rel" "$coverage" "$threshold"
    else
      failed_packages+=("./$rel ${coverage}% < ${threshold}%")
    fi
  elif [[ "$verbose" == "1" ]]; then
    printf '%-28s %9s%% %9s%%\n' "./$rel" "$coverage" "$threshold"
  fi
done

if [[ "$verbose" != "1" ]]; then
  if [[ ${#failed_packages[@]} -eq 0 ]]; then
    printf 'Coverage check passed: %d packages at or above %s%%\n' "${#packages[@]}" "$threshold"
  else
    printf 'Coverage check failed: %d of %d packages below %s%%\n' "${#failed_packages[@]}" "${#packages[@]}" "$threshold"
    for line in "${failed_packages[@]}"; do
      printf '  %s\n' "$line"
    done
  fi
fi

exit "$status"
