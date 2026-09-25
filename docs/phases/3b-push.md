# Phase 3b — Push Notifications: FCM (Wear OS) and ntfy (watchOS)

> **Goal:** the relay notifies the wrist when an agent needs approval or finishes, without spamming, through FCM v1 for Wear OS and ntfy for the Apple Watch (via the iPhone ntfy app).

| | |
|---|---|
| **Depends on** | Phase 3a (the `Notifier` hook and the store's `FCMToken`) |
| **Parallel with** | 2b, 2c, 4 |
| **Touches** | `pkg/push/**`, `pkg/relay/server.go` (wiring only), `go.mod`/`go.sum`, `docs/STATUS.md` |

---

## Read first

- [`docs/reference/contracts.md`](../reference/contracts.md) §4 (payloads, triggers, debounce, digest) and §5 (env vars).

---

## Files

| File | Responsibility |
|---|---|
| `pkg/push/push.go` | `Message` type, `Sender` interface, `Dispatcher` (transitions → debounce/digest → senders) |
| `pkg/push/fcm.go` | FCM HTTP v1 sender |
| `pkg/push/ntfy.go` | ntfy sender |
| `pkg/push/*_test.go` | Tests with `httptest` fake endpoints and a fake clock |

---

## Steps

### 1. `push.go`

```go
type Event string

const (
    EventBlocked  Event = "blocked"
    EventDone     Event = "done"
    EventDigest   Event = "digest"
    EventResolved Event = "resolved" // withdraw a pane's blocked notification (contracts.md §4.1)
)

type Message struct {
    Event          Event
    PaneID, Agent, Label string
    Title, Body    string
    StateChangeSeq uint64
    Fingerprint    string
    AllowOptionID  string
    DenyOptionID   string
    AnyBlocked     bool // a digest that covers at least one blocked agent
}

type Sender interface {
    Name() string
    Send(ctx context.Context, m Message) error
}

// ResolvedSender is a Sender that can withdraw a notification it already
// shows. Only senders whose SendsResolved() is true get EventResolved.
type ResolvedSender interface {
    Sender
    SendsResolved() bool
}

// NoRetry marks a Send error the Dispatcher must not retry (the sender already
// retried per target; retrying the whole Send would deliver twice).
func NoRetry(err error) error

type Dispatcher struct {
    Senders          []Sender
    Now              func() time.Time // injectable clock
    Logger           *slog.Logger
    DebounceDuration time.Duration    // default 5 s
    WindowDuration   time.Duration    // default 10 s
    // private: lastPush, held messages, the newest state of held panes,
    // the prompt each pane's blocked notification shows, the window timer
}

func NewDispatcher(senders []Sender, now func() time.Time, logger *slog.Logger) *Dispatcher
// A zero-value Dispatcher gets the same defaults on first use (no panic).

// OnAgentUpdate implements relay.Notifier.
func (d *Dispatcher) OnAgentUpdate(prev *model.AgentState, cur model.AgentState)
```

**Building a message from a transition:**

| Transition | Title | Body |
|---|---|---|
| `prev == nil` or `prev.Status != blocked`, and `cur.Status == blocked` | `"<label> needs approval"` | `"<prompt.title>: <prompt.detail>"`. If there is no detail, just the title. For kind `unknown`: `"Open Agent Watch to see the question"` |
| `prev.Status == working` and `cur.Status == done` | `"<label> finished"` | `"Task finished"` (the history item arrives separately; do not wait for it) |
| the pane's own `blocked` push went out, and `cur.Status != blocked` | — | `resolved`: only `pane_id` + `state_change_seq`, sent at once (no debounce, window or digest), only to `ResolvedSender`s that return true |

- **Body length:** ≤ 240 characters, cut on a rune boundary, with `…` appended.
- **`AllowOptionID` / `DenyOptionID`:** the first option with role `allow_once` / `deny` in `cur.Prompt.Options`, or empty.

**Debounce, window and digest** (`contracts.md` §4.3 has the exact rules):
- **Debounce:** the same `pane_id + event` pushed less than 5 s ago.
  - `done`: drop it (the notification already says it finished).
  - `blocked`: **hold it** to the end of the window (trailing edge), never drop it: a new prompt right after an answer must not be lost.
- **No duplicates:** never push a `blocked` for the prompt the pane's notification already shows (same `state_change_seq` and `fingerprint`), at once or at the end of the window.
- **Window:** the first push of a 10 s window goes out **immediately** (latency matters for `blocked`); later ones are held. A push held by the debounce opens a window if none is open. Document this choice in a code comment.
- **End of the window:** at most one push per held agent, **rebuilt from its current state**, and only if it is still in the state a held push announced. An agent whose prompt was answered meanwhile gets nothing; one that answered and blocked again gets its current prompt.
- **Digest:** when more than 3 pushes would go out in the window (the first one included), the held ones go out as one `digest`:
  - title `"<n> agents need you"` if any is `blocked`, else `"<n> agents finished"`; `<n>` counts agents, not messages;
  - body: the distinct labels joined with `", "`, ≤ 240 characters;
  - FCM priority `high` when any covered agent is `blocked` (`AnyBlocked`).

**Sending:**
- Call every sender in its own goroutine, with a 10 s timeout.
- Log failures (sender name, event, pane) and **never** retry more than once. A `NoRetry` error is not retried at all (FCM retries per token itself).
- A failed push must never affect the relay's state.

### 2. `fcm.go`

```bash
go get golang.org/x/oauth2
```

```go
type FCM struct {
    ProjectID string
    Client    *http.Client // from google.CredentialsFromJSON(ctx, json, "https://www.googleapis.com/auth/firebase.messaging")
    Tokens    func() []string // device FCM tokens from the store
    Endpoint  string          // default "https://fcm.googleapis.com"; overridable in tests
    OnInvalidToken func(token string) // store removes it; may be called from several goroutines
    Logger         *slog.Logger
    RetryDelay     time.Duration // pause before the one retry of a token; default 1 s
    TokenTimeout   time.Duration // bounds one token's delivery, retry included; default 10 s
    EnableResolved bool          // send "resolved" (SendsResolved() reports it); set once, before use
}
```

- **Setup:** read the service-account JSON from `AW_FCM_CREDENTIALS`. `ProjectID` is its `project_id`. `google.CredentialsFromJSON(...).TokenSource` with `oauth2.NewClient` handles token refresh; do not hand-roll JWTs.
- **Request:** `POST {Endpoint}/v1/projects/{ProjectID}/messages:send`, once per device token. **Tokens are sent concurrently, each with its own `TokenTimeout`**, so one device that never answers cannot use up the others' budget. A transient failure (network error, 429, 5xx) is retried once for that token only, after `RetryDelay`; `Send` then returns a `NoRetry` error, so healthy devices never get a push twice.

  ```json
  {"message":{"token":"<device fcm token>",
              "data":{"event":"blocked","pane_id":"w5:pAE","agent":"claude","label":"bizum",
                      "title":"bizum needs approval","body":"Bash command: go test ./...",
                      "state_change_seq":"334","fingerprint":"fd6ff7388739252d",
                      "allow_option_id":"opt-1","deny_option_id":"opt-3"},
              "android":{"priority":"high","ttl":"600s"}}}
  ```

- **All data values are strings.** Always include every key, with empty strings when unknown, so the Kotlin side never sees a missing key. `state_change_seq` is always a number (`"0"` for a digest).
- **`resolved`** carries only `event`, `pane_id` and `state_change_seq`, at `normal` priority. It is sent only when `EnableResolved` is set (`AW_PUSH_RESOLVED`); off by default, because older watch apps show an unknown event as a bogus approval.
- **Dead tokens:** call `OnInvalidToken` **only** when FCM says the token itself is dead: error code `UNREGISTERED` (on a 404 or a 400), or a `400 INVALID_ARGUMENT` whose field violation is `message.token`. Any other error keeps the token, **a bare 404 included**: a wrong project id or a proxy in the way would otherwise wipe every device.

### 3. `ntfy.go`

```go
type Ntfy struct {
    BaseURL string // AW_NTFY_URL
    Topic   string // AW_NTFY_TOPIC
    Token   string // AW_NTFY_TOKEN, optional
    Client  *http.Client
}
```

- `POST {BaseURL}/{Topic}`, with the body = `m.Body` as plain text, and the headers from `contracts.md` §4.2.
- A non-2xx response is an error.
- ntfy notifications carry no actions in this version: approvals happen in the watch app.

### 4. Wiring (`pkg/relay/server.go`)

- Build the senders from the config. FCM is enabled when `AW_FCM_CREDENTIALS` is set, with `EnableResolved = AW_PUSH_RESOLVED` (set before the dispatcher starts); ntfy when `AW_NTFY_URL` and `AW_NTFY_TOPIC` are set.
- **Never log the ntfy topic** (anyone who knows it reads the pushes), and keep it out of push errors.
- Pass the `Dispatcher` as the hub's `Notifier`.
- With no sender configured, use the no-op notifier and log `push disabled` once at startup.
- **`POST /v1/push/register`:** `platform` must be `fcm` (else `invalid_request`). Store the token on the calling device (`Device.FCMToken`) and save.

### 5. Tests

| Test | Asserts |
|---|---|
| Transition table | Only `→blocked` and `working→done` produce messages; `idle→working` does not |
| Debounce | A repeated `done` within 5 s → one message; a re-block within 5 s is held and pushed at the end of the window with the current prompt |
| Digest | 5 panes blocked within 10 s → the first is sent immediately, then one digest covering the other 4 (`4 agents need you`, high priority) |
| Stale held push | A held `blocked` whose agent was answered before the flush → nothing sent |
| Resolved | Only after the pane's own `blocked` push; never to ntfy; not sent while `EnableResolved` is off |
| FCM payload | The fake endpoint receives exactly the JSON above; every data value is a string; the auth header is present |
| FCM dead token | `UNREGISTERED`, or `INVALID_ARGUMENT` on `message.token` → `OnInvalidToken`, the token is removed from the store. A bare 404 or a payload `INVALID_ARGUMENT` → token kept |
| FCM per token | One hung token does not delay the others; a 5xx is retried once for that token only |
| ntfy | Headers `Title`, `Priority`, `Tags` correct; `Authorization` only when the token is set |
| Failure isolation | A sender that times out does not delay the other sender or the relay |

Use an injectable clock. **No real network calls in tests.**

---

## Manual verification

1. **Wear OS:**
   - Create a Firebase project (the owner already has one for the app).
   - Download the service-account JSON **to the VPS only**, and set `AW_FCM_CREDENTIALS`.
   - Pair the Pixel Watch 2 (Phase 4), make a sandbox agent block, and check that the notification arrives with Allow/Deny.
   - **`resolved`:** only after the installed app handles it (the current data layer does), set `AW_PUSH_RESOLVED=1`, restart the relay, answer a blocked agent on the Mac, and check that the watch's approval notification disappears. Not verified yet (Phase 5).
2. **watchOS:**
   - Install ntfy on the iPhone and subscribe to the topic.
   - Make a sandbox agent block, and check that the notification appears on the iPhone and mirrors to the watch (or the simulator, if paired).

---

## Definition of done

- [ ] Tests pass with `go test -race ./pkg/push/... ./pkg/relay/...`.
- [ ] No credential file or topic name committed (`git grep -nE 'private_key|ntfy.sh/[a-z0-9]{8,}'` is empty).
- [ ] `docs/STATUS.md` Phase 3b ticked (manual checks may be deferred to Phase 5; say so).
- [ ] Commit only `pkg/push`, `pkg/relay/server.go`, `go.mod`, `go.sum`, `docs/STATUS.md`.

---

## Prompt for the executing agent

```
You are executing Phase 3b (push notifications) of the Agent Watch refactor in /Users/me/Code/personal/agent-watch-herdr.
Read AGENTS.md, docs/reference/contracts.md §4–§5 and docs/phases/3b-push.md, then implement pkg/push and wire it into
pkg/relay/server.go as specified. Use golang.org/x/oauth2/google for FCM auth (no hand-written JWT). Every FCM data value
must be a string and every key must always be present. Tests use httptest fakes and an injectable clock; no real network.
Never commit credentials or ntfy topics. Run `go vet ./... && go test -race ./...`, paste the output, tick Phase 3b in
docs/STATUS.md, and commit only the paths the guide lists.
```
