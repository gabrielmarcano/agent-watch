# Agent Watch Roadmap

This document outlines the milestones achieved in the Herdr-native architecture and the planned future enhancements.

---

## Milestones Achieved (v0.2.0 — Release Gate)

### Core Architecture & Host Bridge
- **Herdr-Native Control Plane:** Built host daemon `agent-watch-bridge` in Go 1.22+, directly interfacing with the Herdr UNIX socket API (`events.subscribe`, `agent.list`, `agent.prompt`, `agent.send_keys`).
- **Zero Inbound Ports:** Switched from local listeners to an outbound TLS WebSocket client connecting to the cloud relay.
- **Strict Safety Verification:** Implemented pre-action verification (validates `pane_id`, `agent`, `expected_seq`, and `fingerprint` before sending keystrokes).
- **Herdr Plugin Integration:** Packaged bridge control (`start`, `stop`, `status`, `configure`, `pair`) as a standard Herdr plugin (`herdr-plugin.toml`).
- **Daemon Lifecycle:** Supervised by `launchd` on macOS and `systemd --user` on Linux.

### Cloud Relay & Security
- **Cloud Relay Server:** Built static Go binary `agent-watch-relay` supporting SSE broadcast, in-memory state aggregation, and SQLite history persistence.
- **Cryptographic Device Pairing:** 6-digit short-lived pairing flow storing salted SHA-256 device token hashes.
- **Bearer Token Auth:** Constant-time token verification on all protected endpoints with 401 rejection for unauthenticated requests and query-string tokens.
- **Push Dispatch:** Direct FCM HTTP v1 notifications for Wear OS and ntfy dispatch for watchOS.

### Agent Adapters (`pkg/agents`)
- **First-Class Agent Support:** Specialized adapters for Claude Code (`claude`), OpenCode (`opencode`), and Antigravity CLI (`agy`).
- **Dynamic Option Parsing:** Option roles (`allow_once`, `allow_always`, `deny`, `choice`) derived semantically from labels, never by rigid position.
- **Interactive Question Menus:** Full multiple-choice question dialog parsing with individual option chips on wrist.
- **Transcript History Readers:** Deep turn-based history extraction (Claude JSONL tail reader, OpenCode SQLite WAL reader, and screen capture fallback).

### Smartwatch Clients
- **Wear OS Application (Primary Client):**
  - Built with Jetpack Compose for Wear OS, featuring rotary crown inertia navigation.
  - Interactive approval prompts with Allow, Deny, and More Options dialogs.
  - Deep linking from push notifications directly into blocked agent prompts.
  - Voice dictation via Android speech recognizer to pinned agents.
  - Response reader with markdown formatting and history inspection.
  - Offline indicators ("Mac is offline", "herdr stopped") with automatic reconnection.
- **watchOS Application (Best Effort):**
  - Native SwiftUI application conforming to `/v1` REST + SSE contracts.

---

## Future Enhancements & Next Steps

### 1. UI & Ergonomic Refinement (Wear OS)
- **Design Overhaul:** Transition Wear OS UI from functional alpha state to an ergonomic, glanceable design following Wear OS Design Guidelines.
- **Compact Cards & Glanceable Badges:** Refine list spacing, typography hierarchy, and active agent badges for round screens.
- **Haptic Feedback:** Distinct vibration patterns for approvals, denials, and incoming questions.

### 2. Multi-Channel Notifiers
- **Telegram Bot Notifier:** Optional Telegram notifications with inline approval buttons as a fallback when watch is charging or out of reach.
- **Discord Webhook Alerts:** Configurable relay webhook to post agent milestone summaries to private channels.

### 3. Extended Agent Adapters
- **Codex & Pi Adapters:** Dedicated adapters for emerging CLI tools (OpenAI Codex, Pi, Amp) in `pkg/agents`.
- **PreToolUse Hook for Antigravity CLI:** Custom lifecycle hook to report `blocked` status dynamically to Herdr.

### 4. Phone Companion App (Optional)
- Companion phone app for easier initial pairing and Bluetooth BLE proxy tethering when Wi-Fi is unavailable on the watch.
