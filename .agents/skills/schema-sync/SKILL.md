---
name: schema-sync
description: "Change a JSON contract (agent state, prompt, history, API bodies, SSE events, wire messages, push payload) consistently across docs/reference/contracts.md, pkg/model (Go) and the Android/Wear OS Kotlin models. Use for any field addition/rename, new endpoint body, or when the pre-commit hook reports contracts out of sync."
---

# Keep the contract copies identical

| # | Copy | Path |
|---|---|---|
| 1 | Source of truth | `docs/reference/contracts.md` |
| 2 | Go | `pkg/model/{agent,api,wire}.go` + golden `pkg/model/testdata/agent_state.json` |
| 3 | Kotlin (`:core`) | `android/core/src/main/java/com/gabriel/agentwatch/model/*.kt` |

*(Note: `watchos-app/` is stopped/legacy and no longer required by the pre-commit hook.)*

## Rules
- **Additive only** once a client is deployed: new fields are optional (`omitempty` / nullable / default value). Never rename or remove; add the new field, migrate, then drop the old one in a later release.
- **Status and role enums are closed sets.** `AgentStatus` is herdr-core's enum; do not add values.
- **JSON keys are snake_case in every language.** Kotlin uses the snake_case property names with `@Keep`.

## Steps
1. **Edit `contracts.md`:** the struct, the field table and the example JSON.
2. **Edit the Go structs.** If §1.2 changed, update `pkg/model/testdata/agent_state.json` to match its example, and the struct literal of `TestGoldenAgentState` (`pkg/model/model_test.go`).
3. **Fill the value where it is produced.** An `AgentState` field is built in `buildAgentState` (`pkg/bridge/engine.go`).
4. **Run `go test ./pkg/model/... ./pkg/bridge/... ./pkg/herdr/...`.** The golden test must pass.
5. **Update the Kotlin model.** Add `@Keep` if the class is new, and an assert for the new field in `ContractsTest` (why: `android/ARCHITECTURE.md` §6). Then run:

   ```bash
   cd android && ./gradlew :core:testDebugUnitTest --tests '*ContractsTest*'
   ```

6. **Grep for the old field name** in `cmd pkg android`. Nothing may still use it.
7. **Commit all copies together** in one commit, with explicit paths.
8. **Ship it.** The relay stores agents as `model.AgentState`, so a deployed relay drops the new field until it runs the new code. Bump the changed components per `VERSIONS`, deploy the relay (`relay-deploy` skill), restart the bridge (`make restart`), and publish/install the Android app.

## If a client cannot be updated right now
Say so to the owner explicitly and add a line under **Open items** in `docs/STATUS.md`. If the change touches no JSON field, commit with `AW_CONTRACT_NO_JSON_CHANGE=1` and say so in the message.
