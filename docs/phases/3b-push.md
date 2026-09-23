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
    EventBlocked Event = "blocked"
    EventDone    Event = "done"
    EventDigest  Event = "digest"
)

type Message struct {
    Event          Event
    PaneID, Agent, Label string
    Title, Body    string
    StateChangeSeq uint64
    Fingerprint    string
    AllowOptionID  string
    DenyOptionID   string
}

type Sender interface {
    Name() string
    Send(ctx context.Context, m Message) error
}

type Dispatcher struct {
    Senders []Sender
    Now     func() time.Time // injectable clock
    Logger  *slog.Logger
    // private: lastPush map[paneID+event]time.Time, window []pending, timer
}

// OnAgentUpdate implements relay.Notifier.
func (d *Dispatcher) OnAgentUpdate(prev *model.AgentState, cur model.AgentState)
```

**Building a message from a transition:**

| Transition | Title | Body |
|---|---|---|
| `prev == nil` or `prev.Status != blocked`, and `cur.Status == blocked` | `"<label> needs approval"` | `"<prompt.title>: <prompt.detail>"`. If there is no detail, just the title. For kind `unknown`: `"Open Agent Watch to see the question"` |
| `prev.Status == working` and `cur.Status == done` | `"<label> finished"` | `"Task finished"` (the history item arrives separately; do not wait for it) |

- **Body length:** ≤ 240 characters, cut on a rune boundary, with `…` appended.
- **`AllowOptionID` / `DenyOptionID`:** the first option with role `allow_once` / `deny` in `cur.Prompt.Options`, or empty.

**Debounce and digest** (`contracts.md` §4.3):
- Drop a message if the same `pane_id + event` was sent less than 5 s ago.
- Accepted messages go into a 10 s window. Each time the window flushes:
  - **≤ 3 messages:** send each one.
  - **More than 3:** send one `digest` message. Title: `"<n> agents need you"`. Body: the labels, deduplicated, joined with `", "`.
- **Latency matters for `blocked`.** Send the first message of a window **immediately**, and only hold the rest. The digest then covers messages 2..n. Document this choice in a code comment.

**Sending:**
- Call every sender in its own goroutine, with a 10 s timeout.
- Log failures (sender name, event, pane) and **never** retry more than once.
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
    OnInvalidToken func(token string) // store removes it
}
```

- **Setup:** read the service-account JSON from `AW_FCM_CREDENTIALS`. `ProjectID` is its `project_id`. `google.CredentialsFromJSON(...).TokenSource` with `oauth2.NewClient` handles token refresh; do not hand-roll JWTs.
- **Request:** `POST {Endpoint}/v1/projects/{ProjectID}/messages:send`, once per device token:

  ```json
  {"message":{"token":"<device fcm token>",
              "data":{"event":"blocked","pane_id":"w5:pAE","agent":"claude","label":"bizum",
                      "title":"bizum needs approval","body":"Bash command: go test ./...",
                      "state_change_seq":"334","fingerprint":"9f2c61d0a4b3e871",
                      "allow_option_id":"opt-1","deny_option_id":"opt-3"},
              "android":{"priority":"high","ttl":"600s"}}}
  ```

- **All data values are strings.** Always include every key, with empty strings when unknown, so the Kotlin side never sees a missing key.
- **Invalid tokens:** a `404`, or a `400` with `UNREGISTERED` / `INVALID_ARGUMENT` about the token, means the token is dead. Call `OnInvalidToken`.

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

- Build the senders from the config. FCM is enabled when `AW_FCM_CREDENTIALS` is set; ntfy when `AW_NTFY_URL` and `AW_NTFY_TOPIC` are set.
- Pass the `Dispatcher` as the hub's `Notifier`.
- With no sender configured, use the no-op notifier and log `push disabled` once at startup.
- **`POST /v1/push/register`:** `platform` must be `fcm` (else `invalid_request`). Store the token on the calling device (`Device.FCMToken`) and save.

### 5. Tests

| Test | Asserts |
|---|---|
| Transition table | Only `→blocked` and `working→done` produce messages; `idle→working` does not |
| Debounce | Two `blocked` for the same pane within 5 s → one message |
| Digest | 5 panes blocked within 10 s → the first is sent immediately, then one digest listing the other 4 |
| FCM payload | The fake endpoint receives exactly the JSON above; every data value is a string; the auth header is present |
| FCM invalid token | Fake returns 404 → `OnInvalidToken` called, the token is removed from the store |
| ntfy | Headers `Title`, `Priority`, `Tags` correct; `Authorization` only when the token is set |
| Failure isolation | A sender that times out does not delay the other sender or the relay |

Use an injectable clock. **No real network calls in tests.**

---

## Manual verification

1. **Wear OS:**
   - Create a Firebase project (the owner already has one for the app).
   - Download the service-account JSON **to the VPS only**, and set `AW_FCM_CREDENTIALS`.
   - Pair the Pixel Watch 2 (Phase 4), make a sandbox agent block, and check that the notification arrives with Allow/Deny.
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
