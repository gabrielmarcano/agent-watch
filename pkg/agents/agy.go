package agents

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

type agyAdapter struct {
	*genericAdapter
	cfg Config
}

func newAgyAdapter(cfg Config) *agyAdapter {
	return &agyAdapter{
		genericAdapter: newGenericAdapter(),
		cfg:            cfg,
	}
}

// SplitScreenTurn implements ScreenTurnReader: agy echoes the user's message
// as "> text", wrapped lines indented by 2, and prints its reply under it.
func (a *agyAdapter) SplitScreenTurn(lines []string) (string, []string, bool) {
	return echoedMessage(lines, "> ")
}

func (a *agyAdapter) TaskTitle(rawTitle string) string {
	t := strings.TrimSpace(rawTitle)
	if strings.HasPrefix(t, "agy --conversation") {
		return ""
	}
	return t
}

func (a *agyAdapter) ScreenTurn(screen string) *model.HistoryItem {
	lines := screenBody(screen)
	if query, reply, ok := a.SplitScreenTurn(lines); ok {
		if reply = dedent(reply); len(reply) > 0 {
			return screenItem(query, reply)
		}
		return nil
	}
	return screenItem("", lines)
}

func (a *agyAdapter) ObserveSettledTurn(ctx context.Context, ref SessionRef) (*model.HistoryItem, BackgroundTasks, error) {
	item, err := a.LastTurn(ctx, ref)
	return item, BackgroundTasks{}, err
}

func (a *agyAdapter) Name() string {
	return "agy"
}

func (a *agyAdapter) PromptWhileWorking() bool {
	return true
}

func (a *agyAdapter) ParsePrompt(screen string) (Prompt, bool) {
	m, ok := findMenu(screen)
	if !ok {
		return Prompt{}, false
	}

	lines := screenLines(screen)

	// Antigravity replaces its input box (">" between two rules) with the
	// dialog. herdr may report the pane as done or working while the dialog is
	// open, so the screen, not the status, decides.
	if !dialogAtBottom(lines, m, ">") {
		return Prompt{}, false
	}

	// Find the separator line above the menu
	sepIdx := -1
	for j := m.StartLine - 1; j >= 0; j-- {
		if isBoxOrSepLine(lines[j]) {
			sepIdx = j
			break
		}
	}

	var title string
	var detail string

	if sepIdx > 0 {
		// Title is the line above the separator
		above := sepIdx - 1
		for above >= 0 && strings.TrimSpace(lines[above]) == "" {
			above--
		}
		if above >= 0 {
			title = cleanBoxChars(lines[above])
		}

		if title == "Command" {
			// Find "Requesting permission for:" then next non-empty line
			for k := sepIdx + 1; k < m.StartLine; k++ {
				cleaned := cleanBoxChars(lines[k])
				if strings.Contains(cleaned, "Requesting permission for:") {
					for l := k + 1; l < m.StartLine; l++ {
						cmd := cleanBoxChars(lines[l])
						if cmd != "" && !strings.HasPrefix(cmd, "Run this command?") {
							detail = cmd
							break
						}
					}
					break
				}
			}
		} else if title == "Pending edit" {
			// First non-empty line below separator is file path + diff count
			for k := sepIdx + 1; k < m.StartLine; k++ {
				cleaned := cleanBoxChars(lines[k])
				if cleaned != "" {
					detail = cleaned
					break
				}
			}
		}
	}

	if title != "" {
		m.Title = title
		m.Detail = detail
	}

	p := buildPrompt(m, digitKeys)

	// Quirk: Antigravity disables Esc during file edits and uses option 2 to reject
	if title == "Pending edit" {
		p.CancelKeys = []string{"2"}
	}

	return p, true
}

type agyStep struct {
	Type      string          `json:"type"`
	Content   string          `json:"content"`
	CreatedAt string          `json:"created_at"`
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
}

// agyEntry is one parsed step of an agy transcript.
type agyEntry struct {
	stepType  string
	content   string
	createdAt string
	toolCalls bool
}

// parseAgyEntries reads the steps of an agy transcript tail, in order.
func parseAgyEntries(ctx context.Context, content string) ([]agyEntry, error) {
	var entries []agyEntry
	for _, l := range strings.Split(content, "\n") {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}

		var row agyStep
		if err := json.Unmarshal([]byte(trimmed), &row); err != nil {
			continue
		}

		hasToolCalls := false
		if len(row.ToolCalls) > 0 && string(row.ToolCalls) != "null" && string(row.ToolCalls) != "[]" {
			hasToolCalls = true
		}

		entries = append(entries, agyEntry{
			stepType:  row.Type,
			content:   row.Content,
			createdAt: row.CreatedAt,
			toolCalls: hasToolCalls,
		})
	}
	return entries, nil
}

func (a *agyAdapter) LastTurn(ctx context.Context, ref SessionRef) (*model.HistoryItem, error) {
	path := a.resolvePath(ref)
	if path == "" {
		return nil, ErrNoTranscript
	}

	content, err := TailFile(path, 256*1024)
	if err != nil {
		return nil, ErrNoTranscript
	}

	steps, err := parseAgyEntries(ctx, content)
	if err != nil {
		return nil, err
	}

	// Query = content of last USER_INPUT step
	lastUserIdx := -1
	for i := len(steps) - 1; i >= 0; i-- {
		if steps[i].stepType == "USER_INPUT" {
			lastUserIdx = i
			break
		}
	}
	if lastUserIdx < 0 {
		return nil, ErrNoTranscript
	}

	query := agyUserRequest(steps[lastUserIdx].content)

	// Response = content of the last PLANNER_RESPONSE after that query that has
	// non-empty content and no tool_calls.
	var response, completedAt string
	for i := lastUserIdx + 1; i < len(steps); i++ {
		s := steps[i]
		if s.stepType == "PLANNER_RESPONSE" && !s.toolCalls && strings.TrimSpace(s.content) != "" {
			response, completedAt = s.content, transcriptTime(s.createdAt)
		}
	}

	response = strings.TrimSpace(response)
	if response == "" {
		return nil, ErrNoTranscript
	}

	response = model.TruncateUTF8(response, model.MaxResponseBytes)

	return &model.HistoryItem{
		Query:       query,
		Response:    response,
		Source:      "transcript",
		AskedAt:     transcriptTime(steps[lastUserIdx].createdAt),
		CompletedAt: completedAt,
	}, nil
}

// RecentTurns implements HistoryReader: up to max finished turns, newest
// first, from the transcript's 256 KiB tail. A turn ends where the next
// USER_INPUT starts; its answer is the last PLANNER_RESPONSE with text and no
// tool calls in between.
func (a *agyAdapter) RecentTurns(ctx context.Context, ref SessionRef, max int) ([]model.HistoryItem, error) {
	if max <= 0 {
		return nil, ErrNoTranscript
	}
	path := a.resolvePath(ref)
	if path == "" {
		return nil, ErrNoTranscript
	}
	content, err := TailFile(path, 256*1024)
	if err != nil {
		return nil, ErrNoTranscript
	}
	steps, err := parseAgyEntries(ctx, content)
	if err != nil {
		return nil, err
	}

	items := make([]model.HistoryItem, 0, max)
	for i, s := range steps {
		if s.stepType != "USER_INPUT" {
			continue
		}
		query := agyUserRequest(s.content)
		if query == "" {
			continue
		}
		var response, completedAt string
		for j := i + 1; j < len(steps) && steps[j].stepType != "USER_INPUT"; j++ {
			next := steps[j]
			if next.stepType == "PLANNER_RESPONSE" && !next.toolCalls && strings.TrimSpace(next.content) != "" {
				response, completedAt = next.content, transcriptTime(next.createdAt)
			}
		}
		if strings.TrimSpace(response) == "" {
			continue
		}
		items = append(items, model.HistoryItem{
			Query:       query,
			Response:    model.TruncateUTF8(strings.TrimSpace(response), model.MaxResponseBytes),
			Source:      "transcript",
			AskedAt:     transcriptTime(s.createdAt),
			CompletedAt: completedAt,
		})
	}
	if len(items) == 0 {
		return nil, ErrNoTranscript
	}
	// Oldest first in the file: keep the newest max, newest first.
	if len(items) > max {
		items = items[len(items)-max:]
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items, nil
}

// agyUserRequest returns what the user typed. Antigravity (1.2.x) stores a
// USER_INPUT step as "<USER_REQUEST>\n…\n</USER_REQUEST>" followed by blocks
// such as <ADDITIONAL_METADATA> (local time) or <USER_SETTINGS_CHANGE>.
// Content without the wrapper is returned as is.
func agyUserRequest(content string) string {
	const open, closing = "<USER_REQUEST>", "</USER_REQUEST>"
	start := strings.Index(content, open)
	if start < 0 {
		return strings.TrimSpace(content)
	}
	rest := content[start+len(open):]
	if end := strings.Index(rest, closing); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

func (a *agyAdapter) resolvePath(ref SessionRef) string {
	if ref.Kind == "path" && ref.Value != "" {
		return ref.Value
	}

	id := ""
	if ref.Kind == "id" && ref.Value != "" {
		id = ref.Value
	} else if ref.Value == "" && ref.CWD != "" {
		id = a.conversationForCWD(ref.CWD)
	}

	if id != "" && validSessionID(id) && a.cfg.AgyBrainDir != "" {
		target := filepath.Join(a.cfg.AgyBrainDir, id, ".system_generated", "logs", "transcript_full.jsonl")
		if _, err := os.Stat(target); err == nil {
			return target
		}
	}

	return ""
}

// conversationForCWD reads ~/.gemini/antigravity-cli/cache/last_conversations.json
// (workspace path -> conversation UUID) when herdr-core reported no explicit
// session id or path.
func (a *agyAdapter) conversationForCWD(cwd string) string {
	if a.cfg.AgyBrainDir == "" || cwd == "" {
		return ""
	}
	cachePath := filepath.Join(filepath.Dir(a.cfg.AgyBrainDir), "cache", "last_conversations.json")
	f, err := os.Open(cachePath)
	if err != nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(f, maxIndexEntrySize+1))
	f.Close()
	if err != nil || len(data) > maxIndexEntrySize {
		return ""
	}
	var m map[string]string
	if json.Unmarshal(data, &m) != nil {
		return ""
	}
	if id, ok := m[cwd]; ok && validSessionID(id) {
		return id
	}
	if id, ok := m[filepath.Clean(cwd)]; ok && validSessionID(id) {
		return id
	}
	return ""
}
