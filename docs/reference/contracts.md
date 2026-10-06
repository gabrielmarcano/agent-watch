# Contracts Reference (source of truth for every JSON shape)

Every JSON object that crosses a process boundary is defined here.

- **The shapes the watch sends or receives (§1–§2)** must match this file exactly in the Go structs (`pkg/model/`), the Kotlin models (`wearos-app/`) and the Swift models (`watchos-app/`; the watchOS app's state: `docs/STATUS.md`). To change one, follow the `schema-sync` skill: it lists every copy.
- **§3** exists only in Go (`pkg/model/wire.go`). **§4**'s data keys are read by `wearos-app/app/src/main/java/com/gabriel/agentwatch/network/AgentNotifications.kt`. **§5** is the relay's environment and data dir. **§6** (the bridge's config, local files and CLI output) is defined in `pkg/bridge` and `cmd/bridge`, not in `pkg/model`.

**Conventions (apply everywhere):**
- JSON keys are `snake_case`.
- Timestamps are RFC 3339 in UTC, e.g. `2026-09-23T17:04:05Z`. In Go, produce them with `model.Now()`.
- Optional fields use `omitempty` in Go and are nullable in the clients.
- Unknown JSON fields must be **ignored** by every decoder, never rejected. This lets the relay add fields without breaking old watches.
- IDs are opaque strings. Never parse them.

---

## 1. Agent state (`pkg/model/agent.go`)

### 1.1 `AgentStatus`

Herdr's enum, copied verbatim. Never rename, never remap, never add values.

| Value | Meaning |
|---|---|
| `idle` | Agent waits for a prompt |
| `working` | Agent is processing |
| `blocked` | Agent waits for the user to answer a menu (permission, question, plan approval) |
| `done` | Agent finished a turn and nobody has looked at it yet |
| `unknown` | Herdr cannot tell |

**Severity order** (used for the `agents` order, §1.5, and by the watch's list, complication and tile): `blocked > done > working > idle > unknown`.

### 1.2 `AgentState`

```go
type AgentState struct {
    PaneID         string         `json:"pane_id"`
    Agent          string         `json:"agent"`
    Label          string         `json:"label"`
    Name           string         `json:"name,omitempty"`
    Title          string         `json:"title,omitempty"`
    CWD            string         `json:"cwd,omitempty"`
    WorkspaceID    string         `json:"workspace_id"`
    Workspace      string         `json:"workspace,omitempty"`
    Status         AgentStatus    `json:"status"`
    Focused        bool           `json:"focused"`
    StateChangeSeq uint64         `json:"state_change_seq"`
    Prompt         *PendingPrompt `json:"prompt,omitempty"`
    BackgroundAgents int          `json:"background_agents,omitempty"`
    UpdatedAt      string         `json:"updated_at"`
}
```

| Field | Source (bridge side) | Rule |
|---|---|---|
| `pane_id` | herdr `pane_id` | Primary key everywhere. Contains `:` (e.g. `w5:pAW`) |
| `agent` | herdr `agent` | Lowercase herdr id: `claude`, `agy`, `opencode`, `codex`, … Never empty: a pane whose herdr `agent` is null is not tracked (the relay gets `agent_removed`) |
| `label` | computed | The name the owner gave, first non-empty of: herdr `name`; the tab's `label` from `tab.list` (ignored when it is only the tab's `number`, herdr's default); `title`; `basename(cwd)`; `pane_id`. Agents that share a tab share its label |
| `name` | herdr `name` | Explicit pane name or slug if set, otherwise omitted |
| `title` | herdr `terminal_title_stripped` | The agent's own task title (e.g. the one Claude Code sets); omitted when empty or when it only names the program (`OpenCode`, `agy --conversation …`) |
| `cwd` | herdr `foreground_cwd`, else `cwd` | Omit if both are null |
| `workspace_id` | herdr `workspace_id` | Internal workspace ID |
| `workspace` | herdr workspace label | Human workspace name (e.g. `my-project`), otherwise omitted |
| `status` | herdr `agent_status` | |
| `focused` | herdr `focused` | |
| `state_change_seq` | herdr `state_change_seq` | Used as the optimistic-concurrency token for commands |
| `prompt` | parsed by the adapter | Present **only** when `status == "blocked"`, otherwise omitted |
| `background_agents` | the adapter's turn-end reader (Claude only: `agents.md` §3.4) | The background agents (sub-agents) the agent's last turn left running, **while the agent only waits on them**: its turn has ended, no newer turn has started, and herdr still reports `working`. Present **only** with `status == "working"` and a count > 0, otherwise omitted (0). Refreshed by the bridge's periodic turn check, so it can lag up to that interval (`agents.md` §1). Clients show such an agent as done with the count; `status` stays herdr's |
| `updated_at` | bridge clock | Set each time the bridge rebuilds the state from a herdr change, publishes a newly parsed prompt or a new `background_agents`; a workspace rename alone does not change it |

**Example:**

```json
{
  "pane_id": "w5:pAE",
  "agent": "claude",
  "label": "my-app",
  "name": "my-app",
  "title": "Fix the login loop",
  "cwd": "/Users/me/Code/app",
  "workspace_id": "w5",
  "workspace": "work",
  "status": "blocked",
  "focused": false,
  "state_change_seq": 334,
  "prompt": {
    "kind": "permission",
    "title": "Bash command",
    "detail": "go test ./...",
    "options": [
      { "id": "opt-1", "label": "Yes", "role": "allow_once" },
      { "id": "opt-2", "label": "Yes, and don't ask again for go test commands", "role": "allow_always" },
      { "id": "opt-3", "label": "No, and tell Claude what to do differently", "role": "deny" }
    ],
    "fingerprint": "fd6ff7388739252d"
  },
  "updated_at": "2026-09-23T17:04:05Z"
}
```

A Claude agent whose turn ended while two of its background agents still run (only the fields that differ):

```json
{ "status": "working", "background_agents": 2 }
```

The `fingerprint` above is the real `model.Fingerprint` of this prompt (§1.3); `TestFingerprintKnownAnswers` in `pkg/agents` checks it. The golden file `pkg/model/testdata/agent_state.json` carries the same value.

### 1.3 `PendingPrompt` and `PromptOption`

```go
type PromptKind string

const (
    PromptPermission PromptKind = "permission" // allow/deny style menu
    PromptQuestion   PromptKind = "question"   // multiple choice (AskUserQuestion, plan approval, …)
    PromptUnknown    PromptKind = "unknown"    // could not parse; RawTail is set
)

type OptionRole string

const (
    RoleAllowOnce   OptionRole = "allow_once"
    RoleAllowAlways OptionRole = "allow_always"
    RoleDeny        OptionRole = "deny"
    RoleChoice      OptionRole = "choice"
)

type PromptOption struct {
    ID          string     `json:"id"`
    Label       string     `json:"label"`
    Description string     `json:"description,omitempty"` // lines printed under the label, if any
    Role        OptionRole `json:"role"`
}

type PendingPrompt struct {
    Kind        PromptKind     `json:"kind"`
    Title       string         `json:"title"`
    Detail      string         `json:"detail,omitempty"`
    Options     []PromptOption `json:"options"`
    Fingerprint string         `json:"fingerprint"`
    RawTail     string         `json:"raw_tail,omitempty"`
}
```

**Rules:**
- **`kind` is decided by the roles.**
  - `permission` if at least one option is `allow_once` or `allow_always` **and** at least one is `deny`.
  - `question` if options were parsed but that condition fails.
  - `unknown` if no options were parsed.
- **`id` is `opt-<n>`**, where `<n>` is the number printed in the menu. If the menu has no numbers, `<n>` is the 1-based position.
- **`options` is never null.** For `unknown` it is an empty array `[]`.
- **`raw_tail` is set only for `unknown`.** It holds the last 12 non-empty lines of the visible screen, each trimmed of trailing spaces.
- **`label` and `description`:** `label` is the option's first line; `description` holds the lines printed under it (the description Claude and OpenCode questions show for each answer, or the rest of a label that wrapped), joined with spaces. It is omitted when there are none. An option's **text** is `label`, plus a space and `description` when present: roles are derived from the text (a "don't ask again" on the second line still makes `allow_always`). Exception: OpenCode's buttons have fixed roles (agents.md §5.1).
- **Options the watch cannot answer are left out**: a free-text entry (Claude's "Type something.", OpenCode's "Type your own answer") opens a text field that keys cannot fill. The other options keep their ids.
- **`fingerprint`** is the first 16 hex characters of `sha256(kind + "\n" + title + "\n" + detail + "\n" + text_1 + "\n" + … + text_n)`, with each option's text as defined above. It lets the bridge detect that the menu on screen changed between display and tap.
- **Keys are never part of this object.** The bridge keeps the option → keys map privately.

### 1.4 `HistoryItem`

```go
type HistoryItem struct {
    ID          string `json:"id"`
    PaneID      string `json:"pane_id"`
    Agent       string `json:"agent"`
    Label       string `json:"label"`
    Query       string `json:"query,omitempty"`
    Response    string `json:"response"`
    Source      string `json:"source"` // "transcript" | "screen"
    CompletedAt string `json:"completed_at"`
}
```

- **`id`** is the first 16 hex characters of `sha256(pane_id + "\n" + session_value + "\n" + query + "\n" + response)`. It is deterministic, so a resent item deduplicates on the relay.
- **`response`** is truncated to 65 536 bytes (`model.MaxResponseBytes`; why that bound: its comment) on a UTF-8 boundary, with `\n\n…[truncated]` appended when cut.
- **`source`:**
  - `"transcript"` means `response` is the agent's own markdown.
  - `"screen"` means `response` is plain terminal text, so clients must not render it as markdown. One exception: a table the agent drew with box characters arrives as a markdown pipe table (header, `| --- |` separator, one line per row), and clients render those lines as a table (agents.md §6).

### 1.5 `AgentsSnapshot`

```go
type AgentsSnapshot struct {
    HostOnline  bool         `json:"host_online"`
    HerdrOnline bool         `json:"herdr_online"`
    Agents      []AgentState `json:"agents"`
    GeneratedAt string       `json:"generated_at"`
}
```

- **`agents` order:** by severity (see 1.1), then `label` ascending. It is never null.
- **`host_online=false`:** the bridge is not connected to the relay. `agents` is then the last known list; clients grey it out.
- **`herdr_online=false`:** the bridge is connected, but cannot reach herdr.

---

## 2. Watch-facing HTTP API (`pkg/model/api.go`)

Base URL: `https://relay.<domain>`. All paths are prefixed with `/v1`.

**Authentication:**

| Caller | Header |
|---|---|
| Watch (every endpoint except `POST /v1/pair` and `GET /v1/healthz`) | `Authorization: Bearer <device_token>` |
| Bridge (`/v1/host` WebSocket and `/v1/host/*`) | `Authorization: Bearer <host_token>` |

Never accept a token in the query string.

**`{pane_id}` in paths is percent-encoded by clients** (`w5:pAW` → `w5%3ApAW`). In Go, read it with `r.PathValue("pane_id")`, which returns it decoded.

### 2.1 Endpoints

| Method + path | Auth | Request body | Success response |
|---|---|---|---|
| `POST /v1/pair` | none (rate-limited) | `PairRequest` | `200 PairResponse` |
| `POST /v1/host/pair-code` | host | — | `200 PairCodeResponse` |
| `GET /v1/host/status` | host | — | `200 HostStatusResponse` |
| `GET /v1/agents` | device | — | `200 AgentsSnapshot` |
| `GET /v1/events` | device | — | `200 text/event-stream` (see 2.3) |
| `GET /v1/history?pane_id=&limit=` | device | — | `200 HistoryResponse` |
| `POST /v1/agents/{pane_id}/prompt` | device | `PromptRequest` | `200 CommandResponse` |
| `POST /v1/agents/{pane_id}/answer` | device | `AnswerRequest` | `200 CommandResponse` |
| `POST /v1/agents/{pane_id}/cancel` | device | `CancelRequest` | `200 CommandResponse` |
| `POST /v1/push/register` | device | `PushRegisterRequest` | `200 CommandResponse` |
| `GET /v1/healthz` | none | — | `200 {"ok":true}` |

`GET /v1/history` parameters:
- `pane_id` is optional. Without it, the response mixes all panes.
- `limit` defaults to 20, maximum 200. A missing, invalid or ≤ 0 value means 20.
- Items are sorted newest first.

**History retention** (`pkg/relay/store.go`): the relay keeps at most 20 items per pane and 200 in total (the oldest go first), and, each time a history item arrives, drops every pane whose newest item is older than 7 days. So a request with `pane_id` never returns more than 20.

**Pairing limits** (`pkg/relay/auth.go`): a code lives 5 minutes and works once; at most 3 codes are active (a new one evicts the oldest). `POST /v1/pair` allows 5 attempts per client IP and 20 in total per 10 minutes, and counts every attempt it lets through, successful ones included; past that it answers `429 rate_limited`.

### 2.2 Bodies

```go
type PairRequest struct {
    Code       string `json:"code"`        // 6 digits
    DeviceName string `json:"device_name"` // e.g. "Pixel Watch 2"
}
type PairResponse struct {
    DeviceID    string `json:"device_id"`
    DeviceToken string `json:"device_token"` // 64 hex chars; shown once, stored hashed
}
type PairCodeResponse struct {
    Code      string `json:"code"`
    ExpiresAt string `json:"expires_at"` // now + 5 minutes
}
type HostStatusResponse struct {
    HostOnline  bool `json:"host_online"`
    HerdrOnline bool `json:"herdr_online"`
    Devices     int  `json:"devices"`
    Agents      int  `json:"agents"`
}
type HistoryResponse struct {
    Items []HistoryItem `json:"items"`
}
type PromptRequest struct {
    Text        string `json:"text"`         // 1..4000 characters (Unicode code points, not bytes)
    ExpectedSeq uint64 `json:"expected_seq"`
}
type AnswerRequest struct {
    OptionID    string `json:"option_id"`
    ExpectedSeq uint64 `json:"expected_seq"`
    Fingerprint string `json:"fingerprint"`
}
type CancelRequest struct {
    ExpectedSeq uint64 `json:"expected_seq"`
    Fingerprint string `json:"fingerprint,omitempty"` // optional: the prompt the watch showed
}
type PushRegisterRequest struct {
    Platform string `json:"platform"` // only "fcm" for now
    Token    string `json:"token"`
}
type CommandResponse struct {
    OK bool `json:"ok"` // always true on 200
}
type ErrorResponse struct {
    Error ErrorBody `json:"error"`
}
type ErrorBody struct {
    Code    string `json:"code"`
    Message string `json:"message"`
}
```

**Command body fields** (the relay copies them into `CommandMsg`, §3; the bridge re-validates them):

| Field | Bodies | Rule |
|---|---|---|
| `text` | prompt | 1 to 4000 **characters**, counted as Unicode code points after JSON decoding (`é` and `😀` count 1 each), never bytes. Outside that range, or blank (whitespace only) → `invalid_request` (blank text is refused by the bridge). The prompt body may be up to 64 KiB, so 4000 characters fit however the client escapes them |
| `expected_seq` | prompt, answer, cancel | The `state_change_seq` the watch showed. A mismatch → `stale_state` |
| `option_id` | answer | An `id` from `prompt.options`. Not in the current prompt → `unknown_option` |
| `fingerprint` | answer (required), cancel (optional) | The `prompt.fingerprint` the watch showed. A mismatch → `prompt_changed`. On cancel, when it is omitted the bridge compares against the prompt it published for `expected_seq` |

**Example** (`POST /v1/agents/w5%3ApAE/cancel`):

```json
{ "expected_seq": 334, "fingerprint": "fd6ff7388739252d" }
```

### 2.3 Server-Sent Events (`GET /v1/events`)

Response headers:
- `Content-Type: text/event-stream`
- `Cache-Control: no-cache`
- `X-Accel-Buffering: no`

Every 15 s the relay writes the comment line `:` followed by a blank line, as a keepalive. Each write has a 10 s deadline: a client that stops reading is disconnected (it reconnects), and a slow one never blocks the others.

| `event:` | `data:` (one JSON line) | When |
|---|---|---|
| `snapshot` | `AgentsSnapshot` | Immediately on connect, and after the host reconnects |
| `agent` | `AgentState` | An agent was added or changed |
| `agent_removed` | `{"pane_id": "..."}` | A pane closed or lost its agent |
| `host` | `{"host_online": bool, "herdr_online": bool}` | Either flag changed |
| `history` | `HistoryItem` | A new history item was stored (never for a resent duplicate, same `id`) |

**Client rules:**
- On any disconnect, reconnect with backoff (1 s, 2 s, 4 s … max 30 s) and **replace** local state with the next `snapshot`.
- **Silence is a disconnect.** A stream with no bytes for 45 s (three missed keepalives) is dead, often a half-open socket: drop it and reconnect. The Wear OS app does this with the SSE read timeout, and marks its list stale until the next `snapshot`.

### 2.4 Error codes

Every non-200 response of the endpoints in §2.1 carries an `ErrorResponse`. A path or method the relay does not serve gets Go's plain-text `404`/`405`, and `/v1/host` answers a plain-text `503` while the relay shuts down: decode the body only when it is JSON.

| Code | HTTP | Meaning |
|---|---|---|
| `invalid_request` | 400 | Malformed body or bad parameter |
| `unauthorized` | 401 | Missing or unknown token |
| `unknown_pane` | 404 | Pane is not in the relay's list; or herdr's list no longer has it, or it has no agent (bridge) |
| `stale_state` | 409 | `expected_seq` no longer matches; also `answer`/`cancel` to an agent that is not `blocked`, or a repeated command |
| `prompt_changed` | 409 | Fingerprint mismatch; the menu is no longer on the screen; a cancel without `fingerprint` for which the bridge published no prompt; or, for OpenCode, the focus on the host is not on the button the keys assume (the `message` says to answer on the Mac; nothing was pressed) |
| `agent_busy` | 409 | Prompt sent while `working` to an agent that cannot queue |
| `agent_blocked` | 409 | Prompt sent while `blocked`, or while a menu is open on the pane although herdr reports another status, or herdr refused the prompt as blocked |
| `agent_state_unknown` | 409 | Prompt sent while the agent's state does not accept prompts (e.g. `unknown`) |
| `unknown_option` | 409 | `option_id` is not in the current prompt |
| `pair_code_invalid` | 403 | Wrong or expired code |
| `rate_limited` | 429 | Too many attempts |
| `host_offline` | 503 | Bridge not connected, or it disconnected (or was replaced) before answering a command |
| `herdr_offline` | 503 | Bridge connected, herdr unreachable |
| `timeout` | 504 | The relay's 7 s budget ran out (one budget for sending the command and waiting for `command_result`), or the bridge's own 6 s budget did (e.g. waiting behind another command on the same pane). Timeouts nest from the inside out, bridge 6 s < relay 7 s < watch 8 s, so a `timeout` means the bridge has already given up |
| `internal` | 500 | Bug, or herdr failed `agent.prompt` / `agent.send_keys` for an unclassified reason (the text or keys may have reached the pane) |

What the Wear OS app shows for each code: `commandErrorFeedback` in `wearos-app/app/src/main/java/com/gabriel/agentwatch/approval/CommandFeedback.kt`.

---

## 3. Bridge ↔ relay WebSocket (`pkg/model/wire.go`)

URL: `wss://relay.<domain>/v1/host`, with the header `Authorization: Bearer <host_token>`.

**Handshake response:** once the host token is verified, the relay's `101 Switching Protocols` carries its version in `X-Agent-Watch-Relay-Version: <version>` (format below; absent from a relay that predates it). A rejected handshake (`401`) never carries it, and no watch-facing endpoint exposes it (`/v1/healthz` stays `{"ok":true}`). The bridge keeps the value of its last successful handshake and reports it as `relay_version` (§6.1, §6.2).

**Version strings** (`hello.version`, the relay header, `status.json`, `status --json`, the `version` commands): the component's version from `VERSIONS` at the repo root, plus the commit the binary was built from, which Go stamps on its own: `x.y.z (<commit>)`, `x.y.z (<commit>, modified)` for a build with uncommitted changes, or just `x.y.z` for a build without the VCS stamp. A plain `go build` without the Makefile's ldflags reports `dev (…)`. They are display strings: never parse or compare them for ordering.

- Every frame is one JSON text message with a `type` field.
- To decode, first unmarshal into `struct{ Type string \`json:"type"\` }`, then unmarshal again into the concrete type.
- **Only one host may be connected.** A second connection replaces the first: the relay closes the old one with status 4000 `replaced`.

```go
type HelloMsg struct {
    Type          string `json:"type"` // "hello"
    Version       string `json:"version"`        // bridge version with its commit, e.g. "x.y.z (<commit>)"
    Host          string `json:"host"`           // host_name or os.Hostname()
    HerdrVersion  string `json:"herdr_version"`  // from herdr "ping"
    HerdrProtocol int    `json:"herdr_protocol"` // from herdr "ping"
    HerdrOnline   bool   `json:"herdr_online"`
}
type HerdrStatusMsg struct {
    Type        string `json:"type"` // "herdr_status"
    HerdrOnline bool   `json:"herdr_online"`
}
type HostPresenceMsg struct {
    Type        string `json:"type"` // "host_presence"
    IdleSeconds uint64 `json:"idle_seconds"`
    Locked      bool   `json:"locked"`
}
type SnapshotMsg struct {
    Type   string       `json:"type"` // "snapshot"
    Agents []AgentState `json:"agents"`
}
type AgentUpdateMsg struct {
    Type  string     `json:"type"` // "agent_update"
    Agent AgentState `json:"agent"`
}
type AgentRemovedMsg struct {
    Type   string `json:"type"` // "agent_removed"
    PaneID string `json:"pane_id"`
}
type HistoryItemMsg struct {
    Type string      `json:"type"` // "history_item"
    Item HistoryItem `json:"item"`
}
type CommandMsg struct {
    Type        string `json:"type"`       // "command"
    RequestID   string `json:"request_id"` // random 16 hex chars
    Action      string `json:"action"`     // "prompt" | "answer" | "cancel"
    PaneID      string `json:"pane_id"`
    ExpectedSeq uint64 `json:"expected_seq"`
    Text        string `json:"text,omitempty"`        // prompt
    OptionID    string `json:"option_id,omitempty"`   // answer
    Fingerprint string `json:"fingerprint,omitempty"` // answer; cancel when the watch sent one
}
type CommandResultMsg struct {
    Type      string `json:"type"` // "command_result"
    RequestID string `json:"request_id"`
    OK        bool   `json:"ok"`
    ErrorCode string `json:"error_code,omitempty"` // codes from §2.4
    Message   string `json:"message,omitempty"`
}
type ResyncMsg struct {
    Type string `json:"type"` // "resync"
}
```

**Sequence on connect:**

1. The bridge sends `hello`.
2. The bridge sends `snapshot`.
3. From then on, it sends `agent_update`, `agent_removed`, `history_item` and `herdr_status` as things change.
4. On macOS the bridge sends `host_presence` right after `snapshot`, then every 15 s while connected (the relay's use: §4.3). Each read is bounded to 2 s (`presenceTimeout`, `pkg/bridge/presence.go`); a failed read sends nothing.

On a `resync` the bridge sends `hello` and `snapshot` again (the current relay never sends one).

**Relay rules:**
- If no `hello` arrives within 5 s, close the socket with status 4001.
- Reply to unknown `type` values by ignoring them (forward compatibility).

**Keepalive:** both sides send a WebSocket ping every 30 s and expect the pong within 10 s.
- **Bridge:** no pong → it reconnects.
- **Relay:** no pong → it drops the host: closes the socket, broadcasts `host` with `host_online=false`, and fails the host's in-flight commands with `host_offline`.

**In-flight commands** fail with `host_offline` as soon as their host disconnects, is dropped, or is replaced by a new connection, without waiting for the command budget (§2.4).

**Reconnect backoff** (bridge side): 1 s, 2 s, 4 s … up to 60 s, each ±20 % jitter. The backoff resets after 60 s of healthy connection. A host token the relay rejects (401/403) waits the maximum backoff (60 s, same jitter) before the next try.

---

## 4. Push payloads (`pkg/push`)

### 4.1 FCM (Wear OS) — data-only message

All values are strings (FCM data maps allow only strings). No `notification` block: the app builds the notification itself.

| Key | Example | Notes |
|---|---|---|
| `event` | `blocked` | `blocked` \| `done` \| `digest` (and `resolved`, below) |
| `pane_id` | `w5:pAE` | Empty for `digest` |
| `agent` | `claude` | |
| `label` | `my-app` | |
| `title` | `my-app needs approval` | Ready to display |
| `body` | `Bash command: go test ./...` | ≤ 240 chars. For `blocked`: `<title>: <detail>` (the title alone without a detail), or a fixed invitation to open the app for an `unknown` prompt. For `done`: the agent's reply as one line (markdown markers dropped; a markdown table becomes one line per row, `first cell: other cells · …`, without its header); a longer reply is its first line (≤ 110 chars), ` … ` and its end (`replyPreview`, `pkg/push/push.go`); or `Task finished` when no reply arrived in time (§4.3) |
| `state_change_seq` | `334` | Decimal string, always a number (`0` for `digest`) |
| `fingerprint` | `fd6ff7388739252d` | Empty except for `blocked` |
| `allow_option_id` | `opt-1` | First `allow_once` option, else empty |
| `deny_option_id` | `opt-3` | First `deny` option, else empty |
| `kind` | `question` | Only for `blocked`: the prompt's kind (`permission`, `question`, `unknown`) |
| `options` | `[{"id":"opt-1","label":"Rojo"}]` | Only for `blocked`: a `question`'s one-tap answers as a JSON array string, `""` otherwise. At most 4, in menu order, never an `allow_always` option (the notification cannot ask for the confirmation the app asks for); labels ≤ 40 characters |

**`resolved`: withdraw a `blocked` notification.**

> ⚠️ **Disabled by default.** The relay sends `resolved` only when the FCM sender's `EnableResolved` is set (`AW_PUSH_RESOLVED`, §5). Enable it **only once every installed watch app handles `resolved`**: older apps show an unknown event as an approval on the pane's notification id, which replaces a real approval with a bogus one. Whether it is on: `docs/STATUS.md`.

It carries **only** these three keys, and never a notification block:

```json
{"message":{"token":"<device fcm token>",
            "data":{"event":"resolved","pane_id":"w5:pAE","state_change_seq":"335"},
            "android":{"priority":"normal","ttl":"600s"}}}
```

| Key | Example | Notes |
|---|---|---|
| `event` | `resolved` | |
| `pane_id` | `w5:pAE` | The pane whose `blocked` notification the app removes |
| `state_change_seq` | `335` | Seq of the update that left `blocked`, so greater than the `blocked` push's seq |

- FCM does not guarantee delivery order. The app ignores a `blocked` for a pane whose `state_change_seq` is ≤ the last `resolved` seq it got for that pane.
- Apps must ignore an `event` value they do not know, like unknown JSON fields.

FCM message options:
- `android.priority = "high"` for `blocked` and for a `digest` covering at least one `blocked` agent (otherwise Doze may hold an approval). `"normal"` otherwise: `done`, a `done`-only `digest`, `resolved`.
- `android.ttl = "600s"`.

**Dead tokens:** the relay unregisters a device's FCM token only when FCM says the token itself is dead:
- error code `UNREGISTERED`, on a 404 or a 400;
- or `400 INVALID_ARGUMENT` whose field violation is `message.token`.

Any other error keeps the token, a bare 404 included (a wrong project id must not wipe every device).

### 4.2 ntfy (watchOS via iPhone)

`POST {AW_NTFY_URL}/{AW_NTFY_TOPIC}`. The body is the plain-text message; everything else goes in headers.

| Header | Value |
|---|---|
| `Title` | Same as FCM `title` |
| `Priority` | `5` for `blocked`, `3` for `done`, `4` for `digest` |
| `Tags` | `warning` for `blocked`, `white_check_mark` for `done`, `bell` for `digest` |
| `Authorization` | `Bearer {AW_NTFY_TOKEN}` (only when the token is set) |

ntfy never gets `resolved`: it cannot withdraw a notification it already delivered. ntfy messages carry no actions and no click URL: approvals happen in the watch app.

### 4.3 When to push (relay)

| Transition (per pane, from `agent_update`) | Event |
|---|---|
| any → `blocked` | `blocked` |
| `working` → `done` | `done` |
| `blocked` → any other status, when that pane's own `blocked` push went out | `resolved` (FCM only, and only when enabled: §4.1) |
| anything else (e.g. `working` → `working` with a new `background_agents`) | no push |

- **`resolved`** goes out at once, once per `blocked` push:
  - no debounce, no window, never counted toward or included in a `digest`, never sent to ntfy;
  - a pane whose `blocked` push was dropped, or covered by a `digest`, gets none: the watch shows no notification of its own for it.
- **Removed panes:** push only sees `agent_update`.
  - A pane that leaves `blocked` through a `snapshot` gets its `resolved` with its next `agent_update`.
  - A pane removed while blocked (`agent_removed`, or missing from a `snapshot`) gets none; its notification stays until dismissed.

- **Held back while the owner is at the host** (`AW_PUSH_PRESENCE_IDLE`, §5): while the host's last `host_presence` (§3) is under 45 s old, its screen is unlocked and its last input is under that threshold, `blocked` and `done` pushes are not sent. When presence ends (the threshold passes, the screen locks, or no report for 45 s), each pane whose `blocked` push was held back and that the relay still shows `blocked` gets one, built from its current state, through the debounce, window and digest below. A held-back `done` is not sent later. A host that disconnects ends presence without a catch-up; what was held back waits for its next report (a reconnected bridge reports right after its snapshot). A relay restart forgets it. A push already held in a window, or a `done` waiting for its reply, still goes out when its wait ends, whatever the presence is then. A host that never reports (Linux, an older bridge) is never held back.
- **Turns that end while the pane stays `working`** (background agents still running, §1.2) push nothing: their reply reaches the watch as a `history_item`, and the agent shows as done with its count. The `done` push comes once herdr reports `working` → `done`, after the last background agent's report turn, with that turn's reply. Why: a coordinator gets one report turn per finished agent, and a push for each would be noise.
- **`done` waits for the reply:** the bridge sends the turn's `history_item` right after the transition. The relay holds the `done` push up to 3 s for that pane's next new history item and uses its response as the body; when none arrives, it pushes with `Task finished`. The debounce and the window below apply when it goes out.
- **Debounce** (the same pane pushed the same event less than 5 s ago):
  - `done`: skip it. The pane's notification already says it finished.
  - `blocked`: **hold it** until the window ends (trailing edge), never drop it. A new prompt right after an answer is never lost.
- **No duplicates:** a `blocked` push is never sent for the prompt the pane's notification already shows (same `state_change_seq` **and** `fingerprint`). This applies at once and at the end of the window.
- **Window:** the first push of a 10 s window goes out at once (latency matters for `blocked`). Later ones are held until the window ends.
  - A push held by the debounce opens a window if none is open.
  - The first push that is not held still goes out at once.
- **End of the window:** each held agent gets at most one push, built from its **current** state, and only if it is still in the state a held push announced (`blocked` or `done`).
  - An agent that left that state gets nothing: the watch never offers to approve a prompt that was already answered.
  - An agent that answered and blocked again gets the push for its current prompt (fingerprint and option ids included).
- **Digest:** if more than 3 pushes would go out in the window (the first one included), the held ones go out as one `digest` push instead:
  - title `N agents need you` if any of them is `blocked`, `N agents finished` if all are `done`;
  - N counts **agents**, not messages;
  - body = the distinct labels joined with `, `, ≤ 240 chars.

---

## 5. Relay configuration (environment variables)

The relay reads them from `/etc/agent-watch-relay/env` (systemd `EnvironmentFile=`). `make deploy-relay ARGS=--sync-env` can set them there from `agent-watch.env` (`deploy/relay/README.md` § `--sync-env`).

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `AW_LISTEN` | no | `:8080` | Listen address. Bind it to the address the reverse proxy reaches, never to a public interface (the value per proxy topology: `deploy/relay/README.md`) |
| `AW_HOST_TOKEN` | **yes** | — | 64 hex chars; the same value goes in the bridge config |
| `AW_DATA_DIR` | no | `/var/lib/agent-watch-relay` | Holds `store.json`, `relay.lock` and `admin.sock` (see below) |
| `AW_FCM_CREDENTIALS` | no | — | Path to the Firebase service-account JSON. FCM is disabled if unset |
| `AW_NTFY_URL` | no | — | e.g. `https://ntfy.sh`. ntfy is disabled if unset |
| `AW_NTFY_TOPIC` | with ntfy | — | Random, unguessable topic name |
| `AW_NTFY_TOKEN` | no | — | ntfy access token |
| `AW_PUSH_RESOLVED` | no | `false` | `1`/`true` sends the FCM `resolved` push that withdraws an answered approval (§4.1 says when it is safe to enable) |
| `AW_PUSH_PRESENCE_IDLE` | no | `10m` | Input idle time on the host after which the owner counts as away; pushes are held back while they are there (§4.3). A Go duration (`90s`, `10m`); `0` turns it off |
| `AW_TRUSTED_PROXIES` | no | empty | Comma-separated CIDRs (a bare IP counts as one host) of the reverse proxies allowed to report the client IP. Empty: the TCP peer address is the client IP and every forwarding header is ignored |
| `AW_CLIENT_IP_HEADER` | no | empty | A single-IP header, e.g. `CF-Connecting-IP`, honored from a trusted proxy before `X-Forwarded-For`. Requires `AW_TRUSTED_PROXIES`. Set it only when nothing but that CDN can reach the proxy, or clients can spoof it |

**Client IP** (used by the `POST /v1/pair` rate limiter):

1. The TCP peer is not in `AW_TRUSTED_PROXIES` → the peer address. Headers are ignored.
2. The peer is trusted → the first usable of:
   - `AW_CLIENT_IP_HEADER`, when set and a valid IP;
   - the rightmost `X-Forwarded-For` entry that is not itself a trusted proxy (entries left of it are client-supplied and never used; a malformed entry stops the walk);
   - `X-Real-IP`;
   - the peer address.

`AW_TRUST_CF_IP` was removed. If it is still set, the relay ignores it and logs one warning at startup.

**Files in `AW_DATA_DIR`** (the directory is `0700`):

| File | Meaning |
|---|---|
| `store.json` | Devices (device-token hashes only, plus each device's FCM push token) and history. `0600`, owned by the service user |
| `relay.lock` | Exclusive `flock` held by the running relay for its whole life. A second relay on the same directory refuses to start |
| `admin.sock` | Local admin API, a `0600` Unix socket that exists only while the relay runs. Never exposed over TCP |

**Admin API on `admin.sock`** (`pkg/relay/admin.go`; used by `agent-watch-relay devices`, whose behaviour is in `deploy/relay/README.md` § Devices):

| Request | Response |
|---|---|
| `GET /devices` | `200`, a JSON array of `{"id", "name", "created_at", "last_seen"}`: never the token hash or the push token |
| `DELETE /devices/{id}` | `204`; `404 {"error": "…"}` for an unknown id; `500 {"error": "…"}` when the store cannot be saved |

---

## 6. Bridge configuration (`config.toml`)

- **Location:** `$HERDR_PLUGIN_CONFIG_DIR/config.toml`. For the plugin id `herdr-agent-watch` this is `~/.config/herdr/plugins/config/herdr-agent-watch/config.toml`.
- **Mode:** `0600`, in a `0700` directory. Both are enforced on every write, also for a file that already exists.
- **Written by:** `agent-watch-bridge configure` (atomically). Without `--config` it writes the config herdr's environment names, else the one the installed service uses, else the default above.
  - `--env-file <agent-watch.env>` (what `make configure-bridge` runs) takes `relay_url = wss://<AW_RELAY_DOMAIN>/v1/host` and `host_token = AW_HOST_TOKEN` from the file (§7), so the token never appears in argv. `--relay-url` / `--host-token` override it value by value.
  - It rewrites the whole file. When that drops a previous `host_name` or `claude_config_dirs`, it prints a `note:` on stderr; pass `--host-name` / `--claude-config-dir` again to keep them.
- **`relay_url`:** `wss://` (`ws://` only for `localhost` / `127.0.0.1`). A URL without a path gets `/v1/host` appended.

```toml
relay_url  = "wss://relay.example.com/v1/host"   # https:// is derived for /v1/host/* calls
host_token = "…64 hex chars…"
host_name  = ""                          # empty → os.Hostname()
claude_config_dirs = []                  # extra Claude profiles; which ones are found without it: agents.md §3.2
```

### 6.1 `status.json` (written by `agent-watch-bridge run`)

- **Path:** `$HERDR_PLUGIN_STATE_DIR/status.json`, else `~/.local/state/agent-watch/status.json`. The installed service pins `HERDR_PLUGIN_STATE_DIR` in its definition, and `status` reads the installed service's state dir first.
- **Written** atomically at start, every 5 s, and once more on exit.
- **Every key is always present** (no `omitempty`): older readers decode the first six unconditionally.

```json
{ "pid": 4242, "relay_connected": true, "herdr_online": true, "agents": 11, "blocked": 1,
  "last_error": "", "relay_error": "", "herdr_error": "", "version": "x.y.z (<commit>)",
  "relay_version": "x.y.z (<commit>)", "updated_at": "2026-09-23T17:04:05Z" }
```

| Key | Meaning |
|---|---|
| `pid` | The running bridge's pid. **`0` means not running**: a clean stop writes `pid: 0` with an empty `last_error`; a start that failed (e.g. an invalid config) writes `pid: 0` with the reason in `last_error` |
| `relay_connected`, `herdr_online` | Link state |
| `agents`, `blocked` | Agents tracked; how many are `blocked` |
| `relay_error` | Why the relay is not connected (a rejected token, a failed dial). Never contains the token |
| `herdr_error` | Why herdr is offline |
| `last_error` | `relay_error` and `herdr_error` joined with `; `, or the start failure. `""` when healthy |
| `version` | Version of the bridge that wrote the file, with its commit (§3 version strings) |
| `relay_version` | The relay's version from the last successful handshake (§3), kept while the relay is unreachable. `""` before the first connection, from a relay that does not send it, and in the stopped status (`pid: 0`) |

A reader must not trust `pid` alone: a file left by a crash names a dead pid. `status` checks that the pid is alive and that the file is fresh (15 s).

### 6.2 `agent-watch-bridge status --json [--local]`

`--local` reads local files only (service definition, config, `status.json`, a pid check): no network and no `launchctl` / `systemctl`. The macOS menu bar polls it. Every key is always present.

```json
{
  "installed": true, "definition_error": "",
  "configured": true, "config_error": "",
  "running": true, "stale": false,
  "relay_connected": true, "herdr_online": true,
  "agents": 11, "blocked": 1,
  "last_error": "", "relay_error": "", "herdr_error": "",
  "relay_host": "relay.example.com",
  "pid": 4242, "updated_at": "2026-09-23T17:04:05Z", "age_seconds": 3,
  "version": "x.y.z (<commit>)", "daemon_version": "x.y.z (<commit>)",
  "relay_version": "x.y.z (<commit>)",
  "service": "launchd",
  "definition_path": "/Users/me/Library/LaunchAgents/com.gabrielmarcano.agent-watch-bridge.plist",
  "binary": "/Users/me/Code/agent-watch/bin/agent-watch-bridge",
  "config_path": "/Users/me/.config/herdr/plugins/config/herdr-agent-watch/config.toml",
  "state_dir": "/Users/me/.local/state/agent-watch",
  "status_path": "/Users/me/.local/state/agent-watch/status.json",
  "log_path": "/Users/me/Library/Logs/agent-watch-bridge.log"
}
```

- **`running`:** `status.json` names a live pid. While not running, `relay_connected`, `relay_version`, `herdr_online`, `agents` and `blocked` are reported as `false` / `""` / `0`, whatever the file says.
- **`stale`:** running, but `status.json` is older than 15 s.
- **`age_seconds`:** `-1` when unknown. **`version`** is the CLI; **`daemon_version`** is the bridge that wrote `status.json`. Both carry the commit (§3 version strings), so a CLI and a daemon built from different commits differ even with the same `BRIDGE_VERSION`.
- **`relay_host`:** `host[:port]` of `relay_url`, never a token. **`service`:** `launchd` or `systemd`; on Linux `log_path` is a `journalctl` command.
- **Without `--local`** the output adds `relay_status` (the relay's `HostStatusResponse`, §2.2) or `relay_status_error`.
- The human form (no `--json`) exits with status 1 when the bridge is not running.

### 6.3 `agent-watch-bridge pair --json`

```json
{"code":"417293","expires_at":"2026-09-23T17:09:05Z","expires_in_seconds":300,"relay_host":"relay.example.com"}
```

`expires_at` is the relay's `PairCodeResponse.expires_at`; `expires_in_seconds` is computed from it (`0` if it cannot be parsed or has passed). `pair` needs only the config and the relay, not a running bridge.

---

## 7. Shared configuration file (`agent-watch.env`)

One file at the repo root holds everything a deployment needs. `agent-watch.env.example` is committed and documents every key; `agent-watch.env` is git-ignored, mode `0600`, refused by the guards, and created by `make config` (which also generates `AW_HOST_TOKEN`).

**Syntax** (the Makefile `-include`s the file, so these are make's rules, and every reader applies them):

- `KEY=value`, one per line; no `export`, no quotes (the value is the rest of the line, trimmed, taken literally).
- `#` starts a comment anywhere on a line, so a value cannot contain `#`.
- The last assignment of a key wins.

| Key | Read by | Meaning |
|---|---|---|
| `AW_RELAY_DOMAIN` | bridge `configure --env-file`, Wear OS build, `make watchos-config` | Relay host (`host[:port]`, no scheme or path). Derived: `wss://<domain>/v1/host` (bridge), `https://<domain>` (watch pairing default) |
| `AW_HOST_TOKEN` | bridge `configure --env-file`, `deploy.sh --sync-env` | 64 hex chars shared by the relay (§5) and the bridge (§6). `make config` fills it when empty and never prints it |
| `AW_RELAY_SSH` | `make deploy-relay` / `deploy.sh` without a target | SSH target of the VPS (root) |
| `AW_RELAY_SSH_OPTS` | same | Extra `ssh`/`scp` options, word-split (`-i <key> -o Port=<n>`). `SSH_OPTS` in the environment overrides it |
| `AW_LISTEN`, `AW_TRUSTED_PROXIES`, `AW_CLIENT_IP_HEADER`, `AW_PUSH_RESOLVED`, `AW_PUSH_PRESENCE_IDLE`, `AW_FCM_CREDENTIALS`, `AW_NTFY_URL`, `AW_NTFY_TOPIC`, `AW_NTFY_TOKEN` | `deploy.sh --sync-env` only | Relay variables (§5). Commented out in the example; `AW_FCM_CREDENTIALS` is a path **on the server** |
| `AW_WATCHOS_BUNDLE_ID` | `make watchos-config` | Bundle id for the Phase 6 watchOS project. Empty: the project's own |

How `deploy.sh --sync-env` copies the relay keys to the server: `deploy/relay/README.md` § `--sync-env`.

**Wear OS:** `BuildConfig.DEFAULT_RELAY_URL` is `"https://<AW_RELAY_DOMAIN>"`, or `""` without the file or the key. A malformed domain fails the build. The application id stays `com.gabriel.agentwatch`.

**watchOS:** `make watchos-config` writes the git-ignored `watchos-app/Config.generated.xcconfig` (`AW_RELAY_DOMAIN`, `AW_RELAY_URL`, `PRODUCT_BUNDLE_IDENTIFIER` when set). Only the rewritten watchOS project reads it, as its base configuration (Phase 6; state in `docs/STATUS.md`).
