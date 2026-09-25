# watchOS Implementation Plan for Agent Watch

> ⚠️ **Legacy document.** This plan describes the old LAN/Tailscale client (direct SSE to the computer on port 8420, the pre-herdr state model). It no longer matches the architecture: the watchOS app is to be rewritten against the relay's `/v1` API in **Phase 6** ([`docs/phases/6-watchos.md`](../docs/phases/6-watchos.md)), copying the Wear OS UX. Until then `watchos-app/` is the legacy client; its only `/v1` code is the `CancelRequest` struct in `Models/AgentState.swift`.

This document outlines the step-by-step plan to implement a 1:1 watchOS (SwiftUI) client for Agent Watch, based on the existing Wear OS architecture.

## 1. Project Setup
- **Target**: watchOS application (SwiftUI lifecycle).
- **Architecture**: MVVM with `@Observable` (or `ObservableObject`).
- **Dependencies**:
  - Unofficial EventSource library for Swift (or `URLSession.bytes` for SSE).
  - A Markdown rendering package compatible with SwiftUI (e.g., `MarkdownUI` or similar) to replace Mikepenz's markdown library.

## 2. Data Models (Equivalents to Kotlin Data Classes)
Create Swift `struct`s conforming to `Codable`:
- `HistoryItem`: `id`, `query`, `response`, `timestamp`.
- `AgentState`: `status` (`idle`, `thinking`, `waiting_for_permission`, `done`), `session_id`, `cwd`, `last_query`, `last_response`, `history`, `tool_name`, `tool_input`, `timestamp`.

## 3. Network & State Management
- **`AgentNetworkService.swift`** (Equivalent to `SseClient.kt`):
  - Needs to handle Server-Sent Events (SSE) connecting to `http://<IP>:8420/events`.
  - Fallback logic: Try `localIp` first, switch to `tailscaleIp` on failure.
  - Implement upstream HTTP POST functions: `/input` (for dictation commands) and `/register` (for APNs tokens).
- **`AgentViewModel.swift`**:
  - Holds the central `@Published` (or `@Observable`) `AgentState`.
  - Parses incoming JSON from the network service and updates the UI state.
  - Handles UserDefaults for storing `local_ip` and `tailscale_ip`.

## 4. UI Layer (SwiftUI Views)
- **`ContentView.swift`** (Equivalent to `AgentScreen.kt`):
  - Main orchestrator. 
  - Status indicator with breathing animation (using SwiftUI `.animation`).
  - Action buttons: "ALLOW" / "DENY" (for `waiting_for_permission`).
  - Voice Dictation button.
  - Navigation links to other views.
- **`ConfigView.swift`** (Equivalent to `ServerConfigScreen.kt`):
  - Setup fields for Local Network IP and Tailscale Mesh IP.
  - Use watchOS native dictation to input IPs.
- **`HistoryListView.swift`** (Equivalent to `HistoryListScreen.kt`):
  - Use SwiftUI `List` or `ScrollView`.
  - Implement `.digitalCrownRotation` for smooth hardware scrolling.
- **`ReaderDetailView.swift`** (Equivalent to `ResponseReaderScreen.kt`):
  - Full-screen Markdown rendering view for long responses.

## 5. Utilities
- **`MarkdownFormatter.swift`**:
  - Port the regex logic from `MarkdownFormatter.kt` to clean raw markdown and truncate text for the history list previews.

## 6. Push Notifications (100% Free Alternative Strategy)
- **Constraint**: Native iOS/watchOS push notifications (APNs) strictly require a paid Apple Developer account ($99/year), meaning we cannot use Firebase Cloud Messaging (FCM) natively on this client for free.
- **Alternative Path**: Instead of native pushes directly to "Agent Watch", the Node Bridge server should be configured to emit notifications via a free third-party messaging service.
- **Recommendations**:
  - **ntfy.sh**: A free, open-source HTTP pub-sub service. The user installs the free `ntfy` app on their iPhone (which mirrors to watchOS) and subscribes to a topic. The Node bridge simply makes an HTTP POST to that topic.
  - **Telegram Bot**: The Node bridge uses the Telegram Bot API to send a direct message to the user upon task completion. The Telegram app naturally delivers the push notification to the Apple Watch.
- **Action**: This is documented but put **on hold** for now. We will rely on real-time SSE polling when the app is in the foreground.

## Next Steps
1. Initialize the Xcode project in this directory.
2. Implement the Data Models and Network Service (SSE).
3. Build the basic SwiftUI skeleton.
