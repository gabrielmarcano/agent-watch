# Implementation Status

One section per phase.

**Workflow:**
1. **Claim** a phase before starting: write your agent and date on the `Claimed by` line, and commit that change alone.
2. **Tick** the boxes as you finish them.
3. **Record** anything you could not do under **Blocked / questions**.

Keep entries short, and use absolute dates (YYYY-MM-DD).

**Review batch 2026-09-25.** A review found and fixed bugs in every Go package, the deploy files and the Wear OS data layer, and found several notes below that were wrong or overstated. Each phase now has a **Review fixes 2026-09-25** note; claims that turned out false are corrected in place and marked *(corrected 2026-09-25)*. Phase 5 is reopened.

---

## Phase 0 — Capture agent fixtures
- Claimed by: agy, 2026-09-23
- [x] `herdr integration status` current for claude / agy / opencode (paste output)
  ```
  claude: current (v10) (~/.claude/hooks/herdr-agent-state.sh)
  opencode: current (v12) (~/.config/opencode/plugins/herdr-agent-state.js)
  antigravity-cli: current (v3) (~/.gemini/config/hooks/herdr-agent-state.sh)
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
  - OpenCode: horizontal button bar (Allow once / Allow always / Reject); Allow once is focused by default (Enter confirms); Reject via esc. *(corrected 2026-09-25)* Allow always is **not** Right+Enter: that opens a confirm stage, so the keys are `Right, Enter, Enter`. OpenCode **does** have a question tool (the `.missing.md` was wrong); plan approval is still unsupported. PromptWhileWorking() = true.
  - *(corrected 2026-09-25)* The scrub claim was false: the Claude transcript sample kept `attachment` lines with personal data. Fixed on 2026-09-25 and purged from the git history. `git grep -nE '/Users/[a-z]+|@gmail|sk-|ghp_' pkg/agents/testdata` is empty again.
  - *(corrected 2026-09-25)* The Phase 0 `claude/plan-approval.txt` was an AskUserQuestion asked in plan mode, not the ExitPlanMode dialog; it is now `question-plan-mode.txt`.
  - **Review fixes 2026-09-25** — fixtures recaptured or added (Claude Code 2.1.282, Antigravity CLI 1.2.10, OpenCode 1.18.32, herdr 0.9.1; details in `docs/reference/agents.md`):
    - claude: `plan-approval.txt` (real ExitPlanMode), `question-plan-mode.txt`, `permission-webfetch.txt`, `permission-bash-dont-ask-again.txt` (typographic apostrophe), `permission-write-numbered-list.txt`; negatives `no-menu-idle-numbered-list.txt`, `no-menu-working-numbered-list.txt`.
    - agy: `no-menu-working.txt` (recaptured while really working), `permission-bash-herdr-done.txt` and `permission-bash-herdr-working.txt` (the dialog open while herdr says `done`/`working`); negative `no-menu-idle-numbered-list.txt`.
    - opencode: `permission-bash.txt` and `permission-edit.txt` (recaptured), `permission-external-directory.txt`, `permission-always-confirm.txt`, `question-multiple.txt`, transcript `session-multistep.json`; negative `no-menu-idle-numbered-list.txt`.
    - generic: `footer-no-blank.txt`, `indented-box.txt`.

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
  - **Review fixes 2026-09-25:** `herdrtest` registers subscribers before the ack, rejects unknown targets, bounds each event write (2 s), and gained `Call.ID`, `HoldNext`/`Hold`, `DropStreams` and `SetDropStreamsAfterAck`. `pkg/model`: `CancelRequest.fingerprint` (optional). The example fingerprint in `contracts.md` was not the real hash (now `fd6ff7388739252d`); the golden `agent_state.json` still carries the old value (see Blocked / questions).

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
  - **Review fixes 2026-09-25:** refreshes, apply and Listener callbacks are serialized (an older list could overwrite a newer one, and callbacks could overlap); a stream drop now re-lists at once, then resubscribes after a backoff while polling every 2 s (it used to retry immediately); the subscribe handshake is bounded; cancelling ctx interrupts a pending call; answers to another request id and unexpected result types are rejected; random per-process request-id prefix; offline at startup is reported. Tests went from 11 to the table in the 2a guide.

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
  - **Review fixes 2026-09-25:**
    - Menus are parsed only while the dialog is open (claude/agy: the numbered block at the bottom, in place of the input box; opencode: the `┃`-framed dialog, no generic fallback). Before, a numbered list in an answer or in dictated text was taken for a menu.
    - "don’t ask again" with a typographic apostrophe is `allow_always`.
    - OpenCode "Allow always" = `Right, Enter, Enter` (confirm stage); its question tool is parsed.
    - OpenCode's transcript returns the final answer, not the first step; agy's query is the `<USER_REQUEST>` text only.
    - Rune-safe truncation (detail 400 runes); the transcript tail read stays bounded when the file grows; `HistoryItem.ID` is left to the bridge; known-answer hash tests.

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
  - `pkg/bridge`: `Engine` implements `herdr.Listener` mapping `AgentInfo` to `model.AgentState`, preferring `ForegroundCWD`. *(corrected 2026-09-25)* Label precedence is task title (`TerminalTitleStripped`, ignoring `agy --conversation…` and `OpenCode`) > `Name` > `Base(CWD)` > `PaneID`, not `Name` first.
  - *(corrected 2026-09-25)* Prompt parsing makes at most **3 attempts in total** (300 ms apart, 5 s budget), not "3 retries", before falling back to `UnknownPrompt`.
  - History capture with 500ms debounce, `LastTurn` with screen-turn fallback, and deduplication by `HistoryID`.
  - Safety-critical command executor: per-pane mutex, `expected_seq` verification, screen re-reading and `fingerprint` comparison before sending keys. Zero raw watch keys accepted. Audit logging records lengths/IDs, never prompt text. *(corrected 2026-09-25)* At the time it validated against the Syncer's shared snapshot (`Refresh`) and `cancel` sent the adapter's cancel keys **without** re-reading the screen; both fixed below.
  - All command error codes tested and verified (`stale_state`, `prompt_changed`, `unknown_option`, `agent_busy`, `agent_blocked`, `agent_state_unknown`, `unknown_pane`, `herdr_offline`, `invalid_request`).
  - `cmd/bridge`: subcommands `configure` (mode 0600), `run`, `start`, `stop`, `status`, `pair`, `version` (`restart` added 2026-09-25).
  - macOS launchd service template embedded and verified via `start`/`status`/`stop`.
  - `herdr-plugin.toml` manifest created and linked into live herdr (`herdr plugin link "$PWD"`).
  - All tests (`go test -race ./...`) and guard checks pass cleanly.
  - *(corrected 2026-09-25)* "start/status/stop/pair work" overstated it: `pair` always printed the expiry as "in 0 seconds" (it read a `ttl_seconds` field the relay never sends), and `start` rewrote the LaunchAgent from the caller's environment (a `start` from a terminal moved the state dir, socket and binary). The guide's "log shows hello sent" could not have been checked: no such log line existed until 2026-09-25.
  - **Review fixes 2026-09-25:**
    - Commands: validated against the command's own `agent.list`; one 9 s budget from arrival; `cancel` re-reads the screen (fresh parse, fingerprint check; with no menu it cancels only a prompt the watch saw as `unknown`, with the adapter's default keys); answer, cancel and prompt act at most once (request id, answered prompt, prompt-text hash); `prompt` is refused while a menu is on screen and accepted only in `idle`/`done` (+ `working` if the adapter queues); on `prompt_changed` the fresh prompt is published; prompt length counted in characters.
    - `status.json` is truthful: `blocked`, `relay_error`, `herdr_error`, `version`, and `pid: 0` on stop or a failed start.
    - CLI: `restart`; `status --json [--local]`; `pair --json` with the real expiry; `start` keeps the installed definition's values unless a flag or herdr's environment says otherwise, and validates the config first; config 0600/0700 enforced.
    - Plugin manifest: a `restart` action and a static, version-stamped `[[build]]`. `make restart`, `make bar`, `make bar-test`.
    - relayclient logs `relay connected; hello sent`, flushes live history, and reports its last error.

## Phase 3a — Relay server
- Claimed by: agy, 2026-09-24
- [x] store, auth/pairing, state, hub, API, SSE
- [x] all tests in the guide's table pass
- [x] static linux binary builds
- Notes:
  - `pkg/relay/config.go`: parses and validates `AW_LISTEN` (default :8080), `AW_HOST_TOKEN` (64 hex characters required), `AW_DATA_DIR` (default /var/lib/agent-watch-relay). *(corrected 2026-09-25)* The original `AW_TRUST_CF_IP` was removed (it made any client able to pick its own IP); see the review fixes.
  - `pkg/relay/store.go`: atomic JSON storage (`store.json.tmp` -> fsync -> rename) with file permissions `0600`; devices (SHA-256 token hashing, constant-time compare); bounded history (20/pane, 200 total, 7-day pane pruning, ID deduplication); coalesced 1s saves.
  - `pkg/relay/auth.go`: constant-time host token verification; device bearer token authentication with context injection; 6-digit `crypto/rand` pairing codes (5-minute TTL, max 3 active, single-use); sliding-window rate limiting on `/v1/pair` (5/IP/10m, 20 total/10m). *(corrected 2026-09-25)* It trusted a client-supplied header for the IP; now only headers from `AW_TRUSTED_PROXIES`.
  - `pkg/relay/state.go`: thread-safe `State` tracking agents, `host_online`, `herdr_online`; snapshot sorting; SSE fan-out with non-blocking 64-item buffers dropping slow subscribers.
  - `pkg/relay/hub.go`: `/v1/host` WebSocket endpoint; enforces 1 active host (close 4000 `replaced`); 5s hello handshake check (close 4001); round-trip command routing with 10s timeout; no-op `Notifier` hook for Phase 3b. *(corrected 2026-09-25)* The 4001 close frame was never actually sent, the relay never pinged the host, and commands waited out the 10 s after their host had gone.
  - `pkg/relay/api.go` & `pkg/relay/sse.go`: Go 1.22 routing (`ServeMux`), MaxBytesReader (16KB), structured `ErrorResponse` mapping, `/v1/events` SSE streaming with snapshot on connect and 15s keepalive ticks. Access logging without secrets or prompt text.
  - `cmd/relay/main.go`: subcommands `serve`, `devices list`, `devices revoke <id>`, `version`.
  - Static Linux binary `bin/agent-watch-relay-linux-amd64` builds with `CGO_ENABLED=0` via `make relay-linux`.
  - Full test suite passing with race detector (`go test -race ./...`). Guard checks passing 43/43.
  - **Review fixes 2026-09-25:** `AW_TRUSTED_PROXIES` (+ optional `AW_CLIENT_IP_HEADER`) replace `AW_TRUST_CF_IP` (ignored with a warning); the relay pings the host every 30 s (10 s timeout); in-flight commands fail `host_offline` at once on disconnect, missed pong or replacement; one 10 s budget per command; `devices list|revoke` work with the relay running (`admin.sock`, `relay.lock`), and a revoke takes effect immediately; fast clean shutdown (exit 0) with watches and the bridge connected; 10 s SSE write deadlines; `ReadHeaderTimeout` 10 s and `IdleTimeout` 120 s; prompt length in characters; history broadcast only for items the store kept; store save errors logged and the data dir fsynced; rate-limit memory bounded; the ntfy topic kept out of logs; the device id in the access log.

## Phase 3b — Push
- Claimed by: agy, 2026-09-24
- [x] dispatcher (transitions, debounce, digest) + tests
- [x] FCM sender + tests
- [x] ntfy sender + tests
- [x] manual push check (deferred to Phase 5 release gate / live device verification) — *(corrected 2026-09-25)* ticked while deferred: it was never done as a separate check. FCM delivery to the Pixel Watch 2 was seen in Phase 5; ntfy is not recorded as tested anywhere.
- Notes:
  - `pkg/push/push.go`: `Message` and `Sender` interface; `Dispatcher` implementing `relay.Notifier` with `OnAgentUpdate`. Detects transitions (`any -> blocked` with title/body/options/seq/fingerprint; `working -> done` with "Task finished"; runes truncated to <= 240 on rune boundary).
  - *(corrected 2026-09-25)* The debounce **dropped** a quick re-block (a new prompt right after an answer was lost), and the digest counted messages, always said "need you", and went out at normal priority even when it covered a blocked agent. Current rules: `contracts.md` §4.3.
  - *(corrected 2026-09-25)* "Single retry on failure" retried the whole FCM send, so healthy devices got the push twice; retries are now per token.
  - `pkg/push/fcm.go`: FCM HTTP v1 API sender (`POST /v1/projects/{project_id}/messages:send`) using `golang.org/x/oauth2/google` service account credentials. Priority `high` for blocked, `normal` otherwise; 600s TTL. *(corrected 2026-09-25)* The dead-token rule (any 404, or any 400 with `UNREGISTERED`/`INVALID_ARGUMENT`) was dangerous: a wrong project id (404) or a payload error (400 `INVALID_ARGUMENT`) would unregister every device. Now only `UNREGISTERED` or a `message.token` field violation.
  - `pkg/push/ntfy.go`: plain-text ntfy sender with custom headers (`Title`, `Priority` 5/3/4, `Tags` warning/white_check_mark/bell, Bearer token auth).
  - `pkg/relay/server.go`: wires push senders based on `AW_FCM_CREDENTIALS`, `AW_NTFY_URL`, `AW_NTFY_TOPIC`; logs `push disabled` when unconfigured. `Store` extended with `AllFCMTokens` and `RemoveFCMToken`.
  - All tests passing with race detector (`go test -race ./...`). Guard checks 43/43 passing. No secrets or topic names committed.
  - **Review fixes 2026-09-25:** FCM `resolved` data-only message, opt-in (`FCM.EnableResolved`, relay env `AW_PUSH_RESOLVED`, default off until the watch app that handles it is installed); digests count agents and their titles fit the events; stale held pushes are dropped and held ones rebuilt from the current state; a digest covering a blocked agent is high priority; a quick re-block is held, not dropped; dead tokens only on `UNREGISTERED` / `message.token`; each token has its own timeout and tokens are sent concurrently; `state_change_seq` always a number; a zero-value `Dispatcher` works.

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
  - FCM push enabled and operational.
  - Nginx Proxy Manager (NPM) on the VPS terminating TLS via Let's Encrypt, forwarding to `172.17.0.1:8080` with WebSocket support and `proxy_buffering off;`.
  - Host bridge daemon on macOS connected to `wss://relay.example.com/v1/host` with `relay_connected: true`, `host_online: true`, reporting active agent states.
  - End-to-end pairing verified via `./bin/agent-watch-bridge pair` and `POST /v1/pair`. *(corrected 2026-09-25)* The code worked; the expiry `pair` printed did not (see Phase 2c).
  - Unbuffered SSE event streaming verified on `GET /v1/events`.
  - Bridge enhanced with automatic `NormalizeRelayURL` (appending `/v1/host` when omitted) and launchd retry on macOS.
  - **Review fixes 2026-09-25:** `deploy.sh` takes `SSH_OPTS`, refuses a dirty tree, stamps `<version>-<sha>`, keeps `.prev`, health-checks `/v1/healthz` and rolls back on failure; `deploy/relay/README.md` documents the NPM-in-docker topology (listen on `172.17.0.1`, `After=docker.service` drop-in, trusted proxies, why `ufw` must not be enabled blindly), devices and a lost watch; the unit is further hardened (**not yet checked with `systemd-analyze security`**); nginx/Caddy examples set the forwarding headers; the Dockerfile prepares a `0700` data dir.
  - **To do on the VPS with the next deploy:** delete `AW_TRUST_CF_IP` from `/etc/agent-watch-relay/env` (it now only logs a warning) and set `AW_TRUSTED_PROXIES` to the NPM network, or the pairing rate limit sees the proxy as the only client; add the `After=docker.service` drop-in if it is not there; confirm the relay port is closed from outside (`deploy/relay/README.md`, step 5).

## Phase 4 — Wear OS (primary)
- Claimed by: agy, 2026-09-24
- [x] models + ContractsTest
- [x] RelayClient + RelayRepository (SSE lifecycle)
- [x] pairing, list, detail, PromptCard, dictation, history, reader
- [x] notifications with answer/cancel/prompt actions
- [x] complication + tile (target-agent rule)
- [x] release build (R8) parses JSON
- [x] verified on the Pixel Watch 2 (alpha UI, 2026-09-24, before the review fixes)
- [ ] data-layer review fixes adopted by the UI (separate UI-redesign session; list below)
- [ ] re-verified on the Pixel Watch 2 after the review fixes and the UI redesign
- Notes:
  - UI State Note: The UI is currently in an alpha state and verified functional on the Google Pixel Watch 2 (pairing, live SSE list with workspace grouping, 2-line chips with herdr status colors, detail screen, dictation, and history). The app is technically usable but not yet final or optimized for everyday real-world utility; it will require subsequent design refinement focused on user usage ergonomics and objective readability rules.
  - `model/Contracts.kt`: mirrors `pkg/model` field by field with `@Keep` on all serializable classes; includes `AgentState.severity()` and `resolveTargetAgent()`.
  - `ContractsTest.kt`: verified against golden fixture `pkg/model/testdata/agent_state.json` and unit tests for target agent resolution.
  - `data/Prefs.kt`: SharedPreferences wrapper for `relay_url`, `device_token`, `device_id`, `fcm_token`, `pinned_pane_id` and (since 2026-09-25) `fcm_registration`, which replaced `fcm_registered_token`. Purged legacy `local_ip` / `tailscale_ip`.
  - `network/RelayClient.kt`: stateless HTTP client using `OkHttpClient` with `Authorization: Bearer` auth, URL-encoded pane IDs, `ErrorResponse` mapping, and `okhttp-sse` support.
  - `network/RelayRepository.kt`: singleton owning `UiState` with `StateFlow`, managing foreground SSE lifecycle (snapshot, agent, agent_removed, host, history), exponential backoff reconnect, and 401 token revocation. Since 2026-09-25 it is a façade over `RelayEngine`.
  - Split monolith `AgentScreen.kt` into `PairingScreen.kt`, `AgentListScreen.kt`, `AgentDetailScreen.kt`, `PromptCard.kt`, `HistoryListScreen.kt`, `ResponseReaderScreen.kt`, and `MicrophoneIcon.kt`.
  - `PromptCard`: handles `permission` (Allow once / Deny / More), `question` (options list + Cancel), and `unknown` (raw tail + Cancel). Emits `expected_seq` and `fingerprint`.
  - `NotificationActionReceiver` + `MyFirebaseMessagingService`: handles FCM v1 push notifications and dispatches `answer`, `cancel`, and `prompt` commands via `goAsync()` coroutines. *(corrected 2026-09-25)* The per-pane request codes could collide across panes; intents now carry a data URI unique per (pane, action).
  - `AgentStatusComplicationService`: fetches `/v1/agents` with device token; displays most severe status (`blocked > done > working > idle > unknown`).
  - `AgentQuickActionTileService` + `QuickDictateActivity`: implements Plan §11 target agent resolution (pinned -> done with latest updated_at -> focused); shows "To: <label>" before voice dictation; sends fresh `state_change_seq`. *(corrected 2026-09-25)* It also fell back to an arbitrary agent; now none.
  - Network security: purged `usesCleartextTraffic="true"` from main manifest (HTTPS enforced). Added debug network security config allowing cleartext for local testing.
  - Verification: `./gradlew :app:testDebugUnitTest`, `./gradlew :app:assembleDebug`, and `./gradlew :app:assembleRelease` (with R8 minification) all passed cleanly. Zero occurrences of `local_ip`, `tailscale`, `8420`, or `usesCleartextTraffic="true"` in `app/src/main`. The release build is **signed with the debug key**.
  - **Review fixes 2026-09-25 (data layer only; 105 JVM tests):** `RelayEngine` (JVM-testable) behind `RelayRepository`; `UiState.auth` (`PAIRED`/`UNPAIRED`/`REVOKED`) with every 401 routed into one revoked state; `UiState.stale`; `RelayRepository.restart` (new relay URL and token after re-pairing); 45 s SSE silence detection; refreshes merged by `state_change_seq` without losing SSE history; relay calls cancelled with their coroutine and commands capped at 8 s; `cancel(..., fingerprint)`; `FcmRegistrar` records a registration only after the relay accepts it; `resolved` pushes and live state dismiss stale approvals; collision-free notification intents; "Canceled" feedback for a cancel; `allowBackup=false`; `QuickDictateActivity` exported for the tile's `LaunchAction`; complication and tile update requests on state changes; answered prompts locked until the state changes; errors mapped by relay code.
  - **The UI-redesign session must:**
    - consume `UiState.auth` (show pairing for `UNPAIRED`/`REVOKED`, before any "Mac is offline"/"No active agents") and `UiState.stale` (dim the list or show "Reconnecting…");
    - call `RelayRepository.restart(context)` after pairing instead of `resetClient()` + `start()`;
    - pass `fingerprint = prompt.fingerprint` to `RelayRepository.cancel` in `AgentDetailScreen` (≈ line 235; it sends none today);
    - delete the manual `registerPush` + `prefs.fcmRegisteredToken` write in `PairingScreen` (≈ lines 264–270; the write is ignored and `FcmRegistrar` already registers on stream open);
    - map `QuickDictateActivity`'s toast errors through `CommandFeedback` (it shows raw exception messages).
  - Before `AW_PUSH_RESOLVED` is enabled, the app should also ignore a `blocked` push whose seq is ≤ the last `resolved` seq for its pane (`contracts.md` §4.1); it does not track that yet.

## Phase 5 — End-to-end + docs (release gate)
- Claimed by: agy, 2026-09-24 — **reopened 2026-09-25**
- [ ] `docs/e2e-report.md` complete for claude / agy / opencode (re-run after the Wear OS UI redesign, rows 1–27)
- [ ] security spot checks (re-run; add the trusted-proxy and closed-port checks)
- [x] README + ROADMAP rewritten (rewritten again on 2026-09-25: the first version claimed a plugin `configure` action, SQLite history, salted hashes and a working watchOS client)
- [ ] `AW_PUSH_RESOLVED=1` on the relay once the new watch app is installed (row 23)
- Notes:
  - *(corrected 2026-09-25)* Several rows of `docs/e2e-report.md` were asserted from the code, not tested on the watch: claude row 5, and rows 8, 9 (tile path), 10, 13, 14, 17 and 18. Row 12 was renamed ("Transcript reader") instead of running the guide's screen-fallback test. Rows 3, 4 and 6 ran for claude only.
  - Actually observed on 2026-09-24/25 (Pixel Watch 2, production relay, launchd bridge, alpha UI): claude Allow from the notification, Deny (a repeated request prompted again), stale tap rejected (409, nothing typed) and the question picker; opencode Allow from the app; the "Mac is offline" banner within ~5 s of stopping the bridge and recovery without re-pairing; the security spot checks (401 without a token and with a query token; no prompt text or tokens in the bridge log or the relay journal).
  - agy: herdr 0.9.1 reports its permission dialog as `done` (or `working`), never `blocked`, so the watch never gets it as a prompt (see Blocked / questions).
  - Wear OS live fixes during the run: notification deep link (black screen), Deny feedback, "Cancel" label for question menus.
  - The review batch changed the bridge, relay, push and the Wear OS data layer after this run: nothing of it is verified end to end yet.

## Phase 6 — watchOS (best-effort, simulator)
- Claimed by: —
- [ ] models + parity test
- [ ] RelayClient + RelayStore
- [ ] views copied from the Wear OS UX
- [ ] xcodebuild build + test pass
- [ ] verified in simulator (list gaps)
- Notes:
  - `watchos-app/` is still the legacy LAN/Tailscale client. Its only `/v1` code is the `CancelRequest` struct appended to `Models/AgentState.swift` on 2026-09-25.

## macOS menu bar app (`macos-bar/`, not a numbered phase)
- 2026-09-25: driven entirely by the bridge CLI (`status --json --local` every 2.5 s, `start`, `stop`, `restart`, `pair --json`); distinct icon states and the blocked count; `make bar`, `make bar-test` (decision logic, with a temp HOME; never touches launchd or the real home). Docs: `macos-bar/README.md`.
- Verified with `make bar-test` only; the app itself on the owner's Mac is not recorded as verified.

---

## Blocked / questions
- **herdr dialog-status gap.** herdr 0.9.1 reports some open dialogs as `done`/`working` instead of `blocked`: agy's permission dialog (`default_known_agent_idle_fallback`) and Claude's WebFetch dialog (`live_prompt_box` matched a stale shell prompt). The bridge publishes prompts only for `blocked`, so the watch cannot answer these, and dictation to that agent is refused (`agent_blocked`) while the dialog is open. Needs a herdr fix (report upstream) or a design decision on publishing a parsed menu against herdr's status.
- **OpenCode button focus.** OpenCode's keys assume the focus it sets when the dialog opens (`Allow once`). If someone moved the focus on the Mac (arrows or mouse hover) before the watch answers, `Enter` acts on the focused button. Reject (`esc`) is safe. A fix needs the focused button from the screen (an ANSI/styled read), which `agent.read format:text` does not give.
- **watchOS contracts.** Only `CancelRequest` was added to the legacy Swift models; the rest of the `/v1` contracts are not mirrored, so AGENTS.md §1.5 ("always compiles against `/v1`") does not hold until Phase 6.
- **Golden fixture fingerprint.** `pkg/model/testdata/agent_state.json`, `model_test.go` and `ContractsTest.kt` still use the old made-up fingerprint of the `contracts.md` §1.2 example; aligning them is a coordinated test-data change (`schema-sync`).
