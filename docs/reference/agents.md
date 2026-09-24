# Agent Knowledge Reference (for `pkg/agents`)

Everything that depends on **which** coding agent runs in a pane lives in `pkg/agents` and is documented here:

- how its approval menu looks
- which keys answer it
- how to cancel it
- where its transcript lives
- how to read the last turn from that transcript

The rest of the system (`pkg/herdr`, the relay, the clients) must stay agent-agnostic.

**Status legend:**
- ✅ verified on the development Mac on 2026-09-23
- 🔍 must be captured and verified in Phase 0 before the adapter is written

---

## 1. Adapter contract (recap)

```go
type Adapter interface {
    Name() string                                  // herdr agent id, e.g. "claude"
    ParsePrompt(screen string) (Prompt, bool)      // screen = agent.read visible/text
    CancelKeys() []string                          // keys that dismiss the current menu
    PromptWhileWorking() bool                      // may we type a new prompt while working?
    LastTurn(ctx context.Context, ref SessionRef) (*model.HistoryItem, error)
}
```

- **Registry:** an exact match on herdr's `agent` field. Anything not registered uses `generic`.
- **Overrides:** adapters embed the generic behaviour and override only what differs.
- **Missing transcript:** `LastTurn` returns `ErrNoTranscript` when it cannot find or read one. The caller then falls back to a screen capture (§6).

---

## 2. Generic behaviour (all agents unless overridden)

| Aspect | Generic rule |
|---|---|
| Menu detection | The **last** block of ≥ 2 consecutive lines matching `^\s*(?:[❯›>▶●]\s*)?(\d+)[.)]\s+(.+?)\s*$` on the visible screen. Up to 2 continuation lines (indented, no number) may follow an option; append them to its label with a space |
| Cursor row | The option line that starts with a marker `❯ › > ▶ ●`. If none has one, assume the first |
| Title | The nearest non-empty line above the menu block that is not a box-drawing line (`─│╭╮╰╯┌┐└┘`), with box characters and surrounding whitespace trimmed |
| Detail | Non-empty lines between the title and the menu, joined with `\n`, trimmed, max 400 chars |
| Role by label | lowercase the label, then: contains `don't ask again`, `always`, `for this session`, `all edits`, `for all` → `allow_always`; else starts with `yes` or contains `allow`, `approve`, `proceed`, `accept` → `allow_once`; else starts with `no` or contains `deny`, `reject`, `cancel`, `decline` → `deny`; else `choice` |
| Keys for option n | `[fmt.Sprint(n)]` (digit selection) |
| Cancel | `["esc"]` |
| Prompt while working | `false` |
| History | none — always `ErrNoTranscript` |

**Hard rules:**
- **Never map options by position.** Always use the label.
- **With no `deny` option parsed,** the Deny button uses `CancelKeys()`.
- **With no menu found,** return `ok=false`. The bridge then publishes `Kind=unknown` with `RawTail`, and the watch offers **only** Cancel.

---

## 3. Claude Code (`claude`)

### 3.1 Menus

| Item | Status | Value |
|---|---|---|
| Permission menu | ✅ (Claude Code behaviour) | `1. Yes` / `2. Yes, and don't ask again for …` / `3. No, and tell Claude what to do differently (esc)`. **The number and wording of options vary by tool**: some prompts have only 2 options, and edits say "allow all edits during this session" |
| Selected-row marker | ✅ | `❯` |
| Digit selects immediately | ✅ | Confirmed: typing the option digit selects immediately (no Enter needed). |
| Cancel | ✅ | `esc` rejects the tool call |
| AskUserQuestion / plan approval | ✅ | Confirmed: both render numbered menus with digit selection (`1. ...`, `2. ...`). Mapped to `kind: "question"`. Captured in `question-multiple.txt` and `plan-approval.txt`. |
| Prompt while working | ✅ | Claude queues typed messages while working → `PromptWhileWorking() = true` |

### 3.2 Transcript

| Item | Status | Value |
|---|---|---|
| herdr session ref | ✅ | `agent_session.kind = "id"`, `value` = session UUID. A current integration (v10) may report `kind="path"` instead. Support both |
| File location | ✅ | `<config_dir>/projects/<slug>/<session_uuid>.jsonl` |
| Slug | ✅ | The session's cwd with every `/` and `.` replaced by `-` (e.g. `/Users/me/Code/app` → `-Users-me-Code-app`) |
| Config dirs | ✅ | From `claude_config_dirs` in the bridge config (default `~/.claude`). Users with `CLAUDE_CONFIG_DIR` add theirs. Try each dir in order. If the slug dir does not match, glob `<config_dir>/projects/*/<uuid>.jsonl` |
| Format | ✅ | JSONL. Each line has `type`, `uuid`, `timestamp`, `sessionId`, `isSidechain`, `message` |

**Line kinds seen** (`type`): `user`, `assistant`, `attachment`, `system`, `queue-operation`, `last-prompt`, plus others. Ignore every type except `user` and `assistant`.

`message.content` is a string **or** an array of blocks with a `type` field:

| Line `type` | Block types seen | Meaning |
|---|---|---|
| `user` | string | A real user prompt |
| `user` | `tool_result` | Tool output — **not** a user prompt |
| `assistant` | `text` | Visible answer text |
| `assistant` | `tool_use`, `thinking` | Not answer text |

**`LastTurn` algorithm:**
1. Tail-read the last 256 KB of the file. Drop the first, partial line. If the file is smaller, read all of it.
2. Skip lines with `isSidechain == true` (sub-agents).
3. **Query** = the last `user` line whose content is a string, or an array containing at least one `text` block, and that is not a command wrapper (text starting with `<command-`, `<local-command-`, `<bash-` or `<system-reminder>`). Join its `text` blocks with `\n`.
4. **Response** = walk forward from the query line and collect `text` blocks from `assistant` lines. Whenever a `tool_use` block appears, reset the collection. Join the remaining blocks with `\n\n`. This yields the **final** answer segment after the last tool call.
5. If the response is empty, return `ErrNoTranscript` (fall back to screen).
6. Set `session_value` to the UUID when computing the `HistoryItem.id`.

---

## 4. Antigravity CLI (`agy`)

### 4.1 Menus

| Item | Status | Value |
|---|---|---|
| Approval menu | ✅ | Numbered vertical list inside a box. Header indicates tool/kind (e.g. `Command`, `File edit`). Captured in `permission-bash.txt` and `permission-edit.txt`. Multiple-choice and plan approval are unsupported (`.missing.md`). |
| Digit selection vs arrows | ✅ | Digit selects immediately without Enter. |
| Cancel | ✅ | `esc` cancels/rejects standard commands. **Crucial quirk:** In file edits, Esc is explicitly disabled by the TUI (`"Esc disabled during file edits — press 1 to accept or 2 to reject."`), so `cancel_keys` for file edits is `["2"]`. |
| Prompt while working | ✅ | Agy queues typed prompts while working → `PromptWhileWorking() = true`. |

### 4.2 Transcript

| Item | Status | Value |
|---|---|---|
| herdr session ref | ✅ (integration code) | The integration reports `conversationId` and, when available, `transcriptPath` → `kind = "path"` |
| File location | ✅ | `~/.gemini/antigravity-cli/brain/<conversation_uuid>/.system_generated/logs/transcript_full.jsonl`. With `kind="id"`, build this path from the id |
| Format | ✅ | JSONL. Keys: `type`, `content`, `created_at`, `source`, `status`, `step_index`; `PLANNER_RESPONSE` also has `thinking`, `tool_calls` |
| Step types seen | ✅ | `USER_INPUT`, `PLANNER_RESPONSE`, `GENERIC`, `SYSTEM_MESSAGE` |

**`LastTurn` algorithm:**
1. Tail-read the last 256 KB, as for Claude.
2. **Query** = `content` of the last `USER_INPUT` step.
3. **Response** = `content` of the last `PLANNER_RESPONSE` after that query that has a non-empty `content` and an empty or missing `tool_calls`. `PLANNER_RESPONSE` steps with tool calls have no `content`.
4. If there is no such response, return `ErrNoTranscript`.

---

## 5. OpenCode (`opencode`)

### 5.1 Menus

| Item | Status | Value |
|---|---|---|
| Approval UI | ✅ | Rendered with `△ Permission required`, action description, patterns, and a horizontal button bar: `Allow once   Allow always   Reject`. Footer: `ctrl+f fullscreen  ⇆ select  enter confirm`. Captured in `permission-bash.txt` and `permission-edit.txt`. Multiple-choice question and plan approval are unsupported (`.missing.md`). |
| Keys | ✅ | `Allow once` is selected by default and confirmed with `["Enter"]`. `Allow always` is selected via `["Right", "Enter"]` (or `["Tab", "Enter"]`). `Reject` is `["esc"]` or `["Right", "Right", "Enter"]`. |
| Cancel | ✅ | `esc` rejects the permission request / interrupts. |
| Prompt while working | ✅ | OpenCode queues typed prompts while working and executes them when the current turn finishes → `PromptWhileWorking() = true`. |

### 5.2 Transcript (SQLite)

| Item | Status | Value |
|---|---|---|
| herdr session ref | ✅ (integration code) | `kind = "id"`, `value` = OpenCode session id (format `ses_…`) |
| Database | ✅ | `~/.local/share/opencode/opencode.db` (WAL mode). OpenCode 1.18.32 |
| Tables | ✅ | `message(id, session_id, time_created, time_updated, data)`, `part(id, message_id, session_id, time_created, time_updated, data)` |
| `message.data` JSON | ✅ | Has `role` (`user` / `assistant`), `time`, `modelID`, `providerID`, `finish`, … |
| `part.data` JSON | ✅ | `type` ∈ `text`, `reasoning`, `tool`, `step-start`, `step-finish`. Text is in `text` |

**Driver:** `modernc.org/sqlite` (pure Go, keeps the binary static). Open it read-only with `file:<path>?mode=ro&_pragma=busy_timeout(2000)`. Never write to this DB.

**`LastTurn` algorithm:**
```sql
SELECT id, data FROM message
WHERE session_id = ?
ORDER BY time_created DESC, id DESC
LIMIT 40;
```
1. **Query message** = the newest message whose `data.role == "user"`.
2. **Response message** = the newest message whose `data.role == "assistant"` and that is newer than the query message.
3. For each of the two, read its parts:

   ```sql
   SELECT data FROM part WHERE message_id = ? ORDER BY time_created, id
   ```

4. Join the `text` of the parts whose `type == "text"`. Skip `reasoning` and `tool`.
5. If either side is empty, return `ErrNoTranscript`.

⚠️ This schema is internal to OpenCode and may change between versions. On any SQL or JSON error, return `ErrNoTranscript`; never crash. The screen fallback covers it.

---

## 6. Screen-capture fallback (every agent)

Used when an agent has no transcript reader, or when its reader returns an error.

1. `agent.read {target: pane_id, source: "recent_unwrapped", lines: 200, format: "text"}`.
2. Trim trailing blank lines.
3. Drop the agent's input box: cut everything from the last line that contains only box-drawing characters or a prompt marker (`❯`, `>`) downwards, if found within the last 15 lines.
4. Keep the last 80 lines.
5. `HistoryItem{Source: "screen", Query: "", Response: <text>}`.

---

## 7. Capturing fixtures (Phase 0 and every adapter change)

Follow the `capture-fixture` skill. Summary:

1. Create a **dedicated sandbox** pane. Never use the owner's working panes.
2. In a throw-away directory, start the agent and make it ask for permission, e.g. "run `ls`" with the default permission mode.
3. Capture:
   ```bash
   herdr agent read <sandbox_pane> --source visible --format text \
     > pkg/agents/testdata/<agent>/<case>.txt
   ```
4. Write `pkg/agents/testdata/<agent>/<case>.golden.json` with the `PendingPrompt` you expect, plus the key map (`{"opt-1":["1"],...}`) and the cancel keys.
5. In the sandbox **only**, try the keys and record which ones worked. Update the ✅/🔍 marks in this file.
6. Close the sandbox pane.

**Naming:** `permission-bash.txt`, `permission-edit.txt`, `question-multiple.txt`, `plan-approval.txt`, `no-menu-working.txt`.
