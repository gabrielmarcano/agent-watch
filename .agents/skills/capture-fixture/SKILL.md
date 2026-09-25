---
name: capture-fixture
description: "Safely capture a coding agent's blocked-menu screen, key behaviour and transcript sample from herdr into pkg/agents/testdata, using a disposable aw-sandbox workspace. Use for Phase 0, whenever an adapter changes, when an agent's TUI changed, or when adding support for a new agent."
---

# Capture an agent fixture without touching the owner's sessions

The herdr on this Mac runs the owner's **real** agent sessions. Everything below happens inside a workspace labelled **`aw-sandbox`**. The repo guards (agy and OpenCode hooks) refuse `send-keys` / `prompt` / `pane run` against any pane outside that workspace. If you get `BLOCKED`, you targeted the wrong pane: stop and re-check. Never work around the hook.

For the complete end-to-end audit runbook covering all CLIs, key checks, and transcript verification, see [`docs/guides/agent-cli-audit.md`](../../docs/guides/agent-cli-audit.md).

## 1. Create the sandbox

```bash
mkdir -p /tmp/aw-sandbox && cd /tmp/aw-sandbox && git init -q && printf 'hello\n' > note.txt
herdr workspace create --label aw-sandbox --cwd /tmp/aw-sandbox --no-focus
herdr workspace list        # note the workspace_id of "aw-sandbox"
herdr pane list --workspace <aw-sandbox workspace_id>   # note the pane_id → call it $SBX
```

For more panes: `herdr tab create --workspace <id> --cwd /tmp/aw-sandbox --label <agent>`, then list the panes again.

## 2. Start the agent in the sandbox pane

```bash
herdr pane run $SBX "claude"        # or: agy, opencode (default permission mode; no yolo/auto flags)
herdr agent list                    # wait until $SBX shows the right "agent" and agent_status "idle"
```

## 3. Make it block and capture the screen

```bash
herdr agent prompt $SBX "Run the shell command ls -la and tell me what you see"
herdr agent list                    # wait for agent_status "blocked" on $SBX
herdr agent read $SBX --source visible --format text > pkg/agents/testdata/<agent>/<case>.txt
```

Case names and what to ask: see `docs/phases/0-fixtures.md` §3.

## 4. Find the keys (sandbox only)

Reproduce the menu before each attempt, then check the result with `herdr agent read $SBX --source visible` and `herdr agent list`:

- `herdr agent send-keys $SBX 1`: does the digit select immediately, or only move the cursor?
- `herdr agent send-keys $SBX 1 Enter`: needed if the digit only moved the cursor.
- `herdr agent send-keys $SBX esc`: is that treated as a denial?
- The digit of the "No" option: is that a denial?

Record the results in `<case>.golden.json`. The format is in `docs/phases/0-fixtures.md` §4.

## 5. Transcript sample

After a finished sandbox turn, locate the transcript through `herdr agent get $SBX` → `agent_session` (details in `docs/reference/agents.md`). Copy it to `pkg/agents/testdata/<agent>/`, then **scrub** it:
- home paths → `/tmp/aw-sandbox`;
- usernames → `me`;
- emails and tokens → `REDACTED`.

For Claude, keep **only** the lines whose `type` is `user` or `assistant`. The other lines (`attachment`, snapshots, `last-prompt`…) carry the owner's global instructions, session context and system prompt; the reader never needs them. Check: `grep -c '"type":"attachment"' pkg/agents/testdata/claude/*.jsonl` prints `0`.

Then write `transcript.expected.json`.

## 6. Tear down

```bash
herdr workspace close <aw-sandbox workspace_id>
rm -rf /tmp/aw-sandbox
herdr agent list      # only the owner's panes remain
```

## Checklist before committing
- [ ] The screen files are verbatim (no manual edits except scrubbing personal data).
- [ ] Every `.txt` with a menu has a `.golden.json`; the no-menu case has `{"prompt": null}`.
- [ ] `docs/reference/agents.md` ✅/🔍 marks are updated.
- [ ] `git add pkg/agents/testdata/<agent> docs/reference/agents.md` (explicit paths only).
