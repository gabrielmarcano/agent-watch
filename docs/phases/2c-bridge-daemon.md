# Phase 2c — The Bridge: Engine, Relay Link, CLI, launchd, herdr Plugin

> **Goal:** the `agent-watch-bridge` binary that runs on the Mac. It connects `pkg/herdr` and `pkg/agents` to the relay over an outbound WebSocket, executes watch commands safely, and installs itself as a launchd service from a herdr plugin action.

| | |
|---|---|
| **Depends on** | 2a (`pkg/herdr`), 2b (`pkg/agents`). The WebSocket side can be written against the contracts before 3a exists, and tested with an in-test fake relay |
| **Parallel with** | 3a/3b (relay), 4 (Wear OS) |
| **Touches** | `pkg/relayclient/**`, `pkg/bridge/**`, `cmd/bridge/**`, `deploy/launchd/**`, `herdr-plugin.toml`, `go.mod`/`go.sum`, `docs/STATUS.md` |

---

## Read first

- [`docs/reference/contracts.md`](../reference/contracts.md) §3 (wire protocol), §6 (bridge config and status file).
- [`HERDR_REFACTOR_PLAN.md`](../../HERDR_REFACTOR_PLAN.md) §6.2 (command safety rules) and §12 (plugin + launchd).
- [`docs/reference/herdr-socket-api.md`](../reference/herdr-socket-api.md): the safety warning.

---

## Architecture inside the binary

```
cmd/bridge/main.go         subcommands: configure | run | start | restart | stop | status | pair | version
      │ run
      ▼
pkg/bridge.Engine  ◄── herdr.Listener (OnChanges, OnHerdrOnline) ◄── herdr.Syncer
      │  builds model.AgentState, parses prompts (agents.Registry), captures history
      │  executes commands (validate → herdr.SendKeys / herdr.Prompt)
      ▼
pkg/relayclient.Client  ── wss /v1/host ──► relay
```

---

## Steps

### 1. `pkg/relayclient`

```bash
go get github.com/coder/websocket
```

```go
type Client struct {
    URL        string            // wss://relay.example.com/v1/host
    Token      string
    OnConnect  func(ctx context.Context) []any // messages to send first: hello + snapshot
    OnMessage  func(msg any)                   // decoded with model.DecodeWire; called sequentially
    Logger     *slog.Logger
}

func (c *Client) Run(ctx context.Context) error    // reconnect loop; returns only when ctx is done
func (c *Client) Send(msg any)                     // non-blocking; see queueing rules
func (c *Client) Connected() bool
```

- **Dial:** `websocket.Dial(ctx, URL, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + Token}}})`. Then `conn.SetReadLimit(1 << 20)`.
- **On connect:** write every message returned by `OnConnect`, in order, **before** anything queued.
- **Keepalive:** a goroutine calls `conn.Ping(ctx)` every 30 s with a 10 s timeout. On failure, close the connection and reconnect.
- **Backoff:** 1 s → 60 s, ±20 % jitter. Reset it after 60 s of healthy connection.
- **Queueing while disconnected:**
  - `agent_update`, `agent_removed` and `herdr_status` are **dropped**. The snapshot on reconnect covers them.
  - `history_item` goes into a bounded queue of 50 (drop the oldest) and is flushed after the snapshot.
  - `command_result` for a closed connection is dropped. The relay already timed that command out.
- **Close codes:** on `4000 replaced`, log a warning and keep retrying. On `401`/`403` during the handshake, log `invalid host token` and back off at the maximum.
- **Logs and status:** after the handshake it logs `relay connected; hello sent` (with the message types sent and how many queued history items were flushed). `LastError()` says why the relay is not connected (rejected token, failed dial, dropped connection) without the token; the engine copies it into `status.json` as `relay_error`.

### 2. `pkg/bridge.Engine`

```go
type Engine struct {
    Herdr    *herdr.Client
    Syncer   *herdr.Syncer
    Agents   *agents.Registry
    Relay    *relayclient.Client
    Version  string
    HostName string
    Logger   *slog.Logger
    // private: mu, states map[string]*paneState, herdrOnline bool, pong herdr.Pong, per-pane command locks
}

type paneState struct {
    info       herdr.AgentInfo
    public     model.AgentState
    prompt     *agents.Prompt // private keys; nil unless blocked
    lastHistID string
}

func (e *Engine) OnChanges(changes []herdr.Change)          // herdr.Listener
func (e *Engine) OnHerdrOnline(online bool, pong herdr.Pong) // herdr.Listener
func (e *Engine) ConnectMessages(ctx context.Context) []any  // for relayclient.OnConnect: hello + snapshot
func (e *Engine) HandleRelayMessage(msg any)                 // for relayclient.OnMessage
func (e *Engine) Status() StatusFile                         // for the status file writer
```

**Building `model.AgentState` from `herdr.AgentInfo`:** follow the table in `contracts.md` §1.2.

**On `Added` / `Updated`:**
1. Rebuild `public`.
2. **If `status == blocked`** and (`Prev == nil` or `Prev.AgentStatus != "blocked"` or `StateChangeSeq` changed):
   - `Read(pane, visible)`, then `Agents.For(agent).ParsePrompt(screen)`.
   - **If parsing fails**, try again: at most 3 attempts in total, 300 ms apart, within a 5 s budget (the TUI may draw the menu slightly after herdr flips the status). If none parses, or the budget runs out, use `agents.UnknownPrompt(screen)`.
   - Until the parse finishes, publish the agent **without** a prompt: the previous prompt belongs to an older screen and is dropped.
   - Store the result in `paneState.prompt`, and set `public.Prompt = &prompt.Public`.
3. **If `status != blocked`:** clear `prompt` and `public.Prompt`.
4. `Relay.Send(AgentUpdateMsg{Agent: public})`.
5. **History:** if `Prev != nil && Prev.AgentStatus == "working"` and the new status is `done` or `idle`, run `captureHistory(pane)` in a goroutine after 500 ms.

**On `Removed`:** delete the pane state and send `AgentRemovedMsg`.

**`captureHistory(pane)`:**
1. `ref := info.TrustedSession()`.
2. If `ref != nil`, call `Agents.For(agent).LastTurn(ctx, SessionRef{..., CWD: cwd})` with a 3 s timeout.
3. If that fails or `ref == nil`, call `Read(pane, recent_unwrapped, 200)`, then `agents.ScreenTurn(text)`.
4. Fill in:
   - `ID` = `model.HistoryID(pane, sessionValue, query, response)`
   - `PaneID`, `Agent`, `Label`
   - `CompletedAt` = `model.Now()`
5. If `ID == lastHistID`, skip it. Otherwise store it and `Relay.Send(HistoryItemMsg{...})`.

**On `OnHerdrOnline`:** store the flag and send `HerdrStatusMsg`. When herdr goes **offline**, keep the last states, but nothing can be commanded; commands then answer `herdr_offline`.

### 3. Command execution (the safety-critical part)

`HandleRelayMessage` receives `model.CommandMsg` (ignore `ResyncMsg` except to re-send `ConnectMessages`). Execute each command in its own goroutine, holding a **per-pane mutex**, so two taps on the same agent never interleave. Always reply with exactly one `CommandResultMsg`.

**Budget:** one 6 s context per command, started **on arrival** (so waiting for the pane lock counts). Timeouts nest from the inside out (bridge 6 s < relay 7 s < watch 8 s), so nothing runs after the relay or the watch has already reported `timeout`. Each herdr call gets a 3 s slice of it.

```
execute(cmd):                             // ctx = 9 s from arrival
  lock the pane                           → timeout if the budget runs out while waiting
  if request_id already seen              → stale_state ("duplicate request_id")
  if !herdrOnline                         → herdr_offline
  agents, err := Herdr.ListAgents(ctx)    // this command's OWN list, never the Syncer's shared snapshot
                                          (ErrUnavailable → herdr_offline); afterwards, whatever the
                                          outcome, trigger Syncer.Refresh in the background
  info := find(agents, cmd.PaneID)        → missing, or no agent: unknown_pane
  if info.StateChangeSeq != cmd.ExpectedSeq → stale_state
  ad := Agents.For(agent)
  switch cmd.Action:
  case "prompt":
     if strings.TrimSpace(cmd.Text) == "" || runes(cmd.Text) > 4000 → invalid_request
     switch status:                       // an allowlist: anything unknown is refused
       idle, done → ok
       working    → if !ad.PromptWhileWorking() → agent_busy
       blocked    → agent_blocked
       other      → agent_state_unknown   // "unknown", "", a future value
     screen := Read(pane, visible)         (fail closed: a read error refuses the prompt)
     if ad.ParsePrompt(screen) finds a menu → agent_blocked   // herdr 0.9.1 reports some dialogs as done/working
     claim (pane, seq, sha256(text))       → already claimed: stale_state (a double tap types once)
     err := Herdr.Prompt(pane, cmd.Text)   (herdr "agent_blocked" → agent_blocked; the claim is
                                           released only if nothing can have been typed)
  case "answer":
     if status != blocked                 → stale_state
     screen := Read(pane, visible)
     p, ok := ad.ParsePrompt(screen); if !ok → prompt_changed
     if p.Public.Fingerprint != cmd.Fingerprint → publish p; prompt_changed
     keys, ok := p.Keys[cmd.OptionID]; if !ok → unknown_option
     pressOnce(pane, seq, p.Fingerprint, keys)
  case "cancel":
     if status != blocked                 → stale_state
     screen := Read(pane, visible)        // never the cached prompt: agy's "2" cancels an edit
                                          // but means "always allow" in its bash prompt
     p, ok := ad.ParsePrompt(screen)
     if !ok:                              // no parseable menu, herdr still says blocked at seq
        if the watch was showing an unknown prompt (cmd.Fingerprint is the unknown fingerprint,
           or none was sent and the published prompt for seq is kind=unknown):
             pressOnce(pane, seq, unknownFingerprint, ad.CancelKeys())   // the adapter's default keys
        else → prompt_changed             // the menu the watch saw is gone
     expected := cmd.Fingerprint, or, when empty, the fingerprint this bridge published for seq
     if expected == "" or p.Public.Fingerprint != expected → publish p; prompt_changed
     pressOnce(pane, seq, p.Fingerprint, p.EffectiveCancelKeys(ad))
  default → invalid_request

pressOnce(pane, seq, fp, keys):           // claimed before sending: pressing twice is the worse outcome
  if (pane, seq, fp) already claimed      → stale_state ("prompt already answered")
  Herdr.SendKeys(pane, keys)
```

- **"publish p"**: on `prompt_changed` the bridge publishes the prompt it just parsed (if the pane is still blocked at that seq), so the watch stops showing one it can never answer.
- **Claims** (answered prompts, prompt-text hashes) are forgotten when herdr reports a new `state_change_seq` for the pane, or the pane goes away. The last 256 request ids are remembered, whatever the seq.

- **Audit log** (INFO): one line per command with `action`, `pane_id`, `agent`, `result`, and `text_len` for prompts. **Never log the prompt text.**
- **The bridge never accepts raw keys.** No code path may pass watch-supplied strings to `SendKeys`.

### 4. `cmd/bridge` subcommands

Use the standard `flag` package with one `FlagSet` per subcommand.

| Subcommand | Behaviour |
|---|---|
| `configure --relay-url URL --host-token TOKEN [--host-name N] [--claude-config-dir D]... [--config PATH]` | Validate that the URL starts with `wss://` (allow `ws://` only when the host is `localhost`/`127.0.0.1`) and that the token is 64 hex chars; append `/v1/host` to a URL without a path. Write `config.toml` atomically with mode `0600` in a `0700` dir (both enforced even if they exist). Print the path. If the installed service uses that config, say to run `restart` |
| `run [--config PATH]` | Validate the config; on failure write a stopped `status.json` (`pid: 0`, the reason in `last_error`) and exit 1. Build Client, Registry, Syncer, Engine and relayclient. Start the status-file writer (`contracts.md` §6.1: every 5 s, a final `pid: 0` on exit). Run until SIGINT/SIGTERM. Log to stderr with `slog` text handler |
| `start [--binary P] [--config P] [--socket P] [--state-dir D] [--log P]` | Validate the config and the resolved paths first (a bad config never reaches launchd), then write the service definition and (re)load it (§5). Print `changed:` lines for values that differ from the installed definition, then "started" and the log path |
| `restart` | Restart the installed service **without rewriting its definition** (`launchctl kickstart -k`, or `bootstrap` of the existing plist when it is unloaded; `systemctl --user restart`). Refuse if nothing is installed, the config it uses is invalid, or its binary is missing |
| `stop` | Unload the service; the definition stays on disk. Print "stopped" |
| `status [--json] [--local]` | Human form: the bridge (live pid from `status.json`) and the service manager's view, relay link, herdr, agents and blocked count, last error, paths, and `GET {https relay}/v1/host/status`. Exit 1 if the bridge is not running. `--json` prints `contracts.md` §6.2. `--local` skips the network and the service manager (what the menu bar polls) |
| `pair [--json] [--config PATH]` | `POST {https relay}/v1/host/pair-code` with the host token. Print the code in large, spaced digits (`4 1 7 · 2 9 3`), the real expiry from `expires_at` and the relay URL (`--json`: `contracts.md` §6.3). **Also** call herdr `notification.show` `{"title":"Agent Watch pairing code 417293","body":"Enter it on your watch. Expires at 17:09 (in 5 min).","sound":"request"}`, because a herdr action's stdout may not be visible. Needs only the config and the relay, not a running bridge |
| `version` | Print `version` (set by `-ldflags -X main.version=…`) |

- **Config path for `configure`:** `--config`, else `$HERDR_PLUGIN_CONFIG_DIR/config.toml`, else the installed service's config, else `~/.config/herdr/plugins/config/herdr-agent-watch/config.toml`.
- **Paths `status` and `pair` read:** the installed service's first (that is where the running bridge writes), then herdr's environment, then the defaults.
- **State dir default:** `$HERDR_PLUGIN_STATE_DIR`, else `~/.local/state/agent-watch`.
- **HTTPS base:** derived from `relay_url` by replacing `wss://` with `https://` (`ws://` → `http://`) and dropping any path.

```bash
go get github.com/BurntSushi/toml
```

### 5. Service installation (`start` / `stop`)

**macOS (primary):** write `~/Library/LaunchAgents/com.gabrielmarcano.agent-watch-bridge.plist` from the template in `deploy/launchd/com.gabrielmarcano.agent-watch-bridge.plist.tmpl`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.gabrielmarcano.agent-watch-bridge</string>
  <key>ProgramArguments</key>
  <array>
    <string>{{.Binary}}</string>
    <string>run</string>
    <string>--config</string>
    <string>{{.ConfigPath}}</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HERDR_SOCKET_PATH</key><string>{{.SocketPath}}</string>
    <key>HERDR_PLUGIN_STATE_DIR</key><string>{{.StateDir}}</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>10</integer>
  <key>StandardOutPath</key><string>{{.LogPath}}</string>
  <key>StandardErrorPath</key><string>{{.LogPath}}</string>
</dict>
</plist>
```

**Where each value comes from** (`start`, per value, the first that is set):

| Value | 1. flag | 2. herdr's plugin environment | 3. installed definition | 4. default |
|---|---|---|---|---|
| `{{.Binary}}` | `--binary` | this binary, when run as a plugin action (`HERDR_PLUGIN_*` set) | its `ProgramArguments[0]`, if that file still exists | this binary (`os.Executable()`, symlinks resolved) |
| `{{.ConfigPath}}` | `--config` | `$HERDR_PLUGIN_CONFIG_DIR/config.toml` | its `--config` | `~/.config/herdr/plugins/config/herdr-agent-watch/config.toml` |
| `{{.SocketPath}}` | `--socket` | `$HERDR_SOCKET_PATH` | its `HERDR_SOCKET_PATH` | `~/.config/herdr/herdr.sock` |
| `{{.StateDir}}` | `--state-dir` | `$HERDR_PLUGIN_STATE_DIR` | its `HERDR_PLUGIN_STATE_DIR` | `~/.local/state/agent-watch` |
| `{{.LogPath}}` | `--log` | — | its log path | `~/Library/Logs/agent-watch-bridge.log` |

- So `start` from a terminal or the menu bar keeps the definition herdr installed, instead of rewriting it with the caller's environment.
- Every path must be absolute and free of control characters. Values are XML-escaped in the plist (quoted and `%`/`$`-escaped in the systemd unit), and both parse back. The definition is written atomically.
- Embed the template with `//go:embed`.

Commands, where `uid := os.Getuid()` and `target := fmt.Sprintf("gui/%d", uid)`:

```bash
launchctl bootout   gui/<uid>/com.gabrielmarcano.agent-watch-bridge   # start: ignore "not loaded" errors
launchctl bootstrap gui/<uid> ~/Library/LaunchAgents/com.gabrielmarcano.agent-watch-bridge.plist
                                                                      # retried for ~1.5 s: bootout is asynchronous
launchctl kickstart -k gui/<uid>/com.gabrielmarcano.agent-watch-bridge   # restart (bootstrap if not loaded)
launchctl print     gui/<uid>/com.gabrielmarcano.agent-watch-bridge   # for status: parse "state = running" and "pid = N"
```

**Linux (secondary, best-effort):**
- Write `~/.config/systemd/user/agent-watch-bridge.service` with `ExecStart=<binary> run --config <path>`, `Restart=always`, `RestartSec=5` and the same `Environment=` lines. Output goes to the journal (`journalctl --user -u agent-watch-bridge`).
- `start` runs `systemctl --user daemon-reload`, `systemctl --user enable agent-watch-bridge` and `systemctl --user restart agent-watch-bridge` (a plain `enable --now` would keep an old binary running). `restart` is `systemctl --user restart`; `stop` is `stop` + `disable`.
- Choose the implementation with `runtime.GOOS`.

### 6. `herdr-plugin.toml` (repo root)

The manifest is in §12.2 of the plan: five one-shot actions (`start`, `restart`, `stop`, `status`, `pair`) and a static, version-stamped `[[build]]` (the same flags as `make bridge`). Plugin actions run with the plugin root as their working directory, so `./bin/agent-watch-bridge` resolves.

**Local development loop:**

```bash
make bridge
herdr plugin link "$PWD"                 # absolute path required
herdr plugin action list --plugin herdr-agent-watch
./bin/agent-watch-bridge configure --relay-url wss://relay.example.com --host-token <64hex>
herdr plugin action invoke --plugin herdr-agent-watch start
herdr plugin action invoke --plugin herdr-agent-watch status
tail -f ~/Library/Logs/agent-watch-bridge.log
```

- `[[build]]` runs only on `herdr plugin install`, never on `link`. That is why `make bridge` comes first.
- After changing Go code, rebuild and restart:

  ```bash
  make restart        # make bridge, then agent-watch-bridge restart
  ```

  `restart` keeps the installed definition as it is. Use `start` only to change it (a new flag value, a moved binary); `start` does a bootout + bootstrap.

### 7. Tests

| Package | Test | How |
|---|---|---|
| `relayclient` | reconnect, OnConnect ordering, history queue flush, drop of updates while offline | `httptest.NewServer` running a `websocket.Accept` handler as a fake relay |
| `relayclient` | Authorization header present, never in the URL | Inspect the request in the fake |
| `bridge` | AgentState mapping (label fallbacks, cwd preference) | Unit test with `herdr.AgentInfo` values |
| `bridge` | blocked → prompt parsed and sent; retry then `unknown` when the screen has no menu | `herdrtest` + a fixture screen from `pkg/agents/testdata/claude` |
| `bridge` | every error code of §3 (`stale_state`, `prompt_changed`, `unknown_option`, `agent_busy`, `agent_blocked`, `agent_state_unknown`, `unknown_pane`, `herdr_offline`) | `herdrtest` + a fake relay; assert the recorded herdr calls (e.g. **no** `send_keys` on `prompt_changed`) |
| `bridge` | `answer` sends exactly the golden keys for the option | `herdrtest.Calls()` |
| `bridge` | history: working→done triggers `LastTurn`, falls back to the screen, dedups by ID | `herdrtest` + a temp transcript file |
| `cmd/bridge` | `configure` writes 0600, rejects bad URL/token | Temp dir |
| `cmd/bridge` | plist and unit rendering contain absolute paths and every key above, and parse back | Golden string test (do not call `launchctl` in tests) |
| `cmd/bridge` | `start` value resolution, `restart` never rewrites, `status --json --local`, `pair` expiry | An `app` with a temp `HOME` and a fake command runner; a `TestMain` safety net keeps tests off the real home, launchd and systemd |

### 8. Manual verification on the Mac (requires the owner's herdr, no relay needed)

1. Run a local relay stub: `go run ./cmd/relay serve` if 3a is done. Otherwise use the fake relay from the tests, exposed via a tiny `main` in the scratchpad.
2. Configure it with `ws://127.0.0.1:8080/v1/host` and `run` in the foreground.
3. Confirm that `hello` and a `snapshot` with the owner's agents arrive, and that switching panes produces `agent_update` messages.
4. **Do not send commands to the owner's panes.** Test commands only against a sandbox pane (see the `capture-fixture` skill).

---

## Definition of done

- [ ] `go vet ./... && go test -race ./...` pass.
- [ ] `make bridge` builds with `CGO_ENABLED=0`.
- [ ] `herdr plugin link "$PWD"` lists the 5 actions; `start`, `restart`, `status` and `stop` work; the log file shows `relay connected; hello sent`.
- [ ] A sandbox pane went blocked → the relay (stub) received the parsed prompt → an `answer` from the stub approved it; `cancel` denied it.
- [ ] `docs/STATUS.md` Phase 2c ticked, with notes on what was verified manually.
- [ ] Commit only the paths under **Touches**.

---

## Pitfalls

- **launchd has no `PATH` or `HERDR_*` variables.** Everything the daemon needs must be in the plist or the config file.
- **Running two bridges** (one from a terminal, one from launchd) makes the relay replace connections back and forth. Before a foreground `run`, check `launchctl print …`, and `stop` the service first.
- **Calling `Syncer.Refresh` synchronously from a Listener callback** deadlocks (the callback holds the Syncer's turn). Use a goroutine, as `refreshSoon` does.
- **Blocking the Syncer's listener goroutine** with network I/O delays every update. Keep `OnChanges` fast, and do screen reads and history capture in goroutines guarded by the pane state.
- **Sending keys based on a cached prompt.** Always re-read the screen and compare fingerprints right before sending.

---

## Prompt for the executing agent

```
You are executing Phase 2c (the bridge daemon) of the Agent Watch refactor in this repository.
Read AGENTS.md, docs/reference/contracts.md (§3, §6), HERDR_REFACTOR_PLAN.md §6.2 and §12, the safety warning in
docs/reference/herdr-socket-api.md, and docs/phases/2c-bridge-daemon.md. Implement pkg/relayclient, pkg/bridge,
cmd/bridge, deploy/launchd and herdr-plugin.toml exactly as specified. The command executor is safety-critical:
re-validate state_change_seq and the prompt fingerprint right before every key press, never pass watch-supplied
strings to send_keys, and never log prompt text. Tests use pkg/herdrtest and an in-test fake relay. Manual tests
may only send keys/prompts to panes you created in an "aw-sandbox" workspace. Run `go vet ./... && go test -race ./...`,
paste the output, tick Phase 2c in docs/STATUS.md, and commit only the paths the guide lists.
```
