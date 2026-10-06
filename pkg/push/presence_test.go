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
	delete(states, "w1:p2")                                              // pane closed

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

func TestPresence_OfflineKeepsHeldBackForReconnect(t *testing.T) {
	d, clock, timers, sender, states := presenceDispatcher(t)
	d.OnHostPresence(time.Second, false)
	block(d, states, "w1:p1", 10)
	d.OnHostOffline()

	clock.Advance(time.Hour)
	timers.Fire()
	if n := pushed(d, sender); n != 0 {
		t.Fatalf("pushes while offline = %d, want 0", n)
	}
	// Offline means away: a new prompt pushes at once.
	block(d, states, "w1:p2", 20)
	if n := pushed(d, sender); n != 1 {
		t.Fatalf("pushes after a new prompt offline = %d, want 1", n)
	}

	// The reconnected host reports an owner who is gone: the held-back prompt
	// is caught up, in the window the w1:p2 push opened.
	d.OnHostPresence(20*time.Minute, false)
	flushWindow(d, timers)
	msgs := sender.getMessages()
	if len(msgs) != 2 || msgs[1].PaneID != "w1:p1" {
		t.Fatalf("pushes = %+v, want two, the second for w1:p1", msgs)
	}
}

func TestPresence_OfflineThenAnsweredIsNotCaughtUp(t *testing.T) {
	d, _, _, sender, states := presenceDispatcher(t)
	d.OnHostPresence(time.Second, false)
	block(d, states, "w1:p1", 10)
	d.OnHostOffline()
	states["w1:p1"] = agentAt("w1:p1", "w1:p1", model.StatusWorking, 11)

	d.OnHostPresence(20*time.Minute, false)
	if n := pushed(d, sender); n != 0 {
		t.Fatalf("catch-up pushes = %d, want 0", n)
	}
}

func TestPresence_LeavingBlockedDropsHeldBack(t *testing.T) {
	d, _, _, _, states := presenceDispatcher(t)
	d.OnHostPresence(time.Second, false)
	block(d, states, "w1:p1", 10)

	prev := states["w1:p1"]
	cur := agentAt("w1:p1", "w1:p1", model.StatusWorking, 11)
	states["w1:p1"] = cur
	d.OnAgentUpdate(&prev, cur)

	d.mu.Lock()
	_, held := d.presence.quiet["w1:p1"]
	d.mu.Unlock()
	if held {
		t.Fatal("w1:p1 is still held back after leaving blocked")
	}
}

func TestPresence_StaleReportIsAwayForNewPrompts(t *testing.T) {
	d, clock, _, sender, states := presenceDispatcher(t)
	d.OnHostPresence(time.Second, false)
	clock.Advance(PresenceStale)
	block(d, states, "w1:p1", 10)
	if n := pushed(d, sender); n != 1 {
		t.Fatalf("pushes with a stale report = %d, want 1", n)
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
	// The senders run concurrently, so the order of the two is not fixed.
	if got := eventsOf(msgs); len(got) != 2 || got[0] != "blocked:w1:p1" || got[1] != "digest:" {
		t.Fatalf("catch-up = %v, want the first blocked push then one digest", got)
	}
}
