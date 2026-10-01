---
trigger: always_on
description: "Safety and protocol rules for talking to herdr (live owner sessions)"
---

# herdr integration rules

> Applies to: pkg/herdr, pkg/herdrtest, pkg/bridge, cmd/bridge, herdr-plugin.toml, and ANY herdr command you run.

Full reference, with the herdr version it was verified on: `docs/reference/herdr-socket-api.md`.

## Safety (non-negotiable)
- The herdr on this Mac runs the **owner's real agent sessions**.
- **Never, outside panes you created in the `aw-sandbox` workspace:** anything that types, starts, closes, moves or renames: `agent.prompt`, `agent.send_keys`, `agent.start`, `pane.send_*`, `pane.run`, `pane.close`, `tab.close` (and their CLI forms).
- **Safe anywhere (read-only):** `ping`, `agent.list`, `agent.get`, `agent.read`, `pane.get`, `pane.list`, `session.snapshot`, `workspace.list`, `tab.list`, `events.subscribe` (listening only), and the CLI's `herdr --version`, `herdr api schema`, `herdr integration status`.
- What the guards enforce, and for which tools: `tools/guards/README.md`. Where they don't reach, these rules still apply.
- To start a new agent session for the owner, create a tab labelled `aw-session-<name>` and `herdr pane run` the agent with its whole task once (`tools/guards/README.md`); after that the pane is closed to input.
- Tests never touch the real socket: use `pkg/herdrtest`.

## Protocol facts that break code when forgotten (details: `herdr-socket-api.md`)
- **One request per connection;** only `events.subscribe` stays open (§1).
- **The request `id` is a string** (§1).
- **`agent.read` source is `recent_unwrapped`** on the socket, not the CLI's `recent-unwrapped` (§2.1).
- **`pane.agent_status_changed` needs a `pane_id`** per subscription: re-subscribe when the agent panes change (§5).
- **Events only trigger a debounced `agent.list`;** the list is authoritative (§5).
- **Key agents by `pane_id`**, never by session id (session ids change when a session restarts, can be missing or stale, and some agents report a path). Trust `agent_session` only when `agent_session.agent == agent` (§3.1).
- **Use `state_change_seq`** as the change counter, not `revision` (§3).
- **An ack from `send_keys` / `prompt` means herdr took the bytes**, not that the TUI acted (§4).

## Command safety (bridge)
- The watch never supplies keys. Keys come only from `pkg/agents` for a parsed option id.
- Right before any key press:
  1. re-list with the command's own `agent.list`, never the shared snapshot (a concurrent, older refresh can overwrite it);
  2. check `expected_seq`;
  3. re-read the screen;
  4. re-parse it and compare the fingerprint;
  5. if the adapter implements `FocusGuard` (OpenCode) and the keys depend on focus, re-read with `format: "ansi"` and verify the focused button (`CheckFocus`).

  On mismatch, send nothing.
- `agent.prompt` is never sent to `blocked` agents, nor while the adapter parses a menu on the screen (herdr can miss an open dialog: `tools/herdr-overrides/README.md`). Answers go through `send_keys`.
- `cancel` keys come from the menu on screen right now, never from a cached prompt. With no parseable menu, `cancel` only cancels a prompt the watch saw as `unknown` (the adapter's default keys); otherwise it returns `prompt_changed`.
- **Each command acts at most once:** commands on one pane run one at a time; the prompt is claimed (pane, seq, fingerprint or prompt-text hash) before sending, because pressing twice is worse than not pressing; recent `request_id`s are remembered. The command's budget starts on arrival (`pkg/bridge/cmdguard.go`, `commands.go`).

## Plugin and service
- launchd provides no `HERDR_*` env or `PATH`: pass everything through the plist or `config.toml`.
- `[[build]]` does not run on `herdr plugin link`: run `make bridge` first.
- herdr runs plugin actions from the plugin root (hence `./bin/…` in the manifest) with `HERDR_SOCKET_PATH`, `HERDR_PLUGIN_CONFIG_DIR` and `HERDR_PLUGIN_STATE_DIR` set; `start` pins them into the service definition, so a later `start` from a terminal keeps them.
