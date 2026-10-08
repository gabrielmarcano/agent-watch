package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Record shapes as Claude Code writes them (keys checked on the owner's
// transcripts, 2026-10-07; ids, texts and times made up).

// jsonLine writes v as Claude Code does: one line, `<` and `>` unescaped.
func jsonLine(t *testing.T, v map[string]any) string {
	t.Helper()
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// toolResult is the user line answering a tool call, with Claude Code's
// structured result.
func toolResult(t *testing.T, result map[string]any, sidechain bool) string {
	return jsonLine(t, map[string]any{
		"type": "user", "isSidechain": sidechain, "timestamp": "2026-10-07T20:00:00.000Z",
		"message":       map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": "ok"}}},
		"toolUseResult": result,
	})
}

func bgShell(t *testing.T, id string) string {
	return toolResult(t, map[string]any{"stdout": "", "stderr": "", "interrupted": false, "backgroundTaskId": id}, false)
}

func bgMonitor(t *testing.T, id string, timeoutMs int64, persistent bool) string {
	return toolResult(t, map[string]any{"taskId": id, "timeoutMs": timeoutMs, "persistent": persistent}, false)
}

func notification(id, status, event string) string {
	n := "<task-notification>\n<task-id>" + id + "</task-id>\n"
	if status != "" {
		n += "<status>" + status + "</status>\n"
	}
	n += "<summary>s</summary>\n"
	if event != "" {
		n += "<event>" + event + "</event>\n"
	}
	return n + "</task-notification>"
}

func TestClaudeBackgroundTasks(t *testing.T) {
	start := time.Date(2026, 10, 7, 20, 0, 0, 0, time.UTC)
	userNote := func(text string) string {
		return jsonLine(t, map[string]any{"type": "user", "isSidechain": false, "origin": map[string]any{"kind": "task-notification"},
			"message": map[string]any{"role": "user", "content": text}})
	}
	cases := []struct {
		name     string
		lines    string
		at       time.Duration // time after the launches
		shells   int
		monitors int
	}{
		{"none", claudeTurnLines("q", "a"), 0, 0, 0},
		{"a shell running", bgShell(t, "b1"), 0, 1, 0},
		{"a foreground command moved to the background when it timed out",
			toolResult(t, map[string]any{"stdout": "", "backgroundTaskId": "b1", "timedOutAfterMs": 120000}, false), 0, 1, 0},
		{"a shell that completed", bgShell(t, "b1") + userNote(notification("b1", "completed", "")), 0, 0, 0},
		{"a report that arrived mid-turn", bgShell(t, "b1") + bgShell(t, "b2") +
			jsonLine(t, map[string]any{"type": "queue-operation", "operation": "enqueue", "content": notification("b1", "failed", "")}) +
			jsonLine(t, map[string]any{"type": "attachment", "isSidechain": false, "attachment": map[string]any{"type": "queued_command", "prompt": notification("b2", "killed", "")}}),
			0, 0, 0},
		{"a report marked as Claude Code's own", bgShell(t, "b1") +
			jsonLine(t, map[string]any{"type": "user", "isMeta": true, "message": map[string]any{"role": "user", "content": "[SYSTEM NOTIFICATION - NOT USER INPUT]\n\n" + notification("b1", "completed", "")}}),
			0, 0, 0},
		{"stopped with TaskStop", bgShell(t, "b1") +
			toolResult(t, map[string]any{"message": "Successfully stopped task: b1 (sleep 60)", "task_id": "b1", "task_type": "local_bash"}, false), 0, 0, 0},
		{"orphans of the previous session", bgShell(t, "b1") + bgShell(t, "b2") +
			userNote("<task-notification>\n<task-id>b1</task-id>\n<task-id>b2</task-id>\n<task-id>__orphan_summary__:shell</task-id>\n<status>stopped</status>\n<summary>2 background shell command tasks didn't finish</summary>\n</task-notification>"),
			0, 0, 0},
		{"a monitor before its timeout", bgMonitor(t, "m1", 1_200_000, false), 19 * time.Minute, 0, 1},
		{"a monitor past its timeout", bgMonitor(t, "m1", 1_200_000, false), 21 * time.Minute, 0, 0},
		{"a persistent monitor", bgMonitor(t, "m1", 3_600_000, true), 48 * time.Hour, 0, 1},
		{"a monitor's event keeps it running", bgMonitor(t, "m1", 1_200_000, false) + userNote(notification("m1", "", "SERVED abc123")), 0, 0, 1},
		{"a monitor that expired", bgMonitor(t, "m1", 1_200_000, false) +
			userNote(notification("m1", "", "[Monitor expired after 20m with no events delivered.]")), 0, 0, 0},
		{"a monitor whose stream ended", bgMonitor(t, "m1", 1_200_000, false) + userNote(notification("m1", "completed", "done")), 0, 0, 0},
		{"a sub-agent's own task", toolResult(t, map[string]any{"backgroundTaskId": "b1"}, true), 0, 0, 0},
		{"tool output that quotes a report ends nothing", bgShell(t, "b1") +
			toolResult(t, map[string]any{"stdout": notification("b1", "completed", "")}, false), 0, 1, 0},
		{"a shell and two monitors", bgShell(t, "b1") + bgMonitor(t, "m1", 1_200_000, false) + bgMonitor(t, "m2", 0, true) + bgShell(t, "b2") +
			userNote(notification("b2", "completed", "")), time.Minute, 1, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newClaudeAdapter(Config{})
			c.now = func() time.Time { return start.Add(tc.at) }
			got, err := c.BackgroundTasks(context.Background(), SessionRef{Kind: "path", Value: writeFile(t, "t.jsonl", tc.lines)})
			if err != nil || got.Shells != tc.shells || got.Monitors != tc.monitors {
				t.Errorf("got %+v, %v; want %d shells, %d monitors", got, err, tc.shells, tc.monitors)
			}
		})
	}
}

// Only the tail of backgroundTaskWindow bytes is read: a task started before
// it is not counted (and its end, inside it, changes nothing).
func TestClaudeBackgroundTasks_Window(t *testing.T) {
	early := bgShell(t, "b1") + bgShell(t, "b2")
	filler := strings.Repeat(claudeTurnLines("q", strings.Repeat("x", 100)), 50)
	late := jsonLine(t, map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": notification("b1", "completed", "")}}) + bgShell(t, "b3")
	c := newClaudeAdapter(Config{})
	c.taskWindow = int64(len(filler) + len(late))
	got, err := c.BackgroundTasks(context.Background(), SessionRef{Kind: "path", Value: writeFile(t, "w.jsonl", early+filler+late)})
	if err != nil || got != (BackgroundTasks{Shells: 1}) {
		t.Errorf("got %+v, %v; want only b3", got, err)
	}
}

// An unchanged transcript is not read again, but a monitor's deadline still
// counts against the clock; an appended record is read.
func TestClaudeBackgroundTasks_Cache(t *testing.T) {
	start := time.Date(2026, 10, 7, 20, 0, 0, 0, time.UTC)
	now := start
	c := newClaudeAdapter(Config{})
	c.now = func() time.Time { return now }
	path := writeFile(t, "c.jsonl", bgShell(t, "b1")+bgMonitor(t, "m1", 60_000, false))
	ref := SessionRef{Kind: "path", Value: path}
	check := func(step string, want BackgroundTasks) {
		t.Helper()
		if got, err := c.BackgroundTasks(context.Background(), ref); err != nil || got != want {
			t.Errorf("%s: got %+v, %v; want %+v", step, got, err, want)
		}
	}
	check("first read", BackgroundTasks{Shells: 1, Monitors: 1})
	now = start.Add(2 * time.Minute)
	check("the monitor timed out", BackgroundTasks{Shells: 1})
	appendFile(t, path, jsonLine(t, map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": notification("b1", "completed", "")}}))
	check("the shell completed", BackgroundTasks{})
}
