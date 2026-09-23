---
paths:
  - "pkg/herdr/**"
  - "pkg/herdrtest/**"
  - "pkg/bridge/**"
  - "cmd/bridge/**"
  - "herdr-plugin.toml"
---

# herdr integration rules

Full reference: `docs/reference/herdr-socket-api.md` (verified on herdr 0.9.1, protocol 22).

## Safety (non-negotiable)
- The herdr on this Mac runs the **owner's real agent sessions**. Never call `agent.prompt`, `agent.send_keys`, `pane.send_*`, `agent.start` or `pane.close` against a pane you did not create in the `aw-sandbox` workspace. A hook enforces this for the CLI and blocks raw socket writes.
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
  4. re-parse it and compare the fingerprint.

  On mismatch, send nothing.
- `agent.prompt` is never sent to `blocked` agents. Answers go through `send_keys`.

## Plugin and service
- The plugin actions are one-shot. The long-running process is `agent-watch-bridge run` under launchd (`com.gabrielmarcano.agent-watch-bridge`).
- launchd provides no `HERDR_*` env or `PATH`: pass everything through the plist or `config.toml`.
- `[[build]]` does not run on `herdr plugin link`: run `make bridge` first.
