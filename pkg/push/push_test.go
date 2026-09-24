package push

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

type mockSender struct {
	name     string
	mu       sync.Mutex
	messages []Message
	delay    time.Duration
	failErr  error
}

func (m *mockSender) Name() string {
	return m.name
}

func (m *mockSender) Send(ctx context.Context, msg Message) error {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
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
	currentTime := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	d := NewDispatcher([]Sender{sender}, func() time.Time { return currentTime }, nil)

	blocked := model.AgentState{PaneID: "p1", Label: "agent1", Status: model.StatusBlocked}

	// 1. First event goes through immediately
	d.OnAgentUpdate(nil, blocked)
	d.Wait()
	msgs := sender.getMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	// 2. Second event for same pane + event within 3 seconds -> dropped by debounce
	currentTime = currentTime.Add(3 * time.Second)
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
	// blocked2 is within window, held for flush
	d.Flush()
	d.Wait()
	msgs = sender.getMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected message from different pane, got %d", len(msgs))
	}

	// 4. Same pane after 6 seconds (> 5s debounce window) -> allowed
	currentTime = currentTime.Add(6 * time.Second)
	d.OnAgentUpdate(nil, blocked)
	d.Wait()
	msgs = sender.getMessages()
	if len(msgs) != 3 {
		t.Fatalf("expected message after debounce window to succeed, got %d", len(msgs))
	}
}

func TestDispatcher_Digest(t *testing.T) {
	sender := &mockSender{name: "mock"}
	currentTime := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	d := NewDispatcher([]Sender{sender}, func() time.Time { return currentTime }, nil)

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

	// Flush window
	d.Flush()
	d.Wait()

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

func TestDispatcher_FailureIsolation(t *testing.T) {
	slowSender := &mockSender{
		name:  "slow",
		delay: 100 * time.Millisecond,
	}
	fastSender := &mockSender{
		name: "fast",
	}

	d := NewDispatcher([]Sender{slowSender, fastSender}, nil, nil)
	blocked := model.AgentState{PaneID: "p1", Label: "agent1", Status: model.StatusBlocked}

	start := time.Now()
	d.OnAgentUpdate(nil, blocked)
	elapsed := time.Since(start)

	// OnAgentUpdate must be asynchronous and return immediately without waiting for slow sender
	if elapsed > 50*time.Millisecond {
		t.Fatalf("OnAgentUpdate blocked for %v, expected non-blocking dispatch", elapsed)
	}

	// Fast sender should receive message before slow sender finishes
	time.Sleep(10 * time.Millisecond)
	if len(fastSender.getMessages()) != 1 {
		t.Fatalf("expected fast sender to receive message promptly")
	}

	d.Wait()
	if len(slowSender.getMessages()) != 1 {
		t.Fatalf("expected slow sender to eventually receive message")
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
