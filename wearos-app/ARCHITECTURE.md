# Agent Watch — Wear OS Client Architecture

The Wear OS app is the primary client of the relay's `/v1` API (`docs/reference/contracts.md` §1, §2, §4.1). It never talks to the computer directly and never sends raw keys. Build, install and device checks: `docs/phases/4-wearos.md` and `.agents/skills/wearos-deploy/SKILL.md`.

> **State on 2026-09-25:** the data layer below was reworked and is covered by 105 JVM tests. **The UI (`ui/screens/`) is the first alpha and is being redesigned in a separate session**; that session must adopt the data-layer API listed in §4. Nothing from the rework is re-verified on the watch yet.

## 1. Stack

- Kotlin, Jetpack Compose for Wear OS (`androidx.wear.compose`), rotary input.
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
| `ui/screens/`, `ui/theme/` | Alpha UI: pairing, agent list, agent detail with `PromptCard`, history, markdown reader |
| `complication/`, `tile/` | Status complication; Quick Dictate tile + `QuickDictateActivity` |
| `util/` | `MarkdownFormatter` (plain previews) |

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
                                 └──► SurfaceUpdates (complication / tile refresh, throttled)

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

- **Check `auth` first:** `UNPAIRED` or `REVOKED` → pairing screen, never "Mac is offline" or an empty list.
- **`stale`:** the list is not backed by a live stream (before the first snapshot, reconnecting, stopped): dim it or say "Reconnecting…".
- **Commands:** use the `state_change_seq` and `fingerprint` of the `AgentState` shown at tap time; disable the buttons while in flight and, after a success, until the agent's state changes (`isAwaitingUpdate`); never auto-retry after a 409.
- **Pairing:** save the token, then `RelayRepository.restart(context)`. Do not call `registerPush` or write `fcmRegisteredToken` (ignored); `FcmRegistrar` registers when the stream opens and records it only after the relay accepts it.
- **Known gaps in the alpha UI** (tracked in `docs/STATUS.md`, Phase 4): it ignores `auth` and `stale`, pairs with `resetClient()` + `start()`, sends no fingerprint on the in-app Cancel, registers push by hand after pairing, and Quick Dictate shows raw exception messages.

## 5. Notifications

- One notification per pane (`pane_id.hashCode()`); digests use a fixed id. Channels: `agent_blocked` (high), `agent_done` (default), `agent_watch_feedback` (low, silent action results).
- Every `PendingIntent` carries a data URI unique per pane and action (`agentwatch://notification/<action>/<pane>`), so extras from different panes can never be swapped.
- `resolved` pushes and live state (SSE/refresh) dismiss approvals that no longer match the agent. The relay sends `resolved` only with `AW_PUSH_RESOLVED` enabled.
- Action results: "Approved", "Denied", "Canceled", "Sent" (auto-dismissed after 3 s), or an error message with an Open action.

## 6. Tests

`./gradlew :app:testDebugUnitTest` (JVM, no device): `RelayEngine*Test` against `FakeRelay` (auth, restart, silence, merging), `RelayClientTest`, `FcmRegistrarTest`, `AgentStoreTest`, `HistoryMergeTest`, `AgentNotificationsTest`, `SurfaceUpdatesTest`, the `approval/` tests, and `ContractsTest` (parses `pkg/model/testdata/agent_state.json`).
