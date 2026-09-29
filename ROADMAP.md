# Agent Watch Roadmap

This document outlines the milestones achieved in the Herdr-native architecture and the planned next steps. What is verified, and where, is tracked in [`docs/STATUS.md`](docs/STATUS.md).

---

## Milestones Achieved

### Core Architecture & Host Bridge
- **Herdr-Native Control Plane:** Host daemon `agent-watch-bridge` in Go 1.22+, talking directly to the Herdr UNIX socket API (`events.subscribe`, `agent.list`, `agent.read`, `agent.prompt`, `agent.send_keys`). Snapshot-authoritative sync with debounced re-lists, degraded polling and backoff when the event stream drops.
- **Zero Inbound Ports:** An outbound TLS WebSocket client to the cloud relay, with pings both ways.
- **Strict Safety Verification:** Before any key press the bridge re-lists the agent, re-reads the screen and checks `pane_id`, the agent, `expected_seq` and the prompt `fingerprint`, for `answer` and `cancel`. Commands act at most once; dictation is refused while a menu is on screen.
- **Herdr Plugin Integration:** Bridge control as a Herdr plugin (`herdr-plugin.toml`) with the actions `start`, `restart`, `stop`, `status` and `pair`; `configure` is a CLI command. Static, version-stamped plugin build.
- **Daemon Lifecycle:** Supervised by `launchd` on macOS and `systemd --user` on Linux; `start` keeps the installed service's values, `restart` never rewrites them. `status --json --local` for tools.
- **macOS Menu Bar App:** bridge and relay health as a status circle, versions, start/stop/restart and pairing through the bridge CLI.

### Cloud Relay & Security
- **Cloud Relay Server:** Static Go binary `agent-watch-relay` with SSE broadcast, in-memory state aggregation, and history persisted in one atomically written JSON file (`store.json`).
- **Device Pairing:** 6-digit short-lived pairing codes; the relay stores only the SHA-256 hash of each 256-bit device token. Devices can be listed and revoked while the relay runs.
- **Bearer Token Auth:** Constant-time token verification on all protected endpoints, with 401 for unauthenticated requests; tokens in query strings are ignored.
- **Pairing rate limit** per client IP, taken from forwarding headers only when a trusted proxy (`AW_TRUSTED_PROXIES`) wrote them.
- **Push Dispatch:** FCM HTTP v1 data messages for Wear OS (debounce, digest, per-token delivery, careful dead-token rule) and ntfy for watchOS. An opt-in `resolved` push withdraws answered approvals.
- **Safe deploys:** `deploy.sh` refuses dirty trees, stamps the version, health-checks and rolls back.
- **Versions and releases:** one `VERSIONS` file with a version per component, binaries that report their commit, CI on GitHub Actions and date-tagged GitHub releases (`vYYYY.MM.DD`).

### Agent Adapters (`pkg/agents`)
- **First-Class Agent Support:** Specialized adapters for Claude Code (`claude`), OpenCode (`opencode`), and Antigravity CLI (`agy`), with real captured fixtures.
- **Dynamic Option Parsing:** Option roles (`allow_once`, `allow_always`, `deny`, `choice`) derived from labels, never by position. Menus are detected only while their dialog is open.
- **Interactive Question Menus:** Multiple-choice question dialogs (Claude AskUserQuestion and plan approval, OpenCode's question tool) as option chips on the wrist.
- **Transcript History Readers:** Claude JSONL tail reader, Antigravity JSONL, OpenCode read-only SQLite, and a screen-capture fallback.

### Smartwatch Clients
- **Wear OS Application (Primary Client):**
  - Wear Compose Material 3 UI for round screens, with rotary input; agents ordered by how much they need you.
  - Approval prompts with Allow, Deny and More options, in the app and from notifications; a question's answers right in its notification.
  - Deep links from push notifications into blocked agents.
  - Voice dictation to the pinned agent, from the app and from the Quick Dictate tile.
  - An **Agents** tile with the two agents that need you most, and a complication with how many need you.
  - The agent's last reply and history with a markdown reader; terminal tables shown as one record per row.
  - Offline indicators ("Device offline", "Relay offline", "herdr stopped") with automatic reconnection.
  - A reviewed, JVM-tested data layer: revoked-pairing state, stale-list detection, re-pairing restart, FCM registration only after the relay accepts it.
- **watchOS Application:** still the legacy pre-relay client; it is not on the `/v1` API yet (Phase 6).

---

## Next Steps

### 1. Re-verify End to End (Phase 5)
- Re-run the whole checklist of `docs/phases/5-e2e.md` on the Pixel Watch 2 with a release build, for claude, agy and opencode, including the new rows (duplicate taps, cancel after a menu change, live revoke, trusted-proxy rate limit, silent stream, menu bar).
- Replace `docs/e2e-report.md`; several rows of the first run were asserted from code.

### 2. Enable the `resolved` Push
- The watch app already ignores a `blocked` push older than the last `resolved` for its pane. That app is on the watch since 2026-09-26: set `AW_PUSH_RESOLVED=1` on the relay and verify that answered approvals disappear from the watch.

### 3. Close the herdr Dialog-Status Gap Upstream
- herdr 0.9.1 misses Antigravity 1.2.x permission dialogs and Claude dialogs after a relaunch in the same pane. Mitigated with temporary local detection overrides (`tools/herdr-overrides/`); remove them once herdr fixes it (draft issues in `tools/herdr-overrides/UPSTREAM-ISSUES.md`).

### 4. Android Phone Client (Phase 7)
- A native phone app as a first-class relay client next to the watch, sharing the Wear OS data layer through a `:core` module; phone notifications stay local-only and approving from a locked phone requires unlock. Guide: `docs/phases/7-android-mobile.md`.

### 5. watchOS Client (Phase 6)
- Rewrite the SwiftUI app against the `/v1` contracts, copying the validated Wear OS UX; verified in the simulator only.

### 6. Later Ideas
- **Multi-Channel Notifiers:** a Telegram bot with inline approval buttons as a fallback when the watch is charging; Discord webhook summaries.
- **Extended Agent Adapters:** dedicated adapters for Codex, Pi, Amp and other CLI agents.
