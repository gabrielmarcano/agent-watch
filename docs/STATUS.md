# Implementation Status

One section per phase.

**Workflow:**
1. **Claim** a phase before starting: write your agent and date on the `Claimed by` line, and commit that change alone.
2. **Tick** the boxes as you finish them.
3. **Record** anything you could not do under **Blocked / questions**.

Keep entries short, and use absolute dates (YYYY-MM-DD).

---

## Phase 0 — Capture agent fixtures
- Claimed by: agy, 2026-09-23
- [x] `herdr integration status` current for claude / agy / opencode (paste output)
  ```
  claude: current (v10) (/Users/me/.claude/hooks/herdr-agent-state.sh)
  opencode: current (v12) (/Users/me/.config/opencode/plugins/herdr-agent-state.js)
  antigravity-cli: current (v3) (/Users/me/.gemini/config/hooks/herdr-agent-state.sh)
  ```
- [x] claude fixtures + golden files
- [x] agy fixtures + golden files
- [x] opencode fixtures + golden files
- [x] transcript samples (scrubbed) + expected results
- [x] no 🔍 left in `docs/reference/agents.md`
- [x] sandbox removed
- Notes:
  - Claude: digit selects immediately without Enter; esc cancels; AskUserQuestion and plan approval render numbered menus (question kind); PromptWhileWorking() = true.
  - Antigravity (agy): digit selects immediately without Enter; esc cancels commands. File edits explicitly disable Esc ("Esc disabled during file edits — press 1 to accept or 2 to reject."), so cancel_keys is ["2"]. Multiple-choice and plan approval are unsupported (.missing.md). PromptWhileWorking() = true.
  - OpenCode: horizontal button bar (Allow once / Allow always / Reject); Allow once is focused by default (Enter confirms); Allow always via Right+Enter; Reject via esc or Right+Right+Enter. Multiple-choice and plan approval unsupported (.missing.md). PromptWhileWorking() = true.
  - All testdata scrubbed of personal paths, emails, usernames, and tokens; passes `git grep -nE '/Users/[a-z]+|@gmail|sk-|ghp_' pkg/agents/testdata`.

## Phase 1 — Foundation
- Claimed by: agy, 2026-09-23
- [x] legacy removed (`bridge/`, `claude-plugin/`, `.claude-plugin/`, `agent_integrations_analysis.md`)
- [x] `go.mod` (`github.com/gabrielmarcano/agent-monitor`, go 1.22) + Makefile
- [x] `pkg/model` + tests (golden `testdata/agent_state.json`)
- [x] `pkg/herdrtest` + tests
- [x] `make check` and `make build` pass
- Notes:
  - Go module github.com/gabrielmarcano/agent-monitor initialized at Go 1.22 floor.
  - All contracts from docs/reference/contracts.md encoded in pkg/model (agent.go, api.go, wire.go) with round-trip, golden file, and helper tests.
  - pkg/herdrtest implements full in-process fake socket server covering 7 verified herdr behaviors.
  - Legacy code removed; zero references to old bridge/plugins remain; make check and make build pass.

## Phase 2a — herdr client
- Claimed by: agy, 2026-09-23
- [x] client, subscribe, syncer
- [x] all tests in the guide's table pass
- [x] read-only smoke test against real herdr (paste 3–5 lines)
  ```
  [HerdrOnline] online=true version=0.9.1 protocol=22
  [Added] pane=w5:pB5 agent=claude status=working focused=false
  [Added] pane=w9:p1 agent=claude status=idle focused=false
  [Added] pane=w7:p1 agent=agy status=working focused=true
  ```
- Notes:
  - Agent-agnostic `pkg/herdr` implemented: `types.go`, `client.go`, `subscribe.go`, `sync.go`.
  - Client respects one connection per call, string IDs, `recent_unwrapped` underscore source, and omits `wait` in prompt.
  - Syncer handles online/offline transitions, initial snapshot diffing, live event-driven debounced re-listing, and fallback degraded polling when event streams drop.
  - Filters out non-agent shells (`agent: null, agent_status: unknown`).
  - All 11 tests pass with race detector; read-only live herdr smoke test verified.

## Phase 2b — Agent adapters
- Claimed by: agy, 2026-09-23
- [x] generic parser + unit tests
- [x] claude adapter (fixtures + transcript)
- [x] agy adapter (fixtures + transcript)
- [x] opencode adapter (fixtures + SQLite)
- [x] CGO-free build confirmed
- Notes:
  - `pkg/agents`: pure Go agent adapters implemented for `generic`, `claude`, `agy`, and `opencode` with zero `pkg/herdr` imports.
  - Generic parser in `menu.go`: parses numbered blocks, continuation lines, cursor detection, role classification via keyword matching, and arrow/digit key generators.
  - All 11 Phase 0 fixtures in `pkg/agents/testdata/{claude,agy,opencode}/` pass against their respective adapters and golden JSON contracts.
  - Synthetic test cases under `pkg/agents/testdata/generic/` verify edge cases (cursors, continuation lines, boxed tables, multiple blocks, non-menus).
  - Transcript readers implemented and verified against expected golden outputs: Claude (`.jsonl`), Antigravity (`transcript_full.jsonl`), and OpenCode (`modernc.org/sqlite` read-only queries on `message` and `part` tables).
  - Screen fallback formatter `ScreenTurn` implemented per `agents.md` §6 with input box cutting and UTF-8 truncation.
  - CGO-free static build verified with `CGO_ENABLED=0` (`make build`).
  - `go test -race ./...` and `go vet ./...` pass with 0 errors.

## Phase 2c — Bridge daemon
- Claimed by: agy, 2026-09-24
- [x] relayclient + tests
- [x] engine + command executor + tests (every error code)
- [x] cmd/bridge subcommands
- [x] launchd install/uninstall verified on the Mac
- [x] herdr plugin linked; start/status/stop/pair work
- [x] sandbox blocked → parsed → answered via stub relay
- Notes:
  - `pkg/relayclient`: outbound WebSocket link to `wss://relay.<domain>/v1/host` with Bearer auth, exponential backoff (1s–60s) + jitter, 30s ping keepalive, sequential `OnMessage`, and bounded 50-item history queue.
  - `pkg/bridge`: `Engine` implements `herdr.Listener` mapping `AgentInfo` to `model.AgentState` (label precedence: Name > TerminalTitleStripped > Base(CWD) > PaneID; ForegroundCWD preference).
  - Prompt parsing with 3x retry before falling back to `UnknownPrompt`.
  - History capture with 500ms debounce, `LastTurn` with screen-turn fallback, and deduplication by `HistoryID`.
  - Safety-critical command executor: per-pane mutex, `Refresh` check, `expected_seq` verification, screen re-reading and `fingerprint` comparison before sending keys. Zero raw watch keys accepted. Audit logging records lengths/IDs, never prompt text.
  - All command error codes tested and verified (`stale_state`, `prompt_changed`, `unknown_option`, `agent_busy`, `agent_blocked`, `agent_state_unknown`, `unknown_pane`, `herdr_offline`, `invalid_request`).
  - `cmd/bridge`: subcommands `configure` (mode 0600), `run`, `start`, `stop`, `status`, `pair`, `version`.
  - macOS launchd service template embedded and verified via `start`/`status`/`stop`.
  - `herdr-plugin.toml` manifest created and linked into live herdr (`herdr plugin link "$PWD"`).
  - All tests (`go test -race ./...`) and guard checks pass cleanly.


## Phase 3a — Relay server
- Claimed by: agy, 2026-09-24
- [x] store, auth/pairing, state, hub, API, SSE
- [x] all tests in the guide's table pass
- [x] static linux binary builds
- Notes:
  - `pkg/relay/config.go`: parses and validates `AW_LISTEN` (default :8080), `AW_HOST_TOKEN` (64 hex characters required), `AW_DATA_DIR` (default /var/lib/agent-watch-relay), and `AW_TRUST_CF_IP`.
  - `pkg/relay/store.go`: atomic JSON storage (`store.json.tmp` -> fsync -> rename) with file permissions `0600`; devices (SHA-256 token hashing, constant-time compare); bounded history (20/pane, 200 total, 7-day pane pruning, ID deduplication); coalesced 1s saves.
  - `pkg/relay/auth.go`: constant-time host token verification; device bearer token authentication with context injection; 6-digit `crypto/rand` pairing codes (5-minute TTL, max 3 active, single-use); sliding-window rate limiting on `/v1/pair` (5/IP/10m, 20 total/10m; respects `CF-Connecting-IP`).
  - `pkg/relay/state.go`: thread-safe `State` tracking agents, `host_online`, `herdr_online`; snapshot sorting; SSE fan-out with non-blocking 64-item buffers dropping slow subscribers.
  - `pkg/relay/hub.go`: `/v1/host` WebSocket endpoint; enforces 1 active host (close 4000 `replaced`); 5s hello handshake check (close 4001); round-trip command routing with 10s timeout; no-op `Notifier` hook for Phase 3b.
  - `pkg/relay/api.go` & `pkg/relay/sse.go`: Go 1.22 routing (`ServeMux`), MaxBytesReader (16KB), structured `ErrorResponse` mapping, `/v1/events` SSE streaming with snapshot on connect and 15s keepalive ticks. Access logging without secrets or prompt text.
  - `cmd/relay/main.go`: subcommands `serve`, `devices list`, `devices revoke <id>`, `version`.
  - Static Linux binary `bin/agent-watch-relay-linux-amd64` builds with `CGO_ENABLED=0` via `make relay-linux`.
  - Full test suite passing with race detector (`go test -race ./...`). Guard checks passing 43/43.

## Phase 3b — Push
- Claimed by: agy, 2026-09-24
- [ ] dispatcher (transitions, debounce, digest) + tests
- [ ] FCM sender + tests
- [ ] ntfy sender + tests
- [ ] manual push check (may be deferred to Phase 5)
- Notes:

## Phase 3c — Relay deploy
- Claimed by: —
- [ ] `deploy/relay/` files (no secrets)
- [ ] systemd service running on the VPS
- [ ] Cloudflare: proxied DNS, Full (strict), cache bypass
- [ ] verify commands pass; SSE open > 3 min through Cloudflare
- Public URL: —
- Notes:

## Phase 4 — Wear OS (primary)
- Claimed by: —
- [ ] models + ContractsTest
- [ ] RelayClient + RelayRepository (SSE lifecycle)
- [ ] pairing, list, detail, PromptCard, dictation, history, reader
- [ ] notifications with answer/cancel/prompt actions
- [ ] complication + tile (target-agent rule)
- [ ] release build (R8) parses JSON
- [ ] verified on the Pixel Watch 2
- Notes:

## Phase 5 — End-to-end + docs (release gate)
- Claimed by: —
- [ ] `docs/e2e-report.md` complete for claude / agy / opencode
- [ ] security spot checks
- [ ] README + ROADMAP rewritten
- Notes:

## Phase 6 — watchOS (best-effort, simulator)
- Claimed by: —
- [ ] models + parity test
- [ ] RelayClient + RelayStore
- [ ] views copied from the Wear OS UX
- [ ] xcodebuild build + test pass
- [ ] verified in simulator (list gaps)
- Notes:

---

## Blocked / questions
- (none)
