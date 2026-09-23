# Phase 4 — Wear OS Client (primary, Pixel Watch 2)

> **Goal:** the Wear OS app, rewritten against the relay's `/v1` API. It must support:
> - pairing with a code;
> - a multi-agent list;
> - approving and denying from parsed options (in the app and from notifications);
> - dictation to the right agent;
> - per-agent and global history with a markdown reader;
> - a complication and a tile.
>
> It is verified on the owner's **Google Pixel Watch 2**.

| | |
|---|---|
| **Depends on** | Phase 1 (contracts frozen). Build against a relay stub or the local relay (3a) until the real one is deployed |
| **Parallel with** | 2a, 2b, 2c, 3a, 3b. It only touches `wearos-app/` |
| **Touches** | `wearos-app/**`, `docs/STATUS.md` |
| **Must not** | send raw keys, talk to the Mac directly, or keep any LAN/Tailscale IP code |

---

## Read first

- [`docs/reference/contracts.md`](../reference/contracts.md) §1, §2, §4.1. The app is a client of exactly this.
- [`HERDR_REFACTOR_PLAN.md`](../../HERDR_REFACTOR_PLAN.md) §11 (target agent selection) and §13.1.
- `.claude/rules/wearos.md` (conventions: `@Keep`, rotary, lightweight tiles and complications).

---

## Current code → what happens to it

| Current file | Action |
|---|---|
| `model/AgentState.kt` | **Rewrite**: mirror `pkg/model` (§1 below) |
| `network/SseClient.kt` | **Replace** with `network/RelayClient.kt` + `network/RelayRepository.kt` |
| `network/MyFirebaseMessagingService.kt` | **Rewrite** payload handling and actions (§5) |
| `network/NotificationActionReceiver.kt` | **Rewrite**: `answer`/`cancel`/`prompt` with ids and seq, no `"y"`/`"n"` |
| `ui/screens/ServerConfigScreen.kt` | **Replace** with `ui/screens/PairingScreen.kt` |
| `ui/screens/AgentScreen.kt` (1003 lines) | **Split**: `AgentListScreen.kt`, `AgentDetailScreen.kt`, `PromptCard.kt`; keep `ResponseReaderScreen` and `MicrophoneIcon` and move them to their own files |
| `ui/screens/HistoryListScreen.kt` | **Adapt** to `HistoryItem` from the relay (per agent + global) |
| `complication/AgentStatusComplicationService.kt` | **Rewrite** the fetch: `GET /v1/agents` with the token, most severe status |
| `tile/AgentQuickActionTileService.kt`, `tile/QuickDictateActivity.kt` | **Adapt** to the target-agent rule (§11 of the plan) |
| `util/MarkdownFormatter.kt` | Keep (used for notification text and previews) |
| `MainActivity.kt` | **Simplify**: navigation root; the pairing gate |
| `AndroidManifest.xml` | Remove `usesCleartextTraffic="true"` (§8) |

---

## Steps

### 1. Models (`model/Contracts.kt`, replacing `AgentState.kt`)

Mirror `pkg/model` **field by field** with Gson, snake_case names, `@Keep` on every class. Nullable where the Go field is `omitempty`.

```kotlin
@Keep data class PromptOption(val id: String = "", val label: String = "", val role: String = "choice")

@Keep data class PendingPrompt(
    val kind: String = "unknown",          // "permission" | "question" | "unknown"
    val title: String = "",
    val detail: String? = null,
    val options: List<PromptOption> = emptyList(),
    val fingerprint: String = "",
    val raw_tail: String? = null,
)

@Keep data class AgentState(
    val pane_id: String = "",
    val agent: String = "",
    val label: String = "",
    val cwd: String? = null,
    val workspace_id: String = "",
    val status: String = "unknown",        // idle | working | blocked | done | unknown
    val focused: Boolean = false,
    val state_change_seq: Long = 0,        // uint64 in Go; Long is enough in practice
    val prompt: PendingPrompt? = null,
    val updated_at: String = "",
)

@Keep data class HistoryItem(
    val id: String = "", val pane_id: String = "", val agent: String = "", val label: String = "",
    val query: String? = null, val response: String = "", val source: String = "screen", val completed_at: String = "",
)

@Keep data class AgentsSnapshot(
    val host_online: Boolean = false, val herdr_online: Boolean = false,
    val agents: List<AgentState> = emptyList(), val generated_at: String = "",
)

@Keep data class HostEvent(val host_online: Boolean = false, val herdr_online: Boolean = false)
@Keep data class PaneRef(val pane_id: String = "")
@Keep data class HistoryResponse(val items: List<HistoryItem> = emptyList())
@Keep data class PairRequest(val code: String, val device_name: String)
@Keep data class PairResponse(val device_id: String = "", val device_token: String = "")
@Keep data class PromptRequest(val text: String, val expected_seq: Long)
@Keep data class AnswerRequest(val option_id: String, val expected_seq: Long, val fingerprint: String)
@Keep data class CancelRequest(val expected_seq: Long)
@Keep data class PushRegisterRequest(val platform: String = "fcm", val token: String)
@Keep data class ErrorBody(val code: String = "internal", val message: String = "")
@Keep data class ErrorResponse(val error: ErrorBody = ErrorBody())

fun AgentState.severity(): Int = when (status) {
    "blocked" -> 4; "done" -> 3; "working" -> 2; "idle" -> 1; else -> 0
}
```

**Contract parity test** (`app/src/test/java/.../ContractsTest.kt`):
1. Read `../../pkg/model/testdata/agent_state.json`. Gradle unit tests run with the working dir at `wearos-app/app`.
2. Parse it with Gson into `AgentState`.
3. Assert every field.

If Phase 1 changed that golden file, this test tells you the app is out of date.

### 2. Settings storage (`data/Prefs.kt`)

A tiny wrapper over `SharedPreferences("AgentWatchPrefs")` with these keys:

| Key | Content |
|---|---|
| `relay_url` | e.g. `https://relay.example.com` |
| `device_token` | from `/v1/pair` |
| `device_id` | from `/v1/pair` |
| `fcm_token` | the latest token from `onNewToken` |
| `fcm_registered_token` | the token last sent to the relay |
| `pinned_pane_id` | the agent used for dictation |

On first launch of the new version, **delete** `local_ip` and `tailscale_ip`.

### 3. Networking

**`network/RelayClient.kt`**: a stateless HTTP client. One shared `OkHttpClient` with `readTimeout(0)` for SSE, plus a normal one with a 15 s timeout for REST.

```kotlin
class RelayClient(private val baseUrl: String, private val token: String?) {
    suspend fun pair(code: String, deviceName: String): Result<PairResponse>
    suspend fun agents(): Result<AgentsSnapshot>
    suspend fun history(paneId: String?, limit: Int = 20): Result<List<HistoryItem>>
    suspend fun prompt(paneId: String, text: String, expectedSeq: Long): Result<Unit>
    suspend fun answer(paneId: String, optionId: String, expectedSeq: Long, fingerprint: String): Result<Unit>
    suspend fun cancel(paneId: String, expectedSeq: Long): Result<Unit>
    suspend fun registerPush(fcmToken: String): Result<Unit>
    fun events(listener: EventSourceListener): EventSource   // GET /v1/events
}
```

- Every request carries `Authorization: Bearer <token>`. **Never** put the token in the URL.
- Encode pane ids in paths with `URLEncoder.encode(paneId, "UTF-8")`.
- Map non-2xx responses to `Result.failure(RelayError(code, message, httpStatus))` by parsing `ErrorResponse`.
- Run REST on `Dispatchers.IO`.

**`network/RelayRepository.kt`**: a process-wide singleton (`object`, or created in `Application`) that owns the live state.

```kotlin
data class UiState(
    val connection: Connection = Connection.Connecting, // Connecting | Live | Offline(reason)
    val hostOnline: Boolean = false,
    val herdrOnline: Boolean = false,
    val agents: List<AgentState> = emptyList(),          // sorted: severity desc, label asc
    val history: List<HistoryItem> = emptyList(),        // newest first, max 200
)
object RelayRepository {
    val state: StateFlow<UiState>
    fun start(context: Context)   // opens SSE if paired; idempotent
    fun stop()
    suspend fun refresh()         // GET /v1/agents + GET /v1/history
}
```

**SSE handling:**

| `event` | Action |
|---|---|
| `snapshot` | **Replace** `agents`, `hostOnline`, `herdrOnline` |
| `agent` | Upsert by `pane_id` and re-sort |
| `agent_removed` | Remove it |
| `host` | Update the flags |
| `history` | Prepend, dedup by `id` |

- **Reconnect** on `onFailure`/`onClosed`: 1 s, 2 s, 4 s … up to 30 s. After reconnecting, rely on the next `snapshot`.
- **Lifecycle:** start SSE while the app is in the foreground (`ProcessLifecycleOwner` `ON_START`/`ON_STOP`), to save battery. Notifications cover the background.
- **On 401 anywhere:** clear `device_token` and navigate to pairing.

### 4. Screens and navigation (`MainActivity.kt` + `ui/screens/`)

Use `SwipeDismissableNavHost` (wear compose navigation, already a dependency):

| Route | Screen | Notes |
|---|---|---|
| `pairing` | `PairingScreen` | Relay URL field (prefill `https://`), 6-digit code field, "Pair" button. On success: save the token, register FCM (§5), go to `agents`. Show relay errors from §2.4 of the contracts |
| `agents` | `AgentListScreen` | `ScalingLazyColumn` with `rotaryScrollable`. Header: connection state + "Mac offline" / "herdr stopped" banners. One `Chip` per agent: label, agent kind, status color, and ⚠ when blocked. Bottom: "History" chip |
| `agent/{paneId}` | `AgentDetailScreen` | Status, cwd (shortened), `PromptCard` when blocked, "Dictate" button (idle/done, or working if the agent allows it; otherwise disabled with the reason), "History" for this agent. **Opening this screen sets `pinned_pane_id`** |
| `history?paneId=` | `HistoryListScreen` | From `UiState.history` (filtered), then `refresh()` on open |
| `reader/{historyId}` | `ResponseReaderScreen` | Markdown renderer when `source == "transcript"`; plain `Text` when `"screen"` |

Status colors (`ui/theme/Color.kt`):

| Status | Color |
|---|---|
| `blocked` | amber |
| `done` | green |
| `working` | light blue |
| `idle` | grey |
| `unknown` | dark grey |

Reuse the existing palette where it fits.

**`PromptCard`** (`ui/screens/PromptCard.kt`):

| `prompt.kind` | UI |
|---|---|
| `permission` | Title + detail (monospace, max 6 lines, scrollable). **Allow** = the first `allow_once` option. **Deny** = the first `deny` option, else `cancel`. **More** → a list of every option, with `allow_always` labelled "(always)" |
| `question` | Title + detail. One chip per option. **Cancel** at the bottom |
| `unknown` | `raw_tail` in monospace, plus a single **Cancel** button and a hint "Answer this on the computer" |

- **Every tap sends** `expected_seq = agent.state_change_seq` and `fingerprint = prompt.fingerprint`.
- **While the request is in flight,** disable the buttons.
- **On `409` (`stale_state`, `prompt_changed`, `unknown_option`),** show a short confirmation ("Changed — refreshed") and wait for the SSE update. **Never auto-retry an answer.**

**Dictation:**
1. Reuse the existing speech-input flow (`MicrophoneIcon` / `RemoteInput`).
2. Show the target label **before** sending ("To: bizum").
3. Send `prompt(paneId, text, expectedSeq)`.
4. On `agent_busy` / `agent_blocked`, show the reason.

### 5. Notifications (`MyFirebaseMessagingService.kt`, `NotificationActionReceiver.kt`)

**`onNewToken`:** save it to `fcm_token`. If paired and different from `fcm_registered_token`, call `registerPush` (via `WorkManager` or a coroutine) and store `fcm_registered_token` on success. Also register right after pairing.

**`onMessageReceived`:** read the data keys from `contracts.md` §4.1, all strings.

- **Notification id:** `pane_id.hashCode()`, so a pane has at most one notification and a new one replaces the old. Digest notifications use a fixed id.
- **Channels:**
  - `agent_blocked`: `IMPORTANCE_HIGH`, vibration;
  - `agent_done`: `IMPORTANCE_DEFAULT`.
- **Tap:** opens `MainActivity` with an extra `pane_id` → navigate to `agent/{paneId}`.
- **Actions for `blocked`:**
  - **Allow**, only if `allow_option_id` is not empty: broadcast `ACTION_ANSWER` with extras `pane_id`, `option_id`, `state_change_seq`, `fingerprint`.
  - **Deny:** if `deny_option_id` is set, `ACTION_ANSWER` with that option; else `ACTION_CANCEL` with `pane_id` and `state_change_seq`.
  - **Open:** open the app on that agent.
- **Action for `done`:** **Reply** (`RemoteInput`) → broadcast `ACTION_PROMPT` with `pane_id`, `state_change_seq` and the text.
- **`PendingIntent` request codes** must be unique per pane **and** action (e.g. `pane_id.hashCode() * 10 + actionIndex`). Otherwise extras from different panes overwrite each other.

**`NotificationActionReceiver`:**
- Use `goAsync()`, and run the call on a coroutine with a 10 s timeout.
- **On success:** replace the notification with a silent one ("Approved", "Denied", "Sent") that auto-cancels after 3 s.
- **On `409`:** replace it with "Changed — open the app" plus an Open action.
- **On network error:** "Could not reach the relay".

### 6. Complication and tile

**`AgentStatusComplicationService`:**
- `GET /v1/agents` with the token.
- Show the most severe status: `SHORT_TEXT` = a count + icon (e.g. `2 ⚠` when 2 are blocked, `✓` when all are idle/done, `—` when the host is offline).
- Tap opens the app.
- Keep it under 1 network call and no JSON beyond the snapshot.
- On any failure, show `—`.

**Tile + `QuickDictateActivity`:**
- The dictation target follows the plan's §11:
  1. the `pinned_pane_id`, if it is still in `GET /v1/agents`;
  2. otherwise the agent with status `done` and the most recent `updated_at`;
  3. otherwise the agent with `focused == true`.
- The tile shows the target label.
- `QuickDictateActivity`:
  1. fetches `/v1/agents`;
  2. resolves the target;
  3. shows "To: <label>";
  4. runs speech input;
  5. sends `prompt` with the fresh `state_change_seq`.

### 7. Build, install on the Pixel Watch 2, logs

```bash
cd wearos-app
# JDK 17: Android Studio's bundled JBR works:
export JAVA_HOME="/Applications/Android Studio.app/Contents/jbr/Contents/Home"
./gradlew :app:testDebugUnitTest :app:assembleDebug
```

**Wireless debugging on the watch** (the owner does the pairing once):
1. Watch: Settings → Developer options → Wireless debugging → **Pair new device**.
2. Mac: `adb pair <ip>:<pair-port> <code>`, then `adb connect <ip>:<port>`.
3. `./gradlew :app:installDebug`
4. `adb logcat -s AgentWatch:V FCM:V OkHttp:V AndroidRuntime:E`

`google-services.json` is required for FCM and is git-ignored. The owner has it in `wearos-app/app/`. Never commit it.

### 8. Manifest and network security

- Remove `android:usesCleartextTraffic="true"`: production is HTTPS only.
- To test against a local relay over HTTP, add **debug-only** `src/debug/res/xml/network_security_config.xml`, allowing cleartext for `10.0.2.2` and the Mac's LAN IP, and reference it from `src/debug/AndroidManifest.xml`. Release builds must not allow cleartext.

---

## Definition of done (verified on the Pixel Watch 2)

- [ ] `./gradlew :app:testDebugUnitTest` passes, including `ContractsTest` against `pkg/model/testdata/agent_state.json`.
- [ ] `./gradlew :app:assembleRelease` succeeds (R8 on) and the release build still parses JSON. This proves the `@Keep` annotations.
- [ ] `git grep -nE 'local_ip|tailscale|8420|usesCleartextTraffic="true"' wearos-app/app/src/main` returns nothing.
- [ ] On the watch, against the real or local relay:
  - [ ] pairing works;
  - [ ] the list shows the owner's agents live;
  - [ ] a sandbox agent blocking shows the real command;
  - [ ] Allow and Deny work from the app **and** from the notification;
  - [ ] dictation goes to the pinned agent;
  - [ ] history opens with markdown;
  - [ ] the complication and the tile update.
- [ ] `docs/STATUS.md` Phase 4 ticked, with a short note of what was checked on the device.
- [ ] Commit only `wearos-app/**` (never `google-services.json`) and `docs/STATUS.md`.

---

## Pitfalls

- **`PendingIntent` extras overwritten** between notifications → approving the wrong pane. Use unique request codes.
- **R8 strips Gson models** without `@Keep` → every field is null in release builds only.
- **SSE left open in the background** drains the battery. Tie it to the process lifecycle.
- **Sending `expected_seq` from a stale screen:** always use the value from the `AgentState` rendered at tap time, never one cached elsewhere.

---

## Prompt for the executing agent

```
You are executing Phase 4 (Wear OS client) of the Agent Watch refactor in /Users/me/Code/personal/agent-watch-herdr.
Read AGENTS.md, .claude/rules/wearos.md, docs/reference/contracts.md (§1, §2, §4.1), HERDR_REFACTOR_PLAN.md §11 and §13.1,
and docs/phases/4-wearos.md. Rewrite wearos-app/ as the guide specifies: models mirroring pkg/model with @Keep, RelayClient +
RelayRepository over /v1 with bearer tokens, pairing screen, agent list/detail, PromptCard, notifications with
answer/cancel/prompt actions carrying pane_id + state_change_seq + fingerprint, complication and tile following the
target-agent rule. Never send raw keys and remove all LAN/Tailscale IP code. Run `./gradlew :app:testDebugUnitTest
:app:assembleDebug` and report the output. Device checks on the Pixel Watch 2 are done with the owner; list exactly what
he should verify. Tick Phase 4 in docs/STATUS.md and commit only wearos-app/** (never google-services.json) and docs/STATUS.md.
```
