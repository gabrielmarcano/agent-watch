# Herdr Core (`hc`) Socket API Reference (verified)

Everything here was **probed live against `herdr-core` (`hc`) 0.1.14 and `Herdr.app` (`herdr-app`) 0.9.10** on 2026-10-29 (`hc schema --json`, `hello`, `terminal.list`, `terminal.read`, `terminal.keys`, `events.subscribe`, and error responses). When in doubt, re-derive it with the `herdr-probe` skill ([`.agents/skills/herdr-probe/SKILL.md`](../../.agents/skills/herdr-probe/SKILL.md)). Do not trust memory or blog posts.

> ⚠️ **Safety first.** The `herdr-core` on the development Mac runs the owner's **real** agent sessions: sending `"1"` to a real Claude terminal approves whatever it was asking. Which methods are safe and which are never called on the owner's terminals: [`.agents/rules/herdr-integration.md`](../../.agents/rules/herdr-integration.md) § Safety.

---

## 1. Transport

- **Socket resolution (`herdr.DefaultSocketPath`):**
  1. `$HC_TERMINAL_SOCKET`
  2. `$HERDR_SOCKET_PATH` (if set to the legacy `~/.config/herdr/herdr.sock` path from an older LaunchAgent plist and the `herdr-core` default socket exists on disk, the `herdr-core` socket is preferred)
  3. `${XDG_STATE_HOME:-~/.local/state}/herdr-core/${HC_SESSION:-default}/default.sock`
- **Framing:** newline-delimited JSON-RPC 2.0 (NDJSON). Every request is one JSON object followed by `\n`.
- **Request:**

  ```json
  {"jsonrpc":"2.0","id":"<string>","method":"<method>","params":{...}}
  ```

  `id` is a string.
- **One request per connection** (except `events.subscribe`, which stays open and streams JSON-RPC notifications, §5).
- **Success:** `{"id":"<same id>","jsonrpc":"2.0","result":{...}}`
- **Error:**

  ```json
  {
    "id": "<same id>",
    "jsonrpc": "2.0",
    "error": {
      "code": -32000,
      "message": "no terminal \"t999999\"",
      "data": {
        "kind": "not_found",
        "hint": "run `hc ls` to see terminal addresses and names"
      }
    }
  }
  ```

  `pkg/herdr.Error` unmarshals `error.data.kind` into `Error.Code` (`not_found`, `blocked`, `invalid_params`, `internal`, …) and `error.data.hint` into `Error.Hint`.

The implementation, with its default timeout and cancellation: `Client.Call` in [`pkg/herdr/client.go`](../../pkg/herdr/client.go).

---

## 2. Methods used by this project

| Method | Params | Result | Notes |
|---|---|---|---|
| `hello` | `{}` | `{"version":"0.1.14","generation":1,"framed":false,"features":[...],"methods":[...]}` | Used by `Client.Ping` for health checks |
| `terminal.list` | `{}` | `{"terminals":[Terminal...]}` | Authoritative list of all terminals managed by `herdr-core` |
| `terminal.get` | `{"target":"<address or name>"}` | `Terminal` | Single terminal. Used by the safety guards via `hc get <target> --json` |
| `terminal.read` | `{"target":"<address>","lines"?:int}` | `{"address","title","rows","cols","cursor_row","cursor_col","lines":[...],"text":"..."}` | Plain-text screen read (`lines == 0` reads the visible screen; `lines > 0` reads recent scrollback lines) |
| `terminal.keys` | `{"target":"<address>","keys":[...]}` | `{"address","bytes":int}` | Sends named keys / literal characters (§4) |
| `terminal.send` | `{"target":"<address>","text":"...","enter":true}` | `{"address","bytes":int,"submitted":true,"queued":bool}` | Sends prompt text and presses Enter (`Client.Prompt`) |
| `terminal.run` | `{"argv":[...],"cwd"?,"name"?,"keep":true,"no_parent":true,"no_notify":true,"labels"?:{...}}` | `Terminal` | Starts a new agent terminal (`Client.RunTerminal`). Setting `labels["herdr.space"]` causes `Herdr.app` to adopt it into that space and assign `herdr.tab` and `herdr.pane` |
| `events.subscribe` | `{}` | `{"channel":null,"seq":uint64,"terminals":[Terminal...]}` | Initial ack followed by streaming JSON-RPC notifications (§5) |

**`target`** accepts a terminal `address` (`t2`, `t1w`, …) or `name`. This project always sends `address` (stored in `AgentInfo.PaneID`).

---

## 3. `Terminal` (element of `terminal.list`) and `ToAgentInfo`

Real example (trimmed, values replaced with placeholders):

```json
{
  "address": "t2",
  "id": "00000000000000000000000000000000",
  "name": "agent-watch",
  "argv": ["claude", "--resume", "00000000-0000-0000-0000-000000000000"],
  "cwd": "/Users/me/Code/agent-watch",
  "title": "✳ Migrate bridge to herdr-core",
  "pid": 54192,
  "alive": true,
  "keep": true,
  "parent": null,
  "labels": {
    "herdr.pane": "pn01",
    "herdr.space": "sp01",
    "herdr.tab": "tb01",
    "where": "Personal / agent-watch"
  },
  "foreground": {
    "pid": 54192,
    "name": "2.1.112",
    "argv": ["claude", "--resume", "00000000-0000-0000-0000-000000000000"],
    "cwd": "/Users/me/Code/agent-watch",
    "kind": "claude-code"
  },
  "status": {
    "state": "working",
    "message": "Running tool",
    "turn": 14,
    "status_ms": 1761790000000,
    "output_ms": 1761790000100,
    "input_ms": 1761789995000,
    "exit_code": null
  }
}
```

### How `pkg/herdr` maps `Terminal` to `AgentInfo` (`Terminal.ToAgentInfo`)

- **Filtering (`Client.ListAgents`):**
  - Finished terminals (`status.state == "finished"` or `!alive`) are skipped.
  - Remote terminals (`address` containing `/`, e.g. `remote/t1`) are skipped.
  - Background child terminals (`parent != nil` and no `labels["herdr.space"]`) are not listed as separate top-level panes; instead, live child terminals increment their parent terminal's `SubagentCount`.
  - Non-agent shells (`foreground.kind` empty or `"shell"`, and `argv[0]` not a known agent binary) are skipped.
- **Agent kind normalization:**
  - `claude-code` → `claude`
  - `antigravity` → `agy`
  - `opencode`, `codex`, `pi`, etc. stay as-is.
- **Status mapping:**
  - `status.state`: `idle`, `working`, `blocked`, `done`, `unknown`.
  - `status.message` → `AgentInfo.StatusMsg` (used as the fallback prompt title when a `blocked` terminal has no parseable menu on screen).
- **Change counter (`StateChangeSeq`):**
  - `uint64(*status.status_ms) + status.turn` when `status_ms != nil`, or `status.turn` otherwise.
- **Labels (`Herdr.app` integration):**
  - `labels["herdr.space"]` → `WorkspaceID`
  - `labels["herdr.tab"]` → `TabID`
  - `labels["where"]` (`"<space> / <tab>"`) → split on `" / "` into `WorkspaceLabel` and `TabLabel`. No separate `workspace.list` or `tab.list` polling is needed.
- **Title stripping (`stripLeadingTitleGlyph`):**
  - Strips leading spinner/status glyphs (`✳ `, `● `, etc.) from `title`.
- **Foreground process & session discovery:**
  - `foreground.pid` (or `pid`) → `ForegroundPID`.
  - `foreground.cwd` (or `cwd`) → `ForegroundCWD` / `CWD`.
  - If `foreground.argv` or `argv` carries `--resume <id>`, `-r <id>`, or `--session-id <id>`, `AgentSession` is populated with `Kind: "id", Value: <id>`.

---

## 4. `terminal.keys` key grammar

- **Special keys** (case-insensitive, verified on `hc 0.1.14`): `Enter` (`enter`), `Escape` (`esc`), `Up`, `Down`, `Left`, `Right` (`right`), `Tab`, `Space`, `Backspace` (`BS`), `F1`…`F12`.
- **Single characters:** `"1"`, `"y"`, `"n"`, … are typed literally.
- **Chords** join with `+`: `ctrl+c`, `shift+tab`, `alt+Up`.
- **Order:** multiple keys in one call are applied in order, e.g. `["Right","Enter"]`.
- **An ack only means `herdr-core` accepted the bytes.** It does not prove the TUI reacted.

---

## 5. Events (`events.subscribe`)

Request (keep the connection open afterwards):

```json
{"jsonrpc":"2.0","id":"sub-1","method":"events.subscribe","params":{}}
```

- **Ack line:** `{"id":"sub-1","jsonrpc":"2.0","result":{"channel":null,"seq":1025,"terminals":[...]}}`.
- **Streamed JSON-RPC 2.0 notifications** (no `id` field):
  - `{"jsonrpc":"2.0","method":"event","params":{"seq":1026,"kind":"status",...}}`
  - `{"jsonrpc":"2.0","method":"missed","params":{...}}`
  - `{"jsonrpc":"2.0","method":"heartbeat","params":{...}}` (ignored by `Client.Subscribe`)
- **Global subscription:** `events.subscribe` streams events for all terminals globally; there is no need to re-subscribe when the set of terminals changes.

**Bridge policy:** events are only a **trigger**: on any `"event"` or `"missed"` notification, the bridge schedules a debounced `terminal.list` and diffs the result, and it also polls `terminal.list` periodically (more often while the stream is down). Never build state from event payloads alone. See `Syncer.Run` in [`pkg/herdr/sync.go`](../../pkg/herdr/sync.go).

---

## 6. Errors seen in practice

| `error.data.kind` (`Error.Code`) | When |
|---|---|
| `not_found` | `target` terminal does not exist (`no terminal "t99"`) |
| `blocked` | `terminal.send` on a blocked terminal when refused |
| `invalid_params` | Wrong param name/type or unsupported key name |
| `internal` | Internal `herdr-core` failure |

If the socket cannot be dialed, `herdr-core` is not running (`herdr.ErrUnavailable`). Report `herdr_online=false` and retry with backoff (§7).

---

## 7. Reconnect (bridge policy)

- **Backoff** for dial failures and stream drops: exponential with jitter (values: [`pkg/herdr/clock.go`](../../pkg/herdr/clock.go)).
- **On reconnect:**
  1. `hello` (`Ping`; the bridge then sends `herdr_status` with `herdr_online: true` to the relay)
  2. `terminal.list` (`ListAgents`), diffed against the last known list: the relay gets `agent_update` / `agent_removed` for what changed
  3. `events.subscribe`, then `terminal.list` once more to cover the gap

  A full `snapshot` goes to the relay only when the bridge (re)connects to the relay, or on a relay `resync` ([`contracts.md` §3](contracts.md)).

If a fact here turns out wrong on a newer `herdr-core`, fix this file in the same commit as the code change.
