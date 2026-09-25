package herdr_test

import (
	"context"
	"errors"
	"regexp"
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

	for i := 0; i < 2; i++ {
		if _, err := client.Ping(ctx); err != nil {
			t.Fatalf("Ping failed: %v", err)
		}
	}

	calls := srv.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	// A counter plus a random per-process prefix (not a clock reading).
	idRE := regexp.MustCompile(`^([0-9a-f]{16})-([0-9]+)$`)
	var prefixes, counters []string
	for _, c := range calls {
		m := idRE.FindStringSubmatch(c.ID)
		if m == nil {
			t.Fatalf("request id %q does not match <16 hex>-<counter>", c.ID)
		}
		prefixes = append(prefixes, m[1])
		counters = append(counters, m[2])
	}
	if prefixes[0] != prefixes[1] {
		t.Errorf("id prefix changed between calls (%q, %q); want one random prefix per process", prefixes[0], prefixes[1])
	}
	if counters[0] == counters[1] {
		t.Errorf("two calls reused counter %s", counters[0])
	}
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

// ReadANSI asks herdr for the styled screen, as `herdr agent read --format
// ansi` does (format "ansi", strip_ansi false); Read keeps asking for text.
func TestReadANSIAsksForANSIFormat(t *testing.T) {
	srv := herdrtest.New(t)
	client := herdr.NewClient(srv.SocketPath)

	const styled = "\x1b[0m\x1b[38;2;245;167;66m\x1b[48;2;20;20;20m┃\x1b[0m Allow once\r\n"
	srv.SetScreen("w1:p1", "visible", "┃ Allow once")
	srv.SetANSIScreen("w1:p1", "visible", styled)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := client.ReadANSI(ctx, "w1:p1", herdr.SourceVisible, 0)
	if err != nil {
		t.Fatalf("ReadANSI failed: %v", err)
	}
	if got != styled {
		t.Errorf("ReadANSI got %q, want %q", got, styled)
	}
	calls := srv.Calls()
	p := calls[len(calls)-1].Params
	if p["format"] != "ansi" || p["strip_ansi"] != false || p["source"] != "visible" || p["target"] != "w1:p1" {
		t.Errorf("ReadANSI params = %v, want target w1:p1, source visible, format ansi, strip_ansi false", p)
	}
	if _, ok := p["lines"]; ok {
		t.Errorf("ReadANSI sent lines=%v for lines=0", p["lines"])
	}

	text, err := client.Read(ctx, "w1:p1", herdr.SourceVisible, 0)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if text != "┃ Allow once" {
		t.Errorf("Read got %q, want the text screen", text)
	}
	calls = srv.Calls()
	if f := calls[len(calls)-1].Params["format"]; f != "text" {
		t.Errorf("Read sent format %v, want text", f)
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
