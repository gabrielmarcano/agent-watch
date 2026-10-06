package agents

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Record shapes as Claude Code 2.1.289 writes them (keys checked on the
// owner's transcripts, 2026-10-05; values made up).
func turnDuration(uuid string, sidechain bool) string {
	sc := "false"
	if sidechain {
		sc = "true"
	}
	return `{"type":"system","subtype":"turn_duration","durationMs":1200,"messageCount":4,"pendingBackgroundAgentCount":1,"isMeta":false,"isSidechain":` + sc + `,"uuid":"` + uuid + `","timestamp":"2026-10-05T23:45:11.000Z"}` + "\n"
}

func metaUser(text string) string {
	return `{"type":"user","isMeta":true,"message":{"role":"user","content":"` + text + `"}}` + "\n"
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// A turn that ends while background agents run: LastCompletedTurn returns
// the turn up to its turn_duration record, never the one still being
// written, and a marker that changes with every turn end.
func TestClaudeLastCompletedTurn(t *testing.T) {
	ctx := context.Background()
	path := writeFile(t, "s.jsonl", claudeTurnLines("first question", "first answer"))
	ref := SessionRef{Agent: "claude", Kind: "path", Value: path}
	c := newClaudeAdapter(Config{})

	check := func(step, wantQuery, wantResponse, wantMarker string) {
		t.Helper()
		item, marker, err := c.LastCompletedTurn(ctx, ref)
		if err != nil || marker != wantMarker {
			t.Fatalf("%s: marker %q, err %v; want %q", step, marker, err, wantMarker)
		}
		switch {
		case wantResponse == "" && item != nil:
			t.Errorf("%s: item %+v, want none", step, item)
		case wantResponse != "" && (item == nil || item.Query != wantQuery || item.Response != wantResponse):
			t.Errorf("%s: item %+v, want %q / %q", step, item, wantQuery, wantResponse)
		}
	}

	check("no turn ended yet", "", "", "")
	appendFile(t, path, turnDuration("u1", false))
	check("first turn ended", "first question", "first answer", "u1")

	// A background agent's notification starts a turn; while it is written,
	// the last completed turn stays the first one.
	appendFile(t, path, metaUser("<task-notification>agent done</task-notification>")+
		`{"type":"assistant","message":{"content":[{"type":"text","text":"half an answ"}]}}`+"\n")
	check("second turn in progress", "first question", "first answer", "u1")

	appendFile(t, path, `{"type":"assistant","message":{"content":[{"type":"text","text":"second answer"}]}}`+"\n"+turnDuration("u2", false))
	check("second turn ended (no query: a notification started it)", "", "half an answ\n\nsecond answer", "u2")

	// A sub-agent's turn end is not the main conversation's.
	appendFile(t, path, claudeTurnLines("third question", "third answer")+turnDuration("u3", true))
	check("sidechain turn end ignored", "", "half an answ\n\nsecond answer", "u2")
}

// The turn end is found beyond the first tail window; an unchanged file is
// answered from the cache (the file is replaced by garbage of the same size
// and time to prove it is not read again).
func TestClaudeLastCompletedTurn_GrowsAndCaches(t *testing.T) {
	filler := strings.Repeat(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t","name":"Read"}]}}`+"\n"+
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t","content":"x"}]}}`+"\n", 60)
	content := claudeTurnLines("q", "a") + turnDuration("u1", false) + metaUser("note") + filler
	path := writeFile(t, "long.jsonl", content)
	c := newClaudeAdapter(Config{})
	c.tailWindows = []int64{2 << 10, 64 << 10}
	ref := SessionRef{Kind: "path", Value: path}

	item, marker, err := c.LastCompletedTurn(context.Background(), ref)
	if err != nil || marker != "u1" || item == nil || item.Response != "a" || item.Query != "q" {
		t.Fatalf("got %+v, %q, %v", item, marker, err)
	}

	fi, _ := os.Stat(path)
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(fi.Size()))), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	if item, marker, err := c.LastCompletedTurn(context.Background(), ref); err != nil || marker != "u1" || item == nil || item.Response != "a" {
		t.Errorf("unchanged file: got %+v, %q, %v; want the cached turn", item, marker, err)
	}
}

// Claude Code's own user lines (isMeta): right after a turn end one starts
// a turn with no query; inside a turn (a skill's text after its tool call)
// it changes nothing.
func TestClaudeLastTurn_MetaLines(t *testing.T) {
	skill := `{"type":"user","message":{"content":"use the skill"}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"s1","name":"Skill"}]}}` + "\n" +
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"s1","content":"ok"}]}}` + "\n" +
		metaUser("Base directory for this skill: /tmp/aw-sandbox") +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"done with the skill"}]}}` + "\n"
	c := newClaudeAdapter(Config{})
	item, err := c.LastTurn(context.Background(), SessionRef{Kind: "path", Value: writeFile(t, "skill.jsonl", skill)})
	if err != nil || item.Query != "use the skill" || item.Response != "done with the skill" {
		t.Errorf("skill turn: %+v, %v", item, err)
	}

	notified := claudeTurnLines("q", "a") + turnDuration("u1", false) + metaUser("<task-notification>x</task-notification>") +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"the agent finished"}]}}` + "\n"
	item, err = c.LastTurn(context.Background(), SessionRef{Kind: "path", Value: writeFile(t, "n.jsonl", notified)})
	if err != nil || item.Query != "" || item.Response != "the agent finished" {
		t.Errorf("notification turn: %+v, %v", item, err)
	}
}
