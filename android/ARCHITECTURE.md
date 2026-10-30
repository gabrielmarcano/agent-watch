# Agent Watch — Android Architecture (`:core`, `:app`, `:mobile`)

The Android codebase is organized as a multi-module Gradle project under `android/`:
- **`:core`**: Shared Android/Kotlin domain and data layer (network, models, key-value storage, update engine, notification dismissal, relay state machine).
- **`:app`**: The Wear OS wrist client (`androidx.wear.compose.material3`).
- **`:mobile`**: The Android phone messaging-style client (Material 3).

Both clients communicate with the relay's `/v1` API (`docs/reference/contracts.md` §1, §2, §4.1). Neither client talks to the host computer directly nor sends raw keystrokes. Build, install, and device verification procedures live in `.agents/skills/wearos-deploy/SKILL.md` (watch) and `docs/GUIDE.md` (phone).

---

## 1. Stack & Modules

- **`:core`**:
  - Models (`Contracts.kt`, `AgentKey.kt`) matching `pkg/model` with `@Keep` for R8.
  - OkHttp 4 + `okhttp-sse`, Gson.
  - Coroutines & Kotlin StateFlow.
  - `KeyValueStore` seam (`SharedPrefsKeyValueStore` + `InMemoryKeyValueStore`).
  - `UpdateController` 8-phase state machine over `UpdatePlatformPort`.
  - Notification dismissal and identity (`NotificationDismissal.kt`).
- **`:app` (Wear OS)**:
  - Jetpack Compose for Wear OS **Material 3** (`androidx.wear.compose.material3`): `AppScaffold`, `ScreenScaffold` + `TransformingLazyColumn` (native rotary, the clock scrolls away), `EdgeButton`, confirmation and alert dialogs.
  - Two tiles on ProtoLayout Material 3 (`Material3TileService`); complications (`androidx.wear.watchface.complications.datasource`).
  - System text input via `androidx.wear:wear-input` (keyboard, handwriting, voice).
  - Versions and SDK levels: `android/build.gradle.kts` and `app/build.gradle.kts` (its comment explains why Wear Material 3 stays on the 1.6 line).
- **`:mobile` (Phone)**:
  - Jetpack Compose Material 3 with edge-to-edge support (API 37 compile SDK).
  - WhatsApp-style conversation UI, reactive `MobileChatState` (pin/mute/hide, seen marks, optimistic outbox).
  - Local-only grouped notifications (data-only FCM pushes).
  - Multi-host browsing and 3-step `NewSessionController` over `SessionTransport`.

---

## 2. Packages

### `:core`

| Package | What lives there |
|---|---|
| `model/` | `Contracts.kt`: mirrors `pkg/model` field by field; `AgentKey.kt`: host + pane id identity, token hashing, target resolution (`resolveTargetAgent`). |
| `data/` | `KeyValueStore`: thread-safe persistence seam (`SharedPrefsKeyValueStore` and `InMemoryKeyValueStore`). `Prefs`: application preferences over `KeyValueStore`, with synchronized atomic `recordResolvedSeq(agent, seq)`. `AgentStore`: merges SSE and refreshes by `AgentKey`. `HistoryMerge`: deduplicates by `id`, sorted newest first, capped at limit. |
| `network/` | `RelayClient`: stateless `/v1` REST/SSE client. `RelayEngine`: core SSE lifecycle, reconnect backoff, per-pane and global history, session options caching, and command execution. `RelayRepository`: Android façade over `RelayEngine`. `UiState`: immutable state containing `agents`, `history`, `paneHistory`, `hosts`, `sessionOptionsByHost`, `auth`, `connection`, `stale`. `NotificationDismissal.kt`: shared notification constants, URI generation, request codes, and dismissal helpers (`dismissResolved`, `dismissStaleApprovals`, `cancelAllNotifications`). |
| `update/` | `UpdateController`: pure 8-phase update state machine (`IDLE`, `CHECKING`, `UP_TO_DATE`, `AVAILABLE`, `DOWNLOADING`, `READY`, `NEEDS_PERMISSION`, `FAILED`) over `UpdatePlatformPort` seam. `UpdateManager`: Android wrapper used by `:app` and `:mobile`. |
| `approval/` | `RelayErrorKind`: client-neutral error classification (`classifyRelayError`) with `refresh` and `clearsOptimisticPrompt` semantics. `CommandFeedback`: maps errors to wrist-sized feedback strings while keeping texts identical. |

### `:app` (Wear OS)

| Package | What lives there |
|---|---|
| `approval/` | `PromptActions`: primary buttons by role (ALLOW only ever maps to `allow_once`; the answered-prompt lock). |
| `ui/theme/` | Fixed palette (no dynamic colour; every text pair ≥ 6.6:1) and `statusStyle()`: icon, word and colours per herdr status. |
| `ui/logic/` | Pure presentation logic with JVM tests: `attentionSections` (list order), `hostPages` and `pageStatus` (the list's pages and each page's notice and dimming, `listStatus`), `headPreview`/`tailPreview`, `ageOf`, relay URL checks. |
| `ui/components/` | `ScreenList` (`ScreenScaffold` + `TransformingLazyColumn`), `transformedItem`, age text, the system input intents (`PromptInput`, `TextInput`), `rememberPaneHistory` (observes `UiState.historyFor(agent)` and refreshes on screen start). |
| `ui/screens/` | Agent list, agent screen + `PromptSection`, dictation confirm (`DictationFlow`), full text, history, reader, pairing, settings. |
| `complication/` | `complicationContent` (state → count and the agent a tap opens, across online hosts), `complicationBadge` (glyph and how many need the user) and data source: `SHORT_TEXT`, `LONG_TEXT`, `MONOCHROMATIC_IMAGE`. |
| `tile/` | Quick Dictate (`tileContent`, `dictationTarget`, `QuickDictateActivity`); Agents (`agentsTile`: the two most urgent agents across online hosts, each opening its screen, and how many more); `TileCommon` (palette, 3 s fetch, launching into app with pane and host). |
| `util/` | `MarkdownFormatter` (plain previews; tables become one line per row), `MarkdownBlocks` (reader's block parser; tables become records). |

### `:mobile` (Phone)

| Package | What lives there |
|---|---|
| `ui/` | `MobileChatState`: process-wide reactive singleton over `ChatStateStorage` (`SharedPrefsChatStateStorage` + `InMemoryChatStateStorage`). `ChatListScreen`, `ChatScreen`, `MachinesScreen`, `NewSessionScreen`, `SettingsScreen`, `PairingScreen`. `rememberPaneHistory` (observes `UiState.historyFor(agent)`). |
| `ui/logic/` | `NewSessionController` over `SessionTransport` seam (`loadSessionOptions`, `listFolders`, `createSession`). |
| `ui/theme/` | `ThemePrefs`: theme persistence over `KeyValueStore`. Material 3 dynamic and fixed themes. |
| `push/` | `MobileMessagingService`: data-only FCM push receiver; `MobileNotifications`: grouped notifications and quick reply. |

---

## 3. Data Flow & Engine Architecture

```
             GET /v1/agents, /v1/history          POST answer / cancel / prompt
 RelayClient ──────────────────────┐        ┌──────────────── (capped: contracts §2.4)
      ▲                            ▼        │
      │ SSE /v1/events      RelayEngine ── AgentStore + HistoryMerge
      │ (silence =               │
      │  reconnect)              ▼
      └──────────────── MutableStateFlow<UiState> ──► RelayRepository.state ──► UI
                                 │
                   hooks ────────┼──► RelayObservers.onAgentsUpdated (dismiss stale approvals)
                                 ├──► RelayObservers.onAuthCleared (cancel notifications, clear state)
                                 └──► PushRegistration.ensure (on every stream open)
 state collector (RelayRepository.init) ──► RelayObservers.onState (complication / tiles refresh)

 FCM ──► MessagingService ──► blocked / done / digest notifications
                          └──► resolved: dismiss the pane's approval
 Notification action ──► ActionReceiver / Service ──► RelayClient
```

- **One engine per process:** Managed via `RelayRepository.init(context)`. It listens for 401s process-wide (`RelayClient.unauthorizedListener`).
- **`RelayObservers` lifecycle:** Both `AgentWatchApp` and `MobileApplication` install `RelayObservers`:
  - `onAgentsUpdated`: Dismisses approvals that are no longer waiting.
  - `onAuthCleared`: Triggered on HTTP 401 (`onUnauthorized`), user unpairing/forgetting, or fresh pairing. Cancels active notifications and wipes client chat outboxes.
- **SSE lifecycle:** Started in `MainActivity.onStart`, stopped in `onStop` (battery). Notifications cover the background.
- **Network:** HTTPS only; release builds allow no cleartext (the debug build allows a local relay: `src/debug/res/xml/network_security_config.xml`). Host and pane ids are URL-encoded in paths. Commands go to `/v1/hosts/{host}/agents/{pane_id}/…` when the agent has a host, else to the older `/v1/agents/{pane_id}/…` (contracts §1.2).
- **Reconnect:** The backoff and silence rules of `contracts.md` §2.3; only a stream that delivered a `snapshot` resets the backoff. The SSE client's read timeout detects a silent (half-open) stream. First `snapshot` after reconnect re-fetches `GET /v1/history`.
- **Merging:** A refresh never overrides an agent that SSE touched after the refresh started; within an agent the higher `state_change_seq` wins. History from both sources is merged by `id`.
- **Per-pane history caching:** `UiState` owns `paneHistory: Map<AgentKey, List<HistoryItem>>` populated on demand via `RelayRepository.loadPaneHistory(agent, limit)`. `UiState.historyFor(agent)` dynamically merges and deduplicates global and pane history.
- **Host options caching:** `UiState` owns `sessionOptionsByHost: Map<String, SessionOptionsResponse>` loaded via `RelayRepository.loadSessionOptions(hostId)`.

---

## 3a. Several Hosts

Every herdr numbers its own panes, so `w1:p1` can exist on every host (contracts §1.2). An agent's identity is `AgentKey` (host, pane id), never the bare pane id:

- **Everywhere:** The store, Compose `key`s (`AgentKey.token`), nav routes, intent extras (`pane_id` and `host`) and URIs, notification ids, request codes and tags, resolved seqs, the pinned target, tile and complication extras, `SentAnswer`, commands and one agent's history.
- **No host (`""`):** A relay that predates hosts. `AgentKey.token` is then the pane id alone, so ids, URIs and stored values stay as they were. A key without a host from an intent, a tile or a pin saved before hosts finds its pane only while one host has it (`findAgent`), never by guessing between two; the agent screen shows such a pin as set by the same rule (`isPinnedTarget`).
- **Naming a host:** Only while the relay knows two or more (`hostNameFor`): the list page's header, a line on the agent screen and the dictation confirm, the tiles and the complication. With one host nothing names it. Pushes: the relay's `title` already names it (contracts §4.1); the app shows it as it is.
- **Online:** Each host has its own flags (`UiState.hosts`); `hostOnline` and `herdrOnline` are the relay's aggregates (contracts §1.5), used as before when the relay lists no hosts.
- **Tiles, complication and Quick Dictate:** Agents of online hosts only (an offline host's last known agents cannot be answered, so they leave the count and are never the target: `onlineAgents`, `targetCandidates`); "device offline" only when every host is (`allHostsOffline`), and then the Quick Dictate tile still names its target.

---

## 4. Wear OS Client (`:app`)

### 4a. What the Wear UI Must Use

```kotlin
RelayRepository.state: StateFlow<UiState>
data class UiState(connection, hostOnline, herdrOnline, agents, history, paneHistory, sessionOptionsByHost, stale: Boolean, auth: AuthState, hosts)
enum class AuthState { PAIRED, UNPAIRED, REVOKED }

RelayRepository.start(context) / stop()          // foreground only
RelayRepository.restart(context)                 // after pairing or re-pairing
RelayRepository.refresh()
RelayRepository.answer(agent: AgentKey, optionId, expectedSeq, fingerprint)   // agent: the shown AgentState's key
RelayRepository.cancel(agent, expectedSeq, fingerprint)   // pass the shown prompt's fingerprint
RelayRepository.prompt(agent, text, expectedSeq)
commandErrorFeedback(error, surface)             // the message to show for a failed command
```

- **Check `auth` first:** `UNPAIRED` or `REVOKED` → pairing screen, never the device-offline notice or an empty list.
- **`stale`:** The list is not backed by a live stream (before the first snapshot, reconnecting, stopped): dim it and show the connecting or relay-offline notice (`listStatus`), never the device-offline one.
- **Commands:** Use the `state_change_seq` and `fingerprint` of the `AgentState` shown at tap time; disable buttons while in flight and, after success, until the agent's state changes (`isAwaitingUpdate`); never auto-retry after a 409.
- **Pairing:** Save the token, then `RelayRepository.restart(context)`. Do not call `registerPush` or write `fcmRegisteredToken` (ignored); `FcmRegistrar` registers when the stream opens and records it only after the relay accepts it.
- **How the UI uses it:** `MainActivity` observes `auth` at runtime (anything but `PAIRED` → pairing; `REVOKED` adds `session_expired` line); each screen collects only its own slice of `state`; every command error goes through `commandErrorFeedback`.

### 4b. Screens

| Route | Screen |
|---|---|
| `pairing` | Relay URL (default `BuildConfig.DEFAULT_RELAY_URL`, edited with the system keyboard) and the 6-digit code |
| `agents` | One page per host in the relay's `hosts` order (`hostPages`), swiped sideways (`HorizontalPagerScaffold`, with a page indicator); each page: host's name, own notice line (`pageStatus`), only its agents. With 0 or 1 hosts, one page with every agent, no name and no indicator. Each page: sections by attention (`attentionSections`: needs you, done, working, idle, unknown state) across workspaces; each card: status icon, name (2 lines), agent logo and status word, background line when present, workspace; History and Settings |
| `agent/{host}/{paneId}` | `host` is `_` for no host. Name first (the label), then one fact per line: status, background line when present, status age, agent logo with workspace, agent's own title, host name with several hosts; prompt items (command head or `unknown` tail, View all, Deny · Allow, options with description, confirmation for `allow_always`); last reply with Read all (`headTailPreview`); Reply (system input) as edge button; "Pin to tile" |
| `dictation/{host}/{paneId}` | Text entered and target (with host name on own line with several hosts), then Send (Change enters it again) |
| `history?host=&paneId=`, `reader/{historyId}` | Cards with age: across all panes each card is named by session title, then query; one pane's history names session once in header, each card by query; answer rendered block by block |
| `settings` | Pair again, unpair, version |
| `text` | A command or screen tail in full |

### 4c. UX Rules

- **Target agent** for Quick Dictate and tile across online hosts (§3a): pinned agent (set only with "Pin to tile"; host and pane), else latest `done`, else the focused pane, else none (`resolveTarget` in `model/AgentKey.kt`, `resolveTargetAgent` in `model/Contracts.kt`). **The focused step never fires today:** herdr-core reports no focus, so the bridge publishes `focused: false` always (`contracts.md` §1.2), and a relay round-trip cannot tell which pane the person is looking at. With several online hosts it is skipped anyway (the owner's decision, 2026-10-11), so tile and Quick Dictate say there is no agent to dictate to. Never an arbitrary agent.
- **Prompt input** (Reply, Change, Quick Dictate): Wear's system RemoteInput, where the user picks voice, keyboard or handwriting. Not `ACTION_RECOGNIZE_SPEECH`: documented for voice only (`EXTRA_RESULTS`), and a prompt typed from the voice screen's keyboard never reached relay. RemoteInput has no voice-first option, so voice costs one tap. Never drop input silently (`ui/logic/InputResult.kt`): text in any known form goes to confirm screen; only backing out is silent; anything else shows a line and reject haptic. Logs carry result code and extra keys, never text.
- **Approvals:** Deny · Allow with the positive action on the right (Wear convention), full-height buttons 8 dp apart; every other option sits apart under More options, and `allow_always` asks for confirmation. An `unknown` prompt shows the screen tail, a line asking to answer on the device, and Cancel only (`ui/screens/PromptSection.kt`).
- **Waiting on background agents:** A `working` agent with `background_agents` > 0 (`contracts.md` §1.2) has finished its turn: row and screen show done icon, colour and word, with count on background line (`agentStatus`, `ui/logic/AgentSections.kt`). Stays in Working section, while complication, tile and notifications keep `working`.
- **Background line:** What the agent still runs in the background on its own line: `2 agents · 1 shell · 1 monitor` (`background_shells` and `background_monitors`). Shells and monitors change no status, section or surface.
- **Short lines (the owner's rule, 2026-10-05):** A line that does not wrap is unreadable once long. Use short words, one fact per line, and put extra information on a line of its own instead of chaining with ` · `. Chain only short facts that always fit, such as section title and count (`Done · 2`). A long name gets its own line, cut at end.
- **Agent type:** Small monochrome logo, tinted like text beside it and sized in sp (`AgentLogo`; mapped via `agentBrand` in `ui/logic/AgentBrand.kt`), never the agent id as text; content description is agent's name. Converted from lobe-icons monochrome SVGs.
- **Readability and feedback:** Status is icon + word + colour, never truncated, colour never alone; no text under 12 sp, checked at font scale 1.24; feedback where the finger is (confirmation dialog plus haptics, errors right above buttons); no Back buttons (swipe to dismiss).

### 4d. Notifications

- One notification per agent (`AgentKey.token.hashCode()`: pane id's hash without host, §3a); digests use fixed id. Channels: `agent_blocked` (high), `agent_done` (default), `agent_watch_feedback` (low, silent action results).
- Every `PendingIntent` carries a data URI unique per agent and action (`agentwatch://notification/<action>/<pane>?host=<host>`, no query without host), so extras from different agents can never be swapped.
- `resolved` pushes and live state dismiss approvals that no longer match the agent, matched by host and pane; a late `blocked` is dropped against the last `resolved` seq of its (host, pane).
- Buttons (`blockedButtons`): Question answers, permission Allow and Deny, nothing for `unknown` prompt, then Open. Finished agent notification offers Reply (keyboard or voice → `prompt`). Action results: short confirmation (auto-dismissed after 3 s) or error message with Open.

---

## 5. Mobile Phone Client (`:mobile`)

### 5a. Reactive Chat State (`MobileChatState`)

- **Singleton ownership:** Instantiated in `MobileApplication` over `ChatStateStorage` seam (`SharedPrefsChatStateStorage` for production, preserving `"AgentWatchChatFlags"` and `"AgentWatchChatSeen"` SharedPreferences; `InMemoryChatStateStorage` for tests).
- **StateFlow exposure:** Exposes `val state: StateFlow<ChatStateSnapshot>` containing `pinned`, `muted`, `hidden`, `seen: Map<String, SeenMark>`, and `outbox: Map<String, List<PendingMessage>>`.
- **Pure query model:** `isPinned`, `isMuted`, `isHidden`, `shouldSuppressPush`, `seen(agent)`, `seenAt(key)`, and `pendingFor(agent, history, now)`. Never mutates outbox during Compose composition.
- **Synchronized mutations:** `togglePin`, `setMuted`, `unmute`, `setHidden`, `unhide`, `markSeen`, `addOutbox`, `reconcileOutbox`, and `clearAll`.
- **Auth reset:** `clearAll()` is invoked on HTTP 401 (`onUnauthorized`), user unpairing/forgetting, and fresh pairing via `RelayObservers.onAuthCleared`.

### 5b. Notifications

- **Grouped conversation notifications:** One active notification per unmuted chat under a summary group (`DIGEST_ID = 1000`).
- **Inline quick replies:** Uses Android `RemoteInput` for direct in-notification message sending.
- **Approval actions:** Approve and Deny actions marked with `setAuthenticationRequired(true)` to prevent accidental wrist/pocket activations.
- **Dismissal:** Automatically dismissed when agent state changes via `RelayObservers.onAgentsUpdated` and wiped on `RelayObservers.onAuthCleared`.

### 5c. New Session Flow (`NewSessionController`)

- **State machine seam:** Controlled by `NewSessionController` over `SessionTransport` (`loadSessionOptions`, `listFolders`, `createSession`).
- **Multi-step workflow:** Host selection → agent kind picker → folder browser (with path breadcrumbs and recent folders) → title & creation.
- **Error classification:** Relay errors are classified via `classifyRelayError` into `RelayErrorKind` and mapped to localized `@StringRes` in the phone UI.

---

## 6. Testing & Verification

- **Pure JVM unit tests:**
  - `:core`: `RelayEngineTest`, `RelayEngineHistoryTest`, `RelayEngineAuthTest`, `UpdateControllerTest`, `CommandFeedbackTest`, `PrefsTest`, `KeyValueStoreTest`.
  - `:mobile`: `MobileChatStateTest`, `NewSessionTest`.
  - `:app`: `CommandFeedbackTest`, `AgentSectionsTest`.
- **Full verification** (per module; the root project has no aggregate `test`/`lint`/`assembleDebug` tasks), one Gradle build at a time:
  ```bash
  cd android && ./gradlew :core:testDebugUnitTest :app:testDebugUnitTest :app:lintDebug \
    :app:assembleDebug :mobile:testDebugUnitTest :mobile:lintDebug :mobile:assembleDebug
  ```
