# Herdr Socket API Reference (verified)

Everything here was **probed live against herdr 0.9.1, socket protocol 22**, on 2026-09-23. When in doubt, re-derive it with the commands in §8. Do not trust memory or blog posts.

> ⚠️ **Safety first.** The herdr on the development Mac runs the owner's **real** agent sessions. **Never** call `agent.prompt`, `agent.send_keys`, `pane.send_*`, `agent.start` or `pane.close` against a pane you did not create yourself for testing. Sending `"1"` to a real Claude pane approves whatever it was asking. Read-only methods (`ping`, `agent.list`, `agent.get`, `agent.read`, `session.snapshot`, `events.subscribe`) are safe. See the `capture-fixture` skill for the sandbox procedure.

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

### Minimal Go call (reference implementation shape)

```go
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
    var d net.Dialer
    conn, err := d.DialContext(ctx, "unix", c.SocketPath)
    if err != nil {
        return fmt.Errorf("herdr dial: %w", err) // treat as herdr offline
    }
    defer conn.Close()
    if dl, ok := ctx.Deadline(); ok {
        _ = conn.SetDeadline(dl)
    }
    if params == nil {
        params = struct{}{} // herdr requires an object, never null
    }
    req := map[string]any{"id": newID(), "method": method, "params": params}
    if err := json.NewEncoder(conn).Encode(req); err != nil { // Encode appends '\n'
        return fmt.Errorf("herdr write: %w", err)
    }
    line, err := bufio.NewReader(conn).ReadBytes('\n')
    if err != nil && len(line) == 0 {
        return fmt.Errorf("herdr read: %w", err)
    }
    var resp struct {
        Result json.RawMessage `json:"result"`
        Error  *Error          `json:"error"`
    }
    if err := json.Unmarshal(line, &resp); err != nil {
        return fmt.Errorf("herdr decode: %w", err)
    }
    if resp.Error != nil {
        return resp.Error // *Error implements error; callers use errors.As
    }
    if out != nil {
        return json.Unmarshal(resp.Result, out)
    }
    return nil
}
```

---

## 2. Methods used by this project

| Method | Params | Result `type` | Notes |
|---|---|---|---|
| `ping` | `{}` | `pong` | `{"version":"0.9.1","protocol":22,"capabilities":{...}}` — use for `hello` and health checks |
| `agent.list` | `{}` | `agent_list` | `{"agents":[AgentInfo...]}` — the authoritative list |
| `agent.get` | `{"target":"<pane_id or name>"}` | — | Single agent |
| `agent.read` | `{"target", "source", "lines"?, "format"?, "strip_ansi"?}` | `pane_read` | `{"read":{"text", "truncated", "revision", "pane_id", ...}}` |
| `agent.send_keys` | `{"target", "keys":[...]}` | — | Key grammar in §4 |
| `agent.prompt` | `{"target", "text", "wait"?}` | — | Never pass `wait` (it blocks); omit it or send `null` |
| `session.snapshot` | `{}` | `session_snapshot` | Workspaces, tabs, panes, agents, `focused_pane_id` |
| `workspace.list` | `{}` | `workspace_list` | `{"workspaces":[{"workspace_id", "number", "label", ...}]}` — the bridge reads `label` for `AgentState.workspace` |
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
| `workspace_id`, `tab_id`, `terminal_id` | string | yes | |
| `agent_status` | enum | yes | `idle` / `working` / `blocked` / `done` / `unknown` |
| `focused` | bool | yes | |
| `revision` | uint64 | yes | **Do not use** as a change detector (stub on some versions) |
| `state_change_seq` | uint64 | default 0 | Increments on every status change — use this |
| `agent` | string\|null | no | Herdr agent id (`claude`, `agy`, `opencode`, …) |
| `display_agent` | string\|null | no | Human name; not used |
| `name` | string\|null | no | User-set pane name |
| `cwd`, `foreground_cwd` | string\|null | no | |
| `terminal_title`, `terminal_title_stripped` | string\|null | no | `_stripped` has the spinner/emoji removed |
| `agent_session` | object\|null | no | See §3.1 |
| `interactive_ready`, `launch_pending`, `screen_detection_skipped` | bool | no | Ignore |
| `state_labels`, `tokens` | map | no | Ignore |

### 3.1 `agent_session`

`{"agent": string, "kind": "id"|"path", "source": string, "value": string}`

- **`kind: "id"`:** `value` is a session id (a UUID for claude and opencode).
- **`kind: "path"`:** `value` is an absolute transcript path (pi; agy reports a transcript path when available).
- **It can be stale.** Herdr keeps the last session any harness announced for the pane. **Only trust it when `agent_session.agent == agent`.**
- **It can be missing.** The agent's herdr integration may not be installed or up to date. Check with `herdr integration status`.

Agent ids known to herdr 0.9.1 (from `server.agent_manifests`): `pi claude codex gemini cursor devin agy cline opencode copilot kimi kiro droid amp grok hermes kilo qodercli qwen letta maki muse`.

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
- **Event lines:** `{"event":"<snake_case>","data":{...}}`.
  - The subscription `type` is dot-form (`pane.agent_status_changed`).
  - The streamed `event` is snake_case (`pane_agent_status_changed`).
- **`pane.agent_status_changed` REQUIRES `pane_id`.** Omitting it fails with ``invalid_request: missing field `pane_id` ``. So you need one subscription per agent pane, and you must **re-subscribe** (close and reopen the stream with the new list) whenever the set of agent panes changes.
- **Global events** (no `pane_id` needed): `pane.created`, `pane.closed`, `pane.exited`, `pane.agent_detected`, `pane.focused`, `workspace.*`, `tab.*`.
- **`pane_agent_status_changed` payload:** `{pane_id, workspace_id, agent_status, agent?, display_agent?, title?, state_labels}`.
- **`pane_agent_detected` bursts:** it can fire for the whole herd at once, so debounce it.
- **An empty `subscriptions` array** gets an ack but never any events.

**Project rule:** events are only a **trigger**. On any event, schedule a debounced (150 ms) `agent.list` and diff the result. Never build state from event payloads alone. Also poll `agent.list` every 15 s while the stream is healthy, and every 2 s while it is down.

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

## 7. Reconnect policy

- **Backoff** for dial failures and stream drops: 500 ms, 1 s, 2 s, 4 s … up to 30 s, each ±20 % jitter.
- **On reconnect:**
  1. `ping`
  2. `agent.list`
  3. re-subscribe
  4. send a full `snapshot` to the relay

---

## 8. Re-verifying this document

```bash
herdr --version                                 # server version
herdr api schema --json > /tmp/herdr-schema.json  # full machine-readable schema
herdr agent list                                # live agents (read-only)
herdr agent read <pane_id> --source visible --format text   # read-only
herdr integration status                        # which agent integrations report sessions
```

To probe a single socket method without writing Go (read-only methods only):

```bash
python3 - <<'EOF'
import json, os, socket
s = socket.socket(socket.AF_UNIX); s.connect(os.path.expanduser('~/.config/herdr/herdr.sock'))
s.sendall((json.dumps({"id": "p1", "method": "ping", "params": {}}) + "\n").encode())
print(s.makefile().readline())
EOF
```

If a fact here turns out wrong on a newer herdr, fix this file in the same commit as the code change.
