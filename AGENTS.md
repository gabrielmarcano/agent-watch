# Agent Guidelines (`AGENTS.md`)

Rules for every AI agent working in this repository: Claude Code, Antigravity, OpenCode, Codex, Cursor and others. This file holds only what applies everywhere. Everything else has **exactly one home**, listed in §0.

**Never copy a fact from its home into another doc: link to it.** When you change a fact, change it in its home and remove any copy you find (replace it with a link). The code wins over any doc. Two docs that disagree mean one of them holds a copy: fix that in the same commit.

---

## 0. Where Everything Lives

| What | Its one home |
|---|---|
| State: phases, claims, open items, blockers, next steps | [`docs/STATUS.md`](docs/STATUS.md) |
| Guides for the open phases | [`docs/phases/`](docs/phases/) |
| Every JSON shape, endpoint, error code, push payload, env var, config key and CLI output | [`docs/reference/contracts.md`](docs/reference/contracts.md) |
| The herdr socket: methods, events, keys, verified behaviour | [`docs/reference/herdr-socket-api.md`](docs/reference/herdr-socket-api.md) |
| Per agent: menus, option roles, keys, transcripts | [`docs/reference/agents.md`](docs/reference/agents.md) |
| Wear OS internals, screens, notifications and UX decisions | [`wearos-app/ARCHITECTURE.md`](wearos-app/ARCHITECTURE.md) |
| Exact texts the watch shows | `wearos-app/app/src/main/res/values/strings.xml`, `approval/CommandFeedback.kt` |
| Install, operate, troubleshoot; versions, releases and CI; security model; known issues | [`docs/GUIDE.md`](docs/GUIDE.md) |
| Relay operations: VPS, proxy, devices, backups | [`deploy/relay/README.md`](deploy/relay/README.md) |
| macOS menu bar app | [`macos-bar/README.md`](macos-bar/README.md) |
| What the guards block, their setup and tests | [`tools/guards/README.md`](tools/guards/README.md) |
| The temporary herdr detection overrides | [`tools/herdr-overrides/README.md`](tools/herdr-overrides/README.md) |
| Component versions | [`VERSIONS`](VERSIONS) |
| Rules per area | `.agents/rules/*.md` (§4) |
| Procedures | `.agents/skills/*/SKILL.md`: `capture-fixture` (capture and audit agent CLIs), `add-agent-adapter`, `schema-sync`, `herdr-probe`, `wearos-deploy`, `relay-deploy` |

**To start work:** pick an open phase in `docs/STATUS.md`, claim it there as its workflow line says, then read its guide.

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
  - it is triggered on demand by a herdr status transition;
  - the file is the one herdr's `agent_session` names (a path, or a session id the adapter resolves to that session's file).

  Scanning directories for the "latest file" and continuous tailing are not allowed. Why transcripts at all: herdr exposes no reply text, and a screen capture loses the markdown.

### 1.2 Multi-agent by design
- Priority agents: **Claude (`claude`), Antigravity (`agy`), OpenCode (`opencode`)**. Every other agent herdr detects must work through the **generic adapter**.
- **All agent-specific knowledge lives in `pkg/agents`** and nowhere else: menu layout, key mapping, cancel keys, transcript format, and whether a prompt can be queued while working. `pkg/herdr`, the bridge core, the relay and the clients stay agent-agnostic.
- **Never map prompt options by position.** Roles (`allow_once`, `allow_always`, `deny`, `choice`) come from the option **label**. Why: in Claude Code, `2` means "Yes, and don't ask again".

### 1.3 Two Go binaries, one module
- Go 1.22+, one module (`go.mod`; it keeps its old name, `agent-monitor`, although the repo is `agent-watch`).
- `agent-watch-bridge`: static binary on the Mac/Linux host. Keep it thin: herdr ↔ relay translation plus on-demand transcript reads.
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
├── go.mod · go.sum · Makefile    # make config / configure-bridge / deploy-relay / watchos-config read agent-watch.env
├── agent-watch.env.example       # the one deployment config file, documented; the real agent-watch.env is git-ignored (secret)
├── herdr-plugin.toml             # herdr-agent-watch plugin manifest
├── VERSIONS                      # one version per component (make check-versions)
├── .github/workflows/            # CI (ci, wearos, macos-bar) and date-tagged releases
├── cmd/
│   ├── bridge/                   # host daemon + CLI (configure/run/start/restart/stop/status/pair/version)
│   └── relay/                    # VPS relay (serve/devices/version)
├── pkg/
│   ├── model/                    # shared contracts: state, API DTOs, wire envelopes
│   ├── herdr/                    # socket client — agent-agnostic
│   ├── herdrtest/                # fake herdr socket for tests
│   ├── agents/                   # adapters: claude, agy, opencode, generic (+ testdata/)
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
├── tools/herdr-overrides/        # TEMPORARY herdr detection overrides (agy, claude); remove when upstream fixes them
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
- **Personal deployment details** (the owner's domain, SSH target, IPs, device ids, project names) never go into tracked files: use placeholders such as `relay.<domain>`.
- Commands that need VPS or Cloudflare credentials are handed to the owner, not run by agents.
- The security design (hashed tokens, pairing limits, the bridge's checks before pressing keys) is described in [`docs/GUIDE.md` § Security Model](docs/GUIDE.md#security-model); the rules that implement it are in `relay-security.md` and `herdr-integration.md`.

---

## 4. Area Rules

| Rule | Applies to |
|---|---|
| [`herdr-integration.md`](.agents/rules/herdr-integration.md) | any herdr command; `pkg/herdr`, `pkg/bridge`, `cmd/bridge`, the plugin (always on) |
| [`contracts.md`](.agents/rules/contracts.md) | any JSON shape: `pkg/model`, `contracts.md`, the client models (always on) |
| [`go-backend.md`](.agents/rules/go-backend.md) | every Go file |
| [`agent-adapters.md`](.agents/rules/agent-adapters.md) | `pkg/agents/**` |
| [`relay-security.md`](.agents/rules/relay-security.md) | `pkg/relay`, `pkg/push`, `cmd/relay`, `deploy/` |
| [`wearos.md`](.agents/rules/wearos.md) | `wearos-app/**` |
| [`watchos.md`](.agents/rules/watchos.md) | `watchos-app/**` |

How they load: Antigravity CLI by each file's `trigger`/`glob` frontmatter; OpenCode through `opencode.json`; Claude Code through `.claude/rules` (a link to `.agents/rules`). **Any other agent: read the ones for the files you touch.**

---

## 5. Guards and Git

- **Guards:** one implementation (`tools/guards/guards.py`), wired into Antigravity, OpenCode, Claude Code and the git pre-commit hook. What they block and how to set them up: [`tools/guards/README.md`](tools/guards/README.md). If a guard blocks you, fix the cause; never work around it.
- **The working tree is shared with other sessions:**
  - commit only the paths you changed, by name (never `git add -A` or `.`);
  - never `git commit --amend` or `--no-verify`;
  - commit after each verified step, so other sessions don't overwrite your work;
  - never `git push` unless the owner asks.

---

## 6. Before Claiming Done

1. The "before done" commands of every area rule you touched pass (Go: `go vet ./...`, `go test -race ./...`).
2. No legacy reference (§1.1) came back.
3. If you changed `tools/guards/`, `.agents/hooks.json`, `.opencode/` or `.githooks/`: `bash tools/guards/test_guards.sh`.
4. Every fact you changed is changed in its home (§0), and no copy of it is left elsewhere.
5. `docs/STATUS.md` is up to date: your phase's state, and the open items you closed or found.
6. Nothing is left behind: no uncommitted scratch files or secrets; only your own paths committed.
