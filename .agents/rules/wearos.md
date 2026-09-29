---
trigger: glob
glob: "wearos-app/**"
description: "Wear OS client rules (primary client, Pixel Watch 2)"
---

# Wear OS client rules (primary client, Pixel Watch 2)

> Applies to: wearos-app/**.

The app, its data-layer rules and its UX decisions: `wearos-app/ARCHITECTURE.md`. Contracts: `docs/reference/contracts.md`.

- **Reference implementation** (`AGENTS.md` §1.5): every feature ships here first.
- **Models mirror `pkg/model` field by field**, with snake_case Gson names, `@Keep` on every class, and nullable where Go uses `omitempty`. `ContractsTest` parses `pkg/model/testdata/agent_state.json`. If it fails, the app is out of date.
- **Network:**
  - HTTPS only, to the relay's `/v1` API; no cleartext in release builds;
  - pane ids URL-encoded in paths.
- **Commands:**
  - never send raw keys;
  - approvals are `answer {option_id, expected_seq, fingerprint}` or `cancel {expected_seq, fingerprint}` (send the shown prompt's fingerprint whenever a prompt is shown; it is optional only for old clients), using the values from the `AgentState` shown at tap time;
  - never auto-retry an answer after a 409.
- **Notifications:** every `PendingIntent` needs an identity unique per pane **and** action, or extras from different agents overwrite each other. Request codes alone are not enough (they can collide across panes): put a data URI per (pane, action) on the intent (`AgentNotifications.intentUri`).
- **Battery:** SSE runs only while the app is in the foreground (process lifecycle). Complications and tiles make one `GET /v1/agents` and do no heavy parsing.
- **UI:** Wear Compose Material 3 (`androidx.wear.compose.material3`), `ScreenScaffold` + `TransformingLazyColumn` (native rotary scrolling), list → agent screen navigation. Dictation is confirmed with the text and the target label before sending.
- **`google-services.json`:** never create or edit it; the owner places it.
- **Build:** `./gradlew :app:testDebugUnitTest :app:assembleDebug` (JDK 17). One Gradle build at a time on this Mac: check `pgrep -fl GradleDaemon` first, and run `./gradlew --stop` when idle. Device install: `.agents/skills/wearos-deploy/SKILL.md`.
- **Device checks:** never approve, deny, cancel or dictate to the owner's real agents, from the watch or from an emulator paired with the production relay; only `aw-sandbox` agents (`wearos-deploy` skill).
