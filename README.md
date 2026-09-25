# Agent Watch

Agent Watch connects your wrist to your coding agents. When Claude Code, OpenCode, Antigravity, or other terminal agents need permission, ask a multiple-choice question, or finish a long task, Agent Watch alerts your smartwatch with glanceable context and actionable buttons (Allow, Deny, options, voice dictation). Your Mac stays closed or in your bag while you approve commands and monitor progress on the go.

<p align="center">
  <img src="docs/assets/watch-agents-list.png" width="200" alt="Agent List on Pixel Watch 2" />
  <img src="docs/assets/watch-blocked.png" width="200" alt="Blocked Approval on Pixel Watch 2" />
  <img src="docs/assets/watch-question.png" width="200" alt="Question Picker on Pixel Watch 2" />
</p>

---

## Architecture

Agent Watch is built around a thin outbound host bridge, a minimal cloud relay, and native wrist clients:

```
┌──────────────────────────────────────────────┐
│  Host (Mac / Linux)                          │
│                                              │
│  [Coding Agents: Claude, OpenCode, AGY, ...] │
│         │ (pty / lifecycle hooks)            │
│         ▼                                    │
│  [herdr socket API ($HERDR_SOCKET_PATH)]     │
│         │ (UNIX socket, JSON-RPC)            │
│         ▼                                    │
│  [agent-watch-bridge (launchd / systemd)]    │
└──────────────────────┬───────────────────────┘
                       │
                       │ Outbound TLS WebSocket (WSS)
                       │ (No incoming ports, No VPN)
                       ▼
┌──────────────────────────────────────────────┐
│  Cloud Relay (VPS behind Cloudflare)         │
│                                              │
│  [agent-watch-relay]                         │
│   ├── State Hub & In-Memory Cache            │
│   ├── Short-Lived Pairing & Device Auth      │
│   ├── SSE Broadcast (/v1/events)             │
│   ├── Push Dispatcher (FCM v1 / ntfy)        │
│   └── SQLite History Store                   │
└──────────────────────┬───────────────────────┘
                       │
                       │ HTTPS / SSE / FCM Push
                       ▼
┌──────────────────────────────────────────────┐
│  Smartwatch Clients                          │
│                                              │
│  • Wear OS (Google Pixel Watch 2 — Primary)  │
│  • watchOS (Apple Watch — Best Effort)       │
└──────────────────────────────────────────────┘
```

- **Outbound-Only Networking:** The Mac host dials outbound to the relay VPS. The watch never connects directly to your Mac, and no VPN is required on the watch.
- **Sole Source of Truth:** Herdr is the authoritative multiplexer and control plane. Agent Watch interfaces cleanly through Herdr's verified socket API.

---

## Requirements

1. **Herdr:** `herdr` ≥ 0.9.1 installed on your development machine.
2. **Cloud Relay:** A small Linux VPS (1 vCPU, 512 MB RAM) running behind Cloudflare with a public domain (e.g. `relay.example.com`).
3. **Smartwatch:**
   - **Wear OS** (Android 13+, Wear OS 4+ like Google Pixel Watch 2 or Galaxy Watch) — *Primary reference implementation*.
   - **watchOS** (watchOS 10+) — *Best-effort secondary client*.

---

## Setup Guide

Follow the setup steps in order:

### 1. Deploy the Cloud Relay
Deploy `agent-watch-relay` to your VPS behind Cloudflare:
- See the complete deploy guide: [`docs/phases/3c-relay-deploy.md`](./docs/phases/3c-relay-deploy.md).
- Create systemd service `/etc/systemd/system/agent-watch-relay.service`.
- Verify the relay is reachable:
  ```bash
  curl https://relay.example.com/v1/healthz
  # Expected: ok
  ```

### 2. Install and Configure the Host Bridge
On your development Mac:
1. Build the bridge binary:
   ```bash
   make bridge
   ```
2. Link the plugin into herdr:
   ```bash
   herdr plugin link .
   ```
3. Configure the relay URL and credentials:
   ```bash
   herdr plugin action invoke configure --plugin herdr-agent-watch
   ```
   Or edit `~/.config/herdr/plugins/config/herdr-agent-watch/config.toml` directly with `relay_url` and `host_token`.
4. Start the bridge daemon (supervised by launchd):
   ```bash
   herdr plugin action invoke start --plugin herdr-agent-watch
   ```
5. Verify bridge status:
   ```bash
   herdr plugin action invoke status --plugin herdr-agent-watch
   # Checks relay_connected: true, herdr_online: true
   ```

### 3. Pair Your Smartwatch
1. Generate a short-lived 6-digit pairing code from the bridge:
   ```bash
   herdr plugin action invoke pair --plugin herdr-agent-watch
   ```
2. Open the Agent Watch app on your watch:
   - On first launch, enter your relay domain (e.g. `relay.example.com`) and the 6-digit code.
   - The watch exchanges the code for a persistent device bearer token (the relay stores only its SHA-256 hash).
   - Once paired, your live agent workspaces appear automatically.

---

## Supported Agents

All agent-specific approval menu parsing and key mappings live in `pkg/agents`:

| Agent | Status | Approval UI | Key Mapping | Transcript History |
|---|---|---|---|---|
| **Claude Code** (`claude`) | First-class | Numbered menu, plan approval, question dialogs | Digit direct | Tail reader + Screen fallback |
| **OpenCode** (`opencode`) | First-class | Horizontal button bar (`Allow once`, `Allow always`, `Reject`) | Arrows + Enter sequence | SQLite (`opencode.db`) |
| **Antigravity CLI** (`agy`) | First-class | Numbered box menu (`Command`, `File edit`) | Digit direct | JSONL + Screen capture |
| **Generic** | Universal fallback | Any numbered list matching `^\d+[.)]` | Digit direct | Screen capture |

---

## Security Model

- **Outbound-only host:** The Mac opens an outbound TLS WebSocket (`wss://relay.<domain>/v1/host`). No inbound ports, listeners, or open firewall holes on the development Mac.
- **Hashed device tokens:** The relay stores only `sha256(device_token)`. If the relay database were compromised, existing tokens cannot be reconstructed.
- **Zero raw keys:** The watch client never sends raw key presses or terminal input. The watch sends high-level actions (`answer` with option ID, `cancel`, or `prompt` text). The host bridge maps actions to keys only after re-validating the prompt fingerprint.
- **Strict safety checks:** Before dispatching an answer, the bridge verifies:
  1. The pane has a detected agent.
  2. `expected_seq` matches the agent's current state.
  3. The prompt fingerprint matches what was parsed on the screen.
- **Sanitized logs:** The relay and bridge logs never print prompt contents, transcript text, or authentication tokens.

---

## Troubleshooting

- **Check status anytime:**
  ```bash
  herdr plugin action invoke status --plugin herdr-agent-watch
  ```
- **Log files:**
  - Bridge logs: `~/Library/Logs/agent-watch-bridge.log`
  - Relay logs: `journalctl -u agent-watch-relay -f` on the VPS
- **"Mac offline" banner:**
  - Displayed on the watch when the host bridge disconnects from the relay (e.g. laptop closed or sleeping).
  - Existing history remains browsable on the watch.
  - When the Mac wakes up, the bridge automatically reconnects and clears the banner without user intervention.
- **"herdr stopped" indicator:**
  - Indicates that the bridge is running but the herdr daemon is not responding on its UNIX socket. Start herdr to restore live monitoring.
