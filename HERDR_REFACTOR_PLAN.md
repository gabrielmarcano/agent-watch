# Herdr Refactor & Architecture Plan for Agent Watch (Go Edition)

**Status:** Revised — verified against herdr 0.9.1 (socket protocol 22) on 2026-09-23.
**Scope:** `cmd/bridge/`, `cmd/relay/`, `pkg/`, `herdr-agent-watch` plugin, `wearos-app/`, `watchos-app/`.
**Branch Rule:** All work on `feat/herdr-focus`. Zero legacy code kept (no Warp, no AppleScript, no Claude Code plugin, no tailing sidecars, no Node.js runtime).

---

## 0. Revision Log

| # | Change | Why |
|---|---|---|
| 1 | Status enum is herdr's, verbatim (`idle / working / blocked / done / unknown`) | The first draft mixed herdr's enum with legacy names (`thinking`, `waiting_for_permission`) |
| 2 | Agent key is `pane_id`, not the session UUID | Session ids change on `/clear`, can be missing, can go stale, and pi reports a path instead |
| 3 | Blocked prompts are **parsed from the screen** into options, never mapped by position | In Claude Code, `2` means "Yes, don't ask again", so the old Deny button granted permanent permission |
| 4 | Agent-specific logic lives only in `pkg/agents` adapters (Claude, agy, OpenCode first; generic fallback) | Multi-agent, multi-provider by design |
| 5 | History comes from each agent's own transcript, located via herdr's `agent_session`, with a screen-capture fallback | herdr exposes no response text, and screen text loses the markdown |
| 6 | Socket-only herdr client (no CLI `exec`) | The socket already exposes `agent.prompt`, `agent.send_keys`, `agent.read` |
| 7 | Bridge runs under launchd (macOS) / systemd `--user` (Linux); no plugin `[[events]]` hook | Plugin actions are one-shot, and a per-event process would duplicate the daemon's subscription |
| 8 | watchOS push via ntfy (free); APNs dropped | APNs requires the paid Apple Developer account (see `watchos-app/IMPLEMENTATION_PLAN.md` §6) |
| 9 | No server-side "selected agent"; every command names its `pane_id` | Selection was undefined with several agents and several watches |
| 10 | Commands carry `expected_seq` (+ prompt `fingerprint`) and are re-validated on the Mac | Prevents a stale tap from typing `1` into a live prompt |
| 11 | Tokens in `Authorization` headers, never query strings; paired device tokens | The relay can drive agents on the Mac, so it is effectively a remote-execution surface |
| 12 | Go module path matches the remote: `github.com/gabrielmarcano/agent-monitor` | The first draft used the wrong path |
| 13 | Wear OS is the primary client (Pixel Watch 2); watchOS is best-effort, simulator-only, last phase | Wear OS is the daily driver; there is no physical Apple Watch to test on |
| 14 | §12.1 spells out what runs on the Mac: one binary; the plugin is only its installer/controller | The first draft did not say which process is long-running or who keeps it alive |
| 15 | Phases split into executable guides under `docs/`; contracts frozen in `docs/reference/contracts.md`; Phase 0 is now only fixtures, and `pkg/model` moved to Phase 1 | Lets smaller agents execute phases without design context |
| 16 | The socket's `agent.read` source is `recent_unwrapped` (underscore) | Verified: the hyphenated CLI spelling fails on the socket with `invalid_request` |
| 17 | Review batch (2026-09-25): `prompt` is refused while a menu is on screen; `cancel` re-reads the screen and may carry `fingerprint`; commands are idempotent; the relay pings the host too | herdr 0.9.1 reports some open dialogs as `done`/`working`; a stale cached prompt or a double tap must never type into a live menu |
| 18 | Relay client IP comes from `AW_TRUSTED_PROXIES` (+ optional `AW_CLIENT_IP_HEADER`); `AW_TRUST_CF_IP` removed. FCM `resolved` push added, off until `AW_PUSH_RESOLVED` | Forwarding headers are client-controlled unless a trusted proxy wrote them; older watch apps show an unknown push as a bogus approval |
| 19 | Android phone client planned as Phase 7: a relay client sharing the Wear OS data layer (`:core`), phone notifications local-only, approvals need unlock | The owner wants a phone app as a real project; the relay already serves any paired client, so no backend change is required |

---

## 1. Core Decisions

1. **Herdr-native control plane.** Herdr is the single source of truth for agent detection, lifecycle status, and input delivery. Nothing outside herdr decides whether an agent is working or blocked.
2. **Agent knowledge is isolated in adapters.** Anything that depends on a specific agent lives in `pkg/agents` and nowhere else:
   - menu layout
   - cancel key
   - transcript format
   - whether a prompt can be queued while the agent is working

   Priority adapters: **Claude (`claude`), Antigravity (`agy`), OpenCode (`opencode`)**. Every other agent herdr detects (codex, pi, cursor, copilot, gemini, kimi, …) works through the **generic adapter**.
3. **Go for bridge and relay.** Single module, Go 1.22+ (1.27 installed). Static binaries: `agent-watch-bridge` (Mac/Linux host), `agent-watch-relay` (Linux VPS). Shared contracts in `pkg/model`.
4. **Thin Mac, smart relay.** The Mac runs one small binary that translates herdr socket ↔ outbound WebSocket and reads transcripts on demand. Everything else lives on the relay:
   - aggregated state
   - history storage
   - push dispatch
   - pairing and auth
5. **Outbound relay networking.** The bridge dials `wss://relay.<domain>/v1/host`. Watches use HTTPS + SSE on the relay. No inbound ports on the Mac, no VPN on the watch.
6. **Wrist-first scope.** Status, approvals, dictation, history, complications/tiles. No terminal emulator (Moshi covers that). A phone app is planned as a separate client (Phase 7), not as a watch companion: Wear OS already routes the watch's traffic through the phone over Bluetooth.
7. **Wear OS first, watchOS as extra support.**
   - **Wear OS is the primary client and reference implementation.** Every feature ships there first and is verified end-to-end on the daily-driver device: a **Google Pixel Watch 2**.
   - **watchOS is best-effort.** It follows the same `/v1` API and its models stay in sync with `pkg/model`, so it always compiles. Its UI features may lag behind Wear OS.
   - **watchOS is verified only in the Xcode simulator** (there is no physical Apple Watch). It never blocks a phase.

---

## 2. High-Level Architecture

```
┌──────────────────────────────── Mac Host ────────────────────────────────┐
│                                                                          │
│  Herdr server ── Unix socket ($HERDR_SOCKET_PATH,                        │
│       │          default ~/.config/herdr/herdr.sock, NDJSON)             │
│       │                                                                  │
│  agent-watch-bridge (Go, launchd LaunchAgent, KeepAlive)                 │
│   ├─ pkg/herdr    snapshot + events.subscribe, prompt/send_keys/read     │
│   ├─ pkg/agents   adapters: claude · agy · opencode · generic            │
│   │                (prompt parsing, key mapping, transcript readers)     │
│   └─ pkg/relayclient  outbound WSS, backoff + jitter, 30s ping           │
│                                                                          │
│  Agent transcripts (read-only, on demand):                               │
│   claude → *.jsonl · agy → transcript_full.jsonl · opencode → opencode.db │
└──────────────────────────────────┬───────────────────────────────────────┘
                                   │ WSS + Authorization: Bearer <host token>
                                   ▼
┌───────────────────── VPS (relay.<domain>, TLS proxy) ────────────────────┐
│  agent-watch-relay (Go, systemd)                                         │
│   ├─ host hub (one host), command routing + timeouts                     │
│   ├─ /v1 REST + SSE for watches, device pairing + tokens                 │
│   ├─ store: devices, push targets, history (atomic JSON file)            │
│   └─ pkg/push: FCM v1 (Wear OS) · ntfy (watchOS, free)                   │
└──────────────┬──────────────────────────────────────┬────────────────────┘
               │ HTTPS + SSE                          │ ntfy publish
       ┌───────▼────────┐                    ┌────────▼────────┐
       │  Wear OS app   │                    │  watchOS app    │
       │  (+ FCM push)  │                    │ (+ ntfy on iPhone│
       └────────────────┘                    │  mirrored)      │
                                             └─────────────────┘
```

---

## 3. Herdr Integration Facts (verified on 0.9.1)

| Topic | Fact |
|---|---|
| Transport | Unix socket, newline-delimited JSON. Request `{"id": "<string>", "method", "params"}`; `id` must be a string |
| RPC lifecycle | **One request per connection**: the server closes after replying. Only `events.subscribe` stays open |
| Agent status | `idle / working / blocked / done / unknown` |
| Agent identity | herdr `agent` field: `claude`, `agy`, `opencode`, `codex`, `pi`, … (`server.agent_manifests`) |
| Stable key | `pane_id` (e.g. `w5:pAW`; contains `:`, so URL-encode it in paths) |
| Session ref | `agent_session {agent, kind: id\|path, source, value}`. Optional; may be stale. Trust it only when `agent_session.agent == agent` |
| Change counter | `state_change_seq` per pane (`revision` is not a reliable change detector) |
| Events | `events.subscribe`. `pane.agent_status_changed` **requires `pane_id`** in the subscription; `pane.created / closed / exited / agent_detected` are global. Stream lines are `{"event": "<snake_case>", "data": {...}}` |
| Prompt | `agent.prompt` is rejected with `agent_blocked` when blocked. Accepted otherwise, including while `working` |
| Keys | `agent.send_keys`: special keys `Enter Escape(esc) Up Down Left Right Tab Space Backspace F1–F12`, single chars (`"1"`), chords (`ctrl+c`). No `PageUp/Home/End/Delete` |
| Ack semantics | An ack means herdr took the bytes, **not** that the TUI acted on them |
| Screen read | `agent.read` with `source ∈ visible \| recent \| recent_unwrapped \| detection` (**underscore on the socket**; the CLI flag spells it `recent-unwrapped`), `format: text` (plain, no ANSI) |
| Health | `ping` → `{version, protocol, capabilities}` |
| Plugin env | `HERDR_SOCKET_PATH`, `HERDR_PLUGIN_CONFIG_DIR`, `HERDR_PLUGIN_STATE_DIR`, `HERDR_PLUGIN_EVENT_JSON`, `HERDR_PLUGIN_CONTEXT_JSON` |
| Plugin build | `[[build]]` runs on `herdr plugin install` only, **not** on `herdr plugin link` |

**Sync strategy (snapshot is authoritative, events only accelerate):**
1. Bootstrap with `agent.list` (or `session.snapshot`).
2. Open `events.subscribe` with the global lifecycle events plus one `pane.agent_status_changed` per agent pane. Resubscribe whenever the pane set changes.
3. Any event triggers a **debounced re-list** (~150 ms). Events never mutate state directly.
4. Safety poll: every 15 s while the stream is healthy, every 2 s while it is down.
5. On socket loss: exponential backoff + jitter, then back to step 1.

**Prerequisite on each host:** `herdr integration status` must show the priority agents as `current`. On this Mac, `claude` is `outdated (v7 < v10)`, so run `herdr integration install claude`.

---

## 4. Go Package Layout

```
agent-monitor/                      (repo root, module github.com/gabrielmarcano/agent-monitor)
├── go.mod · go.sum · Makefile
├── herdr-plugin.toml
├── cmd/
│   ├── bridge/                     # subcommands: configure, run, start, restart, stop, status, pair, version
│   └── relay/main.go               # subcommands: serve, devices (list/revoke)
├── pkg/
│   ├── model/                      # shared contracts (the ONLY schema source)
│   │   ├── agent.go                # AgentState, PendingPrompt, PromptOption, HistoryItem
│   │   ├── api.go                  # watch-facing request/response DTOs
│   │   └── wire.go                 # bridge↔relay envelopes
│   ├── herdr/                      # socket client — no agent-specific code
│   │   ├── rpc.go                  # one-shot call helper
│   │   ├── subscribe.go            # events stream + resubscription
│   │   └── sync.go                 # snapshot/diff engine
│   ├── herdrtest/                  # fake herdr socket server for tests
│   ├── agents/                     # ALL agent-specific knowledge
│   │   ├── adapter.go              # Adapter interface + registry
│   │   ├── menu.go                 # generic numbered-menu parser + role classifier
│   │   ├── claude.go · agy.go · opencode.go · generic.go
│   │   └── testdata/<agent>/       # real captured screens + transcript samples
│   ├── relayclient/                # bridge side of the WSS link
│   ├── relay/                      # relay server: hub, api, sse, store, auth
│   └── push/                       # Notifier interface: fcm.go, ntfy.go
└── deploy/
    ├── launchd/                    # LaunchAgent template (written by `bridge start`)
    └── relay/                      # systemd unit, env + proxy examples, deploy.sh, Dockerfile, ops README
```

---

## 5. Shared Domain Model (`pkg/model`)

```go
package model

// Mirrors herdr's enum verbatim. Never rename or remap.
type AgentStatus string

const (
    StatusIdle    AgentStatus = "idle"
    StatusWorking AgentStatus = "working"
    StatusBlocked AgentStatus = "blocked"
    StatusDone    AgentStatus = "done"
    StatusUnknown AgentStatus = "unknown"
)

type AgentState struct {
    PaneID         string         `json:"pane_id"`          // stable key
    Agent          string         `json:"agent"`            // herdr id: "claude", "agy", "opencode", ...
    Label          string         `json:"label"`            // task title (terminal_title_stripped) || herdr name || basename(cwd) || pane_id
    Name           string         `json:"name,omitempty"`   // herdr pane name
    CWD            string         `json:"cwd,omitempty"`
    WorkspaceID    string         `json:"workspace_id"`
    Workspace      string         `json:"workspace,omitempty"` // herdr workspace label
    Status         AgentStatus    `json:"status"`
    Focused        bool           `json:"focused"`
    StateChangeSeq uint64         `json:"state_change_seq"`
    Prompt         *PendingPrompt `json:"prompt,omitempty"` // only while blocked
    UpdatedAt      string         `json:"updated_at"`       // RFC 3339
}

type PromptKind string // "permission" | "question" | "unknown"

type OptionRole string // "allow_once" | "allow_always" | "deny" | "choice"

type PromptOption struct {
    ID    string     `json:"id"`    // "opt-1", ... (keys are resolved on the Mac, never sent)
    Label string     `json:"label"`
    Role  OptionRole `json:"role"`
}

type PendingPrompt struct {
    Kind        PromptKind     `json:"kind"`
    Title       string         `json:"title"`            // e.g. "Bash command"
    Detail      string         `json:"detail,omitempty"` // e.g. the command or file path
    Options     []PromptOption `json:"options"`
    Fingerprint string         `json:"fingerprint"`      // hash of the parsed prompt
    RawTail     string         `json:"raw_tail,omitempty"` // last lines when Kind == unknown
}

type HistoryItem struct {
    ID          string `json:"id"`     // stable hash(pane_id + session + turn)
    PaneID      string `json:"pane_id"`
    Agent       string `json:"agent"`
    Label       string `json:"label"`
    Query       string `json:"query,omitempty"`
    Response    string `json:"response"`      // markdown when Source == "transcript"
    Source      string `json:"source"`        // "transcript" | "screen"
    CompletedAt string `json:"completed_at"`
}

type AgentsSnapshot struct {
    HostOnline  bool         `json:"host_online"`  // bridge connected to the relay
    HerdrOnline bool         `json:"herdr_online"` // bridge can reach the herdr socket
    Agents      []AgentState `json:"agents"`
    GeneratedAt string       `json:"generated_at"`
}
```

Both clients mirror these structs field by field. The legacy fields `tool_input`, `session_id`, `last_query`, `last_response` and `history` are removed from the state object.

---

## 6. Agent Adapters (`pkg/agents`)

```go
type Adapter interface {
    Name() string                                  // herdr agent id it serves
    ParsePrompt(screen string) (Prompt, bool)      // screen = agent.read visible, text
    CancelKeys() []string                          // default: ["esc"]
    PromptWhileWorking() bool                      // may we type while the agent is working?
    LastTurn(ctx context.Context, ref SessionRef) (*model.HistoryItem, error) // ErrNoTranscript → fallback
}

// Prompt is the Mac-side view: public model + the key sequence per option.
type Prompt struct {
    Public model.PendingPrompt
    Keys   map[string][]string // option id → keys, e.g. {"opt-3": {"3"}}
}
```

**Registry:** exact match on herdr's `agent` field. Unknown agents get `generic`. Adapters only override what differs from generic.

### 6.1 Prompt parsing (all agents)

- **Generic parser (`menu.go`):**
  - Finds the last numbered-option block on the visible screen, with an optional cursor marker (`❯ › > ▶ ●`).
  - Takes the question and title from the non-empty lines above that block.
  - Classifies each option's **role by its label, never by its position**:
    - `yes / allow / approve / proceed` → `allow_once`
    - `always / don't ask again / for this session / all` → `allow_always`
    - `no / deny / reject / cancel` → `deny`
    - anything else → `choice`
- **Key resolution:**
  - If the adapter says the menu accepts digits, send `[digit]`, plus `Enter` if the adapter requires confirmation.
  - Otherwise navigate with `Up`/`Down` from the cursor row, then `Enter`.
- **Deny with no `deny` option parsed:** use the adapter's `CancelKeys()`.
- **Nothing parseable:** `Kind = unknown` with `RawTail`. The watch then offers **Cancel only**, never a blind Allow.
- **Fixtures are mandatory.** Every adapter ships real captures under `testdata/<agent>/`, taken with `herdr agent read <pane> --source visible --format text`. No adapter change is merged without a fixture that exercises it.

| Agent | Known facts | To verify in Phase 0 |
|---|---|---|
| `claude` | Permission menu `1. Yes` / `2. Yes, and don't ask again …` / `3. No …(esc)`. Option count and wording vary by tool. `esc` cancels. Typing while working queues the message | Capture Bash, Edit, WebFetch, AskUserQuestion and plan-approval screens |
| `agy` | herdr integration installed (`current v3`) | Capture its approval UI; digit vs. arrow selection; cancel key |
| `opencode` | herdr integration installed (`current v12`); permission handled in-TUI | Capture its approval UI; digit vs. arrow selection; cancel key |
| generic | Numbered menus + `esc` | Smoke test against codex/pi when available |

### 6.2 Command safety rules (bridge-side, all agents)

1. The watch **never sends raw keys**. It sends `answer {option_id}` or `cancel`, and the Mac resolves the keys.
2. `answer` carries `expected_seq` and `fingerprint`; `cancel` carries `expected_seq` and, optionally, `fingerprint`. Right before sending keys, the bridge re-lists the agent (its own `agent.list`, not the shared snapshot) and re-reads the screen, and rejects with `stale_state` or `prompt_changed` on any mismatch. `cancel` takes its keys from the menu on screen, never from the cached prompt; with no parseable menu it cancels only a prompt the watch saw as `unknown`, with the adapter's default cancel keys.
3. `prompt` is accepted when `idle` or `done`. When `working`, it is accepted only if `PromptWhileWorking()`; otherwise it returns `agent_busy`. It is rejected when `blocked` (`agent_blocked`), `unknown` or any other status (`agent_state_unknown`). **It is also rejected (`agent_blocked`) while the adapter parses a menu on the screen**, whatever herdr's status: herdr 0.9.1 reports some open dialogs as `done` or `working`, and the text plus Enter would land in the menu.
4. Commands are accepted only for panes with a detected agent. No `pane.send_text` or shell panes.
5. `allow_always` options are shown in a secondary "More" list, never as the primary button.
6. **Each command acts at most once.** A replayed `request_id`, a second answer or cancel for a prompt already answered at that `state_change_seq`, and the same prompt text sent twice at one seq are rejected with `stale_state` until herdr reports a new seq.

---

## 7. History (own implementation, transcript-first)

- **Trigger:** a pane goes `working → done|idle`. The bridge waits ~500 ms (transcript flush), then calls `adapter.LastTurn(ref)`.
- **Locating the transcript:** use herdr's `agent_session`, but only if `agent_session.agent == pane.agent`. No global tailing and no directory scanning for "latest file".
- **Bounded reads:** tail the last ~256 KB of a transcript. Truncate a response to 16 KB.

| Agent | Source | Extraction |
|---|---|---|
| `claude` | `kind: path` → transcript JSONL (sent by the current integration). `kind: id` → resolve `<id>.jsonl` under `~/.claude`, every `~/.claude-*` profile and the configured `claude_config_dirs` (custom `CLAUDE_CONFIG_DIR` elsewhere) | Last `user` text + last `assistant` text blocks (logic from legacy `parseTranscript`) |
| `agy` | `kind: path` → `…/brain/<id>/.system_generated/logs/transcript_full.jsonl` | Last `USER_INPUT` + last `PLANNER_RESPONSE` without `tool_calls` (logic from legacy sidecar) |
| `opencode` | `kind: id` → read-only SQLite `~/.local/share/opencode/opencode.db` (`message` / `part` tables keyed by `session_id`, JSON `data`) via pure-Go `modernc.org/sqlite`, `mode=ro` | Last user message text + last assistant text parts |
| generic / any failure | `agent.read` with `source: "recent_unwrapped"`, `format: "text"` | Screen text, `Source = "screen"` |

- A reader failure (format change, missing file, locked DB) **always falls back to screen capture** and logs a warning. It never drops the event.
- **Storage lives on the relay:** the last 20 items per pane plus a global cap of 200, persisted in the relay store and deduplicated by `ID`. The watch can browse history while the Mac is asleep.

---

## 8. Bridge ↔ Relay Protocol (`pkg/model/wire.go`)

`wss://relay.<domain>/v1/host` with `Authorization: Bearer <HOST_TOKEN>`. JSON envelopes `{ "type": ..., ... }`.

| Direction | Type | Payload |
|---|---|---|
| H → R | `hello` | `version`, `host`, `herdr_protocol` |
| H → R | `herdr_status` | `herdr_online` (bridge is up but the herdr socket is unreachable) |
| H → R | `snapshot` | `agents[]` (full; sent on every (re)connect) |
| H → R | `agent_update` / `agent_removed` | one `AgentState` / `pane_id` |
| H → R | `history_item` | one `HistoryItem` |
| H → R | `command_result` | `request_id`, `ok`, `error_code`, `message` |
| R → H | `command` | `request_id`, `action: prompt\|answer\|cancel`, `pane_id`, `expected_seq`, `text` / `option_id` + `fingerprint` |
| R → H | `resync` | (asks for a full snapshot) |

- **Keepalive:** both sides ping the WebSocket every 30 s and expect the pong within 10 s (proxies such as Cloudflare drop idle connections at about 100 s). No pong: the bridge reconnects; the relay drops the host.
- **Host offline:** the relay marks `host_online=false` and broadcasts it.
- **Command timeout:** one 7 s budget for sending the command and waiting for its result → `timeout`. The bridge answers within 6 s of receiving it. A command sent while the host is offline fails immediately with `host_offline`, and so do in-flight commands the moment their host disconnects, misses a pong or is replaced.
- **Error codes:** `stale_state`, `prompt_changed`, `agent_busy`, `agent_blocked`, `agent_state_unknown`, `unknown_pane`, `host_offline`, `timeout`.

---

## 9. Relay Public API (`/v1`, watches)

All endpoints except `POST /v1/pair` require `Authorization: Bearer <device_token>`. `{pane_id}` is URL-encoded.

| Method | Path | Purpose |
|---|---|---|
| POST | `/v1/pair` | `{code}` → `{device_token}`. Rate-limited |
| GET | `/v1/agents` | `AgentsSnapshot` |
| GET | `/v1/events` | SSE: `snapshot` on connect, then `agent`, `agent_removed`, `host`, `history`; `:` keepalive every 15 s |
| GET | `/v1/history?pane_id=&limit=` | History items, newest first |
| POST | `/v1/agents/{pane_id}/prompt` | `{text, expected_seq}` |
| POST | `/v1/agents/{pane_id}/answer` | `{option_id, expected_seq, fingerprint}` |
| POST | `/v1/agents/{pane_id}/cancel` | `{expected_seq, fingerprint?}` |
| POST | `/v1/push/register` | `{platform: "fcm", token}` (Wear OS) |
| POST | `/v1/host/pair-code` | Host token. Returns a 6-digit code (used by the bridge `pair` action) |
| GET | `/v1/host/status` | Host token. `host_online`, `herdr_online`, device and agent counts |
| GET | `/v1/healthz` | No auth. Liveness |

**Pairing:**
1. `herdr plugin action invoke … pair` asks the relay for a 6-digit code (5-minute TTL).
2. The watch enters the relay URL + code and receives a random 256-bit device token. The relay stores only its hash.
3. Devices are listed and revoked with `agent-watch-relay devices`, also while the relay runs (local admin socket; a revoked token is rejected at once).

**Relay store:** one JSON file, written atomically (temp file + rename), holding devices, push targets and history. No database.

---

## 10. Push Notifications (relay-side)

- **Triggers**, from `agent_update` diffs:
  - `→ blocked`: high priority, "`<label>` needs approval: `<detail>`"
  - `working → done`: normal priority, first line of the response
- **Anti-spam** (exact rules: `contracts.md` §4.3):
  - Debounce 5 s per pane and event. A repeated `done` is dropped; a quick re-block is held to the end of the window, never dropped.
  - The first push of a 10 s window goes out at once; when more than 3 would go out in the window, the held ones become one digest ("N agents need you" / "N agents finished").
- **`resolved`** (FCM only, opt-in with `AW_PUSH_RESOLVED`): withdraws a pane's approval notification once it leaves `blocked`.
- **`pkg/push.Notifier` interface:**
  - **FCM v1** (Wear OS): data messages, JWT service-account auth. The service account JSON lives only on the VPS.
  - **ntfy** (watchOS, free): the relay POSTs to a topic (ntfy.sh or self-hosted on the same VPS); the ntfy iPhone app mirrors to the Apple Watch. Topic name + access token are secrets. Limitation: tapping the notification does not open Agent Watch.
  - **Telegram** can be added later behind the same interface.
  - **APNs** is out of scope until a paid Apple Developer account exists.

---

## 11. Target Agent Selection (no server-side selection)

- Every command names its `pane_id`. The relay and bridge keep **no** "selected agent".
- Each watch stores a **pinned agent** locally: the last agent the user opened or acted on.
- Quick Dictate (tile / complication) targets, in order:
  1. the pinned agent, if it still exists;
  2. otherwise the agent that most recently finished (`done`);
  3. otherwise herdr's focused pane.
- The dictation screen **always shows the target label before sending**.
- The complication shows the most severe status across agents: `blocked > done > working > idle > unknown`.

---

## 12. Herdr Plugin (`herdr-plugin.toml`)

### 12.1 What runs on the Mac

There is **one binary**, `agent-watch-bridge`. The herdr plugin is not a second program: it is only the manifest that lets herdr install and control that binary.

| Piece | What it is | Lifetime |
|---|---|---|
| `herdr-plugin.toml` | Manifest: how to build the binary and which actions herdr exposes | Static file |
| `agent-watch-bridge start` / `restart` / `stop` / `status` / `pair` | One-shot commands herdr runs when you invoke an action (also runnable from a terminal or the macOS menu bar app) | Seconds, then exit |
| `agent-watch-bridge run` | The actual bridge: herdr socket ↔ relay WebSocket | Long-running, kept alive by **launchd** |

- **Why launchd and not herdr keeps it alive:**
  - Plugin actions are one-shot, and the manifest has no "keep this process alive" option.
  - Plugin panes (`herdr plugin pane`) are visible terminal panes: they take a tab and die with the herdr server.
  - launchd restarts the bridge after a crash or a reboot, and survives herdr updates. Collie does the same with systemd `--user`.
- **When herdr is not running**, the bridge stays connected to the relay and reports `herdr_online=false`. The watch can then tell "herdr is stopped" apart from "the Mac is offline".
- **Day-to-day use:** you run `start` and `pair` once. After that there is nothing to open or keep running by hand.

### 12.2 Manifest

The repository's `herdr-plugin.toml`:

```toml
id = "herdr-agent-watch"
name = "Agent Watch"
version = "0.3.0"   # = BRIDGE_VERSION in VERSIONS; `make check-versions` fails when they differ
min_herdr_version = "0.9.0"   # the version this plan was verified against
description = "Monitor and answer your agent herd from your smartwatch via a remote relay"
platforms = ["macos", "linux"]

# Runs on `herdr plugin install` only. For `herdr plugin link`, run `make bridge` first.
# Same flags as `make bridge`: static (CGO_ENABLED=0), stripped, stamped with
# BRIDGE_VERSION read from VERSIONS (the binary adds its commit on its own).
[[build]]
command = ["sh", "-c", 'v="$(sed -n "s/^BRIDGE_VERSION=//p" VERSIONS)" && [ -n "$v" ] || { echo "BRIDGE_VERSION not found in VERSIONS" >&2; exit 1; }; CGO_ENABLED=0 exec go build -ldflags "-s -w -X main.version=$v" -o bin/agent-watch-bridge ./cmd/bridge']
platforms = ["macos", "linux"]

[[actions]]
id = "start"     # writes/loads the LaunchAgent (macOS) or systemd --user unit (Linux)
title = "Start bridge service"
command = ["./bin/agent-watch-bridge", "start"]

[[actions]]
id = "restart"   # restarts the installed service without rewriting it
title = "Restart bridge service"
command = ["./bin/agent-watch-bridge", "restart"]

[[actions]]
id = "stop"      # unloads the service
title = "Stop bridge service"
command = ["./bin/agent-watch-bridge", "stop"]

[[actions]]
id = "status"
title = "Bridge and relay status"
command = ["./bin/agent-watch-bridge", "status"]

[[actions]]
id = "pair"
title = "Pair a watch (shows a code)"
command = ["./bin/agent-watch-bridge", "pair"]
```

- **Five one-shot actions:** `start`, `restart`, `stop`, `status`, `pair`. `configure` is not an action (it takes the token as an argument): run `./bin/agent-watch-bridge configure …` in a terminal.
- **Build:** static (`CGO_ENABLED=0`), stripped and version-stamped, the same flags as `make bridge`. Versions live only in `VERSIONS` at the repo root (one per component); the build reads `BRIDGE_VERSION` from it, and the manifest's own `version` must equal it (`make check-versions`, also run by `go test`). The binaries add their commit themselves: `0.3.0 (c8aa72e)`.
- **Service environment:** launchd does not inherit `HERDR_*`, so `start` pins the config path (`run --config`), `HERDR_SOCKET_PATH` and `HERDR_PLUGIN_STATE_DIR` in the LaunchAgent plist (or the systemd `--user` unit). Each value comes from a `start` flag, else herdr's plugin environment, else the installed definition, else the default, so a `start` from a terminal or the menu bar keeps what herdr installed. The service runs `agent-watch-bridge run` with `KeepAlive`.
- **`restart`** restarts the installed service without rewriting its definition (`make restart` rebuilds `bin/agent-watch-bridge` first).
- **Config file:** `$HERDR_PLUGIN_CONFIG_DIR/config.toml` (mode 0600) holds `relay_url`, `host_token`, `host_name` and `claude_config_dirs`.
- **No `[[events]]` hook:** the daemon's own subscription covers it.
- **Local dev:** `make bridge && herdr plugin link "$PWD"` (absolute path).

---

## 13. Client Changes

### 13.1 Wear OS (`wearos-app/`) — primary

Target device: **Google Pixel Watch 2**. The current `minSdk 30` / `targetSdk 34` already cover it. Deploy with `adb` over Wi-Fi debugging on the watch.

1. `AgentState.kt` mirrors `pkg/model` (`@Keep`). Removes `tool_input`, `session_id`, `last_query`, `last_response`, `history`.
2. `SseClient.kt` → `/v1/events` with a bearer token and reconnect; REST calls per §9.
3. `ServerConfigScreen.kt` → pairing screen (relay URL + 6-digit code) instead of a LAN IP.
4. `AgentScreen.kt`:
   - agent list (`ScalingLazyColumn`, sorted by severity) → agent detail. This is the Wear-idiomatic pattern, and it scales better than a carousel with 10+ agents
   - prompt card rendering `PendingPrompt`: primary **Allow** (`allow_once`), **Deny** (`deny`/cancel), **More** (other options incl. `allow_always`)
   - `unknown` prompts show `RawTail` + Cancel only
5. History screens stay (per agent + global). The markdown reader is kept for `source == "transcript"`; `screen` items render as plain text.
6. Complication, tile and QuickDictate follow §11.

### 13.2 watchOS (`watchos-app/`) — best-effort, simulator only

Starts after Wear OS is verified. Sync the model and network layer first so the app compiles against `/v1`; UI features follow as time allows. Verification is the Xcode watchOS simulator against the real relay.

1. `AgentState.swift` mirrors `pkg/model` (`Codable`, `Identifiable`, `Sendable`); `AnyCodable` is deleted.
2. `AgentNetworkService.swift` → `/v1` with a bearer token, SSE via `AsyncSequence` with reconnect.
3. Same pairing, prompt-card, history and pinned-agent behavior as Wear OS.
4. No push code in the app; ntfy on the iPhone covers alerts.

---

## 14. Phases

**Step-by-step guides for every phase live in [`docs/`](docs/README.md)** and progress is tracked in [`docs/STATUS.md`](docs/STATUS.md). The exact JSON shapes are frozen in [`docs/reference/contracts.md`](docs/reference/contracts.md).

| Phase | Guide | Work | Depends on |
|---|---|---|---|
| **0. Fixtures** | [0-fixtures](docs/phases/0-fixtures.md) | Capture blocked screens and transcript samples for claude/agy/opencode in a sandbox; resolve every 🔍 in `docs/reference/agents.md`; update herdr's claude integration | — |
| **1. Foundation** | [1-foundation](docs/phases/1-foundation.md) | Delete legacy; `go mod init github.com/gabrielmarcano/agent-monitor`; Makefile; `pkg/model` from the contracts; `pkg/herdrtest` fake socket | — |
| **2a. herdr client** | [2a-herdr-client](docs/phases/2a-herdr-client.md) | `pkg/herdr` (rpc, subscribe, syncer) | 1 |
| **2b. Adapters** | [2b-agent-adapters](docs/phases/2b-agent-adapters.md) | `pkg/agents` (menu parser, claude/agy/opencode/generic, transcript readers) | 1, 0 |
| **2c. Bridge** | [2c-bridge-daemon](docs/phases/2c-bridge-daemon.md) | `pkg/relayclient`, `pkg/bridge` (engine + command executor), `cmd/bridge`, launchd, herdr plugin | 2a, 2b |
| **3a. Relay** | [3a-relay-server](docs/phases/3a-relay-server.md) | `pkg/relay` (hub, API, SSE, pairing, store), `cmd/relay` | 1 |
| **3b. Push** | [3b-push](docs/phases/3b-push.md) | `pkg/push` (dispatcher, FCM, ntfy) | 3a |
| **3c. Deploy** | [3c-relay-deploy](docs/phases/3c-relay-deploy.md) | systemd + reverse proxy + Cloudflare on the VPS | 3a |
| **4. Wear OS** | [4-wearos](docs/phases/4-wearos.md) | Models, network, pairing, list/detail, prompt card, notifications, history, tile/complication (§13.1) | 1 (can start against a relay stub) |
| **5. End-to-end + docs** | [5-e2e](docs/phases/5-e2e.md) | E2E checklist **on the Pixel Watch 2**, README rewrite, ROADMAP refresh | 2c, 3b, 3c, 4 |
| **6. watchOS (best-effort)** | [6-watchos](docs/phases/6-watchos.md) | Model + network sync first, then UI parity (§13.2); simulator only | 5 (off the critical path; never blocks a release) |
| **7. Android phone** | [7-android-mobile](docs/phases/7-android-mobile.md) | `:core` module shared with Wear OS, phone app on the same `/v1` API | 5 |

**What can run in parallel:**
- **0 ∥ 1:** Phase 0 writes only `pkg/agents/testdata` and docs.
- **1 runs alone** among code phases: it deletes legacy and creates the module root, touching most of the git index.
- **2a ∥ 2b ∥ 3a ∥ 4** after Phase 1: disjoint directories, sharing only the frozen `pkg/model`. Caveat: 2b and 3a/3b may both edit `go.mod`/`go.sum`, so commit one before the other runs `go get`.
- **6 (watchOS)** could technically run in parallel with 4 (no shared files), but it goes last on purpose: Wear OS is the priority, and watchOS should copy a UI that has already been validated on a real device.
- **7 (Android phone)** can run in parallel with 6, except its step 1 (the Android module restructure), which runs alone.
- **Git discipline:** parallel agents commit only their own paths (`git add <paths>`, never `-A`).

**Verification per phase:**
- `go vet ./... && go test ./...`, with the fake herdr socket and adapter fixture tests.
- **End-to-end on the Pixel Watch 2, once for each of claude, agy and opencode:**
  - blocked → the watch shows the real command → **Deny** → the agent reports a denial, and a later identical request prompts again (proves Deny did not select "don't ask again")
  - **Allow** → the agent proceeds
  - dictation while `idle` → the agent starts working
  - `done` → a history item with the markdown response appears on the watch
  - Mac asleep → the watch shows "host offline" and commands fail with `host_offline`
  - herdr stopped with the Mac awake → the watch shows "herdr stopped"
- **watchOS (Phase 6):** the same flows in the Xcode simulator, except push (ntfy is checked on the iPhone).

---

## 15. Risks & Mitigations

| Risk | Mitigation |
|---|---|
| herdr protocol changes | Log `herdr_protocol` in `hello`; warn when above the tested version; fake-socket tests pin the contract |
| Agent TUI wording/layout changes | Label-based roles, fingerprint check, `unknown` → Cancel only; fixtures catch regressions |
| Transcript format / opencode DB schema changes | Screen-capture fallback, never a crash; per-adapter warnings |
| Relay compromise = remote control of agents | Hashed device tokens, pairing TTL + rate limit, no raw keys, agent panes only, revocable devices, TLS everywhere |
| ntfy topic leakage | Random topic + access token, stored only on the relay |
| Mac asleep / offline | `host_online=false` surfaced on the watch; history still browsable from the relay |
| watchOS regressions go unnoticed (no physical device) | Models kept in sync with `pkg/model` so the app always compiles; simulator pass in Phase 6; watchOS is labelled best-effort in the README |
