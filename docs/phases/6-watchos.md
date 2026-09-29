# Phase 6 — watchOS Client (best-effort, simulator only)

> **Goal:** bring the SwiftUI app to the same `/v1` API, copying the UX already validated on Wear OS. There is **no physical Apple Watch**, so verification happens in the Xcode watchOS simulator, and results must be reported as "verified in the simulator".

| | |
|---|---|
| **Order and parallel work** | [`docs/STATUS.md`](../STATUS.md). Model + network sync (steps 1–2) may start earlier if a contract change forces it |
| **Touches** | `watchos-app/**`, `.agents/rules/watchos.md`, `docs/STATUS.md`, and every line that calls the watchOS app legacy or "not on the relay API yet": `docs/GUIDE.md`, `SECURITY.md`, `docs/reference/contracts.md`, `Makefile` (`watchos-config` comment), `tools/config/awenv.sh`, `agent-watch.env.example` |
| **Needs the owner** | Pairing a simulator: `agent-watch-bridge pair` prints the code. A simulator paired with the production relay becomes a registered device: give the owner its name so he can revoke it. Sandbox agents to answer: the `capture-fixture` skill, steps 1–2. ntfy: the owner enables it on the relay and checks his iPhone |
| **Never blocks** | a release. If time runs out, steps 1–3 alone (it compiles and shows the agent list) are an acceptable stopping point |

---

## Read first

- [`docs/reference/contracts.md`](../reference/contracts.md) §1, §2.
- The behaviour to copy: [`wearos-app/ARCHITECTURE.md`](../../wearos-app/ARCHITECTURE.md) §3–4 (data-layer rules), §4a (screens), §4b (UX rules, target agent), §5 (notifications); SSE client rules in `contracts.md` §2.3.
- `.agents/rules/watchos.md`.

---

## Current code → what happens to it

| Current file | Action |
|---|---|
| `Models/AgentState.swift` (with `AnyCodable`) | **Rewrite** as `Models/Contracts.swift`; delete `AnyCodable` |
| `Models/HistoryItem.swift` | Merge into `Contracts.swift` with the new fields |
| `Network/AgentNetworkService.swift` | **Rewrite** as `Network/RelayClient.swift` (REST) + `Network/RelayStore.swift` (SSE + state) |
| `Views/ConfigView.swift` | **Replace** with `Views/PairingView.swift` |
| `Views/ContentView.swift` | **Split** into `AgentListView.swift`, `AgentDetailView.swift`, `PromptCardView.swift` |
| `Views/HistoryListView.swift`, `Views/ReaderDetailView.swift` | Adapt to the new `HistoryItem` |
| `Utilities/*` | Keep |
| `project.yml` | `SWIFT_VERSION: 5.9`; `deploymentTarget.watchOS: "10.0"` (needed for `NavigationStack` niceties; confirm the simulator runtime exists) |
| `Config.generated.xcconfig` (git-ignored, from `make watchos-config`) | **Wire it in** as the target's base configuration (`configFiles` in `project.yml`, optional so the project builds without it). It sets `AW_RELAY_URL` (`https://<AW_RELAY_DOMAIN>`) and, when `AW_WATCHOS_BUNDLE_ID` is set, `PRODUCT_BUNDLE_IDENTIFIER`. Expose `AW_RELAY_URL` through an `Info.plist` key as `PairingView`'s default (empty when unset); drop any hard-coded domain. Reference: `contracts.md` §7 |

---

## Steps

### 1. Models (`Models/Contracts.swift`)

Mirror `contracts.md` §1–§2 field by field (`Codable`, `Identifiable`, `Sendable`), nullable where Go uses `omitempty`. Snake_case via `CodingKeys`, or `decoder.keyDecodingStrategy = .convertFromSnakeCase` with camelCase properties: **pick one and use it everywhere.**

**Contract parity check:** add a unit-test target in `project.yml` (`AgentWatchTests`, type `bundle.unit-test`, platform watchOS). Its test decodes `pkg/model/testdata/agent_state.json`, located with `URL(fileURLWithPath: #filePath)` walking up to the repo root, and asserts every field. Add it to the scheme so `xcodebuild … test` runs it: under the `AgentWatch` target, `scheme: { testTargets: [AgentWatchTests] }`.

### 2. Networking

**`RelayClient`:** `async` functions mirroring the Wear OS `RelayClient`.
- `URLSession.shared`.
- Auth as in `contracts.md` §2.
- Pane ids percent-encoded with `.urlPathAllowed` minus `:`.

**`RelayStore`:** `@MainActor final class RelayStore: ObservableObject`, with `@Published` `agents`, `hostOnline`, `herdrOnline`, `history`, `connection`.
- SSE with `URLSession.bytes(for:)` and `for try await line in bytes.lines`. Parse the `event:` / `data:` pairs, separated by a blank line.
- Reconnect and silence rules: `contracts.md` §2.3.
- Apply the same event rules as Wear OS (`wearos-app/ARCHITECTURE.md` §3–4).
- Keep the SSE open only while the scene is `.active` (`@Environment(\.scenePhase)`).

**Storage:** `@AppStorage` for `relayURL` and `pinnedPaneId`. **Keychain** for `deviceToken`, with a small `KeychainHelper` using `SecItemAdd`/`SecItemCopyMatching`.

### 3. Views (copy the Wear OS UX)

The screens and UX rules are in `wearos-app/ARCHITECTURE.md` §4a–§4b (list sections and notice, agent screen, prompt, dictation confirmation, pin to tile, reader). The watchOS mapping:
- `NavigationStack`: `AgentListView` → `AgentDetailView` (with `PromptCardView`), plus history, reader and pairing views.
- **Dictation:** a `TextField` (watchOS offers the dictation input automatically), then the same confirmation before Send.
- **Reader:** markdown via `AttributedString(markdown:)`.
- **No push code:** ntfy on the iPhone handles alerts. Remove APNs registration code if any remains.

### 4. Build and run in the simulator

The commands (XcodeGen, `xcodebuild` build and test on a simulator) are in `.agents/rules/watchos.md` § Build. To check the UI, open the project in Xcode and run it on the simulator against the real relay (HTTPS works from the simulator).

---

## Definition of done

- [ ] `xcodebuild … build` and `… test` succeed (paste the tail of the output).
- [ ] `git grep -nE 'AnyCodable|tailscale|localIp|8420' watchos-app` returns nothing.
- [ ] **In the simulator:**
  - pairing works;
  - the list is live;
  - Allow / Deny / Cancel on a sandbox agent work;
  - dictation reaches the pinned agent;
  - history opens.
- [ ] Push: ntfy checked on the iPhone (the simulator gets none).
- [ ] The lines listed under **Touches** no longer call the app legacy.
- [ ] `docs/STATUS.md` updated per its workflow ("verified in the simulator", gaps listed as open items), and this guide deleted.
- [ ] Commit only the paths listed under **Touches**, plus the deletion of this guide.

---

## Prompt for the executing agent

```
You are executing Phase 6 (watchOS client, best-effort) of the Agent Watch refactor in this repository.
Read AGENTS.md, .agents/rules/watchos.md, docs/reference/contracts.md (§1, §2), wearos-app/ARCHITECTURE.md (the UX to copy) and
docs/phases/6-watchos.md. Rewrite watchos-app/ as specified, regenerate the project with xcodegen, and verify with
xcodebuild build/test on a watchOS simulator. There is no physical Apple Watch: report results as "verified in simulator"
and never claim device behaviour. Update the lines listed under Touches and docs/STATUS.md per its workflow, delete this
guide, and commit only those paths.
```
