# Phase 0 — Capture Agent Fixtures and Close the Unknowns

> **Goal:** replace every 🔍 in [`docs/reference/agents.md`](../reference/agents.md) with verified facts and real captured screens, so that Phase 2b (adapters) is pure implementation with no guessing.

| | |
|---|---|
| **Depends on** | nothing |
| **Blocks** | Phase 2b (adapters). Phases 1, 2a, 3 and 4 can start without it |
| **Touches** | `pkg/agents/testdata/**`, `docs/reference/agents.md`, `docs/STATUS.md` |
| **Must not touch** | any Go/Kotlin/Swift source; any herdr pane you did not create |
| **Needs the owner?** | Yes, for step 1 (updating the Claude integration changes his Claude config) |

---

## Read first

1. [`docs/reference/herdr-socket-api.md`](../reference/herdr-socket-api.md), especially the safety warning at the top.
2. [`docs/reference/agents.md`](../reference/agents.md).
3. The `capture-fixture` skill (`.agents/skills/capture-fixture/SKILL.md`).

---

## Steps

### 1. Bring agent integrations up to date (owner approval required)

```bash
herdr integration status
```

- Every priority agent (`claude`, `agy`, `opencode`) must show `current`.
- If `claude` shows `outdated`, **ask the owner** before running:

  ```bash
  herdr integration install claude
  ```

  It rewrites a hook in the owner's Claude config directory.
- Then run `herdr integration status` again and paste the output into the Phase 0 section of `docs/STATUS.md`.

### 2. Create the sandbox

The sandbox isolates your experiments from the owner's real sessions.

```bash
mkdir -p /tmp/aw-sandbox && cd /tmp/aw-sandbox && git init -q
printf 'hello\n' > note.txt
herdr workspace create --help   # read the flags; create a workspace named "aw-sandbox"
```

- Create one workspace named `aw-sandbox` with its cwd at `/tmp/aw-sandbox`. Use `herdr workspace create` / `herdr tab create` as their `--help` describes.
- Write down the `pane_id` of **every** pane you create in the sandbox, then run `herdr agent list`.
- From now on, you may only send keys or prompts to panes in that list.

### 3. Capture each agent's menus

For **each** agent in `claude`, `agy` and `opencode`:

1. Start the agent in a sandbox pane with its **default** permission mode. Do not use auto/yolo modes; they skip the menus.
2. Wait until `herdr agent list` shows the pane with the right `agent` and `agent_status: idle`.
3. Type each case below into the agent **by hand in the sandbox pane**, or with `herdr agent prompt <sandbox_pane> "<text>"`.
4. Wait for `agent_status: blocked`, then capture the screen:

   ```bash
   herdr agent read <sandbox_pane> --source visible --format text \
     > pkg/agents/testdata/<agent>/<case>.txt
   ```

| Case file | What to ask the agent | Expected menu |
|---|---|---|
| `permission-bash.txt` | "Run the shell command `ls -la` and tell me what you see" | Shell permission |
| `permission-edit.txt` | "Append the line `world` to note.txt" | File-edit permission |
| `question-multiple.txt` | Claude only: "Ask me with a multiple-choice question which color I prefer: red, green or blue" | AskUserQuestion |
| `plan-approval.txt` | Claude only: enter plan mode (`shift+tab` until plan mode), ask "Plan how to rename note.txt" | Plan approval |
| `no-menu-working.txt` | Any long task; capture while `working` | No menu (negative test) |

If an agent cannot produce a case, write a `<case>.missing.md` file with one line explaining why.

### 4. Find out which keys answer each menu (sandbox only)

For every captured permission menu, reproduce the menu and test these in order. After each attempt, run `herdr agent read … --source visible` and `herdr agent list`, and note what happened.

1. **Digit of the "Yes" option** (e.g. `herdr agent send-keys <sandbox_pane> 1`): did it approve immediately, or only move the cursor?
2. **If it only moved the cursor:** try the digit, then `Enter`.
3. **Cancel:** reproduce the menu, send `esc`. Did the agent treat it as a denial?
4. **Deny option:** reproduce the menu, send the digit of the "No" option.

For each case, write the golden file `pkg/agents/testdata/<agent>/<case>.golden.json`:

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
  "notes": "Digit selects immediately, no Enter needed."
}
```

- **`fingerprint` is omitted on purpose.** Tests compute it.
- **`verified_keys: false`** means you captured the screen but could not test the keys. Say why in `notes`.
- **For `no-menu-working`**, the golden file is `{"prompt": null}`.

### 5. Check "prompt while working"

For each agent:
1. Start a long task.
2. While it is `working`, run `herdr agent prompt <sandbox_pane> "also say hi"`.
3. Record whether the agent queued the message, lost it, or mixed it into the current input.

### 6. Capture a transcript sample for each agent (synthetic content only)

Once a sandbox turn has finished (`done`), copy the agent's transcript for that session into testdata. **Only sandbox sessions**, never the owner's real work.

| Agent | Where to find the file | Save as |
|---|---|---|
| `claude` | `herdr agent list` → `agent_session.value` = UUID → `find ~/.claude* -name '<uuid>.jsonl'` | `pkg/agents/testdata/claude/transcript.jsonl` |
| `agy` | `agent_session.value` (path, or id → `~/.gemini/antigravity-cli/brain/<id>/.system_generated/logs/transcript_full.jsonl`) | `pkg/agents/testdata/agy/transcript_full.jsonl` |
| `opencode` | `agent_session.value` = `ses_…` | Export with the script below into `pkg/agents/testdata/opencode/session.json` |

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

Before saving, scrub the sample. Replace these with `/tmp/aw-sandbox`, `me` or `REDACTED`:
- absolute home paths
- usernames
- emails
- tokens

For Claude, also drop every line whose `type` is not `user` or `assistant`. `attachment` lines embed the owner's global instructions, session context and the full system prompt; the grep below does not catch them.

Then add `pkg/agents/testdata/<agent>/transcript.expected.json` with the `query` and `response` you expect `LastTurn` to return.

### 7. Record the findings

- Update [`docs/reference/agents.md`](../reference/agents.md): turn every 🔍 into ✅ with the real value, or into ❌ with the explanation.
- Fill in the Phase 0 section of `docs/STATUS.md`.

### 8. Clean up

- Close the sandbox panes and workspace.
- `rm -rf /tmp/aw-sandbox`.
- Confirm with `herdr agent list` that only the owner's panes remain.

---

## Definition of done

- [ ] `herdr integration status` shows `current` for claude, agy, opencode (or the owner declined, recorded in STATUS).
- [ ] `pkg/agents/testdata/{claude,agy,opencode}/` each contain the captured `.txt` files and a `.golden.json` for each.
- [ ] Each agent has a transcript sample plus `transcript.expected.json`, all scrubbed.
- [ ] No 🔍 left in `docs/reference/agents.md`.
- [ ] `git grep -nE '/Users/[a-z]+|@gmail|sk-|ghp_' pkg/agents/testdata` returns nothing.
- [ ] `grep -c '"type":"attachment"' pkg/agents/testdata/claude/*.jsonl` prints `0`.
- [ ] Sandbox removed.
- [ ] Commit only the files listed under **Touches**: `git add pkg/agents/testdata docs/reference/agents.md docs/STATUS.md`.

---

## Pitfalls

- **Sending keys to the wrong pane.** Always copy the `pane_id` from your own sandbox list; never from memory.
- **Auto-approve modes** (Claude "auto mode", `--dangerously-skip-permissions`, OpenCode permissive config) hide the menu. Use defaults.
- **Terminal width changes the wrap of long labels.** Capture at the pane's normal size; the parser must handle continuation lines.
- **Screens contain box-drawing characters and status bars.** That is expected; keep the capture verbatim.

---

## Prompt for the executing agent

```
You are executing Phase 0 of the Agent Watch refactor in /Users/me/Code/personal/agent-watch-herdr.
Read docs/phases/0-fixtures.md and follow it step by step. Also read docs/reference/herdr-socket-api.md
(the safety warning at the top is mandatory) and .agents/skills/capture-fixture/SKILL.md.
Rules: only send keys or prompts to panes you created inside the "aw-sandbox" workspace; never touch other
panes. Ask the owner before running `herdr integration install claude`. Do not write Go/Kotlin/Swift code.
Scrub personal data from every fixture. When done, tick the Definition of Done in docs/STATUS.md and commit
only pkg/agents/testdata, docs/reference/agents.md and docs/STATUS.md. Report which 🔍 items were resolved
and which could not be.
```
