# Phase 1 — Foundation: Go Module, Contracts in Code, Fake Herdr, Legacy Removal

> **Goal:** a compiling Go module that contains:
> - every contract from [`docs/reference/contracts.md`](../reference/contracts.md) as Go types, with tests;
> - a fake herdr socket server for tests;
> - no legacy code left in the repo.

| | |
|---|---|
| **Depends on** | nothing |
| **Blocks** | 2a, 2b, 2c, 3a, 3b (all Go work) |
| **Touches** | `go.mod`, `go.sum`, `Makefile`, `.gitignore`, `pkg/model/**`, `pkg/herdrtest/**`, deletes legacy paths, `docs/STATUS.md` |
| **Run alone** | Yes. It creates the module root and deletes directories, so no other agent may commit while it runs |

---

## Read first

- [`AGENTS.md`](../../AGENTS.md)
- [`docs/reference/contracts.md`](../reference/contracts.md) (you will encode it)
- [`docs/reference/herdr-socket-api.md`](../reference/herdr-socket-api.md) §1–§5 (the fake must behave like this)

---

## Steps

### 1. Delete legacy code

```bash
git rm -r -q bridge claude-plugin .claude-plugin agent_integrations_analysis.md
```

Then clean up the files that still mention the old system:
- **`.gitignore`:**
  - remove the `bridge/…` and `node_modules/` lines;
  - add `bin/`, `firebase-service-account*.json`, `store.json`, `.env*` and `!*.example`.
- **`README.md`:** leave it for Phase 5. Only add one line at the top: `> ⚠️ Being rewritten for the herdr-native architecture — see HERDR_REFACTOR_PLAN.md.`

### 2. Initialise the module

```bash
go mod init github.com/gabrielmarcano/agent-monitor
go mod edit -go=1.22
```

- Go 1.22 is the **minimum** the code may use. It brings the `net/http` routing patterns (`"POST /v1/agents/{pane_id}/prompt"`) and `r.PathValue`.
- Do not use newer standard-library features without also raising the `go` line.

**Allowed third-party dependencies** (add them only in the phase that needs them):

| Module | Used by | Phase |
|---|---|---|
| `github.com/coder/websocket` | relayclient, relay | 2c, 3a |
| `github.com/BurntSushi/toml` | bridge config | 2c |
| `modernc.org/sqlite` | opencode transcript reader | 2b |
| `golang.org/x/oauth2` (`google` subpackage) | FCM auth | 3b |

Anything else needs the owner's approval. The standard library is preferred.

### 3. Makefile

```makefile
.PHONY: build bridge relay relay-linux test vet fmt check clean

VERSION ?= 0.2.0
LDFLAGS := -s -w -X main.version=$(VERSION)

build: bridge relay

bridge:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/agent-watch-bridge ./cmd/bridge

relay:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/agent-watch-relay ./cmd/relay

relay-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/agent-watch-relay-linux-amd64 ./cmd/relay

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w cmd pkg

check: fmt vet test

clean:
	rm -rf bin
```

- Add `bin/` to `.gitignore`.
- Until `cmd/bridge` and `cmd/relay` exist, create each `main.go` as a stub, so that `make build` works from day one:

  ```go
  package main

  var version = "dev"

  func main() {}
  ```

### 4. `pkg/model` — encode the contracts

Create these three files and copy the types **exactly** from `contracts.md`:

| File | Content |
|---|---|
| `pkg/model/agent.go` | §1: `AgentStatus` + consts, `AgentState`, `PromptKind` + consts, `OptionRole` + consts, `PromptOption`, `PendingPrompt`, `HistoryItem`, `AgentsSnapshot` |
| `pkg/model/api.go` | §2: request/response bodies, `ErrorResponse`, `ErrorBody`, an `ErrorCode` type with a const for every code in §2.4, and `func (c ErrorCode) HTTPStatus() int` |
| `pkg/model/wire.go` | §3: every message struct, `const` for each `type` string, plus `func DecodeWire(data []byte) (any, error)` that switches on `type` and returns the concrete struct (or `ErrUnknownType`) |

Also add these helpers in `pkg/model/agent.go`, because both sides need them:

```go
// Severity returns a sort weight: blocked=4, done=3, working=2, idle=1, unknown=0.
func (s AgentStatus) Severity() int

// SortAgents orders by Severity desc, then Label asc. Sorts in place.
func SortAgents(agents []AgentState)

// Fingerprint implements contracts.md §1.3.
func Fingerprint(kind PromptKind, title, detail string, labels []string) string

// HistoryID implements contracts.md §1.4.
func HistoryID(paneID, sessionValue, query, response string) string

// TruncateUTF8 cuts s to at most max bytes on a rune boundary and appends "\n\n…[truncated]" when cut.
func TruncateUTF8(s string, max int) string

// Now returns the current UTC time in RFC 3339 (the only timestamp format allowed).
func Now() string
```

**Tests** (`pkg/model/model_test.go`):
- **Round-trip:** for one example of every struct, marshal to JSON and unmarshal back; the result must be equal.
- **Golden JSON:** marshal the `AgentState` example from `contracts.md` §1.2 and compare it byte-for-byte with `pkg/model/testdata/agent_state.json`, which you create from that example. This proves the field names.
- **Omitted fields:** a non-blocked `AgentState` marshals without a `prompt` key. An `unknown` prompt marshals `options` as `[]`, not `null`.
- **`DecodeWire`:** decodes every message type, and returns `ErrUnknownType` for `{"type":"nope"}`.
- **`Fingerprint`:** deterministic, and changes when any label changes.
- **`TruncateUTF8`:** never splits a multi-byte rune. Test it with a string of `ñ` characters.
- **`SortAgents`:** blocked first, then done, then working, then idle, then unknown.

### 5. `pkg/herdrtest` — the fake herdr socket

Every later phase tests against this fake, never against the real herdr.

```go
package herdrtest

// Server is an in-process fake herdr socket server.
type Server struct {
    SocketPath string // e.g. filepath.Join(t.TempDir(), "herdr.sock")
    // unexported: listener, mutex, agents, screens, recorded calls, subscribers
}

func New(t testing.TB) *Server                     // starts listening; t.Cleanup closes it
func (s *Server) SetAgents(agents []map[string]any) // replaces the agent.list payload; bumps nothing by itself
func (s *Server) SetWorkspaces(ws []map[string]any) // replaces the workspace.list payload
func (s *Server) SetScreen(paneID, source, text string) // what agent.read returns
func (s *Server) EmitStatusChanged(paneID, status string) // pushes a pane_agent_status_changed event to matching subscribers
func (s *Server) EmitGlobal(event string, data map[string]any) // e.g. "pane_created"
func (s *Server) Calls() []Call                    // every request received, in order
func (s *Server) FailNext(method, code, message string) // next call to method returns this error
func (s *Server) HoldNext(method string) *Hold     // next call to method waits until Release (a slow or hung herdr)
func (s *Server) DropStreams()                     // close every events.subscribe stream; the server keeps answering
func (s *Server) SetDropStreamsAfterAck(on bool)   // every events.subscribe closes right after its ack (on) or streams normally (off)
func (s *Server) Stop()                            // simulate herdr going away (close listener + streams, abandon held calls)
func (s *Server) Start()                           // come back on the same SocketPath
func (s *Server) Close()                           // stop for good and remove the temp socket dir

type Call struct {
    ID     string // the request id, as the client sent it
    Method string
    Params map[string]any
}

// Hold pauses one call. The answer is computed when the request arrives and
// written only after Release; for events.subscribe, the ack and the stream
// start after Release. A call never released is dropped when the server stops.
type Hold struct{ /* unexported */ }
func (h *Hold) Received() <-chan struct{} // closed once the held call reached the server
func (h *Hold) Release()                  // let it be answered; safe to call twice
```

- **Event writes are bounded:** each `Emit*` write to a subscriber has a 2 s deadline. A subscriber that stops reading is dropped, as herdr drops a dead stream, so an `Emit*` call never blocks the test.
- **Subscribers are registered before the ack** is written, so an event emitted right after `Subscribe` returns is delivered.

**Behaviour it must copy from real herdr** (see `herdr-socket-api.md`):

1. **One request per connection.** Read one line, write one line, close. For `events.subscribe`, keep the connection open instead.
2. **Reject a numeric `id`** with `{"id":"","error":{"code":"invalid_request",...}}`.
3. **`ping`** → `{"type":"pong","version":"0.9.1","protocol":22,"capabilities":{}}`.
4. **`agent.list`** → `{"type":"agent_list","agents":[...]}`.
5. **`agent.read`:**
   - returns `{"type":"pane_read","read":{"pane_id":..., "text":..., "truncated":false, "revision":0, "source":..., "format":"text", "workspace_id":..., "tab_id":...}}`;
   - rejects `source` values other than `visible`, `recent`, `recent_unwrapped` and `detection` with `invalid_request`, **including `recent-unwrapped`**.
6. **`agent.send_keys` / `agent.prompt`:**
   - record the call;
   - unknown target → `agent_not_found`;
   - `agent.prompt` on an agent whose status is `blocked` → `agent_blocked`;
   - otherwise → `{"type":"ok"}`.
7. **`events.subscribe`:**
   - reply `{"id":..., "result":{"type":"subscription_started"}}`;
   - a `pane.agent_status_changed` subscription without `pane_id` → `invalid_request: missing field \`pane_id\``;
   - stream events as `{"event":"pane_agent_status_changed","data":{"pane_id":...,"workspace_id":...,"agent_status":...}}`, **only** to streams subscribed to that `pane_id`.

**Tests** (`pkg/herdrtest/server_test.go`): exercise each behaviour above using a raw `net.Dial("unix", …)`, not a client library. The client library is Phase 2a.

### 6. Verify

```bash
gofmt -l cmd pkg        # must print nothing
go vet ./...
go test -race ./...
make build
git grep -nE 'osascript|agy-sidecar|claude-plugin|/webhook|Warp' -- ':!docs' ':!HERDR_REFACTOR_PLAN.md' ':!AGENTS.md' ':!.agents' ':!tools/guards'
                        # must print nothing (except client code, which Phase 4/6 rewrite)
```

---

## Definition of done

- [ ] Legacy directories removed; `.gitignore` updated; README banner added.
- [ ] `go.mod` has module `github.com/gabrielmarcano/agent-monitor`, `go 1.22`.
- [ ] `pkg/model` matches `contracts.md` field-for-field; all tests pass.
- [ ] `pkg/herdrtest` implements all 7 behaviours; all tests pass.
- [ ] `make check` and `make build` succeed.
- [ ] `docs/STATUS.md` Phase 1 ticked.
- [ ] Commit with explicit paths: `git add go.mod Makefile .gitignore README.md cmd pkg/model pkg/herdrtest docs/STATUS.md`, plus the deletions already staged by `git rm`.

---

## Pitfalls

- **`json:"options"` without `omitempty`**, and initialise the slice to `[]PromptOption{}`. Otherwise an unknown prompt serialises as `null`, and the Kotlin/Swift decoders expect an array.
- **`uint64` for `state_change_seq`.** Never `int`.
- **Timestamps:** only through `model.Now()`, so the format never drifts.
- **Socket path length:** macOS limits Unix socket paths to 104 bytes, and `t.TempDir()` paths can be long. If `Listen` fails, use `os.MkdirTemp("", "hs")` for a shorter path.

---

## Prompt for the executing agent

```
You are executing Phase 1 of the Agent Watch refactor in this repository.
Read AGENTS.md, then docs/phases/1-foundation.md, and follow it exactly. The types must match
docs/reference/contracts.md field for field; the fake server must behave as docs/reference/herdr-socket-api.md
describes. Use Go 1.22 features only and no third-party modules in this phase. Do not contact the real herdr
socket from tests. Run the Verify commands and paste their output in your final report. Commit only the paths
listed in the Definition of Done (never `git add -A`), and tick Phase 1 in docs/STATUS.md.
```
