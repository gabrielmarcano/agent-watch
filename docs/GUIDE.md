# Agent Watch — Full Guide

How to set up, run and troubleshoot Agent Watch, in detail. The [README](../README.md) is the short version.

**Contents:** [Requirements](#requirements) · [Setup](#setup-guide) · [Day-to-day](#day-to-day) · [Versions and releases](#versions-and-releases) · [Supported agents](#supported-agents) · [Security model](#security-model) · [Known issues](#known-issues) · [Troubleshooting](#troubleshooting)

---

## Requirements

The minimums below come from the build files named with each one; those files win if this list lags.

1. **herdr-core** (`hc`) 0.1.14 or later (or Herdr.app) on the machine that runs your agents.
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

On the machine that runs herdr-core:

1. Build the bridge binary:
   ```bash
   make bridge
   ```
2. Write the bridge config from `agent-watch.env` (relay URL and host token; `config.toml`, mode `0600`):
   ```bash
   make configure-bridge
   # = ./bin/agent-watch-bridge configure --env-file agent-watch.env
   ```
   - Optional settings go in `ARGS`, e.g. `make configure-bridge ARGS="--host-name my-mac"`; pass them again on every run. What `configure` writes, where, and its flags: [`contracts.md` §6](reference/contracts.md).
   - `--claude-config-dir` is only for Claude profiles the bridge does not find on its own ([`agents.md` §3.2](reference/agents.md)).
   - To let the phone start new agent sessions on this machine, name the folders and agent kinds it may use (repeatable; no root means the feature is off): `make configure-bridge ARGS="--session-root ~/Code --session-kind claude --session-kind opencode"`. What the policy does: [`contracts.md` §2.1](reference/contracts.md).
   - Without the file: `./bin/agent-watch-bridge configure --relay-url wss://relay.<domain> --host-token <64-hex-token>` (the token then shows in `ps`).
3. Start the bridge service (a LaunchAgent on macOS, a `systemd --user` unit on Linux):
   ```bash
   ./bin/agent-watch-bridge start
   ```
   Already installed? After `make configure-bridge`, apply the new config with `make restart`.
   On Linux, a `systemd --user` unit stops when you log out unless lingering is on: `loginctl enable-linger` (a server you only reach over SSH needs it).
4. Verify the bridge:
   ```bash
   ./bin/agent-watch-bridge status
   # bridge:   running (pid …); launchd: running (pid …)
   # relay:    connected to relay.<domain>
   # herdr:    online
   # agents:   N (M blocked)
   # …then the config, status and log paths
   ```

### Another Machine (several hosts)

Each machine with herdr runs its own bridge, with **its own host token**; the watch shows one page per machine (relay `0.7.0`, Wear OS `1.4.0`).

**Install Wear OS `1.4.0` on the watch first.** An older app mixes up the agents of two machines that share a pane id (herdr numbers each machine's panes alike), and keeps doing so until the relay restarts.

1. On the VPS, register the machine and copy the token it prints once: `sudo agent-watch-relay hosts add <id> --name "<name>"` ([`deploy/relay/README.md` § Hosts](../deploy/relay/README.md#hosts-one-per-machine)).
2. On that machine, clone the repo, create its `agent-watch.env` with `make config`, replace its `AW_HOST_TOKEN` with that token, and set `AW_HOST_ID=<id>`. Deploy and sync the relay only from the first machine: `make deploy-relay ARGS=--sync-env` refuses another machine's file, which would hand the relay its token.
3. Install and start its bridge as in [step 2](#2-install-and-configure-the-host-bridge). Linux runs it as a `systemd --user` unit. Without Go on that machine, put a [release](#versions-and-releases)'s bridge for it in the checkout's `bin/` instead of `make bridge`; `herdr plugin link` and the plugin actions work the same.

The first machine's token, `AW_HOST_TOKEN` in the relay's env file, keeps working as the host `AW_HOST_ID` (default `main`). The phone lists every machine too (one filter, not pages), and can start a session on any of them whose bridge is `0.9.0` and has session roots (below).

### 3. Build and Install the Android Phone App

The phone is its own relay client, with its own device token: pairing it does not touch the watch, and `agent-watch-relay devices revoke <id>` cuts only the phone. New sessions need relay `0.9.0` and bridge `0.9.0`.

1. **Build** with the JDK from [Requirements](#requirements):
   ```bash
   cd wearos-app
   export JAVA_HOME="/Applications/Android Studio.app/Contents/jbr/Contents/Home"
   ./gradlew :mobile:assembleDebug
   ```
   **Push (optional):** in the same Firebase project as the watch, add another Android app with the package name `com.gabriel.agentwatch.mobile`, download its `google-services.json` to `wearos-app/mobile/`, and build again. It is git-ignored; never commit it. Without that file the app builds and works, it just has no notifications. The relay's FCM service account ([step 4](#4-build-and-install-the-wear-os-app), item 5) sends to both apps.
2. **Install** it on the phone with the phone connected over USB: `./gradlew :mobile:installDebug` (or `adb install -r mobile/build/outputs/apk/debug/mobile-debug.apk`). For updates that install without USB later, install a release-signed build once instead ([Android updates](#android-updates-phone-and-watch)); a debug build cannot be replaced by the release-signed APK.
3. **Pair:** `./bin/agent-watch-bridge pair`, then enter the relay URL (prefilled from `agent-watch.env` at build time) and the code. The phone keeps its device token in its private app storage (backup off); the relay stores only its hash.
4. **Allow it to start agents** (bridge `0.9.0`): on each machine, name the folders and agent kinds the phone may use, then apply the config:
   ```bash
   make configure-bridge ARGS="--session-root ~/Code --session-root ~/Documents --session-kind claude --session-kind opencode"
   make restart
   ```
   With no `--session-root`, that machine starts no sessions and the phone says so. The folder browser cannot leave the roots, and a folder or kind outside them is refused (`not_allowed`, [`contracts.md` §2.1](reference/contracts.md)). The first session in a folder creates one herdr workspace for it; each session gets its own tab and agent, and nothing takes focus on that machine.

What the app does today: agents as chats, one list filterable by machine, "Needs you" first, unread dots; a chat shows the agent's turns as a conversation with the approval menu inline, typed replies, and the history reader; "+" starts a session on a chosen machine, agent kind and folder, then opens its chat as soon as herdr reports it. With `mobile/google-services.json` in place it also posts local notifications, one chat per agent, grouped like a messaging app: tapping one opens that chat, and replies and approvals can be sent from the notification. Notifications stay on the phone (Android does not mirror them to the watch); Allow and a question's answer need the phone unlocked, while Deny, Cancel and a reply do not. A newer published build appears as an update card at the top of the chat list ([Android updates](#android-updates-phone-and-watch)). No settings screen yet: [STATUS](STATUS.md). Release builds use the key in `wearos-app/keystore.properties` when it exists; without it they are debug-signed, and `make publish-android` refuses to publish them.

### 4. Build and Install the Wear OS App

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
4. **Install:** `./gradlew :app:installDebug`. For updates that install from the watch later, install a release-signed build once instead ([Android updates](#android-updates-phone-and-watch)); a debug build cannot be replaced by the release-signed APK.
5. **Push on the relay:** copy the Firebase **service-account** JSON to the VPS only, as [`deploy/relay/README.md` § First-time setup](../deploy/relay/README.md#first-time-setup-once-per-vps) says, set `AW_FCM_CREDENTIALS` to its server path in `agent-watch.env`, and run `make deploy-relay ARGS=--sync-env`. Without it the relay runs with push disabled.

**watchOS** ([status](STATUS.md)): `make watchos-config` writes the git-ignored `watchos-app/Config.generated.xcconfig` ([`contracts.md` §7](reference/contracts.md)).

The release build (`assembleRelease`, R8 on) uses the key in `wearos-app/keystore.properties` when it exists; without it, it is debug-signed, which is fine for a throwaway build but cannot be updated by a published APK. See [Android updates](#android-updates-phone-and-watch).

### Android updates (phone and watch)

The relay serves the latest APK of each Android app, so later versions install from the phone or the watch without USB or the same Wi-Fi. Android still asks you to confirm each install, and the first one asks you to allow Agent Watch as an install source; that permission is once per app.

**1. Create the release key once.** Every published build must use the same key: Android refuses an APK signed by another key, and the only fix is uninstalling the app (losing its pairing and local state). In `wearos-app/`:

```bash
keytool -genkeypair -v -keystore agent-watch-release.jks \
  -alias agent-watch -keyalg RSA -keysize 4096 -validity 10000
chmod 600 agent-watch-release.jks
```

Create `wearos-app/keystore.properties` next to it (git-ignored; keep both backed up somewhere safe). Values are bare, without quotes; `storeFile` is relative to `wearos-app/`, or absolute:

```
storeFile=agent-watch-release.jks
storePassword=<the keystore password>
keyAlias=agent-watch
keyPassword=<the key password>
```

Both apps use this key. Without the file, release builds fall back to the debug key so tests still run; `make publish-android` refuses a debug-signed APK.

**2. Install a release-signed build once.** A debug-signed app cannot be replaced by a release-signed one. Build and install the release APKs on each device:

```bash
cd wearos-app
export JAVA_HOME="/Applications/Android Studio.app/Contents/jbr/Contents/Home"
./gradlew :mobile:assembleRelease :app:assembleRelease

# Phone, over USB:
adb install -r mobile/build/outputs/apk/release/mobile-release.apk

# Watch, over the wireless adb from step 4:
adb install -r app/build/outputs/apk/release/app-release.apk
```

If a debug-signed build is already installed, uninstall it first (`adb uninstall com.gabriel.agentwatch.mobile` on the phone, `adb uninstall com.gabriel.agentwatch` on the watch) and pair again. The owner's watch `1.3.1` needs that one-time step to leave the debug key.

**3. Bump the versions and publish.** Increase `MOBILE_VERSION_CODE` and/or `WEAROS_VERSION_CODE` in [`VERSIONS`](../VERSIONS) (a published code can never go down), commit the version change, then from a clean tree:

```bash
make publish-android
```

It builds both release APKs, checks that each one is release-signed, and publishes only the apps whose `version_code` increased: `<app>-<code>.apk` and `<app>.json` in the relay's `AW_UPDATE_DIR` (default `/var/lib/agent-watch-relay/updates`; [`deploy/relay/README.md`](../deploy/relay/README.md) § Android updates). The relay reads the metadata on every request, so no restart is needed. It needs root over SSH (it chowns the files to the relay's `agentwatch` user) and `AW_RELAY_SSH` in `agent-watch.env`, like `make deploy-relay`.

To publish one app only — the other is neither built nor touched on the relay — run the script with `--phone` or `--watch` (it needs the same environment as the make target):

```bash
AW_ENV_FILE=agent-watch.env deploy/relay/publish-android.sh --phone
```

**4. Update from the device.** The phone checks when it starts and after pairing; the watch checks on start and from Settings → **Check for updates**. A newer build shows an update card: **Update** downloads it (the card shows progress and can be cancelled), verifies its size and SHA-256, then **Install** opens Android's installer. If Android asks to allow installs from Agent Watch, accept it; the card then offers **Install** again. The watch installer is not verified on a device yet: if a watch has no way to allow installs or no installer, update that watch over `adb` (and report it in [STATUS](STATUS.md)).

### 5. Pair Your Smartwatch

1. Get a short-lived pairing code:
   ```bash
   ./bin/agent-watch-bridge pair
   ```
   It shows the code, its real expiry and the relay URL, and also posts a notification.
2. On the watch, open Agent Watch, check the relay URL (`https://relay.<domain>`, prefilled from `agent-watch.env` at build time; `https://` is added if you leave it out) and enter the code.
3. The watch exchanges the code for a device token (the relay stores only its SHA-256 hash) and registers for push. Your agents appear.

**Only Notify When Away** (off by default; macOS only for now): turn it on in the menu bar app, or with `agent-watch-bridge presence on`. While you use the computer (input in the last 10 minutes by default, screen unlocked), the watch does not buzz; a prompt still waiting when you leave is pushed then. While it is on, the bridge sends your relay the input idle time and screen-lock state every 15 s. Rules and the relay setting: [`contracts.md` §4.3](reference/contracts.md).

### 6. Optional: macOS Menu Bar App

`make bar && open "bin/Agent Watch.app"`: a menu bar companion for the bridge. What it shows and does: [`macos-bar/README.md`](../macos-bar/README.md).

### Day-to-day

| Command | What it does |
|---|---|
| `make config` | Create `agent-watch.env` and its host token ([step 0](#0-create-your-configuration-file)) |
| `make configure-bridge` | Rewrite the bridge's `config.toml` from `agent-watch.env`; then `make restart` |
| `make deploy-relay [ARGS=--sync-env]` | Build and deploy the relay to `AW_RELAY_SSH`; `--sync-env` also updates the server's env file from `agent-watch.env` |
| `make publish-android` | Build, sign and publish both Android APKs to the relay ([Android updates](#android-updates-phone-and-watch)) |
| `make restart` | Rebuild `bin/agent-watch-bridge` and restart the installed service without rewriting its definition |
| `./bin/agent-watch-bridge restart` | Restart the installed service |
| `./bin/agent-watch-bridge start` | Install or rewrite the service definition; keeps the installed values unless flags or environment override them |
| `./bin/agent-watch-bridge stop` | Stop the service; it stays stopped across logins and reboots until the next `start` |
| `./bin/agent-watch-bridge status` | Human status, including the relay's view; exit 1 when the bridge is not running |
| `./bin/agent-watch-bridge status --json --local` | Local files only, no network: what the menu bar reads ([`contracts.md` §6.2](reference/contracts.md)) |
| `sudo agent-watch-relay devices list` / `devices revoke <id>` | On the VPS ([`deploy/relay/README.md`](../deploy/relay/README.md) § Devices) |
| `make bar` | Fast rebuild of the menu bar app (quit the running one and `open "bin/Agent Watch.app"` again) |
| `make app` | Build self-contained `Agent Watch.app` with embedded universal bridge helper |
| `make dist-mac` | Package the self-contained app into both `.zip` and `.dmg` in `bin/` |
| `make check` | Format, vet and test the Go code (`make bar-test` for the menu bar logic) |

---

## Versions and Releases

**Each component has its own version** in [`VERSIONS`](../VERSIONS), whose header says when and how to bump them. The Go binaries add the commit they were built from ([`contracts.md` §3](reference/contracts.md), version strings).

**Where to see them:** `agent-watch-bridge version`, `agent-watch-relay version`, the menu bar's **Versions** section ([`macos-bar/README.md`](../macos-bar/README.md)), and the watch's Settings.

**A release is a dated snapshot of the whole system.** Every push to `main` that changes a released component (bridge, relay, menu bar), `VERSIONS` or the release workflow publishes one automatically, once the Go tests pass. Documentation-only and Wear OS-only changes do not publish releases (the watch app is not part of a release). The workflow chooses `vYYYY.MM.DD` for the first release on a UTC day and `vYYYY.MM.DD.2`, `.3`… for later pushes that day.

The [release workflow](../.github/workflows/release.yml) publishes a GitHub release with:
- `agent-watch-bridge` for macOS (arm64, amd64) and Linux (amd64, arm64), and `agent-watch-relay` for Linux (amd64, arm64), as static binaries;
- `Agent Watch.app` (universal, self-contained with embedded bridge helper) as a DMG (`AgentWatchBar_<version>_macos_universal.dmg`) and a zip (`AgentWatchBar_<version>_macos_universal.zip`);
- `SHA256SUMS`, and notes with each component's version and the commits since the previous tag.

The Android apps (Wear OS and phone) are not attached: they need your own `google-services.json` and signing key, so build them from source (steps [3](#3-build-and-install-the-android-phone-app) and [4](#4-build-and-install-the-wear-os-app)). To update installed copies without USB, publish them to the relay ([Android updates](#android-updates-phone-and-watch)).

**Installing from a release** instead of building:
- Check the download with `shasum -a 256 -c SHA256SUMS --ignore-missing`.
- The macOS binaries and the app are not notarized. A file downloaded with a browser is quarantined: `xattr -d com.apple.quarantine <file>` (or right click → Open for the app).
- The setup guide applies unchanged with the downloaded binary in place of `bin/…`. Run `agent-watch-bridge start` from a herdr-core environment or pass `--socket`.

### Continuous Integration

GitHub Actions, sized for the free plan (macOS minutes count ten times, so macOS jobs run only when their files change):

| Workflow | Runner | When | What |
|---|---|---|---|
| [`ci.yml`](../.github/workflows/ci.yml) | Linux | every push to `main` and pull request | `gofmt`, `go vet`, `go test -race`, static cross-builds; guard and `agent-watch.env` tool tests |
| [`wearos.yml`](../.github/workflows/wearos.yml) | Linux | changes under `wearos-app/` or `VERSIONS` | unit tests, lint and debug builds of both Android clients — the Wear OS app and the phone app — with a placeholder `google-services.json` |
| [`macos-bar.yml`](../.github/workflows/macos-bar.yml) | macOS | changes to the menu bar, the bridge CLI or `VERSIONS` | `make bar-test` and `make bar` |
| [`release.yml`](../.github/workflows/release.yml) | Linux + macOS | changes to released components on `main` (or by hand, as a dry run without publishing) | the release above |

---

## Supported Agents

| Agent | Support | On the watch |
|---|---|---|
| **Claude Code** (`claude`) | First-class | Permissions, plan approval, questions, history |
| **OpenCode** (`opencode`) | First-class | Permissions, questions, history |
| **Antigravity CLI** (`agy`) | First-class | Permissions, history |
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
- **Approving from the phone needs an unlock:** notification actions that grant something (Allow, or a question's answer) are refused while the phone is locked; Deny, Cancel and a reply stay available. The phone's notifications are built locally, marked local-only, and never mirrored to the watch.
- **Sanitized logs:** The relay and bridge logs never print prompt contents, transcript text, or authentication tokens.
- **One secret file on the host:** `agent-watch.env` is `0600` and git-ignored, and the guards refuse to commit it. `make config` generates the token without printing it; `configure --env-file` and `deploy.sh --sync-env` read it from the file (never from argv) and print key names only.

---

## Known Issues

- **OpenCode approvals need the default focus.** If someone moves the focus in OpenCode's dialog at the computer (arrow keys or mouse hover), the watch's Allow is refused with nothing pressed: answer at the computer. Deny always works. Why: [`agents.md` §5.1](reference/agents.md).
- **The watchOS app is not on the relay API yet** ([status](STATUS.md)).
- **An approval answered at the computer can stay in the watch's or phone's notifications** until the app next sees the live state (e.g. when you open it). The `resolved` push that withdraws them is off by default (`AW_PUSH_RESOLVED`). The current apps handle it; whether the relay has it on yet is in [STATUS](STATUS.md).

---

## Troubleshooting

- **Check status anytime:**
  ```bash
  ./bin/agent-watch-bridge status
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
- **`status` shows a relay error with "close 4000":** two bridges use the same host token (a foreground `run` beside the service, or a second machine given the first one's token) and replace each other in a loop. Stop one, or give the second machine its own token ([Another Machine](#another-machine-several-hosts)).
- **Relay rejects the host token:** `status` shows the relay error. The relay and the bridge must hold the same `AW_HOST_TOKEN`: give both the one in `agent-watch.env` with `make deploy-relay ARGS=--sync-env` and `make configure-bridge && make restart`. If the relay log says `AW_HOST_TOKEN is the token of a registered host`, another machine's token reached the relay's env file: put the first machine's own token back in its `agent-watch.env` and sync from there.
