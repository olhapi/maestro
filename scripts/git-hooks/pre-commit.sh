#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=/dev/null
. "$SCRIPT_DIR/shared.sh"

ROOT=$(repo_root)
cd "$ROOT"

STAGED=$(git diff --cached --name-only --diff-filter=ACMR)
if [ -z "$STAGED" ]; then
  log_step "no staged files; skipping pre-commit checks"
  exit 0
fi

needs_make_test=0
needs_frontend=0
go_packages=""

old_ifs=$IFS
IFS='
'
for path in $STAGED; do
  case "$path" in
    frontend/*)
      needs_frontend=1
      ;;
  esac

  case "$path" in
    go.mod|go.sum|Makefile|scripts/check_coverage.sh)
      needs_make_test=1
      ;;
    cmd/*|internal/*|pkg/*)
      case "$path" in
        *.go)
          pkg_dir="./$(dirname "$path")"
          case " $go_packages " in
            *" $pkg_dir "*) ;;
            *) go_packages="$go_packages $pkg_dir" ;;
          esac
          ;;
      esac
      ;;
  esac
done
IFS=$old_ifs

if [ "$needs_make_test" -eq 1 ]; then
  run_step make test
elif [ -n "$go_packages" ]; then
  run_step go test $go_packages
fi

if [ "$needs_frontend" -eq 1 ]; then
  run_step npm --prefix frontend run lint
  run_step npm --prefix frontend run test:ci
fi
