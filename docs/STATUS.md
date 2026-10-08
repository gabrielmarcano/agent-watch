# Status

The one place for the project's state: what is done, what is open, what comes next. Other docs link here instead of restating it. History (how each phase went, fixes, deploys) lives in git: `git log -- <path>`.

**Workflow for agents:** before starting a phase, put your name and the date in its **Claimed by** cell and commit that change alone; do the same under **Open items** for other work that spans several commits or sessions (add the item if it is missing). A fix that fits in one commit needs no claim of its own: update **Open items** in that same commit. When you finish, set its state, remove the items you closed from **Open items**, and add what you could not do. **Then delete the phase's guide:** anything in it still worth knowing moves first to its home (`AGENTS.md` §0), and the rest stays in git history.

---

## Phases

**Done:** phases 0–4b (fixtures, foundation, herdr client, agent adapters, bridge, relay, push, relay deploy, Wear OS client and its redesign), the macOS menu bar app (verified only by `make bar-test`), the shared `agent-watch.env`, and versions, CI and releases. Where each part is documented: `AGENTS.md` §0.

| Phase | State | Claimed by |
|---|---|---|
| **[5 End-to-end](phases/5-e2e.md) (release gate)** | **open** | — |
| [6 watchOS](phases/6-watchos.md) (best-effort) | not started; after 5 | — |
| [7 Android phone client](phases/7-android-mobile.md) | not started; after 5 | — |

**Deployed** (2026-10-07): the relay at `0.6.0` (commit `050328a` on `main`); on the owner's Mac, the bridge `0.7.0` (commit `050328a`) and the menu bar `0.6.0` (rebuilt from `main` on 2026-10-07; Only Notify When Away off); the Wear OS app `1.3.1` (release build, version code 12, 2026-10-07; built before the squash merge, from the same code as `050328a`) on the owner's Pixel Watch 2. The component versions are in `VERSIONS`.

**What can run in parallel:**
- **5 runs alone:** it tests the whole system.
- **6 and 7** start after 5 and can run together (disjoint code directories), **except 7's step 1**, which moves `wearos-app/`: nothing else may touch the Android tree while it runs. Both edit `docs/GUIDE.md`, `contracts.md`, this file and the `schema-sync` skill: commit those by path, one at a time.
- **A contract change** is cross-cutting: stop parallel work and follow the `schema-sync` skill.

---

## Open items

### Phase 5 (release gate)
- Run the whole of [`phases/5-e2e.md`](phases/5-e2e.md) (every row and the security checks). No row has a valid result yet. Row 23 (`AW_PUSH_RESOLVED=1`) is unblocked: the watch runs the app that handles `resolved`.

### Quiet pushes while the owner is at the Mac
- Implemented in bridge `0.5.0` and relay `0.5.0` (design: [`phases/quiet-at-mac.md`](phases/quiet-at-mac.md), plan: [`phases/quiet-at-mac-plan.md`](phases/quiet-at-mac-plan.md)), deployed 2026-10-06 (the relay logs `push presence idle=10m0s`; the bridge sends `host_presence` under launchd), not verified on the watch yet; claimed by Claude (2026-10-05). Since bridge `0.6.0` it is opt-in, **Only Notify When Away** (off by default): the menu bar item or `agent-watch-bridge presence on`.
- Left to check on the owner's Mac and watch, with Only Notify When Away turned on: the item's check follows `presence on`/`off`; turning it off sends a held-back prompt at once; the screen-lock key `ioreg` shows while locked (`CGSSessionScreenIsLocked`, or the top-level `IOConsoleLocked`), no buzz while at the Mac, a push about 10 minutes after the last input for a prompt still waiting, a push within 15 s of locking the screen, and an immediate push when away.

### Code debt (for later)

Known and accepted for now; none blocks a phase.

- **`AW_ANDROID_APPLICATION_ID`:** nothing in the app depends on the application id any more. Left to wire:
  - `applicationId` in `wearos-app/app/build.gradle.kts`;
  - the placeholder `google-services.json` in `.github/workflows/wearos.yml`;
  - the `wearos-deploy` skill's `adb` commands;
  - the docs that say the id is fixed.

  Phase 7 expects the key.
- **Focus refusal:** the watch recognises OpenCode's focus refusal by the relay's message text. A dedicated error code would be sturdier.
- **OpenCode focus guard:** a millisecond window remains between the ANSI read and `send_keys`.
- **Claude transcript:** herdr reports only the session id, not the transcript path its hook receives. A turn that ends while the bridge restarts gets the generic `Task finished` body.
- **Wear OS release APK:** signed with the debug key.
- **macOS Background App Activity** (the owner's request, 2026-10-06): menu bar `0.5.0` shows as **Agent Watch** with the Wear OS app's icon (checked by the owner on 2026-10-06, after turning Open at Login off and on). The bridge still shows as `agent-watch-bridge` with the `exec` icon: its LaunchAgent names the app in `AssociatedBundleIdentifiers` (bridge `0.5.1`), but macOS lists it on its own (`sfltool dumpbtm`), probably because the key needs both signed with the same Team ID (there is no Developer ID account). The fix that needs no Team ID is the packaging below (the bridge inside the app).
- **Single-app packaging, modelled on Tailscale's macOS app (the owner's idea, 2026-10-06, for later):** one `Agent Watch.app` that carries the bridge binary and registers it with `SMAppService.agent`, so Settings shows one item. Linux keeps what it has (the same bridge under systemd `--user`, started and stopped with the CLI, as Tailscale does with `tailscaled` and `tailscale up`/`down`); a Linux tray app would be a separate, optional client of the same CLI. Costs found in the code:
  - the bundled plist is fixed, so the `HERDR_*` paths `start` pins today must come from `config.toml` or defaults;
  - one owner of the macOS service (the app, or the CLI and the herdr plugin), never both;
  - ad-hoc signing may ask for approval again after each rebuild (untested);
  - updating the bridge means rebuilding the app.
- **Notifications:** grouping is deferred (one notification per pane plus the digest already bound them).
- **A transcript read that fails at the turn's end** falls back to a screen capture, and that path is slower than the relay's wait for the reply (`push.DefaultReplyWait`), so the done push says `Task finished`.
- **Trial (2026-10-01): reply previews show the start and the end.** The done push and the watch's last-reply card show a long reply's first line, "…" and its end, where the question or next step usually is (175 of 200 stored replies were longer than the push, 149 longer than the card). If the owner does not like it, go back to the start only: `replyPreview` (`pkg/push/push.go`) and `headTailPreview` (`wearos-app/.../ui/logic/TextPreview.kt`).
- **A reply over the cap** (`model.MaxResponseBytes`, 64 KiB since 2026-10-01; no real reply has reached it) keeps its beginning. The owner's idea, if it ever bites: keep the end instead.
- **To watch, not planned:** the screen-capture fallback reads the last 200 lines (`pkg/bridge/engine.go`), never measured; a very long reply on screen could lose its start there. Only the fallback path uses it (6 of 200 stored replies).
- **Tiles and complication:** a 401 from them may not revoke the pairing (`wearos-app/ARCHITECTURE.md` §3).
- **Legacy Swift models** need a touch for every contract change until Phase 6 (`schema-sync` skill, step 6).
- **Relay deploy is amd64-only** (`deploy/relay/README.md` § First-time setup).
- **Codex and Cursor have no guard adapter:** only the pre-commit hook covers them (`AGENTS.md` §5).
- **Tests and CI:**
  - `gofmt -l cmd pkg` in the Makefile and CI misses `deploy/launchd/template.go`;
  - `TestGoldenFixtures` walks a fixed agent list (`pkg/agents/adapters_test.go`): a new agent's fixtures run only once it is added there;
  - the fake herdr delivers global events to a subscription with no event types, which real herdr does not (`pkg/herdrtest/server.go`, `EmitGlobal`);
  - `pkg/relay/state_test.go` and `pkg/bridge/engine_test.go` sleep longer than `go-backend.md` allows.
- **Claude's agents view** (`← for agents`): a dialog raised while the conversation is in the background is not on screen, so herdr says `done` and the watch cannot see or answer it (`docs/reference/agents.md` §3.1). Not fixable from the screen; the owner reopens the conversation.
- **The watch could show an older reply than the Mac** (Claude's agents view): fixed in bridge `0.4.2`, deployed 2026-10-05, not verified on the watch yet. The Claude adapter checks herdr's session against the pane title and finds the shown one in Claude's session index (`docs/reference/agents.md` §3.3, the exception in `AGENTS.md` §1.1); the agents view is never published as a reply, a turn waiting on `AskUserQuestion` publishes nothing, a failed screen read is retried once, and coming back from the agents view captures the shown conversation's last reply. What remains:
  - a conversation reopened from the agents view whose last reply the relay already holds does not come back to the top of the watch's list (the relay drops an item with a known id);
  - when herdr names a session that is in no profile any more (a retired background worker) and the title is not herdr's, the watch gets a screen capture, not the transcript;
  - a turn waiting on a question that herdr reports as `done` publishes nothing, and the watch cannot answer it either (the dialog-detection gap, `tools/herdr-overrides/README.md`).

  The watch side (history re-fetched after a reconnect and on screen start) is in PR #6.
- **Claude turns that end while the pane stays `working`** (background agents running; herdr never reports `done`): bridge `0.4.3` checks working Claude panes every 15 s for a new turn end in the transcript and publishes its reply (`docs/reference/agents.md` §3.4). Deployed (the bridge logs `a turn ended while the pane stays working`, 2026-10-07); not verified on the watch. This periodic check widens `AGENTS.md` §1.1 (it was "on a status transition" only); the owner approved it. Turns that end within the same 15 s window as the next one publish only the last.
  - **Background agents on the watch** (bridge `0.4.4`, relay `0.4.2`, Wear OS `1.2.3`): the same check publishes `background_agents` (`contracts.md` §1.2, `agents.md` §3.4), and the watch shows such an agent as `Done`, with `N agents` on the background line (Wear OS `1.3.1`, `wearos-app/ARCHITECTURE.md` §4b); no push until herdr reports `done` (`contracts.md` §4.3). Verified on the watch (2026-10-07). Known gaps: the count lags up to 15 s (a report turn can show as done for that long), and after a turn interrupted with `esc` the agent shows `Working`. watchOS only got the model field (Phase 6).
- **A pane labelled with the generic terminal title `Claude Code`** (an untitled conversation, no herdr name or tab label) is hard to recognise on the watch.
- **Long single-line texts left** (the short-lines rule, `wearos-app/ARCHITECTURE.md` §4b; the list card and the agent screen's header follow it since Wear OS `1.2.4`):
  - the agents tile's second line chains the status and the agent id as text (`AgentsTileService.kt`), and has no logo;
  - the complication's text chains `<status> · <agent>` and `· +N more` (`complication_status_agent`, `complication_status_more`).
- **agy 1.2.17 menus:** `Create file` and `Question` parse without a `detail`; picking `Write-in...` leaves a text field the watch cannot answer (`docs/reference/agents.md` §4.1).

### Checks nobody has done yet

Only changes that affect what the system does (the owner's rule, 2026-10-07): a text, wording or layout tweak needs no check of its own here.

- Wear OS 1.2.2 on the watch, still unchecked: Quick Dictate and Change with the system input; an empty notification reply sends nothing; after the stream drops (Wi-Fi off and on) or the app returns from the background, the agent screen shows the latest reply. (Verified by the owner on 2026-10-05: Reply with voice and with the keyboard reaches the confirm screen and the agent.)
- Menu bar on the owner's Mac: the switch in the first row starts and stops the bridge. (Checked by the owner: the switch's colour on 2026-10-06; Open at Login and the relay error lines on 2026-10-07.)
- ntfy delivery (watchOS push) has never been tested.
- The watch over LTE: only Wi-Fi and its phone's Bluetooth connection have been checked (Bluetooth by the owner on 2026-10-07).
- `herdr plugin install gabrielmarcano/agent-watch` end to end.
- **`make deploy-relay ARGS=--sync-env` has never run.** Plain deploys run on the VPS all the time; `--sync-env` also rewrites the server's env file (its live secrets) from the owner's `agent-watch.env`, and that merge is covered only by `tools/config/test_awenv.sh` (BSD awk locally, Linux awk in CI). The owner's `agent-watch.env` was filled from the running deployment, so the first run must list `AW_HOST_TOKEN` as **unchanged**; `changed` means the file's token is not the server's.

---

## Blocked / waiting on upstream

- **herdr misses Claude's dialogs after a relaunch in the same pane:** mitigated by the temporary override in [`tools/herdr-overrides/`](../tools/herdr-overrides/README.md), which explains the cause. Last check (`herdr-overrides.sh check`), 2026-10-07 with the herdr 0.9.3 server: still needed for claude (upstream manifest `2026.09.11.1`); agy's override was uninstalled on 2026-10-05 (upstream fixed it). The upstream issue is drafted (`tools/herdr-overrides/UPSTREAM-ISSUES.md`) and, by the owner's decision, not being filed for now.

---

## Next steps

1. **Phase 5**, above.
2. **Drop the claude herdr override** once herdr detects the dialogs by itself (the drafted upstream issue stays unfiled for now).
3. **Phase 7**, the Android phone client.
4. **Phase 6**, the watchOS client.
5. **Later ideas:**
   - a Telegram bot with inline approval buttons, for when the watch is charging;
   - Discord webhook summaries;
   - dedicated adapters for Codex, Pi, Amp and other CLI agents;
   - marking an agent read from the watch. herdr 0.9.1 has no call that marks a pane seen: only focusing it does (`agent.focus`/`pane.focus`), which moves the owner's terminal to that pane. A watch-only read mark (per pane and `state_change_seq`, no herdr call) would avoid that. **Needs the owner's decision**; recommended: the watch-only mark, set automatically when the agent's screen or reply is opened (no button).
