package push

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

type mockSender struct {
	name     string
	mu       sync.Mutex
	messages []Message
	failErr  error
}

func (m *mockSender) Name() string {
	return m.name
}

func (m *mockSender) Send(ctx context.Context, msg Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failErr != nil {
		return m.failErr
	}
	m.messages = append(m.messages, msg)
	return nil
}

func (m *mockSender) getMessages() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Message, len(m.messages))
	copy(out, m.messages)
	return out
}

// fakeClock is the Dispatcher's injectable clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeTimers replaces time.AfterFunc for the window timer: nothing fires on
// its own, the test fires it.
type fakeTimers struct {
	mu      sync.Mutex
	pending []*fakeTimer
}

type fakeTimer struct {
	owner   *fakeTimers
	d       time.Duration
	f       func()
	stopped bool
}

func (t *fakeTimer) Stop() bool {
	t.owner.mu.Lock()
	defer t.owner.mu.Unlock()
	wasActive := !t.stopped
	t.stopped = true
	return wasActive
}

func (ft *fakeTimers) AfterFunc(d time.Duration, f func()) stopper {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	t := &fakeTimer{owner: ft, d: d, f: f}
	ft.pending = append(ft.pending, t)
	return t
}

// Fire runs every timer that is armed and not stopped, as if its duration had
// elapsed, and returns their durations.
func (ft *fakeTimers) Fire() []time.Duration {
	ft.mu.Lock()
	var due []*fakeTimer
	for _, t := range ft.pending {
		if !t.stopped {
			t.stopped = true
			due = append(due, t)
		}
	}
	ft.pending = nil
	ft.mu.Unlock()

	var ds []time.Duration
	for _, t := range due {
		ds = append(ds, t.d)
		t.f()
	}
	return ds
}

// newTestDispatcher returns a Dispatcher on a fake clock and fake timers.
func newTestDispatcher(senders ...Sender) (*Dispatcher, *fakeClock, *fakeTimers) {
	clock := &fakeClock{now: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)}
	timers := &fakeTimers{}
	d := NewDispatcher(senders, clock.Now, nil)
	d.afterFunc = timers.AfterFunc
	return d, clock, timers
}

func blockedState(pane, label string) model.AgentState {
	return model.AgentState{PaneID: pane, Label: label, Status: model.StatusBlocked}
}

// A Dispatcher built without NewDispatcher gets the defaults instead of
// panicking: no clock, no logger, no durations. No panics in daemons.
func TestDispatcher_ZeroValueIsUsable(t *testing.T) {
	good := &mockSender{name: "good"}
	failing := &mockSender{name: "failing", failErr: errors.New("boom")} // logs through Logger
	d := &Dispatcher{Senders: []Sender{good, failing}}

	d.OnAgentUpdate(nil, blockedState("p1", "one")) // sent at once
	d.OnAgentUpdate(nil, blockedState("p2", "two")) // held
	d.Flush()                                       // also stops the real window timer
	d.Wait()

	if n := len(good.getMessages()); n != 2 {
		t.Fatalf("good sender got %d messages, want 2", n)
	}
	if d.DebounceDuration != 5*time.Second || d.WindowDuration != 10*time.Second {
		t.Fatalf("durations = %v/%v, want the 5s/10s defaults", d.DebounceDuration, d.WindowDuration)
	}
	(&Dispatcher{}).Flush() // nothing to flush, nothing to panic on
}

// The debounce map must not keep one entry per pane forever: entries the
// debounce can no longer match are dropped.
func TestDispatcher_LastPushIsPruned(t *testing.T) {
	d, clock, timers := newTestDispatcher(&mockSender{name: "mock"})

	for i := 0; i < 50; i++ {
		d.OnAgentUpdate(nil, blockedState(fmt.Sprintf("p%d", i), "a")) // pushed at once
		timers.Fire()                                                  // its own window
	}
	clock.Advance(d.DebounceDuration)
	d.OnAgentUpdate(nil, blockedState("p-last", "a"))
	d.Wait()

	d.mu.Lock()
	n := len(d.lastPush)
	d.mu.Unlock()
	if n != 1 {
		t.Fatalf("lastPush holds %d entries after the debounce window, want 1 (only p-last)", n)
	}
}

// Pruning drops only the entries the debounce can no longer match: one
// younger than DebounceDuration survives.
func TestDispatcher_PruneKeepsDebounce(t *testing.T) {
	d, clock, timers := newTestDispatcher(&mockSender{name: "mock"})

	d.OnAgentUpdate(nil, blockedState("p1", "a")) // pushed at t0
	d.Wait()
	clock.Advance(d.DebounceDuration - time.Second)
	d.OnAgentUpdate(nil, blockedState("p2", "b")) // held
	timers.Fire()                                 // p2 pushed at t4
	d.Wait()
	clock.Advance(1500 * time.Millisecond)
	d.OnAgentUpdate(nil, blockedState("p3", "c")) // t5.5: prunes p1 (5.5 s), keeps p2 (1.5 s)
	d.Wait()

	d.mu.Lock()
	var keys []string
	for k := range d.lastPush {
		keys = append(keys, k)
	}
	d.mu.Unlock()
	sort.Strings(keys)
	if want := []string{"p2:blocked", "p3:blocked"}; fmt.Sprint(keys) != fmt.Sprint(want) {
		t.Fatalf("lastPush keys = %v, want %v", keys, want)
	}
}

func TestDispatcher_TransitionTable(t *testing.T) {
	sender := &mockSender{name: "mock"}
	currentTime := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	d := NewDispatcher([]Sender{sender}, func() time.Time { return currentTime }, nil)

	prompt := &model.PendingPrompt{
		Kind:        model.PromptPermission,
		Title:       "Bash command",
		Detail:      "go test ./...",
		Fingerprint: "fp123",
		Options: []model.PromptOption{
			{ID: "opt-1", Label: "Yes", Role: model.RoleAllowOnce},
			{ID: "opt-2", Label: "No", Role: model.RoleDeny},
		},
	}

	idle := model.AgentState{PaneID: "p1", Label: "test", Status: model.StatusIdle}
	working := model.AgentState{PaneID: "p1", Label: "test", Status: model.StatusWorking}
	blocked := model.AgentState{PaneID: "p1", Label: "test", Status: model.StatusBlocked, Prompt: prompt, StateChangeSeq: 42}
	done := model.AgentState{PaneID: "p1", Label: "test", Status: model.StatusDone, StateChangeSeq: 43}

	tests := []struct {
		name       string
		prev       *model.AgentState
		cur        model.AgentState
		wantPush   bool
		wantEvent  Event
		wantTitle  string
		wantBody   string
		wantAllow  string
		wantDeny   string
		wantSeq    uint64
		wantFinger string
	}{
		{
			name:       "nil -> blocked",
			prev:       nil,
			cur:        blocked,
			wantPush:   true,
			wantEvent:  EventBlocked,
			wantTitle:  "test needs approval",
			wantBody:   "Bash command: go test ./...",
			wantAllow:  "opt-1",
			wantDeny:   "opt-2",
			wantSeq:    42,
			wantFinger: "fp123",
		},
		{
			name:     "idle -> working",
			prev:     &idle,
			cur:      working,
			wantPush: false,
		},
		{
			name:     "blocked -> working",
			prev:     &blocked,
			cur:      working,
			wantPush: false,
		},
		{
			name:      "working -> done",
			prev:      &working,
			cur:       done,
			wantPush:  true,
			wantEvent: EventDone,
			wantTitle: "test finished",
			wantBody:  "Task finished",
			wantSeq:   43,
		},
		{
			name:     "idle -> done (not working -> done)",
			prev:     &idle,
			cur:      done,
			wantPush: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			currentTime = currentTime.Add(10 * time.Second) // advance clock past debounce
			msg, shouldPush := d.buildMessage(tc.prev, tc.cur)
			if shouldPush != tc.wantPush {
				t.Fatalf("buildMessage shouldPush = %v, want %v", shouldPush, tc.wantPush)
			}
			if !shouldPush {
				return
			}
			if msg.Event != tc.wantEvent {
				t.Errorf("Event = %s, want %s", msg.Event, tc.wantEvent)
			}
			if msg.Title != tc.wantTitle {
				t.Errorf("Title = %q, want %q", msg.Title, tc.wantTitle)
			}
			if msg.Body != tc.wantBody {
				t.Errorf("Body = %q, want %q", msg.Body, tc.wantBody)
			}
			if msg.AllowOptionID != tc.wantAllow {
				t.Errorf("AllowOptionID = %q, want %q", msg.AllowOptionID, tc.wantAllow)
			}
			if msg.DenyOptionID != tc.wantDeny {
				t.Errorf("DenyOptionID = %q, want %q", msg.DenyOptionID, tc.wantDeny)
			}
			if msg.StateChangeSeq != tc.wantSeq {
				t.Errorf("StateChangeSeq = %d, want %d", msg.StateChangeSeq, tc.wantSeq)
			}
			if msg.Fingerprint != tc.wantFinger {
				t.Errorf("Fingerprint = %q, want %q", msg.Fingerprint, tc.wantFinger)
			}
		})
	}
}

func TestDispatcher_UnknownPromptBody(t *testing.T) {
	d := NewDispatcher(nil, nil, nil)
	blocked := model.AgentState{
		PaneID: "p1",
		Label:  "claude",
		Status: model.StatusBlocked,
		Prompt: &model.PendingPrompt{
			Kind: model.PromptUnknown,
		},
	}
	msg, shouldPush := d.buildMessage(nil, blocked)
	if !shouldPush {
		t.Fatalf("expected shouldPush true")
	}
	if msg.Body != "Open Agent Watch to see the question" {
		t.Fatalf("expected fallback body for unknown prompt, got %q", msg.Body)
	}
}

// The debounce spaces a pane's pushes: a new blocked prompt within 5 s of the
// pane's last push is held until the window ends, not pushed at once.
func TestDispatcher_Debounce(t *testing.T) {
	sender := &mockSender{name: "mock"}
	d, clock, timers := newTestDispatcher(sender)

	// 1. First event goes through immediately
	b1 := agentAt("p1", "agent1", model.StatusBlocked, 1)
	d.OnAgentUpdate(nil, b1)
	d.Wait()
	if n := len(sender.getMessages()); n != 1 {
		t.Fatalf("expected 1 message, got %d", n)
	}

	// 2. A new prompt on the same pane 3 s later is held, not pushed at once
	clock.Advance(3 * time.Second)
	w2 := agentAt("p1", "agent1", model.StatusWorking, 2)
	d.OnAgentUpdate(&b1, w2)
	b3 := agentAt("p1", "agent1", model.StatusBlocked, 3)
	d.OnAgentUpdate(&w2, b3)
	d.Wait()
	if n := len(sender.getMessages()); n != 1 {
		t.Fatalf("a prompt within 3 s went out at once: %d messages", n)
	}

	// 3. Another pane is held too (the window is open); the timer sends both
	d.OnAgentUpdate(nil, blockedState("p2", "agent2"))
	d.Wait()
	if fired := timers.Fire(); len(fired) != 1 || fired[0] != d.WindowDuration {
		t.Fatalf("window timers fired = %v, want one of %v", fired, d.WindowDuration)
	}
	d.Wait()
	if got, want := eventsOf(sender.getMessages()), []string{"blocked:p1", "blocked:p1", "blocked:p2"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("pushes = %v, want %v", got, want)
	}

	// 4. Same pane 6 s after its last push (> 5 s debounce) -> at once
	clock.Advance(6 * time.Second)
	w4 := agentAt("p1", "agent1", model.StatusWorking, 4)
	d.OnAgentUpdate(&b3, w4)
	d.OnAgentUpdate(&w4, agentAt("p1", "agent1", model.StatusBlocked, 5))
	d.Wait()
	if n := len(sender.getMessages()); n != 4 {
		t.Fatalf("expected the prompt after the debounce to go out at once, got %d messages", n)
	}
}

// promptState is pane p1 blocked on a prompt with the given fingerprint.
func promptState(seq uint64, fingerprint string) model.AgentState {
	s := agentAt("p1", "one", model.StatusBlocked, seq)
	s.Prompt = &model.PendingPrompt{
		Kind: model.PromptPermission, Title: "Bash command", Detail: fingerprint, Fingerprint: fingerprint,
		Options: []model.PromptOption{{ID: fingerprint + "-yes", Label: "Yes", Role: model.RoleAllowOnce}},
	}
	return s
}

// p1Pushes returns the fingerprints of the blocked pushes for p1, in order.
func p1Pushes(sender *mockSender) []string {
	var fps []string
	for _, m := range sender.getMessages() {
		if m.PaneID == "p1" && m.Event == EventBlocked {
			fps = append(fps, m.Fingerprint)
		}
	}
	return fps
}

// Trailing edge: a new prompt on a pane within 5 s of its last push is not
// swallowed by the debounce. It is held and pushed when the window ends, for
// the pane's current prompt.
func TestDispatcher_QuickReblockIsHeldNotLost(t *testing.T) {
	sender := &mockSender{name: "mock"}
	d, clock, timers := newTestDispatcher(sender)

	onA := promptState(10, "fp-A")
	d.OnAgentUpdate(nil, onA) // pushed at once
	d.Wait()
	clock.Advance(time.Second)
	working := agentAt("p1", "one", model.StatusWorking, 11)
	d.OnAgentUpdate(&onA, working) // answered on the wrist
	clock.Advance(time.Second)
	d.OnAgentUpdate(&working, promptState(12, "fp-B")) // next prompt, 2 s after the push
	d.Wait()
	if got := p1Pushes(sender); fmt.Sprint(got) != "[fp-A]" {
		t.Fatalf("before the flush p1 pushes = %v, want [fp-A]", got)
	}

	flushWindow(d, timers)
	if got := p1Pushes(sender); fmt.Sprint(got) != "[fp-A fp-B]" {
		t.Fatalf("p1 pushes = %v, want [fp-A fp-B]: the second prompt was lost", got)
	}
}

// A held prompt with no window open opens one of its own; a push from
// another pane in that window still goes out at once.
func TestDispatcher_HeldPromptOpensItsOwnWindow(t *testing.T) {
	sender := &mockSender{name: "mock"}
	d, clock, timers := newTestDispatcher(sender)

	d.OnAgentUpdate(nil, blockedState("p0", "zero")) // opens a window
	d.Wait()
	clock.Advance(9 * time.Second)
	onA := promptState(10, "fp-A")
	d.OnAgentUpdate(nil, onA) // held
	flushWindow(d, timers)    // t9: fp-A pushed, no window open any more
	clock.Advance(time.Second)
	working := agentAt("p1", "one", model.StatusWorking, 11)
	d.OnAgentUpdate(&onA, working)
	d.OnAgentUpdate(&working, promptState(12, "fp-B")) // 1 s after fp-A: held
	d.Wait()
	if got := p1Pushes(sender); fmt.Sprint(got) != "[fp-A]" {
		t.Fatalf("p1 pushes = %v, want [fp-A] until the flush", got)
	}

	d.OnAgentUpdate(nil, blockedState("p2", "two")) // first push of the new window
	d.Wait()
	if msgs := sender.getMessages(); msgs[len(msgs)-1].PaneID != "p2" {
		t.Fatalf("p2 was held behind a held prompt instead of going out at once: %v", eventsOf(msgs))
	}

	if fired := timers.Fire(); len(fired) != 1 {
		t.Fatalf("held prompt armed %d window timers, want 1", len(fired))
	}
	d.Wait()
	if got := p1Pushes(sender); fmt.Sprint(got) != "[fp-A fp-B]" {
		t.Fatalf("p1 pushes = %v, want [fp-A fp-B]", got)
	}
}

// The prompt a pane's notification already shows (same state_change_seq and
// fingerprint) is never pushed again, whether it comes back at once, after
// the debounce, or at the flush.
func TestDispatcher_SamePromptIsNeverPushedTwice(t *testing.T) {
	t.Run("re-reported", func(t *testing.T) {
		sender := &mockSender{name: "mock"}
		d, clock, timers := newTestDispatcher(sender)

		onA := promptState(10, "fp-A")
		d.OnAgentUpdate(nil, onA)
		clock.Advance(time.Second)
		d.OnAgentUpdate(nil, onA) // the relay lost prev: same prompt again
		flushWindow(d, timers)
		clock.Advance(6 * time.Second)
		d.OnAgentUpdate(nil, onA) // again, past the debounce
		flushWindow(d, timers)

		if got := p1Pushes(sender); fmt.Sprint(got) != "[fp-A]" {
			t.Fatalf("p1 pushes = %v, want [fp-A] once", got)
		}
	})

	t.Run("pushed at once, then flushed", func(t *testing.T) {
		sender := &mockSender{name: "mock"}
		d, clock, timers := newTestDispatcher(sender)

		onA := promptState(10, "fp-A")
		d.OnAgentUpdate(nil, onA) // t0
		flushWindow(d, timers)
		clock.Advance(time.Second)
		w := agentAt("p1", "one", model.StatusWorking, 11)
		d.OnAgentUpdate(&onA, w)
		onB := promptState(12, "fp-B")
		d.OnAgentUpdate(&w, onB) // t1: held, opens a window
		clock.Advance(5 * time.Second)
		w2 := agentAt("p1", "one", model.StatusWorking, 13)
		d.OnAgentUpdate(&onB, w2)
		d.OnAgentUpdate(&w2, promptState(14, "fp-C")) // t6: past the debounce, pushed at once
		flushWindow(d, timers)                        // the held fp-B is now fp-C: already shown

		if got := p1Pushes(sender); fmt.Sprint(got) != "[fp-A fp-C]" {
			t.Fatalf("p1 pushes = %v, want [fp-A fp-C]", got)
		}
	})
}

// A held prompt answered before the window ends is not pushed.
func TestDispatcher_HeldPromptAnsweredBeforeFlush(t *testing.T) {
	sender := &mockSender{name: "mock"}
	d, clock, timers := newTestDispatcher(sender)

	onA := promptState(10, "fp-A")
	d.OnAgentUpdate(nil, onA)
	clock.Advance(time.Second)
	w := agentAt("p1", "one", model.StatusWorking, 11)
	d.OnAgentUpdate(&onA, w)
	onB := promptState(12, "fp-B")
	d.OnAgentUpdate(&w, onB) // held
	d.OnAgentUpdate(&onB, agentAt("p1", "one", model.StatusWorking, 13))
	flushWindow(d, timers)

	if got := p1Pushes(sender); fmt.Sprint(got) != "[fp-A]" {
		t.Fatalf("p1 pushes = %v, want [fp-A]", got)
	}
}

// done keeps the plain debounce: a second "finished" within 5 s is dropped,
// the pane's notification already says it finished.
func TestDispatcher_QuickDoneIsDropped(t *testing.T) {
	sender := &mockSender{name: "mock"}
	d, clock, timers := newTestDispatcher(sender)

	finish(d, "p1", "one", 5) // pushed at once
	d.Wait()
	clock.Advance(2 * time.Second)
	finish(d, "p1", "one", 7)
	flushWindow(d, timers)

	if got := eventsOf(sender.getMessages()); fmt.Sprint(got) != "[done:p1]" {
		t.Fatalf("pushes = %v, want one done", got)
	}
}

// agentAt returns pane's state with the given status and seq.
func agentAt(pane, label string, status model.AgentStatus, seq uint64) model.AgentState {
	return model.AgentState{PaneID: pane, Label: label, Status: status, StateChangeSeq: seq}
}

// finish reports pane going working → done, which pushes a done message.
func finish(d *Dispatcher, pane, label string, seq uint64) {
	working := agentAt(pane, label, model.StatusWorking, seq-1)
	d.OnAgentUpdate(&working, agentAt(pane, label, model.StatusDone, seq))
}

// flushWindow fires the window timer and waits for every send.
func flushWindow(d *Dispatcher, timers *fakeTimers) {
	timers.Fire()
	d.Wait()
}

// The digest counts agents, not messages: an agent held twice in the window
// counts once, and its label appears once.
func TestDispatcher_DigestCountsDistinctAgents(t *testing.T) {
	sender := &mockSender{name: "mock"}
	d, clock, timers := newTestDispatcher(sender)

	d.OnAgentUpdate(nil, blockedState("p1", "a")) // sent right away
	d.Wait()
	finish(d, "p2", "b", 10) // held
	clock.Advance(d.DebounceDuration + time.Second)
	finish(d, "p2", "b", 12) // held again: past the debounce
	d.OnAgentUpdate(nil, blockedState("p3", "c"))
	d.OnAgentUpdate(nil, blockedState("p4", "d"))
	flushWindow(d, timers)

	msgs := sender.getMessages()
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (p1 + one digest): %+v", len(msgs), msgs)
	}
	digest := msgs[1]
	if digest.Event != EventDigest {
		t.Fatalf("second message is %s, want digest", digest.Event)
	}
	if digest.Title != "3 agents need you" {
		t.Errorf("digest title = %q, want %q (p2, p3, p4)", digest.Title, "3 agents need you")
	}
	if digest.Body != "b, c, d" {
		t.Errorf("digest body = %q, want %q", digest.Body, "b, c, d")
	}
}

// The digest body obeys the same 240-character limit as every other body.
func TestDispatcher_DigestBodyIsTruncated(t *testing.T) {
	sender := &mockSender{name: "mock"}
	d, _, timers := newTestDispatcher(sender)

	d.OnAgentUpdate(nil, blockedState("p0", "zero"))
	d.Wait()
	for i := 1; i <= 30; i++ {
		d.OnAgentUpdate(nil, blockedState(fmt.Sprintf("p%d", i), fmt.Sprintf("a-rather-long-label-%02d", i)))
	}
	flushWindow(d, timers)

	msgs := sender.getMessages()
	if len(msgs) != 2 || msgs[1].Event != EventDigest {
		t.Fatalf("want p0 then one digest, got %d messages", len(msgs))
	}
	if n := utf8.RuneCountInString(msgs[1].Body); n > 240 || !strings.HasSuffix(msgs[1].Body, "…") {
		t.Fatalf("digest body has %d runes (want <= 240, cut with …): %q", n, msgs[1].Body)
	}
}

// A digest of finished agents only must not claim they need the user, and
// only a digest covering a blocked agent is urgent (FCM high priority).
func TestDispatcher_DigestTitleFitsTheEvents(t *testing.T) {
	tests := []struct {
		name       string
		blocked    []string // panes that block, after p0
		done       []string // panes that finish, after p0
		wantTitle  string
		wantUrgent bool
	}{
		{"done only", nil, []string{"p1", "p2", "p3", "p4"}, "4 agents finished", false},
		{"blocked only", []string{"p1", "p2", "p3", "p4"}, nil, "4 agents need you", true},
		{"mixed", []string{"p1", "p2"}, []string{"p3", "p4"}, "4 agents need you", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &mockSender{name: "mock"}
			d, _, timers := newTestDispatcher(sender)

			finish(d, "p0", "zero", 5) // sent right away
			d.Wait()
			for _, p := range tt.blocked {
				d.OnAgentUpdate(nil, blockedState(p, p))
			}
			for _, p := range tt.done {
				finish(d, p, p, 5)
			}
			flushWindow(d, timers)

			msgs := sender.getMessages()
			if len(msgs) != 2 || msgs[1].Event != EventDigest {
				t.Fatalf("want p0 then one digest, got %+v", msgs)
			}
			if msgs[1].Title != tt.wantTitle {
				t.Errorf("digest title = %q, want %q", msgs[1].Title, tt.wantTitle)
			}
			if msgs[1].AnyBlocked != tt.wantUrgent {
				t.Errorf("digest AnyBlocked = %t, want %t", msgs[1].AnyBlocked, tt.wantUrgent)
			}
		})
	}
}

// A message held for the window is dropped at flush time when its agent has
// left the state it announces: no "needs approval" for an answered prompt, no
// "finished" for an agent that is working again. The survivors are counted
// again, so the digest threshold applies to what actually goes out.
func TestDispatcher_FlushDropsStaleMessages(t *testing.T) {
	sender := &mockSender{name: "mock"}
	d, _, timers := newTestDispatcher(sender)

	d.OnAgentUpdate(nil, blockedState("p1", "a")) // sent right away
	for _, p := range []string{"p2", "p3", "p4"} {
		d.OnAgentUpdate(nil, blockedState(p, p))
	}
	finish(d, "p5", "p5", 7)

	// Before the flush: p2's prompt is answered, p5 starts working again.
	blocked2 := blockedState("p2", "p2")
	d.OnAgentUpdate(&blocked2, agentAt("p2", "p2", model.StatusWorking, 3))
	done5 := agentAt("p5", "p5", model.StatusDone, 7)
	d.OnAgentUpdate(&done5, agentAt("p5", "p5", model.StatusWorking, 8))
	flushWindow(d, timers)

	var got []string
	for _, m := range sender.getMessages() {
		got = append(got, fmt.Sprintf("%s:%s", m.Event, m.PaneID))
	}
	sort.Strings(got) // sends run concurrently
	// 1 sent + p3 + p4 = 3 pushes: no digest, and nothing for p2 or p5.
	want := []string{"blocked:p1", "blocked:p3", "blocked:p4"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("pushes = %v, want %v", got, want)
	}
}

// When an agent answers a held prompt and blocks again on a new one (whose own
// push the debounce swallowed), the flush pushes the new prompt, never the
// answered one: its fingerprint and option ids would be stale.
func TestDispatcher_FlushPushesTheCurrentPrompt(t *testing.T) {
	sender := &mockSender{name: "mock"}
	d, clock, timers := newTestDispatcher(sender)

	promptA := &model.PendingPrompt{
		Kind: model.PromptPermission, Title: "Bash command", Detail: "rm -rf build", Fingerprint: "fp-A",
		Options: []model.PromptOption{{ID: "a-yes", Label: "Yes", Role: model.RoleAllowOnce}, {ID: "a-no", Label: "No", Role: model.RoleDeny}},
	}
	promptB := &model.PendingPrompt{
		Kind: model.PromptPermission, Title: "Edit file", Detail: "main.go", Fingerprint: "fp-B",
		Options: []model.PromptOption{{ID: "b-yes", Label: "Yes", Role: model.RoleAllowOnce}, {ID: "b-no", Label: "No", Role: model.RoleDeny}},
	}
	blockedOn := func(p *model.PendingPrompt, seq uint64) model.AgentState {
		s := agentAt("p2", "two", model.StatusBlocked, seq)
		s.Prompt = p
		return s
	}

	d.OnAgentUpdate(nil, blockedState("p1", "one")) // opens the window
	d.Wait()
	onA := blockedOn(promptA, 20)
	d.OnAgentUpdate(nil, onA) // held
	clock.Advance(time.Second)
	working := agentAt("p2", "two", model.StatusWorking, 21)
	d.OnAgentUpdate(&onA, working)                    // A answered
	d.OnAgentUpdate(&working, blockedOn(promptB, 22)) // B: debounced
	flushWindow(d, timers)

	msgs := sender.getMessages()
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want p1 and one for p2: %+v", len(msgs), msgs)
	}
	m := msgs[1]
	if m.PaneID != "p2" || m.Event != EventBlocked {
		t.Fatalf("second push = %s:%s, want blocked:p2", m.Event, m.PaneID)
	}
	if m.Fingerprint != "fp-B" || m.AllowOptionID != "b-yes" || m.DenyOptionID != "b-no" ||
		m.StateChangeSeq != 22 || m.Body != "Edit file: main.go" {
		t.Fatalf("p2 push describes the answered prompt: %+v", m)
	}
}

func TestDispatcher_Digest(t *testing.T) {
	sender := &mockSender{name: "mock"}
	d, _, timers := newTestDispatcher(sender)

	// Send 5 distinct blocked panes within 10s:
	// First pane should be sent immediately.
	// Panes 2..5 should be held in window.
	// On flush, because total is 5 (> 3), a single digest message should be sent
	// listing the other 4 panes.
	for i := 1; i <= 5; i++ {
		cur := model.AgentState{
			PaneID: fmt.Sprintf("p%d", i),
			Label:  fmt.Sprintf("agent%d", i),
			Status: model.StatusBlocked,
		}
		d.OnAgentUpdate(nil, cur)
	}

	d.Wait()
	// Only the first message should have been sent so far
	msgs := sender.getMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message sent immediately, got %d", len(msgs))
	}
	if msgs[0].PaneID != "p1" {
		t.Fatalf("expected first message to be p1, got %s", msgs[0].PaneID)
	}

	flushWindow(d, timers)

	msgs = sender.getMessages()
	// Should now have 2 messages: the immediate one, and the 1 digest
	if len(msgs) != 2 {
		t.Fatalf("expected total 2 messages (1 immediate + 1 digest), got %d", len(msgs))
	}

	digest := msgs[1]
	if digest.Event != EventDigest {
		t.Fatalf("expected second message event to be digest, got %s", digest.Event)
	}
	if digest.Title != "4 agents need you" {
		t.Fatalf("expected title '4 agents need you', got %q", digest.Title)
	}
	expectedBody := "agent2, agent3, agent4, agent5"
	if digest.Body != expectedBody {
		t.Fatalf("expected digest body %q, got %q", expectedBody, digest.Body)
	}
}

// resolvedMock is a mockSender whose app can withdraw a notification (FCM).
type resolvedMock struct {
	mockSender
}

func (m *resolvedMock) SendsResolved() bool { return true }

func newResolvedMock() *resolvedMock {
	return &resolvedMock{mockSender: mockSender{name: "fcm"}}
}

func eventsOf(msgs []Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, fmt.Sprintf("%s:%s", m.Event, m.PaneID))
	}
	sort.Strings(out)
	return out
}

// When an agent whose blocked push went out leaves blocked, for any status,
// the app gets one "resolved" message so it can withdraw the notification;
// senders that cannot withdraw (ntfy) never get it.
func TestDispatcher_ResolvedWithdrawsABlockedPush(t *testing.T) {
	for _, next := range []model.AgentStatus{model.StatusWorking, model.StatusIdle, model.StatusDone, model.StatusUnknown} {
		t.Run(string(next), func(t *testing.T) {
			fcm := newResolvedMock()
			ntfy := &mockSender{name: "ntfy"}
			d, _, _ := newTestDispatcher(fcm, ntfy)

			blocked := agentAt("p1", "one", model.StatusBlocked, 42)
			d.OnAgentUpdate(nil, blocked)
			d.Wait()
			left := agentAt("p1", "one", next, 43)
			d.OnAgentUpdate(&blocked, left)
			d.Wait()
			// Nothing is left to withdraw after that.
			d.OnAgentUpdate(&left, agentAt("p1", "one", model.StatusIdle, 44))
			d.Wait()

			got := fcm.getMessages()
			if len(got) != 2 {
				t.Fatalf("fcm got %v, want blocked then one resolved", eventsOf(got))
			}
			if want := (Message{Event: EventResolved, PaneID: "p1", StateChangeSeq: 43}); got[1] != want {
				t.Fatalf("resolved message = %+v, want %+v", got[1], want)
			}
			if n := len(ntfy.getMessages()); n != 1 {
				t.Fatalf("ntfy got %d messages, want only the blocked one", n)
			}
		})
	}
}

// A resolved message goes out at once: it is not debounced, not held for the
// window, not counted toward a digest, and never part of one.
func TestDispatcher_ResolvedSkipsTheWindow(t *testing.T) {
	fcm := newResolvedMock()
	d, _, timers := newTestDispatcher(fcm)

	b1 := agentAt("p1", "one", model.StatusBlocked, 1)
	d.OnAgentUpdate(nil, b1) // sent, opens the window
	d.Wait()
	d.OnAgentUpdate(&b1, agentAt("p1", "one", model.StatusWorking, 2))
	d.Wait()
	if got := fcm.getMessages(); len(got) != 2 || got[1].Event != EventResolved {
		t.Fatalf("resolved was not sent at once: %v", eventsOf(got))
	}

	d.OnAgentUpdate(nil, blockedState("p2", "two"))
	d.OnAgentUpdate(nil, blockedState("p3", "three"))
	flushWindow(d, timers)

	// 1 sent + 2 held = 3 pushes: no digest (a counted resolved would make 4).
	want := []string{"blocked:p1", "blocked:p2", "blocked:p3", "resolved:p1"}
	if got := eventsOf(fcm.getMessages()); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("pushes = %v, want %v", got, want)
	}
}

// Only a pane whose own blocked push went out gets a resolved one: not a
// pane held and answered before the flush, not a pane covered by a digest,
// not a pane that never blocked.
func TestDispatcher_ResolvedOnlyAfterABlockedPush(t *testing.T) {
	fcm := newResolvedMock()
	d, _, timers := newTestDispatcher(fcm)

	d.OnAgentUpdate(nil, blockedState("p0", "zero")) // sent, opens the window
	d.Wait()
	b1 := blockedState("p1", "one")
	d.OnAgentUpdate(nil, b1)
	d.OnAgentUpdate(&b1, agentAt("p1", "one", model.StatusWorking, 1)) // answered while held
	for _, p := range []string{"p2", "p3", "p4"} {
		d.OnAgentUpdate(nil, blockedState(p, p)) // will be a digest
	}
	flushWindow(d, timers)

	for _, p := range []string{"p2", "p3", "p4"} {
		b := blockedState(p, p)
		d.OnAgentUpdate(&b, agentAt(p, p, model.StatusWorking, 1))
	}
	finish(d, "p5", "five", 3)
	d.Wait()

	for _, m := range fcm.getMessages() {
		if m.Event == EventResolved {
			t.Fatalf("unexpected resolved for %s; pushes: %v", m.PaneID, eventsOf(fcm.getMessages()))
		}
	}
}

// A blocked push sent one by one at the end of the window is withdrawn too.
func TestDispatcher_ResolvedAfterAFlushedBlockedPush(t *testing.T) {
	fcm := newResolvedMock()
	d, _, timers := newTestDispatcher(fcm)

	d.OnAgentUpdate(nil, blockedState("p0", "zero"))
	b1 := blockedState("p1", "one")
	d.OnAgentUpdate(nil, b1) // held, then sent on its own
	flushWindow(d, timers)
	d.OnAgentUpdate(&b1, agentAt("p1", "one", model.StatusWorking, 8))
	d.Wait()

	want := []string{"blocked:p0", "blocked:p1", "resolved:p1"}
	if got := eventsOf(fcm.getMessages()); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("pushes = %v, want %v", got, want)
	}
}

// Panes removed while blocked are never seen leaving blocked, so the set of
// shown blocked pushes is capped; the oldest entries go first.
func TestDispatcher_ShownBlockedIsBounded(t *testing.T) {
	fcm := newResolvedMock()
	d, clock, timers := newTestDispatcher(fcm)

	n := maxShownBlocked + 10
	for i := 0; i < n; i++ {
		d.OnAgentUpdate(nil, blockedState(fmt.Sprintf("p%d", i), "a")) // each opens its own window
		timers.Fire()
		clock.Advance(time.Second)
	}
	d.Wait()

	d.mu.Lock()
	size := len(d.blockedShown)
	d.mu.Unlock()
	if size > maxShownBlocked {
		t.Fatalf("blockedShown holds %d panes, want at most %d", size, maxShownBlocked)
	}

	oldest, newest := blockedState("p0", "a"), blockedState(fmt.Sprintf("p%d", n-1), "a")
	d.OnAgentUpdate(&oldest, agentAt("p0", "a", model.StatusWorking, 1))
	d.OnAgentUpdate(&newest, agentAt(newest.PaneID, "a", model.StatusWorking, 1))
	d.Wait()
	var resolved []string
	for _, m := range fcm.getMessages() {
		if m.Event == EventResolved {
			resolved = append(resolved, m.PaneID)
		}
	}
	if fmt.Sprint(resolved) != fmt.Sprint([]string{newest.PaneID}) {
		t.Fatalf("resolved sent for %v, want only the newest pane %s", resolved, newest.PaneID)
	}
}

// chanSender reports every message it gets on a channel, and can be held
// inside Send until released.
type chanSender struct {
	name    string
	got     chan Message
	release chan struct{} // nil: never blocks
}

func newChanSender(name string) *chanSender {
	return &chanSender{name: name, got: make(chan Message, 64)}
}

func (s *chanSender) Name() string { return s.name }

func (s *chanSender) Send(ctx context.Context, m Message) error {
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.got <- m
	return nil
}

// A sender stuck in Send delays neither OnAgentUpdate nor the other senders.
func TestDispatcher_FailureIsolation(t *testing.T) {
	stuck := newChanSender("stuck")
	stuck.release = make(chan struct{})
	fast := newChanSender("fast")
	d, _, _ := newTestDispatcher(stuck, fast)

	returned := make(chan struct{})
	go func() {
		d.OnAgentUpdate(nil, blockedState("p1", "agent1"))
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(testDeadline):
		t.Fatalf("OnAgentUpdate waited for a stuck sender")
	}

	select {
	case m := <-fast.got:
		if m.PaneID != "p1" {
			t.Fatalf("fast sender got pane %q, want p1", m.PaneID)
		}
	case <-time.After(testDeadline):
		t.Fatalf("fast sender waited for the stuck one")
	}

	close(stuck.release)
	d.Wait()
	select {
	case <-stuck.got:
	default:
		t.Fatalf("the stuck sender never got the message once released")
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := TruncateRunes("hello", 10); got != "hello" {
		t.Errorf("got %q, want 'hello'", got)
	}
	if got := TruncateRunes("abcdef", 4); got != "abc…" {
		t.Errorf("got %q, want 'abc…'", got)
	}
	// Multi-byte UTF-8 test
	s := "日本語テスト文字列"
	if got := TruncateRunes(s, 5); got != "日本語テ…" {
		t.Errorf("got %q, want '日本語テ…'", got)
	}
}

// With ReplyWait set, a done push waits for the pane's reply (the history item
// the bridge sends right after the transition) and shows it as its body.
func TestDispatcher_DoneWaitsForTheReply(t *testing.T) {
	sender := &mockSender{name: "mock"}
	d, _, timers := newTestDispatcher(sender)
	d.ReplyWait = 3 * time.Second

	finish(d, "p1", "api", 5)
	d.Wait()
	if got := sender.getMessages(); len(got) != 0 {
		t.Fatalf("pushed before the reply: %v", eventsOf(got))
	}

	d.OnHistoryItem(model.HistoryItem{PaneID: "p2", Response: "another pane"})
	d.OnHistoryItem(model.HistoryItem{PaneID: "p1", Response: "## Done\n\nI fixed **the login loop** in `auth.ts`.\n- added a test"})
	d.Wait()
	got := sender.getMessages()
	if len(got) != 1 || got[0].Event != EventDone {
		t.Fatalf("pushes = %v, want one done", eventsOf(got))
	}
	if want := "Done I fixed the login loop in auth.ts. added a test"; got[0].Body != want {
		t.Errorf("body = %q, want %q", got[0].Body, want)
	}

	// The reply-wait timer that no longer matters does nothing when it fires.
	flushWindow(d, timers)
	if n := len(sender.getMessages()); n != 1 {
		t.Errorf("pushes after the timers = %d, want 1", n)
	}
}

// No reply within ReplyWait: the done push goes out with the generic body.
func TestDispatcher_DoneWithoutAReply(t *testing.T) {
	sender := &mockSender{name: "mock"}
	d, _, timers := newTestDispatcher(sender)
	d.ReplyWait = 3 * time.Second

	finish(d, "p1", "api", 5)
	timers.Fire() // the reply wait ends
	d.Wait()
	got := sender.getMessages()
	if len(got) != 1 || got[0].Body != "Task finished" {
		t.Fatalf("pushes = %+v, want one done with the generic body", got)
	}

	// A reply after the push changes nothing.
	d.OnHistoryItem(model.HistoryItem{PaneID: "p1", Response: "late"})
	d.Wait()
	if n := len(sender.getMessages()); n != 1 {
		t.Errorf("pushes = %d, want 1", n)
	}
}

// ReplyWait 0 (the zero value) keeps the old behaviour: done pushes at once.
func TestDispatcher_DoneAtOnceWithoutReplyWait(t *testing.T) {
	sender := &mockSender{name: "mock"}
	d, _, _ := newTestDispatcher(sender)

	finish(d, "p1", "api", 5)
	d.Wait()
	if got := sender.getMessages(); len(got) != 1 || got[0].Body != "Task finished" {
		t.Fatalf("pushes = %+v", got)
	}
}

func TestReplyPreview(t *testing.T) {
	long := strings.Repeat("word ", 100)
	if got := replyPreview(long); utf8.RuneCountInString(got) != 240 || !strings.HasSuffix(got, "…") {
		t.Errorf("preview not cut to 240 runes: %d", utf8.RuneCountInString(got))
	}
	if got := replyPreview("  \n\n "); got != "" {
		t.Errorf("blank reply preview = %q", got)
	}
}
