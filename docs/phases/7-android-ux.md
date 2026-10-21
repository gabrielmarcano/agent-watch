# Phase 7 — the phone's UI and UX (the redesign)

> **Goal:** the phone app looks and feels like the messaging apps people already use (WhatsApp first, Telegram and Slack for the parts WhatsApp does not do), on the owner's Pixel 8 Pro with the latest Android, and every agent is one conversation you answer from the lock screen.
>
> **How it ships:** the owner lists what feels wrong; each change is a small slice that is built, published with `deploy/relay/publish-android.sh --phone` and checked on the device through the update card. The release key is in place, so the loop is minutes, not days.

| | |
|---|---|
| **Order and parallel work** | [`docs/STATUS.md`](../STATUS.md) |
| **Touches** | `wearos-app/mobile/` (the UI), `wearos-app/mobile/build.gradle.kts` (the toolchain), `wearos-app/app/src/main/res/values/strings.xml` only if a string moves, `wearos-app/ARCHITECTURE.md` §4b (the watch's look is the reference for *information*, not for the phone's palette), `docs/STATUS.md` |
| **Must not** | Change the relay's API or the contracts (no new fields, so no `schema-sync`); change the watch app's UI; send raw keys; change the notification safety rules (approving needs the phone unlocked) |

---

## 0. Principles

1. **A chat is the unit of work.** One agent = one conversation, newest at the bottom, with an unread count. The phone is already a chat client; the redesign makes it feel like one people use all day.
2. **The thumb wins.** Primary actions live at the bottom: the composer, the FAB, the bottom navigation. Nothing important hides in a top-bar overflow.
3. **An approval is a card in the conversation,** never a dialog and never a menu. It states what the agent wants, in the agent's words, with the options as buttons.
4. **Motion and haptics are part of the product.** A tap, a send, an approve and a cancel each feel different, and each is quieter than a notification.
5. **Never fake liveness.** The app shows the agent's real herdr status; there is no "typing…" animation, because the agent's state is known.
6. **The phone is a first-class client,** not a watch companion: it can start sessions, answer prompts, and update itself.

## 1. Toolchain: Android 17 and Material 3 Expressive

What the phone compiles against today, and where it goes:

| | Today | Target | Note |
|---|---|---|---|
| `compileSdk` (`:mobile`) | 36 | **37** | `platforms;android-37.0` is installable from the SDK manager; the watch module stays at 36 |
| `targetSdk` (`:mobile`) | 34 | **36** | 35+ draws the app behind the system bars, so every screen handles the insets (the composer already does; the bars get light icons on the phone's dark look) |
| Compose BOM (`:mobile`) | 2025.09.00 | **2026.03.00** | Material 3 1.4.0 (the expressive line: the motion scheme, the shapes). The newest BOM (2026.09.00) ships Compose 1.12, which needs AGP 9.1; that comes with the AGP change, not before. Compose 1.10 also moved the Material icons out of material3: `:mobile` depends on `material-icons-core` now (the six icons it uses are in that set) |
| AGP | 8.13.2 | 8.13.2, then maybe 9.x | AGP 8.13 warns about `compileSdk 37`; the warning is suppressed with `android.suppressUnsupportedCompileSdk=37` in `wearos-app/gradle.properties`. Moving to AGP 9.x is its own change, and only if the watch module still builds and behaves on the wrist |
| `minSdk` | 30 | 30 | unchanged |

Why not jump straight to AGP 9: the watch app shares the Gradle root, and the watch is the reference client that must not change behaviour. The phone's toolchain is a contained change; the AGP bump is a separate one.

What Android 17 buys, concretely: edge-to-edge by default (the app must own its insets), predictive back on every screen, the newer notification and permission surfaces, and platform versions that match the owner's phone.

## 2. Information architecture

Bottom navigation, like WhatsApp's, with three destinations and one FAB:

```
┌─────────────────────────────────┐
│   Chats      Machines  Settings│   ← bottom navigation bar
│      ⊕                          │   ← FAB: new session (Chats only)
└─────────────────────────────────┘
```

- **Chats** — the conversation list (the current home screen).
- **Machines** — the hosts the relay knows, above all the owner's other machine (section 12).
- **Settings** — new: relay, pairing, notifications, appearance, about.
- **FAB → New session** — a three-step sheet, not a long form.

The update banner stays a card at the top of **Chats** (it is a one-off, and Chats is where the app opens).

## 3. Chats (the list)

| Element | Decision |
|---|---|
| Search | A sticky search field (WhatsApp): filters by agent name, session and the last message. It replaces the "refresh" icon; the list refreshes by itself (SSE) and pull-to-refresh stays as a fallback |
| Filters | One row of chips: **All · Needs you · Working · Done** — a filter instead of the "Needs you" section, so the order stays "most recent first" like a messenger |
| Machine | The host chips stay, but they collapse into one chip ("Machine: All ▾") that opens a sheet, so the filter row stays one line |
| Row | Avatar, name, preview (the prompt when one waits, else the last thing said), time, status dot. The **unread dot becomes a count badge**, WhatsApp-style |
| Order | Most recent first, always. "Needs you" is a filter, and the count of chats waiting is shown on the Chats tab |
| Swipe | Swipe right to mark read (the row springs back; it is never dismissed). The long-press selection mode (mark read, mute, open) waits for the notifications slice: muting a chat needs the per-chat channels |
| Empty | "No agents yet" plus a button that starts a session (WhatsApp's empty state is a call to action, not a dead end) |

## 4. The conversation

WhatsApp's bubble model, adapted to an agent:

- **Bubbles:** your messages on the right in a tinted container, the agent's on the left; the time inside the bubble, small; a long answer keeps the markdown reader (tap to expand) instead of a wall of text.
- **Day separators:** already there; keep.
- **Sent bubbles:** a tick (sent) and a spinner (sending), red with a retry on failure — WhatsApp's behaviour, and the optimistic send already exists (`ChatOutbox`).
- **The top bar:** avatar, name, one line of status ("Working · mac-mini"), and a menu (the session's history, mark read, unpair). Predictive back returns to the list with the row highlighted.
- **The composer:** a rounded field that grows with the text, the send button turning into a mic when the field is empty (voice through the keyboard's own mic — no voice recording of our own), the keyboard's action key sends. Sending plays a short haptic.
- **The approval card:** the prompt as a distinct card in the conversation, with the agent's own question, the options as buttons (the option's label is the button's label — never a position), "More options" for the rest, and Deny/Cancel as the quiet actions. The chosen action is confirmed in place, not with a toast.
- **The agent's info lines** (workspace, folder, screen capture) move to the top of the conversation, small and quiet; they are context, not content.

## 5. New session

A bottom sheet in three steps, the way a messenger asks for a contact:

1. **Machine** — the online hosts.
2. **Agent** — the kinds the bridge allows (claude, agy, opencode).
3. **Folder** — a browsable tree inside the allowed roots; the chosen path is shown as a crumb line.

**Start** sends the session; the sheet closes and the chat opens as soon as the device reports the agent (the behaviour exists; the form changes).

## 6. Settings

WhatsApp's shape, with our content:

| Section | Items |
|---|---|
| **Account** | Relay URL (read-only once paired, editable after unpairing), pair another phone, unpair this phone, device id |
| **Notifications** | Channel per kind (approvals, chats, feedback), per-chat mute, quiet hours, "only notify when away" (the bridge's presence switch, shown read-only) |
| **Appearance** | Theme (system / light / dark), dynamic colour on or off, and the phone's own palette (below) |
| **Data** | Check for updates (the same action the update card uses), the relay's connection state, clear the local cache |
| **About** | Version, the guide's links, the project's licence |

## 7. Notifications

- **One notification per chat**, with the agent's avatar, the last line and the time — the phone already groups them, and the group summary stays for two or more.
- **Approvals** get their own high-importance channel and their own sound, and the action buttons (Allow / Deny / Reply) stay inline. Approving still needs the phone unlocked.
- **A reply from a notification** is one tap in the notification itself (RemoteInput), not "open the app".
- **Per-chat mute** from the chat's menu, like a messenger.
- **Vibration:** a light tick for a reply, a short double pulse for an approval, nothing for a status change. The app's own actions use a different, quieter pattern so a silent notification never competes with a tap.

## 8. Motion and haptics

- **Motion:** the Material 3 motion scheme (spring-based, no fixed durations). The *expressive* scheme (`MotionScheme.expressive()`) is still internal in Material 3 1.4.0, so it arrives with the AGP 9 / Compose 1.12 change; until then the standard springs run. Container transforms for the update card, shared elements between the list and the conversation, a spring on the composer's send button.
- **Haptics:** `TextHandleMove` (the lightest) for a row tap, `Confirm` for Allow, `Reject` for Deny, `LongPress` for the selection mode. Never a haptic for a passive update.
- **Respect the system's animation scale:** Android's "remove animations" accessibility setting must switch the springs off. No custom loops.

## 9. The phone's own palette

The watch keeps its fixed dark palette (its colours carry status meaning on a tiny screen). The phone gets its own theme, in the messenger tradition: a light and a dark scheme, blue as the accent, WhatsApp-like bubble containers, and **dynamic colour** on Android 12+ (the Pixel 8 Pro's wallpaper palette) with a toggle to turn it off. Status colours keep their meaning on top of it: the status word always accompanies the colour, and the colour is never the only signal.

This changes the watch's rule that "other clients copy the watch's look": the watch's look is the reference for *information architecture* (what a row shows, what an approval says), not for the phone's palette. `wearos-app/ARCHITECTURE.md` §4b is amended in the same change.

## 10. What we do not copy from WhatsApp

- No calls, no status/stories, no voice notes: an agent conversation is text, and voice belongs to the keyboard.
- No fake presence: the app shows the agent's real status, never "online".
- No "typing…": herdr's state is the truth, and the app says "Working".
- Nothing that weakens an approval: an approval stays explicit, with the agent's own words in front of the button.

## 11. Slices

Each slice is one commit, one publish, one look on the device.

| # | Slice | What it changes | Visible? |
|---|---|---|---|
| 1 | **Toolchain** | `:mobile` to `compileSdk 37` / `targetSdk 36`, Compose BOM 2026.03.00 (Material 3 1.4.0, the expressive line), edge-to-edge insets, the suppress flag | No (except the insets) |
| 2 | **Theme** | The phone's own light/dark palette, dynamic colour (on by default; the switch lands with the Settings screen), shapes, typography | Yes |
| 3 | **Chats** | Search, filter chips, the machine chip, unread counts, pull-to-refresh, the swipe to mark read, the empty state | Yes |
| 4 | **Conversation** | The time inside the bubble, a tick on a sent message, the top bar with the agent's face, its real state and a menu (mark read, mute, details), the predictive back, the shared face that flies from the row into the chat | Yes |
| 5 | **Composer** | The growing field, the IME action, the send haptic, a failure with a retry | Yes |
| 6 | **Approvals** | The choice confirmed in the card, the haptics (confirm on Allow, reject on Deny) | Yes |
| 7 | **Navigation and settings** | The bottom bar (Chats, Machines, Settings) with the waiting count on the Chats tab; the Settings screen (relay, device, forget, the theme switches, the muted and hidden chats, the update check, the connection) | Yes |
| 8 | **New session** | Three steps: the machine, the agent, the folder | Yes |
| 9 | **Machines** | The hosts the relay knows: their state, their agents, their projects, their chats, a session started there | Yes |
| 10 | **Notifications** | A muted chat stays quiet, the approvals have their own channel (a short double pulse) and the replies a light tick | Yes |
| 11 | **States and polish** | The connection strip in the chat, the avatar's status name for the screen reader, the refresh error | Yes |
| 12 | **The owner's first look (2026-10-21)** | The chat opens at the end at once, the global **history goes away** (every chat has its own), the back gesture works, the long press offers WhatsApp's options (pin, mute, mark as read, hide) and the pinned chats sit on top | Yes |
| 13 | **The WhatsApp audit (2026-10-21)** | The screens were looked at on a running phone (an Android 14 image in an emulator, paired with the relay) and compared with WhatsApp's: the title is a big line at the top (no empty space), the search is a filled pill, the rows are two lines unless the agent is working, the bottom bar floats above the system bar with the selected tab on a pill, the chat's canvas is a step below the bubbles, the composer's field is a filled pill, and the inline code lost its box | Yes |

What the build left out, and why:

- **The send button turning into a mic.** The app cannot ask the keyboard for its own voice input, so a mic button would be a decoration that does nothing: voice stays where it works, in the keyboard.
- **The long-press selection mode.** The swipe marks a chat read; the mode waits for the mute work, which is in the notifications slice.
- **The expressive motion scheme.** Still internal in Material 3 1.4.0: it comes with the AGP 9 / Compose 1.12 change, which touches the watch too.
- **The new session is a full-screen wizard**, not a modal sheet: the sheet would hide the machine list it starts from, and the wizard keeps the three steps obvious.
- **The long-press selection mode** (a multi-select with a bar of actions) is still not there: the sheet
  with the options is, and it is what the owner asked for. A multi-select comes later, if it is asked
  for again.

## 15. What the second pass changed (2026-10-21)

- **The update moved to the settings.** The card with the new build, the check and the install are
  in the settings now, where the version is; the chat list is only the chats.
- **The settings are a messaging app's settings:** rows with an icon, the name, a hint under it and a
  chevron; the sections in the accent colour; the destructive action at the end.
- **The bottom bar** marks the current tab with a filled circle, on a translucent bar with a hairline
  border, the way WhatsApp's Android bar does since 2026.
- **A long answer is a part with "Read more"** under it, and "Read less" to fold it back.
- **The chat opens at the newest message** (the list is reversed) and a **down arrow** appears when
  the owner has scrolled up, to take them back to the latest.
- **Tapping the agent's face** in the chat opens its sheet: where it runs, and the same options the
  long press offers (pin, mute, mark as read or unread, archive).

## 14. What the WhatsApp audit changed (2026-10-21)

The app was run in an emulator (an Android 14 image), paired with the relay, and looked at screen by
screen next to WhatsApp's. What was wrong, and what it is now:

- **The title left a hole.** The large top app bar put "Chats" in the middle of the screen with a
  void above it, and the two nested scaffolds added the status bar inset twice. The title is now a
  big line at the top of the list, and the outer scaffold leaves the insets to the screens.
- **The bottom bar was under the system bar.** It now floats above it, rounded, with the selected tab
  on a pill, the way WhatsApp's Android bar does since 2026.
- **The bubbles vanished into the background.** With the system palette the chat's canvas and the
  incoming bubble were the same colour. The canvas is now a step below the surface.
- **The chat opened in the middle of the last message**, or at its first message when the history was
  already cached. Scrolling to the end by hand was never reliable. The conversation is now a
  **reversed list** (the newest turn is the first item, at the bottom), the way every messaging app
  draws it: the chat opens at the end without any scroll, a long last message is read from its
  bottom, and a new turn appears at the bottom while the owner stays where they are.
- **The rows were three lines tall.** The status line only shows when the agent is working or
  waiting; a quiet chat is two lines, like WhatsApp's.
- **The search and the composer were outlined boxes.** Both are filled pills now.
- **The inline code had a grey box behind every word.** It is monospace and nothing else.
- **The pairing screen showed Markdown backticks.** It says what to run, in plain words.

What was left alone on purpose: the unread badge keeps the app's accent colour instead of WhatsApp's
green, and the filter chips (a messaging app has none) stay, because an agent list needs "Needs you".

## 13. What the owner's first look changed (2026-10-21)

- **The chat opens at the end, at once.** The first jump to the last message is a scroll, not an
  animation: a long conversation used to crawl down to the bottom for seconds. Later turns animate.
- **The global history is gone.** Every chat carries its own history (the relay keeps 100 turns per
  pane), so a separate list of past answers was a second place to look at the same thing. The bottom
  navigation is now Chats, Machines and Settings.
- **The back gesture works.** The predictive back handler now navigates when the gesture *completes*
  (its flow ends) instead of waiting for a progress value that never arrived.
- **A long press on a chat opens WhatsApp's sheet:** pin, mute, mark as read, hide. Pinned chats sit
  on top of the list with a pin mark; a hidden chat leaves the list and comes back from the settings
  (the agent keeps running: hiding is the phone's own reading habit, not relay state).

## 12. Machines: the other herdr hosts

The owner works on two machines: this Mac and an Ubuntu server ("home") that runs herdr too, with its own projects. The relay already knows every host ([`docs/reference/contracts.md`](../reference/contracts.md) §1.6): its id, its name, whether it is connected, and whether it reaches its herdr. The snapshot carries **each host's agents**, and a device can already start a session on any host: `GET /v1/hosts/{host}/sessions/options` gives the agent kinds and the roots that machine allows, `GET /v1/hosts/{host}/folders` browses them, and `POST /v1/hosts/{host}/sessions` starts one.

What is missing is a place in the app to *look* at a machine:

- A **Machines** tab: one card per host — its name, whether it is online, whether its herdr answers, how many agents it runs and how many are waiting for the owner.
- Tapping a machine shows **its** chats (the list, filtered to that host) and **its projects**: the roots the bridge allows there, browsable, so a session starts on the "home" machine in one tap.
- A machine that is offline, or whose herdr is stopped, says so and says what to check on that machine (its bridge, its herdr).

What it does **not** do yet:

- It does not show the remote machine's **live herdr workspaces** (the tabs and sessions herdr holds there): the relay knows the host's agents, not its workspace tree. Reporting the workspaces is a bridge → relay addition, a contract change through the `schema-sync` skill, and it is a later slice.
- The prerequisite is phase 8 step 3: the "home" machine must be registered on the relay (`agent-watch-relay hosts add`) and run the bridge. Until then the app shows it as offline, which is the truth.

## 13. Checks

- The `:mobile` JVM tests and lint stay green (the CI line: `./gradlew :core:testDebugUnitTest :app:testDebugUnitTest :app:lintDebug :app:assembleDebug :mobile:testDebugUnitTest :mobile:lintDebug :mobile:assembleDebug`).
- The watch app is untouched: same application id, same signing key, same behaviour on the wrist.
- The relay's API and the contracts do not change (no `schema-sync`).
- Each slice is checked on the owner's phone: the update card, the screen it touches, and the back gesture.
- The notification rules keep their safety: approving needs the phone unlocked, and the watch must not show a duplicate.
- **Android 16+ notification cooldown:** a user who dismisses several notifications in a day may see the app's notifications paused by the system. Check that the approvals still arrive on the owner's phone after the update to `targetSdk 36`.
