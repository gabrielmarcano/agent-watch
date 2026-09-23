# Repo guards

One Python script (`guards.py`, standard library only) holds every safety rule. Three thin adapters call it, so every agent tool is covered the same way.

| Adapter | File | Covers |
|---|---|---|
| Antigravity CLI | `.agents/hooks.json` | `PreToolUse` (every tool): shell commands and file writes. `Stop`: contract drift |
| OpenCode | `.opencode/plugins/agent-watch-guards.js` | `tool.execute.before` (bash + edit/write/patch). `tool.execute.after`: gofmt on edited Go files |
| git | `.githooks/pre-commit` | Secrets, gofmt, contract sync — for **any** tool and for humans |

## What is blocked

| Rule | Why |
|---|---|
| `herdr` input/pane commands whose target is outside the `aw-sandbox` workspace | The dev Mac runs the owner's real agent sessions. The target is resolved live with `herdr pane get` + `herdr workspace list` |
| `herdr server stop`, `integration install/uninstall`, closing a non-sandbox workspace | Owner-only |
| Raw socket scripts using mutating herdr methods | They would bypass the sandbox check |
| `git add -A` / `.`, `commit --amend`, `commit --no-verify`, `push` without `AW_OWNER_APPROVED_PUSH=1` | Shared worktree; the owner decides when to publish |
| Writes to `bridge/`, `claude-plugin/`, `.claude-plugin/`, `google-services.json`, `firebase-service-account*.json`, `.env` | Legacy code must not return; secrets are placed by hand |
| Commits with secrets, unformatted Go, or `pkg/model` changes without `contracts.md` + both client models | Permanent leaks; schema drift. Escape hatch for helper-only changes: `AW_CONTRACT_NO_JSON_CHANGE=1` |

## One-time setup per machine / clone

```bash
git config core.hooksPath .githooks        # enables the pre-commit hook (repo-local setting)
```

- **Antigravity CLI:** loads `.agents/hooks.json` once you **trust** the workspace folder. Check with `/hooks` inside agy.
- **OpenCode:** auto-discovers `.opencode/plugins/*.js` on start.
- **Requirements:** `python3` on `PATH`, and `gofmt` for the format check (skipped if missing).

## Tests

```bash
bash tools/guards/test_guards.sh
```

The script covers the core rules, the agy and OpenCode input formats, and the pre-commit hook in a throw-away repo. herdr is used read-only.

## Verify inside each tool (once, after setup)

Ask the agent to run `git add -A` in this repo. It must be refused with a `BLOCKED:` message that mentions the shared worktree. Nothing gets staged, so the test is harmless.
