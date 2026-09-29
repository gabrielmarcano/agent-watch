# Temporary herdr detection overrides (agy, claude)

> **Temporary.** Remove these overrides as soon as herdr detects the dialogs itself. `check` tells you when.

herdr is the source of truth for agent status: the bridge only publishes a prompt, and the watch can only answer it, when herdr says `blocked`. herdr 0.9.1 misses some open permission dialogs:

| Agent | Missed dialogs | Cause |
|---|---|---|
| `agy` | Every permission dialog of Antigravity CLI 1.2.x (reported `done`/`idle`, or `working` with a background task) | The upstream rule expects wording agy 1.2.x no longer shows |
| `claude` | Any permission dialog after Claude was relaunched in the same pane (reported `idle`) | A stale input box from the previous run is still on screen and wins |

Hooks can't fix it: herdr 0.9.1 ignores the state that the claude and agy integrations report (reserved / identity-only sources). herdr does support **local manifest overrides**, which always win over its remote manifests. Details and draft upstream issues: [`UPSTREAM-ISSUES.md`](UPSTREAM-ISSUES.md).

## How it works

- `<agent>.rule.toml` holds the one extra rule. Nothing from herdr is copied into this repo.
- `install` builds `~/.config/herdr/agent-detection/<agent>.toml` from herdr's **cached remote manifest** (`~/.local/state/herdr/agent-detection/remote/<agent>.toml`), bumps its version with a `.900` suffix, appends the rule and a marker line with the base version, then runs `herdr server reload-agent-manifests`.
- Re-running `install` rebases the override on whatever upstream manifest herdr has now, so new upstream rules are not lost.
- An invalid override is ignored by herdr with a warning (it falls back to the remote manifest).

## Commands

```bash
tools/herdr-overrides/herdr-overrides.sh verify              # fixtures with the overrides (no server change)
tools/herdr-overrides/herdr-overrides.sh verify --upstream   # fixtures with upstream rules only
tools/herdr-overrides/herdr-overrides.sh install             # verify, write, reload herdr
tools/herdr-overrides/herdr-overrides.sh check               # still needed? rebase needed?
tools/herdr-overrides/herdr-overrides.sh uninstall           # remove our files, reload herdr
```

All commands take optional agent names (`agy`, `claude`). `install` refuses to overwrite an override it did not write.

**Who may run them:** `install` and `uninstall` write `~/.config/herdr/agent-detection/` and reload the owner's live herdr. Agents may run them when their task needs it, and say so in their report (`AGENTS.md` §3); `verify` and `check` change nothing.

## Fixtures

`verify` runs `herdr agent explain --file` over real captures: the dialogs and no-dialog screens in `pkg/agents/testdata/{agy,claude}/`, plus the live cases in `testdata/` here (agy 1.2.11 dialogs; Claude dialogs with and without a stale input box; an idle Claude with a typed `❯` prompt). Run `verify` (and `verify --upstream`) for the current numbers.

## When to remove

- **After herdr updates its manifests** (`herdr server update-agent-manifests`, or a herdr upgrade), run `check`. The override hides newer upstream rules until you act:
  - `upstream manifest moved … re-run install` → re-run `install` to rebase, or uninstall.
  - `upstream alone now detects every fixture` → `uninstall <agent>`: upstream fixed it.
- The claude override is the riskier one to keep: Claude's upstream manifest changes often.
- Also remove them if a herdr upgrade makes `verify` fail.
