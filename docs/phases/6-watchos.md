# Phase 6 — watchOS Client (best-effort, simulator only)

> **Goal:** bring the SwiftUI app to the same `/v1` API, copying the UX already validated on Wear OS. There is **no physical Apple Watch**, so verification happens in the Xcode watchOS simulator, and results must be reported as "verified in the simulator".

| | |
|---|---|
| **Depends on** | Phase 5 (the Wear OS UX is validated). Model + network sync (steps 1–3) may start earlier if a contract change forces it |
| **Parallel with** | nothing required; it runs last |
| **Touches** | `watchos-app/**`, `docs/STATUS.md` |
| **Never blocks** | a release. If time runs out, steps 1–3 alone (it compiles and shows the agent list) are an acceptable stopping point |

---

## Read first

- [`docs/reference/contracts.md`](../reference/contracts.md) §1, §2.
- [`docs/phases/4-wearos.md`](4-wearos.md): the behaviour to copy (PromptCard rules, target-agent rule, error handling).
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

---

## Steps

### 1. Models (`Models/Contracts.swift`)

`Codable`, `Identifiable`, `Sendable`, snake_case via `CodingKeys`, or `decoder.keyDecodingStrategy = .convertFromSnakeCase` with camelCase properties. **Pick one and use it everywhere.**

```swift
struct PromptOption: Codable, Identifiable, Sendable, Hashable { let id: String; let label: String; let role: String }
struct PendingPrompt: Codable, Sendable, Hashable {
    let kind: String; let title: String; let detail: String?
    let options: [PromptOption]; let fingerprint: String; let rawTail: String?
}
struct AgentState: Codable, Identifiable, Sendable, Hashable {
    var id: String { paneId }
    let paneId: String; let agent: String; let label: String; let cwd: String?
    let workspaceId: String; let status: String; let focused: Bool
    let stateChangeSeq: UInt64; let prompt: PendingPrompt?; let updatedAt: String
}
struct HistoryItem: Codable, Identifiable, Sendable, Hashable {
    let id: String; let paneId: String; let agent: String; let label: String
    let query: String?; let response: String; let source: String; let completedAt: String
}
struct AgentsSnapshot: Codable, Sendable { let hostOnline: Bool; let herdrOnline: Bool; let agents: [AgentState]; let generatedAt: String }
// + request/response bodies and ErrorResponse, mirroring contracts.md §2.2
```

**Contract parity check:** add a unit-test target in `project.yml` (`AgentWatchTests`, type `bundle.unit-test`, platform watchOS). Its test decodes `pkg/model/testdata/agent_state.json`, located with `URL(fileURLWithPath: #filePath)` walking up to the repo root, and asserts every field.

### 2. Networking

**`RelayClient`:** `async` functions mirroring the Wear OS `RelayClient`.
- `URLSession.shared`.
- `Authorization: Bearer` header on every request.
- Pane ids percent-encoded with `.urlPathAllowed` minus `:`.

**`RelayStore`:** `@MainActor final class RelayStore: ObservableObject`, with `@Published` `agents`, `hostOnline`, `herdrOnline`, `history`, `connection`.
- SSE with `URLSession.bytes(for:)` and `for try await line in bytes.lines`. Parse the `event:` / `data:` pairs, separated by a blank line.
- Reconnect with backoff 1 s → 30 s.
- Apply the same event rules as Wear OS (§3 of the Phase 4 guide).
- Keep the SSE open only while the scene is `.active` (`@Environment(\.scenePhase)`).

**Storage:** `@AppStorage` for `relayURL` and `pinnedPaneId`. **Keychain** for `deviceToken`, with a small `KeychainHelper` using `SecItemAdd`/`SecItemCopyMatching`.

### 3. Views (copy the Wear OS UX)

- `NavigationStack`:
  - **`AgentListView`:** List sorted by severity, status color dot, ⚠ when blocked, offline banners.
  - **`AgentDetailView`:** status, `PromptCardView`, Dictate, History. Opening it sets `pinnedPaneId`.
- **`PromptCardView`:** the same rules as the Wear OS `PromptCard` (permission / question / unknown).
- **Dictation:** `TextField` with dictation (watchOS offers the dictation input automatically). Show "To: <label>" before sending.
- **History and reader:** markdown via `AttributedString(markdown:)` for `source == "transcript"`, plain `Text` otherwise.
- **No push code:** ntfy on the iPhone handles alerts. Remove APNs registration code if any remains.

### 4. Build and run in the simulator

```bash
cd watchos-app
xcodegen generate
xcrun simctl list devices available | grep -i watch            # pick a watch simulator
xcodebuild -project AgentWatch.xcodeproj -scheme AgentWatch \
  -destination 'platform=watchOS Simulator,name=Apple Watch Series 9 (45mm)' build
xcodebuild -project AgentWatch.xcodeproj -scheme AgentWatch \
  -destination 'platform=watchOS Simulator,name=Apple Watch Series 9 (45mm)' test
```

Replace the simulator name with one from the `simctl` list. To check the UI, open the project in Xcode and run it on the simulator against the real relay (HTTPS works from the simulator).

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
- [ ] `docs/STATUS.md` Phase 6 ticked with "verified in simulator" and any gaps listed.
- [ ] Commit only `watchos-app/**` and `docs/STATUS.md`.

---

## Prompt for the executing agent

```
You are executing Phase 6 (watchOS client, best-effort) of the Agent Watch refactor in this repository.
Read AGENTS.md, .agents/rules/watchos.md, docs/reference/contracts.md (§1, §2), docs/phases/4-wearos.md (the UX to copy) and
docs/phases/6-watchos.md. Rewrite watchos-app/ as specified, regenerate the project with xcodegen, and verify with
xcodebuild build/test on a watchOS simulator. There is no physical Apple Watch: report results as "verified in simulator"
and never claim device behaviour. Tick Phase 6 in docs/STATUS.md and commit only watchos-app/** and docs/STATUS.md.
```
