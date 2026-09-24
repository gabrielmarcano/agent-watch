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
- [ ] `herdr integration status` current for claude / agy / opencode (paste output)
- [ ] claude fixtures + golden files
- [ ] agy fixtures + golden files
- [ ] opencode fixtures + golden files
- [ ] transcript samples (scrubbed) + expected results
- [ ] no 🔍 left in `docs/reference/agents.md`
- [ ] sandbox removed
- Notes:

## Phase 1 — Foundation
- Claimed by: —
- [ ] legacy removed (`bridge/`, `claude-plugin/`, `.claude-plugin/`, `agent_integrations_analysis.md`)
- [ ] `go.mod` (`github.com/gabrielmarcano/agent-monitor`, go 1.22) + Makefile
- [ ] `pkg/model` + tests (golden `testdata/agent_state.json`)
- [ ] `pkg/herdrtest` + tests
- [ ] `make check` and `make build` pass
- Notes:

## Phase 2a — herdr client
- Claimed by: —
- [ ] client, subscribe, syncer
- [ ] all tests in the guide's table pass
- [ ] read-only smoke test against real herdr (paste 3–5 lines)
- Notes:

## Phase 2b — Agent adapters
- Claimed by: —
- [ ] generic parser + unit tests
- [ ] claude adapter (fixtures + transcript)
- [ ] agy adapter (fixtures + transcript)
- [ ] opencode adapter (fixtures + SQLite)
- [ ] CGO-free build confirmed
- Notes:

## Phase 2c — Bridge daemon
- Claimed by: —
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
