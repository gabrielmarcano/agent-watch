# Phase 7 — Android Phone Client

> **Goal:** a native Android phone app that is a **first-class client of the relay**, next to the watch: live agent list, approvals from parsed options (in the app and from notifications), typed and voice prompts, history with the markdown reader, pairing and device settings.
>
> It is **not** a watch companion. The watch keeps talking to the relay itself; when it has no Wi-Fi, Wear OS routes its traffic through the phone's Bluetooth connection without any app of ours.

| | |
|---|---|
| **Depends on** | Phase 5 (the Wear OS UX and data layer validated end to end) |
| **Parallel with** | Phase 6 (disjoint directories), **except step 1**, which restructures the Android build: nothing else may touch the Android tree while it runs |
| **Touches** | The Android Gradle build (restructured in step 1), path references in `tools/guards/`, `.agents/skills/*`, `.agents/rules/*`, `.github/workflows/*`, `Makefile`, `.gitignore` and docs; `VERSIONS` (the phone app's version); `docs/reference/contracts.md` + all contract copies only if step 4 adds a field (`schema-sync`); `docs/STATUS.md` |
| **Must not** | Create a second copy of the Kotlin contracts; send raw keys; weaken the bridge's validation; change the watch app's application id or signing key; commit Firebase files or personal values |

---

## Read first

- `AGENTS.md`, `.agents/rules/wearos.md` (the Kotlin conventions carry over).
- `docs/reference/contracts.md` §1, §2 (API, error codes), §4 (push payloads, `resolved`).
- `wearos-app/ARCHITECTURE.md`: the data layer this phase shares, and §4b, the UX rules (target agent, approvals) a phone copies.

---

## Why no backend change is needed

The relay does not care what kind of client it serves. Any app that pairs (`POST /v1/pair` with a code from `agent-watch-bridge pair`) gets its **own device token**, revocable on its own, and uses the same `/v1` API:
- the snapshot plus the SSE stream;
- commands carrying `expected_seq` + `fingerprint`, which the bridge re-validates before pressing anything;
- FCM push, registered with `POST /v1/push/register`.

---

## Decisions (confirm with the owner at kickoff)

| Decision | Proposal | Why |
|---|---|---|
| Code sharing | Gradle multi-module: `:core` (Android library: model, network, data, approval and notification logic), `:wear` (today's app), `:mobile` | The data layer and its JVM tests are reused, and the contracts keep one Kotlin copy |
| Layout | Rename the Gradle root `wearos-app/` → `android/`, with `core/`, `wear/`, `mobile/` | A phone module inside `wearos-app/` would mislead. The rename is one mechanical commit |
| Phone application id | Its own id (e.g. the watch's id + `.mobile`), in the **same Firebase project** | Keeps the apps independent. The alternative (same id for both) is tied to the bridging decision below |
| UI | Compose Material 3 for phones | Reuse the watch's information architecture, not its layouts |
| Notifications with two apps | Phone notifications local-only (next section) | Avoids duplicates on the wrist without relay changes |
| Lock screen | Approving actions require device unlock | A phone is easier to pick up than a watch on a wrist |

### Notifications with two apps (the main pitfall)

- Android **bridges** a phone app's notifications to the paired Wear OS watch. The watch app also gets its own FCM push, so every approval would appear **twice on the wrist**.
- Options:
  1. **Phone notifications with `setLocalOnly(true)`:** never bridged; phone and watch each show their own. Simplest, no relay change.
  2. **Same application id for both apps**, with bridging disabled or deduplicated on the watch side (`BridgingManager`, bridge tags / dismissal ids). The standard companion setup, but it ties the two apps' identities and signing.
  3. **Per-device push routing in the relay** (e.g. a device `kind`, and "push the phone only when the watch is offline"). A contract and relay change (`schema-sync`), for later.
- Recommendation: **option 1** now; revisit 3 if both devices ringing is annoying.
- `resolved` withdraws answered approvals on both devices through the shared `:core` logic.

### Security specific to a phone

- **Approving notification actions** (Allow, Allow always, answers to questions) use `setAuthenticationRequired(true)` (API 31+), so a locked phone cannot approve. Decide with the owner whether Deny/Cancel also require unlock.
- **Device token at rest** in Keystore-backed storage; `allowBackup="false"` plus `dataExtractionRules` excluding it (covers device-to-device transfer, which `allowBackup` alone does not).
- Never log tokens or prompt text (same rules as the watch).

### Shared configuration

- `:mobile` reads `agent-watch.env` like `:wear`: `BuildConfig.DEFAULT_RELAY_URL`, and an application id key if the owner wants it configurable.
- After the rename, the Gradle files read `../agent-watch.env` from their new location: adjust the path.

---

## Steps

### 1. Restructure into modules (alone)
- Move to `android/{core,wear}`. `:core` gets `model/`, `network/`, `data/`, `approval/`, the pure notification logic, and their tests. Wear-only code (UI, tiles, complications, Wear notification builders) stays in `:wear`.
- `network/` is not Wear-free today; split these out first:
  - `SurfaceUpdates.kt` imports the tile and complication services, and `RelayRepository` calls it (e.g. a callback interface that `:core` defines and `:wear` implements);
  - `MyFirebaseMessagingService`, `NotificationActionReceiver`, `NotificationSupport` and `AgentNotifications` build and handle Wear notifications: keep their pure logic in `:core` and the Android parts in `:wear`.
- Update every path reference:
  - `tools/guards/guards.py` `CONTRACT_PEERS` (the Kotlin model path) and `tools/guards/test_guards.sh`, then run `bash tools/guards/test_guards.sh`;
  - `.github/workflows/wearos.yml` (paths filter, working directory, the placeholder `google-services.json` it writes) and `release.yml`'s Wear OS line;
  - `.agents/rules/wearos.md` and `.agents/rules/contracts.md`;
  - the `schema-sync` and `wearos-deploy` skills, `AGENTS.md` layout, docs, `Makefile`, `.gitignore` (`google-services.json`, `local.properties`);
  - the Gradle reader of `agent-watch.env`.
- Gate:
  - the same JVM test count as before the move, all green (193 on 2026-09-26);
  - `:wear:assembleRelease`;
  - installed on the Pixel Watch 2: pairing and push still work (same application id and signing key).
- **Commit this step alone**, before any phone code.

### 2. `:mobile` skeleton
- Application id; the Android app added to the same Firebase project (`google-services.json` per module, never committed).
- Its own version pair in `VERSIONS` (e.g. `MOBILE_VERSION_NAME` / `MOBILE_VERSION_CODE`, read by Gradle like the watch's), and a CI job and release-notes line for `:mobile`.
- `BuildConfig.DEFAULT_RELAY_URL`; pairing (editable URL, code entry; a QR from `agent-watch-bridge pair` is a nice later addition).
- `RelayRepository` from `:core`; the SSE stream bound to the foreground, like the watch.

### 3. Screens
- **Agent list:** the watch's information architecture (`wearos-app/ARCHITECTURE.md` §4a): sections by attention (Needs you · Done · Working · Idle · Unknown) across workspaces, the workspace as secondary text.
- **Detail with the prompt card:** permission / question / unknown; roles from labels; ALLOW never `allow_always`; buttons locked after answering (`:core` logic).
- **Prompt input:** keyboard and voice.
- **History** and the markdown reader.
- **Settings:** relay, unpair, notification behaviour.

### 4. Notifications and push
- Channels (approvals high importance, done low), actions with `RemoteInput` for answers, `setAuthenticationRequired(true)` on approving actions, local-only notifications (or the option chosen), `resolved` handling from `:core`.
- If option 3 is chosen: a contract change through `schema-sync` (all four copies), plus relay and tests.

### 5. Verify on the owner's phone (Pixel 8 Pro), with the watch paired too
- Pair; the list is live; approve and deny a **sandbox** agent from the app and from a notification, locked and unlocked; a prompt reaches the agent; history opens.
- `agent-watch-relay devices revoke <id>` cuts the phone at once.
- **No duplicate notification on the watch.**

---

## Tests

- JVM tests for every new pure function (list ordering, the action authentication policy, notification ids…).
- `:core` tests pass for both apps: `./gradlew :core:test :wear:testDebugUnitTest :mobile:testDebugUnitTest :wear:assembleRelease :mobile:assembleRelease`.

---

## Definition of done

- [ ] Step 1 merged on its own; the Wear OS app behaves the same on the watch; guards, skills and docs use the new paths.
- [ ] `:mobile` release build installed on the owner's phone.
- [ ] Every step-5 check passes with the watch paired: no duplicate notification on the wrist; a locked phone cannot approve.
- [ ] No personal values in tracked files (relay domain, Firebase ids): `git grep` before committing.
- [ ] `docs/STATUS.md` updated per its workflow, and this guide deleted; `docs/GUIDE.md` and `AGENTS.md` §2 describe the phone client and the new layout.

---

## Pitfalls

- **A second copy of the contracts.** `:core` is the only Kotlin copy.
- **Changing the watch app's application id or signing key in step 1** loses its pairing and push registration.
- **Bridged notifications** and **lock-screen approvals** (see Decisions).
- **One Gradle build at a time** on the Mac (`.agents/rules/wearos.md`).
- **Real agents:** only sandbox agents (`aw-sandbox`) are approved or prompted during development.

---

## Prompt for the executing agent

```
You are executing Phase 7 (Android phone client) of Agent Watch in this repository.
Read AGENTS.md, docs/phases/7-android-mobile.md, wearos-app/ARCHITECTURE.md and docs/reference/contracts.md (§1, §2, §4).
First confirm the "Decisions" table with the owner. Then do step 1 (module restructure) alone and commit it before any
phone code: keep the Wear OS app's application id, signing key and behaviour unchanged and its tests green. Build
:mobile, verify on the owner's phone with sandbox agents only (never approve or prompt real agents), tick Phase 7 in
docs/STATUS.md, and commit only your own paths.
```
