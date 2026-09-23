#!/usr/bin/env python3
"""PostToolUse hook (matcher: Edit|Write|MultiEdit).

- gofmt -w on edited .go files (silent, never fails the edit).
- Reminders fed back to the agent (exit 2 → stderr is shown to the model;
  the edit itself already happened, nothing is undone):
    * contract files changed → keep Go, Kotlin, Swift and contracts.md in sync
    * adapter code changed   → every behaviour change needs a fixture test
"""
import json
import re
import shutil
import subprocess
import sys

CONTRACT = re.compile(
    r"(pkg/model/(?!.*_test\.go)[^/]+\.go$|docs/reference/contracts\.md$"
    r"|wearos-app/.*/model/[^/]+\.kt$|watchos-app/AgentWatch/Models/[^/]+\.swift$)"
)
ADAPTER = re.compile(r"pkg/agents/(?!.*_test\.go)[^/]+\.go$")


def main():
    try:
        data = json.load(sys.stdin)
    except Exception:
        sys.exit(0)
    tool_input = data.get("tool_input") or {}
    path = tool_input.get("file_path") or tool_input.get("filePath") or ""
    if not path:
        sys.exit(0)

    if path.endswith(".go") and shutil.which("gofmt"):
        subprocess.run(["gofmt", "-w", path], capture_output=True)

    notes = []
    if CONTRACT.search(path):
        notes.append(
            "You changed a contract file. The four copies must stay identical: "
            "docs/reference/contracts.md, pkg/model (Go), wearos-app model (Kotlin, @Keep), "
            "watchos-app Models (Swift). Follow .claude/skills/schema-sync/SKILL.md before finishing."
        )
    if ADAPTER.search(path):
        notes.append(
            "You changed agent adapter code. Every behaviour change needs a fixture or synthetic test in "
            "pkg/agents/testdata that fails without it, and option roles must come from labels, never "
            "positions (docs/reference/agents.md §2)."
        )
    if notes:
        print("REMINDER: " + "\nREMINDER: ".join(notes), file=sys.stderr)
        sys.exit(2)
    sys.exit(0)


if __name__ == "__main__":
    main()
