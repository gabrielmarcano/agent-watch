package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

type claudeAdapter struct {
	*genericAdapter
	cfg Config
	// tailWindows are the transcript tail sizes LastTurn reads, smallest
	// first: a turn with many tool calls can put the user's message
	// megabytes before the end of the file.
	tailWindows []int64
}

func newClaudeAdapter(cfg Config) *claudeAdapter {
	return &claudeAdapter{
		genericAdapter: newGenericAdapter(),
		cfg:            cfg,
		tailWindows:    []int64{256 << 10, 1 << 20, 4 << 20},
	}
}

func (c *claudeAdapter) Name() string {
	return "claude"
}

func (c *claudeAdapter) PromptWhileWorking() bool {
	return true
}

func (c *claudeAdapter) ParsePrompt(screen string) (Prompt, bool) {
	m, ok := findMenu(screen)
	if !ok {
		return Prompt{}, false
	}

	lines := screenLines(screen)

	// Claude hides its input box ("❯" between two rules) while a dialog is
	// open. A numbered list with the input box below it is part of an answer.
	if !dialogAtBottom(lines, m, "❯") {
		return Prompt{}, false
	}

	// Find the top border of the Claude prompt box above m.StartLine (solid line, not dashed diff border)
	topSep := -1
	for j := m.StartLine - 1; j >= 0; j-- {
		if isBoxOrSepLine(lines[j]) && !strings.Contains(lines[j], "╌") {
			topSep = j
			break
		}
	}

	var title string
	var detailLines []string

	if topSep >= 0 && topSep+1 < m.StartLine {
		// Line right after top divider is the Title
		lineIdx := topSep + 1
		for lineIdx < m.StartLine && strings.TrimSpace(lines[lineIdx]) == "" {
			lineIdx++
		}

		if lineIdx < m.StartLine {
			rawTitle := cleanBoxChars(lines[lineIdx])
			// Strip Claude question indicator markers (e.g. ☐ or [ ])
			rawTitle = strings.TrimPrefix(rawTitle, "☐")
			rawTitle = strings.TrimPrefix(rawTitle, "[ ]")
			title = strings.TrimSpace(rawTitle)

			inDiff := false
			for k := lineIdx + 1; k < m.StartLine; k++ {
				curr := strings.TrimRight(lines[k], " \t")
				cleaned := cleanBoxChars(curr)

				// Toggle diff block
				if strings.Contains(curr, "╌╌") {
					inDiff = !inDiff
					continue
				}
				if inDiff {
					continue
				}

				// Skip empty lines, Claude UI tips, and confirmation questions
				if cleaned == "" || isBoxOrSepLine(curr) {
					continue
				}
				if strings.HasPrefix(cleaned, "Tip:") {
					continue
				}
				if strings.HasPrefix(cleaned, "Do you want to ") {
					continue
				}

				detailLines = append(detailLines, cleaned)
			}
		}
	}

	if title != "" {
		m.Title = title
		m.Detail = strings.Join(detailLines, "\n") // buildPrompt caps it on a rune boundary
	}

	// "Type something." (AskUserQuestion) opens a text field the watch cannot fill.
	p := withoutOptions(buildPrompt(m, digitKeys), func(o model.PromptOption) bool {
		return o.Label == "Type something."
	})
	return p, true
}

// SplitScreenTurn implements ScreenTurnReader: Claude echoes the user's
// message as "❯ text" (wrapped lines indented by 2), marks its reply with "⏺"
// and ends the turn with a status line such as "✻ Worked for 1s · done 10:25 AM".
func (c *claudeAdapter) SplitScreenTurn(lines []string) (string, []string, bool) {
	query, rest, ok := echoedMessage(lines, "❯ ")
	if !ok {
		return "", nil, false
	}
	var reply []string
	for _, l := range rest {
		if strings.HasPrefix(l, "✻ ") {
			continue
		}
		reply = append(reply, strings.Replace(l, "⏺ ", "  ", 1))
	}
	return query, reply, true
}

type claudeLine struct {
	Type        string          `json:"type"`
	UUID        string          `json:"uuid"`
	SessionID   string          `json:"sessionId"`
	IsSidechain bool            `json:"isSidechain"`
	Message     json.RawMessage `json:"message"`
}

type claudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type claudeContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// LastTurn reads the transcript's tail, growing the read through tailWindows
// until the user's last message is in it. If even the largest window does
// not reach it, the item holds the final answer without its query.
func (c *claudeAdapter) LastTurn(ctx context.Context, ref SessionRef) (*model.HistoryItem, error) {
	path := c.resolvePath(ref)
	if path == "" {
		return nil, ErrNoTranscript
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, ErrNoTranscript
	}

	for i, window := range c.tailWindows {
		content, err := TailFile(path, window)
		if err != nil {
			return nil, ErrNoTranscript
		}
		query, response, found, err := claudeLastTurn(ctx, content)
		if err != nil {
			return nil, err
		}
		if !found && fi.Size() > window && i < len(c.tailWindows)-1 {
			continue
		}
		if response == "" {
			return nil, ErrNoTranscript
		}
		return &model.HistoryItem{
			Query:    query,
			Response: model.TruncateUTF8(response, 16384),
			Source:   "transcript",
		}, nil
	}
	return nil, ErrNoTranscript
}

// claudeLastTurn finds, in a transcript tail, the user's last message and the
// answer after the last tool call that follows it. found is false when no
// message of the user is in content; the answer is then the text after the
// last tool call in all of content.
func claudeLastTurn(ctx context.Context, content string) (query, response string, found bool, err error) {
	lines := strings.Split(content, "\n")
	type parsedEntry struct {
		isUser    bool
		isAssist  bool
		queryText string
		blocks    []claudeContentBlock
	}

	var entries []parsedEntry

	for _, l := range lines {
		if err := ctx.Err(); err != nil {
			return "", "", false, err
		}
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}

		var row claudeLine
		if err := json.Unmarshal([]byte(trimmed), &row); err != nil {
			continue
		}
		if row.IsSidechain {
			continue
		}
		if row.Type != "user" && row.Type != "assistant" {
			continue
		}

		var msg claudeMessage
		if err := json.Unmarshal(row.Message, &msg); err != nil {
			continue
		}

		if row.Type == "user" {
			// Extract query text
			var textContent string
			// Could be plain string
			var strContent string
			if err := json.Unmarshal(msg.Content, &strContent); err == nil {
				textContent = strContent
			} else {
				// Could be array of blocks
				var blocks []claudeContentBlock
				if err := json.Unmarshal(msg.Content, &blocks); err == nil {
					var textParts []string
					hasToolResult := false
					for _, b := range blocks {
						if b.Type == "tool_result" {
							hasToolResult = true
							break
						}
						if b.Type == "text" && b.Text != "" {
							textParts = append(textParts, b.Text)
						}
					}
					if !hasToolResult && len(textParts) > 0 {
						textContent = strings.Join(textParts, "\n")
					}
				}
			}

			// Filter out command wrappers and system reminders
			t := strings.TrimSpace(textContent)
			if t != "" &&
				!strings.HasPrefix(t, "<command-") &&
				!strings.HasPrefix(t, "<local-command-") &&
				!strings.HasPrefix(t, "<bash-") &&
				!strings.HasPrefix(t, "<system-reminder>") {
				entries = append(entries, parsedEntry{
					isUser:    true,
					queryText: textContent,
				})
			}
		} else if row.Type == "assistant" {
			var blocks []claudeContentBlock
			if err := json.Unmarshal(msg.Content, &blocks); err == nil {
				entries = append(entries, parsedEntry{
					isAssist: true,
					blocks:   blocks,
				})
			}
		}
	}

	// Find the last user query
	lastUserIdx := -1
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].isUser {
			lastUserIdx = i
			break
		}
	}
	if lastUserIdx >= 0 {
		query, found = entries[lastUserIdx].queryText, true
	}

	// Walk forward from lastUserIdx (from the start without one) and collect
	// assistant text blocks, resetting on tool_use
	var responseBlocks []string
	for i := lastUserIdx + 1; i < len(entries); i++ {
		e := entries[i]
		if !e.isAssist {
			continue
		}
		for _, b := range e.blocks {
			if b.Type == "tool_use" {
				responseBlocks = nil
			} else if b.Type == "text" && b.Text != "" {
				responseBlocks = append(responseBlocks, b.Text)
			}
		}
	}

	return query, strings.TrimSpace(strings.Join(responseBlocks, "\n\n")), found, nil
}

func (c *claudeAdapter) resolvePath(ref SessionRef) string {
	if ref.Kind == "path" && ref.Value != "" {
		return ref.Value
	}

	if ref.Kind == "id" && ref.Value != "" {
		slug := ref.CWD
		slug = strings.ReplaceAll(slug, "/", "-")
		slug = strings.ReplaceAll(slug, ".", "-")

		for _, dir := range c.cfg.ClaudeConfigDirs {
			target := filepath.Join(dir, "projects", slug, ref.Value+".jsonl")
			if fi, err := os.Stat(target); err == nil && fi.Mode().IsRegular() {
				return target
			}
			// Glob fallback
			pattern := filepath.Join(dir, "projects", "*", ref.Value+".jsonl")
			if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
				return matches[0]
			}
		}
	}

	return ""
}
