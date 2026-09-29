# Agent Watch menu bar app (macOS)

A small menu bar companion for the host bridge. It shows at a glance whether
the bridge is running and connected to the relay (a status circle on its icon),
and it starts, stops, restarts and pairs through the
`agent-watch-bridge` CLI. It holds no logic of its own about paths or
services: every 2.5 s it runs `agent-watch-bridge status --json --local`
(local files only, no network) and shows what that says.

## Build

```bash
make bar            # or: macos-bar/build.sh
open bin/AgentWatchBar.app
```

- Universal (Apple silicon + Intel), macOS 13 or later, ad-hoc signed.
- The bundle version is `MENUBAR_VERSION` from `VERSIONS` at the repo root:
  bump it there when the app changes. `build.sh` writes it into the built
  `Info.plist` before signing; the committed `Info.plist` holds a `0.0.0`
  placeholder.
- `APP_DIR=/path/AgentWatchBar.app macos-bar/build.sh` builds somewhere else;
  `MENUBAR_VERSION=x.y.z` overrides the version for one build.
- To update a running copy: quit it from its menu, rebuild, open it again.
- `make bar-test` tests the decision logic (`BarLogic.swift`) with the
  harness in `Tests/`: it runs the CLI with an empty temp dir as HOME, and
  never launches the app or touches launchd or the real home.

## Launch at login

System Settings → General → Login Items → **Open at Login** → `+` → choose
`bin/AgentWatchBar.app`.

## Which bridge binary it uses

1. The binary the installed LaunchAgent runs
   (`~/Library/LaunchAgents/com.gabrielmarcano.agent-watch-bridge.plist`,
   first `ProgramArguments` entry).
2. Otherwise `agent-watch-bridge` next to the app (`bin/`, where `make`
   puts both).

If neither exists the icon shows the watch with `!` and a red dot, and the menu says how to build it.

## What the icon says

The bar reports the health of the bridge and the relay, and pairs watches. It never shows agents or blocked counts: that is the watch's job. Next to the clock there is only the icon and a small status circle: **green** connected, **yellow** running but not fully connected yet, **red** it should be working and is not, **gray** off on purpose. The menu's first line says which state it is.

| Icon | Dot | Menu says | What to do |
|---|---|---|---|
| watch with waves | green | Connected to *relay* | Nothing |
| watch | yellow | Connecting to *relay*… | Wait |
| watch | yellow | herdr is not running | Start herdr |
| watch with `!` | red | Relay error (for example the host token was rejected), with the error | Fix the cause; it keeps retrying |
| watch with `!` | red | Bridge not responding (no status for more than 15 s) | Restart |
| crossed watch | gray, or red if it failed to start | Bridge stopped (and why, if it failed) | Start |
| crossed watch | gray | Bridge service not installed | Start |
| crossed watch | gray | Bridge not configured; Start is disabled | Run `make configure-bridge` (or `agent-watch-bridge configure --env-file agent-watch.env`) in a terminal |
| watch with `!` | red | agent-watch-bridge not found, or status unavailable | See the menu |

The tooltip repeats the state. The **Versions** section of the menu shows:

- **Menu bar** `x.y.z`: this app's bundle version.
- **Bridge** `x.y.z (<commit>)`: the running daemon's version with its commit, or the CLI's when it is stopped. A `, modified` after the commit means it was built with uncommitted changes.
- **Relay** `x.y.z (<commit>)`: what the relay reported in its last handshake with the running bridge (`relay_version`), kept while the relay is unreachable. No line while it is unknown: the bridge is stopped, has not connected yet, or the relay predates 0.3.0.

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
