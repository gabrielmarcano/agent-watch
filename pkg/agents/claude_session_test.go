package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claudeProfile is a temp Claude config dir with transcripts and a session
// index, shaped like the ones captured in aw-sandbox on 2026-10-05.
type claudeProfile struct {
	t    *testing.T
	home string
	dir  string
}

func newClaudeProfile(t *testing.T, home, name string) *claudeProfile {
	t.Helper()
	dir := filepath.Join(home, name)
	for _, d := range []string{filepath.Join(dir, "projects", "-private-tmp-aw-sandbox"), filepath.Join(dir, "sessions")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &claudeProfile{t: t, home: home, dir: dir}
}

func claudeTurnLines(q, a string) string {
	return `{"type":"user","message":{"role":"user","content":"` + q + `"}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + a + `"}]}}` + "\n"
}

func claudeTitleLines(id, title string) string {
	return `{"type":"ai-title","aiTitle":"` + title + `","sessionId":"` + id + `"}` + "\n" +
		`{"type":"agent-name","agentName":"` + title + `","sessionId":"` + id + `"}` + "\n"
}

func claudeContinuedIn(from, to string) string {
	return `{"type":"continued-in","timestamp":"2026-10-05T23:27:54.178Z","sessionId":"` + from + `","continuedInSessionId":"` + to + `"}` + "\n"
}

func (p *claudeProfile) transcript(id, content string) string {
	p.t.Helper()
	path := filepath.Join(p.dir, "projects", "-private-tmp-aw-sandbox", id+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		p.t.Fatal(err)
	}
	return path
}

// index writes a session index entry: the captured fixture with the session
// id and cwd replaced.
func (p *claudeProfile) index(file, fixture, id, cwd string) {
	p.t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "claude", fixture))
	if err != nil {
		p.t.Fatal(err)
	}
	s := strings.ReplaceAll(string(b), "3ea11ecc-bd40-4efd-8ed7-2a3ba7b8fe1f", id)
	s = strings.ReplaceAll(s, "9a81252a-b10b-45fe-86ed-cffaac8feb6c", id)
	s = strings.ReplaceAll(s, "/private/tmp/aw-sandbox", cwd)
	if err := os.WriteFile(filepath.Join(p.dir, "sessions", file), []byte(s), 0o644); err != nil {
		p.t.Fatal(err)
	}
}

// The agents-view case reproduced in aw-sandbox: herdr keeps naming the
// pane's first session (A, continued in B when it went to the background)
// while the pane shows another background session (C). The pane's title
// tells them apart, and Claude's session index finds C.
func TestClaudeSessionPath_AgentsView(t *testing.T) {
	const (
		idA, idB, idC = "9a81252a-b10b-45fe-86ed-cffaac8feb6c", "c822ab43-b5fa-43d4-81e8-538c1d9d052c", "3ea11ecc-bd40-4efd-8ed7-2a3ba7b8fe1f"
		spare         = "67648817-0000-4000-8000-000000000000"
		cwd           = "/private/tmp/aw-sandbox"
	)
	home := t.TempDir()
	p := newClaudeProfile(t, home, ".claude-work")
	p.transcript(idA, claudeTurnLines("hello", "hello reply")+claudeTitleLines(idA, "Sandbox hello")+claudeContinuedIn(idA, idB))
	p.transcript(idB, claudeTurnLines("hello", "hello reply")+claudeTitleLines(idB, "Sandbox hello")+claudeTurnLines("second turn", "second reply"))
	p.transcript(idC, claudeTitleLines(idC, "sandbox session initialization")+claudeTurnLines("third", "reply of the session on screen"))
	p.index("77531.json", "session-index-interactive.json", idA, cwd) // stale: still names A
	p.index("78682.json", "session-index-bg.json", idB, cwd)
	p.index("24384.json", "session-index-bg.json", idC, cwd)
	p.index("78666.json", "session-index-bg.json", spare, cwd) // a spare: no transcript yet
	// Key files sit next to the entries; they are never read, even if they parse.
	if err := os.WriteFile(filepath.Join(p.dir, "sessions", "24384.abc.key"), []byte(`{"sessionId":"`+idA+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	c := newClaudeAdapter(Config{Home: home})
	ref := func(id, title string) SessionRef {
		return SessionRef{Agent: "claude", Kind: "id", Value: id, CWD: cwd, Title: title}
	}
	cases := []struct {
		name, id, title string
		want            string // response, "" for ErrNoTranscript
	}{
		{"pane shows another session", idA, "sandbox session initialization", "reply of the session on screen"},
		{"pane shows herdr's (continued) session", idA, "Sandbox hello", "second reply"},
		{"no title: herdr's session", idA, "", "second reply"},
		{"a title no session has: nothing contradicts herdr", idA, "Some terminal title", "second reply"},
		{"herdr names a spare without transcript", spare, "sandbox session initialization", "reply of the session on screen"},
		{"untitled conversation on screen, herdr's has a title", idA, claudeUntitled, ""},
		{"herdr's session is in no profile", "0000aaaa-0000-4000-8000-000000000000", "sandbox session initialization", ""},
	}
	for _, tc := range cases {
		item, err := c.LastTurn(context.Background(), ref(tc.id, tc.title))
		switch {
		case tc.want == "":
			if !errors.Is(err, ErrNoTranscript) {
				t.Errorf("%s: got %+v, %v; want ErrNoTranscript", tc.name, item, err)
			}
		case err != nil || item.Response != tc.want:
			t.Errorf("%s: got %+v, %v; want %q", tc.name, item, err, tc.want)
		}
	}

	// Two sessions with the pane's title: the one in the pane's cwd, else none.
	const idD = "dddddddd-0000-4000-8000-000000000000"
	p.transcript(idD, claudeTitleLines(idD, "sandbox session initialization")+claudeTurnLines("q", "reply elsewhere"))
	p.index("90001.json", "session-index-bg.json", idD, "/somewhere/else")
	item, err := c.LastTurn(context.Background(), ref(idA, "sandbox session initialization"))
	if err != nil || item.Response != "reply of the session on screen" {
		t.Errorf("same title, other cwd: got %+v, %v", item, err)
	}
	r := ref(idA, "sandbox session initialization")
	r.CWD = "/a/third/dir"
	if item, err := c.LastTurn(context.Background(), r); !errors.Is(err, ErrNoTranscript) {
		t.Errorf("ambiguous title: got %+v, %v; want ErrNoTranscript", item, err)
	}
}

// Only the profile that holds herdr's session is consulted: a session with
// the pane's title in another profile is never picked.
func TestClaudeSessionPath_OnlyHerdrsProfile(t *testing.T) {
	const idA, idE = "aaaaaaaa-0000-4000-8000-000000000000", "eeeeeeee-0000-4000-8000-000000000000"
	home := t.TempDir()
	mine := newClaudeProfile(t, home, ".claude")
	other := newClaudeProfile(t, home, ".claude-other")
	mine.transcript(idA, claudeTitleLines(idA, "first")+claudeTurnLines("q", "herdr's reply"))
	mine.index("1.json", "session-index-interactive.json", idA, "/private/tmp/aw-sandbox")
	other.transcript(idE, claudeTitleLines(idE, "shown")+claudeTurnLines("q", "other profile's reply"))
	other.index("2.json", "session-index-bg.json", idE, "/private/tmp/aw-sandbox")

	c := newClaudeAdapter(Config{Home: home})
	item, err := c.LastTurn(context.Background(), SessionRef{Agent: "claude", Kind: "id", Value: idA, CWD: "/private/tmp/aw-sandbox", Title: "shown"})
	if err != nil || item.Response != "herdr's reply" {
		t.Errorf("got %+v, %v; want herdr's reply (the other profile is not searched)", item, err)
	}
}

// The titles are found even when a long turn puts them beyond the first
// tail window.
func TestClaudeTranscriptTitles_GrowsTheRead(t *testing.T) {
	const id = "aaaaaaaa-0000-4000-8000-000000000000"
	p := newClaudeProfile(t, t.TempDir(), ".claude")
	filler := strings.Repeat(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t","name":"Read"}]}}`+"\n", 200)
	path := p.transcript(id, claudeTitleLines(id, "long turn")+filler)
	c := newClaudeAdapter(Config{})
	c.tailWindows = []int64{4 << 10, 64 << 10}
	got, err := c.transcriptTitles(context.Background(), path)
	if err != nil || !got.has("long turn") {
		t.Errorf("got %+v, %v", got, err)
	}
	c.tailWindows = []int64{4 << 10}
	if got, _ := c.transcriptTitles(context.Background(), path); got.any() {
		t.Errorf("one small window: got %+v, want none", got)
	}
}

// A turn that ends in a tool call nobody answered yet (AskUserQuestion, a
// permission) has no reply: ErrNoReply, so the bridge does not publish the
// dialog's screen as one. A tool call with its result and no text after it is
// still ErrNoTranscript (screen fallback).
func TestClaudeLastTurn_PendingToolCall(t *testing.T) {
	ask := `{"type":"assistant","message":{"content":[{"type":"text","text":"Let me ask."},{"type":"tool_use","id":"toolu_1","name":"AskUserQuestion"}]}}` + "\n"
	answer := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"red"}]}}` + "\n"
	final := `{"type":"assistant","message":{"content":[{"type":"text","text":"Red it is."}]}}` + "\n"
	cases := []struct {
		name, transcript string
		wantErr          error
		wantResponse     string
	}{
		{"question open", claudeTurnLines("pick a color", "") + ask, ErrNoReply, ""},
		{"question in its own line", `{"type":"user","message":{"content":"pick"}}` + "\n" + ask, ErrNoReply, ""},
		{"answered, then the reply", `{"type":"user","message":{"content":"pick"}}` + "\n" + ask + answer + final, nil, "Red it is."},
		{"answered, no text yet", `{"type":"user","message":{"content":"pick"}}` + "\n" + ask + answer, ErrNoTranscript, ""},
		{"an older call open, text after it", `{"type":"user","message":{"content":"pick"}}` + "\n" + ask + final, nil, "Red it is."},
	}
	c := newClaudeAdapter(Config{})
	for _, tc := range cases {
		item, err := c.LastTurn(context.Background(), SessionRef{Kind: "path", Value: writeFile(t, "t.jsonl", tc.transcript)})
		if tc.wantErr != nil {
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("%s: got %+v, %v; want %v", tc.name, item, err, tc.wantErr)
			}
			if tc.wantErr == ErrNoReply && !strings.Contains(err.Error(), "AskUserQuestion") {
				t.Errorf("%s: error %q does not name the tool", tc.name, err)
			}
			continue
		}
		if err != nil || item.Response != tc.wantResponse {
			t.Errorf("%s: got %+v, %v; want %q", tc.name, item, err, tc.wantResponse)
		}
	}
}

// The agents view is not a conversation: by its title, and by its screen
// (captured in aw-sandbox), so it is never published as a reply. Every other
// Claude screen in testdata is a conversation.
func TestClaudeViewDetector(t *testing.T) {
	var c ViewDetector = newClaudeAdapter(Config{})
	for title, want := range map[string]bool{
		"claude agents":                     false,
		"1 awaiting input · claude agents":  false,
		"Sandbox hello":                     true,
		"Claude Code":                       true,
		"":                                  true,
		"notes about the claude agents tab": true,
	} {
		if got := c.ShowsConversation(title); got != want {
			t.Errorf("ShowsConversation(%q) = %v, want %v", title, got, want)
		}
	}

	files, err := filepath.Glob(filepath.Join("testdata", "claude", "*.txt"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no claude screens: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		want := !strings.HasPrefix(filepath.Base(f), "agents-view")
		if got := c.ConversationScreen(string(b)); got != want {
			t.Errorf("%s: ConversationScreen = %v, want %v", f, got, want)
		}
		if item := ScreenTurnFor(c.(Adapter), string(b)); (item != nil) != want {
			t.Errorf("%s: ScreenTurnFor = %+v, want an item: %v", f, item, want)
		}
	}

	// A reply that quotes the agents view's hints above the input box is
	// still a conversation.
	quoted := "⏺ The footer says: enter to return · space to reply · ctrl+x to delete\n" +
		"  and the box says ❯ describe a task for a new session\n\n✻ Worked for 2s\n\n" +
		"────\n❯ \n────\n  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents\n"
	if !c.ConversationScreen(quoted) {
		t.Error("a reply quoting the hints was taken for the agents view")
	}
}
