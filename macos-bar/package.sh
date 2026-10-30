#!/usr/bin/env bash
# Packages Agent Watch.app into both a .zip and a compressed .dmg (UDZO)
# with an /Applications symlink for drag-and-drop installation.
#
# Usage:
#   macos-bar/package.sh [APP_PATH] [OUT_DIR]
#
# Defaults:
#   APP_PATH: bin/Agent Watch.app
#   OUT_DIR:  bin
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
APP_PATH="${1:-$REPO_ROOT/bin/Agent Watch.app}"
OUT_DIR="${2:-$REPO_ROOT/bin}"

[ -d "$APP_PATH" ] || { echo "package.sh: app bundle not found: $APP_PATH" >&2; exit 1; }
mkdir -p "$OUT_DIR"

if [ -z "${MENUBAR_VERSION:-}" ]; then
    [ -f "$REPO_ROOT/VERSIONS" ] || { echo "package.sh: $REPO_ROOT/VERSIONS not found" >&2; exit 1; }
    MENUBAR_VERSION="$(sed -n 's/^MENUBAR_VERSION=//p' "$REPO_ROOT/VERSIONS")"
fi
[ -n "$MENUBAR_VERSION" ] || { echo "package.sh: MENUBAR_VERSION is not set in VERSIONS" >&2; exit 1; }

NAME="AgentWatchBar_${MENUBAR_VERSION}_macos_universal"
ZIP_FILE="$OUT_DIR/${NAME}.zip"
DMG_FILE="$OUT_DIR/${NAME}.dmg"

echo "Creating $ZIP_FILE..."
rm -f "$ZIP_FILE"
ditto -c -k --keepParent "$APP_PATH" "$ZIP_FILE"

echo "Creating $DMG_FILE..."
rm -f "$DMG_FILE"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

cp -R "$APP_PATH" "$STAGE/"
ln -s /Applications "$STAGE/Applications"

# UDZO is a read-only zlib-compressed disk image
hdiutil create -volname "Agent Watch" -srcfolder "$STAGE" -ov -format UDZO "$DMG_FILE"

echo "Packaged:"
echo "  $ZIP_FILE"
echo "  $DMG_FILE"
