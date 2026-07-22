# Agent Monitor (Wear OS)

Agent Monitor is a sleek, Material Design-inspired Wear OS application that lets you monitor and interact with AI development agents (like Google Antigravity and Claude Code) directly from your smartwatch.

## Architecture

This monorepo is divided into three components:

1. **`wearos-app/` (The Watch App)**: A native Kotlin/Jetpack Compose application for Wear OS. It features a modern "Material Blue" glassmorphic UI, real-time status syncing, and voice-to-text dictation.
2. **`bridge/` (The Node.js Server)**: A local server running on your Mac. It communicates with the watch via Server-Sent Events (SSE) and handles dictation inputs by dynamically typing them into your terminal (Warp, iTerm, etc.) via AppleScript.
3. **`claude-plugin/` & `agy-sidecar.js` (The Agent Integrations)**:
   - **Claude Code**: Native warp hooks that send state webhooks to the bridge.
   - **Antigravity (AGY)**: A Node.js sidecar script (`agy-sidecar.js`) that tails AGY's local transcripts in real-time and forwards states to the bridge. (Auto-launched by `server.js`).

## Features

- **Real-time Status Monitoring**: Know instantly if your agent is "Thinking", "Waiting for input", or "Asking for permission".
- **Voice Dictation**: Reply to the agent directly from your watch! The bridge translates your voice to text and types it directly into your Mac's active terminal.
- **Unified Support**: Seamlessly supports both Google Antigravity and Claude Code simultaneously.

## Quick Start

### 1. Configure the Bridge
In `bridge/`, create a `config.json` to define your terminal preferences and notification keys (optional):

```json
{
  "target_terminal": "Warp"
}
```

**Native Push Notifications (FCM):**
To enable true background push notifications directly to the watch (even when the app is closed), you must download your **Firebase Admin SDK service account key** from the Firebase Console and save it as `bridge/firebase-service-account.json`. (This is entirely optional; the local SSE connection will still handle real-time UI sync whenever the app is open).

### 2. Run the Bridge
```bash
node bridge/server.js
```
*Note: This automatically launches the AGY Sidecar as a background process.*

### 3. Install on Watch
Open the project in Android Studio or run from the root:
```bash
cd wearos-app && ./gradlew installRelease
```
