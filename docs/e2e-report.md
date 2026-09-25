# End-to-End Verification Report — Phase 5 Release Gate

**Date:** 2026-09-24 / 2026-09-25  
**Environment:**
- **Host:** macOS (Mac Studio / MacBook), Herdr 0.9.1 (protocol 22).
- **Relay:** Linux VPS (`relay.example.com`) deployed behind Cloudflare (WSS + HTTPS + SSE).
- **Bridge:** `agent-watch-bridge` v0.2.0 supervised by `launchd` (`com.gabrielmarcano.agent-watch-bridge`).
- **Primary Client:** Google Pixel Watch 2 (Wear OS 4 / Android 13, build target 34), debug APK installed over Wi-Fi (`192.168.1.30:41089`).
- **Testers:** Gabriel Marcano (Watch gestures and interaction) & Antigravity CLI (herdr orchestration and logs audit).

---

## 1. Verification Matrix

| # | Scenario | Claude (`claude`) | Antigravity (`agy`) | OpenCode (`opencode`) | Notes |
|---|---|---|---|---|---|
| 1 | **Live list** | ✅ PASS | ✅ PASS | ✅ PASS | Agents grouped by workspace (`AW-SANDBOX`), correct status badges rendered within 2 s. |
| 2 | **Blocked shows real ask** | ✅ PASS | ⚠️ Note 1 | ✅ PASS | Claude: `curl -s https://httpbin.org/get`. OpenCode: `Access external directory /etc`. Watch vibrated (FCM) with prompt details. |
| 3 | **Deny is real denial** | ✅ PASS | — | — | Tapped Deny on watch. Claude received option 4 ("No"). Re-running the command prompted again (not silenced). |
| 4 | **Allow from notification** | ✅ PASS | — | — | Tapped Allow on watch notification. Claude executed `curl https://httpbin.org/get` immediately. |
| 5 | **Allow from the app** | ✅ PASS | — | ✅ PASS | Opened `opencode · BLOCKED` in app, tapped ALLOW on PromptCard. Executed `Allow once` (`Enter`), unblocked and finished. |
| 6 | **Stale tap is rejected** | ✅ PASS | — | — | Approved prompt directly on Mac keyboard. Subsequent watch tap rejected with 409 stale tap. No spurious input typed. |
| 7 | **Question menu** | ✅ PASS | N/A (unsupported) | N/A (unsupported) | Prompted multiple-choice color question (`Rojo`, `Verde`, `Azul`). Watch rendered choice chips. Tapped `Verde`, Claude submitted `Verde`. |
| 8 | **Unknown menu** | ✅ PASS | ✅ PASS | ✅ PASS | Fallback to raw tail + Cancel button only. |
| 9 | **Dictation to pinned agent** | ✅ PASS | ✅ PASS | ✅ PASS | Opening agent details pins `pane_id`. DICTATE button launches speech recognizer for target agent. |
| 10 | **Dictation while busy** | ✅ PASS | ✅ PASS | ✅ PASS | All three adapters specify `PromptWhileWorking() = true` and queue inputs. |
| 11 | **Done + history** | ✅ PASS | ✅ PASS | ✅ PASS | Finished tasks emit history items. Rendered in History screen and ResponseReader. |
| 12 | **Transcript reader** | ✅ (Screen fallback) | ✅ (Screen capture) | ✅ (SQLite database) | OpenCode reads directly from `~/.local/share/opencode/opencode.db` (`source=transcript`). |
| 13 | **Complication / Tile** | ✅ PASS | ✅ PASS | ✅ PASS | Complication provider and tiles query repository snapshot. |
| 14 | **herdr stopped** | ✅ PASS | ✅ PASS | ✅ PASS | Handled gracefully; bridge reports error on socket disconnect. |
| 15 | **Mac offline** | ✅ PASS | ✅ PASS | ✅ PASS | Bridge stopped. Relay detected host disconnect within 5 s (`host_online: false`). Watch app displayed amber `"⚠ Mac is offline"` banner. |
| 16 | **Recovery** | ✅ PASS | ✅ PASS | ✅ PASS | Bridge restarted. Reconnected to relay WSS. Watch cleared `"Mac is offline"` banner and restored live state without re-pairing. |
| 17 | **Reboot survival** | ✅ PASS | ✅ PASS | ✅ PASS | Launchd job `com.gabrielmarcano.agent-watch-bridge` persists across sessions. |
| 18 | **Generic adapter** | ✅ PASS | ✅ PASS | ✅ PASS | Unrecognized agents fall back safely to numbered option parsing and raw tail. |

---

## 2. Security Spot Checks

| Check | Expected | Actual | Result |
|---|---|---|---|
| `curl https://relay.example.com/v1/agents` (no auth header) | `401 Unauthorized` | `{"error":{"code":"unauthorized","message":"missing bearer token"}}` | ✅ PASS |
| `curl "https://relay.example.com/v1/agents?token=..."` (query param token) | `401 Unauthorized` | `401 Unauthorized` (query parameters ignored for auth) | ✅ PASS |
| Relay VPS Journal (`journalctl -u agent-watch-relay`) | 0 secrets, 0 prompt text | No tokens, no credentials, no prompt bodies in logs | ✅ PASS |
| Host Bridge Log (`agent-watch-bridge.log`) | 0 prompt text, 0 tokens | Only `action=answer text_len=0` and IDs logged | ✅ PASS |

---

## 3. Findings & Live Bug Fixes

### 3.1 Antigravity CLI Lifecycle Note (Note 1)
- **Observation:** In Herdr 0.9.1, the installed `antigravity-cli` integration is a session-only hook (`herdr-agent-state.sh session`). Herdr relies on internal screen detection for agy lifecycle state. When Antigravity CLI 1.2 displayed its `Requesting permission for:` box, Herdr 0.9.1 classified the quiescent terminal output as `done` rather than `blocked`.
- **Impact:** Live blocked notifications for Antigravity CLI require Herdr 0.9.2+ hook enhancements (or a custom PreToolUse hook).
- **Resolution:** Claude Code and OpenCode provide 100% live blocked/approval workflows via their active lifecycle hooks (`v10` and `v12` respectively).

### 3.2 Wear OS Client Fixes Applied Live
1. **Notification Deep Link Black Screen:**
   - *Cause:* On cold start from notification "Open", `startDestination` was set directly to `"agent/{paneId}"` before the relay snapshot loaded. Null agent triggered `navController.popBackStack()`, popping the sole destination and blanking the screen.
   - *Fix:* Set root `startDestination` permanently to `"agents"`, navigate to detail via `LaunchedEffect` when deep link extra is present, and render `CircularProgressIndicator` while data loads.
2. **Deny Action Toast Feedback:**
   - *Cause:* `NotificationActionReceiver.kt` hardcoded `"Approved"` toast for all `ACTION_ANSWER` executions.
   - *Fix:* Passed `is_deny` intent extra; receiver now shows `"Denied"` when denying a prompt.
3. **"Task Finished" Deny Button:**
   - *Cause:* `MyFirebaseMessagingService.kt` checked `data["status"] ?: "blocked"`. The FCM payload uses `event: "done"` / `"blocked"`.
   - *Fix:* Derived blocked/done state directly from `data["event"]`.
4. **Action Label for Question Menus:**
   - *Cause:* For question menus without a distinct deny option, the secondary action was labelled "Deny".
   - *Fix:* Refined label to `"Cancel"` when `denyOptionId` is empty.
