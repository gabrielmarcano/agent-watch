#!/usr/bin/env bash
# Builds the Agent Watch menu bar app: a universal (arm64 + x86_64) bundle for
# macOS 13+, ad-hoc signed so its Info.plist is bound to the executable.
#
#   macos-bar/build.sh                           # -> "bin/Agent Watch.app"
#   APP_DIR=/tmp/AgentWatch.app macos-bar/build.sh
#   MENUBAR_VERSION=0.0.0 macos-bar/build.sh     # overrides the bundle version
#   EMBED_BRIDGE=1 macos-bar/build.sh            # embeds universal agent-watch-bridge helper
#
# The bundle version (CFBundleShortVersionString and CFBundleVersion) is
# MENUBAR_VERSION from VERSIONS at the repo root, written into the built
# Info.plist before signing; the committed Info.plist holds a placeholder.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
APP_DIR="${APP_DIR:-$REPO_ROOT/bin/Agent Watch.app}"
if [ -z "${MENUBAR_VERSION:-}" ]; then
    [ -f "$REPO_ROOT/VERSIONS" ] || { echo "build.sh: $REPO_ROOT/VERSIONS not found" >&2; exit 1; }
    MENUBAR_VERSION="$(sed -n 's/^MENUBAR_VERSION=//p' "$REPO_ROOT/VERSIONS")"
fi
[ -n "$MENUBAR_VERSION" ] || { echo "build.sh: MENUBAR_VERSION is not set in VERSIONS" >&2; exit 1; }
MIN_MACOS="13.0"
ARCHS=(arm64 x86_64)
SOURCES=("$SCRIPT_DIR/BarLogic.swift" "$SCRIPT_DIR/HeaderView.swift" "$SCRIPT_DIR/main.swift")

echo "Building AgentWatchBar $MENUBAR_VERSION (macOS $MIN_MACOS+, ${ARCHS[*]})..."
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

slices=()
for arch in "${ARCHS[@]}"; do
    swiftc -O -target "$arch-apple-macos$MIN_MACOS" -framework AppKit -framework ServiceManagement \
        -o "$WORK/AgentWatchBar-$arch" "${SOURCES[@]}"
    slices+=("$WORK/AgentWatchBar-$arch")
done

rm -rf "$APP_DIR"
mkdir -p "$APP_DIR/Contents/MacOS" "$APP_DIR/Contents/Resources"
lipo -create "${slices[@]}" -output "$APP_DIR/Contents/MacOS/Agent Watch"

# The icon is drawn from the Wear OS launcher icon (make-icon.swift).
swift "$SCRIPT_DIR/make-icon.swift" "$REPO_ROOT/android/app/src/main/res/drawable" "$WORK/AppIcon.iconset"
iconutil -c icns -o "$APP_DIR/Contents/Resources/AppIcon.icns" "$WORK/AppIcon.iconset"

cp "$SCRIPT_DIR/Info.plist" "$APP_DIR/Contents/Info.plist"
plutil -replace CFBundleShortVersionString -string "$MENUBAR_VERSION" "$APP_DIR/Contents/Info.plist"
plutil -replace CFBundleVersion -string "$MENUBAR_VERSION" "$APP_DIR/Contents/Info.plist"
plutil -replace LSMinimumSystemVersion -string "$MIN_MACOS" "$APP_DIR/Contents/Info.plist"

if [ "${EMBED_BRIDGE:-0}" = "1" ]; then
    echo "Embedding universal agent-watch-bridge helper..."
    mkdir -p "$APP_DIR/Contents/Helpers"
    BRIDGE_VERSION="$(sed -n 's/^BRIDGE_VERSION=//p' "$REPO_ROOT/VERSIONS")"
    BRIDGE_LDFLAGS="-s -w -X main.version=$BRIDGE_VERSION"
    for arch in "${ARCHS[@]}"; do
        go_arch="$arch"
        [ "$go_arch" = "x86_64" ] && go_arch="amd64"
        CGO_ENABLED=0 GOOS=darwin GOARCH="$go_arch" go build -ldflags "$BRIDGE_LDFLAGS" \
            -o "$WORK/agent-watch-bridge-$arch" "$REPO_ROOT/cmd/bridge"
    done
    lipo -create "$WORK/agent-watch-bridge-arm64" "$WORK/agent-watch-bridge-x86_64" \
        -output "$APP_DIR/Contents/Helpers/agent-watch-bridge"
    codesign --force --sign - --timestamp=none "$APP_DIR/Contents/Helpers/agent-watch-bridge"
fi

codesign --force --sign - --timestamp=none "$APP_DIR"

echo "Created $APP_DIR"
