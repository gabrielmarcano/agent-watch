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

**Deployed** (2026-10-01): the relay at `0.4.1` (commit `46ece7c`); the bridge at `0.4.1` (commit `f6479dd`) on the owner's Mac; the Wear OS app `1.2.1` (release build) on the owner's Pixel Watch 2; the menu bar `0.3.1`, built from `main` into `bin/`. The component versions are in `VERSIONS`.

**What can run in parallel:**
- **5 runs alone:** it tests the whole system.
- **6 and 7** start after 5 and can run together (disjoint code directories), **except 7's step 1**, which moves `wearos-app/`: nothing else may touch the Android tree while it runs. Both edit `docs/GUIDE.md`, `contracts.md`, this file and the `schema-sync` skill: commit those by path, one at a time.
- **A contract change** is cross-cutting: stop parallel work and follow the `schema-sync` skill.

---

## Open items

### Phase 5 (release gate)
- Run the whole of [`phases/5-e2e.md`](phases/5-e2e.md) (every row and the security checks). No row has a valid result yet. Row 23 (`AW_PUSH_RESOLVED=1`) is unblocked: the watch runs the app that handles `resolved`.

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
- **agy 1.2.17 menus:** `Create file` and `Question` parse without a `detail`; picking `Write-in...` leaves a text field the watch cannot answer (`docs/reference/agents.md` §4.1).

### Checks nobody has done yet
- ntfy delivery (watchOS push) has never been tested.
- The watch over LTE or its phone's Bluetooth connection: only Wi-Fi has been checked.
- `herdr plugin install gabrielmarcano/agent-watch` end to end.
- **herdr 0.9.3 against a live 0.9.3 server:** on 2026-10-05 the owner's server was still 0.9.1 (it changes version only when restarted, the owner's call). Once it runs 0.9.3, re-probe (`herdr-probe` skill) `ping`, the new `completion_seq`/`title` fields and, from 0.9.2's changelog, the `events_lost` error on a slow subscription (the bridge skips error lines on the stream today; its polling covers missed events) and error responses keeping the request id.
- The first `make deploy-relay ARGS=--sync-env` on the VPS: the merge is covered by `tools/config/test_awenv.sh` (BSD awk locally, Linux awk in CI) but has never run against the real server file. The owner's `agent-watch.env` was filled from the running deployment, so it must list `AW_HOST_TOKEN` as **unchanged**; `changed` means the file's token is not the server's.

---

## Blocked / waiting on upstream

- **herdr misses Claude's dialogs after a relaunch in the same pane:** mitigated by the temporary override in [`tools/herdr-overrides/`](../tools/herdr-overrides/README.md), which explains the cause. Last check (`capture-fixture` skill, §0), 2026-10-05: still needed for claude; agy's override was uninstalled that day (upstream fixed it). The upstream issue is drafted (`tools/herdr-overrides/UPSTREAM-ISSUES.md`) and, by the owner's decision, not being filed for now.

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
