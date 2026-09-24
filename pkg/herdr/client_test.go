package herdr_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/herdrtest"
)

func TestCallStringID(t *testing.T) {
	srv := herdrtest.New(t)
	client := herdr.NewClient(srv.SocketPath)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := client.Ping(ctx)
	if err != nil {
		t.Fatalf("Ping failed: %v", err)
	}

	calls := srv.Calls()
	if len(calls) == 0 {
		t.Fatalf("expected at least 1 call, got 0")
	}
	// The fake server rejects numeric or missing IDs with an error;
	// since Ping succeeded, the ID was a valid string.
}

func TestReadUsesUnderscoreSource(t *testing.T) {
	srv := herdrtest.New(t)
	client := herdr.NewClient(srv.SocketPath)

	srv.SetScreen("w1:p1", "recent_unwrapped", "screen content line 1\nline 2")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	text, err := client.Read(ctx, "w1:p1", herdr.SourceRecentUnwrapped, 100)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if text != "screen content line 1\nline 2" {
		t.Errorf("Read got %q, want expected content", text)
	}

	calls := srv.Calls()
	lastCall := calls[len(calls)-1]
	if lastCall.Method != "agent.read" {
		t.Fatalf("expected agent.read call, got %s", lastCall.Method)
	}
	if source, _ := lastCall.Params["source"].(string); source != "recent_unwrapped" {
		t.Errorf("expected source recent_unwrapped, got %s", source)
	}
}

func TestPromptOmitsWait(t *testing.T) {
	srv := herdrtest.New(t)
	client := herdr.NewClient(srv.SocketPath)

	srv.SetAgents([]map[string]any{
		{"pane_id": "w1:p1", "agent_status": "idle"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := client.Prompt(ctx, "w1:p1", "say hello")
	if err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	calls := srv.Calls()
	lastCall := calls[len(calls)-1]
	if lastCall.Method != "agent.prompt" {
		t.Fatalf("expected agent.prompt call, got %s", lastCall.Method)
	}
	if val, ok := lastCall.Params["wait"]; ok && val != nil {
		t.Errorf("expected wait to be omitted or nil, got: %v", val)
	}
}

func TestErrorMapping(t *testing.T) {
	srv := herdrtest.New(t)
	client := herdr.NewClient(srv.SocketPath)

	srv.FailNext("agent.prompt", "agent_blocked", "agent is blocked")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := client.Prompt(ctx, "w1:p1", "say hello")
	if err == nil {
		t.Fatalf("expected error from FailNext, got nil")
	}

	if !herdr.IsCode(err, "agent_blocked") {
		t.Errorf("expected IsCode(err, agent_blocked) = true, got err: %v", err)
	}
}

func TestUnavailable(t *testing.T) {
	srv := herdrtest.New(t)
	client := herdr.NewClient(srv.SocketPath)

	srv.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := client.ListAgents(ctx)
	if err == nil {
		t.Fatalf("expected error when server stopped, got nil")
	}

	if !errors.Is(err, herdr.ErrUnavailable) {
		t.Errorf("expected errors.Is(err, ErrUnavailable), got: %v", err)
	}
}

func TestSubscribeAck(t *testing.T) {
	srv := herdrtest.New(t)
	client := herdr.NewClient(srv.SocketPath)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	subs := []herdr.Subscription{
		{Type: "pane.agent_status_changed", PaneID: "w1:p1"},
	}

	stream, err := client.Subscribe(ctx, subs)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	srv.EmitStatusChanged("w1:p1", "working")

	select {
	case evt, ok := <-stream:
		if !ok {
			t.Fatalf("stream closed unexpectedly")
		}
		if evt.Name != "pane_agent_status_changed" {
			t.Errorf("expected event pane_agent_status_changed, got %s", evt.Name)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for event on stream")
	}
}

func TestTrustedSession(t *testing.T) {
	agentClaude := "claude"
	agentAgy := "agy"

	// Matching session
	infoMatching := herdr.AgentInfo{
		PaneID: "w1:p1",
		Agent:  &agentClaude,
		AgentSession: &herdr.AgentSession{
			Agent: "claude",
			Kind:  "id",
			Value: "uuid-123",
		},
	}
	if s := infoMatching.TrustedSession(); s == nil || s.Value != "uuid-123" {
		t.Errorf("expected trusted session with uuid-123, got: %v", s)
	}

	// Mismatched session (stale harness ref)
	infoStale := herdr.AgentInfo{
		PaneID: "w1:p1",
		Agent:  &agentAgy,
		AgentSession: &herdr.AgentSession{
			Agent: "claude",
			Kind:  "id",
			Value: "uuid-123",
		},
	}
	if s := infoStale.TrustedSession(); s != nil {
		t.Errorf("expected nil for mismatched session, got: %v", s)
	}

	// Nil Agent or Nil AgentSession
	infoNilAgent := herdr.AgentInfo{
		PaneID: "w1:p1",
		Agent:  nil,
		AgentSession: &herdr.AgentSession{
			Agent: "claude",
			Value: "uuid-123",
		},
	}
	if s := infoNilAgent.TrustedSession(); s != nil {
		t.Errorf("expected nil when Agent is nil, got: %v", s)
	}
}
