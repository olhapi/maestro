#!/usr/bin/env sh

set -eu

PACKAGE_NAME='@olhapi/maestro'
METADATA_URL="${MAESTRO_INSTALL_METADATA_URL:-https://registry.npmjs.org/@olhapi%2Fmaestro}"
REQUESTED_VERSION="${1:-latest}"
INSTALL_ROOT="${MAESTRO_INSTALL_ROOT:-$HOME/.local/share/maestro}"
INSTALL_BIN_DIR="${MAESTRO_INSTALL_BIN_DIR:-$HOME/.local/bin}"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'install: missing required command: %s\n' "$1" >&2
    exit 1
  fi
}

require_cmd curl
require_cmd node
require_cmd tar

fetch_metadata() {
  curl -fsSL "$METADATA_URL"
}

resolve_release() {
  node -e '
const requested = process.argv[1];
const metadata = JSON.parse(process.argv[2]);
let version = requested;
if (requested === "latest") {
  version = metadata["dist-tags"]?.latest;
}
if (!version || !metadata.versions?.[version]) {
  process.stderr.write(`install: unable to resolve ${requested}\n`);
  process.exit(1);
}
const tarball = metadata.versions[version].dist?.tarball;
if (!tarball) {
  process.stderr.write(`install: missing tarball url for ${version}\n`);
  process.exit(1);
}
process.stdout.write(JSON.stringify({ version, tarball }));
  ' "$REQUESTED_VERSION" "$1"
}

METADATA="$(fetch_metadata)"
RESOLVED="$(resolve_release "$METADATA")"
VERSION="$(printf '%s' "$RESOLVED" | node -pe 'JSON.parse(require("fs").readFileSync(0, "utf8")).version')"
TARBALL_URL="$(printf '%s' "$RESOLVED" | node -pe 'JSON.parse(require("fs").readFileSync(0, "utf8")).tarball')"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/maestro-install.XXXXXX")"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

ARCHIVE_PATH="$TMP_DIR/maestro.tgz"
PACKAGE_DIR="$TMP_DIR/package"
VERSION_DIR="$INSTALL_ROOT/versions/$VERSION"
CURRENT_LINK="$INSTALL_ROOT/current"
TARGET_BIN="$CURRENT_LINK/bin/maestro"
BIN_LINK="$INSTALL_BIN_DIR/maestro"

printf 'install: downloading %s %s\n' "$PACKAGE_NAME" "$VERSION"
curl -fsSL "$TARBALL_URL" -o "$ARCHIVE_PATH"
mkdir -p "$PACKAGE_DIR"
tar -xzf "$ARCHIVE_PATH" -C "$TMP_DIR"

mkdir -p "$INSTALL_ROOT/versions" "$INSTALL_BIN_DIR"
rm -rf "$VERSION_DIR"
mv "$PACKAGE_DIR" "$VERSION_DIR"
ln -sfn "$VERSION_DIR" "$CURRENT_LINK"
chmod 755 "$TARGET_BIN"
ln -sfn "$TARGET_BIN" "$BIN_LINK"

printf 'install: installed maestro launcher %s\n' "$VERSION"
printf 'install: runtime image defaults to ghcr.io/olhapi/maestro:%s\n' "$VERSION"
printf 'install: run `maestro self-update` to pin the latest runtime image\n'

case ":$PATH:" in
  *":$INSTALL_BIN_DIR:"*) ;;
  *)
    printf 'install: add %s to your PATH if `maestro` is not found\n' "$INSTALL_BIN_DIR"
    ;;
esac
