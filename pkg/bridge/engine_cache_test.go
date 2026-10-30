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
