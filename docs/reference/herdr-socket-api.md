# Herdr Core (`hc`) Socket API Reference (verified)

Everything here was **probed live against `herdr-core` (`hc`) 0.1.14 and `Herdr.app` (`herdr-app`) 0.9.10** on 2026-10-30: the OpenRPC document (`hc schema --json`), the manual (`hc --skill`), and read-only calls (`hello`, `terminal.list`, `terminal.read`, `events.subscribe`, error responses). The key grammar (§4) was probed on a sandbox terminal labelled `aw=sandbox`. When in doubt, re-derive it with the `herdr-probe` skill ([`.agents/skills/herdr-probe/SKILL.md`](../../.agents/skills/herdr-probe/SKILL.md)). Do not trust memory or blog posts.

> ⚠️ **Safety first.** The `herdr-core` on the development Mac runs the owner's **real** agent sessions: sending `"1"` to a real Claude terminal approves whatever it was asking. Which methods are safe and which are never called on the owner's terminals: [`.agents/rules/herdr-integration.md`](../../.agents/rules/herdr-integration.md) § Safety.

---

## 1. Transport

- **Socket resolution (`herdr.DefaultSocketPath`):**
  1. `$HC_TERMINAL_SOCKET`
  2. `${XDG_STATE_HOME:-~/.local/state}/herdr-core/${HC_SESSION:-default}/default.sock`
  3. `$HERDR_SOCKET_PATH`, when a variable of that (legacy) name is set anyway. A value that is the old `~/.config/herdr/herdr.sock` is *ignored* while the socket of 2 exists, so an old LaunchAgent plist cannot point the bridge at nothing.
- **Framing:** newline-delimited JSON-RPC 2.0 (NDJSON). Every request is one JSON object followed by `\n`.
- **Request:**

  ```json
  {"jsonrpc":"2.0","id":"<string>","method":"<method>","params":{...}}
  ```

  `id` is a string. The core echoes it back; a reply with another `id` is an error (`checkResponseID`).
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

  `pkg/herdr.Error` unmarshals `error.data.kind` into `Error.Code` and `error.data.hint` into `Error.Hint`, and still accepts an error object whose `code` is the string itself. Every call has a deadline: `Client.Timeout` (5 s by default) or the earlier of it and the caller's context ([`pkg/herdr/client.go`](../../pkg/herdr/client.go)).

---

## 2. Methods used by this project

| Method | Params sent | Result | Notes |
|---|---|---|---|
| `hello` | `{}` | `{"version","generation","core":{"id","name"},"framed","methods"[],"features"[]}` | `Client.Ping`: health check; `generation` becomes `Pong.Protocol`, `version` is what the relay's `hello.herdr_version` reports |
| `terminal.list` | `{}` | `{"terminals":[Terminal…],"hidden_finished":int,"machines":[MachineView…]}` | Authoritative, live-only list: the finished ones are left out unless `all` is true. `Address` is `t7` on this core and `machine/t7` for another |
| `terminal.get` | `{"terminal":"<address, name or machine/t7>"}` | `Terminal` | Single terminal, live or finished. Used by the safety guards via `hc get <target> --json` |
| `terminal.read` | `{"terminal":"<address>","lines"?:int}` | `{"text","complete","how","why"}` | The screen plus `lines` of history above it, as text (`how` is `screen`, `history` or `stitched`; `why` says when only the screen was read). §2.1 |
| `terminal.keys` | `{"terminal":"<address>","keys":[...]}` | `{"status","turn"}` | Presses named keys (§4) |
| `terminal.send` | `{"terminal":"<address>","text":"..."}` | `{"started","status","turn","wait"}` | Pastes the text and presses Enter, once nobody has typed for ~3 s. **Refuses a `blocked` program** (`blocked`), unless `force` is set — the bridge never sets it |
| `terminal.run` | `{"argv","cwd","name","keep","no_parent","no_notify","labels"}` | `Terminal` | Starts a new agent terminal (`Client.RunTerminal`). Setting `labels["herdr.space"]` makes `Herdr.app` adopt it into that space and assign `herdr.tab` and `herdr.pane` |
| `events.subscribe` | `{}` | `{"channel","seq","terminals":[Terminal…]}` | Ack with a snapshot, then notifications (§5). The core also accepts `terminal`, `children_of` and `labels` to narrow the stream; the bridge takes the global one |

**`terminal`** accepts an `address` (`t2`, `t26`, …), a `name`, or `machine/t7`. This project always sends the `address`, stored in `AgentInfo.PaneID`. The parameter is named `terminal`, not `target`.

### 2.1 Reads the bridge makes

`Client.Read` maps our two read sources onto `lines`:

| Source | `lines` | Used for |
|---|---|---|
| `SourceVisible` | omitted (0: the screen only) | prompt parsing and the safety re-read before a key press |
| `SourceRecentUnwrapped` | 120 | the history screen capture (`pkg/bridge/engine.go` asks for 200) |

The bridge uses only `text`; `complete`, `how` and `why` are informational and ignored. A full-screen app is not scrolled while it works, so during a turn such a read may return just the screen — the bridge's own budgets and fallbacks live in [`agents.md` §6](agents.md).

---

## 3. `Terminal` (element of `terminal.list`) and `ToAgentInfo`

Real fields (`hc schema`, trimmed; values replaced with placeholders):

```json
{
  "address": "t2",
  "name": "agent-watch",
  "title": "✳ Migrate the bridge to herdr-core",
  "state": "live",
  "cwd": "/Users/me/Code/agent-watch",
  "argv": ["claude", "--resume", "00000000-0000-0000-0000-000000000000"],
  "pid": 54192,
  "parent": null,
  "labels": {
    "herdr.pane": "pn01",
    "herdr.space": "sp01",
    "herdr.tab": "tb01",
    "where": "Personal / agent-watch"
  },
  "foreground": {
    "pid": 54192,
    "name": "node",
    "argv": ["claude", "--resume", "00000000-0000-0000-0000-000000000000"],
    "cwd": "/Users/me/Code/agent-watch"
  },
  "status": {
    "app": "claude-code",
    "state": "working",
    "kind": null,
    "msg": "Running tool",
    "title": "",
    "progress": null
  },
  "status_ms": 1761790000000,
  "turn": 14,
  "reported": true,
  "screen": "exact",
  "cols": 200,
  "rows": 50,
  "created_ms": 1761789000000
}
```

- `state` is `live` or `finished`. A finished one has `exit_code`, `finished_ms`, `reason` (`exited`, `killed`, `server_stop`, …), `signal`, and no `foreground`.
- `status` is the program's own OSC 7501 report, `null` when it never reported or has exited. `status_ms` is when that word last changed; `turn` rises each time the status leaves `working`.
- There is **no** `focused`, `agent_session`, `subagent_count`, `background_shell_count` or `background_monitor_count` in the core's `Terminal`. (`pkg/herdr.Terminal` still decodes those keys: `AgentSession`, `Subagents`, `BgShells`, `BgMonitors` are fixture overrides for `pkg/herdrtest`, and `Focused` is only ever what the core sends, i.e. false.)

### How `pkg/herdr` maps `Terminal` to `AgentInfo` (`Terminal.ToAgentInfo`)

- **Filtering (`Client.ListAgents`):**
  - Remote terminals (`address` containing `/`, e.g. `machine/t7`) are skipped.
  - Terminals whose `state` is neither empty nor `live` are skipped (finished ones).
  - Background child terminals (`parent` set and no `labels["herdr.space"]`, e.g. `hc run --bg` children) are not listed as panes; their live count becomes the parent's `SubagentCount`.
- **Agent detection (`detectAgent`):** the first of `status.app`, the first non-flag token of `foreground.argv`, `foreground.name`, `argv` that names an agent. Normalisation: `claude-code`/`claude` → `claude`, `antigravity`/`agy` → `agy`, `opencode`/`codex`/`pi`/… stay as they are; shells (`zsh`, `bash`, `sh`, `fish`, `dash`, `ksh`, `tcsh`, `csh`), `tmux`, `hc`, `herdr-core`, `env`, `login`, `sleep`, version-like names (starting with a digit) and the empty string name no agent, so the pane is not tracked.
- **Status mapping:** `status.state` (`idle`, `working`, `blocked`, `done`, `error`), with `error` published as `unknown`. A terminal with no `status` report is `idle` when an agent was detected and `unknown` otherwise (a plain shell stays out of the list). `status.msg`, `status.title` and `status.kind` go to `StatusMsg`, `StatusTitle` and `StatusKind` (the last is a pointer, `nil` when the core sends none).
- **Change counter (`StateChangeSeq`):** `*status_ms + turn` when `status_ms` is set and non-zero, else `turn`. This is the sequence the bridge publishes and the relay uses for optimistic concurrency.
- **Labels (`Herdr.app` integration):** `labels["herdr.space"]` → `WorkspaceID`, `labels["herdr.tab"]` → `TabID`, `labels["where"]` (`"<space> / <tab>"`) split on `" / "` into `WorkspaceLabel` and `TabLabel` (falling back to `herdr.workspace_label` and `herdr.tab_label`). No `workspace.list` or `tab.list` polling is needed.
- **Title:** `title` is herdr-core's terminal title as the core reports it (the shell's OSC title), passed on unchanged. Sanitising it into a glanceable task title is the adapter's job (`TaskTitle`, [`agents.md` §1](agents.md)); the wire field is `contracts.md` §1.2's `title`.
- **Foreground:** `foreground.pid` → `ForegroundPID`, `foreground.argv` → `ForegroundArgv`, `foreground.cwd` (else `cwd`) → `CWD`.
- **Session:** the core reports none. When an agent was detected, `AgentSession{Kind:"id", Source:"argv"}` is filled from `foreground.argv`, else `argv`, when it carries `--resume`, `-r`, `--session-id`, `--session`, `-s` or `--conversation` (with their `=` forms). How an id that is not in argv is found is the adapter's business ([`agents.md` §3.2–§3.3](agents.md)).

---

## 4. `terminal.keys` key grammar

Verified on `hc 0.1.14` against a sandbox terminal:

- **Names are case-insensitive:** `Enter`/`enter`/`Return`, `Escape`/`esc`, `Tab`, `Space`, `Backspace`, `Up`/`Down`/`Left`/`Right`, `Home`, `End`, `Insert`, `Delete`, `PageUp` (or `PgUp`), `F1`…`F12`.
- **Single characters:** an unshifted printable ASCII character is typed literally (`"1"`, `"y"`, `"-"`, `"/"`). Characters that need Shift on a US layout (`"$"`, `"?"`) and non-ASCII ones (`"é"`) are refused; use `shift+4` and the like.
- **Chords:** modifiers `ctrl`, `alt`, `shift` and `meta`, joined with `+`, then one key: `ctrl+c`, `shift+tab`, `alt+Up`, `ctrl+alt+x`, `meta+a`. `cmd+`/`super+` are not names, and not every combination is accepted (`ctrl+shift+a` is refused while `ctrl+alt+x` works), so rely only on the combinations the adapters use.
- **Order:** the keys of one call are applied in order, e.g. `["Right","Enter"]`.
- **An ack only means `herdr-core` took the keys** (the result is `{"status","turn"}`). It does not prove the TUI reacted.
- An unknown key name is `invalid_params`, with a hint listing the common ones.

---

## 5. Events (`events.subscribe`)

Request (keep the connection open afterwards):

```json
{"jsonrpc":"2.0","id":"sub-1","method":"events.subscribe","params":{}}
```

- **Ack line:** `{"id":"sub-1","jsonrpc":"2.0","result":{"channel":null,"seq":1025,"terminals":[...]}}`.
- **Streamed JSON-RPC 2.0 notifications** (no `id` field):
  - `{"jsonrpc":"2.0","method":"event","params":{"seq":1026,"kind":"status",...}}` — `kind` is one of `created`, `lost`, `restarted`, `keep`, `finished`, `removed`, `status`, `turn`, `title`, `labels`, `size`, `doc`.
  - `{"jsonrpc":"2.0","method":"missed","params":{...}}` — the subscriber fell behind; a fresh snapshot follows, and events at or below its `seq` are ignored.
  - `{"jsonrpc":"2.0","method":"heartbeat","params":{...}}` — every 15 s (`Client.Subscribe` drops these).
- **Global subscription:** `params: {}` streams every terminal of the core and its connected machines. There is no need to re-subscribe when the set of terminals changes.

**Bridge policy:** events are only a **trigger**: on any `"event"` or `"missed"` notification, the bridge schedules a debounced `terminal.list` (60 ms) and diffs the result; it also polls `terminal.list` on its own, every 30 s while the stream is up and every 2 s while it is down. Never build state from event payloads alone. See `Syncer.Run` in [`pkg/herdr/sync.go`](../../pkg/herdr/sync.go).

---

## 6. Errors seen in practice

| `error.data.kind` (`Error.Code`) | When |
|---|---|
| `not_found` | The `terminal` does not exist (`no terminal "t99"`) |
| `needs_target` | The method needs a specific terminal and got none |
| `finished` | Keys or text sent to a terminal whose program already exited |
| `blocked` | `terminal.send` on a blocked terminal, without `force` |
| `status_mismatch` | `terminal.send` with `if` set and a status that differs |
| `invalid_params` | Wrong param name or type, an unknown key name (`-32602`) |
| `machine_unknown` | `machine/t7` for a machine this core does not have |
| `unavailable` | The terminal's program cannot be reached right now |
| `name_taken`, `spawn_failed`, `invalid_template`, `config` | `terminal.run` refusals |
| `internal`, `unsupported_generation` | Internal failure; a `hello` with an older protocol generation |

The `hc` CLI adds exit codes for the same kinds: 3 `not_found`, 7 `needs_target`, 9 `blocked`, 10 `status_mismatch`, 11 `finished` (`hc --skill`).

If the socket cannot be dialed, `herdr-core` is not running (`herdr.ErrUnavailable`). Report `herdr_online=false` and retry with backoff (§7).

---

## 7. Reconnect (bridge policy)

- **Backoff** for dial failures and stream drops: exponential with jitter, 500 ms doubling to 30 s ([`pkg/herdr/clock.go`](../../pkg/herdr/clock.go), `Syncer.Run`).
- **On reconnect:**
  1. `hello` (`Ping`; the bridge then sends `herdr_status` with `herdr_online: true` to the relay)
  2. `terminal.list` (`ListAgents`), applied as a full replacement: the relay gets `agent_update` / `agent_removed` for what changed
  3. `events.subscribe`, then `terminal.list` once more to cover the gap

  A full `snapshot` goes to the relay only when the bridge (re)connects to the relay, or on a relay `resync` ([`contracts.md` §3](contracts.md)).

If a fact here turns out wrong on a newer `herdr-core`, fix this file in the same commit as the code change.
