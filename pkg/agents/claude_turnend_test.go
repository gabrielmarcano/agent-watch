package agents

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
)

// Record shapes as Claude Code 2.1.289 writes them (keys checked on the
// owner's transcripts, 2026-10-05; values made up).
func turnDuration(uuid string, sidechain bool) string {
	return turnDurationN(uuid, sidechain, 1)
}

// turnDurationN is a turn end with n background agents still running; Claude
// leaves the key out when there are none.
func turnDurationN(uuid string, sidechain bool, n int) string {
	sc := "false"
	if sidechain {
		sc = "true"
	}
	pending := ""
	if n > 0 {
		pending = `"pendingBackgroundAgentCount":` + strconv.Itoa(n) + `,`
	}
	return `{"type":"system","subtype":"turn_duration","durationMs":1200,"messageCount":4,` + pending + `"isMeta":false,"isSidechain":` + sc + `,"uuid":"` + uuid + `","timestamp":"2026-10-05T23:45:11.000Z"}` + "\n"
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
		end, err := c.LastCompletedTurn(ctx, ref)
		item, marker := end.Item, end.Marker
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

	end, err := c.LastCompletedTurn(context.Background(), ref)
	if item := end.Item; err != nil || end.Marker != "u1" || item == nil || item.Response != "a" || item.Query != "q" || end.Background != 0 {
		t.Fatalf("got %+v, %v", end, err)
	}

	fi, _ := os.Stat(path)
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(fi.Size()))), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	if end, err := c.LastCompletedTurn(context.Background(), ref); err != nil || end.Marker != "u1" || end.Item == nil || end.Item.Response != "a" {
		t.Errorf("unchanged file: got %+v, %v; want the cached turn", end, err)
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

// userString is a user line whose content is the string text; isMeta as
// given.
func userString(t *testing.T, text string, isMeta bool) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"type": "user", "isMeta": isMeta, "message": map[string]any{"role": "user", "content": text}})
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

// A background task's report (a shell, a monitor or an agent that finished
// or sent an event) starts a turn whose query is the report's summary, never
// its markup. Shapes as Claude Code writes them (the owner's transcripts,
// 2026-10-07; ids and texts made up).
func TestClaudeLastTurn_TaskNotification(t *testing.T) {
	shell := "<task-notification>\n<task-id>b1</task-id>\n<tool-use-id>toolu_1</tool-use-id>\n<output-file>/tmp/tasks/b1.output</output-file>\n<status>completed</status>\n<summary>Background command \"Run the tests\" completed (exit code 0)</summary>\n</task-notification>"
	monitor := "<task-notification>\n<task-id>b2</task-id>\n<summary>Monitor event: \"build served\"</summary>\n<event>SERVED abc123</event>\nIf this event is something the user would act on now, send a PushNotification.\n</task-notification>"
	stopped := "<task-notification>\n<task-id>b3</task-id>\n<status>stopped</status>\n</task-notification>"
	meta := "[SYSTEM NOTIFICATION - NOT USER INPUT]\nThis is an automated background-task event, NOT a message from the user.\n\n<task-notification>\n<task-id>a1</task-id>\n<status>completed</status>\n<summary>Agent \"Review the diff\" finished</summary>\n</task-notification>"
	answer := `{"type":"assistant","message":{"content":[{"type":"text","text":"noted"}]}}` + "\n"
	cases := []struct {
		name, lines, want string
	}{
		{"a shell", userString(t, shell, false), `Background command "Run the tests" completed (exit code 0)`},
		{"a monitor event", userString(t, monitor, false), `Monitor event: "build served"`},
		{"no summary", userString(t, stopped, false), ""},
		{"two reports in one message", userString(t, shell+"\n"+monitor, false), "Background command \"Run the tests\" completed (exit code 0)\nMonitor event: \"build served\""},
		{"marked as Claude Code's own", userString(t, meta, true), `Agent "Review the diff" finished`},
		{"the user quoting one", userString(t, "why did this fail? "+shell, false), "why did this fail? " + shell},
	}
	c := newClaudeAdapter(Config{})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := claudeTurnLines("q", "a") + turnDuration("u1", false) + tc.lines + answer
			item, err := c.LastTurn(context.Background(), SessionRef{Kind: "path", Value: writeFile(t, "n.jsonl", content)})
			if err != nil || item.Query != tc.want || item.Response != "noted" {
				t.Errorf("got %+v, %v; want query %q", item, err, tc.want)
			}
		})
	}
}

// The background agents a turn end leaves running count only while Claude
// waits on them: a newer turn (a background agent's report, a prompt) sets
// the count to 0 until that turn ends with its own count; a local command
// or `!` shell input starts no turn. Sequences as on the owner's
// transcripts (2026-10-05; values made up).
func TestClaudeLastCompletedTurn_Background(t *testing.T) {
	ctx := context.Background()
	path := writeFile(t, "bg.jsonl", claudeTurnLines("launch three agents", "launched"))
	ref := SessionRef{Agent: "claude", Kind: "path", Value: path}
	c := newClaudeAdapter(Config{})
	answer := func(text string) string {
		return `{"type":"assistant","message":{"content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
	}
	steps := []struct {
		name   string
		add    string
		want   int
		marker string
	}{
		{"no turn end yet", "", 0, ""},
		{"turn ends with 3 running", turnDurationN("u1", false, 3) +
			`{"type":"system","subtype":"stop_hook_summary","isSidechain":false}` + "\n" +
			`{"type":"queue-operation","operation":"enqueue"}` + "\n" +
			`{"type":"ai-title","aiTitle":"t"}` + "\n", 3, "u1"},
		{"a sub-agent's own lines change nothing",
			`{"type":"assistant","isSidechain":true,"message":{"content":[{"type":"text","text":"x"}]}}` + "\n" +
				turnDurationN("s1", true, 0), 3, "u1"},
		{"a local command starts no turn",
			metaUser("<local-command-caveat>Caveat</local-command-caveat>") +
				`{"type":"user","message":{"content":"<command-name>/model</command-name>"}}` + "\n" +
				`{"type":"user","message":{"content":"<local-command-stdout>Set model</local-command-stdout>"}}` + "\n" +
				`{"type":"user","message":{"content":"<bash-input>ls</bash-input>"}}` + "\n", 3, "u1"},
		{"an agent's report starts a turn", metaUser("<task-notification>agent done</task-notification>"), 0, "u1"},
		{"that turn ends with 2 running", answer("noted") + turnDurationN("u2", false, 2), 2, "u2"},
		{"a task notification string starts a turn", `{"type":"user","message":{"content":"<task-notification>x</task-notification>"}}` + "\n", 0, "u2"},
		{"it ends with 1 running", answer("ok") + turnDurationN("u3", false, 1), 1, "u3"},
		{"the owner prompts", `{"type":"user","message":{"content":"status?"}}` + "\n", 0, "u3"},
		{"generating", answer("still 1 running"), 0, "u3"},
		{"ends with 1 running", turnDurationN("u4", false, 1), 1, "u4"},
		{"the last report", metaUser("<task-notification>last</task-notification>") + answer("all done"), 0, "u4"},
		{"the last turn ends with none", turnDurationN("u5", false, 0), 0, "u5"},
	}
	for _, s := range steps {
		if s.add != "" {
			appendFile(t, path, s.add)
		}
		end, err := c.LastCompletedTurn(ctx, ref)
		if err != nil || end.Background != s.want || end.Marker != s.marker {
			t.Fatalf("%s: background %d, marker %q, err %v; want %d, %q", s.name, end.Background, end.Marker, err, s.want, s.marker)
		}
	}
}
