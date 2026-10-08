package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/agents"
	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
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

// background_agents follows the transcript while herdr keeps the pane
// working: the count of the last turn end while Claude waits, 0 while a newer
// turn is generated, kept across herdr updates that stay working, and
// dropped when the pane leaves working. A check that changes nothing sends
// nothing.
func TestEngine_BackgroundAgents(t *testing.T) {
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
	end := func(uuid string, n int) string {
		pending := ""
		if n > 0 {
			pending = `"pendingBackgroundAgentCount":` + strconv.Itoa(n) + `,`
		}
		return `{"type":"system","subtype":"turn_duration",` + pending + `"uuid":"` + uuid + `"}` + "\n"
	}
	h.engine.Agents = agents.NewRegistry(agents.Config{Home: home})
	session := &herdr.AgentSession{Agent: "claude", Kind: "id", Value: "s1"}
	cwd := "/tmp/aw-sandbox"
	pane := func(status string, seq uint64, focused bool) herdr.AgentInfo {
		p := claudePane(status, "", seq, session)
		p.CWD = &cwd
		p.Focused = focused
		return p
	}
	ctx := context.Background()
	isUpdate := func(msg any) bool { _, ok := msg.(model.AgentUpdateMsg); return ok }
	nextCount := func(step string) int {
		t.Helper()
		msg, _ := waitMsg(h, 3*time.Second, isUpdate)
		if msg == nil {
			t.Fatalf("%s: no agent_update", step)
		}
		return msg.(model.AgentUpdateMsg).Agent.BackgroundAgents
	}
	noUpdate := func(step string) {
		t.Helper()
		if msg, _ := waitMsg(h, 200*time.Millisecond, isUpdate); msg != nil {
			t.Fatalf("%s: unexpected %+v", step, msg)
		}
	}

	working := pane("working", 7, false)
	h.track(working)
	if n := nextCount("tracked"); n != 0 {
		t.Fatalf("tracked: background %d", n)
	}

	write(`{"type":"user","message":{"content":"launch two agents"}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"launched"}]}}` + "\n" + end("u1", 2))
	h.engine.checkWorkingTurns(ctx)
	if n := nextCount("turn ended with 2 running"); n != 2 {
		t.Fatalf("turn ended with 2 running: background %d", n)
	}
	h.engine.checkWorkingTurns(ctx)
	noUpdate("unchanged transcript")

	// A herdr update that stays working keeps the count.
	focused := pane("working", 7, true)
	h.engine.OnChanges([]herdr.Change{{Kind: herdr.Updated, Agent: focused, Prev: &working}})
	if n := nextCount("focus change"); n != 2 {
		t.Fatalf("focus change: background %d, want 2", n)
	}

	// An agent's report starts a turn: Claude is generating again.
	write(`{"type":"user","isMeta":true,"message":{"content":"<task-notification>one finished</task-notification>"}}` + "\n")
	h.engine.checkWorkingTurns(ctx)
	if n := nextCount("report turn"); n != 0 {
		t.Fatalf("report turn: background %d, want 0", n)
	}
	write(`{"type":"assistant","message":{"content":[{"type":"text","text":"one left"}]}}` + "\n" + end("u2", 1))
	h.engine.checkWorkingTurns(ctx)
	if n := nextCount("report turn ended"); n != 1 {
		t.Fatalf("report turn ended: background %d, want 1", n)
	}

	// Leaving working drops it.
	done := pane("done", 8, true)
	h.engine.OnChanges([]herdr.Change{{Kind: herdr.Updated, Agent: done, Prev: &focused}})
	if n := nextCount("done"); n != 0 {
		t.Fatalf("done: background %d, want 0", n)
	}
	h.engine.checkWorkingTurns(ctx)
	noUpdate("not working")
}

// background_shells and background_monitors follow the transcript with any
// status: the turn watch reads them while the pane is working, each working
// → done transition reads them again, and herdr updates in between keep
// them.
func TestEngine_BackgroundTasks(t *testing.T) {
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
	h.engine.Agents = agents.NewRegistry(agents.Config{Home: home})
	session := &herdr.AgentSession{Agent: "claude", Kind: "id", Value: "s1"}
	cwd := "/tmp/aw-sandbox"
	pane := func(status string, seq uint64) herdr.AgentInfo {
		p := claudePane(status, "", seq, session)
		p.CWD = &cwd
		return p
	}
	ctx := context.Background()
	update := func(step string, status model.AgentStatus, shells, monitors int) {
		t.Helper()
		msg, _ := waitMsg(h, 3*time.Second, func(msg any) bool {
			u, ok := msg.(model.AgentUpdateMsg)
			return ok && u.Agent.Status == status && u.Agent.BackgroundShells == shells && u.Agent.BackgroundMonitors == monitors
		})
		if msg == nil {
			t.Fatalf("%s: no %s update with %d shells and %d monitors", step, status, shells, monitors)
		}
	}

	working := pane("working", 7)
	h.track(working)
	write(`{"type":"user","message":{"content":"run the tests in the background"}}` + "\n" +
		`{"type":"user","timestamp":"2026-10-07T20:00:00.000Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]},"toolUseResult":{"backgroundTaskId":"b1"}}` + "\n" +
		`{"type":"user","timestamp":"2026-10-07T20:00:01.000Z","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"ok"}]},"toolUseResult":{"taskId":"m1","timeoutMs":0,"persistent":true}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"started"}]}}` + "\n" +
		`{"type":"system","subtype":"turn_duration","uuid":"u1"}` + "\n")
	h.engine.checkWorkingTurns(ctx)
	update("turn watch", model.StatusWorking, 1, 1)

	// herdr reports done once the turn ends: the counts stay.
	done := pane("done", 8)
	h.engine.OnChanges([]herdr.Change{{Kind: herdr.Updated, Agent: done, Prev: &working}})
	update("done", model.StatusDone, 1, 1)

	// The shell's report starts a turn; when it ends, the transition reads
	// the counts again.
	write(`{"type":"user","message":{"content":"<task-notification>\n<task-id>b1</task-id>\n<status>completed</status>\n<summary>Background command \"tests\" completed (exit code 0)</summary>\n</task-notification>"}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"tests pass"}]}}` + "\n" +
		`{"type":"system","subtype":"turn_duration","uuid":"u2"}` + "\n")
	working2 := pane("working", 9)
	h.engine.OnChanges([]herdr.Change{{Kind: herdr.Updated, Agent: working2, Prev: &done}})
	update("report turn", model.StatusWorking, 1, 1)
	done2 := pane("done", 10)
	h.engine.OnChanges([]herdr.Change{{Kind: herdr.Updated, Agent: done2, Prev: &working2}})
	update("report turn ended", model.StatusDone, 0, 1)
}
