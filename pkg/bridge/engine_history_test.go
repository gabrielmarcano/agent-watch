package bridge

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/agents"
	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// agentsViewScreen is the agents-view capture (pkg/agents testdata): what a
// screen fallback reads after ← moved the conversation to the background.
func agentsViewScreen(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "agents", "testdata", "claude", "agents-view-recent.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func claudePane(status, title string, seq uint64, session *herdr.AgentSession) herdr.AgentInfo {
	agent := "claude"
	return herdr.AgentInfo{
		PaneID: "w1:p1", WorkspaceID: "w1", TabID: "t1", Agent: &agent,
		AgentStatus: status, StateChangeSeq: seq,
		TerminalTitleStripped: &title, AgentSession: session,
	}
}

// waitHistory returns the next history item the bridge sends live, or nil
// when none arrives before the deadline. Backfilled turns are skipped: they
// read the same transcript the live capture does and are tested separately.
func waitHistory(h *testHarness, within time.Duration) *model.HistoryItem {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		select {
		case msg := <-h.relayMsgs:
			if hm, ok := msg.(model.HistoryItemMsg); ok && !hm.Backfill {
				return &hm.Item
			}
		case <-time.After(20 * time.Millisecond):
		}
	}
	return nil
}

func (h *testHarness) track(info herdr.AgentInfo) {
	h.engine.OnChanges([]herdr.Change{{Kind: herdr.Added, Agent: info}})
}

// waitBackfills collects n backfilled history items, or fewer on timeout.
func waitBackfills(h *testHarness, n int, within time.Duration) []model.HistoryItemMsg {
	deadline := time.Now().Add(within)
	var got []model.HistoryItemMsg
	for len(got) < n && time.Now().Before(deadline) {
		select {
		case msg := <-h.relayMsgs:
			if hm, ok := msg.(model.HistoryItemMsg); ok && hm.Backfill {
				got = append(got, hm)
			}
		case <-time.After(20 * time.Millisecond):
		}
	}
	return got
}

// A pane first seen with a transcript has its finished turns published as
// Backfill items, oldest first and with the transcript's times; the same
// session is never read again, a new session is.
func TestEngine_HistoryBackfill(t *testing.T) {
	h := newTestHarness(t)
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-tmp-aw-sandbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("s1.jsonl",
		`{"type":"user","timestamp":"2026-10-05T10:00:00.000Z","message":{"role":"user","content":"first question"}}`+"\n"+
			`{"type":"assistant","timestamp":"2026-10-05T10:00:05.000Z","message":{"role":"assistant","content":[{"type":"text","text":"first answer"}]}}`+"\n"+
			`{"type":"system","subtype":"turn_duration","uuid":"e1","timestamp":"2026-10-05T10:00:06.000Z"}`+"\n"+
			`{"type":"user","timestamp":"2026-10-05T10:01:00.000Z","message":{"role":"user","content":"second question"}}`+"\n"+
			`{"type":"assistant","timestamp":"2026-10-05T10:01:05.000Z","message":{"role":"assistant","content":[{"type":"text","text":"second answer"}]}}`+"\n"+
			`{"type":"system","subtype":"turn_duration","uuid":"e2","timestamp":"2026-10-05T10:01:06.000Z"}`+"\n")
	write("s2.jsonl",
		`{"type":"user","timestamp":"2026-10-05T11:00:00.000Z","message":{"role":"user","content":"other question"}}`+"\n"+
			`{"type":"assistant","timestamp":"2026-10-05T11:00:04.000Z","message":{"role":"assistant","content":[{"type":"text","text":"other answer"}]}}`+"\n"+
			`{"type":"system","subtype":"turn_duration","uuid":"e3","timestamp":"2026-10-05T11:00:05.000Z"}`+"\n")
	h.engine.Agents = agents.NewRegistry(agents.Config{Home: home})

	s1 := &herdr.AgentSession{Agent: "claude", Kind: "id", Value: "s1"}
	pane := claudePane("done", "Sandbox hello", 2, s1)
	h.track(pane)

	got := waitBackfills(h, 2, 2*time.Second)
	if len(got) != 2 {
		t.Fatalf("backfilled %d turns, want 2", len(got))
	}
	if got[0].Item.Query != "first question" || got[1].Item.Query != "second question" {
		t.Errorf("order = %q, %q; want oldest first", got[0].Item.Query, got[1].Item.Query)
	}
	first := got[0].Item
	if first.ID == "" || first.PaneID != "w1:p1" || first.Agent != "claude" || first.Label != "Sandbox hello" {
		t.Errorf("item = %+v", first)
	}
	if first.AskedAt != "2026-10-05T10:00:00Z" || first.CompletedAt != "2026-10-05T10:00:06Z" {
		t.Errorf("first times = %q / %q", first.AskedAt, first.CompletedAt)
	}
	if got[1].Item.AskedAt != "2026-10-05T10:01:00Z" || got[1].Item.CompletedAt != "2026-10-05T10:01:06Z" {
		t.Errorf("second times = %q / %q", got[1].Item.AskedAt, got[1].Item.CompletedAt)
	}

	// The same session on a later change is not read again.
	h.engine.OnChanges([]herdr.Change{{Kind: herdr.Updated, Agent: claudePane("done", "Sandbox hello", 3, s1)}})
	if again := waitBackfills(h, 1, 150*time.Millisecond); len(again) != 0 {
		t.Errorf("backfilled the same session again: %+v", again)
	}

	// A new session in the pane is.
	s2 := &herdr.AgentSession{Agent: "claude", Kind: "id", Value: "s2"}
	h.engine.OnChanges([]herdr.Change{{Kind: herdr.Updated, Agent: claudePane("done", "Sandbox hello", 4, s2)}})
	again := waitBackfills(h, 1, 2*time.Second)
	if len(again) != 1 || again[0].Item.Query != "other question" {
		t.Errorf("new session backfill = %+v", again)
	}
}

func (h *testHarness) published() string {
	h.engine.mu.RLock()
	defer h.engine.mu.RUnlock()
	if st := h.engine.states["w1:p1"]; st != nil {
		return st.lastHistID
	}
	return ""
}

// ← mid-turn: herdr reports working → done and the pane shows the agents
// view. Nothing is published: not the selector screen, by its title or by
// its screen.
func TestEngine_HistoryAgentsViewPublishesNothing(t *testing.T) {
	h := newTestHarness(t)
	h.server.SetScreen("w1:p1", "recent_unwrapped", agentsViewScreen(t))

	// By the title.
	h.track(claudePane("done", "1 awaiting input · claude agents", 2, nil))
	h.engine.captureHistory("w1:p1", "/tmp", "claude", claudePane("done", "1 awaiting input · claude agents", 2, nil))
	if got := h.published(); got != "" {
		t.Fatalf("published %q for the agents view (title)", got)
	}
	for _, c := range h.server.Calls() {
		if c.Method == "agent.read" {
			t.Fatalf("read the screen of a pane showing the agents view")
		}
	}

	// By the screen, when the title still names the conversation.
	h.engine.captureHistory("w1:p1", "/tmp", "claude", claudePane("done", "Sandbox hello", 2, nil))
	if got := h.published(); got != "" {
		t.Fatalf("published %q for the agents view (screen)", got)
	}
}

// Back from the agents view to a conversation (title change, status done):
// the pane's last reply is captured, since its turn may have finished while
// the conversation was in the background.
func TestEngine_HistoryBackToConversation(t *testing.T) {
	h := newTestHarness(t)
	h.server.SetScreen("w1:p1", "recent_unwrapped", "❯ say hi\n\n⏺ hi there\n\n✻ Worked for 1s · done 10:25 AM\n\n────\n❯ \n────\n")
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.3", Protocol: 22})

	inView := claudePane("done", "claude agents", 5, nil)
	h.track(inView)
	back := claudePane("done", "Sandbox hello", 5, nil)
	h.engine.OnChanges([]herdr.Change{{Kind: herdr.Updated, Agent: back, Prev: &inView}})

	item := waitHistory(h, 3*time.Second)
	if item == nil || item.Response != "hi there" || item.Query != "say hi" {
		t.Fatalf("history after returning to the conversation = %+v", item)
	}

	// A title change between two conversations is not a trigger.
	other := claudePane("done", "Another title", 5, nil)
	h.engine.OnChanges([]herdr.Change{{Kind: herdr.Updated, Agent: other, Prev: &back}})
	if item := waitHistory(h, 200*time.Millisecond); item != nil {
		t.Fatalf("published %+v on a plain title change", item)
	}
}

// A screen capture that fails (herdr slower than the first budget) is
// retried once with a longer budget instead of publishing nothing.
func TestEngine_HistoryScreenReadRetried(t *testing.T) {
	h := newTestHarness(t)
	h.engine.ScreenReadTimeout = 100 * time.Millisecond
	h.engine.ScreenRetryTimeout = 2 * time.Second
	h.server.SetScreen("w1:p1", "recent_unwrapped", "❯ say hi\n\n⏺ hi again\n\n✻ Worked for 1s · done 10:25 AM\n\n────\n❯ \n────\n")
	hold := h.server.HoldNext("agent.read") // never released: the first read times out
	defer hold.Release()

	info := claudePane("done", "Sandbox hello", 3, nil)
	h.track(info)
	h.engine.captureHistory("w1:p1", "/tmp", "claude", info)
	if h.published() == "" {
		t.Fatal("nothing published after a failed first screen read")
	}
	reads := 0
	for _, c := range h.server.Calls() {
		if c.Method == "agent.read" {
			reads++
		}
	}
	if reads != 2 {
		t.Errorf("agent.read calls = %d, want 2", reads)
	}
}

// A Claude turn waiting on AskUserQuestion has no reply: nothing is
// published, and the dialog's screen is not captured as one.
func TestEngine_HistoryPendingQuestionPublishesNothing(t *testing.T) {
	h := newTestHarness(t)
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-tmp-aw-sandbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := `{"type":"user","message":{"content":"pick a color"}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"AskUserQuestion"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	h.engine.Agents = agents.NewRegistry(agents.Config{Home: home})
	h.server.SetScreen("w1:p1", "recent_unwrapped", "☐ Color\n\nWhich color?\n\n❯ 1. red\n  2. blue\n")

	info := claudePane("done", "", 4, &herdr.AgentSession{Agent: "claude", Kind: "id", Value: "s1"})
	h.track(info)
	h.engine.captureHistory("w1:p1", "/tmp/aw-sandbox", "claude", info)
	if got := h.published(); got != "" {
		t.Fatalf("published %q for a turn waiting on a question", got)
	}
	for _, c := range h.server.Calls() {
		if c.Method == "agent.read" {
			t.Fatal("captured the screen of a turn waiting on a question")
		}
	}
}
