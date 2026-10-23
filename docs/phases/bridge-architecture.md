# Bridge Architecture Deepening Guide

A temporary guide for deepening shallow modules in the bridge (`cmd/bridge`, `pkg/bridge`, `pkg/agents`, `pkg/relayclient`). Delete this file when all slices are done (`docs/STATUS.md` workflow), after moving any lasting facts to their home (`AGENTS.md` §0).

## Workflow & Global Constraints

- **Claim before starting:** put your agent name and date on the slice you are taking in [`docs/STATUS.md`](../STATUS.md) under **Open items → Bridge architecture deepening**, and commit that claim alone before editing code.
- **Rules that apply:** [`AGENTS.md`](../../AGENTS.md), [`.agents/rules/go-backend.md`](../../.agents/rules/go-backend.md), [`.agents/rules/herdr-integration.md`](../../.agents/rules/herdr-integration.md), [`.agents/rules/agent-adapters.md`](../../.agents/rules/agent-adapters.md), and [`.agents/rules/contracts.md`](../../.agents/rules/contracts.md).
- **Package boundaries (non-negotiable):**
  - `pkg/herdr` knows no agent names.
  - `pkg/agents` does not import `pkg/herdr` and performs no network I/O.
  - All agent-specific knowledge lives in `pkg/agents` and nowhere else (`AGENTS.md` §1.2).
  - No wire contract changes (`pkg/model`, `docs/reference/contracts.md`) are required by any slice below.
- **Testing discipline ("replace, don't layer"):**
  - The module's **interface is the test surface**. When a slice deepens a module, delete tests that poke private struct fields or internal locks and replace them with tests that cross the deepened interface using `pkg/herdrtest`, `httptest`, and temp directories.
  - Never use `time.Sleep` longer than 50 ms in tests (`go-backend.md`).
- **Verification before committing each slice:**
  ```bash
  gofmt -l cmd pkg deploy   # must be empty
  go vet ./...
  go test -race ./...
  CGO_ENABLED=0 go build ./...
  ```

## Order and Parallelism

| Slice | Scope | Can run in parallel with |
|---|---|---|
| **Slice 1** | `pkg/agents/*`, `pkg/bridge/engine.go` (history, turn-end, tasks, titles) | Slice 4, Slice 5 |
| **Slice 2** | `cmd/bridge/main.go`, `pkg/bridge/engine.go`, `pkg/bridge/*_test.go` (daemon lifecycle) | Run **after** Slice 1 and Slice 3 (shares `pkg/bridge/engine.go`) |
| **Slice 3** | `pkg/bridge/cmdguard.go`, `pkg/bridge/commands.go`, `pkg/bridge/sessions.go`, `pkg/bridge/engine.go` | Slice 4, Slice 5 (coordinate `engine.go` lines 84–90, 170, 186 if run with Slice 1) |
| **Slice 4** | `cmd/bridge/app.go`, `lifecycle.go`, `status.go`, `envfile.go`, `service.go`, `pkg/bridge/config.go`, `localstatus.go` | Slice 1, Slice 3 (coordinate `cmd/bridge/status.go` with Slice 5) |
| **Slice 5** | `pkg/relayclient/*`, `cmd/bridge/status.go`, `cmd/bridge/pair.go`, `pkg/bridge/config.go` | Slice 1, Slice 3 |

---

## Slice 1: Collapse the Agent Turn & Task Observation Pipeline

**Strength:** `Strong` · **Category:** `local-substitutable`

### Files Involved
- `pkg/agents/adapter.go`
- `pkg/agents/screen.go`
- `pkg/agents/generic.go`
- `pkg/agents/claude.go`, `pkg/agents/claude_session.go`, `pkg/agents/claude_turnend.go`, `pkg/agents/claude_tasks.go`
- `pkg/agents/agy.go`
- `pkg/agents/opencode.go`
- `pkg/bridge/engine.go`

### Current Architectural Friction
1. **Fragmented seam across `pkg/agents` and `pkg/bridge`:** `pkg/agents` splits one concept—observing an agent's conversation state and turns—across six types (`Adapter`, `TurnEndReader`, `BackgroundTaskReader`, `ViewDetector`, `HistoryReader`, and `ScreenTurnReader`). `pkg/bridge/engine.go` has to type-assert these optional interfaces in four places:
   - `backToConversation` (`pkg/bridge/engine.go:370–379`): asserts `agents.ViewDetector`.
   - `captureHistory` (`pkg/bridge/engine.go:388–471`): asserts `agents.ViewDetector`, then `agents.BackgroundTaskReader`, then calls `ad.LastTurn`, and on error falls back to `agents.ScreenTurnFor(ad, text)` (`pkg/agents/screen.go:26–47`), which asserts `agents.ViewDetector` and `agents.ScreenTurnReader` again.
   - `maybeBackfillHistoryLocked` & `backfillHistory` (`pkg/bridge/engine.go:513–605`): asserts `agents.HistoryReader` and `agents.ViewDetector`.
   - `checkWorkingTurns` (`pkg/bridge/engine.go:644–709`): asserts `agents.TurnEndReader`, `agents.ViewDetector`, and `agents.BackgroundTaskReader`.
2. **Agent-specific leakage in `pkg/bridge`:** `agentNames` (`pkg/bridge/engine.go:911–922`) filters terminal titles with `!strings.HasPrefix(t, "agy --conversation") && t != "OpenCode"`. That violates `AGENTS.md` §1.2 ("All agent-specific knowledge lives in `pkg/agents` and nowhere else").
3. **Duplicate transcript I/O in `claudeAdapter`:** During a single `captureHistory` call or a single `checkWorkingTurns` tick, `Engine` calls `LastTurn` (or `LastCompletedTurn`) and `BackgroundTasks` back-to-back with the same `SessionRef`. Inside `pkg/agents/claude.go`, `claude_turnend.go`, and `claude_tasks.go`, both calls independently run `c.sessionPath(ctx, ref)` (`pkg/agents/claude_session.go:26–61`), which stats and tail-reads the transcript to verify `transcriptTitles` and may scan `<configDir>/sessions/*.json`.

### Target Deepened Design
1. **Move title sanitization into `pkg/agents`:**
   - Add `TaskTitle(rawTitle string) string` (or `CleanTitle`) to `agents.Adapter` with the default implementation on `genericAdapter` returning `strings.TrimSpace(rawTitle)`, `agyAdapter` rejecting `"agy --conversation..."` titles, and `opencodeAdapter` rejecting `"OpenCode"`.
   - Delete the hardcoded `"agy --conversation"` and `"OpenCode"` strings from `pkg/bridge/engine.go:917`.
2. **Fold optional reader interfaces into cohesive `Adapter` methods (with defaults on `genericAdapter`):**
   - `ShowsConversation(title string) bool` on `Adapter` (default `true` on `genericAdapter`; overridden on `claudeAdapter`).
   - `ScreenTurn(screen string) *model.HistoryItem` on `Adapter` (absorbs `ScreenTurnFor`, `ConversationScreen`, and `SplitScreenTurn` so callers never type-assert `ViewDetector` or `ScreenTurnReader`).
   - `RecentTurns(ctx context.Context, ref SessionRef, max int) ([]model.HistoryItem, error)` on `Adapter` (default returns `nil, ErrNoTranscript` on `genericAdapter`). Calling `RecentTurns` when `!ad.ShowsConversation(ref.Title)` returns `nil, ErrNoTranscript` inside the adapter.
   - Unify turn and background-task observation so `Engine` does not separately orchestrate `TurnEndReader` + `BackgroundTaskReader` + `ViewDetector`:
     - Either provide `ObserveWorkingTurn(ctx context.Context, ref SessionRef) (TurnEnd, BackgroundTasks, bool, error)` and `ObserveSettledTurn(ctx context.Context, ref SessionRef) (*model.HistoryItem, BackgroundTasks, error)`, or have `claudeAdapter` resolve `sessionPath` once per `SessionRef` and cache/share the resolved path across `LastCompletedTurn`/`LastTurn` and `BackgroundTasks`.
3. **Internalize sub-interfaces:** `ScreenTurnReader`, `ViewDetector`, `TurnEndReader`, `BackgroundTaskReader`, and `HistoryReader` can become unexported implementation details of `pkg/agents` (or removed once methods live on `Adapter` with `genericAdapter` defaults).

### Verification & Tests
- Add a unit test in `pkg/agents/adapters_test.go` verifying `TaskTitle` across `claude`, `agy` (`"agy --conversation foo"` → `""`), `opencode` (`"OpenCode"` → `""`, `"Fixing bug"` → `"Fixing bug"`), and `generic`.
- Verify in `pkg/agents/claude_turnend_test.go` and `pkg/agents/claude_tasks_test.go` that observing a turn + background tasks resolves the session path once.
- Ensure `grep -rn "agy --conversation\|OpenCode" pkg/bridge/` returns empty.

---

## Slice 2: Deepen the Bridge Daemon Lifecycle & Engine Seam

**Strength:** `Strong` · **Category:** `local-substitutable`

### Files Involved
- `cmd/bridge/main.go`
- `pkg/bridge/engine.go`
- `pkg/bridge/presence.go`
- `pkg/bridge/engine_test.go`, `pkg/bridge/engine_cache_test.go`, `pkg/bridge/engine_history_test.go`, `pkg/bridge/engine_turnend_test.go`, `pkg/bridge/commands_test.go`, `pkg/bridge/sessions_test.go`

### Current Architectural Friction
1. **Wide, shallow lifecycle surface on `Engine`:** `Engine` (`pkg/bridge/engine.go:33–91`) exposes 19 exported struct fields (`Herdr`, `Syncer`, `Agents`, `Relay`, `Version`, `HostName`, `StatusPath`, `Logger`, `RetryDelay`, `HistoryDelay`, `TranscriptTimeout`, `ScreenReadTimeout`, `ScreenRetryTimeout`, `TurnCheckInterval`, `Presence`, `PresenceInterval`, `PresenceEnabled`, `CommandTimeout`, `SessionRoots`, `SessionKinds`, `SessionTimeout`, `FolderTimeout`) plus 7 exported wiring methods (`OnHerdrOnline`, `OnChanges`, `ConnectMessages`, `HandleRelayMessage`, `StartStatusWriter`, `StartTurnWatch`, `StartPresence`).
2. **Duplicated manual wiring in `runDaemon` and `newTestHarness`:**
   - `runDaemon` (`cmd/bridge/main.go:126–207`) is **completely untested**. It constructs `herdr.Client`, `herdr.Syncer`, `agents.Registry`, `relayclient.Client`, and `bridge.Engine`, mutates 6 exported fields on `Engine`, wires 3 circular callbacks (`syncer.Listener = engine`, `rClient.OnConnect = engine.ConnectMessages`, `rClient.OnMessage = engine.HandleRelayMessage`), starts 3 background loops (`StartStatusWriter`, `StartTurnWatch`, `StartPresence`), spawns `syncer.Run(ctx)`, runs `rClient.Run(ctx)`, and waits on `<-writerDone`.
   - `newTestHarness` (`pkg/bridge/engine_test.go:38–125`) duplicates that assembly in tests, but omits `syncer.Run(ctx)`, `StartStatusWriter`, `StartTurnWatch`, and `StartPresence`.
3. **White-box test poking past the interface:** Because `newTestHarness` does not run the full `Engine` lifecycle, tests reach directly into `Engine`'s private fields and locks:
   - `h.engine.herdrOnline = true` (`pkg/bridge/commands_test.go:61, 104, 287, 446`, `pkg/bridge/sessions_test.go:270, 338`)
   - `h.engine.mu.Lock(); h.engine.startWorkspaceRefreshLocked(); h.engine.mu.Unlock()` (`pkg/bridge/engine_test.go:268–270`)
   - `h.engine.mu.RLock(); h.engine.tabs[...]` (`pkg/bridge/engine_test.go:237–240`)
   - `h.engine.wsRefreshes` (`pkg/bridge/engine_cache_test.go:91, 99, 121`)
   - `h.engine.checkWorkingTurns(ctx)` (`pkg/bridge/engine_turnend_test.go:53`)
   - `h.engine.captureHistory(...)` (`pkg/bridge/engine_history_test.go:155`)

### Target Deepened Design
1. **Single `Run(ctx context.Context) error` lifecycle method on `Engine`:**
   - Have `NewEngine` (or `New(EngineConfig)`) wire `syncer.Listener`, `relay.OnConnect`, and `relay.OnMessage` internally at construction time.
   - Add `func (e *Engine) Run(ctx context.Context) error`, which starts `StartStatusWriter`, `StartTurnWatch`, `StartPresence`, `Syncer.Run(ctx)`, and `Relay.Run(ctx)`, and waits for `status.json` to flush its final stopped status before returning when `ctx` is cancelled.
2. **Shrink `runDaemon` in `cmd/bridge/main.go`:**
   - `runDaemon` parses CLI flags (`--config`, `--socket`, `--state-dir`), loads config, builds `Engine`, and calls `engine.Run(ctx)`.
3. **Test through the interface (`herdrtest.Server` + `httptest.Server`):**
   - Update `newTestHarness` so tests can drive `Engine` through `herdrtest.Server` state changes and `Relay` messages without mutating `h.engine.herdrOnline`, `h.engine.tabs`, or `h.engine.mu`.
   - Assert workspace/tab refresh counts on `herdrtest.Server` call counts rather than an internal `e.wsRefreshes` counter on `Engine`.

### Verification & Tests
- Add a lifecycle test in `pkg/bridge/engine_test.go` (or `cmd/bridge/main_test.go`) that runs `engine.Run(ctx)` against `herdrtest.Server` and `httptest.Server`, verifies initial snapshot + `status.json` creation, cancels `ctx`, and verifies `status.json` is written with `pid: 0`.
- Remove direct writes to `h.engine.herdrOnline` and direct locking of `h.engine.mu` from `pkg/bridge/*_test.go`.

---

## Slice 3: Extract a Self-Contained Command Guard & Executor Module

**Strength:** `Strong` · **Category:** `in-process`

### Files Involved
- `pkg/bridge/cmdguard.go`
- `pkg/bridge/commands.go`
- `pkg/bridge/sessions.go`
- `pkg/bridge/engine.go`
- `pkg/bridge/commands_safety_test.go`

### Current Architectural Friction
1. **Four separate locks and state maps mixed into `Engine`:**
   - `e.consumed` (`map[string]*consumedPrompts`) is guarded by `e.mu` (`pkg/bridge/cmdguard.go:67`), the same mutex guarding `e.states`, `e.workspaces`, and `e.tabs`.
   - `e.paneLocks` (`map[string]*paneLock`) is guarded by `e.lockMu` (`pkg/bridge/cmdguard.go:148`).
   - `e.requests` (`requestIDLog`) has its own `l.mu` (`pkg/bridge/cmdguard.go:28`).
   - `e.sessionLock` and `e.sessionLockDo` (`pkg/bridge/sessions.go:108–122`) serialize `session.create` commands.
2. **Leakage of replay-guard bookkeeping into `OnChanges`:** `Engine.OnChanges` (`pkg/bridge/engine.go:170, 186`) has to call `e.forgetConsumedLocked(paneID, ...)` while holding `e.mu`, and `executeCommand` (`pkg/bridge/commands.go:182`) calls `e.pruneConsumed(agentsList)`.
3. **Asymmetric claim rollback bug between `Prompt` and `pressPromptKeys`:**
   - In `executeCommand` (`pkg/bridge/commands.go:249–266`), `prompt` calls `e.claimPrompt(cmd.PaneID, info.StateChangeSeq, claim)` before `e.Herdr.Prompt(...)` and calls `e.releasePrompt(...)` when `err` is `herdr.ErrUnavailable` (socket dial failure, so nothing reached herdr) or `agent_blocked`.
   - In `pressPromptKeys` (`pkg/bridge/commands.go:375–386`), used by `answer` and `cancel`, `e.claimPrompt(paneID, seq, fp)` is called before `e.Herdr.SendKeys(...)`, but **never calls `e.releasePrompt`** if `e.Herdr.SendKeys` returns `herdr.ErrUnavailable`! If the herdr socket is briefly unreachable during `SendKeys`, the prompt stays claimed at `seq` and subsequent retries at that `seq` fail with `stale_state` (`"prompt already answered"`).
4. **White-box tests poking internal guard maps:** `pkg/bridge/commands_safety_test.go` (`lines 162, 182–185, 531–565`) directly locks `h.engine.mu` and `h.engine.lockMu` and inspects `len(h.engine.consumed)` and `len(h.engine.paneLocks)`.

### Target Deepened Design
1. **Self-contained `commandGuard` struct in `pkg/bridge/cmdguard.go`:**
   - Owns its own mutex, `paneLocks`, `sessionLock`, `requests` ring buffer, and `consumed` per-pane `(seq, claims)` map—completely decoupled from `Engine.mu`.
   - Provides a small internal interface to `commands.go` and `sessions.go`:
     - `AcquirePane(ctx context.Context, paneID, requestID string) (release func(), duplicate bool, err error)` — acquires the per-pane lock within `ctx` and checks/records `requestID` under that lock.
     - `AcquireSession(ctx context.Context, requestID string) (release func(), duplicate bool, err error)` — acquires the host-session lock within `ctx` and checks/records `requestID`.
     - `DoClaim(paneID string, seq uint64, claim string, fn func() error) (alreadyClaimed bool, err error)` — atomically claims `(paneID, seq, claim)`, runs `fn()`, and automatically rolls back the claim if `fn()` fails with `errors.Is(err, herdr.ErrUnavailable)` or `herdr.IsCode(err, "agent_blocked")`.
     - `Prune(live []herdr.AgentInfo)` and `Forget(paneID string, seq uint64, gone bool)` — self-locked methods so `Engine.mu` is never required for command guard state.
2. **Use `DoClaim` for both `Prompt` and `pressPromptKeys`:**
   - Eliminates the duplicate claim/release pattern and fixes the `SendKeys` dial-failure rollback bug.

### Verification & Tests
- Add a test in `pkg/bridge/commands_safety_test.go` where `SendKeys` fails with `herdr.ErrUnavailable` (e.g. socket unavailable during send) and verify that a retry of `answer` or `cancel` with a new `request_id` at the same `expected_seq` succeeds once herdr is reachable again.
- Replace white-box assertions on `h.engine.consumed` and `h.engine.paneLocks` in `commands_safety_test.go` with behavioural tests through `HandleRelayMessage`.

---

## Slice 4: Unify Configuration, Path Resolution, and Local Status

**Strength:** `Worth exploring` · **Category:** `local-substitutable`

### Files Involved
- `cmd/bridge/app.go`
- `cmd/bridge/lifecycle.go`
- `cmd/bridge/status.go`
- `cmd/bridge/presence.go`
- `cmd/bridge/envfile.go`
- `cmd/bridge/service.go`
- `pkg/bridge/config.go`
- `pkg/bridge/localstatus.go`

### Current Architectural Friction
1. **Duplicated default path computation:**
   - `cmd/bridge/app.go:65–97` (`defaults()` and `envSpec()`) computes default `ConfigPath`, `SocketPath`, `StateDir`, and `LogPath` from `a.home` and `a.getenv`.
   - `pkg/bridge/config.go:61–83` (`DefaultConfigPath()` and `DefaultStateDir()`) computes the exact same paths again using `os.Getenv` and `os.UserHomeDir()`.
2. **Triplicated atomic file write implementation:**
   - `cmd/bridge/service.go:100–120` (`writeFileAtomic`)
   - `pkg/bridge/config.go:185–205` (`SaveConfig`)
   - `pkg/bridge/config.go:259–267` (`WriteStatus`)
   All three implement `CreateTemp` / write / `Chmod` / `Close` / `Rename` with slight variations.
3. **Split `LocalStatus` construction across `cmd/bridge` and `pkg/bridge`:**
   - `cmd/bridge/status.go:22–64` (`localStatus()`) populates half of `bridge.LocalStatus` (checking config, reading `status.json`, setting `ls.LastError = fmt.Sprintf("read status file: %v", err)` when `ReadStatus` fails with a non-ENOENT error).
   - It then calls `ls.ApplyStatusFile(stp, a.now(), a.pidAlive)` in `pkg/bridge/localstatus.go:50–81`, which unconditionally overwrites `ls.LastError = ""` when `stp == nil` (`localstatus.go:53`).
   - To work around that leak across the seam, `cmd/bridge/status.go:58–62` has to stash `lastErr := ls.LastError` before calling `ApplyStatusFile` and restore `ls.LastError = lastErr` afterwards!
4. **Config merge and `.env` parsing trapped in `cmd/bridge`:**
   - `cmd/bridge/envfile.go` (`ReadEnvFile`) and `cmd/bridge/lifecycle.go:34–118` (`cmdConfigure` merging existing config fields such as `PushPresence`, `SessionRoots`, `SessionKinds`) live in `package main` instead of `pkg/bridge`.

### Target Deepened Design
1. **Single atomic file writer in `pkg/bridge`:**
   - Extract `WriteFileAtomic(path string, data []byte, perm os.FileMode, dirPerm os.FileMode) error` in `pkg/bridge/config.go` and use it in `SaveConfig`, `WriteStatus`, and service definition installation.
2. **Move `ReadEnvFile` and config-merge logic into `pkg/bridge`:**
   - Move `envfile.go` into `pkg/bridge` and provide a cohesive config-update helper that merges flags, `agent-watch.env`, and preserved fields (`PushPresence`, `SessionRoots`, `SessionKinds`) while reporting which existing fields were kept or cleared.
3. **Complete `LocalStatus` evaluation in `pkg/bridge/localstatus.go`:**
   - Have `pkg/bridge` assemble the entire `LocalStatus` (config check, `ReadStatus`, `ApplyStatusFile`, and error attribution when `status.json` is unreadable) in one function so `cmd/bridge/status.go` never stashes and restores `ls.LastError`.
   - Unify path default helpers in `pkg/bridge` parameterized by `(home string, getenv func(string) string)` so `cmd/bridge` and `pkg/bridge` share one source of truth.

### Verification & Tests
- Move `cmd/bridge/envfile_test.go` to `pkg/bridge/` and verify all existing `cmd/bridge` and `pkg/bridge` status/config tests pass without the `lastErr` workaround in `cmd/bridge/status.go`.

---

## Slice 5: Deepen `pkg/relayclient` to Own All Host-to-Relay Communication

**Strength:** `Worth exploring` · **Category:** `ports & adapters`

### Files Involved
- `pkg/relayclient/client.go`
- `pkg/relayclient/client_test.go`
- `cmd/bridge/status.go`
- `cmd/bridge/pair.go`
- `pkg/bridge/config.go`

### Current Architectural Friction
1. **Scattered host-to-relay HTTP transport:**
   - `pkg/relayclient/client.go` handles the outbound WebSocket stream (`/v1/host/stream`) with Bearer authentication.
   - `pkg/bridge/config.go:211–241` holds `HTTPSBase` and `NormalizeRelayURL` (`ws(s)://` ↔ `http(s)://`).
   - `cmd/bridge/status.go:183–212` (`relayStatus`) and `cmd/bridge/pair.go:41–75` (`cmdPair`) each manually convert the WebSocket URL via `bridge.HTTPSBase`, construct an `http.Request` for `GET /v1/host/status` or `POST /v1/host/pair-code`, attach `Authorization: Bearer <token>`, read the body with `io.LimitReader`, map HTTP status codes (`401`/`403`), and decode JSON.
2. **White-box test poking in `pkg/relayclient/client_test.go`:**
   - Unlike `pkg/herdr/clock.go`, `pkg/relayclient/client.go` calls `time.Since(connectedAt) >= healthyAfter` (`client.go:118`) and `time.NewTimer` directly without an internal clock seam.
   - Because of this, `pkg/relayclient/client_test.go` reaches past `Client`'s interface to mutate private fields and call private methods directly:
     - `c.connected = true` and `c.sendCh = make(chan outMsg, 1)` (`client_test.go:488–495, 520–524`)
     - `c.connectAndServe(ctx)` (`client_test.go:557`)
     - `c.mu.Lock(); c.historyQ = ...; c.requeueUnsentLocked(ch)` (`client_test.go:596–609`)

### Target Deepened Design
1. **Deepen `pkg/relayclient` to own all host-authenticated relay calls:**
   - Add `HostStatus(ctx context.Context, httpClient *http.Client, relayURL, hostToken string) (HostStatus, error)` and `RequestPairCode(ctx context.Context, httpClient *http.Client, relayURL, hostToken string) (model.PairCodeResponse, error)` (or methods on `relayclient.Client`) in `pkg/relayclient`.
   - Centralize URL scheme conversion (`NormalizeRelayURL`, `HTTPSBase`) in `pkg/relayclient` (keeping thin aliases in `pkg/bridge` only if needed by callers).
   - Simplify `cmd/bridge/status.go` and `cmd/bridge/pair.go` to call `pkg/relayclient` and format the CLI output.
2. **Add an internal clock seam to `pkg/relayclient` and replace white-box tests:**
   - Introduce an internal clock/timer seam in `pkg/relayclient` (matching `pkg/herdr/clock.go`).
   - Replace the tests in `pkg/relayclient/client_test.go:488–614` that mutate `c.connected`, `c.sendCh`, and `c.historyQ` directly with tests that exercise `Client.Run(ctx)` and `Client.Send(msg)` against an `httptest.Server` that drops connections or stalls frames.

### Verification & Tests
- Verify `pkg/relayclient/client_test.go` has zero direct writes to `c.connected`, `c.sendCh`, or `c.historyQ`.
- Run `go test -race ./pkg/relayclient/... ./cmd/bridge/... ./pkg/bridge/...`.
