package agents

import (
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
	// Generate string > 16384 bytes
	bigStr := strings.Repeat("A", 20000)
	item := ScreenTurn(bigStr)
	if len(item.Response) > 16384+len("\n\n…[truncated]") {
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
