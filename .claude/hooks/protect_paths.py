#!/usr/bin/env python3
"""PreToolUse hook (matcher: Edit|Write|MultiEdit): refuse to write where nobody should.

- Legacy paths deleted by the herdr refactor must not come back.
- Secret files are never written by agents (the owner places them by hand).
"""
import json
import os
import re
import sys

# Anchored at the repo root: pkg/bridge/ is new code and must stay writable.
LEGACY = re.compile(r"^(bridge|claude-plugin|\.claude-plugin)/")
SECRETS = re.compile(r"(^|/)(google-services\.json|firebase-service-account[^/]*\.json|\.env)$")
ROOT = os.environ.get("CLAUDE_PROJECT_DIR") or os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


def main():
    try:
        data = json.load(sys.stdin)
    except Exception:
        sys.exit(0)
    tool_input = data.get("tool_input") or {}
    path = tool_input.get("file_path") or tool_input.get("filePath") or ""
    if not path:
        sys.exit(0)
    rel = os.path.relpath(os.path.abspath(path), ROOT)
    if rel.startswith("docs/") or rel.endswith(".md"):
        sys.exit(0)
    if LEGACY.search(rel):
        print(f"BLOCKED: {path} is a legacy path removed by the herdr refactor (Node bridge, Claude "
              "plugin). Read AGENTS.md §1.1: the replacement lives in cmd/, pkg/ and herdr-plugin.toml.",
              file=sys.stderr)
        sys.exit(2)
    if SECRETS.search(path):
        print(f"BLOCKED: {path} holds secrets and is placed by the owner by hand, never written by an "
              "agent. Use the *.example files for templates.", file=sys.stderr)
        sys.exit(2)
    sys.exit(0)


if __name__ == "__main__":
    main()
