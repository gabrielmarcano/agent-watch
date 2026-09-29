---
trigger: glob
glob: "wearos-app/**"
paths:
  - "wearos-app/**"
description: "Wear OS client rules (primary client, Pixel Watch 2)"
---

# Wear OS client rules (primary client, Pixel Watch 2)

> Applies to: wearos-app/**.

The app's data-layer rules, screens, notifications and UX decisions: `wearos-app/ARCHITECTURE.md`. Contracts: `docs/reference/contracts.md`. Every feature ships here first (`AGENTS.md` §1.5).

- **`google-services.json`:** never create or edit it; the owner places it.
- **Build:** `./gradlew :app:testDebugUnitTest :app:lintDebug :app:assembleDebug`, as CI runs it (the JDK: `docs/GUIDE.md` § Requirements). One Gradle build at a time on this Mac: check `pgrep -fl GradleDaemon` first, and run `./gradlew --stop` when idle. Device install: the `wearos-deploy` skill.
- **Device checks:** never approve, deny, cancel or dictate to the owner's real agents, from the watch or from an emulator paired with the production relay. Only `aw-sandbox` agents (the `capture-fixture` skill, steps 1–2).
