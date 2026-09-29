# Status

The one place for the project's state: what is done, what is open, what comes next. Other docs link here instead of restating it. History (how each phase went, fixes, deploys) lives in git: `git log -- <path>`.

**Workflow for agents:** before starting a phase, put your name and the date in its **Claimed by** cell and commit that change alone; for work outside a phase, add your name and the date to its item under **Open items** (or add the item) the same way. When you finish, set its state, remove the items you closed from **Open items**, and add what you could not do. **Then delete the phase's guide:** anything in it still worth knowing moves first to its home (`AGENTS.md` §0), and the rest stays in git history.

---

## Phases

**Done:** phases 0–4b (fixtures, foundation, herdr client, agent adapters, bridge, relay, push, relay deploy, Wear OS client and its redesign), the macOS menu bar app (verified only by `make bar-test`), the shared `agent-watch.env`, and versions, CI and releases. Where each part is documented: `AGENTS.md` §0.

| Phase | State | Claimed by |
|---|---|---|
| **[5 End-to-end](phases/5-e2e.md) (release gate)** | **open** | — |
| [6 watchOS](phases/6-watchos.md) (best-effort) | not started; after 5 | — |
| [7 Android phone client](phases/7-android-mobile.md) | not started; after 5 | — |

**Deployed:** relay and bridge from release `v2026.09.26`, the Wear OS app of that release on the owner's Pixel Watch 2; the menu bar built from `main` into `bin/` (it runs the new build from its next launch). The component versions are in `VERSIONS`.

**What can run in parallel:**
- **5 runs alone:** it tests the whole system.
- **6 and 7** start after 5 and can run together (disjoint directories), **except 7's step 1**, which moves `wearos-app/`: nothing else may touch the Android tree while it runs.
- **A contract change** is cross-cutting: stop parallel work and follow the `schema-sync` skill.

---

## Open items

### Phase 5 (release gate)
- Run every row of [`phases/5-e2e.md`](phases/5-e2e.md) for claude, agy and opencode, on the Pixel Watch 2 with a release build, recording the results in the guide's tables. The first run (2026-09-24/25) asserted several rows from the code, and later changes to the bridge, relay, push and Wear OS data layer were never checked end to end.
- Security spot checks again, plus the trusted-proxy and closed-port checks.
- `AW_PUSH_RESOLVED=1` on the relay (row 23). Unblocked: the watch runs the app that handles `resolved`.

### Code
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
- **Tiles and complication:** they build their own `RelayClient`, so their 401 revokes the pairing only if `RelayRepository.init` already ran in that process (`network/RelayClient.kt`).
- **Legacy Swift models:** the pre-commit hook requires a staged change under `watchos-app/AgentWatch/Models/` for every `pkg/model` change. Until Phase 6 the practice is to append the new type or field to the legacy models; only `CancelRequest` and `PromptOption` are mirrored so far.
- **Relay deploy is amd64-only:** `make deploy-relay` builds `make relay-linux` (linux/amd64); an arm64 VPS needs the release binary installed by hand.
- **Codex and Cursor have no guard adapter:** only the pre-commit hook covers them (`AGENTS.md` §5).
- **Tests and CI:**
  - `gofmt -l cmd pkg` in the Makefile and CI misses `deploy/launchd/template.go`;
  - `TestGoldenFixtures` walks a fixed agent list (`pkg/agents/adapters_test.go`): a new agent's fixtures run only once it is added there;
  - the fake herdr delivers global events to a subscription with no event types, which real herdr does not (`pkg/herdrtest/server.go`, `EmitGlobal`);
  - `pkg/relay/state_test.go` and `pkg/bridge/engine_test.go` sleep longer than `go-backend.md` allows.

### Checks nobody has done yet
- ntfy delivery (watchOS push) has never been tested.
- The menu bar app on the owner's Mac (Phase 5 row 27).
- The first `make deploy-relay ARGS=--sync-env` on the VPS: the merge is covered by `tools/config/test_awenv.sh` (BSD awk locally, Linux awk in CI) but has never run against the real server file. The owner's `agent-watch.env` was filled from the running deployment, so it must list `AW_HOST_TOKEN` as **unchanged**; `changed` means the file's token is not the server's.

---

## Blocked / waiting on upstream

- **herdr misses some dialogs** (agy's, and Claude's after a relaunch): mitigated by the temporary overrides in [`tools/herdr-overrides/`](../tools/herdr-overrides/README.md), which explains the cause. Run its `check` after every herdr update; last check, 2026-09-29: still needed. The upstream issues are drafted but not filed.

---

## Next steps

1. **Phase 5**, above.
2. **Close the herdr gap upstream:** file the drafted issues, then remove the overrides once herdr detects the dialogs by itself.
3. **Phase 7**, the Android phone client.
4. **Phase 6**, the watchOS client.
5. **Later ideas:**
   - a Telegram bot with inline approval buttons, for when the watch is charging;
   - Discord webhook summaries;
   - dedicated adapters for Codex, Pi, Amp and other CLI agents.
