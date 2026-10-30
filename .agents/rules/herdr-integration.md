---
trigger: always_on
description: "Safety and protocol rules for talking to herdr (live owner sessions)"
---

# herdr-core (`hc`) and Herdr integration rules

> Applies to: pkg/herdr, pkg/herdrtest, pkg/bridge, cmd/bridge, and ANY `hc` / `herdr-core` / `herdr-app` / `herdr` command you run.

Full reference, with the version it was verified on: `docs/reference/herdr-socket-api.md`.

## Safety (non-negotiable)
- The `herdr-core` (`hc`) and `Herdr.app` (`herdr-app`) on this Mac run the **owner's real agent sessions**.
- **Never, on the owner's terminals or window:** anything that types, interrupts, kills, removes, re-labels or closes:
  - `hc`: `send`, `keys`, `interrupt`, `kill`, `rm`, `keep`, `label`, `put`, `attach` (without `--observe`), `server stop`, `server restart`, `update`, `machine rm/rename/update`, `connect` (on the default session).
  - `herdr-app do`: `space.close`, `tab.close`, `pane.close`, `terminal.kill`, `terminal.paste`, `terminal.paste_image`, `app.quit`, `app.reset`, `app.update`, `app.update_core`, `window.close`, `layout.reset`.
- **Safe anywhere (read-only):**
  - `hc`: `ls`, `get`, `read`, `templates`, `schema`, `server status`, `machine ls`, `file read`, `attach --observe`, `events` (listening only), and `--version` / `--help` / `--skill`.
  - Socket read-only methods: `hello`, `server.status`, `terminal.list`, `terminal.get`, `terminal.read`, `templates.list`, `machine.list`, `doc.get`, `rpc.discover`, `events.subscribe` (listening only).
  - `herdr-app`: `ls`, `status`, `commands`, `shot`, `check`, `--version`, `--help`, `--skill`.
- **Sandboxes for testing and fixture capture:**
  - Either run an isolated core session with `HC_SESSION=aw-sandbox` (and a scratch `XDG_STATE_HOME` when testing server lifecycle), **or** start a background terminal labelled `aw=sandbox` (`hc run --bg --label aw=sandbox ...`), which stays invisible to the owner's `herdr-app` window because it has no `herdr.space` label.
- What the guards enforce, and for which tools: `tools/guards/README.md`. Where they don't reach, these rules still apply.
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
- `agent.prompt` is never sent to `blocked` agents, nor while the adapter parses a menu on the screen. Answers go through `send_keys`.
- `cancel` keys come from the menu on screen right now, never from a cached prompt. With no parseable menu, `cancel` only cancels a prompt the watch saw as `unknown` (the adapter's default keys); otherwise it returns `prompt_changed`.
- **Each command acts at most once:** commands on one pane run one at a time; the prompt is claimed (pane, seq, fingerprint or prompt-text hash) before sending, because pressing twice is worse than not pressing; recent `request_id`s are remembered. The command's budget starts on arrival (`pkg/bridge/cmdguard.go`, `commands.go`).

## Service
- launchd provides no `HC_*` / `HERDR_*` env or `PATH`: pass everything through the plist or `config.toml`.
