---
trigger: always_on
description: "Safety and protocol rules for talking to herdr (live owner sessions)"
---

# herdr integration rules

> Applies to: pkg/herdr, pkg/herdrtest, pkg/bridge, cmd/bridge, herdr-plugin.toml, and ANY herdr command you run.

Full reference: `docs/reference/herdr-socket-api.md` (verified on herdr 0.9.1, protocol 22).

## Safety (non-negotiable)
- The herdr on this Mac runs the **owner's real agent sessions**. Never call `agent.prompt`, `agent.send_keys`, `pane.send_*`, `agent.start` or `pane.close` against a pane you did not create in the `aw-sandbox` workspace. The guards (`tools/guards/guards.py`, wired into agy, OpenCode and Claude Code) enforce this for the CLI, block raw socket writes, and refuse renaming a workspace to `aw-sandbox` or a tab into `aw-session-*`.
- To start a new agent session for the owner, create a tab labelled `aw-session-<name>` and `herdr pane run` the agent with its whole task once (`tools/guards/README.md`); after that the pane is closed to input.
- Tests never touch the real socket: use `pkg/herdrtest`.

## Protocol facts that break code when forgotten
- **One request per connection.** Dial, write one line, read one line, close. Only `events.subscribe` stays open.
- **The request `id` is a string.**
- **`agent.read` source is `recent_unwrapped`** (underscore) on the socket. The CLI spelling `recent-unwrapped` fails.
- **`pane.agent_status_changed` needs a `pane_id`** per subscription. Re-subscribe when the set of agent panes changes.
- **Events only trigger a debounced `agent.list`.** The list is authoritative; never build state from event payloads.
- **Key agents by `pane_id`**, never by session id. Trust `agent_session` only when `agent_session.agent == agent`.
- **Use `state_change_seq`** as the change counter (not `revision`).
- **An ack from `send_keys` / `prompt` means herdr took the bytes**, not that the TUI acted.

## Command safety (bridge)
- The watch never supplies keys. Keys come only from `pkg/agents` for a parsed option id.
- Right before any key press:
  1. re-list the agent;
  2. check `expected_seq`;
  3. re-read the screen;
  4. re-parse it and compare the fingerprint;
  5. if the adapter implements `FocusGuard` (OpenCode) and the keys depend on focus, re-read with `format: "ansi"` and verify the focused button (`CheckFocus`).

  On mismatch, send nothing.
- `agent.prompt` is never sent to `blocked` agents, nor while the adapter parses a menu on the screen (herdr 0.9.1 reports some open dialogs as `done`/`working`). Answers go through `send_keys`.
- `cancel` keys come from the menu on screen right now, never from a cached prompt.

## Plugin and service
- The plugin actions are one-shot. The long-running process is `agent-watch-bridge run` under launchd (`com.gabrielmarcano.agent-watch-bridge`).
- launchd provides no `HERDR_*` env or `PATH`: pass everything through the plist or `config.toml`.
- `[[build]]` does not run on `herdr plugin link`: run `make bridge` first.
