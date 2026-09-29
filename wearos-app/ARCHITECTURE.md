# Agent Watch — Wear OS Client Architecture

The Wear OS app is the primary client of the relay's `/v1` API (`docs/reference/contracts.md` §1, §2, §4.1). It never talks to the computer directly and never sends raw keys. Build, install and device checks: `.agents/skills/wearos-deploy/SKILL.md`. Its state (what is verified, what is open): `docs/STATUS.md`.

## 1. Stack

- Kotlin 2.2, Jetpack Compose for Wear OS **Material 3** 1.6.2 (`androidx.wear.compose.material3`): `AppScaffold`, `ScreenScaffold` + `TransformingLazyColumn` (native rotary, the clock scrolls away), `EdgeButton`, confirmation and alert dialogs. 1.6 is the newest line on AGP 8; 1.7 needs AGP 9.1 and compileSdk 37.
- Two tiles on ProtoLayout Material 3 (`Material3TileService`); text entry through `androidx.wear:wear-input` (system keyboard, handwriting or voice).
- Toolchain: AGP 8.13.2, Gradle 8.14.3, compileSdk 36, targetSdk 34.
- OkHttp + `okhttp-sse`, Gson (models with `@Keep`, snake_case field names, for R8).
- Firebase Cloud Messaging (data-only messages).
- `minSdk 30` (Wear OS 3+), `targetSdk 34`, JDK 17. Verified only on a Google Pixel Watch 2.

## 2. Packages

| Package | What lives there |
|---|---|
| `model/` | `Contracts.kt`: mirrors `pkg/model` field by field; `severity()`, `resolveTargetAgent()` (pinned → latest `done` → focused → none) |
| `data/` | `Prefs` (`SharedPreferences`: relay URL, device token and id, FCM token, `fcm_registration`, pinned pane); `AgentStore` (merges SSE and refreshes); `HistoryMerge` (dedup by `id`, newest first, max 200); `FcmRegistration` (which FCM token the relay accepted for which pairing) |
| `network/` | `RelayClient` (stateless `/v1` client) and `RelayHttpClients`; `RelayEngine` (the relay link, no Android); `RelayRepository` (Android façade); `UiState`; `FcmRegistrar` + `PushRegistration`; `MyFirebaseMessagingService`, `AgentNotifications`, `NotificationSupport`, `NotificationActionReceiver`; `SurfaceUpdates` |
| `approval/` | `PromptActions` (primary buttons by role: ALLOW only ever maps to `allow_once`; the answered-prompt lock); `CommandFeedback` (relay error code → short wrist message) |
| `ui/theme/` | Fixed palette (no dynamic colour; every text pair ≥ 6.6:1) and `statusStyle()`: icon, word and colours per herdr status |
| `ui/logic/` | Pure presentation logic with JVM tests: `attentionSections` (list order), `headPreview`/`tailPreview`, `ageOf`, `listStatus` (the list's notice and dimming), relay URL checks |
| `ui/components/` | `ScreenList` (`ScreenScaffold` + `TransformingLazyColumn`), `transformedItem`, age text, voice and text input intents, `rememberPaneHistory` (the state's items merged with one `GET /v1/history?pane_id=`) |
| `ui/screens/` | Agent list, agent screen + `PromptSection`, dictation confirm (`DictationFlow`), full text, history, reader, pairing, settings |
| `complication/` | `complicationContent` (state → count and the agent a tap opens), `complicationBadge` (the short type: glyph and how many need the user, a problem's own glyph and a dash) and the data source: SHORT_TEXT, LONG_TEXT and MONOCHROMATIC_IMAGE |
| `tile/` | Quick Dictate (`tileContent`, `dictationTarget`, `QuickDictateActivity`); Agents (`agentsTile`: the two most urgent agents, each opening its screen, and how many more); `TileCommon` (palette, the 3 s fetch, launching into the app) |
| `util/` | `MarkdownFormatter` (plain previews; a table becomes one line per row, `first cell: other cells · …`), `MarkdownBlocks` (the reader's block parser; tables become one record per row, also inside a `screen` capture) |

## 3. Data flow

```
             GET /v1/agents, /v1/history          POST answer / cancel / prompt
 RelayClient ──────────────────────┐        ┌──────────────── (8 s call cap)
      ▲                            ▼        │
      │ SSE /v1/events      RelayEngine ── AgentStore + HistoryMerge
      │ (45 s silence =          │
      │  reconnect)              ▼
      └──────────────── MutableStateFlow<UiState> ──► RelayRepository.state ──► UI
                                 │
                   hooks ────────┼──► ApprovalNotifications (dismiss stale approvals)
                                 ├──► PushRegistration.ensure (on every stream open)
                                 └──► SurfaceUpdates (complication / tiles refresh, throttled)

 FCM ──► MyFirebaseMessagingService ──► blocked / done / digest notifications
                                   └──► resolved: dismiss the pane's approval
 Notification action ──► NotificationActionReceiver (goAsync, 9 s) ──► RelayClient
```

- **One engine per process.** `RelayRepository.init` builds it and makes it the process-wide 401 listener, so a 401 from any client (tile, complication, Quick Dictate, notification actions) revokes the pairing.
- **SSE lifecycle:** started in `MainActivity.onStart`, stopped in `onStop` (battery). Notifications cover the background.
- **Reconnect:** 1 s, 2 s, 4 s … 30 s; only a stream that delivered a `snapshot` resets the backoff. The SSE client's 45 s read timeout detects a silent (half-open) stream.
- **Merging:** a refresh never overrides a pane that SSE touched after the refresh started; within a pane the higher `state_change_seq` wins. History from both sources is merged by `id`.
- **Thread safety:** the engine guards its state with one lock; SSE callbacks arrive on OkHttp threads and callbacks from a replaced stream are ignored (stream generation). Hooks run outside the lock.

## 4. What the UI must use

```kotlin
RelayRepository.state: StateFlow<UiState>
data class UiState(connection, hostOnline, herdrOnline, agents, history, stale: Boolean, auth: AuthState)
enum class AuthState { PAIRED, UNPAIRED, REVOKED }

RelayRepository.start(context) / stop()          // foreground only
RelayRepository.restart(context)                 // after pairing or re-pairing
RelayRepository.refresh()
RelayRepository.answer(paneId, optionId, expectedSeq, fingerprint)
RelayRepository.cancel(paneId, expectedSeq, fingerprint)   // pass the shown prompt's fingerprint
RelayRepository.prompt(paneId, text, expectedSeq)
commandErrorFeedback(error, surface)             // the message to show for a failed command
```

- **Check `auth` first:** `UNPAIRED` or `REVOKED` → pairing screen, never the device-offline notice or an empty list.
- **`stale`:** the list is not backed by a live stream (before the first snapshot, reconnecting, stopped): dim it or say "Reconnecting…".
- **Commands:** use the `state_change_seq` and `fingerprint` of the `AgentState` shown at tap time; disable the buttons while in flight and, after a success, until the agent's state changes (`isAwaitingUpdate`); never auto-retry after a 409.
- **Pairing:** save the token, then `RelayRepository.restart(context)`. Do not call `registerPush` or write `fcmRegisteredToken` (ignored); `FcmRegistrar` registers when the stream opens and records it only after the relay accepts it.
- **How the UI uses it:** `MainActivity` observes `auth` at runtime (anything but `PAIRED` → pairing; `REVOKED` adds the `session_expired` line); the list shows `listStatus(state)` (while `stale`: the connecting or relay-offline notice, never the device-offline one) and dims the last known agents; each screen collects only its own slice of `state`; the agent screen sends the shown `state_change_seq` and fingerprint (cancel included) and locks the prompt until the agent moves; pairing saves the token and calls `restart`; every command error goes through `commandErrorFeedback`, including Quick Dictate.

## 4a. Screens

| Route | Screen |
|---|---|
| `pairing` | Relay URL (default `BuildConfig.DEFAULT_RELAY_URL`, edited with the system keyboard) and the 6-digit code |
| `agents` | Sections by attention (Needs you · Done · Working · Idle · Unknown) across workspaces; notice line; History and Settings |
| `agent/{paneId}` | Name first; the prompt as items (command head or `unknown` tail, View all, Deny · Allow, options with their description, a confirmation for `allow_always`); the last reply with Read all; Reply (dictation) as the edge button; "Pin to tile" |
| `dictation/{paneId}` | What the recognizer understood and the target, then Send |
| `history?paneId=`, `reader/{id}` | Cards with age; the answer rendered block by block (`screen` captures as monospace, except their tables, which the bridge sends as markdown and the reader shows as records) |
| `settings` | Pair again, unpair, version |
| `text` | A command or screen tail in full |

## 4b. UX rules

Other clients (watchOS, the phone app) copy these.

- **Target agent** for Quick Dictate and the tile: the pinned agent (set only with "Pin to tile"), else the latest `done`, else herdr's focused pane, else none. Never an arbitrary agent: a dictated prompt must not land in a pane the user did not choose (`resolveTargetAgent` in `model/Contracts.kt`). The tile passes the `pane_id` it showed, and a closed pane gets nothing (`tile/DictationTarget.kt`). The target label is always shown before sending.
- **Approvals:** Deny · Allow with the positive action on the right (Wear convention), full-height buttons 8 dp apart; every other option sits apart under More options, and `allow_always` asks for confirmation. An `unknown` prompt shows the screen tail, "Answer on the device" and Cancel only (`ui/screens/PromptSection.kt`).
- **Readability and feedback:** status is icon + word + colour, never truncated, colour never alone; no text under 12 sp, checked at font scale 1.24; feedback where the finger is (a confirmation dialog plus haptics, errors right above the buttons); no Back buttons (swipe to dismiss).

## 5. Notifications

- One notification per pane (`pane_id.hashCode()`); digests use a fixed id. Channels: `agent_blocked` (high), `agent_done` (default), `agent_watch_feedback` (low, silent action results).
- Every `PendingIntent` carries a data URI unique per pane and action (`agentwatch://notification/<action>/<pane>`), so extras from different panes can never be swapped.
- `resolved` pushes and live state (SSE/refresh) dismiss approvals that no longer match the agent. The relay sends `resolved` only with `AW_PUSH_RESOLVED` enabled.
- Buttons (`blockedButtons`): a question's answers (the push's `options`, up to 4), a permission's Allow and Deny, nothing for an `unknown` prompt, then Open. Cancel is never offered for a question. A finished agent's notification shows its reply and offers Reply (keyboard or voice → `prompt`).
- Action results: "Approved", "Denied", "Answered", "Canceled", "Sent" (auto-dismissed after 3 s), or an error message with an Open action.

## 6. Tests

`./gradlew :app:testDebugUnitTest` (JVM, no device): `RelayEngine*Test` against `FakeRelay` (auth, restart, silence, merging), `RelayClientTest`, `FcmRegistrarTest`, `AgentStoreTest`, `HistoryMergeTest`, `AgentNotificationsTest`, `SurfaceUpdatesTest`, the `approval/` tests, `ContractsTest` (parses `pkg/model/testdata/agent_state.json`), and the presentation tests: `ui/logic/*Test`, `MarkdownBlocksTest`, `DictationTargetTest`, `TileContentTest`, `AgentsTileContentTest`, `ComplicationContentTest`.
