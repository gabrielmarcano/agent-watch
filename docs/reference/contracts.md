# Contracts Reference (source of truth for every JSON shape)

Every JSON object that crosses a process boundary is defined here. The following must all match this file exactly:

- the Go structs in `pkg/model/`
- the Kotlin models in `wearos-app/`
- the Swift models in `watchos-app/`

If you need a new field:

1. Change this file first.
2. Then change the Go structs.
3. Then change both clients (use the `schema-sync` skill).

**Conventions (apply everywhere):**
- JSON keys are `snake_case`.
- Timestamps are RFC 3339 in UTC, e.g. `2026-09-23T17:04:05Z`. In Go, produce them with `time.Now().UTC().Format(time.RFC3339)`.
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

**Severity order** (used by complications and push): `blocked > done > working > idle > unknown`.

### 1.2 `AgentState`

```go
type AgentState struct {
    PaneID         string         `json:"pane_id"`
    Agent          string         `json:"agent"`
    Label          string         `json:"label"`
    Name           string         `json:"name,omitempty"`
    CWD            string         `json:"cwd,omitempty"`
    WorkspaceID    string         `json:"workspace_id"`
    Workspace      string         `json:"workspace,omitempty"`
    Status         AgentStatus    `json:"status"`
    Focused        bool           `json:"focused"`
    StateChangeSeq uint64         `json:"state_change_seq"`
    Prompt         *PendingPrompt `json:"prompt,omitempty"`
    UpdatedAt      string         `json:"updated_at"`
}
```

| Field | Source (bridge side) | Rule |
|---|---|---|
| `pane_id` | herdr `pane_id` | Primary key everywhere. Contains `:` (e.g. `w5:pAW`) |
| `agent` | herdr `agent` | Lowercase herdr id: `claude`, `agy`, `opencode`, `codex`, … Empty string if herdr reports null |
| `label` | computed | First non-empty of: descriptive task `terminal_title_stripped`, herdr `name`, `basename(cwd)`, `pane_id` |
| `name` | herdr `name` | Explicit pane name or slug if set, otherwise omitted |
| `cwd` | herdr `foreground_cwd`, else `cwd` | Omit if both are null |
| `workspace_id` | herdr `workspace_id` | Internal workspace ID |
| `workspace` | herdr workspace label | Human workspace name (e.g. `my-project`), otherwise omitted |
| `status` | herdr `agent_status` | |
| `focused` | herdr `focused` | |
| `state_change_seq` | herdr `state_change_seq` | Used as the optimistic-concurrency token for commands |
| `prompt` | parsed by the adapter | Present **only** when `status == "blocked"`, otherwise omitted |
| `updated_at` | bridge clock | Set whenever any other field changes |

**Example:**

```json
{
  "pane_id": "w5:pAE",
  "agent": "claude",
  "label": "bizum",
  "name": "bizum",
  "cwd": "/Users/me/Code/app",
  "workspace_id": "w5",
  "workspace": "bizum-app",
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
    "fingerprint": "9f2c61d0a4b3e871"
  },
  "updated_at": "2026-09-23T17:04:05Z"
}
```

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
    ID    string     `json:"id"`
    Label string     `json:"label"`
    Role  OptionRole `json:"role"`
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
- **`fingerprint`** is the first 16 hex characters of `sha256(kind + "\n" + title + "\n" + detail + "\n" + label_1 + "\n" + … + label_n)`. It lets the bridge detect that the menu on screen changed between display and tap.
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
- **`response`** is truncated to 16 384 bytes on a UTF-8 boundary, with `\n\n…[truncated]` appended when cut.
- **`source`:**
  - `"transcript"` means `response` is the agent's own markdown.
  - `"screen"` means `response` is plain terminal text, so clients must not render it as markdown.

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
| Watch (every endpoint except `POST /v1/pair`) | `Authorization: Bearer <device_token>` |
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
- `limit` defaults to 20, maximum 200.
- Items are sorted newest first.

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
    Text        string `json:"text"`         // 1..4000 chars
    ExpectedSeq uint64 `json:"expected_seq"`
}
type AnswerRequest struct {
    OptionID    string `json:"option_id"`
    ExpectedSeq uint64 `json:"expected_seq"`
    Fingerprint string `json:"fingerprint"`
}
type CancelRequest struct {
    ExpectedSeq uint64 `json:"expected_seq"`
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

### 2.3 Server-Sent Events (`GET /v1/events`)

Response headers:
- `Content-Type: text/event-stream`
- `Cache-Control: no-cache`
- `X-Accel-Buffering: no`

Every 15 s the relay writes the comment line `:` followed by a blank line, as a keepalive.

| `event:` | `data:` (one JSON line) | When |
|---|---|---|
| `snapshot` | `AgentsSnapshot` | Immediately on connect, and after the host reconnects |
| `agent` | `AgentState` | An agent was added or changed |
| `agent_removed` | `{"pane_id": "..."}` | A pane closed or lost its agent |
| `host` | `{"host_online": bool, "herdr_online": bool}` | Either flag changed |
| `history` | `HistoryItem` | A new history item was stored |

**Client rule:** on any disconnect, reconnect with backoff (1 s, 2 s, 4 s … max 30 s) and **replace** local state with the next `snapshot`.

### 2.4 Error codes

Every non-200 response carries an `ErrorResponse`.

| Code | HTTP | Meaning | What the watch shows |
|---|---|---|---|
| `invalid_request` | 400 | Malformed body or bad parameter | "Something went wrong" |
| `unauthorized` | 401 | Missing or unknown token | Go to pairing screen |
| `unknown_pane` | 404 | Pane is not in the relay's list | Remove the card |
| `stale_state` | 409 | `expected_seq` no longer matches | "The agent changed — refreshed" |
| `prompt_changed` | 409 | Fingerprint mismatch | "The question changed — refreshed" |
| `agent_busy` | 409 | Prompt sent while `working` to an agent that cannot queue | "Agent is busy" |
| `agent_blocked` | 409 | Prompt sent while `blocked` | "Answer the question first" |
| `agent_state_unknown` | 409 | Command sent while `unknown` | "Agent state unknown" |
| `unknown_option` | 409 | `option_id` is not in the current prompt | "The question changed — refreshed" |
| `pair_code_invalid` | 403 | Wrong or expired code | "Invalid code" |
| `rate_limited` | 429 | Too many attempts | "Try again later" |
| `host_offline` | 503 | Bridge not connected | "Mac offline" |
| `herdr_offline` | 503 | Bridge connected, herdr unreachable | "herdr stopped" |
| `timeout` | 504 | Bridge did not answer within 10 s | "No answer from the Mac" |
| `internal` | 500 | Bug | "Something went wrong" |

---

## 3. Bridge ↔ relay WebSocket (`pkg/model/wire.go`)

URL: `wss://relay.<domain>/v1/host`, with the header `Authorization: Bearer <host_token>`.

- Every frame is one JSON text message with a `type` field.
- To decode, first unmarshal into `struct{ Type string \`json:"type"\` }`, then unmarshal again into the concrete type.
- **Only one host may be connected.** A second connection replaces the first: the relay closes the old one with status 4000 `replaced`.

```go
type HelloMsg struct {
    Type          string `json:"type"` // "hello"
    Version       string `json:"version"`        // bridge version, e.g. "0.2.0"
    Host          string `json:"host"`           // host_name or os.Hostname()
    HerdrVersion  string `json:"herdr_version"`  // from herdr "ping"
    HerdrProtocol int    `json:"herdr_protocol"` // from herdr "ping"
    HerdrOnline   bool   `json:"herdr_online"`
}
type HerdrStatusMsg struct {
    Type        string `json:"type"` // "herdr_status"
    HerdrOnline bool   `json:"herdr_online"`
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
    Fingerprint string `json:"fingerprint,omitempty"` // answer
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

**Relay rules:**
- If no `hello` arrives within 5 s, close the socket with status 4001.
- Reply to unknown `type` values by ignoring them (forward compatibility).

**Keepalive:** the bridge sends a WebSocket ping every 30 s. If no pong arrives within 10 s, it reconnects.

**Reconnect backoff** (bridge side): 1 s, 2 s, 4 s … up to 60 s, each ±20 % jitter. The backoff resets after 60 s of healthy connection.

---

## 4. Push payloads (`pkg/push`)

### 4.1 FCM (Wear OS) — data-only message

All values are strings (FCM data maps allow only strings). No `notification` block: the app builds the notification itself.

| Key | Example | Notes |
|---|---|---|
| `event` | `blocked` | `blocked` \| `done` \| `digest` (and `resolved`, below) |
| `pane_id` | `w5:pAE` | Empty for `digest` |
| `agent` | `claude` | |
| `label` | `bizum` | |
| `title` | `bizum needs approval` | Ready to display |
| `body` | `Bash: go test ./...` | ≤ 240 chars |
| `state_change_seq` | `334` | Decimal string, always a number (`0` for `digest`) |
| `fingerprint` | `9f2c61d0a4b3e871` | Only for `blocked` |
| `allow_option_id` | `opt-1` | First `allow_once` option, else empty |
| `deny_option_id` | `opt-3` | First `deny` option, else empty (the app then calls `cancel`) |

**`resolved`: withdraw a `blocked` notification.**

> ⚠️ **Disabled by default.** The relay sends `resolved` only when the FCM sender's `EnableResolved` is set (`pkg/push.FCM`). Enable it **only once the installed watch app handles `resolved`**: older apps show an unknown event as `"<label> needs you"` on the pane's notification id, which replaces a real approval with a bogus one.

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
- `android.priority = "high"` for `blocked`, `"normal"` otherwise (`resolved` included).
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

ntfy never gets `resolved`: it cannot withdraw a notification it already delivered.

### 4.3 When to push (relay)

| Transition (per pane, from `agent_update`) | Event |
|---|---|
| any → `blocked` | `blocked` |
| `working` → `done` | `done` |
| `blocked` → any other status, when that pane's own `blocked` push went out | `resolved` (FCM only, and only when enabled: §4.1) |
| anything else | no push |

- **`resolved`** goes out at once, once per `blocked` push:
  - no debounce, no window, never counted toward or included in a `digest`, never sent to ntfy;
  - a pane whose `blocked` push was dropped, or covered by a `digest`, gets none: the watch shows no notification of its own for it.
- **Removed panes:** push only sees `agent_update`.
  - A pane that leaves `blocked` through a `snapshot` gets its `resolved` with its next `agent_update`.
  - A pane removed while blocked (`agent_removed`, or missing from a `snapshot`) gets none; its notification stays until dismissed.

- **Debounce:** skip a push if the same pane pushed the same event less than 5 s ago.
- **Window:** the first push of a 10 s window goes out at once (latency matters for `blocked`). Later ones are held until the window ends.
- **End of the window:** each held agent gets at most one push, built from its **current** state, and only if it is still in the state a held push announced (`blocked` or `done`).
  - An agent that left that state gets nothing: the watch never offers to approve a prompt that was already answered.
  - An agent that answered and blocked again gets the push for its current prompt (fingerprint and option ids included).
- **Digest:** if more than 3 pushes would go out in the window (the first one included), the held ones go out as one `digest` push instead:
  - title `N agents need you` if any of them is `blocked`, `N agents finished` if all are `done`;
  - N counts **agents**, not messages;
  - body = the distinct labels joined with `, `, ≤ 240 chars.

---

## 5. Relay configuration (environment variables)

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `AW_LISTEN` | no | `:8080` | Listen address (Cloudflare → origin) |
| `AW_HOST_TOKEN` | **yes** | — | 64 hex chars; the same value goes in the bridge config |
| `AW_DATA_DIR` | no | `/var/lib/agent-watch-relay` | Holds `store.json`, `relay.lock` and `admin.sock` (see below) |
| `AW_FCM_CREDENTIALS` | no | — | Path to the Firebase service-account JSON. FCM is disabled if unset |
| `AW_NTFY_URL` | no | — | e.g. `https://ntfy.sh`. ntfy is disabled if unset |
| `AW_NTFY_TOPIC` | with ntfy | — | Random, unguessable topic name |
| `AW_NTFY_TOKEN` | no | — | ntfy access token |
| `AW_TRUST_CF_IP` | no | `true` | Use `CF-Connecting-IP` as the client IP for rate limiting |

**Files in `AW_DATA_DIR`** (the directory is `0700`):

| File | Meaning |
|---|---|
| `store.json` | Devices (token hashes only) and history. `0600`, owned by the service user |
| `relay.lock` | Exclusive `flock` held by the running relay for its whole life. A second relay on the same directory refuses to start |
| `admin.sock` | Local admin API, a `0600` Unix socket that exists only while the relay runs. Never exposed over TCP |

**`agent-watch-relay devices list|revoke <id>`** works with the relay running or stopped:

- **Relay running** (lock held): the CLI asks it over `admin.sock`. The revoked token is rejected from the next request on, the device's open SSE streams and in-flight requests are cancelled, and `store.json` is flushed at once.
- **Relay stopped:** the CLI takes the lock and edits `store.json` itself.
- Run it as the service user or as root, with the same `AW_DATA_DIR` as the service. Files written as root are handed to the owner of `AW_DATA_DIR`.

---

## 6. Bridge configuration (`config.toml`)

- **Location:** `$HERDR_PLUGIN_CONFIG_DIR/config.toml`. For the plugin id `herdr-agent-watch` this is `~/.config/herdr/plugins/config/herdr-agent-watch/config.toml`.
- **Mode:** `0600`.
- **Written by:** `agent-watch-bridge configure`.

```toml
relay_url  = "wss://relay.example.com"   # https:// is derived for /v1/host/* calls
host_token = "…64 hex chars…"
host_name  = ""                          # empty → os.Hostname()
claude_config_dirs = ["~/.claude"]       # where to resolve Claude session ids; add custom CLAUDE_CONFIG_DIR values
```

- **State file:** the daemon writes `$HERDR_PLUGIN_STATE_DIR/status.json` every 5 s. If that variable is unset, it uses `~/.local/state/agent-watch/status.json`. The `status` action reads this file.

```json
{ "pid": 4242, "relay_connected": true, "herdr_online": true, "agents": 11,
  "last_error": "", "updated_at": "2026-09-23T17:04:05Z" }
```
