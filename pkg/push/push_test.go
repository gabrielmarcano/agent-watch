package push

import (
	"context"
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

// The debounce map must not keep one entry per pane forever: entries the
// debounce can no longer match are dropped.
func TestDispatcher_LastPushIsPruned(t *testing.T) {
	d, clock, _ := newTestDispatcher(&mockSender{name: "mock"})

	for i := 0; i < 50; i++ {
		d.OnAgentUpdate(nil, blockedState(fmt.Sprintf("p%d", i), "a"))
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

// Pruning must not shorten the debounce: an entry younger than it survives.
func TestDispatcher_PruneKeepsDebounce(t *testing.T) {
	sender := &mockSender{name: "mock"}
	d, clock, timers := newTestDispatcher(sender)

	d.OnAgentUpdate(nil, blockedState("p1", "a")) // sent right away
	clock.Advance(d.DebounceDuration - time.Second)
	d.OnAgentUpdate(nil, blockedState("p2", "b")) // held
	clock.Advance(time.Second / 2)
	d.OnAgentUpdate(nil, blockedState("p2", "b")) // p2 debounced
	clock.Advance(time.Second)                    // p1 entry is now past the debounce
	d.OnAgentUpdate(nil, blockedState("p3", "c")) // prunes p1, keeps p2
	clock.Advance(time.Second)
	d.OnAgentUpdate(nil, blockedState("p2", "b")) // still debounced: p2's entry is 2.5 s old
	timers.Fire()
	d.Wait()

	var p2 int
	for _, m := range sender.getMessages() {
		if m.PaneID == "p2" {
			p2++
		}
	}
	if p2 != 1 {
		t.Fatalf("p2 pushed %d times, want 1: pruning broke the debounce", p2)
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

func TestDispatcher_Debounce(t *testing.T) {
	sender := &mockSender{name: "mock"}
	d, clock, timers := newTestDispatcher(sender)

	blocked := model.AgentState{PaneID: "p1", Label: "agent1", Status: model.StatusBlocked}

	// 1. First event goes through immediately
	d.OnAgentUpdate(nil, blocked)
	d.Wait()
	msgs := sender.getMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	// 2. Second event for same pane + event within 3 seconds -> dropped by debounce
	clock.Advance(3 * time.Second)
	d.OnAgentUpdate(nil, blocked)
	d.Wait()
	msgs = sender.getMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected second event within 3s to be debounced, got %d messages", len(msgs))
	}

	// 3. Different pane after 3 seconds -> allowed
	blocked2 := model.AgentState{PaneID: "p2", Label: "agent2", Status: model.StatusBlocked}
	d.OnAgentUpdate(nil, blocked2)
	d.Wait()
	// blocked2 is within window, held until the window timer fires
	if fired := timers.Fire(); len(fired) != 1 || fired[0] != d.WindowDuration {
		t.Fatalf("window timers fired = %v, want one of %v", fired, d.WindowDuration)
	}
	d.Wait()
	msgs = sender.getMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected message from different pane, got %d", len(msgs))
	}

	// 4. Same pane after 6 seconds (> 5s debounce window) -> allowed
	clock.Advance(6 * time.Second)
	d.OnAgentUpdate(nil, blocked)
	d.Wait()
	msgs = sender.getMessages()
	if len(msgs) != 3 {
		t.Fatalf("expected message after debounce window to succeed, got %d", len(msgs))
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

// A digest of finished agents only must not claim they need the user.
func TestDispatcher_DigestTitleFitsTheEvents(t *testing.T) {
	tests := []struct {
		name      string
		blocked   []string // panes that block, after p0
		done      []string // panes that finish, after p0
		wantTitle string
	}{
		{"done only", nil, []string{"p1", "p2", "p3", "p4"}, "4 agents finished"},
		{"blocked only", []string{"p1", "p2", "p3", "p4"}, nil, "4 agents need you"},
		{"mixed", []string{"p1", "p2"}, []string{"p3", "p4"}, "4 agents need you"},
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
