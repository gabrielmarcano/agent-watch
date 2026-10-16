# Temporary herdr detection override (claude)

> **Temporary.** Remove the override as soon as herdr detects these screens itself. `check` tells you when.

herdr is the source of truth for agent status: the bridge only publishes a prompt, and the watch can only answer it, when herdr says `blocked`; and its turn-end watch only runs for panes herdr reports `working` (`pkg/bridge/engine.go`, `checkWorkingTurns`). herdr misses three Claude screens:

| Agent | Missed screen | Cause |
|---|---|---|
| `claude` | Any permission dialog after Claude was relaunched in the same pane (reported `idle`) | A stale input box from the previous run is still on screen and wins |
| `claude` | A question dialog (`AskUserQuestion`) or a WebFetch-style permission in a **named** conversation (reported `idle`) | Claude Code 2.1.301 draws the conversation name in a divider under the dialog, and the upstream rules that read only what follows the last horizontal rule then see nothing |
| `claude` | A pane whose turn ended while its background subagents still run (reported `idle`/`done` about ten seconds after the turn end; herdr issue **#5004**) | The upstream `background_agents_working` rule needs `Waiting for N background agents to finish` to be the **last** line above the prompt box; 2.1.301 writes the "waiting on its own background work" event and the turn summary after it, so `live_prompt_box` (950, `idle`) wins |

The third is not a dialog, and it costs more than a missing prompt: with the pane `idle` the bridge's turn watch never runs, so a reply whose turn ended while subagents keep running is not published, and neither is its `background_agents` count.

Hooks can't fix the dialogs: herdr 0.9.5 ignores the state that the claude integration reports (a reserved source). herdr does support **local manifest overrides**, which always win over its remote manifests. Details and the draft upstream issue for the dialogs: [`UPSTREAM-ISSUES.md`](UPSTREAM-ISSUES.md).

## How it works

- `<agent>.rule.toml` holds the extra rules (claude: the dialog rule and the background-agent rule). Nothing from herdr is copied into this repo.
- `install` builds `~/.config/herdr/agent-detection/<agent>.toml` from herdr's **cached remote manifest** (`~/.local/state/herdr/agent-detection/remote/<agent>.toml`), bumps its version with a `.900` suffix, appends the rule and a marker line with the base version, then runs `herdr server reload-agent-manifests`.
- Re-running `install` rebases the override on whatever upstream manifest herdr has now, so new upstream rules are not lost. **The override shadows the newer upstream rules until then**, which is what `check` warns about: the live herdr kept reporting `idle` for every dialog in a named conversation while it was pinned to upstream `2026.09.11.1`.
- The dialog rule reads the **whole visible screen** (`whole_recent`), not what follows the last horizontal rule: the divider Claude draws under a dialog in a named conversation leaves that region empty.
- The background rule reads `after_last_horizontal_rule` (the agent rows sit under the prompt box) at priority 960: above `live_prompt_box` (950), below the blocked rules (980+). Its `not` list is the same fail-closed set upstream puts on `background_mcp_task_working`, so a dialog that shared the frame would still reach the rules at 300-850; on 2.1.301 a dialog replaces the input box and the rows entirely (live capture, 2026-10-16).
- An invalid override is ignored by herdr with a warning (it falls back to the remote manifest).

## Commands

```bash
tools/herdr-overrides/herdr-overrides.sh verify              # fixtures with the overrides (no server change)
tools/herdr-overrides/herdr-overrides.sh verify --upstream   # fixtures with upstream rules only
tools/herdr-overrides/herdr-overrides.sh install             # verify, write, reload herdr
tools/herdr-overrides/herdr-overrides.sh check               # still needed? rebase needed?
tools/herdr-overrides/herdr-overrides.sh uninstall           # remove our files, reload herdr
```

All commands take optional agent names (only `claude` now). `install` refuses to overwrite an override it did not write.

**Who may run them:** `install` and `uninstall` write `~/.config/herdr/agent-detection/` and reload the owner's live herdr. Agents may run them when their task needs it, and say so in their report (`AGENTS.md` §3); `verify` and `check` change nothing.

## Fixtures

`verify` runs `herdr agent explain --file` over real captures: the dialogs and no-dialog screens in `pkg/agents/testdata/claude/`, plus the live cases in `testdata/` here (Claude dialogs with and without a stale input box, with and without a named conversation's divider under them; an idle Claude with a typed `❯` prompt; a background subagent running after its turn ended, and the same pane once it finished; a dialog that opened while a subagent ran). Run `verify` (and `verify --upstream`) for the current numbers.

## When to remove

- **After herdr updates its manifests** (`herdr server update-agent-manifests`, or a herdr upgrade), run `check`. The override hides newer upstream rules until you act:
  - `upstream manifest moved … re-run install` → re-run `install` to rebase, or uninstall.
  - `upstream alone now detects every fixture` → `uninstall <agent>`: upstream fixed it.
- Claude's upstream manifest changes often, so check after every manifest update.
- The background rule goes when upstream fixes **#5004**; until then, upstream alone reads `claude-background-agent-waiting.txt` as `idle` and `check` keeps the override. If only #5004 is fixed, drop the background rule from `claude.rule.toml` and re-run `verify`; if upstream covers the dialogs too, `uninstall`.
- **agy's override was removed on 2026-10-05:** upstream manifest `agy` 2026.10.05.1 detects every agy fixture by itself.
- Also remove them if a herdr upgrade makes `verify` fail.
