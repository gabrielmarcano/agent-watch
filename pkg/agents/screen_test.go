package agents

import (
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScreenTurn_Basic(t *testing.T) {
	screen := "line 1\nline 2\nline 3\n\n"
	item := ScreenTurn(screen)
	if item.Source != "screen" {
		t.Errorf("expected Source=screen, got %q", item.Source)
	}
	if item.Query != "" {
		t.Errorf("expected empty query, got %q", item.Query)
	}
	if item.Response != "line 1\nline 2\nline 3" {
		t.Errorf("unexpected response: %q", item.Response)
	}
}

func TestScreenTurn_InputBoxCut(t *testing.T) {
	screen := `compiling project...
build succeeded.
tests passed: 42/42.

────────────────────────────
❯ `
	item := ScreenTurn(screen)
	if strings.Contains(item.Response, "────") || strings.Contains(item.Response, "❯") {
		t.Errorf("expected input box to be cut, got:\n%s", item.Response)
	}
	if !strings.Contains(item.Response, "tests passed: 42/42.") {
		t.Errorf("expected output to contain tests passed, got:\n%s", item.Response)
	}
}

func TestScreenTurn_Max80Lines(t *testing.T) {
	var lines []string
	for i := 1; i <= 120; i++ {
		lines = append(lines, "line")
	}
	screen := strings.Join(lines, "\n")
	item := ScreenTurn(screen)
	resultLines := strings.Split(item.Response, "\n")
	if len(resultLines) != 80 {
		t.Errorf("expected 80 lines, got %d", len(resultLines))
	}
}

func TestScreenTurn_Truncation(t *testing.T) {
	bigStr := strings.Repeat("A", model.MaxResponseBytes+4000)
	item := ScreenTurn(bigStr)
	if len(item.Response) > model.MaxResponseBytes+len("\n\n…[truncated]") {
		t.Errorf("response exceeded max length: %d", len(item.Response))
	}
	if !strings.HasSuffix(item.Response, "\n\n…[truncated]") {
		t.Errorf("expected response to end with truncation marker")
	}
}

// Real idle screens: with the adapter's help the history item holds the last
// turn only, the user's message as the query and the reply without the TUI.
func TestScreenTurnFor_RealScreens(t *testing.T) {
	const query = `Without using any tools, reply with a numbered list of exactly three short options for a new name for note.txt, one per line, like "1. notes.txt". Nothing else.`
	cases := []struct {
		agent, fixture, response string
	}{
		{"claude", "claude/no-menu-idle-numbered-list.txt", "1. notes.txt\n2. scratch-notes.txt\n3. todo-notes.txt"},
		{"agy", "agy/no-menu-idle-numbered-list.txt", "1. notes.txt\n2. memo.txt\n3. scratchpad.txt"},
		{"opencode", "opencode/no-menu-idle-numbered-list.txt", "1. notes.txt\n2. memo.txt\n3. todo.txt"},
	}
	registry := NewRegistry(Config{})
	for _, c := range cases {
		data, err := os.ReadFile(filepath.Join("testdata", c.fixture))
		if err != nil {
			t.Fatal(err)
		}
		item := ScreenTurnFor(registry.For(c.agent), string(data))
		if item.Source != "screen" {
			t.Errorf("%s: source = %q", c.agent, item.Source)
		}
		if item.Query != query {
			t.Errorf("%s: query = %q", c.agent, item.Query)
		}
		if item.Response != c.response {
			t.Errorf("%s: response = %q, want %q", c.agent, item.Response, c.response)
		}
	}
}

// A pane working on its last message has nothing to publish: the adapter
// reports an empty reply and ScreenTurnFor yields no item, so the bridge
// keeps the last item instead of publishing the live progress, a queued
// message or the chrome over the input box (aw-sandbox captures).
func TestScreenTurnFor_ClaudeRunningTurn(t *testing.T) {
	ad := NewRegistry(Config{}).For("claude")
	for _, fixture := range []string{
		"claude/no-menu-working.txt",
		"claude/no-menu-working-numbered-list.txt",
		"claude/no-menu-working-queued.txt",
	} {
		data, err := os.ReadFile(filepath.Join("testdata", fixture))
		if err != nil {
			t.Fatal(err)
		}
		if item := ScreenTurnFor(ad, string(data)); item != nil {
			t.Errorf("%s: item = %+v, want none", fixture, item)
		}
	}
}

// A message queued while the pane worked shows under the running turn; when
// the turn closes, both answers sit above the closing status line and the
// queued message is the query of the turn that answered it.
func TestScreenTurnFor_ClaudeQueuedMessageTurn(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "claude", "no-menu-idle-queued-merged.txt"))
	if err != nil {
		t.Fatal(err)
	}
	item := ScreenTurnFor(NewRegistry(Config{}).For("claude"), string(data))
	if item == nil {
		t.Fatal("no item for the closed turn")
	}
	if item.Query != "Also reply QUEUED-ONE when you are done." {
		t.Errorf("query = %q", item.Query)
	}
	if item.Response != "DONE-PY\n\nQUEUED-ONE" {
		t.Errorf("response = %q", item.Response)
	}
}

// The chrome Claude leaves over its input box — a recap, the update notice,
// the titled rule — is not part of the last reply. With no echo on the
// screen, the reply alone is kept.
func TestScreenTurnFor_ClaudeComposerChrome(t *testing.T) {
	screen := "⏺ Renamed note.txt to notes.txt.\n\n" +
		"✻ Worked for 1s · done 10:25 AM\n\n" +
		"  ⎿  Tip: Use /feedback to help us improve!\n\n" +
		"※ recap: the rename is done and git status is clean. (disable recaps in /config)\n\n" +
		"currentVersion: 2.1.301\nUpdate available! Run: claude update\n\n" +
		"──────────── rename-note-to-notes ────────────\n❯ \n────────────\n" +
		"  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents\n"
	item := ScreenTurnFor(NewRegistry(Config{}).For("claude"), screen)
	if item == nil {
		t.Fatal("no item for the screen")
	}
	if item.Query != "" {
		t.Errorf("query = %q", item.Query)
	}
	if item.Response != "Renamed note.txt to notes.txt." {
		t.Errorf("response = %q", item.Response)
	}
}

// A queued message that wraps puts its continuation line right over the
// input box, where the box cut alone cannot reach (the scan stops at the
// next drawn line above it). The message still has no reply under it, so
// nothing is published.
func TestScreenTurnFor_ClaudeWrappedQueuedMessage(t *testing.T) {
	screen := "⏺ Renamed note.txt to notes.txt.\n\n" +
		"✻ Worked for 1s · done 10:25 AM\n\n" +
		"❯ Also check that the wrapped queued message keeps its continuation line above the input\n" +
		"  box, which the box cut alone would not reach.\n\n" +
		"────────────\n❯ \n────────────\n" +
		"  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents\n"
	if item := ScreenTurnFor(NewRegistry(Config{}).For("claude"), screen); item != nil {
		t.Errorf("item = %+v, want none", item)
	}
}

// Without an adapter that knows the screen, the whole screen above the input
// box is kept; OpenCode's heavy frame (┃, ╹▀▀▀) counts as the input box too.
func TestScreenTurn_OpenCodeInputBoxIsCut(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "opencode", "no-menu-idle-numbered-list.txt"))
	if err != nil {
		t.Fatal(err)
	}
	item := ScreenTurn(string(data))
	lines := strings.Split(item.Response, "\n")
	if got := strings.TrimSpace(lines[len(lines)-1]); got != "▣  Build · Muse Spark 1.3 Free · 3.5s" {
		t.Errorf("last line = %q", got)
	}
	for _, gone := range []string{"OpenCode Zen", "▀", "ctrl+p commands"} {
		if strings.Contains(item.Response, gone) {
			t.Errorf("response still has %q", gone)
		}
	}
}

// OpenCode V2 frames tool calls in "┃" like the user's message: the query is
// the user's block, and the tool blocks and V2's footer are not in the reply.
func TestScreenTurnFor_OpenCodeV2ToolBlocks(t *testing.T) {
	cases := []struct {
		fixture, query, response string
	}{
		{"no-menu-idle-shell-v2.txt", "Run the shell command ls -la and tell me what you see",
			"I'll run ls -la to see what's here.\n\n" +
				"ls -la in /private/tmp/aw-sandbox shows:\n\n" +
				"- total 8\n" +
				"- . (current dir, drwxr-xr-x, owner me, modified Oct 10 11:41)\n" +
				"- .. (parent dir, drwxrwxrwt, owner root)\n" +
				"- .git/ (drwxr-xr-x, 9 entries)\n" +
				"- note.txt (-rw-r--r--, 6 bytes)\n\n" +
				"So it's a git repo with a single visible file: note.txt."},
		{"no-menu-idle-declined-v2.txt", "Append the line world to note.txt",
			"Appending that line for you.\n\n→ Explored: 1 read"},
	}
	ad := NewRegistry(Config{}).For("opencode")
	for _, c := range cases {
		data, err := os.ReadFile(filepath.Join("testdata", "opencode", c.fixture))
		if err != nil {
			t.Fatal(err)
		}
		item := ScreenTurnFor(ad, string(data))
		if item.Query != c.query {
			t.Errorf("%s: query = %q, want %q", c.fixture, item.Query, c.query)
		}
		if item.Response != c.response {
			t.Errorf("%s: response = %q, want %q", c.fixture, item.Response, c.response)
		}
	}
}
