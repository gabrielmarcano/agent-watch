# Agent Knowledge Reference (for `pkg/agents`)

Everything that depends on **which** coding agent runs in a pane lives in `pkg/agents` and is documented here:

- how its approval menu looks
- which keys answer it
- how to cancel it
- where its transcript lives
- how to read the last turn from that transcript

The rest of the system (`pkg/herdr`, the relay, the clients) must stay agent-agnostic.

**Status legend:**
- ✅ verified on the development Mac on 2026-09-23; rows marked (2026-09-25) were re-captured with Claude Code 2.1.282, Antigravity CLI 1.2.10, OpenCode 1.18.32 and herdr 0.9.1
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

```go
// Optional, for adapters whose answer keys act on the focused button.
type FocusGuard interface {
    FocusDependent(p Prompt, optionID string) bool                 // must focus be checked before these keys?
    CheckFocus(ansiScreen string, p Prompt, optionID string) error // screen = agent.read visible/ansi
}
```

- **Registry:** an exact match on herdr's `agent` field. Anything not registered uses `generic`.
- **Focus guard:** only `opencode` implements it (§5.1). For an `answer`, after the fresh text parse, the fingerprint check and the key lookup, the bridge reads the screen with `format: "ansi"` when `FocusDependent` is true and calls `CheckFocus`. An error means: send nothing, reply `prompt_changed`, leave the prompt unclaimed.
- **Overrides:** adapters embed the generic behaviour and override only what differs.
- **Missing transcript:** `LastTurn` returns `ErrNoTranscript` when it cannot find or read one. The caller then falls back to a screen capture (§6).

---

## 2. Generic behaviour (all agents unless overridden)

| Aspect | Generic rule |
|---|---|
| Menu detection | The **last** block of ≥ 2 consecutive lines matching `^(?:[❯›>▶●]\s*)?(\d+)[.)]\s+(.+?)\s*$` on the visible screen, after stripping indentation and vertical borders (`│ ┃ ║`) from both ends, so `  │ 1. Yes │` and `  ┃  1. red` match. Up to 2 continuation lines (no number) may follow an option; they are its `description` (joined with spaces), and roles are derived from label + description. A continuation line starts at the label's column (up to 4 deeper); a line at the options' own indentation (a footer such as `  Esc to cancel`) ends the block. Key hints under an option (`shift+tab to …`, `ctrl+…`, `esc to …`) are skipped, not appended. **The adapters for claude, agy and opencode also require the block to be the open dialog** (§3.1, §4.1, §5.1): a numbered list in an answer is not a menu |
| Cursor row | The option line that starts with a marker `❯ › > ▶ ●`. If none has one, assume the first |
| Title | The nearest non-empty line above the menu block that is not a box-drawing line (`─│╭╮╰╯┌┐└┘`), with box characters and surrounding whitespace trimmed |
| Detail | Non-empty lines between the title and the menu, joined with `\n`, trimmed, max 400 characters (runes; never cut inside a UTF-8 sequence) |
| Role by label | lowercase the label and turn typographic apostrophes (`’`) into `'`, then: contains `don't ask again`, `always`, `for this session`, `all edits`, `for all` → `allow_always`; else starts with `yes` or contains `allow`, `approve`, `proceed`, `accept` → `allow_once`; else starts with `no` or contains `deny`, `reject`, `cancel`, `decline` → `deny`; else `choice` |
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
| Dialog detection | ✅ (2026-09-25) | While a dialog is open Claude hides its input box (`❯` between two rules). A numbered block counts as a menu only if at most 6 non-empty lines follow it and none of them is the input line (`❯` alone or `❯ ` + text). Negative fixtures: `no-menu-idle-numbered-list.txt` (idle), `no-menu-working-numbered-list.txt` (working). Positive with a numbered list and numbered diff lines above the dialog: `permission-write-numbered-list.txt` |
| Typographic apostrophe | ✅ (2026-09-25) | Some menus write `Yes, and don’t ask again for: mv *` with U+2019 (`permission-bash-dont-ask-again.txt`). It is `allow_always` |
| WebFetch | ✅ (2026-09-25) | Title `Fetch`, detail = `url: …`, `prompt: …` and `Claude wants to fetch content from <domain>`; options `Yes` / `Yes, and don't ask again for <domain>` / `No, and tell Claude what to do differently (esc)`; no footer line. **herdr 0.9.1 reported the pane `done`, not `blocked`, with this dialog open** (`herdr agent explain`: rule `live_prompt_box` matched a stale shell prompt in scrollback). Digit 3 denied. `permission-webfetch.txt` |
| AskUserQuestion | ✅ | Numbered menu with digit selection; the line under each option is its `description`. `Type something.` (a free-text field) is left out of the options; the others keep their ids. Mapped to `kind: "question"`. Captured in `question-multiple.txt` and, asked in plan mode, `question-plan-mode.txt` (captured in Phase 0 as `plan-approval.txt`) |
| Plan approval (ExitPlanMode) | ✅ (2026-09-25) | `Claude has written up a plan and is ready to execute. Would you like to proceed?` with `1. Yes, and use auto mode` (`allow_always`), `2. Yes, manually approve edits` (`allow_once`), `3. Tell Claude what to change` (`choice`; its hint line `shift+tab to approve with this feedback` is not part of the label). No deny option → `kind: "question"`, Deny uses `esc`. The plan's own numbered steps above the dialog are not the menu. Digit 2 approved immediately. `plan-approval.txt` |
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
6. `LastTurn` fills only `query`, `response` and `source`. The bridge computes `HistoryItem.id` with `SessionRef.Value` as `session_value` (the UUID for `kind="id"`, the path for `kind="path"`).

---

## 4. Antigravity CLI (`agy`)

### 4.1 Menus

| Item | Status | Value |
|---|---|---|
| Approval menu | ✅ | Numbered vertical list inside a box. Header indicates tool/kind (e.g. `Command`, `Pending edit`). Captured in `permission-bash.txt` and `permission-edit.txt`. Multiple-choice and plan approval are unsupported (`.missing.md`). |
| Dialog detection | ✅ (2026-09-25) | The dialog replaces the input box (`>` between two rules); its tail is `↑/↓ Navigate · …` and `esc to cancel`. Same rule as Claude with `>` as the input line. Negative: `no-menu-idle-numbered-list.txt`; `no-menu-working.txt` is now captured while really working (`Generating...` spinner, herdr `working`) |
| herdr status with the dialog open | ✅ (2026-09-25) | **herdr 0.9.1 does not report `blocked` for agy.** With the permission dialog open it reported `done` (`permission-bash-herdr-done.txt`; `agent explain`: state idle, rule none, `default_known_agent_idle_fallback`), and `working` when a background task was running (`permission-bash-herdr-working.txt`). The bridge refuses dictation whenever `ParsePrompt` finds a menu, so these screens must parse |
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
2. **Query** = the last `USER_INPUT` step's `content` inside `<USER_REQUEST>…</USER_REQUEST>`, trimmed. Antigravity 1.2.x appends `<ADDITIONAL_METADATA>` (local time) and sometimes `<USER_SETTINGS_CHANGE>` after it; content without the wrapper is used as is.
3. **Response** = `content` of the last `PLANNER_RESPONSE` after that query that has a non-empty `content` and an empty or missing `tool_calls`. `PLANNER_RESPONSE` steps with tool calls have no `content`.
4. If there is no such response, return `ErrNoTranscript`.

---

## 5. OpenCode (`opencode`)

### 5.1 Menus

| Item | Status | Value |
|---|---|---|
| Approval UI | ✅ (2026-09-25) | Drawn inside a `┃` frame at the bottom, in place of the input box (which ends with a `╹▀▀▀` line). `△ Permission required`, then `<icon> <action>`, a body, and a horizontal button bar `Allow once   Allow always   Reject` (footer `ctrl+f fullscreen  ⇆ select  enter confirm`). Icons/actions (from the 1.18.32 source; the first, second and fourth captured): `# Shell command` + `$ <command>`, `→ Edit <file>` + a diff, `→ Read <file>`, `← Access external directory <dir>` + `Patterns` / `- <glob>`, `% WebFetch <url>`, `✱ Glob/Grep "<pattern>"`, `⚙ Call tool <name>`. Detail = action, then any `$ …` / `Path: …` line, then `Patterns: …`. Captured: `permission-bash.txt`, `permission-edit.txt`, `permission-external-directory.txt` |
| Dialog detection | ✅ (2026-09-25) | Only the framed dialog counts: the button bar (or question footer) must be in the `┃` frame with no `╹` input-box line below it. There is **no** generic numbered fallback (it matched numbered lists in answers: `no-menu-idle-numbered-list.txt`) |
| Keys | ✅ (2026-09-25) | Buttons, not digits. Bindings (from the 1.18.32 source, checked live): Left/`h` previous, Right/`l` next, **both wrap** (Left on `Allow once` goes to `Reject`); Enter selects; esc = Reject. herdr has no Home/End keys (`invalid_key`), and there are no per-option shortcuts. **No key sequence reaches an allow button whatever the focus**, so keys assume the focus OpenCode sets when the dialog mounts: `Allow once` (reset on every new request and when coming back from the confirm stage; verified). `Allow once` = `["Enter"]`. `Allow always` opens a second stage (`△ Always allow`, `This will allow the following patterns until OpenCode is restarted`, `- <pattern>`, buttons `Confirm   Cancel`, Confirm focused), so it is `["Right", "Enter", "Enter"]` (`Right, Enter` alone left the dialog on that stage). `Reject` = `["esc"]`, focus-independent. If someone moved the focus on the Mac (arrow keys or mouse hover) before the watch answers, `Enter` would act on that button: the focus check (next row) refuses the answer instead |
| Focus check | ✅ (2026-09-25) | Before `["Enter"]` or `["Right", "Enter", "Enter"]`, the bridge reads the visible screen with `format: "ansi"` (herdr-socket-api.md §2.1) and `CheckFocus` verifies the focus is on the button the keys assume: `Allow once` on the first stage, `Confirm` on the always stage.<br>**Rule** (1.18.32 `permission.tsx`): the focused button's background is `theme.warning`; the `┃` framing the button row and the `△` before the title are drawn with foreground `theme.warning`. The focused button is the one whose background equals that foreground. Colours are compared as herdr gives them (truecolor `38;2`/`48;2`, palette `38;5`/`48;5`); no fixed value is assumed (`245;167;66` is only the default theme's). In the source, `warning` differs from the unfocused button background (`backgroundMenu`, falling back to `backgroundElement`) in all 33 bundled themes, dark and light.<br>**Fail closed:** focus elsewhere, zero or two buttons in the accent colour, `┃` and `△` in different colours, a label missing or repeated on the row, inverse video, a different dialog than the text read showed, or anything in the read but printable text and SGR → `prompt_changed`, no keys sent, the prompt not consumed. The owner answers on the Mac, or the watch retries once the focus is back.<br>**Not checked:** `["esc"]` (Reject, Cancel) and the question tool's digits act whatever the focus, so they cost no extra read.<br>**Narrow terminals:** below 80 columns the source keeps the buttons on one row and moves only the hints below it (not captured). If the buttons ever sit on separate rows, the check refuses.<br>**Limit:** a focus change in the milliseconds between the ansi read and the key press is not caught.<br>Captures: `focus/<theme>-<stage>-<focused>.ansi` for the `opencode` (default), `tokyonight`, `lucent-orng` and `system` themes, focus on each button of both stages. The rule is checked against these live captures; the bridge's refusal path only against `pkg/herdrtest`, not end to end on the Mac |
| Always-allow stage | ✅ (2026-09-25) | Parsed as its own prompt: `Confirm` (`allow_always`, `["Enter"]`), `Cancel` (`deny` by label, `["esc"]`). Its esc goes back to the first stage; it does not reject. `permission-always-confirm.txt` |
| Question tool | ✅ (2026-09-25) | Exists (the Phase 0 `.missing.md` was wrong). Framed numbered list, description under each option (the option's `description`), last option `Type your own answer` (a free-text field, left out of the options), footer `↑↓ select  enter submit  esc dismiss`. Digits 1–9 pick an answer; a single question is submitted at once (digit 2 answered). esc dismisses (rejects) it. `question-multiple.txt` |
| Cancel | ✅ | `esc` rejects the permission request / dismisses the question. For a sub-agent's permission, Reject opens a `Reject permission` stage with a text box (from the source; not captured). |
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
2. **Response message** = the newest message whose `data.role == "assistant"`, that is newer than the query message and that has text. OpenCode stores one assistant message per step; earlier steps are tool calls, with or without a line of text before them (`session-multistep.json`).
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
3. Drop the agent's input box: cut everything from the last run of lines that contain only box-drawing characters (light or heavy: `─ │ ┃ ╹ ▀ …`), a prompt marker (`❯`, `>`) or a `┃` frame downwards, if found within the last 15 lines. The status lines under the box go with it.
4. **Last turn only** when the adapter recognises the user's message (`ScreenTurnReader`): the message becomes `query` and only what follows it the `response`, dedented:
   - **claude:** the message is echoed as `❯ text` (wrapped lines indented by 2); `⏺` marks the reply; the `✻ Worked for …` status line is dropped.
   - **agy:** the message is echoed as `> text` (wrapped lines indented by 2).
   - **opencode:** the message is the last `┃`-framed block; the `Thought · …` line and the `▣  <mode> · <model> · <time>` footer are dropped, and so is the sidebar on the right (text after a gap of 4+ spaces, or starting at column 40 or further).
   Otherwise the whole screen above the input box is kept.
5. Keep the last 80 lines.
6. `HistoryItem{Source: "screen", Query: <message or "">, Response: <text>}`.

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

**Naming:** `permission-bash.txt`, `permission-edit.txt`, `question-multiple.txt`, `plan-approval.txt`, `no-menu-working.txt`. Extra cases use a descriptive suffix (`permission-webfetch.txt`, `permission-bash-herdr-done.txt`, `no-menu-idle-numbered-list.txt`). Record the herdr status you observed in the golden's `herdr_status` and `notes`.
