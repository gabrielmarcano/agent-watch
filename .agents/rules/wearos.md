---
trigger: glob
glob: "wearos-app/**"
description: "Wear OS client rules (primary client, Pixel Watch 2)"
---

# Wear OS client rules (primary client, Pixel Watch 2)

> Applies to: wearos-app/**.

Guide: `docs/phases/4-wearos.md`. Contracts: `docs/reference/contracts.md`.

- **Reference implementation.** Every feature ships here first and is verified on the owner's Google Pixel Watch 2. Say "verified on device" only if the owner confirmed it on the watch.
- **Models mirror `pkg/model` field by field**, with snake_case Gson names, `@Keep` on every class, and nullable where Go uses `omitempty`. `ContractsTest` parses `pkg/model/testdata/agent_state.json`. If it fails, the app is out of date.
- **Network:**
  - HTTPS only, to the relay's `/v1` API;
  - `Authorization: Bearer` header, never tokens in URLs;
  - pane ids URL-encoded in paths;
  - no LAN/Tailscale IPs, no port 8420, no cleartext in release builds.
- **Commands:**
  - never send raw keys;
  - approvals are `answer {option_id, expected_seq, fingerprint}` or `cancel {expected_seq, fingerprint}` (send the shown prompt's fingerprint whenever a prompt is shown; it is optional only for old clients), using the values from the `AgentState` shown at tap time;
  - never auto-retry an answer after a 409.
- **Notifications:** every `PendingIntent` needs an identity unique per pane **and** action, or extras from different agents overwrite each other. Request codes alone are not enough (they can collide across panes): put a data URI per (pane, action) on the intent (`AgentNotifications.intentUri`).
- **Battery:** SSE runs only while the app is in the foreground (process lifecycle). Complications and tiles make one `GET /v1/agents` and do no heavy parsing.
- **UI:** Wear Compose Material, `ScalingLazyColumn` with `rotaryScrollable`, list → detail navigation. The dictation target label is shown before sending.
- **Secrets:** never create, edit or commit `google-services.json`. The owner places it.
- **Build:** `./gradlew :app:testDebugUnitTest :app:assembleDebug` (JDK 17). Device install: `.agents/skills/wearos-deploy/SKILL.md`.
