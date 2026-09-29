# Status

The one place for the project's state: what is done, what is open, what comes next. Other docs link here instead of restating it. History (how each phase went, fixes, deploys) lives in git: `git log -- <path>`.

**Workflow for agents:** before starting a phase, put your name and the date in its **Claimed by** cell and commit that change alone. When you finish, set its state, remove the items you closed from **Open items**, and add what you could not do. **Then delete the phase's guide:** anything in it still worth knowing moves first to its home (`AGENTS.md` §0), and the rest stays in git history.

---

## Phases

| Phase | State | Claimed by | Where it lives now |
|---|---|---|---|
| 0 Agent fixtures | done | — | `pkg/agents/testdata/`, the `capture-fixture` skill |
| 1 Foundation | done | — | `pkg/model`, `pkg/herdrtest` |
| 2a herdr client | done | — | `pkg/herdr`, [`reference/herdr-socket-api.md`](reference/herdr-socket-api.md) |
| 2b Agent adapters | done | — | `pkg/agents`, [`reference/agents.md`](reference/agents.md) |
| 2c Bridge daemon | done | — | `cmd/bridge`, `pkg/bridge` |
| 3a Relay server | done | — | `cmd/relay`, `pkg/relay`, [`reference/contracts.md`](reference/contracts.md) |
| 3b Push | done (ntfy never tested) | — | `pkg/push` |
| 3c Relay deploy | done | — | [`deploy/relay/README.md`](../deploy/relay/README.md) |
| 4 Wear OS client | done | — | [`wearos-app/ARCHITECTURE.md`](../wearos-app/ARCHITECTURE.md) |
| 4b Wear OS UI redesign | done: checked by the owner on the Pixel Watch 2, 2026-09-26 | — | [`wearos-app/ARCHITECTURE.md`](../wearos-app/ARCHITECTURE.md) |
| **5 End-to-end (release gate)** | **open** | — | [`phases/5-e2e.md`](phases/5-e2e.md) |
| 6 watchOS (best-effort) | not started; after 5 | — | [`phases/6-watchos.md`](phases/6-watchos.md) |
| 7 Android phone client | not started; after 5 | — | [`phases/7-android-mobile.md`](phases/7-android-mobile.md) |
| macOS menu bar app | done; verified only by `make bar-test` | — | [`macos-bar/README.md`](../macos-bar/README.md) |
| Shared config (`agent-watch.env`) | done | — | [`GUIDE.md`](GUIDE.md) § Setup, `agent-watch.env.example` |
| Versions, CI, releases | done | — | [`GUIDE.md`](GUIDE.md) § Versions and Releases |

**Deployed:** relay, bridge and menu bar from release `v2026.09.26`; the Wear OS app of that release on the owner's Pixel Watch 2. The component versions are in `VERSIONS`.

**What can run in parallel:**
- **5 runs alone:** it tests the whole system.
- **6 and 7** start after 5 and can run together (disjoint directories), **except 7's step 1**, which moves `wearos-app/`: nothing else may touch the Android tree while it runs.
- **A contract change** is cross-cutting: stop parallel work and follow the `schema-sync` skill.

---

## Open items

### Phase 5 (release gate)
- Run the whole checklist of [`phases/5-e2e.md`](phases/5-e2e.md) (rows 1–27) for claude, agy and opencode, on the Pixel Watch 2 with a release build, recording the results in the guide's tables.
  - The first run (2026-09-24/25, alpha UI) asserted several rows from the code. What it really observed: for claude, Allow from the notification, Deny (a repeated request prompted again), a stale tap rejected with nothing typed, and the question picker; for opencode, Allow from the app; the offline banner and recovery without re-pairing; the 401 checks and clean logs (no tokens or prompt text).
  - Nothing changed by the 2026-09-25 review batch (bridge, relay, push, Wear OS data layer) is verified end to end yet.
- Security spot checks again, plus the trusted-proxy and closed-port checks.
- `AW_PUSH_RESOLVED=1` on the relay (row 23). Unblocked: the watch runs the app that handles `resolved`.

### Code
- **Menu bar:** its "not configured" hint suggests `configure --host-token <64 hex>` (`macos-bar/BarLogic.swift`), which shows the token in `ps`. It should point to `make configure-bridge`.
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

### Checks nobody has done yet
- ntfy delivery (watchOS push) has never been tested.
- The menu bar app on the owner's Mac (Phase 5 row 27).
- The first `make deploy-relay ARGS=--sync-env` on the VPS: the env merge is POSIX awk, tested with BSD awk only.

### Owner decisions
- `herdr-plugin.toml` sets `min_herdr_version = "0.9.0"`, but the docs say herdr ≥ 0.9.1 (the version everything was verified on).
- The sample values in `contracts.md` §1.2, the golden `pkg/model/testdata/agent_state.json` and the model tests use the name of a real project (`bizum`). Replace them with a neutral name?

---

## Blocked / waiting on upstream

- **herdr misses some dialogs** (agy's, and Claude's after a relaunch): mitigated by the temporary overrides in [`tools/herdr-overrides/`](../tools/herdr-overrides/README.md), which explains the cause. Run its `check` after every herdr update; last check, 2026-09-29: still needed. The upstream issues are drafted but not filed.
- **watchOS contracts:** only `CancelRequest` is mirrored in the legacy Swift models until Phase 6.

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
