# Phase 8: several hosts on one relay (design)

A temporary guide: delete it when the phase is done (`docs/STATUS.md` workflow), after moving what stays true to its home (`AGENTS.md` §0).

## Goal

The owner runs herdr on more than one machine (herdr's saved SSH machines: the Mac plus Linux hosts). The watch should show and answer the agents of every machine, one machine per page, swiping between them.

## Decisions (the owner's, 2026-10-10)

- **One bridge per machine.** Each machine runs `agent-watch-bridge` against its own herdr and dials the relay. Not one bridge reaching the others with `herdr --machine`: that shells out to the herdr CLI (`AGENTS.md` §1.1), cannot read remote transcripts, and stops when the Mac sleeps.
- **One host token per machine.** The relay knows which host a connection is from its token, never from what the bridge says; one machine can be revoked alone.
- **The watch swipes between machines:** one page per machine with its agents; notifications name the machine.
- **Presence stays global:** while the owner is at the Mac (quiet-at-mac, `contracts.md` §4.3), no machine's `blocked`/`done` buzzes. Linux hosts send no presence.

## Why it is not a small change

herdr numbers its own panes, so `w1:p1` exists on every machine, and today the bare `pane_id` is the agent's identity everywhere: the relay's state, history and command routing, the push dispatcher, the FCM payload, and the Wear OS app's store, list keys, notification ids, intents, tiles and complication. The full survey (2026-10-10) found, with two hosts connected:

- a snapshot from one host wipes the other's agents (`ReplaceAll`);
- `host_online`, `herdr_online`, `host_presence` and `OnHostOffline` are single global flags;
- a watch command goes to whichever connection is current, possibly the wrong machine;
- the Wear OS list crashes on a duplicate Compose key; one machine's notification replaces another's.

The only host identity on the wire, `hello.host`, is logged and dropped.

## Design

### Host identity (relay)

- **Host registry in `store.json`** (`version` 2): `hosts: [{id, name, token_hash, created_at, last_seen}]`, hashed like device tokens.
  - `id`: `[a-z0-9-]{1,32}`, chosen by the owner, stable; `name`: what the watch shows.
  - Relay CLI: `agent-watch-relay hosts add <id> [--name …]` prints the new token once (through the admin socket, like `devices`); `hosts list`; `hosts revoke <id>` (closes its connection).
- **`AW_HOST_TOKEN` keeps working** as the host `AW_HOST_ID` (default `main`), so the Mac's bridge needs no new token. It becomes optional once a host exists in the store.
- **The relay stamps the host id** on everything a connection sends. The bridge's wire messages do not change.

### Contract (`schema-sync` skill; additive only)

- `AgentState`, `HistoryItem`: `host` (host id).
- `AgentsSnapshot`: `hosts: [{id, name, online, herdr_online}]`. `host_online` / `herdr_online` stay, meaning "any host online" / "every online host's herdr online", for old apps.
- SSE: `agent_removed` and `host` gain `host`; a `host` event describes one host.
- Watch API: new `POST /v1/hosts/{host}/agents/{pane_id}/{prompt|answer|cancel}` and `GET /v1/history?host=&pane_id=`. The old `/v1/agents/{pane_id}/…` works only while one host is connected; otherwise `409 host_required` (new error code).
- FCM data: `host`, `host_name`. The resolved-seq rule becomes per host and pane.
- `HistoryItem.id` stays as it is (the relay buckets by host and pane, so a cross-host collision is harmless).

### Relay

- `State`: agents keyed by `(host, pane_id)`; `ReplaceAll`, `Remove`, `SetHost` per host.
- `Hub`: one connection per host id. A second connection **with the same token** replaces the first (4000 `replaced`), as today; different hosts coexist. Commands route to the agent's host; `host_offline` is per host.
- History: buckets per `(host, pane_id)`; the 20-per-pane cap stays, the 200-item total stays global. Migration to `version` 2: existing history belongs to `AW_HOST_ID`.
- Push: every per-pane map keyed by `(host, pane_id)`; titles prefixed with the host's name when more than one host is registered.
- Presence: one global state (the owner's decision). A host going offline clears it only if that host sent the last report.
- Pairing stays global: a paired watch sees every host; any host can mint a pairing code. `GET /v1/host/status` answers for the calling host, plus relay-wide counts.

### Bridge

- No protocol change. `status --json` shows the relay's view of this host.
- Linux hosts: the existing install (`docs/GUIDE.md`, systemd `--user`), each with its own token. Machine names, SSH targets and tokens live only in the owner's `agent-watch.env` or memory, never in tracked files.

### Wear OS (reference client)

- Identity = `(host, pane_id)` everywhere: `AgentStore` keys, Compose `key`s, nav routes (`agent/{host}/{paneId}`), intent extras and URIs, notification ids (`hash(host + pane)`), `ResolvedSeqs`, the pinned target, tile and complication extras.
- **List screen:** a horizontal pager, one page per host in `hosts` order (by name), with a page indicator; each page has the host's name and its own offline / herdr-stopped state. With one host it looks as today (no indicator).
- Notifications and the digest name the host when more than one is registered.
- Tiles and complication: the most urgent agent across all hosts, with the host's name when there is more than one; "offline" only when every host is.
- watchOS (legacy, `docs/STATUS.md`): out of scope; it keeps working while one host is connected.

## Steps and what can run in parallel

| Step | Work | Parallel? |
|---|---|---|
| 1 | Contract: `contracts.md`, `pkg/model`, Kotlin and Swift copies, golden file (`schema-sync` skill). **Done 2026-10-10:** §1.2, §1.4–§1.6, §2.1–§2.4, §4.1. §3, §4.3, §5 and §7 followed in 2a | **Alone:** every layer reads these files |
| 2a | Relay + push + relay CLI (`pkg/relay`, `pkg/push`, `cmd/relay`, `deploy/relay/README.md`) | **With 2b**: disjoint directories |
| 2b | Wear OS (`wearos-app/`, `ARCHITECTURE.md`) | **With 2a** |
| 3 | Deploy the relay; register the hosts; install the bridge on each Linux machine | After 2a. Installing on the owner's machines is his call |
| 4 | End to end on the watch: two hosts with the same pane id, commands to each, a host going offline | After 2b and 3 |

Shared files in 2a ‖ 2b: `docs/STATUS.md`, `docs/GUIDE.md`, `VERSIONS`. Commit them by path, one session at a time: the git index is shared.

## Versions

Relay and Wear OS: a minor bump each. The bridge needs none unless step 2a finds a change.
