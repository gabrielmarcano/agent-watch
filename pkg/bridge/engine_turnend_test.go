package bridge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/agents"
	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
)

// A Claude pane with background agents stays working while its turns end
// (herdr 0.9.1 never reports done). The turn watch publishes each ended
// turn once, never a turn still being written, and nothing for panes that
// are not working.
func TestEngine_TurnEndWhileWorking(t *testing.T) {
	h := newTestHarness(t)
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-tmp-aw-sandbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s1.jsonl")
	write := func(s string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(s); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	turn := func(q, a, uuid string) string {
		return `{"type":"user","message":{"content":"` + q + `"}}` + "\n" +
			`{"type":"assistant","message":{"content":[{"type":"text","text":"` + a + `"}]}}` + "\n" +
			`{"type":"system","subtype":"turn_duration","pendingBackgroundAgentCount":1,"uuid":"` + uuid + `"}` + "\n"
	}
	h.engine.Agents = agents.NewRegistry(agents.Config{Home: home})
	session := &herdr.AgentSession{Agent: "claude", Kind: "id", Value: "s1"}
	pane := claudePane("working", "", 7, session)
	cwd := "/tmp/aw-sandbox"
	pane.CWD = &cwd
	h.track(pane)
	ctx := context.Background()

	write(turn("first", "first answer", "u1"))
	h.engine.checkWorkingTurns(ctx)
	if item := waitHistory(h, 3*time.Second); item == nil || item.Response != "first answer" || item.Source != "transcript" {
		t.Fatalf("first ended turn: %+v", item)
	}

	// Nothing new: nothing sent.
	h.engine.checkWorkingTurns(ctx)
	// A turn still being written: nothing sent.
	write(`{"type":"user","message":{"content":"second"}}` + "\n" + `{"type":"assistant","message":{"content":[{"type":"text","text":"partial"}]}}` + "\n")
	h.engine.checkWorkingTurns(ctx)
	if item := waitHistory(h, 200*time.Millisecond); item != nil {
		t.Fatalf("published %+v without a new turn end", item)
	}

	write(`{"type":"system","subtype":"turn_duration","uuid":"u2"}` + "\n")
	h.engine.checkWorkingTurns(ctx)
	if item := waitHistory(h, 3*time.Second); item == nil || item.Query != "second" || item.Response != "partial" {
		t.Fatalf("second ended turn: %+v", item)
	}

	// A pane that is not working is left to the status transitions.
	write(turn("third", "third answer", "u3"))
	done := claudePane("done", "", 8, session)
	done.CWD = &cwd
	h.engine.OnChanges([]herdr.Change{{Kind: herdr.Updated, Agent: done, Prev: &pane}})
	if item := waitHistory(h, 3*time.Second); item == nil || item.Response != "third answer" {
		t.Fatalf("working -> done capture: %+v", item)
	}
	write(turn("fourth", "fourth answer", "u4"))
	h.engine.checkWorkingTurns(ctx)
	if item := waitHistory(h, 200*time.Millisecond); item != nil {
		t.Fatalf("turn watch published %+v for a pane that is not working", item)
	}
}
