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
- [ ] relayclient + tests
- [ ] engine + command executor + tests (every error code)
- [ ] cmd/bridge subcommands
- [ ] launchd install/uninstall verified on the Mac
- [ ] herdr plugin linked; start/status/stop/pair work
- [ ] sandbox blocked → parsed → answered via stub relay
- Notes:

## Phase 3a — Relay server
- Claimed by: —
- [ ] store, auth/pairing, state, hub, API, SSE
- [ ] all tests in the guide's table pass
- [ ] static linux binary builds
- Notes:

## Phase 3b — Push
- Claimed by: —
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
