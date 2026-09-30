---
name: capture-fixture
description: "Capture and audit coding agent CLIs through herdr without touching the owner's sessions: blocked-menu screens, key behaviour, prompt-while-working and transcript samples, into pkg/agents/testdata, using a disposable aw-sandbox workspace. Use when adding an agent, whenever an adapter changes, after a herdr or agent CLI upgrade, or when an approval fails on the watch."
---

# Capture and audit agent CLIs without touching the owner's sessions

The herdr on this Mac runs the owner's **real** agent sessions. Everything below happens inside a workspace labelled **`aw-sandbox`**. The repo guards refuse `send-keys` / `prompt` / `pane run` against any pane outside it; the one exception, `pane run` in a fresh `aw-session-*` tab, is in `tools/guards/README.md`. If you get `BLOCKED`, you targeted the wrong pane: stop and re-check. Never work around the hook.

Results go to `pkg/agents/testdata/<agent>/` (fixtures) and `docs/reference/agents.md` (the verified facts).

## 0. Preflight, and the checklist after a herdr upgrade

1. `herdr integration status`: the integrations of the first-class agents (`AGENTS.md` §1.2; agy's is `antigravity-cli`), and of any agent you are adding, must say `current`. If one is `outdated` or `not installed`, **ask the owner**: `herdr integration install` edits his configuration and is owner-only. A new agent's CLI is his to install too.
2. **After a herdr upgrade or a manifest update**, in this order:
   1. `tools/herdr-overrides/herdr-overrides.sh check`, and follow what it says (`tools/herdr-overrides/README.md`).
   2. Compare `herdr --version` and the socket schema with the version and protocol in `docs/reference/herdr-socket-api.md`'s header (the `herdr-probe` skill); fix that file where herdr changed.
   3. Run the audit (§3) for every first-class agent.
   4. Record the newly verified herdr version in `herdr-socket-api.md`'s header and `docs/reference/agents.md`'s ✅ line. Only if the old version no longer works, raise the minimum: `min_herdr_version` in `herdr-plugin.toml` together with `docs/GUIDE.md` § Requirements.
   5. Update the date of the last overrides check in `docs/STATUS.md` § Blocked / waiting on upstream.

## 1. Create the sandbox

```bash
(mkdir -p /tmp/aw-sandbox && cd /tmp/aw-sandbox && git init -q && printf 'hello\n' > note.txt)   # subshell: stay in the repo
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

If `herdr agent get $SBX` shows no `agent_session` once the agent has run a turn, its herdr integration is missing or the agent runs with another config dir or profile: no transcript sample is possible (step 0).

## 3. Make it block and capture the screen

```bash
mkdir -p pkg/agents/testdata/<agent>
herdr agent prompt $SBX "Run the shell command ls -la and tell me what you see"
herdr agent list                    # wait for agent_status "blocked" on $SBX
herdr agent read $SBX --source visible --format text > pkg/agents/testdata/<agent>/<case>.txt
```

| Case file | What to ask the agent | Expected menu |
|---|---|---|
| `permission-bash.txt` | "Run the shell command `ls -la` and tell me what you see" | Shell permission |
| `permission-edit.txt` | "Append the line `world` to note.txt" | File-edit permission |
| `question-multiple.txt` | "Ask me with a multiple-choice question which color I prefer: red, green or blue" | A question menu |
| `plan-approval.txt` | Enter plan mode (Claude: `shift+tab` until plan mode), then "Plan how to rename note.txt" | Plan approval |
| `no-menu-working.txt` | Any long task; capture while `working` | No menu (negative test) |

- **A case the agent cannot produce:** write `<case>.missing.md` with one line saying why.
- **An agent that runs `ls -la` without asking** (a sandboxed default mode): ask for something outside its sandbox instead, e.g. reading a file outside `/tmp/aw-sandbox`, and note the mode in the golden's `notes`.
- **Extra cases** use a descriptive suffix: `permission-webfetch.txt`, `permission-bash-herdr-done.txt`, `no-menu-idle-numbered-list.txt`.
- **No `blocked`?** Check step 0 before assuming the TUI changed: agy's dialogs, and Claude's after a relaunch in the same pane, depend on the herdr overrides (`docs/reference/agents.md` §3.1, §4.1).
- **Focus fixtures** (OpenCode's button bar): capture with `--format ansi` into `pkg/agents/testdata/opencode/focus/` (`docs/reference/agents.md` §5.1). The text format drops the colours that show focus.
- **An audit** runs every case for every first-class agent and diffs each screen against the committed `<case>.txt`.

## 4. Find the keys (sandbox only)

Reproduce the menu before each attempt, then check the result with `herdr agent read $SBX --source visible` and `herdr agent list`:

- `herdr agent send-keys $SBX 1`: does the digit select immediately, or only move the cursor?
- `herdr agent send-keys $SBX 1 Enter`: needed if the digit only moved the cursor.
- Horizontal buttons: arrows + `Enter`, or letters?
- `herdr agent send-keys $SBX esc`: is that a denial? Try it in **every** dialog kind: some disable it (agy file edits).
- The digit of the "No" option: is that a denial?
- **Prompt while working:** start a long task (e.g. a 15 s `sleep`), and while it is `working` run `herdr agent prompt $SBX "also say hi"`. Queued and answered afterwards → `PromptWhileWorking() = true`; lost or mixed into the input → `false`.

Write `pkg/agents/testdata/<agent>/<case>.golden.json`:

```json
{
  "prompt": {
    "kind": "permission",
    "title": "Bash command",
    "detail": "ls -la",
    "options": [
      {"id": "opt-1", "label": "Yes", "role": "allow_once"},
      {"id": "opt-2", "label": "Yes, and don't ask again for ls commands in /tmp/aw-sandbox", "role": "allow_always"},
      {"id": "opt-3", "label": "No, and tell Claude what to do differently (esc)", "role": "deny"}
    ]
  },
  "keys": {"opt-1": ["1"], "opt-2": ["2"], "opt-3": ["3"]},
  "cancel_keys": ["esc"],
  "verified_keys": true,
  "notes": "Digit selects immediately, no Enter needed.",
  "herdr_status": "blocked"
}
```

- **`fingerprint` is left out on purpose:** the tests compute it.
- **`verified_keys: false`** means you captured the screen but could not test the keys: say why in `notes`.
- **A no-menu case** is `{"prompt": null}`.

## 5. Transcript sample

After a finished sandbox turn, locate the transcript through `herdr agent get $SBX` → `agent_session` (paths per agent: `docs/reference/agents.md` §3.2, §4.2, §5.2). Copy it to `pkg/agents/testdata/<agent>/`.

OpenCode has no file to copy. Export the session read-only into `session.json`:

```bash
python3 - <<'EOF' > pkg/agents/testdata/opencode/session.json
import json, os, sqlite3, sys
sid = "ses_REPLACE_ME"
db = sqlite3.connect("file:" + os.path.expanduser("~/.local/share/opencode/opencode.db") + "?mode=ro", uri=True)
msgs = [{"id": i, "time_created": t, "data": json.loads(d)} for i, t, d in
        db.execute("select id, time_created, data from message where session_id=? order by time_created", (sid,))]
parts = [{"id": i, "message_id": m, "time_created": t, "data": json.loads(d)} for i, m, t, d in
         db.execute("select id, message_id, time_created, data from part where session_id=? order by time_created", (sid,))]
json.dump({"session_id": sid, "messages": msgs, "parts": parts}, sys.stdout, indent=1)
EOF
```

Then **scrub** it:
- home paths → `/tmp/aw-sandbox`;
- usernames → `me`;
- emails and tokens → `REDACTED`;
- private IPs and hostnames → `192.0.2.x`.

For Claude, keep **only** the lines whose `type` is `user` or `assistant`. The other lines (`attachment`, snapshots, `last-prompt`…) carry the owner's global instructions, session context and system prompt; the reader never needs them. Check: `grep -c '"type":"attachment"' pkg/agents/testdata/claude/*.jsonl` prints `0`.

Scrub check (must print nothing): `git grep --untracked -nE '/Users/[a-z]+|@gmail|sk-|ghp_' pkg/agents/testdata`.

Then write `transcript.expected.json`, and check the adapter's last-turn extraction on the sample.

## 6. Tear down

```bash
herdr workspace close <aw-sandbox workspace_id>
rm -rf /tmp/aw-sandbox
herdr agent list      # only the owner's panes remain
```

## Checklist before committing
- [ ] The screen files are verbatim (no manual edits except scrubbing personal data).
- [ ] Every `.txt` with a menu has a `.golden.json`; every no-menu case has `{"prompt": null}`.
- [ ] If menus, keys or transcripts changed: `docs/reference/agents.md` and `pkg/agents/<agent>.go` are updated, and `go test ./pkg/agents/...` passes.
- [ ] `git add pkg/agents/testdata/<agent> docs/reference/agents.md` (explicit paths only).
