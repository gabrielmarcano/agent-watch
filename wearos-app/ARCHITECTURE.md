# Agent Watch — Wear OS Client Architecture

The Wear OS app is the primary client of the relay's `/v1` API (`docs/reference/contracts.md` §1, §2, §4.1). It never talks to the computer directly and never sends raw keys. Build, install and device checks: `.agents/skills/wearos-deploy/SKILL.md`. Its state (what is verified, what is open): `docs/STATUS.md`.

## 1. Stack

- Kotlin, Jetpack Compose for Wear OS **Material 3** (`androidx.wear.compose.material3`): `AppScaffold`, `ScreenScaffold` + `TransformingLazyColumn` (native rotary, the clock scrolls away), `EdgeButton`, confirmation and alert dialogs.
- Two tiles on ProtoLayout Material 3 (`Material3TileService`); text entry through `androidx.wear:wear-input` (system keyboard, handwriting or voice).
- OkHttp + `okhttp-sse`, Gson (models with `@Keep`, snake_case field names, for R8).
- Firebase Cloud Messaging (data-only messages).
- Versions and SDK levels: `wearos-app/build.gradle.kts` and `app/build.gradle.kts` (its comment says why Material 3 stays on the 1.6 line).

## 2. Packages

| Package | What lives there |
|---|---|
| `model/` | `Contracts.kt`: mirrors `pkg/model` field by field; `severity()`, `resolveTargetAgent()` (latest `done` → focused → none). `AgentKey.kt`: an agent's identity, `AgentKey` (host, pane id; §3a), `findAgent`, `resolveTarget` (pinned → `resolveTargetAgent`), and the host helpers (`hostNameFor`, `allHostsOffline`, `onlineAgents`) |
| `data/` | `Prefs` (`SharedPreferences`: relay URL, device token and id, FCM token, `fcm_registration`, pinned agent); `AgentStore` (merges SSE and refreshes, keyed by `AgentKey`; keeps the relay's `hosts`); `HistoryMerge` (dedup by `id`, newest first, max 200); `FcmRegistration` (which FCM token the relay accepted for which pairing) |
| `network/` | `RelayClient` (stateless `/v1` client) and `RelayHttpClients`; `RelayEngine` (the relay link, no Android); `RelayRepository` (Android façade); `UiState`; `FcmRegistrar` + `PushRegistration`; `MyFirebaseMessagingService`, `AgentNotifications`, `NotificationSupport`, `NotificationActionReceiver`; `SurfaceUpdates` |
| `approval/` | `PromptActions` (primary buttons by role: ALLOW only ever maps to `allow_once`; the answered-prompt lock); `CommandFeedback` (relay error code → short wrist message) |
| `ui/theme/` | Fixed palette (no dynamic colour; every text pair ≥ 6.6:1) and `statusStyle()`: icon, word and colours per herdr status |
| `ui/logic/` | Pure presentation logic with JVM tests: `attentionSections` (list order), `hostPages` and `pageStatus` (the list's pages and each page's notice and dimming, `listStatus`), `headPreview`/`tailPreview`, `ageOf`, relay URL checks |
| `ui/components/` | `ScreenList` (`ScreenScaffold` + `TransformingLazyColumn`), `transformedItem`, age text, the system input intents (`PromptInput`, `TextInput`), `rememberPaneHistory` (one agent's items in the state merged with `GET /v1/history?host=&pane_id=`, fetched each time the screen starts) |
| `ui/screens/` | Agent list, agent screen + `PromptSection`, dictation confirm (`DictationFlow`), full text, history, reader, pairing, settings |
| `complication/` | `complicationContent` (state → count and the agent a tap opens, across online hosts), `complicationBadge` (the short type: glyph and how many need the user, a problem's own glyph and a dash) and the data source: SHORT_TEXT, LONG_TEXT and MONOCHROMATIC_IMAGE |
| `tile/` | Quick Dictate (`tileContent`, `dictationTarget`, `QuickDictateActivity`); Agents (`agentsTile`: the two most urgent agents across online hosts, each opening its screen, and how many more); `TileCommon` (palette, the 3 s fetch, launching into the app with the agent's pane and host) |
| `util/` | `MarkdownFormatter` (plain previews; a table becomes one line per row, `first cell: other cells · …`), `MarkdownBlocks` (the reader's block parser; tables become one record per row, also inside a `screen` capture) |

## 3. Data flow

```
             GET /v1/agents, /v1/history          POST answer / cancel / prompt
 RelayClient ──────────────────────┐        ┌──────────────── (capped: contracts §2.4)
      ▲                            ▼        │
      │ SSE /v1/events      RelayEngine ── AgentStore + HistoryMerge
      │ (silence =               │
      │  reconnect)              ▼
      └──────────────── MutableStateFlow<UiState> ──► RelayRepository.state ──► UI
                                 │
                   hooks ────────┼──► ApprovalNotifications (dismiss stale approvals)
                                 └──► PushRegistration.ensure (on every stream open)
 state collector (RelayRepository.init) ──► SurfaceUpdates (complication / tiles refresh, throttled)

 FCM ──► MyFirebaseMessagingService ──► blocked / done / digest notifications, then SurfaceUpdates
                                   └──► resolved: dismiss the pane's approval
 Notification action ──► NotificationActionReceiver (goAsync, 9 s) ──► RelayClient
```

- **One engine per process.** `RelayRepository.init` builds it and makes it the process-wide 401 listener (`RelayClient.unauthorizedListener`). Quick Dictate, notification actions and push registration call `init` themselves, so their 401s revoke the pairing; the tiles and the complication build a plain `RelayClient` and do not, so their 401 revokes it only if `init` already ran in that process.
- **SSE lifecycle:** started in `MainActivity.onStart`, stopped in `onStop` (battery). Notifications cover the background. Tiles and the complication make one `GET /v1/agents` per update and do no heavy parsing.
- **Network:** HTTPS only; release builds allow no cleartext (the debug build allows a local relay: `src/debug/res/xml/network_security_config.xml`). Host and pane ids are URL-encoded in paths. Commands go to `/v1/hosts/{host}/agents/{pane_id}/…` when the agent has a host, else to the older `/v1/agents/{pane_id}/…` (a relay that predates hosts; `RelayClient.commandPath`).
- **Reconnect:** the backoff and silence rules of `contracts.md` §2.3; only a stream that delivered a `snapshot` resets the backoff. The SSE client's read timeout detects a silent (half-open) stream. `history` events sent while the stream was down are not replayed, so the first `snapshot` after a reconnect re-fetches `GET /v1/history` (the snapshot carries the agents); `start` fetches both on its own.
- **Merging:** a refresh never overrides an agent that SSE touched after the refresh started; within an agent the higher `state_change_seq` wins. History from both sources is merged by `id`.
- **Thread safety:** the engine guards its state with one lock; SSE callbacks arrive on OkHttp threads and callbacks from a replaced stream are ignored (stream generation). Hooks run outside the lock.

## 3a. Several hosts

Every herdr numbers its own panes, so `w1:p1` can exist on every host (contracts §1.2). An agent's identity is `AgentKey` (host, pane id), never the bare pane id:

- **Everywhere:** the store, Compose `key`s (`AgentKey.token`), nav routes, intent extras (`pane_id` and `host`) and URIs, notification ids, request codes and tags, resolved seqs, the pinned target, tile and complication extras, `SentAnswer`, commands and one agent's history.
- **No host** (`""`): a relay that predates hosts. `AgentKey.token` is then the pane id alone, so ids, URIs and stored values stay as they were. A key without a host from an intent, a tile or a pin saved before hosts finds its pane only while one host has it (`findAgent`), never by guessing between two.
- **Naming a host:** only while the relay knows two or more (`hostNameFor`): the list page's header, a line on the agent screen and the dictation confirm, the tiles and the complication. With one host nothing names it. Pushes: the relay's `title` already names it (contracts §4.1); the app shows it as it is.
- **Online:** each host has its own flags (`UiState.hosts`); `hostOnline` and `herdrOnline` are the relay's aggregates (contracts §1.5), used as before when the relay lists no hosts.
- **Tiles and complication:** the most urgent agent across every online host (an offline host's last known agents cannot be answered, so they leave the count); "device offline" only when every host is (`allHostsOffline`).

## 4. What the UI must use

```kotlin
RelayRepository.state: StateFlow<UiState>
data class UiState(connection, hostOnline, herdrOnline, agents, history, stale: Boolean, auth: AuthState, hosts)
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
- **`stale`:** the list is not backed by a live stream (before the first snapshot, reconnecting, stopped): dim it and show the connecting or relay-offline notice (`listStatus`), never the device-offline one.
- **Commands:** use the `state_change_seq` and `fingerprint` of the `AgentState` shown at tap time; disable the buttons while in flight and, after a success, until the agent's state changes (`isAwaitingUpdate`); never auto-retry after a 409.
- **Pairing:** save the token, then `RelayRepository.restart(context)`. Do not call `registerPush` or write `fcmRegisteredToken` (ignored); `FcmRegistrar` registers when the stream opens and records it only after the relay accepts it.
- **How the UI uses it:** `MainActivity` observes `auth` at runtime (anything but `PAIRED` → pairing; `REVOKED` adds the `session_expired` line); each screen collects only its own slice of `state`; every command error goes through `commandErrorFeedback`, including Quick Dictate.

## 4a. Screens

| Route | Screen |
|---|---|
| `pairing` | Relay URL (default `BuildConfig.DEFAULT_RELAY_URL`, edited with the system keyboard) and the 6-digit code |
| `agents` | One page per host in the relay's `hosts` order (`hostPages`), swiped sideways (`HorizontalPagerScaffold`, with a page indicator); each page: the host's name, its own notice line (relay link, then that host's offline or herdr-stopped state: `pageStatus`), only its agents. With 0 or 1 hosts, one page with every agent, no name and no indicator. Each page: sections by attention (`attentionSections`: needs you, done, working, idle, unknown state) across workspaces; each card: status icon, name (2 lines), the agent's logo and the status word, the background line when there is one, the workspace; History and Settings |
| `agent/{host}/{paneId}` | `host` is `_` for no host (a path segment cannot be empty). Name first (the label), then one fact per line: the status, the background line when there is one, the status's age, the agent's logo with the workspace, the agent's own title (workspace and title only when they differ from the label), the host's name with several hosts; the prompt as items (command head or `unknown` tail, View all, Deny · Allow, options with their description, a confirmation for `allow_always`); the last reply with Read all (whole when it fits, else its first line, a "…" line and its end: `headTailPreview`); Reply (system input) as the edge button; "Pin to tile" |
| `dictation/{host}/{paneId}` | The text entered and the target (with its host's name on its own line with several hosts), then Send (Change enters it again) |
| `history?host=&paneId=`, `reader/{historyId}` | Cards with age: across all panes each card is named by its session's title, then the query; one pane's history names its session once, in the header (`History`, then the title on up to 3 lines), and each card by its query (the age when it has none); the answer rendered block by block (`screen` captures as monospace, except their tables, which the bridge sends as markdown and the reader shows as records) |
| `settings` | Pair again, unpair, version |
| `text` | A command or screen tail in full |

## 4b. UX rules

Other clients (watchOS, the phone app) copy these.

- **Target agent** for Quick Dictate and the tile, across every host: the pinned agent (set only with "Pin to tile"; host and pane), else the latest `done`, else herdr's focused pane, else none. Never an arbitrary agent: a dictated prompt must not land in a pane the user did not choose (`resolveTarget` in `model/AgentKey.kt`). The tile passes the agent (pane and host) it showed, and a closed pane gets nothing (`tile/DictationTarget.kt`). The target label is always shown before sending.
- **Prompt input** (Reply, Change, Quick Dictate): Wear's system RemoteInput, where the user picks voice, keyboard or handwriting. Not `ACTION_RECOGNIZE_SPEECH`: it is documented for voice only (`EXTRA_RESULTS`), and a prompt typed from the voice screen's keyboard never reached the relay. RemoteInput has no voice-first option, so voice costs one tap. Never drop input silently (`ui/logic/InputResult.kt`): text in any known form goes to the confirm screen whatever the result code; only backing out is silent; anything else shows a line and a reject haptic. Logs carry the result code and extra keys, never text.
- **Approvals:** Deny · Allow with the positive action on the right (Wear convention), full-height buttons 8 dp apart; every other option sits apart under More options, and `allow_always` asks for confirmation. An `unknown` prompt shows the screen tail, a line asking to answer on the device, and Cancel only (`ui/screens/PromptSection.kt`).
- **Waiting on background agents:** a `working` agent with `background_agents` > 0 (`contracts.md` §1.2) has finished its turn: its row and its screen show the done icon, colour and word, with their count on the background line (`agentStatus`, `ui/logic/AgentSections.kt`). It stays in the Working section, and the complication, tile and notifications keep herdr's `working`: nothing is asked of the user until the agent reports `done`.
- **Background line:** what the agent still runs in the background, on a line of its own under the status word: `2 agents · 1 shell · 1 monitor` (only the kinds above 0; agents only while it waits on them, shells and monitors with any status, `background_shells` and `background_monitors`). Shells and monitors change no status, section or surface: they only say a report will come back to the agent.
- **Short lines (the owner's rule, 2026-10-05):** a line that does not wrap is unreadable once it is long. Use short words, one fact per line, and put extra information on a line of its own instead of chaining it with ` · `. Chain only short facts that always fit, such as a section's title and count (`Done · 2`); a status with its age (`Working · 3 min ago`) already did not fit the agent screen. A name that may be long (workspace, title) gets its own line, cut at its end.
- **Agent type:** a small monochrome logo, tinted like the text beside it and sized in sp (`AgentLogo`; the mapping is `agentBrand` in `ui/logic/AgentBrand.kt`), never the agent id as text; its content description is the agent's name. It leads the status line of a list card (the status icon stays the card's icon), the workspace line of the agent screen, and the age of history cards and the reader. The marks are lobe-icons' monochrome SVGs (MIT, credited in `NOTICE`), like the herdr GUI app (herdr-gpui) shows for the same agents, mapped from herdr's agent ids (`herdr agent`'s `kinds:`); herdr's kinds without a mark and unknown agents get the app's agent glyph (`ic_agent`). Converted with paths unchanged; each drawable names its source file.
- **Readability and feedback:** status is icon + word + colour, never truncated, colour never alone; no text under 12 sp, checked at font scale 1.24; feedback where the finger is (a confirmation dialog plus haptics, errors right above the buttons); no Back buttons (swipe to dismiss).

## 5. Notifications

- One notification per agent (`AgentKey.token.hashCode()`: the pane id's hash without a host, §3a); digests use a fixed id. Channels: `agent_blocked` (high), `agent_done` (default), `agent_watch_feedback` (low, silent action results).
- Every `PendingIntent` carries a data URI unique per agent and action (`agentwatch://notification/<action>/<pane>?host=<host>`, no query without a host), so extras from different agents can never be swapped. Request codes alone are not enough: they can collide across agents.
- `resolved` pushes and live state (SSE/refresh) dismiss approvals that no longer match the agent, matched by host and pane; a late `blocked` is dropped against the last `resolved` seq of its (host, pane). The relay sends `resolved` only with `AW_PUSH_RESOLVED` enabled.
- Buttons (`blockedButtons`): a question's answers (the push's `options`, `contracts.md` §4.1), a permission's Allow and Deny, nothing for an `unknown` prompt, then Open. Cancel is never offered for a question; it appears only for a push without `kind` (sent by relays older than the `kind` key) that has no deny option. A finished agent's notification shows its reply and offers Reply (keyboard or voice → `prompt`); an empty reply replaces it with an error, never a silent drop.
- Action results: a short confirmation per action (auto-dismissed after 3 s), or an error message with an Open action.

## 6. Tests

`./gradlew :app:testDebugUnitTest` (JVM, no device; the tests live in `app/src/test/`). `ContractsTest` parses `pkg/model/testdata/agent_state.json`, but asserts only the fields it names: a new contract field needs its own assert.
