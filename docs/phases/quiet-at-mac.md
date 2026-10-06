# Quiet pushes while the owner is at the Mac (design)

A temporary guide: delete it when the work is done (`docs/STATUS.md` workflow), after moving what stays true to its home (`AGENTS.md` §0).

## Goal

The watch should buzz only when the owner is away from the Mac or has left it for a while. While they are using the Mac, they see the agents there, so a push is noise.

- **Only pushes change.** The relay keeps the state, the history and the SSE stream exactly as today. The watch app, its list, tiles and complication stay live.
- **Away means:** no keyboard or mouse input on the Mac for **10 minutes**, or the screen is locked (owner's decision, 2026-10-05).
- **Not in scope:** knowing which app is in front, or which pane the owner looks at. Any input on the Mac counts as being there.

## Behaviour

| Situation | Pushes |
|---|---|
| Last input < 10 min ago, screen unlocked | `blocked` and `done` are **not sent** |
| Last input ≥ 10 min ago, or screen locked | Normal (`contracts.md` §4.3) |
| No fresh presence report (Linux host, old bridge, bridge restarting, host offline) | Normal: when in doubt, push |

**Catch-up when the owner leaves.** A `blocked` push held back while they were at the Mac goes out at the moment presence ends, if that pane is **still blocked on the same prompt** (same `state_change_seq` and `fingerprint`). It goes through the normal debounce, window and digest. Example: a prompt appears, the owner walks away without answering, and the watch buzzes 10 minutes after their last input (at once if they lock the screen).

- A held-back `done` is **not** caught up: the reply is already in the history and on the watch's list.
- No catch-up when presence ends because the host disconnected (the watch could not answer anyway). The pane gets its push on its next transition.
- A held-back `blocked` never went out, so it never gets a `resolved` either (as today for a dropped push).

## The signal (bridge, macOS only)

- Every **15 s**, while connected to the relay, the bridge reads:
  - `HIDIdleTime` from `ioreg -c IOHIDSystem -d 4 -r -k HIDIdleTime` (nanoseconds since the last input);
  - whether `CGSSessionScreenIsLocked` is set in `IOConsoleUsers` (`ioreg -n Root -d1`).
- It sends one `host_presence` message per read, and one right after the `snapshot` on every (re)connect.
- A failed read (missing `ioreg`, unparsable output) sends nothing: the relay then treats the host as away, so pushes go out.
- On Linux the bridge sends nothing.
- This widens the bridge's scope in `AGENTS.md` §1.3 ("herdr ↔ relay translation plus on-demand transcript reads"): add "and the Mac's input idle time, for push presence" there, as the owner's decision of 2026-10-05.
- The bridge never interprets the value: the threshold and the decision live on the relay, which owns push (`contracts.md` §4.3).

## Wire message (bridge → relay, `contracts.md` §3, Go only)

```go
type HostPresenceMsg struct {
    Type        string `json:"type"`         // "host_presence"
    IdleSeconds uint64 `json:"idle_seconds"` // whole seconds since the last keyboard/mouse input
    Locked      bool   `json:"locked"`       // the screen is locked
}
```

- Additive. An older relay ignores it (§3: unknown types are ignored). An older bridge never sends it, so its pushes stay as today.
- §3's connect sequence gains step 4: `host_presence` after `snapshot`, then every 15 s.

## Relay rules (`pkg/push` Dispatcher)

- On each report: `last_input_at = received_at − idle_seconds`, `locked`, `report_at = received_at`.
- **Present** at time `t` when all hold:
  - the host is connected;
  - `t − report_at < 45 s` (three missed reports, like the SSE silence rule);
  - not `locked`;
  - `t − last_input_at < AW_PUSH_PRESENCE_IDLE`.
- **Where it applies:** in `OnAgentUpdate`, before a `blocked` or `done` push is enqueued or waits for its reply.
  - Present: the push is not sent.
  - A `blocked` push is remembered per pane (seq and fingerprint) for the catch-up; the newest prompt replaces an older one.
  - A held push at the end of a window, or a `done` released after its reply wait, is sent whatever the presence is at that moment (keeps the window logic as it is).
- **When presence ends** (a timer at `last_input_at + AW_PUSH_PRESENCE_IDLE`, re-armed on each report; a report with `locked`; or no report for 45 s): run the catch-up, then clear what is remembered.
  - On a host disconnect: clear it without a catch-up.
- **Log** one info line per push held back: event, pane id, `reason=host_present`. Never the idle value at info level.

## Config (`contracts.md` §5, `agent-watch.env.example`)

| Variable | Default | Meaning |
|---|---|---|
| `AW_PUSH_PRESENCE_IDLE` | `10m` | Input idle time after which the owner counts as away. `0` turns the feature off: pushes ignore presence |

- The default is on, since it only applies when a bridge sends reports, and only macOS bridges do.

## What changes where

| Area | Change |
|---|---|
| `pkg/model/wire.go` | `WireHostPresence`, `HostPresenceMsg`, `DecodeWire` case (follow the `schema-sync` skill; §3 is Go only) |
| `pkg/bridge` | Presence reader behind an interface: `presence_darwin.go` (runs `ioreg`), the parsing in a portable file, a no-op elsewhere; the 15 s loop calls `relayclient.Client.Send` while `Connected()`, and `OnConnect` adds one report after the `snapshot` |
| `pkg/relayclient/client.go` | `host_presence` is coalesced like `herdr_status` and `snapshot` (only the newest waits in the queue) |
| `pkg/relay/hub.go` | Pass `host_presence` and host disconnects to the Notifier (new methods on the `Notifier` interface and `NoopNotifier`) |
| `pkg/push/push.go` | Presence state, the suppression, the end-of-presence timer and the catch-up |
| `pkg/relay/config.go`, `cmd/relay` | `AW_PUSH_PRESENCE_IDLE` |
| `docs/reference/contracts.md` | §3 (message, sequence), §4.3 (the rule above), §5 (variable) |
| `AGENTS.md` §1.3 | The bridge's widened scope |
| `docs/GUIDE.md` | One line on the behaviour where push is explained, linking to §4.3 |
| `agent-watch.env.example` | The variable, commented out |
| `VERSIONS` | `BRIDGE_VERSION` and `RELAY_VERSION` minor bumps |

## Testing

- **`pkg/push`:** fake clock and `afterFunc`:
  - suppressed while present;
  - normal when the report is stale, locked, idle ≥ threshold, or the threshold is 0;
  - catch-up for a still-blocked pane with the same prompt;
  - no catch-up for an answered or changed prompt, nor on disconnect;
  - `done` is never caught up;
  - catch-up goes through the window and digest.
- **Bridge:** parse captured `ioreg` outputs (`testdata`: idle and unlocked, locked, garbage). The loop is tested with a fake reader; no test runs `ioreg`.
- **Relay:** the hub passes `host_presence` and disconnects to the Notifier; config parsing of the variable.
- `go-backend.md` § Before claiming done.

## Verification on the owner's Mac and watch

1. With the screen locked, `ioreg -n Root -d1` shows `CGSSessionScreenIsLocked` (confirms the lock key before relying on it).
2. At the Mac, an agent blocks: no buzz; the watch's list shows it live.
3. Leave it: the watch buzzes about 10 minutes after the last input, if still blocked.
4. Lock the screen with a blocked agent: the watch buzzes within 15 s.
5. Away from the Mac, a new prompt: immediate push, as today.

## Rollout

- Either order works (both sides ignore what they do not know). Deploy the relay (`relay-deploy` skill), then restart the bridge.
- Record both in `docs/STATUS.md` "Deployed".

## What can run in parallel

- **First, alone:** the wire message (`pkg/model`, `contracts.md` §3). The contract is cross-cutting (`docs/STATUS.md`).
- **Then in parallel:** the bridge side (`pkg/bridge`, `pkg/relayclient`), and the relay side (`pkg/push`, `pkg/relay`). Disjoint packages.
- They share `contracts.md`, `VERSIONS` and `docs/STATUS.md`: commit those by path, one at a time.
