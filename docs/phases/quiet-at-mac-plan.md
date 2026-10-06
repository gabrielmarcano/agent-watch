# Quiet pushes while at the Mac: implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

A temporary guide, deleted with [`quiet-at-mac.md`](quiet-at-mac.md) when the work is done.

**Goal:** The relay holds back `blocked` and `done` pushes while the owner's Mac had input in the last 10 minutes and its screen is unlocked, and pushes a still-waiting prompt when they leave.

**Architecture:**
- The bridge reads the Mac's input idle time and screen lock with `/usr/sbin/ioreg` every 15 s and sends a `host_presence` wire message.
- The relay's push `Dispatcher` keeps that presence, suppresses pushes while it holds, and runs a catch-up when it ends.
- The relay's state and SSE stream are untouched.

**Tech Stack:** Go standard library only (`os/exec`, `regexp`, `time`), `CGO_ENABLED=0`.

**Spec:** [`docs/phases/quiet-at-mac.md`](quiet-at-mac.md)

## Global Constraints

- Away = no input for `AW_PUSH_PRESENCE_IDLE` (default `10m`; `0` turns the feature off), or the screen is locked.
- Only pushes change: state, history and SSE stay exactly as today.
- When in doubt, push: no fresh report (45 s), no report at all, Linux host, host offline.
- Report interval: 15 s, plus one right after `snapshot` on every connect.
- Additive wire message, Go only (`contracts.md` §3); an older relay ignores it.
- `CGO_ENABLED=0`; no new modules. `ioreg` by absolute path (launchd gives no `PATH`).
- No test runs `ioreg`, touches the real herdr socket or the network. No `time.Sleep` over 50 ms in tests.
- Logs never carry tokens, prompt text or transcript content; the idle value only at debug level.
- English everywhere in the repo. Commit only your own paths, by name; never `--amend` or `--no-verify`.

## Review Focus

1. **A pane removed or answered while the owner was at the Mac**: the catch-up must not push it. Test: `TestPresence_CatchUpSkipsAnsweredAndRemoved` (Task 2).
2. **A huge or bogus `idle_seconds`** (e.g. `18446744073709551615`): it must not overflow `time.Duration` into a negative idle that reads as "present". Test: `TestPresence_HugeIdleIsAway` (Task 2).
3. **The bridge restarts while the owner is at the Mac**: the old connection's `OnHostOffline` must clear the held prompts and stop the timer, so a later timer cannot fire a stale catch-up. Test: `TestPresence_OfflineClearsWithoutCatchUp` (Task 2).
4. **`ioreg` output changes or is empty** (a macOS update): no report goes out, so pushes go out as today. Test: `TestParsePresence` garbage and empty rows (Task 4).
5. **Reports stop while connected** (bridge stuck, ioreg failing): presence must end 45 s after the last report and run the catch-up. Test: `TestPresence_StaleReportEndsPresence` (Task 2).

## File map

| File | Responsibility |
|---|---|
| `pkg/model/wire.go` | `WireHostPresence`, `HostPresenceMsg`, its `DecodeWire` case |
| `pkg/push/presence.go` (new) | Presence state, suppression check, end timer, catch-up |
| `pkg/push/push.go` | Two hooks: the suppression in `OnAgentUpdate`, the fields on `Dispatcher` |
| `pkg/relay/state.go` | `State.Get` |
| `pkg/relay/hub.go` | `Notifier` gains `OnHostPresence`/`OnHostOffline`; the hub calls them |
| `pkg/relay/config.go`, `pkg/relay/server.go` | `AW_PUSH_PRESENCE_IDLE`; wiring into the Dispatcher |
| `pkg/bridge/presence.go` (new) | `Presence`, `PresenceReader`, the `ioreg` parsers, `presenceMsg` |
| `pkg/bridge/presence_darwin.go` / `presence_other.go` (new) | `ReadPresence`: runs `ioreg` / reports nothing |
| `pkg/bridge/engine.go` | `Presence` and `PresenceInterval` fields, `StartPresence`, the report in `ConnectMessages` |
| `cmd/bridge/main.go` | Wires `bridge.ReadPresence` and starts the loop |
| `pkg/relayclient/client.go` | `describe` names the new message |

## Order and parallelism

- **Task 1 first, alone:** the wire contract.
- **Then Task 2 → Task 3** (relay side; Task 3 needs Task 2's methods) **in parallel with Task 4** (bridge side). They share no Go package.
- **Task 5 last:** the shared docs and `VERSIONS`. Every task commits `contracts.md` by path, one at a time.
- **Task 6:** deploy and the owner's checks.

---

### Task 1: The `host_presence` wire message

**Files:**
- Modify: `pkg/model/wire.go`
- Modify: `docs/reference/contracts.md` §3
- Test: `pkg/model/model_test.go` (`TestDecodeWire`)

**Interfaces:**
- Produces: `model.WireHostPresence = "host_presence"`; `model.HostPresenceMsg{Type string; IdleSeconds uint64; Locked bool}` with JSON keys `type`, `idle_seconds`, `locked`.

- [ ] **Step 1: Add the failing case** to the table in `TestDecodeWire` (`pkg/model/model_test.go:216`), next to the other rows, in their style:

```go
{`{"type":"host_presence","idle_seconds":42,"locked":false}`, reflect.TypeOf(model.HostPresenceMsg{})},
```


- [ ] **Step 2: Run it:** `go test ./pkg/model/ -run TestDecodeWire`. Expected: FAIL (`unknown wire message type "host_presence"`, or an undefined `HostPresenceMsg`).

- [ ] **Step 3: Implement** in `pkg/model/wire.go`. Add the constant to the block:

```go
	WireHostPresence  = "host_presence"
```

the type after `HerdrStatusMsg`:

```go
// HostPresenceMsg reports whether the owner is using the host (macOS only):
// whole seconds since the last keyboard or mouse input, and whether the
// screen is locked. The relay holds back pushes while the owner is there.
type HostPresenceMsg struct {
	Type        string `json:"type"` // "host_presence"
	IdleSeconds uint64 `json:"idle_seconds"`
	Locked      bool   `json:"locked"`
}
```

and the case in `DecodeWire`, after `WireHerdrStatus`:

```go
	case WireHostPresence:
		var msg HostPresenceMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, fmt.Errorf("decode host_presence: %w", err)
		}
		return msg, nil
```

- [ ] **Step 4: Update `contracts.md` §3:**
  - add the struct to the Go block, after `HerdrStatusMsg`, with the same comments;
  - add step 4 to **Sequence on connect**: `On macOS the bridge sends host_presence right after snapshot, then every 15 s while connected (the relay's use: §4.3).`;
  - in step 3's list, add `host_presence`.

- [ ] **Step 5: Run** `go test ./pkg/model/...` and `gofmt -l pkg/model`. Expected: PASS, no files listed.

- [ ] **Step 6: Commit.** §3 has no Kotlin or Swift copy (`contracts.md` line 6), so the pre-commit hook's peer check does not apply. Use its escape hatch and say why in the message:

```bash
git add pkg/model/wire.go pkg/model/model_test.go docs/reference/contracts.md
AW_CONTRACT_NO_JSON_CHANGE=1 git commit -m "Wire: host_presence message (bridge to relay)

Go-only wire message (contracts.md §3 has no Kotlin or Swift copy), hence
AW_CONTRACT_NO_JSON_CHANGE=1: the watch clients do not change.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Presence in the push Dispatcher

**Files:**
- Create: `pkg/push/presence.go`
- Modify: `pkg/push/push.go` (`Dispatcher` fields, `initLocked`, `OnAgentUpdate`)
- Create: `pkg/push/presence_test.go`
- Modify: `docs/reference/contracts.md` §4.3

**Interfaces:**
- Consumes: `model.AgentState`, `blockedMessage`, `enqueueLocked`, the test helpers `newTestDispatcher`, `fakeClock`, `fakeTimers`, `mockSender`, `agentAt`, `blockedState` (`pkg/push/push_test.go`).
- Produces (Task 3 relies on these):
  - `Dispatcher.PresenceIdle time.Duration` (0 = off);
  - `Dispatcher.Current func(paneID string) (model.AgentState, bool)` (nil = no catch-up);
  - `func (d *Dispatcher) OnHostPresence(idle time.Duration, locked bool)`;
  - `func (d *Dispatcher) OnHostOffline()`;
  - `const PresenceStale = 45 * time.Second`.

- [ ] **Step 1: Write the failing tests** in `pkg/push/presence_test.go`:

```go
package push

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// presenceDispatcher is a Dispatcher with presence on (10 min) whose Current
// reads from states, so tests set what the relay holds at catch-up time.
func presenceDispatcher(t *testing.T) (*Dispatcher, *fakeClock, *fakeTimers, *mockSender, map[string]model.AgentState) {
	t.Helper()
	sender := &mockSender{}
	d, clock, timers := newTestDispatcher(sender)
	d.PresenceIdle = 10 * time.Minute
	states := map[string]model.AgentState{}
	d.Current = func(pane string) (model.AgentState, bool) {
		s, ok := states[pane]
		return s, ok
	}
	return d, clock, timers, sender, states
}

func block(d *Dispatcher, states map[string]model.AgentState, pane string, seq uint64) {
	prev := agentAt(pane, pane, model.StatusWorking, seq-1)
	cur := blockedState(pane, pane)
	cur.StateChangeSeq = seq
	cur.Prompt = &model.PendingPrompt{Kind: model.PromptPermission, Title: "Bash command", Fingerprint: fmt.Sprintf("fp%d", seq)}
	states[pane] = cur
	d.OnAgentUpdate(&prev, cur)
}

func pushed(d *Dispatcher, s *mockSender) int {
	d.Wait()
	return len(s.getMessages())
}

func TestPresence_SuppressedWhilePresent(t *testing.T) {
	d, _, _, sender, states := presenceDispatcher(t)
	d.OnHostPresence(30*time.Second, false)

	block(d, states, "w1:p1", 10)
	prev := agentAt("w1:p2", "two", model.StatusWorking, 1)
	d.OnAgentUpdate(&prev, agentAt("w1:p2", "two", model.StatusDone, 2))

	if n := pushed(d, sender); n != 0 {
		t.Fatalf("pushes while present = %d, want 0", n)
	}
}

func TestPresence_NormalWhenAway(t *testing.T) {
	cases := []struct {
		name   string
		idle   time.Duration
		locked bool
		report bool
		off    bool
	}{
		{name: "idle past threshold", idle: 10 * time.Minute, report: true},
		{name: "locked", idle: time.Second, locked: true, report: true},
		{name: "no report", report: false},
		{name: "feature off", idle: time.Second, report: true, off: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _, _, sender, states := presenceDispatcher(t)
			if tc.off {
				d.PresenceIdle = 0
			}
			if tc.report {
				d.OnHostPresence(tc.idle, tc.locked)
			}
			block(d, states, "w1:p1", 10)
			if n := pushed(d, sender); n != 1 {
				t.Fatalf("pushes = %d, want 1", n)
			}
		})
	}
}

func TestPresence_CatchUpWhenIdleEnds(t *testing.T) {
	// By report: the next report says the threshold has passed.
	d, clock, _, sender, states := presenceDispatcher(t)
	d.OnHostPresence(9*time.Minute+50*time.Second, false)
	block(d, states, "w1:p1", 10)
	if n := pushed(d, sender); n != 0 {
		t.Fatalf("pushes before leaving = %d, want 0", n)
	}
	clock.Advance(15 * time.Second)
	d.OnHostPresence(10*time.Minute+5*time.Second, false)
	d.Wait()
	if msgs := sender.getMessages(); len(msgs) != 1 || msgs[0].Event != EventBlocked || msgs[0].PaneID != "w1:p1" {
		t.Fatalf("catch-up by report = %+v, want one blocked push for w1:p1", msgs)
	}

	// By timer: the threshold passes between two reports.
	d2, clock2, timers2, sender2, states2 := presenceDispatcher(t)
	d2.OnHostPresence(9*time.Minute+50*time.Second, false) // the timer is armed for +10 s
	block(d2, states2, "w1:p1", 10)
	clock2.Advance(10 * time.Second)
	if ds := timers2.Fire(); len(ds) != 1 || ds[0] != 10*time.Second {
		t.Fatalf("presence timer durations = %v, want [10s]", ds)
	}
	if n := pushed(d2, sender2); n != 1 {
		t.Fatalf("catch-up by timer = %d pushes, want 1", n)
	}
}

func TestPresence_CatchUpOnLock(t *testing.T) {
	d, _, _, sender, states := presenceDispatcher(t)
	d.OnHostPresence(time.Second, false)
	block(d, states, "w1:p1", 10)
	d.OnHostPresence(time.Second, true) // screen locked: away now
	if n := pushed(d, sender); n != 1 {
		t.Fatalf("pushes after lock = %d, want 1", n)
	}
}

func TestPresence_CatchUpUsesCurrentPrompt(t *testing.T) {
	d, _, _, sender, states := presenceDispatcher(t)
	d.OnHostPresence(time.Second, false)
	block(d, states, "w1:p1", 10)
	newer := states["w1:p1"]
	newer.StateChangeSeq = 12
	newer.Prompt = &model.PendingPrompt{Kind: model.PromptPermission, Title: "Bash command", Fingerprint: "newer"}
	states["w1:p1"] = newer

	d.OnHostPresence(time.Second, true)
	d.Wait()
	msgs := sender.getMessages()
	if len(msgs) != 1 || msgs[0].StateChangeSeq != 12 || msgs[0].Fingerprint != "newer" {
		t.Fatalf("catch-up = %+v, want the current prompt (seq 12, fingerprint newer)", msgs)
	}
}

func TestPresence_CatchUpSkipsAnsweredAndRemoved(t *testing.T) {
	d, _, _, sender, states := presenceDispatcher(t)
	d.OnHostPresence(time.Second, false)
	block(d, states, "w1:p1", 10)
	block(d, states, "w1:p2", 20)
	states["w1:p1"] = agentAt("w1:p1", "w1:p1", model.StatusWorking, 11) // answered at the Mac
	delete(states, "w1:p2")                                                // pane closed

	d.OnHostPresence(time.Second, true)
	if n := pushed(d, sender); n != 0 {
		t.Fatalf("catch-up pushes = %d, want 0", n)
	}
}

func TestPresence_DoneIsNotCaughtUp(t *testing.T) {
	d, _, _, sender, _ := presenceDispatcher(t)
	d.ReplyWait = 0
	d.OnHostPresence(time.Second, false)
	prev := agentAt("w1:p1", "one", model.StatusWorking, 1)
	d.OnAgentUpdate(&prev, agentAt("w1:p1", "one", model.StatusDone, 2))
	d.OnHostPresence(time.Second, true)
	if n := pushed(d, sender); n != 0 {
		t.Fatalf("pushes = %d, want 0 (done is not caught up)", n)
	}
}

func TestPresence_OfflineClearsWithoutCatchUp(t *testing.T) {
	d, clock, timers, sender, states := presenceDispatcher(t)
	d.OnHostPresence(time.Second, false)
	block(d, states, "w1:p1", 10)
	d.OnHostOffline()

	clock.Advance(time.Hour)
	timers.Fire()
	if n := pushed(d, sender); n != 0 {
		t.Fatalf("pushes after offline = %d, want 0", n)
	}
	// Offline means away: the next prompt pushes at once.
	block(d, states, "w1:p2", 20)
	if n := pushed(d, sender); n != 1 {
		t.Fatalf("pushes after the next prompt = %d, want 1", n)
	}
}

func TestPresence_StaleReportEndsPresence(t *testing.T) {
	d, clock, timers, sender, states := presenceDispatcher(t)
	d.OnHostPresence(time.Second, false)
	block(d, states, "w1:p1", 10)

	clock.Advance(PresenceStale)
	timers.Fire()
	if n := pushed(d, sender); n != 1 {
		t.Fatalf("pushes after %v without a report = %d, want 1", PresenceStale, n)
	}
}

func TestPresence_HugeIdleIsAway(t *testing.T) {
	d, _, _, sender, states := presenceDispatcher(t)
	d.OnHostPresence(time.Duration(math.MaxInt64), false)
	block(d, states, "w1:p1", 10)
	if n := pushed(d, sender); n != 1 {
		t.Fatalf("pushes = %d, want 1", n)
	}
}

func TestPresence_CatchUpGoesThroughDigest(t *testing.T) {
	d, _, timers, sender, states := presenceDispatcher(t)
	d.OnHostPresence(time.Second, false)
	for i, pane := range []string{"w1:p1", "w1:p2", "w1:p3", "w1:p4", "w1:p5"} {
		block(d, states, pane, uint64(10*(i+1)))
	}
	d.OnHostPresence(time.Second, true)
	flushWindow(d, timers)
	d.Wait()
	msgs := sender.getMessages()
	if len(msgs) != 2 || msgs[0].Event != EventBlocked || msgs[1].Event != EventDigest {
		t.Fatalf("catch-up = %v, want the first blocked push then one digest", eventsOf(msgs))
	}
}

func eventsOf(msgs []Message) []Event {
	out := make([]Event, len(msgs))
	for i, m := range msgs {
		out[i] = m.Event
	}
	return out
}
```

The helpers `blockedState`, `agentAt`, `flushWindow` and `fakeTimers` live in `push_test.go`; `Fire` runs every armed timer (window and presence alike) once. `HugeIdle` passes `math.MaxInt64` because the hub clamps before calling (Task 3); the Dispatcher must still read it as away (`now - idle` overflows backwards).

- [ ] **Step 2: Run them:** `go test ./pkg/push/ -run TestPresence`. Expected: FAIL to compile (`d.PresenceIdle`, `d.Current`, `OnHostPresence`, `OnHostOffline`, `PresenceStale` undefined).

- [ ] **Step 3: Add the fields** to `Dispatcher` in `pkg/push/push.go`, after `ReplyWait`:

```go
	// PresenceIdle is how long without input on the host the owner counts as
	// away (AW_PUSH_PRESENCE_IDLE). While they are there, blocked and done
	// pushes are held back (contracts.md §4.3). 0 turns presence off.
	PresenceIdle time.Duration
	// Current returns a pane's current state (the relay's State.Get), for the
	// catch-up when presence ends. nil: no catch-up.
	Current func(paneID string) (model.AgentState, bool)
```

and, after `pendingDone map[string]*pendingDone`:

```go
	presence presenceState // guarded by mu; presence.go
```

In `initLocked`, add:

```go
	if d.presence.quiet == nil {
		d.presence.quiet = make(map[string]struct{})
	}
```

- [ ] **Step 4: Hook the suppression** into `OnAgentUpdate`, replacing the final `if shouldPush { … }` block:

```go
	if shouldPush && d.heldBackLocked(msg) {
		return
	}
	if shouldPush {
		if msg.Event == EventDone && d.ReplyWait > 0 {
			d.waitForReplyLocked(msg, cur)
		} else {
			d.enqueueLocked(msg, cur)
		}
	}
```

- [ ] **Step 5: Create `pkg/push/presence.go`:**

```go
package push

import (
	"sort"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// PresenceStale is how long a presence report counts: three missed 15 s
// reports, like the SSE silence rule (contracts.md §2.3, §4.3).
const PresenceStale = 45 * time.Second

// presenceState is what the host last said about its owner, and the blocked
// pushes held back while they were there.
type presenceState struct {
	reported    bool // a report arrived since the host connected
	reportAt    time.Time
	lastInputAt time.Time
	locked      bool
	quiet       map[string]struct{} // panes whose blocked push was held back
	timer       stopper             // fires when presence would end; nil when none
	gen         uint64              // tells a stale timer from the current one
}

// OnHostPresence implements relay.Notifier: the host's input idle time and
// screen lock, received now.
func (d *Dispatcher) OnHostPresence(idle time.Duration, locked bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.initLocked()

	now := d.Now()
	p := &d.presence
	p.reported, p.reportAt, p.locked = true, now, locked
	p.lastInputAt = now.Add(-idle)
	if idle < 0 || p.lastInputAt.After(now) { // overflowed: treat as long idle
		p.lastInputAt = time.Time{}
	}
	d.Logger.Debug("host presence", "idle", idle, "locked", locked)

	if d.presentLocked(now) {
		d.armPresenceTimerLocked(now)
		return
	}
	d.endPresenceLocked()
}

// OnHostOffline implements relay.Notifier: the host disconnected. Presence
// ends without a catch-up (the watch could not answer anyway).
func (d *Dispatcher) OnHostOffline() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.initLocked()

	d.stopPresenceTimerLocked()
	clear(d.presence.quiet)
	d.presence.reported = false
}

// presentLocked reports whether the owner is at the host at time now.
func (d *Dispatcher) presentLocked(now time.Time) bool {
	p := d.presence
	if d.PresenceIdle <= 0 || !p.reported || p.locked || p.lastInputAt.IsZero() {
		return false
	}
	return now.Sub(p.reportAt) < PresenceStale && now.Sub(p.lastInputAt) < d.PresenceIdle
}

// heldBackLocked holds back m while the owner is at the host. A blocked push
// remembers its pane for the catch-up.
func (d *Dispatcher) heldBackLocked(m Message) bool {
	if !d.presentLocked(d.Now()) {
		return false
	}
	if m.Event == EventBlocked && len(d.presence.quiet) < maxShownBlocked {
		d.presence.quiet[m.PaneID] = struct{}{}
	}
	d.Logger.Info("push held back", "event", m.Event, "pane", m.PaneID, "reason", "host_present")
	return true
}

// armPresenceTimerLocked (re)arms the timer for when presence would end: the
// idle threshold or the report going stale, whichever comes first.
func (d *Dispatcher) armPresenceTimerLocked(now time.Time) {
	d.stopPresenceTimerLocked()
	p := &d.presence
	end := p.lastInputAt.Add(d.PresenceIdle)
	if stale := p.reportAt.Add(PresenceStale); stale.Before(end) {
		end = stale
	}
	p.gen++
	gen := p.gen
	p.timer = d.afterFunc(end.Sub(now), func() { d.presenceTimerFired(gen) })
}

func (d *Dispatcher) stopPresenceTimerLocked() {
	if d.presence.timer != nil {
		d.presence.timer.Stop()
		d.presence.timer = nil
	}
}

// presenceTimerFired ends presence unless a newer report re-armed the timer.
func (d *Dispatcher) presenceTimerFired(gen uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.presence.timer == nil || d.presence.gen != gen {
		return
	}
	d.presence.timer = nil
	if now := d.Now(); d.presentLocked(now) {
		d.armPresenceTimerLocked(now)
		return
	}
	d.endPresenceLocked()
}

// endPresenceLocked pushes every held-back pane that is still blocked now,
// built from its current state, through the normal debounce, window and digest.
func (d *Dispatcher) endPresenceLocked() {
	d.stopPresenceTimerLocked()
	quiet := d.presence.quiet
	if len(quiet) == 0 {
		return
	}
	panes := make([]string, 0, len(quiet))
	for pane := range quiet {
		panes = append(panes, pane)
	}
	sort.Strings(panes)
	clear(quiet)
	if d.Current == nil {
		return
	}
	for _, pane := range panes {
		cur, ok := d.Current(pane)
		if !ok || cur.Status != model.StatusBlocked {
			continue
		}
		d.enqueueLocked(blockedMessage(cur), cur)
	}
}
```

Notes:
- `d.Current` runs under `d.mu`. That is safe: the relay's `State` never calls the Notifier while holding its own lock (`hub.go` calls `OnAgentUpdate` after `Upsert` returns).

- [ ] **Step 6: Run** `go test -race ./pkg/push/...`. Expected: PASS, the old tests included (presence defaults to off: `PresenceIdle` 0).

- [ ] **Step 7: Update `contracts.md` §4.3**, after the table of when to push, with one bullet:

```markdown
- **Held back while the owner is at the host** (`AW_PUSH_PRESENCE_IDLE`, §5): while the host's last `host_presence` (§3) is under 45 s old, its screen is unlocked and its last input is under that threshold, `blocked` and `done` pushes are not sent. When presence ends (the threshold passes, the screen locks, or no report for 45 s), each pane whose `blocked` push was held back and that the relay still shows `blocked` gets one, built from its current state, through the debounce, window and digest below. A held-back `done` is not sent later. A host that disconnects clears what was held back without sending it. A host that never reports (Linux, an older bridge) is never held back.
```

- [ ] **Step 8: Commit:**

```bash
gofmt -l pkg/push
git add pkg/push/presence.go pkg/push/presence_test.go pkg/push/push.go docs/reference/contracts.md
git commit -m "Push: hold back pushes while the owner is at the host

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Relay wiring and `AW_PUSH_PRESENCE_IDLE`

**Files:**
- Modify: `pkg/relay/state.go` (add `Get`)
- Modify: `pkg/relay/hub.go` (`Notifier`, `NoopNotifier`, `handleWireMessage`, the disconnect `defer` in `ServeHost`)
- Modify: `pkg/relay/config.go`, `pkg/relay/server.go`
- Modify: `agent-watch.env.example`, `tools/config/awenv.sh` (`RELAY_KEYS`)
- Modify: `docs/reference/contracts.md` §5 (variable table) and §6 (the relay variables row, line ~682)
- Test: `pkg/relay/hub_test.go`, `pkg/relay/config_test.go`, `pkg/relay/state_test.go`

**Interfaces:**
- Consumes: `model.HostPresenceMsg` (Task 1); `Dispatcher.PresenceIdle`, `Dispatcher.Current`, `OnHostPresence`, `OnHostOffline` (Task 2).
- Produces: `func (s *State) Get(paneID string) (model.AgentState, bool)`; `Config.PushPresenceIdle time.Duration`; `Notifier` with `OnHostPresence(idle time.Duration, locked bool)` and `OnHostOffline()`.

- [ ] **Step 1: Write the failing tests.**

`pkg/relay/state_test.go`:

```go
func TestState_Get(t *testing.T) {
	s := NewState()
	s.Upsert(model.AgentState{PaneID: "w1:p1", Agent: "claude", Status: model.StatusBlocked})
	if a, ok := s.Get("w1:p1"); !ok || a.Status != model.StatusBlocked {
		t.Fatalf("Get(w1:p1) = %+v, %v", a, ok)
	}
	if _, ok := s.Get("w1:none"); ok {
		t.Fatal("Get(w1:none) found a pane")
	}
}
```

`pkg/relay/hub_test.go`, next to `historyNotifier`:

```go
// presenceNotifier records the presence calls the hub makes.
type presenceNotifier struct {
	NoopNotifier
	mu       sync.Mutex
	idles    []time.Duration
	locked   []bool
	offlines int
}

func (n *presenceNotifier) OnHostPresence(idle time.Duration, locked bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.idles = append(n.idles, idle)
	n.locked = append(n.locked, locked)
}

func (n *presenceNotifier) OnHostOffline() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.offlines++
}

func TestHub_PassesHostPresence(t *testing.T) {
	n := &presenceNotifier{}
	h := NewHub(nil, NewState(), nil, n)
	h.handleWireMessage(model.HostPresenceMsg{Type: model.WireHostPresence, IdleSeconds: 42, Locked: true})
	h.handleWireMessage(model.HostPresenceMsg{Type: model.WireHostPresence, IdleSeconds: math.MaxUint64})
	if len(n.idles) != 2 || n.idles[0] != 42*time.Second || !n.locked[0] {
		t.Fatalf("presence calls = %v %v", n.idles, n.locked)
	}
	if n.idles[1] != maxPresenceIdle {
		t.Fatalf("huge idle = %v, want it clamped to %v", n.idles[1], maxPresenceIdle)
	}
}
```

Plus a disconnect test: follow `TestHub_DisconnectFailsInFlightCommandImmediately` (`hub_test.go:505`) with its `hubHarness`, pass a `presenceNotifier`, close the bridge side, and poll (deadline, ≤ 50 ms sleeps) until `n.offlines == 1`. Name it `TestHub_DisconnectReportsHostOffline`. A replaced host must **not** count: assert `offlines == 0` after `TestHub_ConnectAndReplace`'s replace step, in a copy named `TestHub_ReplaceIsNotOffline`.

If `NewHub(nil, …)` panics on a nil auth or store, build the hub the way `hubHarness` does.

`pkg/relay/config_test.go`, in the style of the `AW_PUSH_RESOLVED` table test:

```go
func TestLoadConfig_PushPresenceIdle(t *testing.T) {
	cases := []struct {
		env     string
		set     bool
		want    time.Duration
		wantErr bool
	}{
		{set: false, want: 10 * time.Minute},
		{env: "5m", set: true, want: 5 * time.Minute},
		{env: "0", set: true, want: 0},
		{env: "-1m", set: true, wantErr: true},
		{env: "ten", set: true, wantErr: true},
	}
	for _, tc := range cases {
		setBaseEnv(t)
		t.Setenv("AW_PUSH_PRESENCE_IDLE", tc.env)
		if !tc.set {
			os.Unsetenv("AW_PUSH_PRESENCE_IDLE")
		}
		cfg, err := LoadConfig()
		if tc.wantErr {
			if err == nil {
				t.Errorf("AW_PUSH_PRESENCE_IDLE=%q: want an error", tc.env)
			}
			continue
		}
		if err != nil {
			t.Fatalf("AW_PUSH_PRESENCE_IDLE=%q: %v", tc.env, err)
		}
		if cfg.PushPresenceIdle != tc.want {
			t.Errorf("AW_PUSH_PRESENCE_IDLE=%q: got %v, want %v", tc.env, cfg.PushPresenceIdle, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run** `go test ./pkg/relay/ -run 'TestState_Get|TestHub_PassesHostPresence|TestHub_DisconnectReportsHostOffline|TestHub_ReplaceIsNotOffline|TestLoadConfig_PushPresenceIdle'`. Expected: FAIL to compile.

- [ ] **Step 3: Implement.**

`pkg/relay/state.go`, after `HasPane`:

```go
// Get returns a pane's current state.
func (s *State) Get(paneID string) (model.AgentState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.agents[paneID]
	if !ok {
		return model.AgentState{}, false
	}
	return a, true
}
```


`pkg/relay/hub.go`, extend the interface and the no-op:

```go
	// OnHostPresence reports the host's input idle time and screen lock
	// (host_presence, contracts.md §3): pushes wait while the owner is there.
	OnHostPresence(idle time.Duration, locked bool)
	// OnHostOffline reports that the current host disconnected or was dropped
	// (not replaced by a new connection).
	OnHostOffline()
```

```go
// OnHostPresence is a no-op implementation.
func (NoopNotifier) OnHostPresence(idle time.Duration, locked bool) {}

// OnHostOffline is a no-op implementation.
func (NoopNotifier) OnHostOffline() {}
```

A clamp constant near the top of `hub.go`:

```go
// maxPresenceIdle caps a reported idle time: anything longer is just "away",
// and the cap keeps the conversion to time.Duration from overflowing.
const maxPresenceIdle = 365 * 24 * time.Hour
```

In `handleWireMessage`, after `case model.HerdrStatusMsg:`:

```go
	case model.HostPresenceMsg:
		idle := maxPresenceIdle
		if m.IdleSeconds < uint64(maxPresenceIdle/time.Second) {
			idle = time.Duration(m.IdleSeconds) * time.Second
		}
		h.notifier.OnHostPresence(idle, m.Locked)
```

In `ServeHost`'s disconnect `defer`, record whether this host was still current and call the notifier **after** `h.mu.Unlock()`:

```go
	defer func() {
		host.markGone()
		h.mu.Lock()
		wasCurrent := h.currentHost == host
		if wasCurrent {
			h.currentHost = nil
			h.state.SetHost(false, false)
		}
		h.mu.Unlock()
		if wasCurrent {
			h.notifier.OnHostOffline()
		}
		// … the rest of the existing defer stays as it is
```

`pkg/relay/config.go`: add to `Config`:

```go
	// PushPresenceIdle (AW_PUSH_PRESENCE_IDLE) is how long without input on
	// the host the owner counts as away; pushes wait while they are there.
	// 0 turns it off. Default DefaultPushPresenceIdle.
	PushPresenceIdle time.Duration
```

```go
// DefaultPushPresenceIdle is AW_PUSH_PRESENCE_IDLE's default.
const DefaultPushPresenceIdle = 10 * time.Minute
```

and in `LoadConfig`, after the `AW_PUSH_RESOLVED` block:

```go
	presenceIdle := DefaultPushPresenceIdle
	if v := strings.TrimSpace(os.Getenv("AW_PUSH_PRESENCE_IDLE")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return nil, fmt.Errorf("AW_PUSH_PRESENCE_IDLE must be a duration such as 10m, or 0 to turn it off, got %q", v)
		}
		presenceIdle = d
	}
```

with `PushPresenceIdle: presenceIdle,` in the `cfg` literal. `time.ParseDuration("0")` returns 0.

`pkg/relay/server.go`, where the dispatcher is built:

```go
		dispatcher.PresenceIdle = cfg.PushPresenceIdle
		dispatcher.Current = state.Get
```

and add `"presence_idle", cfg.PushPresenceIdle` to an existing startup `slog.Info` line (or one new line: `slog.Info("push presence", "idle", cfg.PushPresenceIdle)`).

- [ ] **Step 4: Run** `go test -race ./pkg/relay/... ./pkg/push/... ./cmd/relay/...` and `go vet ./...`. Expected: PASS. A compile error in another implementer of `Notifier` means a test fake that does not embed `NoopNotifier`: embed it.

- [ ] **Step 5: Config docs.**
  - `contracts.md` §5 table, after `AW_PUSH_RESOLVED`: `| AW_PUSH_PRESENCE_IDLE | no | 10m | Input idle time on the host after which the owner counts as away; pushes wait while they are there (§4.3). A Go duration (90s, 10m); 0 turns it off |`
  - `contracts.md` §6, the relay variables row: add `AW_PUSH_PRESENCE_IDLE` after `AW_PUSH_RESOLVED`.
  - `tools/config/awenv.sh`: add `AW_PUSH_PRESENCE_IDLE` to `RELAY_KEYS` after `AW_PUSH_RESOLVED`.
  - `agent-watch.env.example`, under `# AW_PUSH_RESOLVED=0`, in that block's comment style: `# AW_PUSH_PRESENCE_IDLE=10m` with a one-line comment pointing to `contracts.md` §5.
  - Run `bash tools/config/test_awenv.sh`. Expected: PASS.

- [ ] **Step 6: Commit:**

```bash
gofmt -l pkg/relay
git add pkg/relay/state.go pkg/relay/state_test.go pkg/relay/hub.go pkg/relay/hub_test.go \
  pkg/relay/config.go pkg/relay/config_test.go pkg/relay/server.go \
  agent-watch.env.example tools/config/awenv.sh docs/reference/contracts.md
git commit -m "Relay: host presence holds back pushes (AW_PUSH_PRESENCE_IDLE)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: The bridge reads and reports presence (macOS)

**Files:**
- Create: `pkg/bridge/presence.go`, `pkg/bridge/presence_darwin.go`, `pkg/bridge/presence_other.go`
- Create: `pkg/bridge/presence_test.go`, `pkg/bridge/testdata/ioreg_hid.txt`, `pkg/bridge/testdata/ioreg_root_unlocked.txt`, `pkg/bridge/testdata/ioreg_root_locked.txt`
- Modify: `pkg/bridge/engine.go` (fields, `StartPresence`, `ConnectMessages`)
- Modify: `cmd/bridge/main.go`
- Modify: `pkg/relayclient/client.go` (`describe`)
- Modify: `AGENTS.md` §1.3
- Test: `pkg/bridge/engine_test.go` (`ConnectMessages`)

**Interfaces:**
- Consumes: `model.HostPresenceMsg`, `model.WireHostPresence` (Task 1).
- Produces:
  - `type Presence struct { Idle time.Duration; Locked bool }`;
  - `type PresenceReader func(ctx context.Context) (Presence, bool)`;
  - `func ReadPresence(ctx context.Context) (Presence, bool)` (darwin: ioreg; elsewhere: `false`);
  - `func parsePresence(hid, root []byte) (Presence, bool)`;
  - `Engine.Presence PresenceReader` (nil: no reports), `Engine.PresenceInterval time.Duration`, `func (e *Engine) StartPresence(ctx context.Context)`.

- [ ] **Step 1: Capture the fixtures** from this Mac (read-only):

```bash
/usr/sbin/ioreg -c IOHIDSystem -d 4 -r -k HIDIdleTime > pkg/bridge/testdata/ioreg_hid.txt
/usr/sbin/ioreg -n Root -d1 > pkg/bridge/testdata/ioreg_root_unlocked.txt
```

Check both for anything personal: the Root output carries the console user's name and uid in `IOConsoleUsers` (`kCGSSessionUserNameKey`, `kCGSSessionLongUserNameKey`, `kCGSSessionUserIDKey` and similar). Replace each value with a placeholder (`user`, `501`) with `sed -i ''`, and keep the format. Then make `ioreg_root_locked.txt` a copy of the unlocked one with `"CGSSessionScreenIsLocked"=Yes,` inserted as the first key of the `IOConsoleUsers` dictionary, in the same syntax as its neighbours. Task 6 step 1 confirms the key on the real lock screen; if it differs, fix the fixture and the parser there.

- [ ] **Step 2: Write the failing tests** in `pkg/bridge/presence_test.go`:

```go
package bridge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParsePresence(t *testing.T) {
	hid := readFixture(t, "ioreg_hid.txt")
	unlocked := readFixture(t, "ioreg_root_unlocked.txt")
	locked := readFixture(t, "ioreg_root_locked.txt")
	cases := []struct {
		name       string
		hid, root  []byte
		wantOK     bool
		wantLocked bool
	}{
		{"unlocked", hid, unlocked, true, false},
		{"locked", hid, locked, true, true},
		{"empty hid", nil, unlocked, false, false},
		{"garbage hid", []byte(`"HIDIdleTime" = banana`), unlocked, false, false},
		{"empty root", hid, nil, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := parsePresence(tc.hid, tc.root)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && p.Locked != tc.wantLocked {
				t.Fatalf("Locked = %v, want %v", p.Locked, tc.wantLocked)
			}
		})
	}
}

func TestParsePresence_IdleValue(t *testing.T) {
	hid := []byte("  |   \"HIDIdleTime\" = 90000000000\n")
	root := []byte(`"IOConsoleUsers" = ({"kCGSSessionOnConsoleKey"=Yes})`)
	p, ok := parsePresence(hid, root)
	if !ok || p.Idle != 90*time.Second {
		t.Fatalf("parsePresence = %+v, %v; want 90s", p, ok)
	}
}

func TestConnectMessages_IncludesPresence(t *testing.T) {
	e := NewEngine(nil, nil, nil, nil, "test", "host", "", nil)
	e.Presence = func(context.Context) (Presence, bool) {
		return Presence{Idle: 75 * time.Second, Locked: true}, true
	}
	msgs := e.ConnectMessages(context.Background())
	if len(msgs) != 3 {
		t.Fatalf("ConnectMessages = %d messages, want hello, snapshot, host_presence", len(msgs))
	}
	p, ok := msgs[2].(model.HostPresenceMsg)
	if !ok || p.Type != model.WireHostPresence || p.IdleSeconds != 75 || !p.Locked {
		t.Fatalf("third message = %#v", msgs[2])
	}
}

func TestConnectMessages_NoPresenceReader(t *testing.T) {
	e := NewEngine(nil, nil, nil, nil, "test", "host", "", nil)
	if n := len(e.ConnectMessages(context.Background())); n != 2 {
		t.Fatalf("ConnectMessages = %d messages, want 2", n)
	}
	e.Presence = func(context.Context) (Presence, bool) { return Presence{}, false }
	if n := len(e.ConnectMessages(context.Background())); n != 2 {
		t.Fatalf("with a failed read: %d messages, want 2", n)
	}
}
```

If `NewEngine` with nil herdr, syncer, registry and relay panics, build the engine the way `engine_test.go` does.

- [ ] **Step 3: Run** `go test ./pkg/bridge/ -run 'TestParsePresence|TestConnectMessages'`. Expected: FAIL to compile.

- [ ] **Step 4: Implement `pkg/bridge/presence.go`:**

```go
package bridge

import (
	"bytes"
	"context"
	"regexp"
	"strconv"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// Presence is whether the owner is using this host: the time since the last
// keyboard or mouse input, and whether the screen is locked. The relay holds
// back pushes while they are there (contracts.md §4.3).
type Presence struct {
	Idle   time.Duration
	Locked bool
}

// PresenceReader reads the host's presence; false when it cannot tell, and
// then the bridge reports nothing (the relay pushes as usual).
type PresenceReader func(ctx context.Context) (Presence, bool)

// presenceTimeout bounds one read (two ioreg runs, ~20 ms each).
const presenceTimeout = 2 * time.Second

var (
	hidIdleRe      = regexp.MustCompile(`"HIDIdleTime" = (\d+)`)
	screenLockedRe = regexp.MustCompile(`"CGSSessionScreenIsLocked"\s*=\s*Yes`)
)

// parsePresence reads ioreg's IOHIDSystem output (HIDIdleTime, nanoseconds)
// and its Root output (IOConsoleUsers, which carries
// CGSSessionScreenIsLocked=Yes only while the screen is locked).
func parsePresence(hid, root []byte) (Presence, bool) {
	m := hidIdleRe.FindSubmatch(hid)
	if m == nil || !bytes.Contains(root, []byte(`"IOConsoleUsers"`)) {
		return Presence{}, false
	}
	ns, err := strconv.ParseUint(string(m[1]), 10, 64)
	if err != nil {
		return Presence{}, false
	}
	idle := time.Duration(ns)
	if ns > uint64(1<<63-1) {
		idle = time.Duration(1<<63 - 1)
	}
	return Presence{Idle: idle, Locked: screenLockedRe.Match(root)}, true
}

// presenceMsg reads the presence once; false when there is nothing to send.
func (e *Engine) presenceMsg(ctx context.Context) (model.HostPresenceMsg, bool) {
	if e.Presence == nil {
		return model.HostPresenceMsg{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, presenceTimeout)
	defer cancel()
	p, ok := e.Presence(ctx)
	if !ok {
		return model.HostPresenceMsg{}, false
	}
	return model.HostPresenceMsg{
		Type:        model.WireHostPresence,
		IdleSeconds: uint64(p.Idle / time.Second),
		Locked:      p.Locked,
	}, true
}

// StartPresence sends a presence report every PresenceInterval while the
// relay is connected. ConnectMessages sends the first one on each connect.
func (e *Engine) StartPresence(ctx context.Context) {
	if e.Presence == nil || e.PresenceInterval <= 0 || e.Relay == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(e.PresenceInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !e.Relay.Connected() {
					continue
				}
				if msg, ok := e.presenceMsg(ctx); ok {
					e.Relay.Send(msg)
				}
			}
		}
	}()
}
```

`pkg/bridge/presence_darwin.go`:

```go
//go:build darwin

package bridge

import (
	"context"
	"os/exec"
)

// ioregPath is absolute: launchd starts the bridge without a PATH.
const ioregPath = "/usr/sbin/ioreg"

// ReadPresence reads the Mac's input idle time and screen lock with ioreg.
func ReadPresence(ctx context.Context) (Presence, bool) {
	hid, err := exec.CommandContext(ctx, ioregPath, "-c", "IOHIDSystem", "-d", "4", "-r", "-k", "HIDIdleTime").Output()
	if err != nil {
		return Presence{}, false
	}
	root, err := exec.CommandContext(ctx, ioregPath, "-n", "Root", "-d1").Output()
	if err != nil {
		return Presence{}, false
	}
	return parsePresence(hid, root)
}
```

`pkg/bridge/presence_other.go`:

```go
//go:build !darwin

package bridge

import "context"

// ReadPresence reports nothing outside macOS: the relay pushes as usual.
func ReadPresence(ctx context.Context) (Presence, bool) {
	return Presence{}, false
}
```

- [ ] **Step 5: Wire it into the engine.** In `Engine` (`pkg/bridge/engine.go`), after `TurnCheckInterval`:

```go
	// Presence reads whether the owner is using the host, for the relay's
	// push presence (nil: no reports). PresenceInterval is how often
	// StartPresence reports (set by NewEngine).
	Presence         PresenceReader
	PresenceInterval time.Duration
```

In `NewEngine`'s literal: `PresenceInterval: 15 * time.Second,`.

In `ConnectMessages`, read the presence **before** taking `e.mu` (it runs a subprocess), and append it last:

```go
func (e *Engine) ConnectMessages(ctx context.Context) []any {
	presence, hasPresence := e.presenceMsg(ctx)

	e.mu.RLock()
	defer e.mu.RUnlock()
	// … existing hello and snapshot code unchanged …

	msgs := []any{hello, snapshot}
	if hasPresence {
		msgs = append(msgs, presence)
	}
	return msgs
}
```

In `cmd/bridge/main.go`, after `rClient.OnMessage = engine.HandleRelayMessage`:

```go
	engine.Presence = bridge.ReadPresence
```

and after `engine.StartTurnWatch(ctx)`:

```go
	engine.StartPresence(ctx)
```

In `pkg/relayclient/client.go` `describe`, after the `HerdrStatusMsg` case:

```go
	case model.HostPresenceMsg, *model.HostPresenceMsg:
		return outMsg{typ: model.WireHostPresence}
```

and add `host_presence` to the "Disconnected: … dropped" list in `Send`'s doc comment.

- [ ] **Step 6: Run** `go test -race ./pkg/bridge/... ./pkg/relayclient/... ./cmd/bridge/...`, `GOOS=linux CGO_ENABLED=0 go build ./cmd/bridge` and `CGO_ENABLED=0 go build ./cmd/bridge`. Expected: PASS, both builds succeed.

- [ ] **Step 7: `AGENTS.md` §1.3**, the bridge bullet: change "Keep it thin: herdr ↔ relay translation plus on-demand transcript reads." to "Keep it thin: herdr ↔ relay translation, on-demand transcript reads, and (macOS only, the owner's decision of 2026-10-05) the host's input idle time and screen lock, read with `ioreg`, for push presence (`contracts.md` §4.3)."

- [ ] **Step 8: Commit:**

```bash
gofmt -l pkg/bridge pkg/relayclient cmd/bridge
git add pkg/bridge/presence.go pkg/bridge/presence_darwin.go pkg/bridge/presence_other.go \
  pkg/bridge/presence_test.go pkg/bridge/testdata/ioreg_hid.txt \
  pkg/bridge/testdata/ioreg_root_unlocked.txt pkg/bridge/testdata/ioreg_root_locked.txt \
  pkg/bridge/engine.go cmd/bridge/main.go pkg/relayclient/client.go AGENTS.md
git commit -m "Bridge: report the Mac's input idle time and screen lock (host_presence)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Guide, versions, status

**Files:**
- Modify: `docs/GUIDE.md`, `VERSIONS`, `docs/STATUS.md`

- [ ] **Step 1: `docs/GUIDE.md`.** Find where pushes or notifications are explained to the user (`grep -n -i "push\|notification" docs/GUIDE.md`). Add one line there: "While you use the Mac (input in the last 10 minutes, screen unlocked), the watch does not buzz; a prompt still waiting when you leave is pushed then. Rules and the setting: [`contracts.md` §4.3](reference/contracts.md)." Do not restate the thresholds anywhere else.
- [ ] **Step 2: `VERSIONS`:** `BRIDGE_VERSION=0.5.0`, `RELAY_VERSION=0.5.0`; then `herdr-plugin.toml`'s `version` to `0.5.0` and `make check-versions`.
- [ ] **Step 3: Run the full Go checks** (`go-backend.md` § Before claiming done): `gofmt -l cmd pkg deploy` (empty), `go vet ./...`, `go test -race ./...`.
- [ ] **Step 4: `docs/STATUS.md`:** under "Quiet pushes while the owner is at the Mac", replace the design line with: implemented in bridge and relay `0.5.0`, not deployed, plus the owner's checks of Task 6 as open items.
- [ ] **Step 5: Commit:**

```bash
git add docs/GUIDE.md VERSIONS herdr-plugin.toml docs/STATUS.md
git commit -m "Quiet pushes at the Mac: guide line, bridge and relay 0.5.0

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Deploy and verify on the owner's Mac and watch

- [ ] **Step 1: Confirm the lock key.** Start a background poll for 60 s, ask the owner to lock the screen (⌃⌘Q) for about 10 s, then unlock:

```bash
for i in $(seq 1 30); do /usr/sbin/ioreg -n Root -d1 | grep -o '"CGSSessionScreenIsLocked"=[A-Za-z]*' || echo none; sleep 2; done
```

Expected: `"CGSSessionScreenIsLocked"=Yes` while locked, `none` otherwise. If the key or format differs, fix `screenLockedRe` and `ioreg_root_locked.txt`, re-run Task 4's tests and commit.

- [ ] **Step 2: Deploy the relay** (`relay-deploy` skill), with `deploy.sh --sync-env` only if the owner set `AW_PUSH_PRESENCE_IDLE` in `agent-watch.env`; the default needs no env change. Check the relay log for the `push presence` line.
- [ ] **Step 3: Restart the bridge** (`make bridge`, then `agent-watch-bridge restart` or the menu bar's Restart). Check the relay log at debug, or that `push held back` lines appear while the owner works.
- [ ] **Step 4: The owner's checks** (spec § Verification, rows 2-5). Record the results in `docs/STATUS.md`. Only call the feature "verified on the watch" once the owner confirms.
- [ ] **Step 5: Close out:**
  - update "Deployed" in `docs/STATUS.md`;
  - move anything still true from `quiet-at-mac.md` to its home;
  - delete `docs/phases/quiet-at-mac.md` and this plan;
  - commit by path.
