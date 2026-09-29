---
name: add-agent-adapter
description: "Add or change support for a coding agent (claude, agy, opencode, codex, pi, …) in pkg/agents: prompt-menu parsing, key mapping, cancel keys, prompt-while-working, and transcript-based history. Use when an agent's approval menu is misparsed, a new agent must be first-class, or its transcript format changed."
---

# Add or change an agent adapter

Read `docs/reference/agents.md` (spec) and `.agents/rules/agent-adapters.md` first.

## Decide: adapter or generic?

- **Numbered menu + `esc` + no transcript needed** → no adapter. The generic one already works. Add a fixture under `testdata/<agent>/` and the agent's herdr id to the `agents` list of `TestGoldenFixtures` (`pkg/agents/adapters_test.go`) to prove it, and stop there.
- **Different keys, menu layout, queueing behaviour, or history wanted** → write an adapter.

## Steps

0. **The agent's CLI and its herdr integration must be installed** (`herdr integration status`). Both are the owner's to install: ask him.
1. **Fixtures first.** Run the `capture-fixture` skill for the agent. You need at least:
   - `permission-bash.txt` + `.golden.json`
   - `no-menu-working.txt`
   - a transcript sample + `transcript.expected.json`
2. **Find herdr's id for the agent.** Use the `agent` field in `herdr agent list`, or the `kinds:` line of `herdr agent`.
3. **Create `pkg/agents/<agent>.go`,** copying the shape of `agy.go`:

   ```go
   type <agent>Adapter struct {
       *genericAdapter
       cfg Config
   }

   func new<Agent>Adapter(cfg Config) *<agent>Adapter {
       return &<agent>Adapter{genericAdapter: newGenericAdapter(), cfg: cfg}
   }

   func (a *<agent>Adapter) Name() string { return "<herdr id>" }
   // Override only what differs from generic (pointer receivers):
   // ParsePrompt, CancelKeys, PromptWhileWorking, LastTurn (interfaces: pkg/agents/adapter.go)
   // SplitScreenTurn (ScreenTurnReader, pkg/agents/screen.go): without it, screen history is the whole screen
   ```

   If an answer's keys act on whichever button has focus (OpenCode's Enter), also implement `FocusGuard` (`pkg/agents/adapter.go`) and capture an `--format ansi` focus fixture.

4. **Register it** in `NewRegistry` (`pkg/agents/adapter.go`): `"<herdr id>": new<Agent>Adapter(cfg),`.
5. **Tests:**
   - Add the agent's herdr id to the `agents` list of `TestGoldenFixtures` (`pkg/agents/adapters_test.go`); it then runs every `testdata/<agent>/*.txt` that has a `.golden.json`.
   - Add transcript tests: happy path, tool-call turn skipped, missing file → `ErrNoTranscript`, truncation.
6. **Document it:** in `docs/reference/agents.md`, add a section for the agent with ✅ facts, following the claude/agy/opencode sections; add its row to `docs/GUIDE.md` § Supported Agents.
7. **Verify:**

   ```bash
   go test -race ./pkg/agents/...
   go vet ./...
   CGO_ENABLED=0 go build ./...
   ```

## Never
- Map roles by option position.
- Return an Allow option when nothing was parsed.
- Read a whole transcript file. Tail reads stay bounded (`.agents/rules/agent-adapters.md`).
- Write to an agent's files or database.
