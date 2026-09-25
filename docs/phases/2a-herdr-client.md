# Phase 2a — `pkg/herdr`: Socket Client and Sync Engine

> **Goal:** an agent-agnostic Go package that talks to the herdr socket and turns it into a clean stream of "agent added / changed / removed" and "herdr online/offline" notifications.

| | |
|---|---|
| **Depends on** | Phase 1 |
| **Parallel with** | 2b (adapters), 3a/3b (relay), 4 (Wear OS) — no shared files |
| **Touches** | `pkg/herdr/**`, `docs/STATUS.md` |
| **Must not** | import `pkg/agents`, know any agent name, or call the real herdr socket in tests |

---

## Read first

- [`docs/reference/herdr-socket-api.md`](../reference/herdr-socket-api.md): all of it. This package implements that document.
- [`docs/reference/contracts.md`](../reference/contracts.md) §1.2 (how `AgentState` fields are derived).
- `pkg/herdrtest` (Phase 1): your tests use it.

---

## Files

| File | Responsibility |
|---|---|
| `pkg/herdr/types.go` | Go mirrors of herdr's JSON: `AgentInfo`, `AgentSession`, `Pong`, `ReadResult`, `Error` |
| `pkg/herdr/client.go` | `Client` with the one-shot `Call` and typed helpers |
| `pkg/herdr/subscribe.go` | Long-lived `events.subscribe` stream |
| `pkg/herdr/sync.go` | `Syncer`: snapshot + events + poll → diff notifications |
| `pkg/herdr/*_test.go` | Tests against `pkg/herdrtest` |

---

## Steps

### 1. `types.go`

```go
package herdr

type AgentSession struct {
    Agent  string `json:"agent"`
    Kind   string `json:"kind"`   // "id" | "path"
    Source string `json:"source"`
    Value  string `json:"value"`
}

type AgentInfo struct {
    PaneID                string        `json:"pane_id"`
    WorkspaceID           string        `json:"workspace_id"`
    TabID                 string        `json:"tab_id"`
    Agent                 *string       `json:"agent"`
    AgentStatus           string        `json:"agent_status"`
    Name                  *string       `json:"name"`
    Focused               bool          `json:"focused"`
    CWD                   *string       `json:"cwd"`
    ForegroundCWD         *string       `json:"foreground_cwd"`
    TerminalTitleStripped *string       `json:"terminal_title_stripped"`
    StateChangeSeq        uint64        `json:"state_change_seq"`
    AgentSession          *AgentSession `json:"agent_session"`
}

// TrustedSession returns the session ref only when it belongs to the pane's current agent
// (herdr keeps stale refs after an agent is replaced). See herdr-socket-api.md §3.1.
func (a AgentInfo) TrustedSession() *AgentSession

type Pong struct {
    Version  string `json:"version"`
    Protocol int    `json:"protocol"`
}

type ReadSource string

const (
    SourceVisible         ReadSource = "visible"
    SourceRecent          ReadSource = "recent"
    SourceRecentUnwrapped ReadSource = "recent_unwrapped" // underscore! the CLI spelling fails on the socket
)

// Error is herdr's {"code","message"}; it implements error.
type Error struct {
    Code    string `json:"code"`
    Message string `json:"message"`
}
func (e *Error) Error() string { return "herdr " + e.Code + ": " + e.Message }

// IsCode reports whether err is a herdr error with this code (uses errors.As).
func IsCode(err error, code string) bool
```

### 2. `client.go`

```go
type Client struct {
    SocketPath string        // required
    Timeout    time.Duration // per call; default 5s when zero
}

// NewClient resolves the socket path: explicit arg, else $HERDR_SOCKET_PATH, else ~/.config/herdr/herdr.sock.
func NewClient(socketPath string) *Client

func (c *Client) Call(ctx context.Context, method string, params, out any) error // see reference §1
func (c *Client) Ping(ctx context.Context) (Pong, error)
func (c *Client) ListAgents(ctx context.Context) ([]AgentInfo, error)
func (c *Client) Read(ctx context.Context, paneID string, src ReadSource, lines int) (string, error) // lines<=0 → omit
func (c *Client) SendKeys(ctx context.Context, paneID string, keys []string) error
func (c *Client) Prompt(ctx context.Context, paneID, text string) error // never sets "wait"
```

- **`Call`** follows the reference implementation in `herdr-socket-api.md` §1: one connection per call.
  - It is bounded by ctx's deadline, or by `Timeout` when ctx has none. **Cancelling ctx abandons the call at once**; the error then wraps ctx's error (`context.Canceled` / `context.DeadlineExceeded`), never `ErrUnavailable`.
  - It **rejects an answer to another request** (response `id` ≠ request `id`; herdr answers a malformed request with an error and an empty `id`, which is kept) and a response with neither `result` nor `error`.
- **Request `id`:** use a counter plus a random per-process prefix, formatted as a **string**.
- **`Ping`, `ListAgents` and `Read` check the result `type`** (`pong`, `agent_list`, `pane_read`) and fail on anything else.
- **`ListAgents`** decodes `{"type":"agent_list","agents":[...]}` and returns `agents`.
- **`Read`** decodes `{"type":"pane_read","read":{"text":...}}` and returns `read.text`.
- **Offline detection:** a dial error must be distinguishable. Export `var ErrUnavailable = errors.New("herdr unavailable")` and wrap dial errors with it, so callers can check `errors.Is(err, herdr.ErrUnavailable)`.

### 3. `subscribe.go`

```go
type Event struct {
    Name string          // snake_case, e.g. "pane_agent_status_changed"
    Data json.RawMessage
}

type Subscription struct {
    Type   string `json:"type"`
    PaneID string `json:"pane_id,omitempty"`
}

// Subscribe opens a stream. The returned channel closes when ctx is cancelled or the connection drops.
// The function returns only after the "subscription_started" ack (or an error).
func (c *Client) Subscribe(ctx context.Context, subs []Subscription) (<-chan Event, error)
```

- **Bounded handshake:** dial, request and ack share one deadline: ctx's, or the client's `Timeout` (5 s) when that is sooner. A hung herdr can never block the Syncer here. The ack's `id` and `type` are checked like `Call`'s.
- **No read deadline after the ack.** The stream itself is long-lived. An event that arrives in the same read as the ack is kept, not lost.
- **Reader goroutine:** scan lines with a `bufio.Scanner`. Raise the buffer to 1 MB with `scanner.Buffer(make([]byte, 64*1024), 1<<20)`, because events can be large.
- **Parsing:** decode each line into `{"event":..., "data":...}` and send an `Event`. Skip lines without `event`.
- **Cancellation:** on `ctx.Done()`, close the connection so the scanner returns, then close the channel.

### 4. `sync.go` — the Syncer

This is the heart of the bridge's view of herdr.

```go
type ChangeKind int

const (
    Added ChangeKind = iota
    Updated
    Removed
)

type Change struct {
    Kind    ChangeKind
    Agent   AgentInfo  // current value (for Removed: the last known value)
    Prev    *AgentInfo // nil for Added
}

// Callbacks never run concurrently with each other, and batches arrive in the
// order their lists were fetched. A callback must not call Syncer.Refresh
// synchronously (it would wait for itself): start a goroutine instead. For the
// same reason, never call Refresh while holding a lock a callback takes.
type Listener interface {
    OnChanges(changes []Change)   // called with a batch, never concurrently
    OnHerdrOnline(online bool, pong Pong)
}

type Syncer struct {
    Client       *Client
    Listener     Listener
    Debounce     time.Duration // default 150ms
    PollHealthy  time.Duration // default 15s
    PollDegraded time.Duration // default 2s
    Logger       *slog.Logger
}

// Run blocks until ctx is cancelled. It never returns an error for herdr being down; it keeps retrying.
func (s *Syncer) Run(ctx context.Context) error

// Snapshot returns the last known agent list (copy). Safe for concurrent use.
func (s *Syncer) Snapshot() []AgentInfo

// Refresh forces an immediate re-list. Refreshes are serialized with each
// other and with Run's own lists (fetch, diff, apply, notify, then the next),
// so an older list never overwrites a newer one. Waiting honours ctx.
func (s *Syncer) Refresh(ctx context.Context) ([]AgentInfo, error)
```

The bridge's command executor does **not** validate against `Refresh`'s result: it calls `Client.ListAgents` itself, so its check can never be a list some other caller fetched earlier.

**Algorithm for `Run`:**

```
loop:
  pong, err := Ping()
  if err: mark offline (OnHerdrOnline(false) once), sleep backoff, continue   // 500ms→30s, ±20% jitter
  mark online (OnHerdrOnline(true, pong) once per transition)
  list()                                // diff + OnChanges
  open stream with subscriptionsFor(current agents), then schedule one more debounced list()
                                        // covers changes between the list and the subscription
  loop:
     select:
       event on stream     → schedule a debounced list() (events inside the window coalesce into one)
       poll timer          → list(); every 15 s while the stream is up, every 2 s while it is down
       pane set changed    → if it differs from the set the open stream covers: close it, resubscribe now
                              (signalled by every applied list, whoever called Refresh)
       stream closed       → list() at once, then resubscribe after a backoff (500 ms→30 s, ±20 %),
                              polling every 2 s meanwhile (degraded); the backoff resets once a
                              stream survives a whole healthy poll interval
       subscribe fails     → same backoff and degraded polling; herdr errors are not "offline"
     any list or subscribe that finds herdr gone (dial error, or a transport error and Ping fails)
                           → back to the top (offline)
  a herdr that answers ping but never completes a list is retried with the same backoff
```

- **Offline at startup is reported.** The first observation counts as a transition: if herdr is down when `Run` starts, the Listener gets `OnHerdrOnline(false)` once.
- **List failures** are logged once per streak (warning), then at debug level, and once more when a list succeeds again.

- **`subscriptionsFor(agents)`:** the global types `pane.created`, `pane.closed`, `pane.exited`, `pane.agent_detected`, plus one `pane.agent_status_changed` with `PaneID` for **every** agent pane.
- **Diff:** compare the new list with the previous one by `pane_id`.
  - New pane → `Added`.
  - Missing pane → `Removed`.
  - `Updated` when **any** of these differ: `AgentStatus`, `StateChangeSeq`, `Agent`, `Name`, `TerminalTitleStripped`, `Focused`, `CWD`, `ForegroundCWD`, `AgentSession`.
- **Panes without an agent:** filter out those where `Agent == nil` **and** `AgentStatus == "unknown"`. They are plain shells, not agents.
- **Delivery:** list → diff → apply → `Listener` runs under one turn (a channel semaphore that honours ctx), only with a non-empty batch, so callbacks are serialized and in fetch order.

### 5. Tests (all against `herdrtest`)

| Test | Asserts |
|---|---|
| `TestCallStringID` | The fake received a string `id` |
| `TestReadUsesUnderscoreSource` | `Read(..., SourceRecentUnwrapped, ...)` succeeds; params contain `recent_unwrapped` |
| `TestPromptOmitsWait` | Recorded params have no `wait` key, or it is `null` |
| `TestErrorMapping` | `FailNext("agent.prompt","agent_blocked",...)` → `IsCode(err,"agent_blocked")` |
| `TestUnavailable` | With the fake stopped, `ListAgents` → `errors.Is(err, ErrUnavailable)` |
| `TestSubscribeAck` | `Subscribe` returns after the ack; `EmitStatusChanged` arrives on the channel |
| `TestSyncerAddUpdateRemove` | `SetAgents` + `EmitStatusChanged` → Added, then Updated (status), then Removed |
| `TestSyncerResubscribesOnNewPane` | After adding a pane, the fake receives a new `events.subscribe` that includes it |
| `TestSyncerSubscribesPaneFoundByAnotherRefresh` | A pane first seen by an outside `Refresh` still triggers the resubscription |
| `TestSyncerOfflineOnline` | `Stop()` → `OnHerdrOnline(false)` exactly once; `Start()` → `OnHerdrOnline(true)` and a full list |
| `TestSyncerReportsOfflineAtStartup` | herdr down when `Run` starts → `OnHerdrOnline(false)` once |
| `TestSyncerStreamDropRelistsThenBacksOff` | `DropStreams()` → an immediate list, then a resubscribe only after the backoff |
| `TestSyncerPollsDegradedWhileStreamDown` | `SetDropStreamsAfterAck(true)` → lists every `PollDegraded` |
| `TestSyncerShutdownWithHungSubscribe` / `TestSyncerShutdownWithHungList` | `HoldNext` on the call; cancelling ctx still ends `Run` |
| `TestRefreshAppliesListsInFetchOrder` | Two overlapping refreshes (`HoldNext`) apply in the order they were fetched |
| `TestRefreshWaitingForAnotherHonoursContext` | A refresh waiting for its turn returns when its ctx ends |
| `TestSyncerIgnoresPlainShells` | A pane with `agent: null, agent_status: unknown` never appears |
| `TestTrustedSession` | Mismatched `agent_session.agent` → nil |
| `call_test.go`, `subscribe_test.go` | Cancel and deadline handling, mismatched response `id`, unexpected result or ack `type`, the bounded subscribe handshake |

Use short durations in tests (`Debounce: 10ms`, `PollDegraded: 50ms`), or the Syncer's internal fake clock (`clock.go`, `export_test.go`) to drive backoff, polling and debouncing deterministically. Wait on conditions with a polling helper; never use fixed `time.Sleep` longer than 50 ms.

### 6. Manual smoke test (read-only, against the real herdr)

Create a throw-away `pkg/herdr/cmd_smoke_test.go`, **or** a tiny program in the session scratchpad (not the repo). It builds a `Syncer` with a listener that prints changes. Run it for 30 s while you switch focus between panes in herdr. Expected: `Updated` lines for `focused`, no errors.

**Delete the smoke program afterwards.** It must **not** call `SendKeys` or `Prompt`.

---

## Definition of done

- [ ] All files above exist; the package imports only the standard library and `pkg/model`, if needed.
- [ ] All tests in the table pass with `go test -race ./pkg/herdr/...`.
- [ ] Manual smoke test observed real changes (paste 3–5 lines of its output in STATUS).
- [ ] `gofmt -l pkg` is empty; `go vet ./...` passes.
- [ ] Commit only `pkg/herdr` and `docs/STATUS.md`.

---

## Pitfalls

- **Reusing a connection for two calls** hangs forever. One dial per call.
- **Forgetting `pane_id`** on `pane.agent_status_changed` makes the whole subscribe call fail.
- **Building state from event payloads** drifts. Events only schedule a re-list.
- **Holding the mutex while calling the Listener** can deadlock if the listener calls `Snapshot()`. Copy first, then call without the lock.

---

## Prompt for the executing agent

```
You are executing Phase 2a (pkg/herdr) of the Agent Watch refactor in /Users/me/Code/personal/agent-watch-herdr.
Read AGENTS.md, docs/reference/herdr-socket-api.md (entirely, including the safety warning) and
docs/phases/2a-herdr-client.md, then implement exactly what the guide specifies. The package must stay
agent-agnostic and depend only on the standard library. Test only against pkg/herdrtest. The optional manual
smoke test against the real herdr must be read-only (no SendKeys/Prompt) and must not be committed.
Run `go vet ./... && go test -race ./pkg/herdr/...`, paste the output in your report, tick Phase 2a in
docs/STATUS.md, and commit only pkg/herdr and docs/STATUS.md.
```
