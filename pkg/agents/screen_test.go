package agents

import (
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
