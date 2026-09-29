package bridge

import (
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/agents"
	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// logSink is a goroutine-safe slog destination for tests.
type logSink struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *logSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func sinkLogger(s *logSink, level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(s, &slog.HandlerOptions{Level: level}))
}

// A failed transcript read is worth a warning: the history silently degrades
// to a screen capture otherwise.
func TestEngine_LogsLastTurnFailure(t *testing.T) {
	h := newTestHarness(t)
	var logs logSink
	h.engine.Logger = sinkLogger(&logs, slog.LevelInfo)
	h.engine.Agents = agents.NewRegistry(agents.Config{ClaudeConfigDirs: []string{t.TempDir()}})
	h.server.SetScreen("w1:p1", "recent_unwrapped", "user: run it\nassistant: done.")
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})

	claude := "claude"
	cwd := t.TempDir()
	working := herdr.AgentInfo{
		PaneID: "w1:p1", WorkspaceID: "w1", TabID: "t1", Agent: &claude, CWD: &cwd,
		AgentStatus: "working", StateChangeSeq: 1,
		AgentSession: &herdr.AgentSession{Agent: "claude", Kind: "id", Source: "hook", Value: "no-such-session"},
	}
	done := working
	done.AgentStatus = "done"
	done.StateChangeSeq = 2
	h.engine.OnChanges([]herdr.Change{{Kind: herdr.Added, Agent: working}})
	h.engine.OnChanges([]herdr.Change{{Kind: herdr.Updated, Agent: done, Prev: &working}})

	if msg, _ := waitMsg(h, 3*time.Second, func(m any) bool { _, ok := m.(model.HistoryItemMsg); return ok }); msg == nil {
		t.Fatal("no history item from the screen fallback")
	}
	out := logs.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "LastTurn") || !strings.Contains(out, "w1:p1") {
		t.Errorf("no warning for the failed transcript read:\n%s", out)
	}
	if strings.Contains(out, "assistant: done") {
		t.Errorf("log contains transcript/screen content:\n%s", out)
	}
}

// A workspace without a label must not make every change batch call
// workspace.list again; and the workspace map is Debug, not Info, noise.
func TestEngine_WorkspaceListIsCached(t *testing.T) {
	h := newTestHarness(t)
	var logs logSink
	h.engine.Logger = sinkLogger(&logs, slog.LevelInfo)
	h.server.SetWorkspaces([]map[string]any{
		{"workspace_id": "w1", "number": 1, "label": ""},
		{"workspace_id": "w2", "number": 2, "label": "api"},
	})

	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})
	pollUntil(t, 3*time.Second, "first workspace refresh", func() bool {
		h.engine.mu.RLock()
		defer h.engine.mu.RUnlock()
		_, known := h.engine.workspaces["w1"]
		return known && !h.engine.wsRefreshing
	})
	h.engine.mu.RLock()
	started := h.engine.wsRefreshes
	h.engine.mu.RUnlock()

	for seq := uint64(1); seq <= 3; seq++ {
		addAgent(h.engine, "w1:p1", "working", seq)
	}

	h.engine.mu.RLock()
	after := h.engine.wsRefreshes
	h.engine.mu.RUnlock()
	if after != started {
		t.Errorf("change batches on an unlabeled workspace started %d extra workspace refreshes, want 0", after-started)
	}
	if n := callsSince(h, 0, "workspace.list"); n != 1 {
		t.Errorf("workspace.list called %d times, want 1", n)
	}
	if strings.Contains(logs.String(), "refreshed workspaces") {
		t.Errorf("workspace map logged at Info:\n%s", logs.String())
	}

	// A workspace the engine has never seen still triggers a refresh, once
	// the short retry interval has passed.
	h.engine.mu.Lock()
	h.engine.wsRefreshedAt = time.Now().Add(-time.Minute)
	h.engine.mu.Unlock()
	agent := "claude"
	h.engine.OnChanges([]herdr.Change{{Kind: herdr.Added, Agent: herdr.AgentInfo{
		PaneID: "w3:p1", WorkspaceID: "w3", TabID: "t1", Agent: &agent, AgentStatus: "idle", StateChangeSeq: 1,
	}}})
	h.engine.mu.RLock()
	final := h.engine.wsRefreshes
	h.engine.mu.RUnlock()
	if final != started+1 {
		t.Errorf("unknown workspace started %d refreshes, want 1", final-started)
	}
}
