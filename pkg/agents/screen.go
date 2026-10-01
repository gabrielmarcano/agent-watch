package agents

import (
	"strings"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// ScreenTurnReader is implemented by adapters that recognise, on a text screen,
// the user's last message. The screen-capture history then holds that turn
// only, instead of everything the screen shows (agents.md §6).
type ScreenTurnReader interface {
	// SplitScreenTurn gets the screen's lines above the agent's input box. It
	// returns the user's last message and the lines of the agent's reply,
	// without the adapter's own chrome (markers, status lines). ok is false
	// when no message of the user is on screen.
	SplitScreenTurn(lines []string) (query string, reply []string, ok bool)
}

// ScreenTurnFor formats a screen capture into a HistoryItem with ad's help
// when ad implements ScreenTurnReader, and like ScreenTurn otherwise.
func ScreenTurnFor(ad Adapter, text string) *model.HistoryItem {
	lines := screenBody(text)
	if r, ok := ad.(ScreenTurnReader); ok {
		if query, reply, ok := r.SplitScreenTurn(lines); ok {
			if reply = dedent(reply); len(reply) > 0 {
				return screenItem(query, reply)
			}
		}
	}
	return screenItem("", lines)
}

// ScreenTurn formats terminal screen text into a HistoryItem per agents.md §6.
func ScreenTurn(text string) *model.HistoryItem {
	return screenItem("", screenBody(text))
}

func screenItem(query string, lines []string) *model.HistoryItem {
	lines = boxTables(lines)
	// Keep the last 80 lines
	if len(lines) > 80 {
		lines = lines[len(lines)-80:]
	}
	return &model.HistoryItem{
		Source:   "screen",
		Query:    query,
		Response: model.TruncateUTF8(strings.Join(lines, "\n"), model.MaxResponseBytes),
	}
}

// screenBody returns the screen's lines without trailing blank lines and
// without the agent's input box: everything from the last run of box-drawing,
// framed or prompt-marker lines downwards, if found within the last 15 lines.
func screenBody(text string) []string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := trimBlankTail(strings.Split(normalized, "\n"))

	searchStart := len(lines) - 15
	if searchStart < 0 {
		searchStart = 0
	}
	cutIdx := -1
	for i := len(lines) - 1; i >= searchStart; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if isBoxOnlyLine(trimmed) || isPromptMarkerLine(trimmed) || isFramedLine(trimmed) {
			cutIdx = i
		} else if trimmed != "" && cutIdx >= 0 {
			break
		}
	}
	if cutIdx >= 0 {
		lines = trimBlankTail(lines[:cutIdx])
	}
	return lines
}

func trimBlankTail(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// dedent drops blank lines at both ends and the indentation every other line shares.
func dedent(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	lines = trimBlankTail(lines)
	common := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		indent := len(l) - len(strings.TrimLeft(l, " "))
		if common < 0 || indent < common {
			common = indent
		}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		if len(l) >= common && common > 0 {
			l = l[common:]
		}
		out[i] = strings.TrimRight(l, " \t")
	}
	return out
}

// isFramedLine reports a line drawn inside a heavy vertical frame, as OpenCode
// draws its input box ("┃  Build · …").
func isFramedLine(s string) bool {
	return strings.HasPrefix(s, "┃")
}

func isBoxOnlyLine(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("─│┌┐└┘├┤┬┴┼╭╮╯╰═║╔╗╚╝╠╣╦╩╬╌╍╎╏━┃┏┓┗┛┣┫┳┻╋╹╻╸╺▀▄-_= ", r) {
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

// echoedMessage finds the user's last message echoed as marker + text, with
// its wrapped lines indented under it, and returns it as one line plus the
// lines that follow it.
func echoedMessage(lines []string, marker string) (string, []string, bool) {
	start := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], marker) && strings.TrimSpace(strings.TrimPrefix(lines[i], marker)) != "" {
			start = i
			break
		}
	}
	if start < 0 {
		return "", nil, false
	}
	parts := []string{strings.TrimSpace(strings.TrimPrefix(lines[start], marker))}
	i := start + 1
	for ; i < len(lines) && strings.HasPrefix(lines[i], "  ") && strings.TrimSpace(lines[i]) != ""; i++ {
		parts = append(parts, strings.TrimSpace(lines[i]))
	}
	return strings.Join(parts, " "), lines[i:], true
}
