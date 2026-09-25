# Phase 4b — Wear OS UI Redesign

> **Goal:** turn the alpha Wear OS UI into one that is comfortable for everyday use on the owner's **Google Pixel Watch 2**: readable at a glance, safe to approve from, and consistent. The data layer is already fixed (Phase 4 review, 2026-09-25); this phase adopts it in the UI.

| | |
|---|---|
| **Depends on** | Phase 4 (data-layer review fixes merged) |
| **Parallel with** | The shared-config task (repo-level `agent-watch.env`), which adds `BuildConfig.DEFAULT_RELAY_URL`. It touches `wearos-app/app/build.gradle.kts` build config fields only; don't remove them |
| **Touches** | `wearos-app/app/src/main/java/com/gabriel/agentwatch/{ui,tile,complication}/**`, navigation in `MainActivity.kt`, `wearos-app/app/src/main/res/**`, dependencies in `wearos-app/app/build.gradle.kts`, UI tests, `wearos-app/ARCHITECTURE.md`, `docs/STATUS.md` (Phase 4/4b lines). Since the M3 decision (below): plugin versions in `wearos-app/build.gradle.kts`, the Gradle wrapper, and `approval/CommandFeedback` wording |
| **Must not** | Change `model/`, `network/`, `data/` behaviour (report needs instead), send raw keys, hard-code any relay domain |

---

## Read first

- `AGENTS.md` (§1.2, §1.5, §1.6 wrist-first, §3, §4 Wear OS), `.agents/rules/wearos.md`.
- `docs/phases/4-wearos.md` and `wearos-app/ARCHITECTURE.md` (current data layer: `RelayEngine`/`RelayRepository`, `UiState`, `AgentStore`, `FcmRegistrar`, notifications).
- `docs/reference/contracts.md` §1 (what exists to display), §2.4 (error codes), §4 (push payloads).
- `HERDR_REFACTOR_PLAN.md` §11 (target agent for dictation/tile) and §13.1 (approval UX).
- `docs/STATUS.md` Phase 4: the list **"The UI-redesign session must"**.

## Data-layer API the UI must adopt

| API | What the UI does |
|---|---|
| `UiState.auth` (`PAIRED`/`UNPAIRED`/`REVOKED`) | Check first. Anything but `PAIRED` → pairing; `REVOKED` says "Session expired — pair again". Never show "Mac is offline"/"No active agents" in that state. Observe it at runtime (MainActivity only picks the start destination once) |
| `UiState.stale` | Dim the list or show "Reconnecting…"; the list stays populated |
| `RelayRepository.restart(context)` | Call after pairing / re-pairing |
| `RelayRepository.cancel(pane, seq, fingerprint = prompt.fingerprint)` | Pass the fingerprint (AgentDetailScreen ≈ line 235 sends none today) |
| `PairingScreen` ≈ lines 264–270 | Delete the manual `registerPush` + `fcmRegisteredToken` write (ignored; `FcmRegistrar` registers on stream open) |
| Default relay URL | `BuildConfig.DEFAULT_RELAY_URL` from the shared config (may be empty). Remove the hard-coded owner domain. The URL must be editable without voice-only input |
| `CommandFeedback` | Use it everywhere, including `QuickDictateActivity` toasts (raw exception text today) |
| `prompt_changed` focus refusal (OpenCode) | Show the relay `message` ("answer it on the Mac") instead of "Prompt changed — refreshed", and don't invite a retry |
| Tile `LaunchAction` | `setPackageName(context.packageName)`, never the literal `com.gabriel.agentwatch`, so a build with another `applicationId` works |

## Findings of the 2026-09-25 UI review (evidence-based, on the Pixel Watch 2)

| Area | Problem |
|---|---|
| Readability | 47 of 57 explicit text sizes < 12 sp (min 8 sp). Status word truncated in list chips ("claude · D…"); with a `name` only a 7 dp colour dot remains |
| List | On open only 1 of 10 agents is visible; `groupBy` keeps first-appearance order so a blocked agent in a second workspace sits under idle ones; `TimeText` overlaps content (no `scrollAway`); offline banner 9.5 sp; HISTORY and SETTINGS look identical; SETTINGS only re-pairs |
| Detail | Item 1 is centred, so the agent name sits under the clock; metadata 8.5–9 sp; opening a detail silently re-pins the tile target |
| PromptCard | Command detail cut at 6 lines with no ellipsis or "view all"; `unknown` shows the **first** 6 of 12 `raw_tail` lines (the question is at the end); Allow/Deny ~72×52 dp with 4 dp gap, Allow on the left (Wear puts positive on the right), no icons, DENY 3.60:1 contrast; "MORE OPTIONS" 2 dp from the buttons, not collapsible; allow_once/always same green; question options 10 sp, `maxLines=2`, label and description glued ("Verde El color verde"); "Type something." free-text option can't be completed from the watch; `Card(onClick={})` announces a useless button |
| History | Label `Row` has no `weight` → ~55 dp empty gap per card; no time shown (`completed_at` unused); text clipped by the bezel |
| Reader | Up to 16 KB in a single item, clipped by the bezel; `screen` history shows raw TUI (bridge-side, out of scope — report) |
| Pairing | URL only by voice; default is the owner's domain; PAIR 2.54:1 contrast; unreachable error path; no unpair |
| Dictation / tile | Sent without confirmation; tile and activity resolve the target separately (tile may say "To: A" and send to B — pass the `pane_id` in the `LaunchAction`); results as raw Toasts; tile is a raw layout (ProtoLayout Material unused), emoji icon, no state, no freshness |
| Complication | Ignores `done`; `ic_dialog_info` for everything; "—" for unpaired/offline/error alike; SHORT_TEXT only |
| Notifications | question/unknown's only quick action is Cancel (destructive); `done` body always "Task finished"; no `BigTextStyle`; no grouping |
| Cross-cutting | Custom `onRotaryScrollEvent` competes with `ScalingLazyColumn`'s built-in rotary (delete it); no haptics or `Confirmation`; no `strings.xml`; no `contentDescription` (mic is an unlabelled Canvas); Wear Compose **M2** 1.4 + phone `compose.material` for markdown; `MainActivity` recomposes everything on each SSE event |

The reviewer's backlog (impact ÷ effort): error mapping in red ✅ (done in Phase 4 review) · ALLOW never `allow_always` ✅ · status first in list chips · `raw_tail` last lines + "view all" · lock buttons after answering ✅ (logic done; keep it) · cancel notifications on resolve ✅ (logic done) · history row/time/per-pane · remove custom rotary + `scrollAway` · type floor (nothing < 12 sp, body ≥ 14 sp) · rebuild list (attention section) / detail (anchor on the name) / split PromptCard into items · complication + tile (ProtoLayout Material, state, `pane_id`) · dictation confirmation · 401 → pairing, offline reason, dim stale list · **Wear Compose Material 3 migration**.

**Decide early** (propose to the owner with screenshots): migrate to Wear Compose Material 3 now (`ScreenScaffold`, `EdgeButton`, `ConfirmationDialog`) instead of polishing M2 and migrating later.

## Design decisions (owner-approved 2026-09-25)

Taken after an emulator audit of every screen (round 384 px / 192 dp AVD, fake host on a local relay). The audit confirmed every finding above.

| # | Decision | Notes |
|---|---|---|
| 1 | **Wear Compose Material 3 now** | `compose-material3` **1.6.2**: the newest release that builds with AGP 8 (compileSdk 35, AGP ≥ 8.6). 1.7.0 needs AGP 9.1 and compileSdk 37: a separate toolchain step later. Toolchain: Gradle 8.14.3, AGP 8.13.2, Kotlin 2.2.21 + Compose compiler plugin, compileSdk 36, targetSdk stays 34. Tiles on `protolayout-material3` 1.4.2 |
| 2 | **Own markdown renderer, by blocks** | Each paragraph, heading, list or code block is one list item, so long answers never sit in one bezel-clipped item. Drops `multiplatform-markdown-renderer-m2` and phone `compose.material` |
| 3 | **Dictation is confirmed before sending** | Speak → screen with the text and the target → Send. Same flow from the detail screen and the tile |
| 4 | **Flat list ordered by attention** | Sections "Needs you · Done · Working · Idle · Unknown"; the workspace is the agent's secondary text |
| 5 | **Fixed palette** | Status colours carry meaning; no dynamic colour from the watch face |

**Principles:** status before name (icon + word + colour, never truncated, colour never alone) · the first blocked agent visible on open · the prompt anchored under the clock, "View all" past 6 lines · Deny | Allow with the positive on the right, ≥ 52 dp tall, ≥ 8 dp apart, extra options styled apart and `allow_always` confirmed · feedback where the finger is (`ConfirmationDialog` + haptics, errors next to the buttons) · nothing < 12 sp, body ≥ 14 sp, checked at font scale 1.24 · AA contrast for all text · honest states (`auth` first, `stale` dims, "Mac offline" only for `host_online=false`) · no Back buttons · a real Settings screen.

**Order:** toolchain + M3 theme → navigation, `auth`/`stale`, pure logic with JVM tests → list → agent + prompt → history + reader → pairing + settings → dictation, tile, complication → notifications → final screenshots → watch check with the owner. Sequential: one Gradle at a time on this Mac, and every step shares the theme and `MainActivity`.

**Reported, outside 4b:** option label and description arrive glued (adapter or a `PromptOption.description` field) · Claude's "Type something." free-text option can't be completed from the watch (needs its own role or omission in `pkg/agents`) · `screen` history is raw TUI (bridge) · `done` push body is always "Task finished" (relay) · the focus refusal is recognised by its `message` text; a dedicated error code would be sturdier.

## Verification environment

- **Emulator first.** Install `system-images;android-34;android-wear;arm64-v8a` with `sdkmanager` (SDK at `/opt/homebrew/share/android-commandlinetools`) and create a **new** round AVD (e.g. `aw-wear-large-round`). Do not touch existing AVDs (another project owns them).
- **Pairing the emulator** with the relay: `./bin/agent-watch-bridge pair` only prints a short-lived code (no service change). The emulator becomes a registered device: tell the owner its name at the end so it can be revoked.
- **The emulator shows the owner's real agents.** Never approve, deny, cancel or dictate to them. To exercise prompts, use agents you start in a disposable herdr workspace named `aw-sandbox` (cwd `/tmp/aw-sandbox`; `.agents/skills/capture-fixture/SKILL.md` rules), and close it when done.
- **The real watch** only for the final check, and **only after asking the owner** in this pane (he does not want to test on the watch until the new UI is ready). `adb install -r` keeps pairing. On the watch never tap approval buttons of real agents, never swipe on the watch face (it dismisses notifications), and remember opening a detail re-pins the tile target.
- **Screenshots** go to your scratchpad; anything committed to `docs/assets/` must show only sandbox agents (no real project names).
- **One Gradle build at a time on this Mac** (a parallel task may need Gradle). Check `pgrep -fl GradleDaemon` before building and stop daemons when idle (`./gradlew --stop`).

## Method

- Logic (sorting, target resolution, error text, formatting) as pure Kotlin with JVM tests, test first.
- Visuals: screenshot each screen and state (loading, empty, offline, stale, revoked, blocked permission/question/unknown, long command, long labels, large font scale) before and after; show the owner at each milestone and ask for feedback.
- Measure: sp sizes, dp targets (≥ 48 dp), contrast ratios (WCAG AA 4.5:1 text, 3:1 large/graphics).

## Definition of Done

- [ ] Every item of the STATUS "UI-redesign session must" list is done.
- [ ] Every review finding above is fixed or explicitly deferred in STATUS with a reason.
- [ ] `./gradlew :app:testDebugUnitTest :app:assembleDebug :app:assembleRelease` green.
- [ ] Emulator screenshots of every screen and state, reviewed with the owner.
- [ ] Real-watch check with the owner, then STATUS Phase 4 items ticked and `wearos-app/ARCHITECTURE.md` updated.
- [ ] Emulator device name handed to the owner for revocation; sandbox closed.

## Git

The main working tree is shared with other sessions: commit only your own files with explicit paths (never `git add -A`), never `--amend`, never `--no-verify`, never push unless the owner asks. Commit after each verified step.
