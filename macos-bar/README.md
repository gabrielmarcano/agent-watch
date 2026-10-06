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
open "bin/Agent Watch.app"
```

- Universal (Apple silicon + Intel), macOS 13 or later, ad-hoc signed.
- Its icon is drawn at build time from the Wear OS launcher icon's vector drawables (`make-icon.swift`), so both apps share one artwork.
- The bundle version is `MENUBAR_VERSION` from `VERSIONS` (bump it as that
  file says). `build.sh` writes it into the built `Info.plist` before
  signing; the committed `Info.plist` holds a `0.0.0` placeholder.
- `APP_DIR=/path/AgentWatch.app macos-bar/build.sh` builds somewhere else;
  `MENUBAR_VERSION=x.y.z` overrides the version for one build.
- To update a running copy: quit it from its menu, rebuild, open it again.
- `make bar-test` tests the decision logic (`BarLogic.swift`) with the
  harness in `Tests/`: it runs the CLI with an empty temp dir as HOME, and
  never launches the app or touches launchd or the real home.

## Launch at login

**Open at Login** in the menu turns it on and off (`SMAppService`). It is
checked while on, and shows a dash while macOS waits for your approval: the
item then opens System Settings → General → Login Items (Login Items &
Extensions on macOS 15 and later), where you allow it. A change made there
shows in the menu too.

After moving or renaming the app (up to menu bar 0.4.0 it was `bin/AgentWatchBar.app`), turn the item off and on again.

## Which bridge binary it uses

1. The binary the installed LaunchAgent runs
   (`~/Library/LaunchAgents/com.gabrielmarcano.agent-watch-bridge.plist`,
   first `ProgramArguments` entry).
2. Otherwise `agent-watch-bridge` next to the app (`bin/`, where `make`
   puts both).

If neither exists the icon shows the watch with `!` and a red dot, and the menu says how to build it.

## What the icon says

The bar reports the health of the bridge and the relay, and pairs watches. It never shows agents or blocked counts: that is the watch's job. Next to the clock there is only the icon and a small status circle: **green** connected, **yellow** running but not fully connected yet, **red** it should be working and is not, **gray** off on purpose. The menu's first row says which state it is, under "Agent Watch".

| Icon | Dot | Menu says | What to do |
|---|---|---|---|
| watch with waves | green | Connected to *relay* | Nothing |
| watch | yellow | Connecting to *relay*… | Wait |
| watch | yellow | herdr is not running | Start herdr |
| watch with `!` | red | Relay error, with a short reason (for example `Relay unreachable (timed out)` or `The relay rejected the host token`); hover over it for the full error, also in the bridge log | Fix the cause; it keeps retrying |
| watch with `!` | red | Bridge not responding (its status file is stale: `contracts.md` §6) | Restart |
| crossed watch | gray, or red if it failed to start | Bridge stopped (and why, if it failed) | Turn the switch on |
| crossed watch | gray | Bridge service not installed | Turn the switch on |
| crossed watch | gray | Bridge not configured; the switch is disabled | Run `make configure-bridge` (or `agent-watch-bridge configure --env-file agent-watch.env`) in a terminal |
| watch with `!` | red | agent-watch-bridge not found, or status unavailable | See the menu |

The tooltip repeats the state. No menu line is longer than `maxMenuLine` (`BarLogic.swift`): errors are shortened there. The **Versions** section of the menu shows:

- **Menu bar:** this app's bundle version.
- **Bridge:** the running daemon's version, or the CLI's when it is stopped.
- **Relay:** what the relay reported in its last handshake with the running bridge (`relay_version`), kept while the relay is unreachable. No line while it is unknown: the bridge is stopped, has not connected yet, or the relay is too old to report it.

The version strings' format is in `docs/reference/contracts.md` §3.

## Menu actions

- **The switch** in the first row (next to "Agent Watch" and the state): on runs `agent-watch-bridge start`, off runs `agent-watch-bridge stop`. Start keeps the installed LaunchAgent's paths, so it never moves the state dir or the binary. Off asks nothing: your watch shows this Mac as offline until you turn it on again. The switch is disabled while an action runs, and while the bridge is not configured.
- **Restart Bridge** (⌘R): `agent-watch-bridge restart`, which restarts the
  installed service without rewriting it (use it after `make bridge`).
- **Pair a Watch…** (⌘P): `agent-watch-bridge pair --json`; shows the code and
  its real expiry.
  "Copy Code" marks the clipboard entry as concealed.
- **Open Bridge Log** (⌘L) and **Show Configuration in Finder** (⌘,): the
  config holds the host token, so it is revealed, not opened in an editor.
- **Open at Login**: § Launch at login.

Any failed action shows an alert with the CLI's output.
