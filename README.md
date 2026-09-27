# Agent Watch

Agent Watch connects your wrist to your coding agents. When Claude Code, OpenCode, Antigravity, or other terminal agents need permission, ask a multiple-choice question, or finish a long task, Agent Watch alerts your smartwatch with glanceable context and actionable buttons (Allow, Deny, options, voice dictation). You can step away from the computer while it keeps running your agents: approve commands and follow progress from the watch.

> **Status (2026-09-26): beta, in daily use.** The host bridge, the relay and the redesigned Wear OS app are deployed and checked on a Pixel Watch 2. The full end-to-end run (Phase 5) is still open, and the watchOS app is the legacy client until Phase 6 ([`docs/STATUS.md`](docs/STATUS.md)).

<p align="center">
  <img src="docs/assets/watch-agents-list.png" width="180" alt="Agent list: agents that need you come first" />
  <img src="docs/assets/watch-blocked.png" width="180" alt="A blocked agent asking for permission" />
  <img src="docs/assets/watch-approve.png" width="180" alt="Allow and Deny, with the agent's other options below" />
  <img src="docs/assets/watch-question.png" width="180" alt="Answering an agent's multiple-choice question" />
</p>
<p align="center"><sub>Wear OS app, sample agents in a sandbox workspace.</sub></p>

---

## Architecture

Agent Watch is built around a thin outbound host bridge, a minimal cloud relay, and native wrist clients:

```
┌──────────────────────────────────────────────┐
│  Host (Mac / Linux)                          │
│                                              │
│  [Coding Agents: Claude, OpenCode, AGY, ...] │
│         │ (pty / lifecycle hooks)            │
│         ▼                                    │
│  [herdr socket API ($HERDR_SOCKET_PATH)]     │
│         │ (UNIX socket, JSON-RPC)            │
│         ▼                                    │
│  [agent-watch-bridge (launchd / systemd)]    │
│         ▲ CLI                                │
│  [macOS menu bar app (optional)]             │
└──────────────────────┬───────────────────────┘
                       │
                       │ Outbound TLS WebSocket (WSS)
                       │ (No incoming ports, No VPN)
                       ▼
┌──────────────────────────────────────────────┐
│  Cloud Relay (VPS behind a TLS proxy)        │
│                                              │
│  [agent-watch-relay]                         │
│   ├── State Hub & In-Memory Cache            │
│   ├── Short-Lived Pairing & Device Auth      │
│   ├── SSE Broadcast (/v1/events)             │
│   ├── Push Dispatcher (FCM v1 / ntfy)        │
│   └── History Store (store.json)             │
└──────────────────────┬───────────────────────┘
                       │
                       │ HTTPS / SSE / FCM Push
                       ▼
┌──────────────────────────────────────────────┐
│  Smartwatch Clients                          │
│                                              │
│  • Wear OS (Google Pixel Watch 2 — Primary)  │
│  • watchOS (legacy client until Phase 6)     │
└──────────────────────────────────────────────┘
```

- **Outbound-Only Networking:** The host dials outbound to the relay. The watch never connects directly to your computer, and no VPN is required on the watch.
- **Any internet path should work for the watch:** it only needs to reach the relay over HTTPS and Firebase for push. Wi-Fi, LTE, or the paired phone's connection over Bluetooth (Wear OS routes it through the phone on its own; no companion app of ours is involved). Only Wi-Fi has been checked so far.
- **Sole Source of Truth:** Herdr is the authoritative multiplexer and control plane. Agent Watch interfaces cleanly through Herdr's verified socket API.
- **Any TLS reverse proxy** in front of the relay works (nginx, Caddy, Nginx Proxy Manager; Cloudflare optional). See [`deploy/relay/README.md`](deploy/relay/README.md).

---

## Requirements

1. **Herdr:** `herdr` ≥ 0.9.1 (verified on 0.9.1) on the machine that runs your agents, with its integrations installed for your agents (`herdr integration status`).
2. **Go 1.22+** to build the bridge and the relay (`CGO_ENABLED=0`, static binaries).
3. **Cloud Relay:** a small Linux VPS (1 vCPU, 512 MB RAM) with a public domain (e.g. `relay.example.com`) and a TLS reverse proxy that passes WebSockets and unbuffered SSE.
4. **Smartwatch:**
   - **Wear OS** 3 or later (the app's `minSdk` is 30) — *primary; verified only on a Google Pixel Watch 2*.
   - **watchOS** — *legacy client* (the pre-relay LAN client), not yet on the relay API; Phase 6 rewrites it, simulator-only.
5. **To build the Wear OS app:** JDK 17 (Android Studio's bundled JBR works), the Android SDK, `adb`, and your own **Firebase project** for push.

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

   Every key is explained in [`agent-watch.env.example`](agent-watch.env.example) and [`contracts.md` §7](docs/reference/contracts.md). Syntax: `KEY=value`, no quotes; `#` starts a comment.

**Never commit or paste `agent-watch.env`.** The pre-commit guard refuses it.

### 1. Deploy the Cloud Relay

1. **First time only:** prepare the VPS (service user, systemd unit, TLS proxy) as in [`docs/phases/3c-relay-deploy.md`](docs/phases/3c-relay-deploy.md). Create `/etc/agent-watch-relay/env` from [`deploy/relay/env.example`](deploy/relay/env.example); its token placeholder is replaced in the next step.
2. Build, install and restart the relay, and copy the relay keys of `agent-watch.env` (the host token included) into the server's env file:
   ```bash
   make deploy-relay ARGS=--sync-env
   ```
   - Only the keys your file sets are updated; the server keeps its other lines, and the previous file stays as `env.bak-<time>`. The output names keys, never values.
   - Later code updates: `make deploy-relay`. Add `ARGS=--sync-env` again whenever you change a relay key or the token.
   - Clean tree only; health check with automatic rollback. Operations, the Nginx Proxy Manager-in-docker topology, client IP (`AW_TRUSTED_PROXIES`), devices: [`deploy/relay/README.md`](deploy/relay/README.md).
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
   - `configure` rewrites the whole `config.toml`. Pass optional settings every time, e.g. `make configure-bridge ARGS="--claude-config-dir ~/work/claude-profile --host-name my-mac"`; it warns when it drops one the old config had.
   - Claude profiles in `~/.claude` and `~/.claude-*` are found on their own; `--claude-config-dir` is only for profiles elsewhere.
   - It is a CLI command, not a plugin action. The file is `~/.config/herdr/plugins/config/herdr-agent-watch/config.toml` by default.
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
   # bridge: running (pid …); relay: connected to relay.<domain>; herdr: online; agents: N (M blocked)
   ```

### 3. Build and Install the Wear OS App

1. **Firebase:** create a Firebase project, add an Android app with the package name `com.gabriel.agentwatch` (the app's id; the `google-services.json` must match it), and download its `google-services.json` to `wearos-app/app/`. It is git-ignored; never commit it. The build fails without it.
2. **Build** with JDK 17:
   ```bash
   cd wearos-app
   export JAVA_HOME="/Applications/Android Studio.app/Contents/jbr/Contents/Home"   # or any JDK 17
   ./gradlew :app:testDebugUnitTest :app:assembleDebug
   ```
   The build reads `AW_RELAY_DOMAIN` from `../agent-watch.env` and exposes `https://<domain>` as `BuildConfig.DEFAULT_RELAY_URL`, the pairing screen's default relay URL (`""` without the file). Rebuild after changing the domain.
3. **Connect the watch over wireless debugging.** The Pixel Watch has no USB data connection, so `adb` goes over Wi-Fi (watch and computer on the same network):
   - on the watch: Settings → Developer options → **Wireless debugging** → **Pair new device**;
   - on the computer: `adb pair <ip>:<pair-port> <code>`, then `adb connect <ip>:<port>` (the connect port is shown on the Wireless debugging screen and changes after a reboot).
4. **Install:** `./gradlew :app:installDebug`.
5. **Push on the relay:** copy the Firebase **service-account** JSON to the VPS only (e.g. `/etc/agent-watch-relay/firebase-service-account.json`, `root:agentwatch`, `0640`), set `AW_FCM_CREDENTIALS` to that server path in `agent-watch.env`, and run `make deploy-relay ARGS=--sync-env`. Without it the relay runs with push disabled.

**watchOS (legacy until Phase 6):** `make watchos-config` writes the git-ignored `watchos-app/Config.generated.xcconfig` (relay URL, and `AW_WATCHOS_BUNDLE_ID` if set) for the Phase 6 project. The legacy Xcode project does not use it.

The release build (`assembleRelease`, R8 on) is signed with the debug key: fine for your own watch, not for distribution.

### 4. Pair Your Smartwatch

1. Get a short-lived 6-digit pairing code (valid 5 minutes):
   ```bash
   ./bin/agent-watch-bridge pair
   # or: herdr plugin action invoke --plugin herdr-agent-watch pair
   ```
   It shows the code, its real expiry and the relay URL, and also posts a herdr notification. `pair` needs only the config and the relay, not a running bridge.
2. On the watch, open Agent Watch, check the relay URL (`https://relay.<domain>`, prefilled from `agent-watch.env` at build time; `https://` is added if you leave it out) and enter the code.
3. The watch exchanges the code for a device token (the relay stores only its SHA-256 hash) and registers for push. Your agents appear.

### 5. Optional: macOS Menu Bar App

A small menu bar companion that shows whether the bridge runs and is connected to the relay (a status circle on its icon), shows the versions, and starts, stops, restarts and pairs through the bridge CLI. It never shows agents; that is the watch's job:

```bash
make bar && open bin/AgentWatchBar.app
```

Details: [`macos-bar/README.md`](macos-bar/README.md).

### Moving an Existing Deployment to `agent-watch.env`

For a relay and bridge set up by hand before this file existed. Nothing changes on the server or the Mac until steps 4 and 5.

1. Create the file **without** a new token:
   ```bash
   (umask 077 && cp agent-watch.env.example agent-watch.env)
   ```
2. In an editor (never `cat` or `echo` it), fill in:
   - `AW_HOST_TOKEN`: the token already in use, i.e. `host_token` of the bridge's `config.toml`, the same as `AW_HOST_TOKEN` in the server's `/etc/agent-watch-relay/env`.
   - `AW_RELAY_DOMAIN`, `AW_RELAY_SSH`, `AW_RELAY_SSH_OPTS`.
   - Relay settings: leave them commented to keep the server's values, or uncomment one with the server's current value to manage it from here.
3. `make config`: it must say `AW_HOST_TOKEN is already set (kept)`, and it fixes the mode to `0600`.
4. `make configure-bridge` (plus `ARGS="--claude-config-dir …"` if the old config had some; it warns about dropped settings), then `make restart`. `status` must show the relay connected.
5. `make deploy-relay ARGS=--sync-env`: `AW_HOST_TOKEN` must be listed as **unchanged**. `changed` means the file's token is not the server's; the bridge now uses the file's, so the sync brings the relay in line, but check you copied the right one.

### Day-to-day

| Command | What it does |
|---|---|
| `make config` | Create `agent-watch.env` from the example, fix its mode to `0600`, generate `AW_HOST_TOKEN` if empty (never replaces one) |
| `make configure-bridge` | Rewrite the bridge's `config.toml` from `agent-watch.env`; then `make restart` |
| `make deploy-relay [ARGS=--sync-env]` | Build and deploy the relay to `AW_RELAY_SSH`; `--sync-env` also updates the server's env file from `agent-watch.env` |
| `make restart` | Rebuild `bin/agent-watch-bridge` and restart the installed service without rewriting its definition |
| `./bin/agent-watch-bridge restart` | Restart only (also a plugin action) |
| `./bin/agent-watch-bridge start` | Install or rewrite the service definition; keeps the installed values unless a flag or herdr's environment gives new ones |
| `./bin/agent-watch-bridge stop` | Stop the service |
| `./bin/agent-watch-bridge status` | Human status, including the relay's view; exit 1 when the bridge is not running |
| `./bin/agent-watch-bridge status --json --local` | Local files only, no network: what the menu bar reads ([`contracts.md` §6.2](docs/reference/contracts.md)) |
| `sudo agent-watch-relay devices list` / `devices revoke <id>` | On the VPS; works with the relay running, and a revoke takes effect at once |
| `make bar` | Rebuild the menu bar app (quit the running one and `open bin/AgentWatchBar.app` again) |
| `make check` | Format, vet and test the Go code (`make bar-test` for the menu bar logic) |
| `tools/herdr-overrides/herdr-overrides.sh check` | After a herdr update: are the temporary detection overrides still needed? ([Known issues](#known-issues)) |

---

## Versions and Releases

**Each component has its own version,** all in one file, [`VERSIONS`](VERSIONS) at the repo root:

| Key | Component |
|---|---|
| `BRIDGE_VERSION` | `agent-watch-bridge` and the herdr plugin manifest |
| `RELAY_VERSION` | `agent-watch-relay` |
| `MENUBAR_VERSION` | the macOS menu bar app |
| `WEAROS_VERSION_NAME` / `WEAROS_VERSION_CODE` | the Wear OS app (the code must go up for every build installed over an older one) |

- Bump only the component that changed, with semantic versioning. The builds (`make`, the herdr plugin build, Gradle, `macos-bar/build.sh`, the deploy script) read the file.
- The one copy elsewhere is `version` in `herdr-plugin.toml` (herdr cannot read the file): keep it equal to `BRIDGE_VERSION`. `make check-versions` (also part of `go test`) fails when they differ.
- The Go binaries add the commit they were built from: `0.3.0 (26b4e93)`, or `0.3.0 (26b4e93, modified)` when built with uncommitted changes.
- **Where to see them:** `agent-watch-bridge version`, `agent-watch-relay version`, the menu bar's **Versions** section (menu bar, bridge, and the relay while the bridge is connected), and the watch's Settings.

**A release is a dated snapshot of the whole system.** Tag the commit with the date, `vYYYY.MM.DD` (`.2`, `.3`… for more on the same day), and push the tag:

```bash
git tag v2026.09.26 && git push origin v2026.09.26
```

The [release workflow](.github/workflows/release.yml) then publishes a GitHub release with:
- `agent-watch-bridge` for macOS (arm64, amd64) and Linux (amd64, arm64), and `agent-watch-relay` for Linux (amd64, arm64), as static binaries;
- `AgentWatchBar.app` (universal) as a zip;
- `SHA256SUMS`, and notes with each component's version and the commits since the previous tag.

The Wear OS app is not attached: it needs your own `google-services.json` and signing key, so build it from source ([step 3](#3-build-and-install-the-wear-os-app)).

**Installing from a release** instead of building:
- Check the download with `shasum -a 256 -c SHA256SUMS --ignore-missing`.
- The macOS binaries and the app are not notarized. A file downloaded with a browser is quarantined: `xattr -d com.apple.quarantine <file>` (or right click → Open for the app).
- The setup guide applies unchanged with the downloaded binary in place of `bin/…`, except the herdr plugin actions, which need a checkout (`herdr plugin link`). Run `agent-watch-bridge start` from a herdr pane (it takes the socket from `$HERDR_SOCKET_PATH`) or pass `--socket`.

### Continuous Integration

GitHub Actions, sized for the free plan (macOS minutes count ten times, so macOS jobs run only when their files change):

| Workflow | Runner | When | What |
|---|---|---|---|
| [`ci.yml`](.github/workflows/ci.yml) | Linux | every push to `main` and pull request | `gofmt`, `go vet`, `go test -race`, static cross-builds; guard and `agent-watch.env` tool tests |
| [`wearos.yml`](.github/workflows/wearos.yml) | Linux | changes under `wearos-app/` or `VERSIONS` | unit tests, lint and a debug build (with a placeholder `google-services.json`) |
| [`macos-bar.yml`](.github/workflows/macos-bar.yml) | macOS | changes to the menu bar, the bridge CLI or `VERSIONS` | `make bar-test` and `make bar` |
| [`release.yml`](.github/workflows/release.yml) | Linux + macOS | a `v*` tag (or by hand, as a dry run without publishing) | the release above |

---

## Supported Agents

All agent-specific approval menu parsing and key mappings live in `pkg/agents` ([`docs/reference/agents.md`](docs/reference/agents.md)):

| Agent | Status | Approval UI | Key Mapping | Transcript History |
|---|---|---|---|---|
| **Claude Code** (`claude`) | First-class | Numbered permission menu, plan approval, AskUserQuestion | Digit direct | JSONL tail reader + screen fallback |
| **OpenCode** (`opencode`) | First-class | Framed button bar (`Allow once`, `Allow always`, `Reject`); question tool | `Enter` / `Right, Enter, Enter` (confirm stage) / `esc` | SQLite (`opencode.db`, read-only) + screen fallback |
| **Antigravity CLI** (`agy`) | First-class, with a herdr gap (Known issues) | Numbered box menu (`Command`, `Pending edit`) | Digit direct | JSONL + screen fallback |
| **Generic** | Universal fallback | Any numbered list matching `^\d+[.)]` | Digit direct | Screen capture |

For claude, agy and opencode a menu counts only while its dialog is open, so a numbered list in an answer is never taken for a menu.

---

## Security Model

- **Outbound-only host:** The bridge opens an outbound TLS WebSocket (`wss://relay.<domain>/v1/host`). No inbound ports, listeners, or open firewall holes on your computer.
- **Hashed device tokens:** The relay stores only `sha256(device_token)`. If the relay's store were compromised, existing tokens cannot be reconstructed. Devices are revocable at once (`agent-watch-relay devices revoke`).
- **Rate-limited pairing:** 6-digit codes, 5-minute TTL, single use; attempts are limited per client IP, which the relay takes from forwarding headers only when they come from a proxy listed in `AW_TRUSTED_PROXIES`.
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

- **herdr 0.9.1 misses some open permission dialogs** (reports `done`/`idle`/`working` instead of `blocked`): every Antigravity CLI 1.2.x dialog, and any Claude Code dialog after Claude was relaunched in the same pane. The bridge only publishes prompts for `blocked` agents, so without a workaround the watch cannot answer them. **Workaround:** temporary local detection overrides for herdr in [`tools/herdr-overrides/`](tools/herdr-overrides/README.md) (`herdr-overrides.sh install`; run `check` after herdr updates its manifests and `uninstall` once upstream handles these dialogs). While a dialog is open, dictation to that agent is refused ("Answer the question first").
- **OpenCode approvals need the default focus.** OpenCode's keys act on the focused button, and focus starts on `Allow once`. Before pressing, the bridge reads the screen with colours and checks the focus is still there; if someone moved it at the computer (arrow keys or mouse hover), the watch's answer is refused with nothing pressed, and must be given at the computer. Reject (`esc`) never depends on focus.
- **watchOS is the legacy client.** The Apple Watch app does not use the relay API yet; Phase 6 rewrites it (simulator-only, best-effort).
- **An approval answered at the computer can stay in the watch's notifications** until the app next sees the live state (e.g. when you open it). The `resolved` push that withdraws them is off by default (`AW_PUSH_RESOLVED`). The installed app already handles it; turning it on is part of the Phase 5 end-to-end run.

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
- **"Mac offline" banner:**
  - Displayed on the watch when the host bridge disconnects from the relay (e.g. the computer is asleep, or the bridge stopped). The relay also pings the bridge and drops a silent one within about 40 s.
  - Existing history remains browsable on the watch.
  - When the computer wakes up, the bridge automatically reconnects and clears the banner without user intervention.
- **"herdr stopped" indicator:**
  - Indicates that the bridge is running but the herdr daemon is not responding on its UNIX socket. Start herdr to restore live monitoring.
- **"Can't reach the relay" on the watch:** the watch has no internet path to the relay. On Bluetooth only, check the phone itself is online (the watch goes out through it). `curl https://relay.<domain>/v1/healthz` from any network tells whether the relay is up.
- **Relay rejects the host token:** `status` shows the relay error. The relay and the bridge must hold the same `AW_HOST_TOKEN`: give both the one in `agent-watch.env` with `make deploy-relay ARGS=--sync-env` and `make configure-bridge && make restart`.

---

## Documentation

- [`HERDR_REFACTOR_PLAN.md`](HERDR_REFACTOR_PLAN.md): the design.
- [`docs/README.md`](docs/README.md): phase guides and references; [`docs/STATUS.md`](docs/STATUS.md): what is done and verified.
- [`docs/reference/contracts.md`](docs/reference/contracts.md): every JSON shape, the relay's and the bridge's configuration, and `agent-watch.env` (§7).
- [`ROADMAP.md`](ROADMAP.md): what comes next, including an Android phone client on the same relay ([Phase 7](docs/phases/7-android-mobile.md)).

---

## License

Copyright 2026 Gabriel Marcano. Licensed under the [Apache License, Version 2.0](LICENSE); see also [`NOTICE`](NOTICE).

Contributions are accepted under the same license. The license does not grant rights to the "Agent Watch" name (section 6).
