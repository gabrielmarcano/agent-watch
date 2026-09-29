# Agent CLI Verification & Audit Runbook

> **Purpose:** A complete, reproducible guide for agents and maintainers to fully audit all supported coding agent CLIs (`claude`, `agy`, `opencode`, and any new CLI). Run this periodically, after CLI version upgrades, after herdr updates, or whenever an agent's approval menu fails to parse or respond.

---

## 1. When to Run This Audit

Run this audit when:
1. **Periodic maintenance** (e.g. monthly check of active agent tools).
2. **Agent CLI upgrade:** `claude`, `antigravity-cli`, or `opencode` was updated on the host.
3. **Herdr upgrade:** `herdr` protocol or integration hooks changed.
4. **Behavioral failure:** an agent's approval menu failed to parse on the watch, keys failed to trigger the expected response, or transcript history stopped updating.
5. **Adding a new agent:** qualifying a new agent to become first-class (see also `.agents/skills/add-agent-adapter/SKILL.md`).

---

## 2. Safety Rules (Non-Negotiable)

- **The host Mac runs the owner's real working agent sessions.**
- **NEVER** send keys, prompts, or commands to any pane outside the `aw-sandbox` workspace. The repo guards (`tools/guards/guards.py`) will block mutating commands to non-sandbox panes.
- Always verify your active pane ID before dispatching any `prompt` or `send-keys`.

---

## 3. Step-by-Step Audit Procedure

### Step 1: Check Herdr Integrations
Check the status of all installed hooks and extensions:
```bash
herdr integration status
```
- Verify that `claude`, `antigravity-cli`, and `opencode` report `current`.
- If an integration shows `outdated`, **ask the owner** before running `herdr integration install <agent>` (since it alters user configuration files).
- After a herdr upgrade or a manifest update, run `tools/herdr-overrides/herdr-overrides.sh check` and follow what it says (`tools/herdr-overrides/README.md`).

### Step 2: Create the Disposable Sandbox
Create a fresh, isolated workspace for testing:
```bash
mkdir -p /tmp/aw-sandbox && cd /tmp/aw-sandbox && git init -q
printf 'hello\n' > note.txt
herdr workspace create --label aw-sandbox --cwd /tmp/aw-sandbox --no-focus
herdr workspace list
```
Note the `workspace_id` (e.g. `w8`). List panes in the workspace:
```bash
herdr pane list --workspace <workspace_id>
```
Store this `pane_id` (referred to as `$PANE`).

---

### Step 3: Run the Full Audit Matrix on Each Agent

Run through the checks below for each agent in `claude`, `agy`, `opencode`:

#### A. Start Agent & Verify Lifecycle
```bash
herdr pane run $PANE "<agent>"  # default permission mode; no auto/yolo flags
herdr agent list                # confirm pane shows correct agent and status "idle"
```

#### B. Test Command Permission Menu
Prompt the agent to run a shell command requiring authorization:
```bash
herdr agent prompt $PANE "Run the shell command ls -la and tell me what you see"
```
Wait for `herdr agent list` to report `agent_status: blocked`. This relies on `tools/herdr-overrides/` being installed: upstream herdr 0.9.1 misses agy's permission dialogs and Claude dialogs after a relaunch in the same pane (`docs/reference/agents.md` §3.1, §4.1).
Capture the screen:
```bash
herdr agent read $PANE --source visible --format text
```
Check:
- Did it render a permission menu?
- Does it match existing fixtures in `pkg/agents/testdata/<agent>/permission-bash.txt`?
- Check key handling:
  - Digit immediate vs digit + Enter.
  - Horizontal buttons (arrows + Enter vs letters).
  - Cancel key (`esc`).
- **OpenCode:** also recapture the focus fixtures with `herdr agent read $PANE --source visible --format ansi` into `pkg/agents/testdata/opencode/focus/` (`docs/reference/agents.md` §5.1).

#### C. Test File-Edit Permission Menu
Prompt the agent to edit a file requiring authorization:
```bash
herdr agent prompt $PANE "Append the line 'world' to note.txt"
```
Wait for `agent_status: blocked` (same caveat as in B), then capture screen:
```bash
herdr agent read $PANE --source visible --format text
```
**Crucial checks:**
- Check option labels and roles (`allow_once`, `allow_always`, `deny`).
- Check cancel behavior: verify whether `esc` works or is explicitly disabled by the TUI (e.g. Antigravity CLI disables `Esc` during file edits and requires option `"2"` to cancel).

#### D. Test Multiple-Choice Questions & Plan Mode
- **AskUserQuestion:**
  Ask: `"Ask me with a multiple-choice question which color I prefer: red, green or blue"`.
  - If agent renders a menu: capture screen to `question-multiple.txt`.
  - If unsupported: document in `question-multiple.missing.md`.
- **Plan Mode:**
  Enter plan mode (e.g. `shift+tab` in Claude) and ask to plan a task.
  - If agent renders a plan approval menu: capture screen to `plan-approval.txt`.
  - If unsupported: document in `plan-approval.missing.md`.

#### E. Test Prompt-While-Working Capability
Trigger a long-running command (e.g. 15-second sleep):
```bash
herdr agent prompt $PANE "Run a bash command that sleeps 15 seconds: python3 -c 'import time; time.sleep(15)'"
```
While `agent_status: working`, immediately send a second prompt:
```bash
herdr agent prompt $PANE "also say hi"
```
Wait for the command to finish. Check the screen:
- Did the agent queue the prompt and answer `"hi"` upon completion?
- If yes: `PromptWhileWorking() = true`.
- If dropped/ignored: `PromptWhileWorking() = false`.

#### F. Test Transcript & History Extraction
After the turn finishes (`agent_status: done` or `idle`), inspect where the session transcript is saved:
- **Claude:** `find ~/.claude* -name '<session_uuid>.jsonl'`
- **Antigravity CLI:** `~/.gemini/antigravity-cli/brain/<session_uuid>/.system_generated/logs/transcript_full.jsonl`
- **OpenCode:** SQLite database at `~/.local/share/opencode/opencode.db` (`message` and `part` tables).

Verify that `LastTurn` logic in `docs/reference/agents.md` can extract the query and response cleanly.

---

### Step 4: Scrub Testdata & Prevent Data Leaks

If any fixture or transcript sample is updated:
1. Replace usernames with `testuser` or `me`.
2. Replace personal emails with `test@example.com`.
3. Replace home paths with `/tmp/aw-sandbox`.
4. Replace private IP addresses and personal hostnames with documentation test ranges (`192.0.2.x`).
5. Run the safety check:
   ```bash
   git grep --untracked -nE '/Users/[a-z]+|@gmail|sk-|ghp_' pkg/agents/testdata
   ```
   **Must return 0 matches (exit code 1).**

---

### Step 5: Update Adapters & Reference

If any agent CLI altered its menus, keys, or transcript formats:
1. Update [`docs/reference/agents.md`](../reference/agents.md) with the new verified facts.
2. Update the corresponding adapter in `pkg/agents/<agent>.go`.
3. Run the Go test suite:
   ```bash
   go test ./pkg/agents/...
   ```

---

### Step 6: Clean Up Sandbox
Always terminate all sandbox sessions and delete the temporary directory when finished:
```bash
herdr workspace close <workspace_id>
rm -rf /tmp/aw-sandbox
herdr agent list   # Verify only owner panes remain
```
