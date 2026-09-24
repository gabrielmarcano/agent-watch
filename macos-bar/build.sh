#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
APP_DIR="$REPO_ROOT/bin/AgentWatchBar.app"

echo "Building AgentWatchBar..."
mkdir -p "$APP_DIR/Contents/MacOS" "$APP_DIR/Contents/Resources"

swiftc -O -framework AppKit -o "$APP_DIR/Contents/MacOS/AgentWatchBar" "$SCRIPT_DIR/main.swift"
cp "$SCRIPT_DIR/Info.plist" "$APP_DIR/Contents/Info.plist"

echo "Created $APP_DIR"
