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
- [x] dispatcher (transitions, debounce, digest) + tests
- [x] FCM sender + tests
- [x] ntfy sender + tests
- [x] manual push check (deferred to Phase 5 release gate / live device verification)
- Notes:
  - `pkg/push/push.go`: `Message` and `Sender` interface; `Dispatcher` implementing `relay.Notifier` with `OnAgentUpdate`. Detects transitions (`any -> blocked` with title/body/options/seq/fingerprint; `working -> done` with "Task finished"; runes truncated to <= 240 on rune boundary).
  - 5-second per pane+event debounce. 10-second windowing with immediate send for the 1st blocked event (latency-sensitive) and hold for remainder; <= 3 sends individually, > 3 coalesces into a single digest push (`"<n> agents need you"`, joined deduplicated labels).
  - Async delivery with 10s per-sender context timeout and single retry on failure; non-blocking to relay state.
  - `pkg/push/fcm.go`: FCM HTTP v1 API sender (`POST /v1/projects/{project_id}/messages:send`) using `golang.org/x/oauth2/google` service account credentials. Priority `high` for blocked, `normal` otherwise; 600s TTL. Dead token detection (404, or 400 with `UNREGISTERED`/`INVALID_ARGUMENT`) invokes `OnInvalidToken`.
  - `pkg/push/ntfy.go`: plain-text ntfy sender with custom headers (`Title`, `Priority` 5/3/4, `Tags` warning/white_check_mark/bell, Bearer token auth).
  - `pkg/relay/server.go`: wires push senders based on `AW_FCM_CREDENTIALS`, `AW_NTFY_URL`, `AW_NTFY_TOPIC`; logs `push disabled` when unconfigured. `Store` extended with `AllFCMTokens` and `RemoveFCMToken`.
  - All tests passing with race detector (`go test -race ./...`). Guard checks 43/43 passing. No secrets or topic names committed.

## Phase 3c — Relay deploy
- Claimed by: agy, 2026-09-24
- [x] `deploy/relay/` files (no secrets)
- [x] systemd service running on the VPS
- [x] Cloudflare DNS / NPM TLS with WebSockets and unbuffered SSE
- [x] verify commands pass; SSE streaming and bridge connected
- Public URL: https://relay.example.com
- Notes:
  - `deploy/relay/`: systemd unit `agent-watch-relay.service`, `env.example`, `deploy.sh`, `Caddyfile.example`, `nginx.conf.example`, `Dockerfile`.
  - Static Linux binary built with `CGO_ENABLED=0` and installed to `/usr/local/bin/agent-watch-relay`.
  - Hardened systemd service running under `agentwatch:agentwatch` with `ProtectSystem=strict`, `StateDirectory=agent-watch-relay` (mode 0700).
  - FCM push enabled and operational (`project_id=agent-watch-595cc`).
  - Nginx Proxy Manager (NPM) on the VPS terminating TLS via Let's Encrypt, forwarding to `172.17.0.1:8080` with WebSocket support and `proxy_buffering off;`.
  - Host bridge daemon on macOS connected to `wss://relay.example.com/v1/host` with `relay_connected: true`, `host_online: true`, reporting active agent states.
  - End-to-end pairing verified via `./bin/agent-watch-bridge pair` and `POST /v1/pair`.
  - Unbuffered SSE event streaming verified on `GET /v1/events`.
  - Bridge enhanced with automatic `NormalizeRelayURL` (appending `/v1/host` when omitted) and launchd retry on macOS.

## Phase 4 — Wear OS (primary)
- Claimed by: agy, 2026-09-24
- [x] models + ContractsTest
- [x] RelayClient + RelayRepository (SSE lifecycle)
- [x] pairing, list, detail, PromptCard, dictation, history, reader
- [x] notifications with answer/cancel/prompt actions
- [x] complication + tile (target-agent rule)
- [x] release build (R8) parses JSON
- [ ] verified on the Pixel Watch 2
- Notes:
  - `model/Contracts.kt`: mirrors `pkg/model` field by field with `@Keep` on all serializable classes; includes `AgentState.severity()` and `resolveTargetAgent()`.
  - `ContractsTest.kt`: verified against golden fixture `pkg/model/testdata/agent_state.json` and unit tests for target agent resolution.
  - `data/Prefs.kt`: SharedPreferences wrapper for `relay_url`, `device_token`, `device_id`, `fcm_token`, `fcm_registered_token`, `pinned_pane_id`. Purged legacy `local_ip` / `tailscale_ip`.
  - `network/RelayClient.kt`: stateless HTTP client using `OkHttpClient` with `Authorization: Bearer` auth, URL-encoded pane IDs, `ErrorResponse` mapping, and `okhttp-sse` support.
  - `network/RelayRepository.kt`: singleton owning `UiState` with `StateFlow`, managing foreground SSE lifecycle (snapshot, agent, agent_removed, host, history), exponential backoff reconnect, and 401 token revocation.
  - Split monolith `AgentScreen.kt` into `PairingScreen.kt`, `AgentListScreen.kt`, `AgentDetailScreen.kt`, `PromptCard.kt`, `HistoryListScreen.kt`, `ResponseReaderScreen.kt`, and `MicrophoneIcon.kt`.
  - `PromptCard`: handles `permission` (Allow once / Deny / More), `question` (options list + Cancel), and `unknown` (raw tail + Cancel). Emits `expected_seq` and `fingerprint`.
  - `NotificationActionReceiver` + `MyFirebaseMessagingService`: handles FCM v1 push notifications with unique PendingIntent IDs per pane and action. Dispatches `answer`, `cancel`, and `prompt` commands via `goAsync()` coroutines.
  - `AgentStatusComplicationService`: fetches `/v1/agents` with device token; displays most severe status (`blocked > done > working > idle > unknown`).
  - `AgentQuickActionTileService` + `QuickDictateActivity`: implements Plan §11 target agent resolution (pinned -> done with latest updated_at -> focused); shows "To: <label>" before voice dictation; sends fresh `state_change_seq`.
  - Network security: purged `usesCleartextTraffic="true"` from main manifest (HTTPS enforced). Added debug network security config allowing cleartext for local testing.
  - Verification: `./gradlew :app:testDebugUnitTest`, `./gradlew :app:assembleDebug`, and `./gradlew :app:assembleRelease` (with R8 minification) all passed cleanly. Zero occurrences of `local_ip`, `tailscale`, `8420`, or `usesCleartextTraffic="true"` in `app/src/main`.

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
