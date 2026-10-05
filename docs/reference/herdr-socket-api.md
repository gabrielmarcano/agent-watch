# Herdr Socket API Reference (verified)

Everything here was **probed live against herdr 0.9.1, socket protocol 22**, on 2026-09-23, and re-probed on 2026-10-05 after the upgrade to herdr 0.9.3: the running server was still 0.9.1 (a server only changes version when it restarts), and the 0.9.3 schema (`herdr api schema`) keeps protocol 22 and every method, param and field used here. When in doubt, re-derive it with the `herdr-probe` skill (`.agents/skills/herdr-probe/SKILL.md`). Do not trust memory or blog posts.

> ⚠️ **Safety first.** The herdr on the development Mac runs the owner's **real** agent sessions: sending `"1"` to a real Claude pane approves whatever it was asking. Which methods are safe and which are never called on the owner's panes: `.agents/rules/herdr-integration.md` § Safety.

---

## 1. Transport

- **Socket:** `$HERDR_SOCKET_PATH`. If unset, fall back to `~/.config/herdr/herdr.sock`. Do **not** use `~/.config/herdr.sock`: that path is wrong.
- **Framing:** newline-delimited JSON (NDJSON). Every request is one JSON object followed by `\n`.
- **Request:**

  ```json
  {"id":"<string>","method":"<method>","params":{...}}
  ```

  `id` **must be a string**. A number makes herdr reject the request with `invalid_request`.

- **One request per connection.**
  1. Dial.
  2. Write one request line.
  3. Read one response line.
  4. Close.

  The server closes the connection after replying; a second request on the same connection never gets an answer.
- **Exception:** `events.subscribe` keeps the connection open and streams events (§5).
- **Success:** `{"id":"<same id>","result":{"type":"<result type>", ...}}`
- **Error:** `{"id":"<same id or empty>","error":{"code":"<code>","message":"<text>"}}`. For malformed requests, `id` is `""` and `code` is `invalid_request`. The message names the offending field; use it to debug.

The implementation, with its default timeout and cancellation: `Client.Call` in `pkg/herdr/client.go`.

---

## 2. Methods used by this project

| Method | Params | Result `type` | Notes |
|---|---|---|---|
| `ping` | `{}` | `pong` | `{"version":"0.9.1","protocol":22,"capabilities":{...}}` — use for `hello` and health checks |
| `agent.list` | `{}` | `agent_list` | `{"agents":[AgentInfo...]}` — the authoritative list |
| `agent.get` | `{"target":"<pane_id or name>"}` | — | Single agent. Not called by the bridge; the guards use its CLI form |
| `agent.read` | `{"target", "source", "lines"?, "format"?, "strip_ansi"?}` | `pane_read` | `{"read":{"text", "truncated", "revision", "pane_id", ...}}` |
| `agent.send_keys` | `{"target", "keys":[...]}` | — | Key grammar in §4 |
| `agent.prompt` | `{"target", "text", "wait"?}` | — | Never pass `wait` (it blocks); omit it or send `null` |
| `session.snapshot` | `{}` | `session_snapshot` | Workspaces, tabs, panes, agents, `focused_pane_id`. Not called by the bridge; handy when probing |
| `workspace.list` | `{}` | `workspace_list` | `{"workspaces":[{"workspace_id", "number", "label", ...}]}` — the bridge reads `label` for `AgentState.workspace` |
| `tab.list` | `{}` (or `{"workspace_id"}`) | `tab_list` | `{"tabs":[{"tab_id", "workspace_id", "number", "label", "pane_count", ...}]}`, every workspace's tabs without params. A tab's default `label` is its `number`. The bridge reads `label` for `AgentState.label` (contracts §1.2) |
| `notification.show` | `{"title", "body"?, "sound"?: none\|done\|request}` | — | Used by `pair`, because a plugin action's stdout may not be visible (`cmd/bridge/pair.go`) |
| `events.subscribe` | `{"subscriptions":[...]}` | `subscription_started` | Streaming, §5 |

**`target`** accepts a `pane_id` (preferred, always unique) or an agent `name`. This project always sends `pane_id`.

### 2.1 `agent.read` sources — note the underscore

`source` ∈ `visible` | `recent` | `recent_unwrapped` | `detection`.

> ⚠️ The **socket** uses `recent_unwrapped` (underscore). The **CLI** flag spells it `recent-unwrapped` (hyphen). Sending the hyphen form to the socket fails with `invalid_request: unknown variant`. Verified.

`format` ∈ `text` | `ansi`, default `text`. The result echoes it in `read.format`.

- **`text`** is plain text without escape codes, one line per row, trailing blanks trimmed. Everything that parses a screen uses it: `Client.Read`.
- **`ansi`** is the same screen with its styling, and only the OpenCode focus check uses it (`Client.ReadANSI`; agents.md §5.1). A text screen cannot show which button has focus; colours can.
  - Every styled span starts with `ESC[0m`, then one SGR sequence per attribute: flags (`ESC[1m`, `ESC[7m` …), then `ESC[38;…m` (fg), `ESC[48;…m` (bg), `ESC[58;…m` (underline colour). A colour is `2;r;g;b` (truecolor) or `5;n` (palette index).
  - Rows end with `\r\n`, not `\n`, and keep their styled trailing blanks.
  - No cursor moves, OSC or other sequences with the default formatter (libghostty-vt's VT formatter, herdr 0.9.1 source).
  - `strip_ansi` is ignored by `agent.read` (in the 0.9.1 source only output waits and output subscriptions read it). `ReadANSI` still sends `false`, exactly what `herdr agent read --format ansi` sends.
  - `ReadANSI` rejects a result whose `read.format` is not `ansi`: plain text there would read as a screen with no colours.

| Use | Params |
|---|---|
| Parse a blocked menu | `{"target": pane, "source": "visible", "format": "text"}` |
| Screen-capture history fallback | `{"target": pane, "source": "recent_unwrapped", "lines": 200, "format": "text"}` |
| Check which button has focus (OpenCode answers) | `{"target": pane, "source": "visible", "format": "ansi", "strip_ansi": false}` |

---

## 3. `AgentInfo` (element of `agent.list`)

Real example (trimmed, values replaced with placeholders):

```json
{
  "pane_id": "w5:pAE", "tab_id": "w5:tA9", "workspace_id": "w5", "terminal_id": "term_00000000000000",
  "agent": "claude", "agent_status": "done", "name": "my-app", "focused": false,
  "cwd": "/Users/me/Code/app", "foreground_cwd": "/Users/me/Code/app",
  "terminal_title": "✳ Add a settings page", "terminal_title_stripped": "Add a settings page",
  "interactive_ready": true, "revision": 3, "state_change_seq": 333,
  "agent_session": { "agent": "claude", "kind": "id", "source": "herdr:claude",
                     "value": "00000000-0000-0000-0000-000000000000" }
}
```

| Field | Type | Required | Use |
|---|---|---|---|
| `pane_id` | string | yes | **Primary key** |
| `workspace_id`, `tab_id`, `terminal_id` | string | yes | `tab_id` joins the agent to its tab's label (`tab.list`) |
| `agent_status` | enum | yes | `idle` / `working` / `blocked` / `done` / `unknown` |
| `focused` | bool | yes | |
| `revision` | uint64 | yes | **Do not use** as a change detector (stub on some versions) |
| `state_change_seq` | uint64 | default 0 | Increments on every status change — use this |
| `agent` | string\|null | no | Herdr agent id (`claude`, `agy`, `opencode`, …) |
| `display_agent` | string\|null | no | Human name; not used |
| `name` | string\|null | no | User-set pane name; `AgentState.name`, and the label's first choice |
| `cwd`, `foreground_cwd` | string\|null | no | |
| `terminal_title`, `terminal_title_stripped` | string\|null | no | `_stripped` has the spinner/emoji removed; `AgentState.title` |
| `agent_session` | object\|null | no | See §3.1 |
| `interactive_ready`, `launch_pending`, `screen_detection_skipped` | bool | no | Ignore |
| `state_labels`, `tokens` | map | no | Ignore |
| `completion_seq`, `title` | uint64, string | no | In the 0.9.3 schema only; a 0.9.1 server does not send them. Not used |

### 3.1 `agent_session`

`{"agent": string, "kind": "id"|"path", "source": string, "value": string}`

- **`kind: "id"`:** `value` is a session id (a UUID for claude, `ses_…` for opencode).
- **`kind: "path"`:** `value` is an absolute transcript path (pi; agy reports a transcript path when available).
- **It can be stale.** Herdr keeps the last session any harness announced for the pane. **Only trust it when `agent_session.agent == agent`.** Even then it can name another conversation of the same agent than the one on screen (Claude's agents view: `agents.md` §3.3).
- **It can be missing.** The agent's herdr integration may not be installed or up to date. Check with `herdr integration status`.

The agent ids herdr knows are the `kinds:` line of `herdr agent` (its help output).

---

## 4. `agent.send_keys` key grammar

- **Special keys** (case-insensitive): `Enter`, `Escape` (alias `esc`), `Up`, `Down`, `Left`, `Right`, `Tab`, `Space`, `Backspace` (alias `BS`), `F1`…`F12`.
- **Single characters:** `"1"`, `"y"`, `"n"`, … are typed literally.
- **Chords** join with `+`: `ctrl+c`, `shift+tab`, `alt+Up`. Modifiers: `ctrl`, `shift`, `alt`, `cmd`, `super`.
- **Not supported** (they return `invalid_key`): `PageUp`, `PageDown`, `Home`, `End`, `Insert`, `Delete`, and tmux syntax such as `C-c`.
- **Order:** multiple keys in one call are applied in order, e.g. `["Down","Down","Enter"]`.
- **An ack only means herdr accepted the bytes.** It does not prove the TUI reacted. To confirm, re-read the pane.

---

## 5. Events (`events.subscribe`)

Request (keep the connection open afterwards):

```json
{"id":"sub-1","method":"events.subscribe","params":{"subscriptions":[
  {"type":"pane.created"},
  {"type":"pane.closed"},
  {"type":"pane.exited"},
  {"type":"pane.agent_detected"},
  {"type":"pane.agent_status_changed","pane_id":"w5:pAW"},
  {"type":"pane.agent_status_changed","pane_id":"w5:pAE"}
]}}
```

- **Ack line:** `{"id":"sub-1","result":{"type":"subscription_started"}}`. This was verified.
- **Event lines:** `{"event":"<name>","data":{...}}`.
  - The subscription `type` is dot-form (`pane.agent_status_changed`).
  - The streamed `event` name is not consistent (re-probed 2026-10-05): status changes stream as `pane.agent_status_changed` (dot form), `pane.created` streams as `pane_created` (snake_case, `data.pane` holds the pane). The bridge never branches on the name.
- **`pane.agent_status_changed` REQUIRES `pane_id`.** Omitting it fails with ``invalid_request: missing field `pane_id` ``. So you need one subscription per agent pane, and you must **re-subscribe** (close and reopen the stream with the new list) whenever the set of agent panes changes.
- **Global events** (no `pane_id` needed): `pane.created`, `pane.closed`, `pane.exited`, `pane.agent_detected`, `pane.focused`, `workspace.*`, `tab.*`.
- **Status change payload:** `{pane_id, workspace_id, agent_status, agent?, display_agent?, title?, state_labels?}`; empty optional fields are left out (a claude pane sent only `pane_id`, `workspace_id`, `agent_status`, `agent`).
- **`pane.agent_detected` bursts:** it can fire for the whole herd at once, so debounce it.
- **An empty `subscriptions` array** gets an ack but never any events.

**Bridge policy:** events are only a **trigger**: on any event the bridge schedules a debounced `agent.list` and diffs the result, and it also polls `agent.list`, more often while the stream is down. Never build state from event payloads alone. The loop and its values: `Syncer.Run` in `pkg/herdr/sync.go`.

---

## 6. Errors seen in practice

| Code | When |
|---|---|
| `invalid_request` | Malformed JSON, wrong param name or type, unknown enum variant, numeric `id` |
| `agent_not_found` | `target` does not exist (`"agent target w0:x not found"`) |
| `agent_blocked` | `agent.prompt` on a blocked agent — nothing was typed |
| `invalid_key` | Unsupported key name in `send_keys` |

If the socket cannot be dialed, herdr is not running. Report `herdr_online=false` and retry with backoff (§7).

---

## 7. Reconnect (bridge policy)

- **Backoff** for dial failures and stream drops: exponential with jitter (values: `pkg/herdr/clock.go`).
- **On reconnect:**
  1. `ping` (the bridge then sends `herdr_status` with `herdr_online: true` to the relay)
  2. `agent.list`, diffed against the last known list: the relay gets `agent_update` / `agent_removed` for what changed
  3. re-subscribe, then `agent.list` once more to cover the gap

  A full `snapshot` goes to the relay only when the bridge (re)connects to the relay, or on a relay `resync` (`contracts.md` §3).

If a fact here turns out wrong on a newer herdr, fix this file in the same commit as the code change.
