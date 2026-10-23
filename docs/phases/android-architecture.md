# Android (`:core` & `:mobile`) Architecture Deepening Guide

A temporary guide for deepening shallow modules in the Android codebase (`wearos-app/core/`, `wearos-app/mobile/`, and shared hooks in `wearos-app/app/`) as the project shifts to mobile Android first. Delete this file when all slices are done (`docs/STATUS.md` workflow), after moving any lasting facts to their home (`AGENTS.md` §0).

## Workflow & Global Constraints

- **Claim before starting:** put your agent name and date on the slice you are taking in [`docs/STATUS.md`](../STATUS.md) under **Open items → Android (`:core` & `:mobile`) architecture deepening**, and commit that claim alone before editing code.
- **Rules that apply:** [`AGENTS.md`](../../AGENTS.md), [`.agents/rules/wearos.md`](../../.agents/rules/wearos.md), [`.agents/rules/contracts.md`](../../.agents/rules/contracts.md), and [`wearos-app/ARCHITECTURE.md`](../../wearos-app/ARCHITECTURE.md).
- **Secrets & build safety (non-negotiable):**
  - Never read, print, create, or commit `google-services.json` or `keystore.properties` (`AGENTS.md` §3, `.agents/rules/wearos.md`).
  - No wire contract changes (`pkg/model`, `docs/reference/contracts.md`, `Models.kt`) are required by any slice below.
- **Testing discipline ("replace, don't layer"):**
  - The module's **interface is the test surface**. When a slice deepens a module, write pure JVM unit tests (`wearos-app/core/src/test/` and `wearos-app/mobile/src/test/`) that exercise the module through its public interface using in-memory adapters (`InMemoryKeyValueStore`, fake `RelayTransport`, fake `UpdatePlatformPort`), and delete obsolete tests or workarounds that poked past the seam.
- **Verification before committing each slice:**
  ```bash
  cd wearos-app && ./gradlew test lint assembleDebug
  ```

## Order and Parallelism

| Slice | Scope | Can run in parallel with |
|---|---|---|
| **Slice 1** | `:mobile` local chat state (`ChatStore`, `ChatSeen`, `ChatOutbox` → `MobileChatState`) | Slice 2, Slice 4 (coordinate `KeyValueStore` if run with Slice 5) |
| **Slice 2** | `:core` `RelayEngine` per-pane history & command refresh; `:mobile` `ChatHistory.kt`, `ChatScreen.kt`; `:app` `PaneHistory.kt` | Slice 1, Slice 3, Slice 5 (coordinate `RelayEngine.kt` with Slice 4) |
| **Slice 3** | `:core` shared notification dismissal, `RelayObservers.onAuthCleared`, `UpdateController`; `:mobile` `MobileApplication`, `MobileNotifications` | Slice 2, Slice 4 (coordinate `MobileApplication.kt` and `onAuthCleared` with Slice 1) |
| **Slice 4** | `:core` `RelayErrorKind`, `CommandFeedback`, `RelayEngine` `SessionOptions` cache; `:mobile` `NewSessionController`, `NewSessionScreen`, `MachinesScreen` | Slice 1, Slice 3, Slice 5 |
| **Slice 5** | `:core` `KeyValueStore` seam & atomic `Prefs.recordResolvedSeq`; `:mobile` `ThemePrefs`, `MobileMessagingService` | Slice 2, Slice 4 (run **before** or together with Slice 1 so `MobileChatState` can use `KeyValueStore`) |

> **Recommended execution order for a single agent:** **Slice 5 → Slice 1 → Slice 3 → Slice 2 → Slice 4** (Slice 5 establishes `KeyValueStore`; Slice 1 builds `MobileChatState` on top of it; Slice 3 wires `MobileChatState.clearAll()` and notification dismissal into `RelayObservers.onAuthCleared` and extracts `UpdateController`; Slice 2 deepens `RelayEngine` per-pane history and command refresh; Slice 4 deepens `RelayErrorKind` and `NewSessionController`).

---

## Slice 1: Unify Local Chat State (`ChatStore`, `ChatSeen`, `ChatOutbox`) into One Reactive Module

**Strength:** `Strong` · **Category:** `in-process`

### Files Involved
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/ui/ChatStore.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/ui/ChatSeen.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/ui/ChatOutbox.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/MobileApplication.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/ui/MobileApp.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/ui/ChatListScreen.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/ui/ChatScreen.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/ui/SettingsScreen.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/ui/PairingScreen.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/push/MobileMessagingService.kt`

### Current Architectural Friction
1. **Stale Compose reads (recomposition bug):** Unlike `ThemePrefs.kt` (which mirrors `SharedPreferences` into `mutableStateOf`), neither `ChatStore` (`ChatStore.kt:16–99`) nor `ChatSeen` (`ChatSeen.kt:17–76`) exposes observable Compose state or `StateFlow`. Both read directly from `SharedPreferences` during composition (`chatStore.isPinned(...)`, `chatStore.isMuted(...)`, `chatStore.isHidden(...)`, `seen.seen(...)`, `seen.seenAt(...)`). When the user unmutes or restores a hidden chat in `SettingsScreen.kt:190, 206` (`chatStore.unmute(token)`, `chatStore.unhide(token)`), or swipes to mark a chat read in `ChatListScreen.kt:342`, nothing triggers recomposition until an unrelated `UiState` SSE emission arrives.
2. **Duplicate instances and shadowed parameters:**
   - `MobileApp.kt:126–127` creates `chatStore` and `chatSeen` and passes `seen = chatSeen` and `onMarkRead` into `ChatScreen`.
   - `ChatScreen.kt:148, 156` **shadows** its own `seen: ChatSeen` parameter with `val seen = remember(context) { ChatSeen(context) }`.
   - `ChatListScreen.kt:144` creates yet another `remember(context) { ChatSeen(context) }` instance.
3. **Composition-time side effects in `ChatOutbox.forAgent`:** `ChatOutbox.forAgent(agent, history, now)` (`ChatOutbox.kt:61–92`) is called directly inside Compose (`ChatScreen.kt:173`) and mutates `pending[key]` (`pending.remove(key)` / `pending[key] = kept`) during composition.
4. **Incomplete cleanup on auth reset:** When the user taps **Forget pairing** in `SettingsScreen.kt:298` (`prefs.clearAuth()`, `chatStore.clearAll()`, `ChatSeen(context).clearAll()`) or when an HTTP `401` triggers `RelayEngine.onUnauthorized`, `ChatOutbox.clear()` is **not** called (only `PairingScreen.kt:78` calls `ChatOutbox.clear()`).

### Target Deepened Design
1. **Define a storage seam (`ChatStateStorage`):**
   - Persist flags and seen marks through an injectable `ChatStateStorage` interface:
     - `SharedPrefsChatStateStorage(context)` for production: preserves the exact `"AgentWatchChatFlags"` (`pinned_tokens`, `muted_tokens`, `hidden_tokens`) and `"AgentWatchChatSeen"` (`seq_<token>`, `at_<token>`) preference files and keys so upgrading the APK loses no user state.
     - `InMemoryChatStateStorage` for JVM unit tests.
2. **Create `MobileChatState` and `ChatStateSnapshot`:**
   - `ChatStateSnapshot` is an immutable data class holding:
     - `pinned: Set<String>`, `muted: Set<String>`, `hidden: Set<String>`
     - `seen: Map<String, SeenMark>` (`data class SeenMark(val seq: Long, val atEpochMs: Long)`)
     - `outbox: Map<String, List<PendingMessage>>`
   - Pure query methods on `ChatStateSnapshot`:
     - `isPinned(key: AgentKey): Boolean`, `isMuted(key: AgentKey): Boolean`, `isHidden(key: AgentKey): Boolean`, `shouldSuppressPush(key: AgentKey): Boolean`
     - `seen(agent: AgentState): Boolean`, `seenAt(key: AgentKey): Long`, `seenSeq(key: AgentKey): Long`
     - `pendingFor(agent: AgentKey, history: List<HistoryItem>, now: Long = System.currentTimeMillis()): List<PendingMessage>` — pure read with zero mutation during composition.
   - `MobileChatState(storage: ChatStateStorage)` exposes `val state: StateFlow<ChatStateSnapshot>` and synchronized mutation methods that update both `storage` and `_state.value` atomically:
     - `setPinned(key, pinned)`, `togglePin(key)`
     - `setMuted(key, muted)`, `toggleMute(key)`, `unmute(token)`
     - `setHidden(key, hidden)`, `unhide(token)`
     - `markSeen(agent: AgentState, items: List<HistoryItem>, now: Long = System.currentTimeMillis())`
     - `addOutbox(agent: AgentKey, text: String, now: Long = System.currentTimeMillis()): PendingMessage`
     - `reconcileOutbox(agent: AgentKey, history: List<HistoryItem>, now: Long = System.currentTimeMillis())`
     - `clearAll()` — wipes flags, seen marks, and outbox atomically.
3. **Single process-wide instance on `MobileApplication`:**
   - Initialize `MobileChatState` once in `MobileApplication` (with a test override hook) so `MobileApp`, `ChatListScreen`, `ChatScreen`, `SettingsScreen`, `PairingScreen`, and `MobileMessagingService` all observe and mutate the same reactive `MobileChatState`.
   - Delete the shadowed `val seen = remember(context) { ChatSeen(context) }` in `ChatScreen.kt:156` and the duplicate `ChatSeen(context)` in `ChatListScreen.kt:144`.

### Verification & Tests
- Add `wearos-app/mobile/src/test/java/com/gabriel/agentwatch/mobile/ui/MobileChatStateTest.kt` testing `MobileChatState` with `InMemoryChatStateStorage`:
  - Pin, mute, hide, unmute, and unhide immediately emit updated `ChatStateSnapshot`s on `state`.
  - `shouldSuppressPush` returns `true` when either muted or hidden.
  - `markSeen` records max `stateChangeSeq` and max history timestamp (`completedAt` / `askedAt`), and `seen(agent)` transitions from `false` to `true` and back to `false` when `agent.stateChangeSeq` advances.
  - `pendingFor` filters acknowledged turns (matching normalized query with `askedAt >= sentAt - 5_000`) and expired items (`> 10 min`) purely without side effects, while `reconcileOutbox` prunes them from state.
  - `clearAll()` resets flags, seen marks, and outbox in a single emission.
---

## Slice 2: Deepen Per-Pane History & Command Execution Behind `RelayEngine`

**Strength:** `Strong` · **Category:** `local-substitutable`

### Files Involved
- `wearos-app/core/src/main/java/com/gabriel/agentwatch/network/RelayEngine.kt`
- `wearos-app/core/src/main/java/com/gabriel/agentwatch/network/RelayRepository.kt`
- `wearos-app/core/src/main/java/com/gabriel/agentwatch/data/HistoryMerge.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/ui/ChatHistory.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/ui/ChatScreen.kt`
- `wearos-app/app/src/main/java/com/gabriel/agentwatch/ui/components/PaneHistory.kt`
- `wearos-app/core/src/test/java/com/gabriel/agentwatch/network/RelayEngineTest.kt`

### Current Architectural Friction
1. **Duplicated composable history fetcher bypasses `RelayEngine`:**
   - `:mobile`'s `ChatHistory.kt:30–42` (`PANE_PAGE = 50`) and `:app`'s `PaneHistory.kt:30–42` (`PANE_PAGE = 20`) are near-identical copies of `rememberPaneHistory(agent: AgentKey)` that reach past `RelayEngine` via `RelayRepository.getClient()?.history(agent, PANE_PAGE)` into ephemeral `var fetched by remember(agent) { mutableStateOf(emptyList()) }`.
   - Because `fetched` is discarded every time the user leaves `ChatScreen`, re-opening a chat first renders only the global 20-item history slice (`RelayEngine.kt:327`) and then jumps when the 50-item pane fetch arrives (`ChatScreen.kt:558–573`), while `ChatListScreen` previews and unread counts only ever see the global 20-item slice.
2. **Duplicate `refresh()` on command failure:**
   - `RelayEngine.refreshAfter` (`RelayEngine.kt:339–345`) already calls `if (commandFailure(r.exceptionOrNull()).refresh) refresh()` on every `approveOption`, `cancelAgent`, and `promptAgent` call.
   - Despite that, `ChatScreen.kt:211, 224` checks `if (feedback.refresh) scope.launch { RelayRepository.refresh() }` a second time.

### Target Deepened Design
1. **Deepen `RelayEngine` and `UiState` to own per-pane history:**
   - Add `val paneHistory: Map<AgentKey, List<HistoryItem>> = emptyMap()` to `UiState` (`RelayEngine.kt:24–40`) and a convenience query `fun UiState.historyFor(agent: AgentKey): List<HistoryItem>` that returns `mergeHistory(history.filter { it.key == agent }, paneHistory[agent].orEmpty())`.
   - Add `suspend fun loadPaneHistory(agent: AgentKey, limit: Int = 50): Result<List<HistoryItem>>` on `RelayEngine` (and `RelayRepository.loadPaneHistory(agent, limit)`), which fetches `client.history(agent, limit)` and merges the result into `UiState.paneHistory[agent]`.
   - When `history_added` arrives over SSE (`RelayEngine.kt:248–252`) or when `applyRefreshed` updates global history, keep `UiState.historyFor(agent)` up to date via `mergeHistory`, and clear `paneHistory` when auth is cleared (`onUnauthorized` / `clearPairing`).
2. **Unify `rememberPaneHistory`:**
   - Simplify `ChatHistory.kt` (`limit = 50`) and `PaneHistory.kt` (`limit = 20`) to observe `RelayRepository.state.map { it.historyFor(agent) }` and trigger `RelayRepository.loadPaneHistory(agent, limit)` on `Lifecycle.State.STARTED`.
3. **Remove duplicate post-command `refresh()` calls:**
   - Delete `if (feedback.refresh) scope.launch { RelayRepository.refresh() }` from `ChatScreen.kt:211, 224` so `RelayEngine.refreshAfter` is the single authority for post-command refreshes.

### Verification & Tests
- Add tests in `wearos-app/core/src/test/java/com/gabriel/agentwatch/network/RelayEngineTest.kt`:
  - `loadPaneHistory(agent, 50)` populates `state.value.historyFor(agent)`, survives across subsequent global refreshes, merges live SSE `history_added` events without duplicates, and clears on `401` `onUnauthorized`.
  - Verify a command failing with `stale_state` (`prompt_changed`) triggers exactly one `agents()` + `history()` refresh via `RelayEngine.refreshAfter`.

---

## Slice 3: Wire Mobile Notification Lifecycle via `RelayObservers` & Decouple `UpdateManager`

**Strength:** `Strong` · **Category:** `ports & adapters`

### Files Involved
- `wearos-app/core/src/main/java/com/gabriel/agentwatch/network/RelayRepository.kt`
- `wearos-app/core/src/main/java/com/gabriel/agentwatch/network/PushMessages.kt`
- `wearos-app/core/src/main/java/com/gabriel/agentwatch/update/UpdateManager.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/MobileApplication.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/ui/MobileApp.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/ui/SettingsScreen.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/ui/PairingScreen.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/push/MobileNotifications.kt`
- `wearos-app/app/src/main/java/com/gabriel/agentwatch/AgentWatchApp.kt`
- `wearos-app/app/src/main/java/com/gabriel/agentwatch/network/AgentNotifications.kt`
- `wearos-app/app/src/main/java/com/gabriel/agentwatch/network/NotificationSupport.kt`

### Current Architectural Friction
1. **Outdated watch-only assumption in `RelayRepository` and `MobileApplication`:**
   - `RelayRepository.kt:30–31, 58–60` and `AgentWatchApp.kt:18` still state that the phone app installs `RelayObservers.NONE`.
   - Because `MobileApplication.kt` does not set `RelayRepository.observers`, `MobileApp.kt:176–178` uses a UI-only workaround: `LaunchedEffect(state.agents) { MobileNotifications.dismissStale(context, AgentsUpdate.All(state.agents)) }`.
   - Consequently, stale mobile notifications are only dismissed while `MobileApp` is composed, `AgentsUpdate.Removed` is missed unless the UI recomposes, and `401` auth revocation never clears active phone or watch notifications.
2. **Duplicated notification identity & dismissal code across `:mobile` and `:app`:**
   - `MobileNotifications.kt:23–48, 139–170` and `:app`'s `AgentNotifications.kt:19–52` + `NotificationSupport.kt:112–148` (`ApprovalNotifications`) duplicate `DIGEST_ID`, `EXTRA_KIND`, `EXTRA_PANE`, `EXTRA_HOST`, `EXTRA_SEQ`, `KIND_APPROVAL`, `KIND_DONE`, `KIND_FEEDBACK`, `idFor(agent)`, `intentUri(agent, action, optionId)`, `requestCode(agent, actionOrd)`, active-approval scanning (`shownApprovals`), `onResolved`, and `dismissStale` / `onAgentsUpdated`.
3. **Hardcoded global singletons in `UpdateManager`:**
   - `UpdateManager.kt:34–282` is a singleton `object` that calls `RelayRepository.init(context)` and `RelayRepository.updateInfo(channel)` directly (`lines 84, 142`), creates its own `OkHttpClient`, and calls `PackageManager` and `FileProvider` inline—preventing JVM unit tests from exercising its 8-phase state machine (`IDLE`, `CHECKING`, `AVAILABLE`, `DOWNLOADING`, `READY`, `NEEDS_PERMISSION`, `FAILED`, `UP_TO_DATE`).

### Target Deepened Design
1. **Share notification identity and `NotificationManager` dismissal in `:core`:**
   - Create `wearos-app/core/src/main/java/com/gabriel/agentwatch/network/NotificationDismissal.kt` (or expand `PushMessages.kt`) holding shared notification constants (`DIGEST_ID`, `EXTRA_KIND`, `EXTRA_PANE`, `EXTRA_HOST`, `EXTRA_SEQ`, `KIND_APPROVAL`, `KIND_DONE`, `KIND_FEEDBACK`), `idFor(agent)`, `intentUri(agent, actionPath, optionId)`, `requestCode(agent, actionOrdinal)`, and `NotificationManager` helpers `dismissResolved(context, message)`, `dismissStaleApprovals(context, update)`, and `cancelAllNotifications(context)`.
   - Have `:mobile`'s `MobileNotifications` and `:app`'s `AgentNotifications` / `ApprovalNotifications` delegate to `:core`.
2. **Wire `RelayObservers` & `onAuthCleared` in `MobileApplication` and `AgentWatchApp`:**
   - Add `fun onAuthCleared(context: Context) {}` to `RelayObservers` (`RelayRepository.kt:28–39`).
   - Add `RelayRepository.clearPairing(context: Context)` (which clears `Prefs.clearAuth()`, invokes `observers.onAuthCleared(context)`, and resets the engine's auth state), and also invoke `observers.onAuthCleared(ctx)` inside `RelayEngineHooks.clearAuth()` (`RelayRepository.kt:99`) and on fresh pairing before storing the new token (`PairingScreen.kt:76–80`).
   - In `MobileApplication.onCreate()`, install `RelayRepository.observers`:
     - `onAgentsUpdated` → `MobileNotifications.dismissStale(context, update)`
     - `onAuthCleared` → `MobileNotifications.cancelAll(context)` and `mobileChatState.clearAll()`
   - Remove the `LaunchedEffect(state.agents)` workaround from `MobileApp.kt:176–178`.
3. **Extract `UpdateController` behind `UpdateManager`:**
   - Define `interface UpdatePlatformPort` in `wearos-app/core/src/main/java/com/gabriel/agentwatch/update/UpdateManager.kt`:
     ```kotlin
     interface UpdatePlatformPort {
         fun installedVersionCode(): Long
         fun installedVersionName(): String
         suspend fun fetchUpdateInfo(channel: String): Result<UpdateInfo>
         suspend fun downloadApk(url: String, destFile: File, onProgress: (Long) -> Unit)
         fun canInstallPackages(): Boolean
         fun launchInstaller(apkFile: File): Boolean
         fun updatesDir(): File
     }
     ```
   - Move the 8-phase state machine into `class UpdateController(private val port: UpdatePlatformPort, private val scope: CoroutineScope)` and keep `object UpdateManager` as the thin Android adapter used by `:mobile`.

### Verification & Tests
- Add `wearos-app/core/src/test/java/com/gabriel/agentwatch/update/UpdateControllerTest.kt` testing all 8 states (`IDLE` → `CHECKING` → `UP_TO_DATE` / `AVAILABLE` → `DOWNLOADING` with progress and SHA-256 verification → `NEEDS_PERMISSION` when `canInstallPackages() == false` → `resumeInstallIfPermitted()` launching installer and transitioning to `READY`, plus size/SHA-256 mismatch → `FAILED`).
- Add a test in `RelayEngineTest.kt` / `:mobile` verifying `onAuthCleared` fires on both `401` `onUnauthorized` and `clearPairing`.

---

## Slice 4: Shift `:core` to Client-Neutral Domain & Deepen `NewSession` State Machine

**Strength:** `Worth exploring` · **Category:** `in-process`

### Files Involved
- `wearos-app/core/src/main/java/com/gabriel/agentwatch/approval/CommandFeedback.kt`
- `wearos-app/core/src/main/java/com/gabriel/agentwatch/network/RelayEngine.kt`
- `wearos-app/core/src/main/java/com/gabriel/agentwatch/network/RelayRepository.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/ui/logic/NewSession.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/ui/NewSessionScreen.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/ui/MachinesScreen.kt`
- `wearos-app/core/src/test/java/com/gabriel/agentwatch/approval/CommandFeedbackTest.kt`
- `wearos-app/mobile/src/test/java/com/gabriel/agentwatch/mobile/ui/logic/NewSessionTest.kt`

### Current Architectural Friction
1. **Wrist-specific error strings in `:core` and duplicate mapper in `:mobile`:**
   - `CommandFeedback.kt:11–55` maps relay error codes to hardcoded watch English strings and omits `not_allowed`.
   - Because of that, `NewSessionScreen.kt:71–78` defines its own `sessionErrorRes(error: Throwable)` mapper for `not_allowed`, `host_offline`, `herdr_unavailable`, and `bridge_busy`.
2. **Uncached `sessionOptions(hostId)` and 623-line composable workflow:**
   - `NewSessionScreen.kt:116–161` embeds a multi-step async workflow (`hostId` → `sessionOptions(hostId)` → `folders(hostId, viewPath)` → `createSession`) inside Compose `LaunchedEffect`s.
   - `MachinesScreen.kt:102–108` independently calls `RelayRepository.sessionOptions(host.id)` per host card on every composition without sharing or caching.
   - `NewSessionScreen.kt:338–367` contains two dead private composables (`HostChips` and `KindChips`) that are never called anywhere.

### Target Deepened Design
1. **Add `RelayErrorKind` in `:core` (`CommandFeedback.kt`):**
   - Introduce `enum class RelayErrorKind(val refresh: Boolean, val clearsOptimisticPrompt: Boolean)` with values `PROMPT_CHANGED`, `AGENT_NOT_BLOCKED`, `AGENT_BLOCKED`, `PANE_NOT_FOUND`, `HOST_OFFLINE`, `HERDR_UNAVAILABLE`, `BRIDGE_BUSY`, `NOT_ALLOWED`, `TIMEOUT`, `NETWORK`, `OTHER`, and `fun classifyRelayError(error: Throwable?): RelayErrorKind`.
   - Keep `commandFailure(error: Throwable?): CommandFailure` deriving its watch strings and flags from `classifyRelayError(error)` so watch texts stay identical (`AGENTS.md` §0).
   - Map `RelayErrorKind` to `@StringRes` in `:mobile` so `NewSessionScreen` and `ChatScreen` share one error classifier.
2. **Cache per-host `SessionOptions` in `RelayEngine`:**
   - Add `val sessionOptionsByHost: Map<String, SessionOptions> = emptyMap()` to `UiState` and `suspend fun loadSessionOptions(hostId: String): Result<SessionOptions>` on `RelayEngine` / `RelayRepository`, so `MachinesScreen` and `NewSessionScreen` render cached host capabilities immediately and refresh them in the background.
3. **Deepen `NewSession.kt` with `NewSessionController` & delete dead composables:**
   - Extract `NewSessionController` in `NewSession.kt` over a small `SessionTransport` seam (`loadSessionOptions(hostId)`, `listFolders(hostId, path)`, `createSession(hostId, kind, cwd, title)`) that manages host selection, recent-folder reconciliation, folder navigation, agent kind selection, and session creation state.
   - Delete the unused `HostChips` (`lines 338–351`) and `KindChips` (`lines 354–367`) composables from `NewSessionScreen.kt`.

### Verification & Tests
- Expand `CommandFeedbackTest.kt` to cover `classifyRelayError` for all error codes including `not_allowed`.
- Expand `NewSessionTest.kt` to test `NewSessionController` across host switching, cached vs loaded `SessionOptions`, folder browsing, and `not_allowed` / `host_offline` error classification.

---

## Slice 5: Unify `SharedPreferences` Persistence Behind a Single Thread-Safe Seam

**Strength:** `Worth exploring` · **Category:** `local-substitutable`

### Files Involved
- `wearos-app/core/src/main/java/com/gabriel/agentwatch/data/KeyValueStore.kt` (new)
- `wearos-app/core/src/main/java/com/gabriel/agentwatch/data/Prefs.kt`
- `wearos-app/core/src/main/java/com/gabriel/agentwatch/network/ResolvedSeqs.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/push/MobileMessagingService.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/push/MobileNotifications.kt`
- `wearos-app/mobile/src/main/java/com/gabriel/agentwatch/mobile/ui/theme/ThemePrefs.kt`
- `wearos-app/app/src/main/java/com/gabriel/agentwatch/network/MyFirebaseMessagingService.kt`
- `wearos-app/app/src/main/java/com/gabriel/agentwatch/network/NotificationActionReceiver.kt`

### Current Architectural Friction
1. **Unsynchronized read-modify-write race on `Prefs.resolvedSeqs`:**
   - In `MobileMessagingService.kt:34`, `MobileNotifications.kt:129`, `MyFirebaseMessagingService.kt`, and `NotificationActionReceiver.kt`, callers execute `prefs.resolvedSeqs = prefs.resolvedSeqs.record(agent, seq)` across concurrent FCM binder threads, broadcast receiver coroutines, and main threads without synchronization.
2. **Direct `Context.getSharedPreferences` coupling in `Prefs` and `ThemePrefs`:**
   - `Prefs(context)` (`Prefs.kt:12–120`) and `ThemePrefs(context)` (`ThemePrefs.kt:17–56`) require an Android `Context` to construct, preventing pure JVM tests from exercising preference persistence, auth clearing, and `resolvedSeqs` recording through `Prefs` directly.

### Target Deepened Design
1. **Introduce `KeyValueStore` in `:core` (`com.gabriel.agentwatch.data`):**
   ```kotlin
   interface KeyValueStore {
       fun getString(key: String, default: String? = null): String?
       fun putString(key: String, value: String?)
       fun getBoolean(key: String, default: Boolean = false): Boolean
       fun putBoolean(key: String, value: Boolean)
       fun getLong(key: String, default: Long = 0L): Long
       fun putLong(key: String, value: Long)
       fun getStringSet(key: String): Set<String>
       fun putStringSet(key: String, values: Set<String>)
       fun remove(key: String)
       fun clear()
       fun allKeys(): Set<String>
   }
   ```
   - Provide `SharedPrefsKeyValueStore(prefs: SharedPreferences)` for production and thread-safe `InMemoryKeyValueStore()` for JVM unit tests.
2. **Deepen `Prefs` and `ThemePrefs` over `KeyValueStore`:**
   - Add a primary constructor `class Prefs(private val store: KeyValueStore)` and keep `constructor(context: Context)` delegating to `SharedPrefsKeyValueStore(context.getSharedPreferences("AgentWatchPrefs", Context.MODE_PRIVATE))`.
   - Do the same for `ThemePrefs(store: KeyValueStore)` (`"AgentWatchPhoneTheme"`).
3. **Add atomic `Prefs.recordResolvedSeq(agent: AgentKey, seq: Long): ResolvedSeqs`:**
   - Synchronize read-modify-write of `resolved_seqs` inside `Prefs.recordResolvedSeq` (using a process-wide lock object on `Prefs.Companion`) so concurrent `Prefs(context)` callers on FCM and receiver threads never clobber each other's recorded sequences.
   - Replace `prefs.resolvedSeqs = prefs.resolvedSeqs.record(...)` call sites in `:mobile` and `:app` with `prefs.recordResolvedSeq(agent, seq)`.

### Verification & Tests
- Add `wearos-app/core/src/test/java/com/gabriel/agentwatch/data/PrefsTest.kt` using `InMemoryKeyValueStore`:
  - Verify `clearAuth()` clears token, deviceId, and fcmRegistration while preserving `relayUrl` and `pinnedAgentKey`.
  - Verify concurrent calls to `recordResolvedSeq` across multiple threads preserve all recorded pane sequences without lost updates.
