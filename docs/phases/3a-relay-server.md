# Phase 3a — The Relay Server: Host Hub, Watch API, SSE, Pairing, Store

> **Goal:** `agent-watch-relay serve`, a single Linux binary. It accepts one bridge over WebSocket, serves the `/v1` API and SSE to watches, pairs devices, and persists devices and history to one JSON file.

| | |
|---|---|
| **Depends on** | Phase 1 (`pkg/model`) |
| **Parallel with** | 2a, 2b, 2c, 4. The contracts are frozen, so the bridge and relay meet in the middle |
| **Touches** | `pkg/relay/**`, `cmd/relay/**`, `go.mod`/`go.sum`, `docs/STATUS.md` |
| **Does not include** | push (Phase 3b) and deployment (Phase 3c). Leave a `Notifier` hook (§4) for 3b |

---

## Read first

- [`docs/reference/contracts.md`](../reference/contracts.md): all of it, especially §2 (HTTP API, SSE, error codes), §3 (wire), §5 (env vars).
- [`HERDR_REFACTOR_PLAN.md`](../../HERDR_REFACTOR_PLAN.md) §9 (pairing) and §15 (security risks).

---

## Files

| File | Responsibility |
|---|---|
| `pkg/relay/config.go` | Load and validate env vars (`contracts.md` §5) |
| `pkg/relay/store.go` | Devices, push targets and history in memory + atomic JSON persistence |
| `pkg/relay/state.go` | Current agents, `host_online`, `herdr_online`; builds `AgentsSnapshot`; fan-out to SSE subscribers |
| `pkg/relay/hub.go` | `/v1/host` WebSocket: one host, hello timeout, message handling, command round-trips |
| `pkg/relay/api.go` | HTTP handlers and routing (`http.ServeMux` patterns) |
| `pkg/relay/sse.go` | `/v1/events` streaming |
| `pkg/relay/auth.go` | Device and host token checks, pairing codes, rate limiting |
| `pkg/relay/clientip.go` | Which address identifies the client (trusted proxies) |
| `pkg/relay/server.go` | `Server` struct wiring everything; `Handler() http.Handler`; `Run(ctx)` |
| `cmd/relay/main.go` | `serve`, `devices list`, `devices revoke <id>`, `version` |

---

## Steps

### 1. Store (`store.go`)

```go
type Device struct {
    ID        string    `json:"id"`         // random 16 hex
    Name      string    `json:"name"`
    TokenHash string    `json:"token_hash"` // hex(sha256(token))
    CreatedAt string    `json:"created_at"`
    LastSeen  string    `json:"last_seen"`
    FCMToken  string    `json:"fcm_token,omitempty"`
}

type storeFile struct {
    Version int                            `json:"version"` // 1
    Devices []Device                       `json:"devices"`
    History map[string][]model.HistoryItem `json:"history"` // pane_id → newest first
}
```

- **Save:** write to `store.json.tmp` with mode `0600`, then `fsync`, then `os.Rename` over `store.json`, then `fsync` the directory. Save at most once per second (coalesce) and on shutdown. Log a failed save; never crash on it.
- **Load:** a missing file means an empty store. A corrupt file is a fatal error at startup, with a clear message. **Never** overwrite it silently.
- **History limits:**
  - 20 items per pane and 200 in total; drop the oldest.
  - Dedup by `ID`.
  - Prune panes that have had no items for 7 days.
- **Tokens:** device tokens are 32 random bytes as 64 hex chars. Store only `sha256` hashes, and compare with `subtle.ConstantTimeCompare`.

### 2. Auth (`auth.go`)

**Host auth:**
- Accept `Authorization: Bearer <AW_HOST_TOKEN>`, compared in constant time.
- Used by `/v1/host`, `/v1/host/pair-code` and `/v1/host/status`.

**Device auth:**
- Hash the bearer token and look it up.
- Update `LastSeen` at most once per minute.
- Put the `Device` in the request context.

**Pairing codes:**
- 6 random digits from `crypto/rand`.
- Kept in memory only, with a 5-minute TTL.
- Single use.
- At most 3 active at once (the oldest is evicted).

**Rate limiting (`POST /v1/pair`):**
- 5 attempts per client IP per 10 minutes, plus 20 attempts in total per 10 minutes.
- The client IP comes from `ClientIPPolicy` (`contracts.md` §5): the TCP peer, unless the peer is in `AW_TRUSTED_PROXIES`; only then `AW_CLIENT_IP_HEADER` (if set), the rightmost untrusted `X-Forwarded-For` hop, or `X-Real-IP`. A header from anyone else is ignored, so a client cannot pick its own rate-limit bucket.
- Clients are forgotten once their 10-minute window has passed, so the limiter's memory stays bounded.
- Over the limit → `429 rate_limited`.
- A wrong code → `403 pair_code_invalid`, which also counts as an attempt.

### 3. State and fan-out (`state.go`)

```go
type State struct {
    // mu; agents map[string]model.AgentState; hostOnline, herdrOnline bool
    // subscribers map[*subscriber]struct{}
}
func (s *State) Snapshot() model.AgentsSnapshot     // uses model.SortAgents; GeneratedAt=model.Now()
func (s *State) ReplaceAll(agents []model.AgentState) // from SnapshotMsg → broadcast "snapshot"
func (s *State) Upsert(a model.AgentState) (prev *model.AgentState) // → broadcast "agent"
func (s *State) Remove(paneID string)               // → broadcast "agent_removed"
func (s *State) SetHost(hostOnline, herdrOnline bool) // → broadcast "host" when changed
func (s *State) Subscribe() (*subscriber, <-chan sseEvent)
func (s *State) Unsubscribe(*subscriber)
```

- **Slow SSE clients:** each subscriber has a buffered channel (64). If it is full, drop that subscriber, close its channel, and let the client reconnect. **Never block the broadcaster.**
- **`Upsert` returns the previous value** so the push dispatcher (3b) can detect transitions.

### 4. Host hub (`hub.go`)

```go
type Hub struct { /* current *hostConn; pending map[requestID]chan model.CommandResultMsg */ }

func (h *Hub) ServeHost(w http.ResponseWriter, r *http.Request) // GET /v1/host (WebSocket)
func (h *Hub) Command(ctx context.Context, cmd model.CommandMsg) (model.CommandResultMsg, error)
```

**`ServeHost`:**
1. Check host auth **before** accepting (401 otherwise).
2. `websocket.Accept(w, r, nil)`, then `SetReadLimit(1 << 20)`.
3. If a host is already connected, close the old connection with status `4000` and reason `replaced`.
4. Wait up to 5 s for `hello` (else close with `4001`; the close frame really goes out, so the bridge sees 4001).
5. Then `SetHost(true, hello.HerdrOnline)`, and start pinging the host every 30 s. **No pong within 10 s → drop the host** (the same as a disconnect, below). A half-open connection is detected within ~40 s instead of lingering.
6. **Read loop** — decode with `model.DecodeWire`:
   - `SnapshotMsg` → `State.ReplaceAll`
   - `AgentUpdateMsg` → `State.Upsert`, then `Notifier.OnAgentUpdate(prev, cur)` (interface below)
   - `AgentRemovedMsg` → `State.Remove`
   - `HistoryItemMsg` → `Store.AddHistory`, then broadcast `history`
   - `HerdrStatusMsg` → `State.SetHost(true, msg.HerdrOnline)`
   - `CommandResultMsg` → deliver to `pending[request_id]`
   - unknown → ignore
7. **On disconnect, missed pong or replacement:** the connection stops being the current host at that moment. `SetHost(false, false)` (unless a new host already took over), and every command waiting on the old connection fails **at once** with `host_offline`. Keep the agents (clients grey them out).

**`Command`:**
- If no host is connected → `host_offline`.
- Otherwise generate a `request_id`, write the `CommandMsg`, then wait for the result. **Writing and waiting share one 7 s budget** (above the bridge's 6 s, below the watch's 8 s) → `timeout`. The host going away first → `host_offline` immediately. A result that arrives at the same instant as either still wins.
- The write is bounded by the budget but not by the caller: a watch hanging up must not close the host's WebSocket mid-write.
- A late result arriving after the timeout is logged (debug) and dropped.

**Notifier hook** (implemented in 3b; use a no-op here):

```go
type Notifier interface {
    OnAgentUpdate(prev *model.AgentState, cur model.AgentState)
}
```

### 5. HTTP API (`api.go`) — Go 1.22 routing

```go
mux := http.NewServeMux()
mux.HandleFunc("GET /v1/healthz", s.healthz)
mux.HandleFunc("POST /v1/pair", s.pair)
mux.Handle("GET /v1/host", s.hub)                                  // WebSocket
mux.Handle("POST /v1/host/pair-code", s.hostAuth(s.pairCode))
mux.Handle("GET /v1/host/status", s.hostAuth(s.hostStatus))
mux.Handle("GET /v1/agents", s.deviceAuth(s.agents))
mux.Handle("GET /v1/events", s.deviceAuth(s.events))
mux.Handle("GET /v1/history", s.deviceAuth(s.history))
mux.Handle("POST /v1/agents/{pane_id}/prompt", s.deviceAuth(s.prompt))
mux.Handle("POST /v1/agents/{pane_id}/answer", s.deviceAuth(s.answer))
mux.Handle("POST /v1/agents/{pane_id}/cancel", s.deviceAuth(s.cancel))
mux.Handle("POST /v1/push/register", s.deviceAuth(s.pushRegister))
```

**Rules for every handler:**
- Limit bodies with `http.MaxBytesReader(w, r.Body, 16<<10)`. Decode with `DisallowUnknownFields` **off**, for forward compatibility.
- Errors always use `writeError(w, code model.ErrorCode, msg string)`, with the HTTP status from `code.HTTPStatus()` and the `ErrorResponse` JSON body.
- **Command handlers:**
  1. Validate the body (e.g. empty `option_id` → `invalid_request`).
  2. Check that the pane exists in `State` (`unknown_pane`).
  3. If the host is offline → `host_offline`. If herdr is offline → `herdr_offline`.
  4. Call `Hub.Command`.
  5. Map `ok=false` + `error_code` to `writeError`.
- **Headers:** `Content-Type: application/json` on every JSON response. No CORS: watches are native apps.
- **Access log:** method, path pattern (not the raw path), status, duration, device id. **Never** log tokens, bodies or prompt text.

### 6. SSE (`sse.go`)

```go
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
    flusher, ok := w.(http.Flusher) // required; 500 if not
    h := w.Header()
    h.Set("Content-Type", "text/event-stream")
    h.Set("Cache-Control", "no-cache")
    h.Set("X-Accel-Buffering", "no")
    // 1. subscribe BEFORE taking the snapshot so no update is lost in between
    // 2. write "event: snapshot\ndata: <json>\n\n", flush
    // 3. loop: select { case ev := <-ch: write+flush; case <-ticker(15s).C: write ":\n\n"+flush; case <-r.Context().Done(): return }
}
```

- **Format:** `data:` is a single line of JSON. Never pretty-print, because newlines break SSE framing.
- **After a host reconnect:** `ReplaceAll` broadcasts a fresh `snapshot` to every subscriber.
- **Every write has a 10 s deadline** (`SetWriteDeadline` through `http.ResponseController`): a client that stops reading is disconnected instead of pinning a goroutine.
- **Never send on a closed subscriber channel:** each subscriber's channel is closed exactly once, and every send happens under the same lock.

### 7. `cmd/relay/main.go`

| Subcommand | Behaviour |
|---|---|
| `serve` | Load the config (fail fast on a missing `AW_HOST_TOKEN` or one that is not 64 hex chars, a bad `AW_TRUSTED_PROXIES`, `AW_CLIENT_IP_HEADER` without trusted proxies, a non-boolean `AW_PUSH_RESOLVED`; warn once if the removed `AW_TRUST_CF_IP` is set). Take the exclusive `flock` on `$AW_DATA_DIR/relay.lock` (a second relay on the same dir refuses to start), open the store, and serve the local admin socket `$AW_DATA_DIR/admin.sock` (`0600`). Serve `http.Server{ReadHeaderTimeout: 10s, IdleTimeout: 120s}` on `AW_LISTEN`, with **no** `ReadTimeout` or `WriteTimeout`: both apply only outside a handler, so they never cut SSE or the hijacked WebSocket. On SIGTERM: cancel every request context at once (open SSE streams end immediately), close the host, shut down within 10 s, flush the store, exit 0 |
| `devices list` | Print id, name, created, last seen |
| `devices revoke <id>` | Remove the device. **Works with the relay running:** the CLI goes through `admin.sock`, the revoked token is rejected from the next request on, the device's open SSE streams and in-flight requests are cancelled, and `store.json` is saved at once. With the relay stopped, the CLI takes the lock and edits `store.json` itself |
| `version` | Print the version |

Run `devices` as the service user or root, with the same `AW_DATA_DIR` as the service (`deploy/relay/README.md`).

### 8. Tests (`pkg/relay/*_test.go`, all with `httptest`)

| Test | Asserts |
|---|---|
| Pairing happy path | `pair-code` (host auth) → `pair` with that code → token works on `/v1/agents` |
| Pairing failures | Expired code, reused code, wrong code → `403`; 6th attempt from the same IP → `429` |
| Token hygiene | `?token=` in the query is ignored; `store.json` contains no raw token; file mode is `0600` |
| Host lifecycle | No hello in 5 s → closed with 4001; a second host replaces the first (4000); disconnect → `host_online=false` broadcast |
| Snapshot + SSE | A subscriber receives `snapshot` first, then `agent`, `agent_removed`, `host`, `history`; keepalive `:` arrives (shorten the interval in tests) |
| Slow subscriber | A subscriber that never reads is dropped without blocking others |
| Command round-trip | `POST …/answer` → the fake host receives `CommandMsg` with the right fields → replies ok → `200 {"ok":true}` |
| Command errors | Host replies `stale_state` → `409`; host silent → `504 timeout`; host offline → `503 host_offline`; unknown pane → `404` |
| Host drop | A missed pong drops the host; a host that disconnects or is replaced while a command waits → `host_offline` at once, not after 7 s |
| Client IP | Forwarding headers ignored from untrusted peers; rightmost untrusted `X-Forwarded-For` hop from trusted ones |
| Live revoke | `devices revoke` through `admin.sock` → the token gets `401` and its SSE stream closes |
| Shutdown | With a watch on SSE and a host connected, `serve` stops quickly and returns nil |
| URL-encoded pane id | `POST /v1/agents/w5%3ApAW/prompt` reaches the handler with `PathValue == "w5:pAW"` |
| History | Dedup by ID; limits 20/200; `GET /v1/history?pane_id=…&limit=5` newest first |
| Store durability | Write, restart the server from the same dir, devices and history survive; a corrupt file → startup error |

---

## Definition of done

- [ ] All tests above pass with `go test -race ./pkg/relay/... ./cmd/relay/...`.
- [ ] `make relay-linux` produces a static binary (`file bin/agent-watch-relay-linux-amd64` shows "statically linked").
- [ ] Local end-to-end with the bridge from 2c (if ready): `AW_HOST_TOKEN=… AW_DATA_DIR=/tmp/awr go run ./cmd/relay serve`, bridge configured with `ws://127.0.0.1:8080/v1/host`, `curl -N -H "Authorization: Bearer <device>" localhost:8080/v1/events` shows the owner's agents.
- [ ] `docs/STATUS.md` Phase 3a ticked.
- [ ] Commit only `pkg/relay`, `cmd/relay`, `go.mod`, `go.sum`, `docs/STATUS.md`.

---

## Pitfalls

- **`WriteTimeout` on the server** kills SSE after N seconds. Leave it at zero; bound each SSE write with a deadline instead.
- **Trusting `X-Forwarded-For` (or `CF-Connecting-IP`) from any peer** lets a client choose its rate-limit bucket. Only from `AW_TRUSTED_PROXIES`.
- **Taking the snapshot before subscribing** loses an update that arrives in between.
- **Holding the state mutex while writing to a subscriber channel** deadlocks under load. Copy the subscribers, then send non-blocking.
- **Logging `r.URL.Path` with pane ids is fine; logging `Authorization` is not.**

---

## Prompt for the executing agent

```
You are executing Phase 3a (the relay server) of the Agent Watch refactor in this repository.
Read AGENTS.md, docs/reference/contracts.md (entirely), HERDR_REFACTOR_PLAN.md §9 and §15, and
docs/phases/3a-relay-server.md. Implement pkg/relay and cmd/relay exactly as specified, with Go 1.22 ServeMux
routing and github.com/coder/websocket. Security is part of correctness: tokens only in Authorization headers,
hashed at rest, constant-time comparisons, rate-limited pairing, no secrets or prompt text in logs. Leave a no-op
Notifier for Phase 3b. Run `go vet ./... && go test -race ./...`, paste the output, tick Phase 3a in docs/STATUS.md,
and commit only pkg/relay, cmd/relay, go.mod, go.sum and docs/STATUS.md.
```
