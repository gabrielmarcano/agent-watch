# Agent Watch — Implementation Guides

These guides turn [`HERDR_REFACTOR_PLAN.md`](../HERDR_REFACTOR_PLAN.md) into executable work. They are written so that **any** coding agent can pick one up and finish it without guessing. That includes smaller models without the design context.

**Order of authority:**
1. The plan (design decisions).
2. `docs/reference/*` (exact facts and shapes).
3. `docs/phases/*` (how to build each part).
4. The code.

When two disagree, the higher one wins, and the lower one must be fixed in the same commit.

---

## How to use these guides (for agents)

1. **Read [`AGENTS.md`](../AGENTS.md).** The rules there apply to every task.
2. **Open [`STATUS.md`](STATUS.md).** Pick a phase whose dependencies are all ticked and that nobody has claimed. Write your claim line in it and commit it first.
3. **Read the phase guide top to bottom before writing code.** Each one lists what to read, the exact files, the steps, the tests, the Definition of Done, the pitfalls, and a copy-paste prompt.
4. **Stay inside the phase's "Touches" list.** If you need to change something outside it, stop and write it in `STATUS.md` under "Blocked / questions".
5. **Finish with the Definition of Done:**
   - run every command it lists;
   - paste the output in your report;
   - tick the boxes in `STATUS.md`;
   - commit only the listed paths.
6. **Never:**
   - send keys or prompts to herdr panes you did not create in the `aw-sandbox` workspace;
   - commit secrets;
   - use `git add -A`;
   - use `git commit --amend`;
   - `git push` unless the owner asked.

---

## Reference (facts, not tasks)

| Doc | What it holds |
|---|---|
| [`reference/contracts.md`](reference/contracts.md) | Every JSON shape: agent state, prompts, history, HTTP API, SSE, WebSocket wire protocol, push payloads, config |
| [`reference/herdr-socket-api.md`](reference/herdr-socket-api.md) | The herdr socket, verified on 0.9.1: transport, methods, events, key grammar, errors, **safety rules** |
| [`reference/agents.md`](reference/agents.md) | Per-agent knowledge (claude, agy, opencode, generic): menus, keys, transcripts |

---

## Phases

| Phase | Guide | Depends on | Output |
|---|---|---|---|
| 0 | [Capture agent fixtures](phases/0-fixtures.md) | — | Real screens + transcripts in `pkg/agents/testdata`, no 🔍 left |
| 1 | [Foundation](phases/1-foundation.md) | — | Go module, `pkg/model`, `pkg/herdrtest`, legacy removed |
| 2a | [herdr client](phases/2a-herdr-client.md) | 1 | `pkg/herdr` |
| 2b | [Agent adapters](phases/2b-agent-adapters.md) | 1, 0 | `pkg/agents` |
| 2c | [Bridge daemon](phases/2c-bridge-daemon.md) | 2a, 2b | `agent-watch-bridge`, launchd, herdr plugin |
| 3a | [Relay server](phases/3a-relay-server.md) | 1 | `agent-watch-relay` |
| 3b | [Push](phases/3b-push.md) | 3a | FCM + ntfy |
| 3c | [Relay deploy](phases/3c-relay-deploy.md) | 3a | Relay live on the VPS behind Cloudflare |
| 4 | [Wear OS client](phases/4-wearos.md) | 1 | App on the Pixel Watch 2 |
| 5 | [End-to-end + docs](phases/5-e2e.md) | 2c, 3b, 3c, 4 | Release gate, README |
| 6 | [watchOS client](phases/6-watchos.md) | 5 | Simulator-verified app (best-effort) |

```
 0 ───────────────┐
 1 ──┬── 2a ──┐   ▼
     │        ├── 2b ── 2c ──┐
     ├── 3a ──┬── 3b ────────┤
     │        └── 3c ────────┤
     └── 4 ──────────────────┴── 5 ── 6
```

### What can run in parallel, and why

| Together | OK? | Reason |
|---|---|---|
| 0 and 1 | ✅ | 0 only writes `pkg/agents/testdata` and docs; 1 never touches those |
| 1 and anything else | ❌ | 1 creates the module root and deletes legacy directories: it touches most of the git index |
| 2a, 2b, 3a, 4 | ✅ | Disjoint directories; they all share only `pkg/model`, which is frozen after 1 |
| 2b and 3a/3b at the same time | ⚠️ | Both may edit `go.mod`/`go.sum`. Commit one before the other runs `go get`, or resolve the conflict carefully. Never overwrite the other's lines |
| 3b and 3c | ✅ | 3c only writes `deploy/relay` |
| 5 and anything | ❌ | 5 is the release gate; it tests the whole system |
| 6 and 4 | ⚠️ | Technically disjoint, but 6 copies a UX that 4 must validate first |

**Changing a contract after Phase 1** is a cross-cutting change:
- Stop the parallel work.
- Update `reference/contracts.md`, `pkg/model`, `wearos-app` and `watchos-app` in one commit (`schema-sync` skill).
- Then resume.

---

## Tooling for agents in this repo

| Tool | Where | For |
|---|---|---|
| Rules (auto-loaded by path in Claude Code) | `.claude/rules/*.md` | Conventions for Go backend, herdr client, adapters, relay security, Wear OS, watchOS |
| Skills | `.claude/skills/*/SKILL.md` | `capture-fixture`, `add-agent-adapter`, `schema-sync`, `wearos-deploy`, `relay-deploy`, `herdr-probe` |
| Hooks | `.claude/hooks/` + `.claude/settings.json` | Block live herdr input and unsafe git; gofmt after edits; contract-sync reminders; secret scan before commits |

Agents that are not Claude Code (Antigravity, OpenCode, Codex…) should read the relevant `.claude/rules/*.md` and `SKILL.md` files as plain documents: they are written tool-agnostically.
