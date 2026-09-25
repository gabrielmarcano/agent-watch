# Agent Watch menu bar app (macOS)

A small menu bar companion for the host bridge. It shows at a glance whether
the bridge is running and connected, and how many agents are **blocked**
waiting for you, and it starts, stops, restarts and pairs through the
`agent-watch-bridge` CLI. It holds no logic of its own about paths or
services: every 2.5 s it runs `agent-watch-bridge status --json --local`
(local files only, no network) and shows what that says.

## Build

```bash
make bar            # or: macos-bar/build.sh
open bin/AgentWatchBar.app
```

- Universal (Apple silicon + Intel), macOS 13 or later, ad-hoc signed.
- `APP_DIR=/path/AgentWatchBar.app macos-bar/build.sh` builds somewhere else;
  `VERSION=x.y.z` stamps the bundle version.
- To update a running copy: quit it from its menu, rebuild, open it again.

## Launch at login

System Settings → General → Login Items → **Open at Login** → `+` → choose
`bin/AgentWatchBar.app`.

## Which bridge binary it uses

1. The binary the installed LaunchAgent runs
   (`~/Library/LaunchAgents/com.gabrielmarcano.agent-watch-bridge.plist`,
   first `ProgramArguments` entry).
2. Otherwise `agent-watch-bridge` next to the app (`bin/`, where `make`
   puts both).

If neither exists the icon shows `?` and the menu says how to build it.

## What the icon says

| Icon | Text | State | What to do |
|---|---|---|---|
| watch with waves | *(none)* | Running and connected, no agent blocked | Nothing |
| watch with waves | **`2`** (orange) | Connected; 2 agents are blocked waiting for you | Answer them |
| watch | `…` | Running, connecting to the relay | Wait |
| watch with `!` | `!` | Relay error (for example the host token was rejected); the menu shows the error | Fix the cause; it keeps retrying |
| watch | `no herdr` | Connected to the relay, but herdr is not running | Start herdr |
| watch with `!` | `stale` | The bridge process exists but has not written its status for more than 15 s | Restart |
| crossed watch | `off` | Installed but stopped (the menu shows why, if it failed to start) | Start |
| crossed watch | `install` | Configured, but the LaunchAgent is not installed yet | Start |
| crossed watch | `setup` | No valid config; Start is disabled | Run `agent-watch-bridge configure --relay-url wss://… --host-token …` in a terminal |
| watch with `!` | `?` | The bridge binary was not found, or its status could not be read | See the menu |

The tooltip repeats the state with the agent and blocked counts.

## Menu actions

- **Start Bridge** (⌘S): `agent-watch-bridge start`. It keeps the installed
  LaunchAgent's paths, so starting from here never moves the state dir or the
  binary.
- **Stop Bridge…** (⌘.): asks first, then `agent-watch-bridge stop`.
- **Restart Bridge** (⌘R): `agent-watch-bridge restart`, which restarts the
  installed service without rewriting it (use it after `make bridge`).
- **Pair a Watch…** (⌘P): `agent-watch-bridge pair --json`; shows the code and
  its real expiry. Needs only the config and the relay, not a running bridge.
  "Copy Code" marks the clipboard entry as concealed.
- **Open Bridge Log** (⌘L) and **Show Configuration in Finder** (⌘,): the
  config holds the host token, so it is revealed, not opened in an editor.

Any failed action shows an alert with the CLI's output.
