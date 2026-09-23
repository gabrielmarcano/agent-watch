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
- **Request `id`:** use a counter plus a random prefix, formatted as a **string**.
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

- **No read deadline.** Unlike `Call`, this connection has no deadline after the ack.
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

// Refresh forces an immediate re-list (used by the bridge right before executing a command).
func (s *Syncer) Refresh(ctx context.Context) ([]AgentInfo, error)
```

**Algorithm for `Run`:**

```
loop:
  pong, err := Ping()
  if err: mark offline (OnHerdrOnline(false) once), sleep backoff, continue   // 500ms→30s, ±20% jitter
  mark online (OnHerdrOnline(true, pong) once per transition), reset backoff
  list()                                // diff + OnChanges
  open stream with subscriptionsFor(current agents)
  while stream healthy:
     select:
       event on stream     → schedule debounced list()
       poll timer (15s)    → list()
       after list(): if the set of pane_ids changed → close stream, reopen with new subscriptions
       stream closed       → break (degraded)
  degraded: poll every 2s with list(); retry the stream on every tick; if Ping fails → back to top (offline)
```

- **`subscriptionsFor(agents)`:** the global types `pane.created`, `pane.closed`, `pane.exited`, `pane.agent_detected`, plus one `pane.agent_status_changed` with `PaneID` for **every** agent pane.
- **Diff:** compare the new list with the previous one by `pane_id`.
  - New pane → `Added`.
  - Missing pane → `Removed`.
  - `Updated` when **any** of these differ: `AgentStatus`, `StateChangeSeq`, `Agent`, `Name`, `TerminalTitleStripped`, `Focused`, `CWD`, `ForegroundCWD`, `AgentSession`.
- **Panes without an agent:** filter out those where `Agent == nil` **and** `AgentStatus == "unknown"`. They are plain shells, not agents.
- **Delivery:** call `Listener.OnChanges` from a single goroutine, only with a non-empty batch.

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
| `TestSyncerOfflineOnline` | `Stop()` → `OnHerdrOnline(false)` exactly once; `Start()` → `OnHerdrOnline(true)` and a full list |
| `TestSyncerIgnoresPlainShells` | A pane with `agent: null, agent_status: unknown` never appears |
| `TestTrustedSession` | Mismatched `agent_session.agent` → nil |

Use short durations in tests (`Debounce: 10ms`, `PollDegraded: 50ms`). Wait on conditions with a polling helper; never use fixed `time.Sleep` longer than 50 ms.

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
