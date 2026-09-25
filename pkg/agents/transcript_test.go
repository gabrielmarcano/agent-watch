package agents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// A 20 KB answer is cut to 16 384 bytes on a rune boundary, plus the marker.
func TestTranscriptTruncation20KB(t *testing.T) {
	ctx := context.Background()
	answer := strings.Repeat("añ€ ", 2600) // 1+2+3+1 bytes: 18 200 bytes, cut lands mid-rune
	answer += strings.Repeat("x", 20*1024-len(answer))
	if len(answer) != 20*1024 {
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
		if len(body) > 16384 || len(body) < 16384-3 {
			t.Errorf("%s: body is %d bytes, want 16381..16384", name, len(body))
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
