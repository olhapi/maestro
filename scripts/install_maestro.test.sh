#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT_UNDER_TEST="$ROOT_DIR/scripts/install_maestro.sh"

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

create_fixture_tarball() {
  local tmp_dir="$1"
  local version="$2"
  local archive_path="$3"
  local fixture_root="$tmp_dir/package"

  mkdir -p "$fixture_root/bin"
  cp "$ROOT_DIR/packaging/npm/root/bin/maestro" "$fixture_root/bin/maestro"
  cat >"$fixture_root/bin/maestro.js" <<EOF
#!/usr/bin/env node
process.stdout.write('fixture maestro ${version}\n');
EOF
  chmod +x "$fixture_root/bin/maestro"
  chmod +x "$fixture_root/bin/maestro.js"
  tar -czf "$archive_path" -C "$tmp_dir" package
}

write_mock_curl() {
  local bin_dir="$1"
  mkdir -p "$bin_dir"

  cat >"$bin_dir/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

output=""
url=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o)
      output="$2"
      shift 2
      ;;
    -fsSL)
      shift
      ;;
    *)
      url="$1"
      shift
      ;;
  esac
done

if [[ "$url" == "$MOCK_METADATA_URL" ]]; then
  if [[ -n "$output" ]]; then
    cp "$MOCK_METADATA_FILE" "$output"
  else
    cat "$MOCK_METADATA_FILE"
  fi
  exit 0
fi

while IFS='|' read -r candidate_url candidate_path; do
  if [[ "$url" == "$candidate_url" ]]; then
    if [[ -z "$output" ]]; then
      cat "$candidate_path"
    else
      cp "$candidate_path" "$output"
    fi
    exit 0
  fi
done <"$MOCK_TARBALL_MAP_FILE"

printf 'unexpected curl url: %s\n' "$url" >&2
exit 1
EOF
  chmod +x "$bin_dir/curl"
}

run_install_case() {
  local requested_version="$1"
  local latest_version="$2"
  local expected_version="$3"

  local tmp_dir bin_dir install_root install_bin_dir output metadata_file tarball_map
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/maestro-install-test.XXXXXX")"
  bin_dir="$tmp_dir/bin"
  install_root="$tmp_dir/install-root"
  install_bin_dir="$tmp_dir/install-bin"
  output="$tmp_dir/output.txt"
  metadata_file="$tmp_dir/metadata.json"
  tarball_map="$tmp_dir/tarballs.map"

  write_mock_curl "$bin_dir"

  local version tarball_url tarball_path
  : >"$tarball_map"
  for version in 1.2.3 1.2.4; do
    tarball_url="https://packages.example.test/olhapi-maestro-${version}.tgz"
    tarball_path="$tmp_dir/olhapi-maestro-${version}.tgz"
    create_fixture_tarball "$tmp_dir/${version}" "$version" "$tarball_path"
    printf '%s|%s\n' "$tarball_url" "$tarball_path" >>"$tarball_map"
  done

  cat >"$metadata_file" <<EOF
{
  "dist-tags": {
    "latest": "${latest_version}"
  },
  "versions": {
    "1.2.3": {
      "dist": {
        "tarball": "https://packages.example.test/olhapi-maestro-1.2.3.tgz"
      }
    },
    "1.2.4": {
      "dist": {
        "tarball": "https://packages.example.test/olhapi-maestro-1.2.4.tgz"
      }
    }
  }
}
EOF

  PATH="$bin_dir:$PATH" \
  MOCK_METADATA_URL="https://registry.example.test/@olhapi%2Fmaestro" \
  MOCK_METADATA_FILE="$metadata_file" \
  MOCK_TARBALL_MAP_FILE="$tarball_map" \
  MAESTRO_INSTALL_METADATA_URL="https://registry.example.test/@olhapi%2Fmaestro" \
  MAESTRO_INSTALL_ROOT="$install_root" \
  MAESTRO_INSTALL_BIN_DIR="$install_bin_dir" \
  "$SCRIPT_UNDER_TEST" "$requested_version" >"$output"

  assert_contains "$output" "install: installed maestro launcher ${expected_version}"
  assert_contains "$output" "install: runtime image defaults to ghcr.io/olhapi/maestro:${expected_version}"

  [[ -x "$install_bin_dir/maestro" ]] || fail "expected installed launcher symlink"
  if [[ "$("$install_bin_dir/maestro")" != "fixture maestro ${expected_version}" ]]; then
    fail "expected installed launcher to point at ${expected_version}"
  fi
  [[ -L "$install_root/current" ]] || fail "expected current release symlink"
}

test_install_explicit_version() {
  run_install_case "1.2.3" "1.2.4" "1.2.3"
}

test_install_latest_version() {
  run_install_case "latest" "1.2.4" "1.2.4"
}

test_install_explicit_version
test_install_latest_version
