#!/usr/bin/env bash
# Builds the Agent Watch menu bar app: a universal (arm64 + x86_64) bundle for
# macOS 13+, ad-hoc signed so its Info.plist is bound to the executable.
#
#   macos-bar/build.sh                      # -> bin/AgentWatchBar.app
#   APP_DIR=/tmp/AgentWatchBar.app macos-bar/build.sh
#   VERSION=0.2.1 macos-bar/build.sh        # stamps the bundle version
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
APP_DIR="${APP_DIR:-$REPO_ROOT/bin/AgentWatchBar.app}"
VERSION="${VERSION:-0.2.0}"
MIN_MACOS="13.0"
ARCHS=(arm64 x86_64)
SOURCES=("$SCRIPT_DIR/BarLogic.swift" "$SCRIPT_DIR/main.swift")

echo "Building AgentWatchBar $VERSION (macOS $MIN_MACOS+, ${ARCHS[*]})..."
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

slices=()
for arch in "${ARCHS[@]}"; do
    swiftc -O -target "$arch-apple-macos$MIN_MACOS" -framework AppKit \
        -o "$WORK/AgentWatchBar-$arch" "${SOURCES[@]}"
    slices+=("$WORK/AgentWatchBar-$arch")
done

rm -rf "$APP_DIR"
mkdir -p "$APP_DIR/Contents/MacOS" "$APP_DIR/Contents/Resources"
lipo -create "${slices[@]}" -output "$APP_DIR/Contents/MacOS/AgentWatchBar"

cp "$SCRIPT_DIR/Info.plist" "$APP_DIR/Contents/Info.plist"
plutil -replace CFBundleShortVersionString -string "$VERSION" "$APP_DIR/Contents/Info.plist"
plutil -replace CFBundleVersion -string "$VERSION" "$APP_DIR/Contents/Info.plist"
plutil -replace LSMinimumSystemVersion -string "$MIN_MACOS" "$APP_DIR/Contents/Info.plist"

codesign --force --sign - --timestamp=none "$APP_DIR"

echo "Created $APP_DIR"
