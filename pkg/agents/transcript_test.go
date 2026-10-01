package agents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// Known-answer vectors, computed outside Go with the formulas in
// contracts.md §1.3 / §1.4 (Python hashlib.sha256, first 16 hex characters).
// The golden test compares against model.Fingerprint itself, so it cannot
// catch a change in the hash; these can.
func TestFingerprintKnownAnswers(t *testing.T) {
	cases := []struct {
		name                string
		kind                model.PromptKind
		title, detail, want string
		labels              []string
	}{
		{
			// The example PendingPrompt in contracts.md §1.2. The document
			// prints this value since 2026-09-25 (it used to show a made-up one).
			name: "contracts.md example", kind: model.PromptPermission,
			title: "Bash command", detail: "go test ./...",
			labels: []string{"Yes", "Yes, and don't ask again for go test commands", "No, and tell Claude what to do differently"},
			want:   "fd6ff7388739252d",
		},
		{
			name: "claude/permission-bash golden", kind: model.PromptPermission,
			title: "Bash command", detail: "touch foo.txt && ls foo.txt\nCreate empty foo.txt and confirm it exists",
			labels: []string{"Yes", "Yes, and always allow access to /tmp/aw-sandbox from this project", "Yes, and switch to auto mode · auto mode handles these prompts for you", "No"},
			want:   "76ffefa05e05e32d",
		},
		{name: "unknown prompt", kind: model.PromptUnknown, want: "2da1cc5cae72ed96"},
	}
	for _, c := range cases {
		if got := model.Fingerprint(c.kind, c.title, c.detail, c.labels); got != c.want {
			t.Errorf("%s: Fingerprint = %s, want %s", c.name, got, c.want)
		}
	}

	// End to end: parsing the real screens yields those exact values.
	screens := []struct{ agent, file, want string }{
		{"claude", "permission-bash.txt", "76ffefa05e05e32d"},
		{"opencode", "permission-bash.txt", "bd367534b30e30ea"},
	}
	reg := NewRegistry(Config{})
	for _, s := range screens {
		b, err := os.ReadFile(filepath.Join("testdata", s.agent, s.file))
		if err != nil {
			t.Fatal(err)
		}
		p, ok := reg.For(s.agent).ParsePrompt(string(b))
		if !ok || p.Public.Fingerprint != s.want {
			t.Errorf("%s/%s: fingerprint %q (ok=%v), want %s", s.agent, s.file, p.Public.Fingerprint, ok, s.want)
		}
	}
	if got := UnknownPrompt("anything").Public.Fingerprint; got != "2da1cc5cae72ed96" {
		t.Errorf("UnknownPrompt fingerprint = %s", got)
	}
}

func TestHistoryIDKnownAnswers(t *testing.T) {
	if got := model.HistoryID("w5:pAE", "2fa2e4dc-97a2-4ab3-999a-f55d96097be1", "also say hi", "Hi"); got != "cd3262f295c67d25" {
		t.Errorf("HistoryID = %s, want cd3262f295c67d25", got)
	}
	if got := model.HistoryID("w5:pAE", "", "", "screen text"); got != "089930590c827b99" {
		t.Errorf("HistoryID (no session, no query) = %s, want 089930590c827b99", got)
	}
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Unparseable lines are skipped; a file with nothing usable is ErrNoTranscript,
// never a panic.
func TestTranscriptBadJSON(t *testing.T) {
	ctx := context.Background()
	claude := newClaudeAdapter(Config{})
	agy := newAgyAdapter(Config{})

	mixed := `not json at all
{"type":"user","message":{"content":"real question"},"isSidechain":false}
{"type":"assistant","message":{"content":[{"type":"text","text":"real answer"}]}
{"type":"assistant","message":"not an object","isSidechain":false}
{"type":"assistant","message":{"content":"a string, not blocks"},"isSidechain":false}
{"type":"assistant","message":{"content":[{"type":"text","text":"real answer"}]},"isSidechain":false}
{"type":"user","message":{"content":42},"isSidechain":false}
`
	item, err := claude.LastTurn(ctx, SessionRef{Kind: "path", Value: writeFile(t, "mixed.jsonl", mixed)})
	if err != nil {
		t.Fatalf("claude mixed: %v", err)
	}
	if item.Query != "real question" || item.Response != "real answer" {
		t.Errorf("claude mixed: got %q / %q", item.Query, item.Response)
	}

	garbage := "{\n}}}\n\x00\x01\n[1,2,3]\n\"string\"\n"
	if _, err := claude.LastTurn(ctx, SessionRef{Kind: "path", Value: writeFile(t, "g.jsonl", garbage)}); !errors.Is(err, ErrNoTranscript) {
		t.Errorf("claude garbage: err = %v, want ErrNoTranscript", err)
	}

	agyMixed := `{"type":"USER_INPUT","content":"<USER_REQUEST>\nq\n</USER_REQUEST>"}
{broken
{"type":"PLANNER_RESPONSE","content":"a","tool_calls":[]}
{"type":"PLANNER_RESPONSE","content":42}
`
	item, err = agy.LastTurn(ctx, SessionRef{Kind: "path", Value: writeFile(t, "agy.jsonl", agyMixed)})
	if err != nil || item.Query != "q" || item.Response != "a" {
		t.Errorf("agy mixed: item %+v, err %v", item, err)
	}
	if _, err := agy.LastTurn(ctx, SessionRef{Kind: "path", Value: writeFile(t, "agyg.jsonl", garbage)}); !errors.Is(err, ErrNoTranscript) {
		t.Errorf("agy garbage: err = %v, want ErrNoTranscript", err)
	}

	// OpenCode: rows whose JSON does not parse are skipped.
	dump := loadOpenCodeDump(t, "session-multistep.json")
	for i := range dump.Messages {
		if i == 0 {
			dump.Messages[i].Data = json.RawMessage(`{"role":`) // truncated JSON
		}
	}
	dump.Parts = append(dump.Parts, dump.Parts[len(dump.Parts)-1])
	dump.Parts[len(dump.Parts)-1].ID = "prt_zzzzbroken"
	dump.Parts[len(dump.Parts)-1].Data = json.RawMessage(`not json`)
	oc := newOpenCodeAdapter(Config{OpenCodeDBPath: writeOpenCodeDB(t, dump)})
	item, err = oc.LastTurn(ctx, SessionRef{Kind: "id", Value: dump.SessionID})
	if err != nil || item.Response != "The file note.txt has 1 line." {
		t.Errorf("opencode with broken rows: item %+v, err %v", item, err)
	}
}

// LastTurn fills Query, Response and Source only. The bridge fills ID (the
// contracts.md §1.4 hash, with SessionRef.Value as session_value), PaneID,
// Agent, Label and CompletedAt.
func TestLastTurnLeavesIDToCaller(t *testing.T) {
	ctx := context.Background()
	dump := loadOpenCodeDump(t, "session-multistep.json")
	agyPath := filepath.Join(t.TempDir(), "brain", "conv-1", ".system_generated", "logs", "transcript_full.jsonl")
	if err := os.MkdirAll(filepath.Dir(agyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agyPath, []byte(`{"type":"USER_INPUT","content":"q"}`+"\n"+`{"type":"PLANNER_RESPONSE","content":"a"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		ad   Adapter
		ref  SessionRef
	}{
		{"claude", newClaudeAdapter(Config{}), SessionRef{Kind: "path", Value: filepath.Join("testdata", "claude", "transcript.jsonl")}},
		{"agy path", newAgyAdapter(Config{}), SessionRef{Kind: "path", Value: agyPath}},
		{"agy id", newAgyAdapter(Config{AgyBrainDir: filepath.Join(filepath.Dir(agyPath), "..", "..", "..")}), SessionRef{Kind: "id", Value: "conv-1"}},
		{"opencode", newOpenCodeAdapter(Config{OpenCodeDBPath: writeOpenCodeDB(t, dump)}), SessionRef{Kind: "id", Value: dump.SessionID}},
	}
	for _, c := range cases {
		item, err := c.ad.LastTurn(ctx, c.ref)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if item.ID != "" || item.PaneID != "" || item.Agent != "" || item.CompletedAt != "" {
			t.Errorf("%s: adapter filled caller fields: %+v", c.name, *item)
		}
		if item.Source != "transcript" || item.Response == "" {
			t.Errorf("%s: Source=%q Response=%q", c.name, item.Source, item.Response)
		}
	}
}

// An answer over model.MaxResponseBytes is cut on a rune boundary, plus the marker.
func TestTranscriptTruncation20KB(t *testing.T) {
	ctx := context.Background()
	size := model.MaxResponseBytes + 4096
	answer := strings.Repeat("añ€ ", 9500) // 1+2+3+1 bytes: 66 500 bytes, the cut lands mid-rune
	answer += strings.Repeat("x", size-len(answer))
	if len(answer) != size {
		t.Fatalf("setup: %d bytes", len(answer))
	}
	const marker = "\n\n…[truncated]"

	check := func(name string, item *model.HistoryItem, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasSuffix(item.Response, marker) {
			t.Errorf("%s: no truncation marker", name)
		}
		body := strings.TrimSuffix(item.Response, marker)
		if len(body) > model.MaxResponseBytes || len(body) < model.MaxResponseBytes-3 {
			t.Errorf("%s: body is %d bytes, want %d..%d", name, len(body), model.MaxResponseBytes-3, model.MaxResponseBytes)
		}
		if !utf8.ValidString(item.Response) || !strings.HasPrefix(answer, body) {
			t.Errorf("%s: response is not a clean UTF-8 prefix of the answer", name)
		}
	}

	claudeLine, _ := json.Marshal(map[string]any{"type": "assistant", "isSidechain": false,
		"message": map[string]any{"content": []map[string]string{{"type": "text", "text": answer}}}})
	claudeFile := `{"type":"user","message":{"content":"long?"},"isSidechain":false}` + "\n" + string(claudeLine) + "\n"
	item, err := newClaudeAdapter(Config{}).LastTurn(ctx, SessionRef{Kind: "path", Value: writeFile(t, "c.jsonl", claudeFile)})
	check("claude", item, err)

	agyLine, _ := json.Marshal(map[string]any{"type": "PLANNER_RESPONSE", "content": answer})
	agyFile := `{"type":"USER_INPUT","content":"long?"}` + "\n" + string(agyLine) + "\n"
	item, err = newAgyAdapter(Config{}).LastTurn(ctx, SessionRef{Kind: "path", Value: writeFile(t, "a.jsonl", agyFile)})
	check("agy", item, err)

	dump := loadOpenCodeDump(t, "session-multistep.json")
	for i := range dump.Parts {
		var d map[string]any
		_ = json.Unmarshal(dump.Parts[i].Data, &d)
		if d["type"] == "text" && d["text"] == "The file note.txt has 1 line." {
			d["text"] = answer
			dump.Parts[i].Data, _ = json.Marshal(d)
		}
	}
	item, err = newOpenCodeAdapter(Config{OpenCodeDBPath: writeOpenCodeDB(t, dump)}).LastTurn(ctx, SessionRef{Kind: "id", Value: dump.SessionID})
	check("opencode", item, err)
}

// The reasoning parts in the captured sessions are all empty, so "reasoning
// is skipped" was vacuous. Give the final step's reasoning part real text.
func TestOpenCodeLastTurn_SkipsReasoningText(t *testing.T) {
	ctx := context.Background()
	dump := loadOpenCodeDump(t, "session-multistep.json")

	final := dump.Messages[len(dump.Messages)-1].ID
	found := false
	for i, p := range dump.Parts {
		var d map[string]any
		_ = json.Unmarshal(p.Data, &d)
		if p.MessageID == final && d["type"] == "reasoning" {
			d["text"] = "PRIVATE REASONING: count the lines first"
			dump.Parts[i].Data, _ = json.Marshal(d)
			found = true
		}
	}
	if !found {
		t.Fatal("fixture has no reasoning part on the final message")
	}

	item, err := newOpenCodeAdapter(Config{OpenCodeDBPath: writeOpenCodeDB(t, dump)}).LastTurn(ctx, SessionRef{Kind: "id", Value: dump.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(item.Response, "PRIVATE REASONING") {
		t.Errorf("reasoning text leaked into the response: %q", item.Response)
	}
	if item.Response != "The file note.txt has 1 line." {
		t.Errorf("response = %q", item.Response)
	}
}

// A real turn with four tool calls: the user's message lies ~20 KB before the
// end. LastTurn grows its read until it reaches the message; if even the
// largest window misses it, the item keeps the answer without the query.
func TestClaudeLastTurn_LongTurnGrowsTheRead(t *testing.T) {
	path := filepath.Join("testdata", "claude", "transcript-long-turn.jsonl")
	b, err := os.ReadFile(filepath.Join("testdata", "claude", "transcript-long-turn.expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want expectedTranscript
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	ref := SessionRef{Agent: "claude", Kind: "path", Value: path}

	c := newClaudeAdapter(Config{})
	c.tailWindows = []int64{4 << 10, 64 << 10}
	item, err := c.LastTurn(context.Background(), ref)
	if err != nil {
		t.Fatalf("LastTurn: %v", err)
	}
	if item.Query != want.Query || item.Response != want.Response {
		t.Errorf("got query %q, response %q", item.Query, item.Response)
	}

	c.tailWindows = []int64{4 << 10}
	item, err = c.LastTurn(context.Background(), ref)
	if err != nil {
		t.Fatalf("LastTurn, one small window: %v", err)
	}
	if item.Query != "" || item.Response != want.Response {
		t.Errorf("one small window: got query %q, response %q", item.Query, item.Response)
	}
}

// Claude Code can continue a conversation in a new session and leave a
// `continued-in` pointer as the old file's last line, while herdr keeps
// reporting the old session id. The reader follows the pointer to the file
// that is still written; a loop, a bad id or a missing file stops the walk.
func TestClaudeResolvePath_FollowsContinuation(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-tmp-aw-sandbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	turn := func(q, a string) string {
		return `{"type":"user","message":{"role":"user","content":"` + q + `"}}` + "\n" +
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + a + `"}]}}` + "\n"
	}
	cont := func(from, to string) string {
		return `{"type":"continued-in","sessionId":"` + from + `","continuedInSessionId":"` + to + `","timestamp":"2026-09-30T23:33:16.000Z"}` + "\n"
	}
	write := func(id, content string) string {
		t.Helper()
		path := filepath.Join(dir, id+".jsonl")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	c := newClaudeAdapter(Config{Home: home})
	ref := func(id string) SessionRef {
		return SessionRef{Agent: "claude", Kind: "id", Value: id, CWD: "/tmp/aw-sandbox"}
	}

	// Two hops: a → b → c.
	write("a", turn("old question", "old answer")+cont("a", "b"))
	write("b", turn("middle question", "middle answer")+cont("b", "c"))
	live := write("c", turn("new question", "new answer"))
	if got := c.resolvePath(ref("a")); got != live {
		t.Errorf("chain: got %q, want %q", got, live)
	}
	item, err := c.LastTurn(context.Background(), ref("a"))
	if err != nil || item == nil || item.Query != "new question" || item.Response != "new answer" {
		t.Errorf("LastTurn through the chain = %+v, %v; want the new turn", item, err)
	}

	// A loop stops at the last file not yet visited.
	loopStart := write("l1", turn("q1", "a1")+cont("l1", "l2"))
	write("l2", turn("q2", "a2")+cont("l2", "l1"))
	if got := c.resolvePath(ref("l1")); got != filepath.Join(dir, "l2.jsonl") {
		t.Errorf("loop: got %q, want l2 (started at %q)", got, loopStart)
	}

	// A pointer to an id that could leave projects/, or to a missing file: stay.
	for _, bad := range []string{"../escape", "*", "missing"} {
		stay := write("x", turn("q", "a")+cont("x", bad))
		if got := c.resolvePath(ref("x")); got != stay {
			t.Errorf("pointer %q: got %q, want %q", bad, got, stay)
		}
	}
}

// Claude profiles are found without configuration: ~/.claude and every
// ~/.claude-* with a projects dir, read on each lookup. The newest copy of a
// session wins over one in a backup profile; ids that could leave projects/
// or act as a glob are refused.
func TestClaudeResolvePath_DiscoversProfiles(t *testing.T) {
	home := t.TempDir()
	write := func(path string, age time.Duration) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-age)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := newClaudeAdapter(Config{Home: home})
	ref := func(id string) SessionRef {
		return SessionRef{Agent: "claude", Kind: "id", Value: id, CWD: "/tmp/aw-sandbox"}
	}

	// A profile made after the adapter: found on the next lookup.
	live := filepath.Join(home, ".claude-work-2", "projects", "-tmp-aw-sandbox", "s1.jsonl")
	write(live, 0)
	if got := c.resolvePath(ref("s1")); got != live {
		t.Errorf("new profile: got %q, want %q", got, live)
	}

	// An older copy in a backup profile does not shadow it.
	write(filepath.Join(home, ".claude-work-2.bak", "projects", "-tmp-aw-sandbox", "s1.jsonl"), time.Hour)
	if got := c.resolvePath(ref("s1")); got != live {
		t.Errorf("with a backup copy: got %q, want %q", got, live)
	}

	// The session's cwd moved: any project of any profile.
	moved := filepath.Join(home, ".claude", "projects", "-old-cwd", "s2.jsonl")
	write(moved, 0)
	if got := c.resolvePath(ref("s2")); got != moved {
		t.Errorf("moved cwd: got %q, want %q", got, moved)
	}

	// A configured profile outside the ~/.claude* pattern.
	other := t.TempDir()
	extra := filepath.Join(other, "projects", "-tmp-aw-sandbox", "s3.jsonl")
	write(extra, 0)
	if got := newClaudeAdapter(Config{Home: home, ClaudeConfigDirs: []string{other}}).resolvePath(ref("s3")); got != extra {
		t.Errorf("configured dir: got %q, want %q", got, extra)
	}

	for _, id := range []string{"*", "s?", "../s1", "..", `a\b`, "[s]1"} {
		if got := c.resolvePath(ref(id)); got != "" {
			t.Errorf("id %q resolved to %q", id, got)
		}
	}

	// End to end: a real transcript in a discovered profile, read by id.
	data, err := os.ReadFile(filepath.Join("testdata", "claude", "transcript-long-turn.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	profiled := filepath.Join(home, ".claude-work-3", "projects", "-tmp-aw-sandbox", "s4.jsonl")
	write(profiled, 0)
	if err := os.WriteFile(profiled, data, 0o644); err != nil {
		t.Fatal(err)
	}
	item, err := c.LastTurn(context.Background(), ref("s4"))
	if err != nil || !strings.HasPrefix(item.Response, "| Paso | Resultado |") {
		t.Errorf("LastTurn by id in a discovered profile: %v, %+v", err, item)
	}
}
