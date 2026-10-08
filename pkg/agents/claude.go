package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

type claudeAdapter struct {
	*genericAdapter
	cfg Config
	// tailWindows are the transcript tail sizes LastTurn reads, smallest
	// first: a turn with many tool calls can put the user's message
	// megabytes before the end of the file.
	tailWindows []int64
	turnEnds    turnEndCache
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
	IsMeta      bool            `json:"isMeta"`
	Subtype     string          `json:"subtype"`
	Message     json.RawMessage `json:"message"`
}

type claudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type claudeContentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	ID        string `json:"id"`          // tool_use
	Name      string `json:"name"`        // tool_use
	ToolUseID string `json:"tool_use_id"` // tool_result
}

// LastTurn reads the transcript of the conversation the pane shows
// (sessionPath), growing the tail read through tailWindows until the user's
// last message is in it. If even the largest window does not reach it, the
// item holds the final answer without its query. A turn that ends in a tool
// call still waiting for its result (a question, or a permission herdr did
// not report as blocked) has no reply yet: ErrNoReply.
func (c *claudeAdapter) LastTurn(ctx context.Context, ref SessionRef) (*model.HistoryItem, error) {
	path, err := c.sessionPath(ctx, ref)
	if err != nil {
		return nil, err
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
		turn, err := claudeLastTurn(ctx, content)
		if err != nil {
			return nil, err
		}
		if !turn.found && fi.Size() > window && i < len(c.tailWindows)-1 {
			continue
		}
		if turn.response == "" {
			if turn.pendingTool != "" {
				return nil, fmt.Errorf("%w: the turn waits on its %s call", ErrNoReply, turn.pendingTool)
			}
			return nil, ErrNoTranscript
		}
		return &model.HistoryItem{
			Query:    turn.query,
			Response: model.TruncateUTF8(turn.response, model.MaxResponseBytes),
			Source:   "transcript",
		}, nil
	}
	return nil, ErrNoTranscript
}

// isCommandWrapper reports whether a user line's trimmed text is Claude
// Code's wrapper around a slash command, a local command's output, `!` shell
// input or output, or a system reminder: never a message of the user.
func isCommandWrapper(t string) bool {
	return strings.HasPrefix(t, "<command-") ||
		strings.HasPrefix(t, "<local-command-") ||
		strings.HasPrefix(t, "<bash-") ||
		strings.HasPrefix(t, "<system-reminder>")
}

// taskNotificationTag opens Claude Code's report of a background task (a
// shell, a monitor or an agent) that finished or sent an event.
const taskNotificationTag = "<task-notification>"

// taskNotificationSummary returns the <summary> texts of the task
// notifications in t, one per line, and whether t holds any notification.
// A notification without a summary adds nothing.
func taskNotificationSummary(t string) (string, bool) {
	if !strings.Contains(t, taskNotificationTag) {
		return "", false
	}
	var summaries []string
	for _, block := range strings.Split(t, taskNotificationTag)[1:] {
		block, _, _ = strings.Cut(block, "</task-notification>")
		_, rest, ok := strings.Cut(block, "<summary>")
		if !ok {
			continue
		}
		summary, _, _ := strings.Cut(rest, "</summary>")
		if summary = strings.TrimSpace(summary); summary != "" {
			summaries = append(summaries, summary)
		}
	}
	return strings.Join(summaries, "\n"), true
}

// claudeTurn is the last turn found in a transcript tail.
type claudeTurn struct {
	query, response string
	// found is false when no message of the user is in the tail; the
	// response is then the text after the last tool call in all of it.
	found bool
	// pendingTool names the tool of the turn's last call when no
	// tool_result answered it and no text followed it; "" otherwise.
	pendingTool string
}

// claudeLastTurn finds, in a transcript tail, the user's last message and the
// answer after the last tool call that follows it.
func claudeLastTurn(ctx context.Context, content string) (claudeTurn, error) {
	lines := strings.Split(content, "\n")
	type parsedEntry struct {
		isUser    bool
		isAssist  bool
		queryText string
		blocks    []claudeContentBlock
		results   []string // the tool_use ids a tool_result line answers
		turnEnd   bool     // Claude's turn_duration record
	}

	var entries []parsedEntry

	for _, l := range lines {
		if err := ctx.Err(); err != nil {
			return claudeTurn{}, err
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
		if row.Type == "system" && row.Subtype == "turn_duration" {
			entries = append(entries, parsedEntry{turnEnd: true})
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
					var results []string
					for _, b := range blocks {
						if b.Type == "tool_result" {
							results = append(results, b.ToolUseID)
						}
						if b.Type == "text" && b.Text != "" {
							textParts = append(textParts, b.Text)
						}
					}
					if len(results) > 0 {
						// Tool output, not a message of the user.
						entries = append(entries, parsedEntry{results: results})
					} else if len(textParts) > 0 {
						textContent = strings.Join(textParts, "\n")
					}
				}
			}

			// Filter out command wrappers and system reminders
			t := strings.TrimSpace(textContent)
			if row.IsMeta {
				// Written by Claude Code, not typed by the user. Right after a
				// turn ended (a background agent's notification) it starts a
				// turn whose query is the notification's summary, if any;
				// inside a turn (a skill's text) it is not a message at all.
				if t != "" && (len(entries) == 0 || entries[len(entries)-1].turnEnd) {
					summary, _ := taskNotificationSummary(t)
					entries = append(entries, parsedEntry{isUser: true, queryText: summary})
				}
			} else if summary, ok := taskNotificationSummary(t); ok && strings.HasPrefix(t, taskNotificationTag) {
				// A background task's report, not the user's message: its
				// summary stands for the raw markup.
				entries = append(entries, parsedEntry{isUser: true, queryText: summary})
			} else if t != "" && !isCommandWrapper(t) {
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
	var turn claudeTurn
	if lastUserIdx >= 0 {
		turn.query, turn.found = entries[lastUserIdx].queryText, true
	}

	// Walk forward from lastUserIdx (from the start without one) and collect
	// assistant text blocks, resetting on tool_use. Track the calls still
	// waiting for their tool_result.
	var responseBlocks []string
	pending := map[string]string{} // tool_use id -> tool name
	lastCall := ""                 // the last tool_use, while no text follows it
	for i := lastUserIdx + 1; i < len(entries); i++ {
		e := entries[i]
		for _, id := range e.results {
			delete(pending, id)
		}
		for _, b := range e.blocks {
			if b.Type == "tool_use" {
				responseBlocks = nil
				pending[b.ID] = b.Name
				lastCall = b.ID
			} else if b.Type == "text" && b.Text != "" {
				responseBlocks = append(responseBlocks, b.Text)
				lastCall = ""
			}
		}
	}
	if name, ok := pending[lastCall]; ok && lastCall != "" {
		turn.pendingTool = name
		if turn.pendingTool == "" {
			turn.pendingTool = "tool"
		}
	}
	turn.response = strings.TrimSpace(strings.Join(responseBlocks, "\n\n"))
	return turn, nil
}

func (c *claudeAdapter) resolvePath(ref SessionRef) string {
	path := ""
	switch {
	case ref.Kind == "path" && ref.Value != "":
		path = ref.Value
	case ref.Kind == "id" && validSessionID(ref.Value):
		path = c.findSession(ref.Value, ref.CWD)
	}
	if path == "" {
		return ""
	}
	return c.followContinuation(path, ref.CWD)
}

// validSessionID refuses anything that could leave projects/ or act as a
// glob pattern: a session id is a UUID.
func validSessionID(id string) bool {
	return id != "" && id != ".." && !strings.ContainsAny(id, `/\*?[`)
}

// findSession returns the newest file of session id in the profiles.
func (c *claudeAdapter) findSession(id, cwd string) string {
	return findSessionIn(c.configDirs(), id, cwd)
}

// findSessionIn returns the newest file of session id in dirs, looking first
// in the project of cwd, then in any project (the session's cwd may have
// moved).
func findSessionIn(dirs []string, id, cwd string) string {
	slug := strings.ReplaceAll(strings.ReplaceAll(cwd, "/", "-"), ".", "-")
	var found []string
	for _, dir := range dirs {
		target := filepath.Join(dir, "projects", slug, id+".jsonl")
		if fi, err := os.Stat(target); err == nil && fi.Mode().IsRegular() {
			found = append(found, target)
		}
	}
	if len(found) == 0 {
		for _, dir := range dirs {
			matches, _ := filepath.Glob(filepath.Join(dir, "projects", "*", id+".jsonl"))
			found = append(found, matches...)
		}
	}
	return newestFile(found)
}

// maxContinuationHops bounds the walk through continued-in pointers.
const maxContinuationHops = 8

// followContinuation walks the `continued-in` pointers Claude Code writes as
// the last line of a session it continued in a new one: herdr keeps reporting
// the old session, but the conversation goes on in the file the chain ends
// at. A loop, an invalid id or a missing file ends the walk where it is.
func (c *claudeAdapter) followContinuation(path, cwd string) string {
	seen := map[string]bool{path: true}
	for hop := 0; hop < maxContinuationHops; hop++ {
		next := continuedIn(path)
		if !validSessionID(next) {
			break
		}
		nextPath := c.findSession(next, cwd)
		if nextPath == "" || seen[nextPath] {
			break
		}
		seen[nextPath] = true
		path = nextPath
	}
	return path
}

// continuedIn returns the session id of the last continued-in pointer in the
// tail of path, or "".
func continuedIn(path string) string {
	content, err := TailFile(path, 256<<10)
	if err != nil {
		return ""
	}
	lines := strings.Split(content, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if !strings.Contains(l, `"continued-in"`) {
			continue
		}
		var row struct {
			Type                 string `json:"type"`
			ContinuedInSessionID string `json:"continuedInSessionId"`
		}
		if json.Unmarshal([]byte(l), &row) == nil && row.Type == "continued-in" {
			return row.ContinuedInSessionID
		}
	}
	return ""
}

// configDirs returns the Claude profiles to search: the configured ones,
// then ~/.claude and every ~/.claude-* holding a projects dir (each
// CLAUDE_CONFIG_DIR the user made that way). It is read on every lookup, so
// a new profile works without restarting the bridge.
func (c *claudeAdapter) configDirs() []string {
	var dirs []string
	seen := map[string]bool{}
	add := func(d string) {
		if d = filepath.Clean(d); !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	for _, d := range c.cfg.ClaudeConfigDirs {
		add(d)
	}
	if c.cfg.Home != "" {
		matches, _ := filepath.Glob(filepath.Join(c.cfg.Home, ".claude*"))
		for _, m := range matches {
			if fi, err := os.Stat(filepath.Join(m, "projects")); err == nil && fi.IsDir() {
				add(m)
			}
		}
	}
	return dirs
}

// newestFile returns the most recently modified of paths: a session copied
// into a backup profile must not shadow the live one.
func newestFile(paths []string) string {
	newest := ""
	var newestTime time.Time
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if newest == "" || fi.ModTime().After(newestTime) {
			newest, newestTime = p, fi.ModTime()
		}
	}
	return newest
}
