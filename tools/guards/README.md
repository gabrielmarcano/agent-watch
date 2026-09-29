# Repo guards

One Python script (`guards.py`, standard library only) holds every safety rule. Four thin adapters call it (three agent tools plus git), so every agent tool is covered the same way.

| Adapter | File | Covers |
|---|---|---|
| Antigravity CLI | `.agents/hooks.json` | `PreToolUse` (every tool): shell commands and file writes. `Stop`: contract drift |
| OpenCode | `.opencode/plugins/agent-watch-guards.js` | `tool.execute.before` (bash + edit/write/patch). `tool.execute.after`: gofmt on edited Go files |
| Claude Code | `.claude/settings.json` | `PreToolUse` on `Bash`, `Write`, `Edit`, `MultiEdit`, `NotebookEdit` (`guards.py claude-pretool`; exit 2 blocks and the reason reaches the agent) |
| git | `.githooks/pre-commit` | Secrets, gofmt, contract sync — for **any** tool and for humans |

## What is blocked

| Rule | Why |
|---|---|
| `herdr` input/pane commands whose target is outside the `aw-sandbox` workspace | The dev Mac runs the owner's real agent sessions. The target is resolved live with `herdr pane get` + `herdr workspace list` |
| Renaming a workspace to `aw-sandbox`, or a tab into `aw-session-*` (CLI or raw socket) | Either would fake the checks above and open the owner's panes |
| `herdr server stop`, `integration install/uninstall`, closing a workspace or a tab outside `aw-sandbox` | Owner-only: closing ends the owner's agent sessions in it (a tab's workspace is resolved live with `herdr tab get`) |
| Raw socket scripts using mutating herdr methods | They would bypass the sandbox check |
| `git add -A` / `.`, `commit --amend`, `commit --no-verify`, `push` without `AW_OWNER_APPROVED_PUSH=1` | Shared worktree; the owner decides when to publish |
| Writes to `bridge/`, `claude-plugin/`, `.claude-plugin/`, `google-services.json`, `firebase-service-account*.json`, `.env`, `agent-watch.env` | Legacy code must not return; secrets are placed by hand (`agent-watch.env` by `make config`; its `.example` stays writable) |
| Commits with secrets, unformatted Go, or `pkg/model` changes without `contracts.md` + both client models | Permanent leaks; schema drift. Escape hatch for helper-only changes: `AW_CONTRACT_NO_JSON_CHANGE=1` |

## Launching a new agent session (the one exception)

Input to panes outside `aw-sandbox` is blocked, but an agent may start a **new** session for the owner:

```bash
herdr tab create --workspace <ws> --label aw-session-<name> --cwd "$PWD" --no-focus   # → root pane id
herdr pane run <that-pane> 'claude "<the whole task, including where to read the context>"'
```

`pane run` is allowed only when the pane's tab label starts with `aw-session-`, the pane has **no agent** yet, and only a **shell** is in its foreground (checked live, fail closed). Once the agent runs there, the pane is closed to further input (`agent prompt`, `send-*`, another `pane run`): the task must go in the launch command, and the owner talks to the new session.

## One-time setup per machine / clone

```bash
git config core.hooksPath .githooks        # enables the pre-commit hook (repo-local setting)
```

- **Antigravity CLI:** loads `.agents/hooks.json` once you **trust** the workspace folder. Check with `/hooks` inside agy.
- **OpenCode:** auto-discovers `.opencode/plugins/*.js` on start.
- **Claude Code:** loads the project `.claude/settings.json` hooks on start (sessions already running may need a restart). Check with `/hooks`.
- **Requirements:** `python3` on `PATH`, and `gofmt` for the format check (skipped if missing).

## Tests

```bash
bash tools/guards/test_guards.sh
```

The script covers the core rules, the agy, OpenCode and Claude Code input formats, the session-launch exception (with a fake herdr) and the pre-commit hook in a throw-away repo. herdr is used read-only.

## Verify inside each tool (once, after setup)

Ask the agent to run `git add -A` in this repo. It must be refused with a `BLOCKED:` message that mentions the shared worktree. Nothing gets staged, so the test is harmless.
