package agents

import (
	"strings"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// ScreenTurn formats terminal screen text into a HistoryItem per agents.md §6.
func ScreenTurn(text string) *model.HistoryItem {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")

	// Trim trailing blank lines
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	if len(lines) == 0 {
		return &model.HistoryItem{
			Source:   "screen",
			Query:    "",
			Response: "",
		}
	}

	// Drop the agent's input box:
	// Cut everything from the last line that contains only box-drawing characters
	// or a prompt marker (❯, >) downwards, if found within the last 15 lines.
	searchStart := len(lines) - 15
	if searchStart < 0 {
		searchStart = 0
	}

	cutIdx := -1
	for i := len(lines) - 1; i >= searchStart; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if isBoxOnlyLine(trimmed) || isPromptMarkerLine(trimmed) {
			cutIdx = i
		} else if trimmed != "" && cutIdx >= 0 {
			break
		}
	}

	if cutIdx >= 0 {
		lines = lines[:cutIdx]
		// Trim any new trailing blank lines after cut
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
	}

	// Keep the last 80 lines
	if len(lines) > 80 {
		lines = lines[len(lines)-80:]
	}

	response := strings.Join(lines, "\n")
	response = model.TruncateUTF8(response, 16384)

	return &model.HistoryItem{
		Source:   "screen",
		Query:    "",
		Response: response,
	}
}

func isBoxOnlyLine(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("─│┌┐└┘├┤┬┴┼╭╮╯╰═║╔╗╚╝╠╣╦╩╬╌╍╎╏-_= ", r) {
			return false
		}
	}
	return true
}

func isPromptMarkerLine(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "❯" || trimmed == ">" || trimmed == "›" || trimmed == "▶" || trimmed == "●" {
		return true
	}
	if strings.HasPrefix(trimmed, "❯ ") || strings.HasPrefix(trimmed, "> ") {
		return true
	}
	return false
}
