# Agent Guidelines & Repository Rules (`AGENTS.md`)

This document defines the development rules, architectural boundaries, and coding standards for all AI agents (Claude Code, Antigravity, OpenCode, Codex, Cursor, Copilot, etc.) contributing to this repository. The full design lives in [`HERDR_REFACTOR_PLAN.md`](./HERDR_REFACTOR_PLAN.md); when the two disagree, the plan wins and this file must be updated.

---

## 0. Where to Start

1. **Pick a task:** [`docs/README.md`](docs/README.md) is the index of phase guides. [`docs/STATUS.md`](docs/STATUS.md) says what is done, claimed and blocked. Claim a phase there before starting.
2. **Look up facts,** never from memory:
   - [`docs/reference/contracts.md`](docs/reference/contracts.md): every JSON shape
   - [`docs/reference/herdr-socket-api.md`](docs/reference/herdr-socket-api.md): the herdr socket, verified
   - [`docs/reference/agents.md`](docs/reference/agents.md): per-agent menus, keys, transcripts
   - [`docs/guides/agent-cli-audit.md`](docs/guides/agent-cli-audit.md): CLI verification runbook when TUIs or integrations change
3. **Rules** live in `.agents/rules/*.md`:
   - **Antigravity CLI** loads them by their `trigger` / `glob` frontmatter.
   - **OpenCode** loads all of them through `opencode.json` → `instructions`.
   - **Any other agent:** read the ones matching the files you touch.
4. **Skills** in `.agents/skills/*/SKILL.md`, discovered by both Antigravity and OpenCode:
   - `capture-fixture`
   - `add-agent-adapter`
   - `schema-sync`
   - `wearos-deploy`
   - `relay-deploy`
   - `herdr-probe`
5. **Guards:** one implementation in `tools/guards/guards.py`, wired in three places:
   - `.agents/hooks.json` for Antigravity CLI;
   - `.opencode/plugins/agent-watch-guards.js` for OpenCode;
   - `.githooks/pre-commit` for every tool and humans.

   They block:
   - input to herdr panes outside the `aw-sandbox` workspace;
   - owner-only herdr commands;
   - `git add -A`, `--amend`, `--no-verify` and unapproved `git push`;
   - writes to legacy or secret paths;
   - commits with secrets, unformatted Go, or `pkg/model` changes without the client and contract updates.

   If a guard blocks you, fix the cause; never work around it. Setup and tests: `tools/guards/README.md`.

> ⚠️ **The herdr on the development Mac runs the owner's real agent sessions.** Only panes you create in the `aw-sandbox` workspace may receive prompts or keys.

---

## 1. Prime Directives & Architectural Rules

### 1.1 Herdr-Native Control Plane (Zero Legacy Policy)
- **Strictly prohibited on `feat/herdr-focus`:**
  - Warp-specific code
  - AppleScript keystrokes (`osascript`)
  - the Claude Code plugin and hook webhooks (`claude-plugin/`, `/webhook`)
  - legacy single-agent endpoints (`/state`, `/input`)
  - the Node.js bridge
  - file-tailing sidecars (`agy-sidecar.js`)
- Herdr (the socket API at `$HERDR_SOCKET_PATH`) is the **sole source of truth** for agent detection, lifecycle status, and input/key delivery. Talk to the socket directly; do not shell out to the `herdr` CLI from Go.
- Do **NOT** add backward-compatibility shims, dual-write adapters, or fallback modes for non-Herdr environments. An agent running outside Herdr is out of scope.
- Reading an agent's **own transcript** for history is allowed, under three conditions:
  - it happens inside a `pkg/agents` adapter
  - it is triggered on demand by a herdr status transition
  - the file comes from herdr's `agent_session`

  Scanning directories for the "latest file" and continuous tailing are not allowed.

### 1.2 Multi-Agent, Multi-Provider by Design
- Priority agents: **Claude (`claude`), Antigravity (`agy`), OpenCode (`opencode`)**. Every other agent herdr detects must work through the **generic adapter**.
- **All agent-specific knowledge lives in `pkg/agents`** and nowhere else:
  - menu layout
  - key mapping
  - cancel key
  - transcript format
  - whether a prompt can be queued while working

  `pkg/herdr`, the bridge core, the relay and the clients stay agent-agnostic.
- **Never map prompt options by position.**
  - Roles (`allow_once`, `allow_always`, `deny`, `choice`) are derived from the option **label**.
  - Example of why: in Claude Code, `2` means "Yes, and don't ask again".
- **Every adapter change ships with a real captured fixture** in `pkg/agents/testdata/<agent>/`, taken with `herdr agent read <pane> --source visible --format text` (`--format ansi` for focus fixtures such as `pkg/agents/testdata/opencode/focus/`).

### 1.3 Go for Host Bridge & Cloud Relay
- Backend is **Go 1.22+**, a single module: `github.com/gabrielmarcano/agent-monitor`.
- `agent-watch-bridge`: static binary on the Mac/Linux host. Keep it thin: herdr ↔ relay translation plus on-demand transcript reads.
  - The herdr plugin (`herdr-plugin.toml`) is only the manifest that installs and controls this binary. Its actions (`start`, `restart`, `stop`, `status`, `pair`) are one-shot. `configure` is a CLI subcommand only.
  - The long-running process is `agent-watch-bridge run`, supervised by launchd (macOS) or systemd `--user` (Linux). Never rely on herdr to keep it alive.
- `agent-watch-relay`: static Linux binary on the VPS. It owns state aggregation, history storage, push, pairing and auth.
- `pkg/model` is the **only** schema source. Clients mirror it field by field.

### 1.4 Outbound Relay Networking (Zero VPN on Watch)
- Watches must **NEVER** require Tailscale, a VPN, or a LAN IP.
- The bridge dials **outbound** to `wss://relay.<domain>/v1/host`. Watches use HTTPS + SSE on the relay. No inbound ports on the Mac.

### 1.5 Clients: Wear OS First, watchOS Best-Effort
- **Wear OS is the primary client and reference implementation.** Features ship there first and are verified on a real Google Pixel Watch 2.
- **watchOS is extra support**, verified only in the Xcode simulator. It never blocks a phase or a release.
- **Schema/API changes must still update both clients' models**, so `watchos-app/` always compiles against the current `/v1` API. watchOS UI features may lag behind Wear OS.
  - Until Phase 6, `watchos-app/` is still the legacy LAN client: its models are not the `/v1` contracts yet (only `CancelRequest` was appended to them). Phase 6 rewrites it.
- **Never claim a watchOS feature works on a real device.** Say "verified in the simulator".

### 1.6 Scope Boundaries
- **No terminal emulator / SSH client** (Moshi covers that).
- **Wrist-first:**
  - glanceable status
  - approvals built from parsed options
  - voice dictation
  - history
  - complications/tiles
- **Android phone client** is planned as Phase 7: a first-class relay client, not a watch companion (Wear OS already reaches the relay through the phone's Bluetooth connection).
- **watchOS push** uses ntfy (free). No APNs until a paid Apple Developer account exists.

---

## 2. Directory Layout

```
agent-monitor/
├── go.mod · go.sum · Makefile    # make config / configure-bridge / deploy-relay / watchos-config read agent-watch.env
├── agent-watch.env.example       # the one deployment config file, documented; the real agent-watch.env is git-ignored (secret)
├── herdr-plugin.toml             # herdr-agent-watch plugin manifest
├── cmd/
│   ├── bridge/                   # host daemon + CLI (configure/run/start/restart/stop/status/pair)
│   └── relay/                    # VPS relay (serve/devices)
├── pkg/
│   ├── model/                    # shared contracts: state, API DTOs, wire envelopes
│   ├── herdr/                    # socket client — agent-agnostic
│   ├── herdrtest/                # fake herdr socket for tests
│   ├── agents/                   # adapters: claude, agy, opencode, generic (+ testdata/)
│   ├── relayclient/              # bridge side of the WSS link
│   ├── relay/                    # relay server: hub, api, sse, auth, store
│   └── push/                     # Notifier: FCM v1 (Wear OS), ntfy (watchOS)
├── deploy/
│   ├── launchd/                  # LaunchAgent template
│   └── relay/                    # systemd unit, env/proxy examples, deploy.sh, Dockerfile, README.md (operations)
├── macos-bar/                    # macOS menu bar app over the bridge CLI (make bar, make bar-test)
├── tools/config/                 # awenv.sh: agent-watch.env reader for the Makefile and deploy.sh (+ test_awenv.sh)
├── tools/guards/                 # guard implementation (hooks, pre-commit)
├── tools/herdr-overrides/        # TEMPORARY herdr detection overrides (agy, claude); remove when upstream fixes them
├── wearos-app/                   # Wear OS client (Kotlin, Jetpack Compose)
└── watchos-app/                  # watchOS client (Swift, SwiftUI; legacy until Phase 6)
```

---

## 3. Security & Secrets Hygiene

- **NEVER commit credentials:**
  - `firebase-service-account.json`
  - host tokens, device tokens, pairing codes
  - ntfy topics/tokens
  - Cloudflare tokens, SSH keys
  - `.env` files and the bridge `config.toml`
  - `agent-watch.env` (the host token and relay secrets; only `agent-watch.env.example` is committed)
- **Never read, print or write `agent-watch.env`.** The owner creates it with `make config`; the guards refuse writes and commits. Tools take secrets from it directly (`configure --env-file`, `deploy.sh --sync-env`), never through argv, logs or output.
- All traffic across public networks uses TLS (HTTPS / WSS).
- **Tokens travel in `Authorization` headers**, never in query strings (they end up in proxy logs).
- The relay stores only **hashes** of device tokens. Pairing codes are short-lived and rate-limited.
- **The watch never sends raw keys.** It sends `prompt`, `answer {option_id}` or `cancel`, and the bridge resolves the keys.
- **The bridge re-validates every command right before acting:**
  - `expected_seq` must match
  - the prompt `fingerprint` must match
  - the pane must have a detected agent
- Commands are rejected for shell panes and for panes without an agent.

---

## 4. Coding Conventions

### Backend (Go)
- Idiomatic Go: clear package boundaries, explicit error handling, **no panics in daemons**.
- Propagate `context.Context` for cancellation and timeouts.
- Herdr RPC is **one request per connection**. Only `events.subscribe` stays open. Status events require a per-pane subscription.
- **Snapshot is authoritative; events only trigger a debounced re-list.**
- Reconnect with exponential backoff + jitter for both the herdr socket and the relay WebSocket. Ping the WebSocket every 30 s.
- Use herdr's status enum **verbatim**: `idle / working / blocked / done / unknown`.
- Key agents by **`pane_id`** (URL-encode it; it contains `:`), never by session id.
- Transcript readers must be bounded (tail reads) and must fall back to screen capture on any failure.
- Tests run against `pkg/herdrtest`, never against a live herdr session.

### Wear OS (Kotlin / Compose)
- Jetpack Compose for Wear OS (`androidx.wear.compose.material`); keep native rotary scroll (`rotaryScrollable`).
- `@Keep` on serializable models (R8).
- Complications and tiles stay lightweight: they read the aggregated snapshot, with no heavy parsing.

### watchOS (Swift / SwiftUI)
- Swift 5.9+, SwiftUI, `NavigationStack`.
- Models conform to `Codable`, `Identifiable`, `Sendable`.
- Reconnect SSE with `AsyncSequence`.

---

## 5. Verification Checklist for Agents

Before claiming a task is done:
1. **No legacy references remain:** Warp, `osascript`, `agy-sidecar.js`, `claude-plugin`, `/webhook`, or the Node bridge.
2. **Go builds and tests pass:** `go vet ./... && go test ./...`.
3. **Adapter changes are covered** by a fixture test in `pkg/agents/testdata/`.
4. **Both clients match `pkg/model`:** `wearos-app` and `watchos-app` models mirror the current schema. Wear OS behavior is verified on the Pixel Watch 2; watchOS only in the simulator, and reported as such.
5. **Input rules hold:**
   - inputs to `blocked` agents go through `answer` / `cancel` → `agent.send_keys`, never `agent.prompt`
   - options are resolved by role, never by position
6. **Nothing is left behind:** no uncommitted scratch files or secrets.
7. **Guards still pass:** if you changed anything under `tools/guards/`, `.agents/hooks.json`, `.opencode/` or `.githooks/`, run `bash tools/guards/test_guards.sh`.
8. **Status is up to date:** your phase is ticked in `docs/STATUS.md`, and only your own paths were committed.
