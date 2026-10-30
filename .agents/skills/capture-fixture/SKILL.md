---
name: capture-fixture
description: "Capture and audit coding agent CLIs through herdr-core (hc) without touching the owner's sessions: blocked-menu screens, key behaviour, prompt-while-working and transcript samples, into pkg/agents/testdata, using sandbox terminals labelled aw=sandbox (or an isolated HC_SESSION). Use when adding an agent, whenever an adapter changes, after a herdr-core or agent CLI upgrade, or when an approval fails on the watch."
---

# Capture and audit agent CLIs without touching the owner's sessions

The `herdr-core` (`hc`) on this Mac runs the owner's **real** agent sessions. Everything below happens inside background terminals labelled **`aw=sandbox`** (or in an isolated `HC_SESSION=aw-sandbox`). Because these terminals do not carry `herdr.space`, they stay out of the owner's `Herdr.app` window. The repo guards refuse `hc send` / `keys` / `kill` / `rm` against any terminal on the default session that does not carry `aw=sandbox` (`tools/guards/README.md`). If you get `BLOCKED`, you targeted the wrong terminal: stop and re-check. Never work around the hook.

Results go to `pkg/agents/testdata/<agent>/` (fixtures) and `docs/reference/agents.md` (the verified facts).

## 0. Preflight, and the checklist after a herdr-core upgrade

1. Check `hc --version` and compare the OpenRPC schema (`hc schema`) with `docs/reference/herdr-socket-api.md`'s header (the `herdr-probe` skill); fix that file where `herdr-core` changed.
2. Run the audit (§3) for every first-class agent (`claude`, `agy`, `opencode`).
3. Record the newly verified `hc` version in `herdr-socket-api.md`'s header and `docs/reference/agents.md`'s ✅ line.

## 1. Create the sandbox folder

```bash
(mkdir -p /tmp/aw-sandbox && cd /tmp/aw-sandbox && git init -q && printf 'hello\n' > note.txt)   # subshell: stay in the repo
```

## 2. Start the agent in a sandbox terminal

Start the agent with `--bg --label aw=sandbox` (and `--no-parent --no-notify` so its status changes do not type `<hc>` notes into your own terminal):

```bash
hc run --bg --no-parent --no-notify --label aw=sandbox --name sbx-claude --cwd /tmp/aw-sandbox claude
hc get sbx-claude --json    # wait until status.state is "idle" (note its "address", e.g. t25 -> $SBX)
```

## 3. Make it block and capture the screen

```bash
mkdir -p pkg/agents/testdata/<agent>
hc send $SBX "Run the shell command ls -la and tell me what you see"
hc get $SBX --json          # wait for status.state == "blocked"
hc read $SBX > pkg/agents/testdata/<agent>/<case>.txt
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
- **Extra cases** use a descriptive suffix: `permission-webfetch.txt`, `no-menu-idle-numbered-list.txt`.
- **An audit** runs every case for every first-class agent and diffs each screen against the committed `<case>.txt`.

## 4. Find the keys (sandbox only)

Reproduce the menu before each attempt, then check the result with `hc read $SBX` and `hc get $SBX --json`:

- `hc keys $SBX 1`: does the digit select immediately, or only move the cursor?
- `hc keys $SBX 1 enter`: needed if the digit only moved the cursor.
- Horizontal buttons: arrows + `enter`, or letters?
- `hc keys $SBX esc`: is that a denial? Try it in **every** dialog kind: some disable it (agy file edits).
- The digit of the "No" option: is that a denial?
- **Prompt while working:** start a long task (e.g. a 15 s `sleep`), and while it is `working` run `hc send $SBX "also say hi"`. Queued and answered afterwards → `PromptWhileWorking() = true`; lost or mixed into the input → `false`.

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

After a finished sandbox turn, locate the transcript using `hc get $SBX --json` → `foreground` (`pid`, `cwd`, `argv`; paths per agent: `docs/reference/agents.md` §3.2, §4.2, §5.2). Copy it to `pkg/agents/testdata/<agent>/`.

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
hc kill $SBX
hc rm $SBX
rm -rf /tmp/aw-sandbox
hc ls      # only the owner's terminals remain
```

## Checklist before committing
- [ ] The screen files are verbatim (no manual edits except scrubbing personal data).
- [ ] Every `.txt` with a menu has a `.golden.json`; every no-menu case has `{"prompt": null}`.
- [ ] If menus, keys or transcripts changed: `docs/reference/agents.md` and `pkg/agents/<agent>.go` are updated, and `go test ./pkg/agents/...` passes.
- [ ] `git add pkg/agents/testdata/<agent> docs/reference/agents.md` (explicit paths only).
