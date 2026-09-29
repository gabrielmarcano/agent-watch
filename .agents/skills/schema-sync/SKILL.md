---
name: schema-sync
description: "Change a JSON contract (agent state, prompt, history, API bodies, SSE events, wire messages, push payload) consistently across docs/reference/contracts.md, pkg/model (Go), the Wear OS Kotlin models and the watchOS Swift models. Use for any field addition/rename, new endpoint body, or when the pre-commit hook reports contracts out of sync."
---

# Keep the four copies of the contracts identical

| # | Copy | Path |
|---|---|---|
| 1 | Source of truth | `docs/reference/contracts.md` |
| 2 | Go | `pkg/model/{agent,api,wire}.go` + golden `pkg/model/testdata/agent_state.json` |
| 3 | Kotlin | `wearos-app/app/src/main/java/com/gabriel/agentwatch/model/*.kt` |
| 4 | Swift | `watchos-app/AgentWatch/Models/*.swift` |

## Rules
- **Additive only** once a client is deployed: new fields are optional (`omitempty` / nullable / default value). Never rename or remove; add the new field, migrate, then drop the old one in a later release.
- **Status and role enums are closed sets.** `AgentStatus` is herdr's enum; do not add values.
- **JSON keys are snake_case in every language.** Kotlin uses the snake_case property names with `@Keep`. Swift uses `CodingKeys` or `.convertFromSnakeCase`, whichever the project already uses.

## Steps
1. **Edit `contracts.md`:** the struct, the field table and the example JSON.
2. **Edit the Go structs.** If §1.2 changed, update `pkg/model/testdata/agent_state.json` to match its example, and the struct literal of `TestGoldenAgentState` (`pkg/model/model_test.go`).
3. **Fill the value where it is produced.** An `AgentState` field is built in `buildAgentState` (`pkg/bridge/engine.go`). If it comes from herdr, add it to `AgentInfo` (`pkg/herdr/types.go`) and to `agentDiffers` (`pkg/herdr/sync.go`), or its changes are never republished.
4. **Run `go test ./pkg/model/... ./pkg/bridge/... ./pkg/herdr/...`.** The golden test must pass.
5. **Update the Kotlin model.** Add `@Keep` if the class is new, and an assert for the new field in `ContractsTest` (it checks fields one by one, so a missing field passes silently). Then run:

   ```bash
   cd wearos-app && ./gradlew :app:testDebugUnitTest --tests '*ContractsTest*'
   ```

6. **Update the Swift model.** Until Phase 6 the app is the legacy client (`docs/STATUS.md`), but the pre-commit hook still wants a staged change under `watchos-app/AgentWatch/Models/`: append the new field or type to the legacy models, as was done for `CancelRequest` and `PromptOption`. If the parity test target exists, run:

   ```bash
   cd watchos-app && xcodegen generate && xcodebuild -project AgentWatch.xcodeproj -scheme AgentWatch -destination 'platform=watchOS Simulator,name=<sim>' test
   ```

7. **Grep for the old field name** in `cmd pkg wearos-app watchos-app`. Nothing may still use it.
8. **Commit all copies together** in one commit, with explicit paths.

## If a client cannot be updated right now
Say so to the owner explicitly and add a line under **Open items** in `docs/STATUS.md`. If the change touches no JSON field, commit with `AW_CONTRACT_NO_JSON_CHANGE=1` and say so in the message.
