# Agent Watch Roadmap

This document outlines the milestones we've achieved so far and our vision for the future of Agent Watch.

## Milestones Achieved

### Backend & Infrastructure
* **Native Push Notifications:** Migrated entirely from third-party services (Pushover) to Firebase Cloud Messaging (FCM V1 API) for instant, secure, and direct watch notifications.
* **Robust Log Tracking:** Replaced flaky Unix `tail` pipes with a robust polling system (`fs.watchFile` & `fs.statSync`) in the Sidecar to parse JSON lines accurately without corruption.
* **Multi-Agent State Isolation:** Fixed cross-pollination bugs on the bridge server, ensuring that prompts and responses from different LLM sessions (e.g., AGY vs Claude) don't bleed into each other.
* **History Management:** Increased the bridge server capacity to buffer the 10 most recent interactions seamlessly.

### Wear OS App
* **Conversation History:** Implemented a dedicated "History" screen allowing users to browse their past interactions.
* **Native Rotary Physics:** Upgraded Wear Compose to `1.4.0` and implemented the `rotaryScrollable` modifier to bring buttery-smooth, native kinetic scrolling with inertia to the digital crown.
* **Obfuscation Fixes:** Implemented `@Keep` annotations on data models to ensure Gson serialization survives R8 release obfuscation on the smartwatch.
* **Polished UI/UX:** Built a dedicated full-screen `ResponseReaderScreen` with infinite scroll for deep-reading long AI outputs, along with clean thematic styling (LightBlue highlights, removed boilerplate branding).

---

## Future Plans & Ideas

### 1. Two-Way Communication (Voice to Terminal)
Currently, Agent Watch is read-only (observing the terminal). The ultimate goal is to allow the user to **tap the microphone on the watch**, dictate a prompt, and send it directly back to the terminal agent to execute, creating a seamless remote pair-programming loop.

### 2. Rich Markdown Rendering
The current `ResponseReaderScreen` shows raw text. We plan to integrate a Wear OS-compatible Markdown parser to properly render:
* **Bold and Italics**
* `Inline code`
* Formatted lists
* Structured tables (if screen size permits)

### 3. Agent Agnostic Integrations
Standardize the webhook payload format so that **any** CLI agent (not just Claude or AGY) can easily send updates to the watch with zero configuration.

### 4. Background Sync & Complications
* **Watch Face Complications:** Show the current status of the agent (e.g., "Idle", "Thinking", "Error") directly on the main watch face.
* **Offline queueing:** If the watch temporarily loses connection, queue the notifications on the bridge and sync them down silently in the background when it reconnects.
