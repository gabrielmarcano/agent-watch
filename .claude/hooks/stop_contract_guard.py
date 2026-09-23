#!/usr/bin/env python3
"""Stop hook: do not end the turn with the contracts out of sync.

If pkg/model (non-test Go) differs from HEAD but neither client model nor
contracts.md changed, ask the agent to finish the sync or explain why not.
Runs at most once per stop (respects stop_hook_active) so it cannot loop.
"""
import json
import os
import subprocess
import sys

ROOT = os.environ.get("CLAUDE_PROJECT_DIR") or os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


def changed(pathspec: str) -> bool:
    out = subprocess.run(["git", "-C", ROOT, "status", "--porcelain", "--", pathspec],
                         capture_output=True, text=True).stdout
    return bool(out.strip())


def main():
    try:
        data = json.load(sys.stdin)
    except Exception:
        sys.exit(0)
    if data.get("stop_hook_active"):
        sys.exit(0)

    model_files = subprocess.run(
        ["git", "-C", ROOT, "status", "--porcelain", "--", "pkg/model"], capture_output=True, text=True
    ).stdout.splitlines()
    model_changed = any(l.endswith(".go") and not l.endswith("_test.go") for l in model_files)
    if not model_changed:
        sys.exit(0)

    others = {
        "docs/reference/contracts.md": changed("docs/reference/contracts.md"),
        "wearos-app model": changed("wearos-app/app/src/main/java/com/gabriel/agentwatch/model"),
        "watchos-app Models": changed("watchos-app/AgentWatch/Models"),
    }
    missing = [k for k, v in others.items() if not v]
    if missing:
        print("pkg/model has uncommitted changes but these were not updated: " + ", ".join(missing)
              + ". Update them (see .claude/skills/schema-sync/SKILL.md), or tell the owner explicitly "
                "why the change does not affect them (e.g. a helper function, not a JSON field).",
              file=sys.stderr)
        sys.exit(2)
    sys.exit(0)


if __name__ == "__main__":
    main()
