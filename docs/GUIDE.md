# Agent Watch — Full Guide

How to set up, run and troubleshoot Agent Watch, in detail. The [README](../README.md) is the short version.

**Contents:** [Requirements](#requirements) · [Setup](#setup-guide) · [Day-to-day](#day-to-day) · [Versions and releases](#versions-and-releases) · [Supported agents](#supported-agents) · [Security model](#security-model) · [Known issues](#known-issues) · [Troubleshooting](#troubleshooting)

---

## Requirements

The minimums below come from the build files named with each one; those files win if this list lags.

1. **herdr** 0.9.1 or later (`min_herdr_version` in `herdr-plugin.toml`) on the machine that runs your agents, with its integrations installed for your agents (`herdr integration status`).
2. **Go** 1.23 or later (`go.mod`) to build the bridge and the relay (static binaries).
3. **Cloud relay:** a small **x86-64 (amd64)** Linux VPS (1 vCPU, 512 MB RAM) with a public domain (e.g. `relay.example.com`). For arm64: [`deploy/relay/README.md` § First-time setup](../deploy/relay/README.md#first-time-setup-once-per-vps).
   - **Any TLS reverse proxy** in front of it works if it passes WebSockets and unbuffered SSE (nginx, Caddy, Nginx Proxy Manager; Cloudflare optional): [`deploy/relay/README.md`](../deploy/relay/README.md).
4. **Smartwatch:**
   - **Wear OS** 3 or later (`minSdk` 30 in `wearos-app/app/build.gradle.kts`) — *primary; verified only on a Google Pixel Watch 2*.
   - **watchOS** — *best-effort*, simulator-only ([status](STATUS.md)).
   - **Network:** the watch needs only a path to the relay over HTTPS and to Firebase for push: Wi-Fi, LTE, or its phone's connection over Bluetooth (Wear OS routes it through the phone on its own; no companion app of ours is involved). Which paths have been checked: [STATUS](STATUS.md).
5. **To build the Wear OS app:** JDK 17 (`wearos-app/app/build.gradle.kts`; Android Studio's bundled JBR works), the Android SDK, `adb`, and your own **Firebase project** for push.

---

## Setup Guide

Follow the steps in order. Placeholders: `relay.<domain>` is your relay's hostname, `<vps>` its SSH target.

### 0. Create Your Configuration File

Everything a deployment needs lives in **one file**, `agent-watch.env` at the repo root. It is git-ignored, and the tools read it directly, so the host token never appears on a command line or in their output.

1. Create it and generate the host token the relay and the bridge share:
   ```bash
   make config
   # created agent-watch.env from agent-watch.env.example
   # agent-watch.env: generated AW_HOST_TOKEN (not shown; it stays in the file)
   ```
   The file is `0600`. Re-running `make config` never replaces a token.
2. Edit `agent-watch.env`:
   - `AW_RELAY_DOMAIN`: your relay's host name (`relay.<domain>`, no `https://`). The bridge URL (`wss://relay.<domain>/v1/host`) and the watch's pairing default (`https://relay.<domain>`) come from it.
   - `AW_RELAY_SSH`: the VPS's SSH target (root), and `AW_RELAY_SSH_OPTS` if you need a key or port (`-i ~/.ssh/<key> -o Port=2222`).
   - Optionally, uncomment the relay settings you want the deploy to manage on the server (`AW_LISTEN`, `AW_TRUSTED_PROXIES`, `AW_FCM_CREDENTIALS`, …).

   Every key and the file's syntax: [`contracts.md` §7](reference/contracts.md) (the example file has a one-line hint per key).

**Never commit or paste `agent-watch.env`.** The pre-commit guard refuses it.

### 1. Deploy the Cloud Relay

1. **First time only:** prepare the VPS (service user, env file, systemd unit, TLS proxy) as in [`deploy/relay/README.md` § First-time setup](../deploy/relay/README.md#first-time-setup-once-per-vps). The env file's token placeholder is replaced in the next step.
2. Build, install and restart the relay, and copy the relay keys of `agent-watch.env` (the host token included) into the server's env file:
   ```bash
   make deploy-relay ARGS=--sync-env
   ```
   - Later code updates: `make deploy-relay`. Add `ARGS=--sync-env` again whenever you change a relay key or the token.
   - What the deploy and `--sync-env` do, the proxy topologies, client IP and devices: [`deploy/relay/README.md`](../deploy/relay/README.md).
3. Verify the relay is reachable:
   ```bash
   curl https://relay.<domain>/v1/healthz
   # Expected: {"ok":true}
   ```

### 2. Install and Configure the Host Bridge

On the machine that runs herdr:

1. Build the bridge binary:
   ```bash
   make bridge
   ```
2. Link the plugin into herdr (absolute path):
   ```bash
   herdr plugin link "$PWD"
   ```
3. Write the bridge config from `agent-watch.env` (relay URL and host token; `config.toml`, mode `0600`):
   ```bash
   make configure-bridge
   # = ./bin/agent-watch-bridge configure --env-file agent-watch.env
   ```
   - Optional settings go in `ARGS`, e.g. `make configure-bridge ARGS="--host-name my-mac"`; pass them again on every run. What `configure` writes, where, and its flags: [`contracts.md` §6](reference/contracts.md).
   - `--claude-config-dir` is only for Claude profiles the bridge does not find on its own ([`agents.md` §3.2](reference/agents.md)).
   - Without the file: `./bin/agent-watch-bridge configure --relay-url wss://relay.<domain> --host-token <64-hex-token>` (the token then shows in `ps`).
4. Start the bridge service (a LaunchAgent on macOS, a `systemd --user` unit on Linux):
   ```bash
   herdr plugin action invoke --plugin herdr-agent-watch start
   # or: ./bin/agent-watch-bridge start
   ```
   Already installed? After `make configure-bridge`, apply the new config with `make restart`.
5. Verify the bridge:
   ```bash
   ./bin/agent-watch-bridge status
   # bridge:   running (pid …); launchd: running (pid …)
   # relay:    connected to relay.<domain>
   # herdr:    online
   # agents:   N (M blocked)
   # …then the config, status and log paths
   ```

### Alternative: Install the Bridge with herdr

Agent Watch is a herdr plugin, listed on [herdr.dev/plugins](https://herdr.dev/plugins/). herdr can install it instead of step 2's `make bridge` and `herdr plugin link`. Whether it has been tested end to end: [STATUS](STATUS.md); please report problems as issues.

1. Install it. herdr clones the repository, shows the source and the build command, and asks before running them. The build compiles the bridge on your machine, so it needs **Go** ([Requirements](#requirements)):
   ```bash
   herdr plugin install gabrielmarcano/agent-watch
   ```
2. Find the plugin's folder: `plugin_root` in
   ```bash
   herdr plugin list --plugin herdr-agent-watch --json
   ```
   The bridge is `<plugin_root>/bin/agent-watch-bridge`, and the folder also holds `agent-watch.env.example`.
3. Configure and start it, as in step 2:
   ```bash
   "<plugin_root>/bin/agent-watch-bridge" configure --env-file /path/to/agent-watch.env
   herdr plugin action invoke --plugin herdr-agent-watch start
   ```

The relay (step 1), the watch app (step 3) and pairing (step 4) are unchanged. The relay deploy (`make deploy-relay`) still needs a checkout of this repository, or a relay binary from a [release](#versions-and-releases).

### 3. Build and Install the Wear OS App

1. **Firebase:** create a Firebase project, add an Android app with the package name `com.gabriel.agentwatch` (the app's id; the `google-services.json` must match it), and download its `google-services.json` to `wearos-app/app/`. It is git-ignored; never commit it. The build fails without it.
2. **Build** with the JDK from [Requirements](#requirements):
   ```bash
   cd wearos-app
   export JAVA_HOME="/Applications/Android Studio.app/Contents/jbr/Contents/Home"   # or another JDK of that version
   ./gradlew :app:testDebugUnitTest :app:assembleDebug
   ```
   The build reads `AW_RELAY_DOMAIN` from `../agent-watch.env` and exposes `https://<domain>` as `BuildConfig.DEFAULT_RELAY_URL`, the pairing screen's default relay URL (`""` without the file). Rebuild after changing the domain.
3. **Connect the watch over wireless debugging.** The Pixel Watch has no USB data connection, so `adb` goes over Wi-Fi (watch and computer on the same network):
   - on the watch: Settings → Developer options → **Wireless debugging** → **Pair new device**;
   - on the computer: `adb pair <ip>:<pair-port> <code>`, then `adb connect <ip>:<port>` (the connect port is shown on the Wireless debugging screen and changes after a reboot).
4. **Install:** `./gradlew :app:installDebug`.
5. **Push on the relay:** copy the Firebase **service-account** JSON to the VPS only, as [`deploy/relay/README.md` § First-time setup](../deploy/relay/README.md#first-time-setup-once-per-vps) says, set `AW_FCM_CREDENTIALS` to its server path in `agent-watch.env`, and run `make deploy-relay ARGS=--sync-env`. Without it the relay runs with push disabled.

**watchOS** ([status](STATUS.md)): `make watchos-config` writes the git-ignored `watchos-app/Config.generated.xcconfig` ([`contracts.md` §7](reference/contracts.md)).

The release build (`assembleRelease`, R8 on) is signed with the debug key: fine for your own watch, not for distribution.

### 4. Pair Your Smartwatch

1. Get a short-lived pairing code:
   ```bash
   ./bin/agent-watch-bridge pair
   # or: herdr plugin action invoke --plugin herdr-agent-watch pair
   ```
   It shows the code, its real expiry and the relay URL, and also posts a herdr notification.
2. On the watch, open Agent Watch, check the relay URL (`https://relay.<domain>`, prefilled from `agent-watch.env` at build time; `https://` is added if you leave it out) and enter the code.
3. The watch exchanges the code for a device token (the relay stores only its SHA-256 hash) and registers for push. Your agents appear.

When the bridge runs on macOS, quiet pushes apply: while you use the Mac (input in the last 10 minutes by default, screen unlocked), the watch does not buzz; a prompt still waiting when you leave is pushed then. For this the bridge sends your relay the Mac's input idle time and screen-lock state every 15 s; `AW_PUSH_PRESENCE_IDLE=0` on the relay turns it off. Rules and the setting: [`contracts.md` §4.3](reference/contracts.md).

### 5. Optional: macOS Menu Bar App

`make bar && open bin/AgentWatchBar.app`: a menu bar companion for the bridge. What it shows and does: [`macos-bar/README.md`](../macos-bar/README.md).

### Day-to-day

| Command | What it does |
|---|---|
| `make config` | Create `agent-watch.env` and its host token ([step 0](#0-create-your-configuration-file)) |
| `make configure-bridge` | Rewrite the bridge's `config.toml` from `agent-watch.env`; then `make restart` |
| `make deploy-relay [ARGS=--sync-env]` | Build and deploy the relay to `AW_RELAY_SSH`; `--sync-env` also updates the server's env file from `agent-watch.env` |
| `make restart` | Rebuild `bin/agent-watch-bridge` and restart the installed service without rewriting its definition |
| `./bin/agent-watch-bridge restart` | Restart only (also a plugin action) |
| `./bin/agent-watch-bridge start` | Install or rewrite the service definition; keeps the installed values unless a flag or herdr's environment gives new ones |
| `./bin/agent-watch-bridge stop` | Stop the service |
| `./bin/agent-watch-bridge status` | Human status, including the relay's view; exit 1 when the bridge is not running |
| `./bin/agent-watch-bridge status --json --local` | Local files only, no network: what the menu bar reads ([`contracts.md` §6.2](reference/contracts.md)) |
| `sudo agent-watch-relay devices list` / `devices revoke <id>` | On the VPS ([`deploy/relay/README.md`](../deploy/relay/README.md) § Devices) |
| `make bar` | Rebuild the menu bar app (quit the running one and `open bin/AgentWatchBar.app` again) |
| `make check` | Format, vet and test the Go code (`make bar-test` for the menu bar logic) |
| `tools/herdr-overrides/herdr-overrides.sh check` | After a herdr update: are the temporary detection overrides still needed? ([Known issues](#known-issues)) |

---

## Versions and Releases

**Each component has its own version** in [`VERSIONS`](../VERSIONS), whose header says when and how to bump them. The Go binaries add the commit they were built from ([`contracts.md` §3](reference/contracts.md), version strings).

**Where to see them:** `agent-watch-bridge version`, `agent-watch-relay version`, the menu bar's **Versions** section ([`macos-bar/README.md`](../macos-bar/README.md)), and the watch's Settings.

**A release is a dated snapshot of the whole system.** Every push to `main` that changes a released component (bridge, relay, menu bar), `VERSIONS` or the release workflow publishes one automatically, once the Go tests pass. Documentation-only and Wear OS-only changes do not publish releases (the watch app is not part of a release). The workflow chooses `vYYYY.MM.DD` for the first release on a UTC day and `vYYYY.MM.DD.2`, `.3`… for later pushes that day.

The [release workflow](../.github/workflows/release.yml) publishes a GitHub release with:
- `agent-watch-bridge` for macOS (arm64, amd64) and Linux (amd64, arm64), and `agent-watch-relay` for Linux (amd64, arm64), as static binaries;
- `AgentWatchBar.app` (universal) as a zip;
- `SHA256SUMS`, and notes with each component's version and the commits since the previous tag.

The Wear OS app is not attached: it needs your own `google-services.json` and signing key, so build it from source ([step 3](#3-build-and-install-the-wear-os-app)).

**Installing from a release** instead of building:
- Check the download with `shasum -a 256 -c SHA256SUMS --ignore-missing`.
- The macOS binaries and the app are not notarized. A file downloaded with a browser is quarantined: `xattr -d com.apple.quarantine <file>` (or right click → Open for the app).
- The setup guide applies unchanged with the downloaded binary in place of `bin/…`, except the herdr plugin actions, which need the plugin in herdr (`herdr plugin link` on a checkout, or [`herdr plugin install`](#alternative-install-the-bridge-with-herdr)). Run `agent-watch-bridge start` from a herdr pane (it takes the socket from `$HERDR_SOCKET_PATH`) or pass `--socket`.

### Continuous Integration

GitHub Actions, sized for the free plan (macOS minutes count ten times, so macOS jobs run only when their files change):

| Workflow | Runner | When | What |
|---|---|---|---|
| [`ci.yml`](../.github/workflows/ci.yml) | Linux | every push to `main` and pull request | `gofmt`, `go vet`, `go test -race`, static cross-builds; guard and `agent-watch.env` tool tests |
| [`wearos.yml`](../.github/workflows/wearos.yml) | Linux | changes under `wearos-app/` or `VERSIONS` | unit tests, lint and a debug build (with a placeholder `google-services.json`) |
| [`macos-bar.yml`](../.github/workflows/macos-bar.yml) | macOS | changes to the menu bar, the bridge CLI or `VERSIONS` | `make bar-test` and `make bar` |
| [`release.yml`](../.github/workflows/release.yml) | Linux + macOS | changes to released components on `main` (or by hand, as a dry run without publishing) | the release above |

---

## Supported Agents

| Agent | Support | On the watch |
|---|---|---|
| **Claude Code** (`claude`) | First-class | Permissions, plan approval, questions, history |
| **OpenCode** (`opencode`) | First-class | Permissions, questions, history |
| **Antigravity CLI** (`agy`) | First-class, with a herdr gap ([known issues](#known-issues)) | Permissions, history |
| **Any other agent herdr detects** | Generic | Numbered menus, history from the screen |

How each agent's menus, keys and history work: [`docs/reference/agents.md`](reference/agents.md).

---

## Security Model

- **Outbound-only host:** The bridge opens an outbound TLS WebSocket (`wss://relay.<domain>/v1/host`). No inbound ports, listeners, or open firewall holes on your computer.
- **Hashed device tokens:** The relay stores only `sha256(device_token)`. If the relay's store were compromised, existing tokens cannot be reconstructed. Devices are revocable at once (`agent-watch-relay devices revoke`).
- **Rate-limited pairing:** short-lived, single-use codes; attempts are limited per client IP, taken from forwarding headers only when a trusted proxy wrote them ([`contracts.md` §2.1, §5](reference/contracts.md)).
- **Zero raw keys:** The watch client never sends raw key presses or terminal input. The watch sends high-level actions (`answer` with option ID, `cancel`, or `prompt` text). The host bridge maps actions to keys itself.
- **Strict safety checks:** Right before pressing keys, the bridge re-lists the agent from herdr and re-reads the screen, and verifies:
  1. The pane has a detected agent.
  2. `expected_seq` matches the agent's current state.
  3. The prompt fingerprint matches what is parsed on the screen now (for `cancel` too).
- **Once only:** a repeated request, a second answer to the same prompt, or the same dictated text sent twice is refused, so a double tap never types twice.
- **No typing into menus:** dictation is refused while a menu is on screen, whatever herdr reports.
- **Sanitized logs:** The relay and bridge logs never print prompt contents, transcript text, or authentication tokens.
- **One secret file on the host:** `agent-watch.env` is `0600` and git-ignored, and the guards refuse to commit it. `make config` generates the token without printing it; `configure --env-file` and `deploy.sh --sync-env` read it from the file (never from argv) and print key names only.

---

## Known Issues

- **herdr misses some open permission dialogs** (Antigravity CLI's, and Claude's after a relaunch in the same pane), so the watch cannot answer them without a workaround. **Workaround:** the temporary local detection overrides in [`tools/herdr-overrides/`](../tools/herdr-overrides/README.md), which explains the cause, how to install them and when to remove them. While a dialog is open, dictation to that agent is refused.
- **OpenCode approvals need the default focus.** If someone moves the focus in OpenCode's dialog at the computer (arrow keys or mouse hover), the watch's Allow is refused with nothing pressed: answer at the computer. Deny always works. Why: [`agents.md` §5.1](reference/agents.md).
- **The watchOS app is not on the relay API yet** ([status](STATUS.md)).
- **An approval answered at the computer can stay in the watch's notifications** until the app next sees the live state (e.g. when you open it). The `resolved` push that withdraws them is off by default (`AW_PUSH_RESOLVED`). The current app handles it; whether the relay has it on yet is in [STATUS](STATUS.md).

---

## Troubleshooting

- **Check status anytime:**
  ```bash
  ./bin/agent-watch-bridge status
  # or: herdr plugin action invoke --plugin herdr-agent-watch status
  ```
- **Log files:**
  - Bridge logs: `~/Library/Logs/agent-watch-bridge.log` (macOS), `journalctl --user -u agent-watch-bridge` (Linux)
  - Relay logs: `journalctl -u agent-watch-relay -f` on the VPS
- **"Device offline" banner:**
  - Displayed on the watch when the host bridge disconnects from the relay (e.g. the computer is asleep, or the bridge stopped). The relay also drops a bridge that stops answering its pings ([`contracts.md` §3](reference/contracts.md)).
  - Existing history remains browsable on the watch.
  - When the computer wakes up, the bridge automatically reconnects and clears the banner without user intervention.
- **"herdr stopped" indicator:**
  - Indicates that the bridge is running but the herdr daemon is not responding on its UNIX socket. Start herdr to restore live monitoring.
- **"Relay offline" on the watch** (or "Can't reach the relay" after a tap): the watch has no internet path to the relay. On Bluetooth only, check the phone itself is online (the watch goes out through it). `curl https://relay.<domain>/v1/healthz` from any network tells whether the relay is up.
- **`status` shows a relay error with "close 4000":** two bridges share the host token (a foreground `run` beside the service, or a second host) and replace each other in a loop. Stop one of them.
- **Relay rejects the host token:** `status` shows the relay error. The relay and the bridge must hold the same `AW_HOST_TOKEN`: give both the one in `agent-watch.env` with `make deploy-relay ARGS=--sync-env` and `make configure-bridge && make restart`.
