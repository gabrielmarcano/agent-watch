# Agent Guidelines (`AGENTS.md`)

Rules for every AI agent working in this repository: Claude Code, Antigravity, OpenCode, Codex, Cursor and others. This file holds only what applies everywhere. Everything else has **exactly one home**, listed in §0.

**Never copy a fact from its home into another doc: link to it.** When you change a fact, change it in its home and remove any copy you find (replace it with a link). The code wins over any doc. Two docs that disagree mean one of them holds a copy: fix that in the same commit.

---

## 0. Where Everything Lives

| What | Its one home |
|---|---|
| State: phases, claims (also for work outside a phase), open items, blockers, next steps, what is deployed | [`docs/STATUS.md`](docs/STATUS.md) |
| Guides for the open phases | [`docs/phases/`](docs/phases/) |
| Every JSON shape, endpoint, error code, push payload, config key, machine-readable CLI output (`--json`, version strings), and product env vars (relay `AW_*`, `agent-watch.env` keys) | [`docs/reference/contracts.md`](docs/reference/contracts.md) |
| Timeouts, limits and intervals | the doc of their layer: `contracts.md` (relay, bridge, watch protocol; pairing and history limits), `herdr-socket-api.md` (herdr side and the bridge's herdr policy), `agents.md` (transcript reads and screen captures), `wearos-app/ARCHITECTURE.md` (the watch app's own timers); other components: their README (`macos-bar/README.md`, `deploy/relay/README.md`) |
| The herdr socket: methods, events, keys, verified behaviour, and the bridge's herdr policy | [`docs/reference/herdr-socket-api.md`](docs/reference/herdr-socket-api.md) |
| Per agent: menus, option roles, keys, transcripts | [`docs/reference/agents.md`](docs/reference/agents.md) |
| Wear OS internals, client rules, screens, notifications and UX decisions | [`wearos-app/ARCHITECTURE.md`](wearos-app/ARCHITECTURE.md) |
| Exact texts the watch shows | the code: `wearos-app/app/src/main/res/values/strings.xml`, `approval/CommandFeedback.kt`, and the notification builders in `network/AgentNotifications.kt`, `MyFirebaseMessagingService.kt`, `NotificationActionReceiver.kt` |
| The pitch and a quick start: a short version of `docs/GUIDE.md`'s setup, kept on purpose (the owner's decision) | [`README.md`](README.md) |
| Installing, operating and troubleshooting the bridge and the watch app; the tool minimums users need; releases and CI; security model; known issues (symptom and workaround only) | [`docs/GUIDE.md`](docs/GUIDE.md) |
| Relay operations: VPS setup, deploys, rollback, the relay CLI, proxy, devices, backups | [`deploy/relay/README.md`](deploy/relay/README.md) |
| macOS menu bar app | [`macos-bar/README.md`](macos-bar/README.md) |
| What the guards block, their setup and tests | [`tools/guards/README.md`](tools/guards/README.md) |
| The herdr dialog-detection gap and its temporary overrides | [`tools/herdr-overrides/README.md`](tools/herdr-overrides/README.md) |
| Component versions and when to bump them | [`VERSIONS`](VERSIONS) |
| Toolchain versions | the build files: `go.mod`, the Gradle files, `herdr-plugin.toml` (`min_herdr_version`) |
| Tool-specific env vars (`SSH_OPTS`, `APP_DIR`, guard escape hatches, `HERDR_*`) | the doc of that tool |
| Repo layout | §2 |
| Rules per area, with their build and test commands | `.agents/rules/*.md` (§4) |
| Procedures | `.agents/skills/*/SKILL.md`: `capture-fixture` (capture and audit agent CLIs; the checklist after a herdr upgrade), `add-agent-adapter`, `schema-sync`, `herdr-probe`, `wearos-deploy`, `relay-deploy` |
| Reporting a vulnerability | [`SECURITY.md`](SECURITY.md) |

**To start work:** claim it in `docs/STATUS.md` when its workflow line asks for a claim (not every fix needs one), then read the phase's guide or the rule and skill for the area.

> ⚠️ **The herdr on the development Mac runs the owner's real agent sessions.** Before any herdr command, read `.agents/rules/herdr-integration.md` § Safety.

---

## 1. Prime Directives

### 1.1 herdr is the control plane (zero legacy)
- **Strictly prohibited:**
  - Warp-specific code
  - AppleScript keystrokes (`osascript`)
  - the Claude Code plugin and hook webhooks (`claude-plugin/`, `/webhook`)
  - legacy single-agent endpoints (`/state`, `/input`)
  - the Node.js bridge
  - file-tailing sidecars (`agy-sidecar.js`)
- herdr (the socket API at `$HERDR_SOCKET_PATH`) is the **sole source of truth** for agent detection, lifecycle status, and input/key delivery. Talk to the socket directly; do not shell out to the `herdr` CLI from Go. Why: the socket already offers list, read, prompt, send_keys and events, with typed errors and no subprocess per call.
- Do **NOT** add backward-compatibility shims, dual-write adapters, or fallback modes for non-herdr environments. An agent running outside herdr is out of scope.
- Reading an agent's **own transcript** for history is allowed, under three conditions:
  - it happens inside a `pkg/agents` adapter;
  - it is triggered on demand by a herdr status transition, or, only while herdr reports the pane `working`, by a periodic bounded check for a turn that ended without a transition (a stat first, a bounded tail read only when the file changed: [`docs/reference/agents.md` §3.4](docs/reference/agents.md));
  - the file is the one herdr's `agent_session` names (a path, or a session id the adapter resolves to that session's file), or the continuation that file itself points to.

  Scanning directories for the "latest file" and continuous tailing are not allowed. Why transcripts at all: herdr exposes no reply text, and a screen capture loses the markdown.
- **One exception (the owner's decision, 2026-10-05):** the Claude adapter may read, read-only and on the same on-demand trigger, Claude Code's session index (`<config dir>/sessions/<pid>.json`), only in the config dir that holds herdr's `agent_session` (its transcript or its index entry), and only to find which session a pane really shows when herdr's session does not match the pane (Claude's agents view switches conversations without telling herdr). It then reads that session's transcript under the same conditions. Nothing else in Claude's config dir (e.g. `daemon/roster.json`, the `.key` files). How: [`docs/reference/agents.md` §3.3](docs/reference/agents.md).

### 1.2 Multi-agent by design
- Priority agents: **Claude (`claude`), Antigravity (`agy`), OpenCode (`opencode`)**. Every other agent herdr detects works through the **generic adapter** until it gets its own adapter (`add-agent-adapter` skill), which makes it first-class.
- **All agent-specific knowledge lives in `pkg/agents`** and nowhere else: menu layout, key mapping, cancel keys, transcript format, and whether a prompt can be queued while working. `pkg/herdr`, the bridge core, the relay and the clients stay agent-agnostic.
- **Never map prompt options by position.** Roles (`allow_once`, `allow_always`, `deny`, `choice`) come from the option **label**. Why: in Claude Code, `2` means "Yes, and don't ask again".

### 1.3 Two Go binaries, one module
- One Go module (`go.mod`; its name: §2).
- `agent-watch-bridge`: static binary on the Mac/Linux host. Keep it thin: herdr ↔ relay translation, on-demand transcript reads, and (macOS only, the owner's decision of 2026-10-05) the host's input idle time and screen lock, read with `ioreg`, for push presence (`contracts.md` §4.3).
  - The herdr plugin (`herdr-plugin.toml`) only installs and controls this binary. Its actions are one-shot.
  - The long-running process is `agent-watch-bridge run`, supervised by launchd (macOS) or systemd `--user` (Linux). Never rely on herdr to keep it alive: plugin actions are one-shot, and the service manager restarts the bridge after a crash or reboot and outlives herdr restarts.
- `agent-watch-relay`: static Linux binary on the VPS. It owns state aggregation, history storage, push, pairing and auth. History lives on the relay so the watch can read it while the host sleeps.

### 1.4 Outbound relay, no VPN
- Watches must **never** require Tailscale, a VPN or a LAN IP.
- The bridge dials **outbound** to the relay; watches use HTTPS + SSE on the relay. No inbound ports on the host.

### 1.5 Clients: Wear OS first, watchOS best-effort
- **Wear OS is the primary client and the reference implementation.** Features ship there first and are verified on the owner's Google Pixel Watch 2. Say "verified on the watch" only when the owner confirmed it.
- **watchOS is extra support**, verified only in the Xcode simulator; it never blocks a phase or a release. Never claim a watchOS feature works on a real device: say "verified in the simulator". Its current state is in `docs/STATUS.md`.

### 1.6 Scope
- **No terminal emulator or SSH client** (Moshi covers that).
- **Wrist-first:** glanceable status, approvals built from parsed options, voice dictation, history, complications and tiles.
- **A phone app is a first-class relay client**, never a watch companion: Wear OS already reaches the relay through the phone's Bluetooth connection.
- **No APNs** until a paid Apple Developer account exists; watchOS alerts go through ntfy.

---

## 2. Directory Layout

```
agent-watch/                      # the Go module keeps its old name, github.com/gabrielmarcano/agent-monitor
├── go.mod · go.sum · Makefile    # build, test, configure and deploy targets
├── agent-watch.env.example       # the one deployment config file, documented; the real agent-watch.env is git-ignored (secret)
├── herdr-plugin.toml             # herdr-agent-watch plugin manifest
├── VERSIONS                      # component versions
├── .github/workflows/            # CI and releases
├── cmd/
│   ├── bridge/                   # host daemon and its CLI
│   └── relay/                    # VPS relay and its CLI
├── pkg/
│   ├── model/                    # shared contracts: state, API DTOs, wire envelopes
│   ├── herdr/                    # socket client — agent-agnostic
│   ├── herdrtest/                # fake herdr socket for tests
│   ├── agents/                   # agent adapters, generic included (+ testdata/)
│   ├── bridge/                   # bridge core: engine, command executor, config, status file
│   ├── buildinfo/                # version + commit stamped into the binaries
│   ├── relayclient/              # bridge side of the WSS link
│   ├── relay/                    # relay server: hub, api, sse, auth, store
│   └── push/                     # Dispatcher (a relay.Notifier) and senders: FCM v1 (Wear OS), ntfy (watchOS)
├── deploy/
│   ├── launchd/                  # LaunchAgent template
│   └── relay/                    # systemd unit, env/proxy examples, deploy.sh, Dockerfile, README.md (operations)
├── macos-bar/                    # macOS menu bar app over the bridge CLI (make bar, make bar-test)
├── tools/config/                 # awenv.sh: agent-watch.env reader for the Makefile and deploy.sh (+ test_awenv.sh)
├── tools/guards/                 # guard implementation (hooks, pre-commit)
├── tools/herdr-overrides/        # TEMPORARY herdr detection override (claude); remove when upstream fixes it
├── wearos-app/                   # Wear OS client (Kotlin, Jetpack Compose)
└── watchos-app/                  # watchOS client (Swift, SwiftUI)
```

---

## 3. Secrets

- **Never commit credentials:**
  - `firebase-service-account.json`, `google-services.json`
  - host tokens, device tokens, pairing codes
  - ntfy topics and tokens
  - Cloudflare tokens, SSH keys
  - `.env` files and the bridge `config.toml`
  - `agent-watch.env` (only `agent-watch.env.example` is committed)
- **Never read, print or write `agent-watch.env`.** The owner creates it with `make config`. Tools take secrets from it directly (`configure --env-file`, `deploy.sh --sync-env`), never through argv, logs or output.
- **Never log** tokens, `Authorization` headers, prompt text or transcript content; log lengths and ids.
- **Tokens travel only in the `Authorization` header**, never in URLs or query strings (they end up in proxy logs).
- **`******` where a credential-like string would be** (e.g. an `Authorization` header value) is your tool masking it on display; the file is fine. Don't "fix" it.
- **Personal deployment details** (the owner's domain, SSH target, IPs, device ids, project names) never go into tracked files: use placeholders such as `relay.<domain>`.
- **Deploys and the owner's machines (the owner's decision):** agents may deploy the relay, run commands on the VPS over SSH, roll it back, and install or uninstall the herdr detection overrides whenever their task needs it, and **must say so in their report** (and update `docs/STATUS.md` "Deployed"). Still the owner's: `herdr server stop` and `herdr integration install`/`uninstall` (the guards refuse them), Cloudflare dashboard changes, revoking his watch, rebooting the host, and pausing shared infrastructure such as the proxy container.
- The security design (hashed tokens, pairing limits, the bridge's checks before pressing keys) is described in [`docs/GUIDE.md` § Security Model](docs/GUIDE.md#security-model); the rules that implement it are in `relay-security.md` and `herdr-integration.md`.

---

## 4. Area Rules

Each rule's first lines say what it applies to.

- [`herdr-integration.md`](.agents/rules/herdr-integration.md): herdr safety, protocol and the bridge's commands
- [`contracts.md`](.agents/rules/contracts.md): JSON shapes and their copies
- [`go-backend.md`](.agents/rules/go-backend.md): Go conventions and checks
- [`agent-adapters.md`](.agents/rules/agent-adapters.md): `pkg/agents`
- [`relay-security.md`](.agents/rules/relay-security.md): the relay, push and deploy
- [`wearos.md`](.agents/rules/wearos.md): the Wear OS app
- [`watchos.md`](.agents/rules/watchos.md): the watchOS app

How they load:
- **Antigravity CLI:** by each file's `trigger`/`glob` frontmatter.
- **OpenCode:** all of them, through `opencode.json`.
- **Claude Code:** through `.claude/rules` (a link to `.agents/rules`): a rule with `paths:` in its frontmatter loads when you work on matching files, the others always. Skills reach it through `.claude/skills` (a link to `.agents/skills`).
- **Any other agent:** read the ones for the files you touch.

---

## 5. Guards, Git and Language

- **Guards:** one implementation (`tools/guards/guards.py`), wired into the agent tools and the git pre-commit hook. Which tools load which guards, what they block and how to set them up: [`tools/guards/README.md`](tools/guards/README.md). If a guard blocks you, its message says the legitimate way forward: take it, or stop and tell the owner; never work around it.
- **A tool with only the pre-commit hook** (that README lists which) has nothing stopping it from sending input to the owner's herdr panes, so `herdr-integration.md` § Safety is entirely on it.
- **The working tree is shared with other sessions:**
  - commit only the paths you changed, by name (never `git add -A`, `-u` or `.`, nor `git commit -a`);
  - never `git commit --amend` or `--no-verify`;
  - commit after each verified step, so other sessions don't overwrite your work;
  - never `git push` unless the owner asks.
- **English only, everywhere:** code, comments, docs, commit messages, branch names, PR and issue titles, descriptions and comments, and release notes, whatever language the owner writes to you in. Non-English text is allowed only as test data (e.g. multi-byte input).

---

## 6. Before Claiming Done

1. The checks of every area rule you touched pass (Go: `go-backend.md` § Before claiming done; Wear OS: `wearos.md`'s build line).
2. No legacy reference (§1.1) came back.
3. If you changed `tools/guards/`, `.agents/hooks.json`, `.opencode/`, `.claude/settings.json` or `.githooks/`: `bash tools/guards/test_guards.sh`.
4. Every fact you changed is changed in its home (§0), and no copy of it is left elsewhere.
5. `docs/STATUS.md` is up to date: your phase's state, and the open items you closed or found.
6. Nothing is left behind: no uncommitted scratch files or secrets; only your own paths committed.
