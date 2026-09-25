# Agent Watch Roadmap

This document outlines the milestones achieved in the Herdr-native architecture and the planned next steps. What is verified, and where, is tracked in [`docs/STATUS.md`](docs/STATUS.md).

---

## Milestones Achieved (v0.2.0)

### Core Architecture & Host Bridge
- **Herdr-Native Control Plane:** Host daemon `agent-watch-bridge` in Go 1.22+, talking directly to the Herdr UNIX socket API (`events.subscribe`, `agent.list`, `agent.read`, `agent.prompt`, `agent.send_keys`). Snapshot-authoritative sync with debounced re-lists, degraded polling and backoff when the event stream drops.
- **Zero Inbound Ports:** An outbound TLS WebSocket client to the cloud relay, with pings both ways.
- **Strict Safety Verification:** Before any key press the bridge re-lists the agent, re-reads the screen and checks `pane_id`, the agent, `expected_seq` and the prompt `fingerprint`, for `answer` and `cancel`. Commands act at most once; dictation is refused while a menu is on screen.
- **Herdr Plugin Integration:** Bridge control as a Herdr plugin (`herdr-plugin.toml`) with the actions `start`, `restart`, `stop`, `status` and `pair`; `configure` is a CLI command. Static, version-stamped plugin build.
- **Daemon Lifecycle:** Supervised by `launchd` on macOS and `systemd --user` on Linux; `start` keeps the installed service's values, `restart` never rewrites them. `status --json --local` for tools.
- **macOS Menu Bar App:** status, blocked count, start/stop/restart and pairing through the bridge CLI.

### Cloud Relay & Security
- **Cloud Relay Server:** Static Go binary `agent-watch-relay` with SSE broadcast, in-memory state aggregation, and history persisted in one atomically written JSON file (`store.json`).
- **Device Pairing:** 6-digit short-lived pairing codes; the relay stores only the SHA-256 hash of each 256-bit device token. Devices can be listed and revoked while the relay runs.
- **Bearer Token Auth:** Constant-time token verification on all protected endpoints, with 401 for unauthenticated requests; tokens in query strings are ignored.
- **Pairing rate limit** per client IP, taken from forwarding headers only when a trusted proxy (`AW_TRUSTED_PROXIES`) wrote them.
- **Push Dispatch:** FCM HTTP v1 data messages for Wear OS (debounce, digest, per-token delivery, careful dead-token rule) and ntfy for watchOS. An opt-in `resolved` push withdraws answered approvals.
- **Safe deploys:** `deploy.sh` refuses dirty trees, stamps the version, health-checks and rolls back.

### Agent Adapters (`pkg/agents`)
- **First-Class Agent Support:** Specialized adapters for Claude Code (`claude`), OpenCode (`opencode`), and Antigravity CLI (`agy`), with real captured fixtures.
- **Dynamic Option Parsing:** Option roles (`allow_once`, `allow_always`, `deny`, `choice`) derived from labels, never by position. Menus are detected only while their dialog is open.
- **Interactive Question Menus:** Multiple-choice question dialogs (Claude AskUserQuestion and plan approval, OpenCode's question tool) as option chips on the wrist.
- **Transcript History Readers:** Claude JSONL tail reader, Antigravity JSONL, OpenCode read-only SQLite, and a screen-capture fallback.

### Smartwatch Clients
- **Wear OS Application (Primary Client, alpha UI):**
  - Jetpack Compose for Wear OS with rotary input.
  - Approval prompts with Allow, Deny and More options, in the app and from notifications.
  - Deep links from push notifications into blocked agents.
  - Voice dictation to the pinned agent (tile and app).
  - History with a markdown reader.
  - Offline indicators ("Mac is offline", "herdr stopped") with automatic reconnection.
  - A reviewed, JVM-tested data layer: revoked-pairing state, stale-list detection, re-pairing restart, FCM registration only after the relay accepts it.
- **watchOS Application:** still the legacy pre-relay client; it is not on the `/v1` API yet (Phase 6).

---

## Next Steps

### 1. Wear OS UI Redesign (dedicated session)
- Move the UI from its functional alpha to an ergonomic, glanceable design following the Wear OS guidelines: compact cards, badges, typography for round screens, distinct haptics.
- Adopt the reviewed data layer: `UiState.auth` and `UiState.stale`, `RelayRepository.restart` after pairing, the prompt fingerprint on Cancel, no manual push registration, mapped errors in Quick Dictate (the list is in `docs/STATUS.md`, Phase 4).

### 2. Re-verify End to End (Phase 5)
- After the redesign, re-run the whole checklist of `docs/phases/5-e2e.md` on the Pixel Watch 2 with a release build, for claude, agy and opencode, including the new rows (duplicate taps, cancel after a menu change, live revoke, trusted-proxy rate limit, silent stream, menu bar).
- Replace `docs/e2e-report.md`; several rows of the first run were asserted from code.

### 3. Enable the `resolved` Push
- Once the new watch app is installed: make it ignore a `blocked` push older than the last `resolved` for its pane, set `AW_PUSH_RESOLVED=1` on the relay, and verify that answered approvals disappear from the watch.

### 4. Close the herdr Dialog-Status Gap
- herdr 0.9.1 reports Antigravity's permission dialog and Claude's WebFetch dialog as `done`/`working`, so the watch cannot answer them. Report it upstream, or decide how the bridge may publish a parsed menu against herdr's status (a custom PreToolUse hook for Antigravity is another option).

### 5. OpenCode Focus Detection
- Read which OpenCode button has the focus (a styled/ANSI screen read) instead of assuming `Allow once`.

### 6. watchOS Client (Phase 6)
- Rewrite the SwiftUI app against the `/v1` contracts, copying the validated Wear OS UX; verified in the simulator only.

### 7. Later Ideas
- **Multi-Channel Notifiers:** a Telegram bot with inline approval buttons as a fallback when the watch is charging; Discord webhook summaries.
- **Extended Agent Adapters:** dedicated adapters for Codex, Pi, Amp and other CLI agents.
- **Phone Companion App (optional):** easier pairing and Bluetooth tethering when the watch has no Wi-Fi.
