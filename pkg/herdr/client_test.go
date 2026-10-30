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

func TestReadLinesAndVisible(t *testing.T) {
	srv := herdrtest.New(t)
	client := herdr.NewClient(srv.SocketPath)

	srv.SetScreen("w1:p1", "visible", "visible line")
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
	if lastCall.Method != "terminal.read" {
		t.Fatalf("expected terminal.read call, got %s", lastCall.Method)
	}
	if term, _ := lastCall.Params["terminal"].(string); term != "w1:p1" {
		t.Errorf("expected terminal w1:p1, got %s", term)
	}
	if lines, _ := lastCall.Params["lines"].(float64); int(lines) != 100 {
		t.Errorf("expected lines 100, got %v", lastCall.Params["lines"])
	}

	vText, err := client.Read(ctx, "w1:p1", herdr.SourceVisible, 0)
	if err != nil {
		t.Fatalf("Read visible failed: %v", err)
	}
	if vText != "visible line" {
		t.Errorf("Read visible got %q, want 'visible line'", vText)
	}
}

func TestPromptCallsTerminalSend(t *testing.T) {
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
	if lastCall.Method != "terminal.send" {
		t.Fatalf("expected terminal.send call, got %s", lastCall.Method)
	}
	if lastCall.Params["terminal"] != "w1:p1" || lastCall.Params["text"] != "say hello" {
		t.Errorf("unexpected terminal.send params: %v", lastCall.Params)
	}
}

func TestErrorMapping(t *testing.T) {
	srv := herdrtest.New(t)
	client := herdr.NewClient(srv.SocketPath)

	srv.FailNext("terminal.send", "blocked", "terminal is blocked")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := client.Prompt(ctx, "w1:p1", "say hello")
	if err == nil {
		t.Fatalf("expected error from FailNext, got nil")
	}

	if !herdr.IsCode(err, "blocked") {
		t.Errorf("expected IsCode(err, blocked) = true, got err: %v", err)
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

	stream, err := client.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	srv.EmitStatusChanged("w1:p1", "working")

	select {
	case evt, ok := <-stream:
		if !ok {
			t.Fatalf("stream closed unexpectedly")
		}
		if evt.Name != "event" {
			t.Errorf("expected event notification, got %s", evt.Name)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for event on stream")
	}
}

func TestRunTerminal(t *testing.T) {
	srv := herdrtest.New(t)
	client := herdr.NewClient(srv.SocketPath)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	started, err := client.RunTerminal(ctx, herdr.RunParams{
		Argv:     []string{"claude"},
		CWD:      "/tmp/proj",
		Name:     "proj",
		Keep:     true,
		NoParent: true,
		NoNotify: true,
		Labels: map[string]string{
			"herdr.space": "sp1",
			"where":       "proj / 1",
		},
	})
	if err != nil {
		t.Fatalf("RunTerminal: %v", err)
	}
	if started.Address == "" || started.Name == nil || *started.Name != "proj" {
		t.Fatalf("RunTerminal = %+v", started)
	}
	if started.Labels["herdr.space"] != "sp1" || started.Labels["herdr.tab"] == "" {
		t.Errorf("RunTerminal labels = %v, want herdr.space=sp1 and herdr.tab populated", started.Labels)
	}
}

func TestListAgentsNormalizesHerdrCoreTerminals(t *testing.T) {
	srv := herdrtest.New(t)
	client := herdr.NewClient(srv.SocketPath)

	srv.SetTerminals([]map[string]any{
		{
			"address":   "t2",
			"state":     "live",
			"title":     "Fix login bug",
			"turn":      12,
			"status_ms": 1000,
			"status": map[string]any{
				"app":   "claude-code",
				"state": "blocked",
				"kind":  "permission",
				"msg":   "Run bash command",
			},
			"foreground": map[string]any{
				"pid":  54192,
				"name": "2.1.314",
				"argv": []string{"claude", "--resume", "uuid-999"},
				"cwd":  "/Users/gabriel/proj",
			},
			"labels": map[string]string{
				"herdr.space": "sp1",
				"herdr.tab":   "tb1",
				"where":       "my-proj / feat-auth",
			},
		},
		// Background sub-agent of t2 (no herdr.space label): counts toward t2's subagent_count.
		{
			"address": "t3",
			"state":   "live",
			"parent":  "t2",
			"status": map[string]any{
				"app":   "claude-code",
				"state": "working",
			},
		},
		// Remote terminal: ignored by local host bridge.
		{
			"address": "home/t1",
			"state":   "live",
			"status": map[string]any{
				"app":   "antigravity",
				"state": "idle",
			},
		},
		// Finished terminal: ignored.
		{
			"address": "t4",
			"state":   "finished",
			"status": map[string]any{
				"app":   "antigravity",
				"state": "done",
			},
		},
	})

	agents, err := client.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("got %d agents, want 1 (%+v)", len(agents), agents)
	}
	a := agents[0]
	if a.PaneID != "t2" || a.Agent == nil || *a.Agent != "claude" {
		t.Errorf("agent = %+v, want PaneID=t2 Agent=claude", a)
	}
	if a.WorkspaceID != "sp1" || a.WorkspaceLabel != "my-proj" || a.TabID != "tb1" || a.TabLabel != "feat-auth" {
		t.Errorf("labels = ws(%q,%q) tab(%q,%q)", a.WorkspaceID, a.WorkspaceLabel, a.TabID, a.TabLabel)
	}
	if a.StateChangeSeq != 1012 {
		t.Errorf("StateChangeSeq = %d, want 1012 (1000 + 12)", a.StateChangeSeq)
	}
	if a.SubagentCount == nil || *a.SubagentCount != 1 {
		t.Errorf("SubagentCount = %v, want 1", a.SubagentCount)
	}
	if a.StatusMsg != "Run bash command" || a.StatusKind == nil || *a.StatusKind != "permission" {
		t.Errorf("status details = kind=%v msg=%q", a.StatusKind, a.StatusMsg)
	}
	if s := a.TrustedSession(); s == nil || s.Value != "uuid-999" {
		t.Errorf("TrustedSession = %+v, want uuid-999 from --resume", s)
	}
}

func TestTrustedSession(t *testing.T) {
	agentClaude := "claude"
	agentAgy := "agy"

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
