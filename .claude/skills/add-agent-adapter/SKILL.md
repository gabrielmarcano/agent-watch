---
name: add-agent-adapter
description: "Add or change support for a coding agent (claude, agy, opencode, codex, pi, …) in pkg/agents: prompt-menu parsing, key mapping, cancel keys, prompt-while-working, and transcript-based history. Use when an agent's approval menu is misparsed, a new agent must be first-class, or its transcript format changed."
---

# Add or change an agent adapter

Read `docs/reference/agents.md` (spec) and `.claude/rules/agent-adapters.md` first.

## Decide: adapter or generic?

- **Numbered menu + `esc` + no transcript needed** → no adapter. The generic one already works. Add a fixture under `testdata/<agent>/` to prove it, and stop there.
- **Different keys, menu layout, queueing behaviour, or history wanted** → write an adapter.

## Steps

1. **Fixtures first.** Run the `capture-fixture` skill for the agent. You need at least:
   - `permission-bash.txt` + `.golden.json`
   - `no-menu-working.txt`
   - a transcript sample + `transcript.expected.json`
2. **Find herdr's id for the agent.** Use the `agent` field in `herdr agent list`, or the ids listed in `docs/reference/herdr-socket-api.md` §3.1.
3. **Create `pkg/agents/<agent>.go`:**

   ```go
   type <agent>Adapter struct{ genericAdapter; cfg Config }

   func (a <agent>Adapter) Name() string { return "<herdr id>" }
   // Override only what differs from generic:
   // func (a <agent>Adapter) ParsePrompt(screen string) (Prompt, bool)
   // func (a <agent>Adapter) CancelKeys() []string
   // func (a <agent>Adapter) PromptWhileWorking() bool
   // func (a <agent>Adapter) LastTurn(ctx context.Context, ref SessionRef) (*model.HistoryItem, error)
   ```

4. **Register it** in `NewRegistry`.
5. **Tests:**
   - The golden fixture test picks up the new `testdata/<agent>/` files automatically.
   - Add transcript tests: happy path, tool-call turn skipped, missing file → `ErrNoTranscript`, truncation.
6. **Document it:** in `docs/reference/agents.md`, add a section for the agent with ✅ facts, following the claude/agy/opencode sections.
7. **Verify:**

   ```bash
   go test -race ./pkg/agents/...
   go vet ./...
   CGO_ENABLED=0 go build ./...
   ```

## Never
- Map roles by option position.
- Return an Allow option when nothing was parsed.
- Read a whole transcript file (always tail 256 KB).
- Write to an agent's files or database.
