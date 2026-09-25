package agents

import (
	"context"
	"encoding/json"
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
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
}

func (a *agyAdapter) LastTurn(ctx context.Context, ref SessionRef) (*model.HistoryItem, error) {
	path, sessionVal := a.resolvePath(ref)
	if path == "" {
		return nil, ErrNoTranscript
	}

	content, err := TailFile(path, 256*1024)
	if err != nil {
		return nil, ErrNoTranscript
	}

	lines := strings.Split(content, "\n")
	type parsedAgyStep struct {
		stepType  string
		content   string
		toolCalls bool
	}

	var steps []parsedAgyStep

	for _, l := range lines {
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

		steps = append(steps, parsedAgyStep{
			stepType:  row.Type,
			content:   row.Content,
			toolCalls: hasToolCalls,
		})
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

	query := steps[lastUserIdx].content

	// Response = content of the last PLANNER_RESPONSE after that query that has
	// non-empty content and no tool_calls.
	var response string
	for i := lastUserIdx + 1; i < len(steps); i++ {
		s := steps[i]
		if s.stepType == "PLANNER_RESPONSE" && !s.toolCalls && strings.TrimSpace(s.content) != "" {
			response = s.content
		}
	}

	response = strings.TrimSpace(response)
	if response == "" {
		return nil, ErrNoTranscript
	}

	response = model.TruncateUTF8(response, 16384)

	return &model.HistoryItem{
		ID:       sessionVal,
		Query:    query,
		Response: response,
		Source:   "transcript",
	}, nil
}

func (a *agyAdapter) resolvePath(ref SessionRef) (string, string) {
	if ref.Kind == "path" && ref.Value != "" {
		// If path is provided, extract conversation id from directory structure if possible
		sessionVal := ref.Value
		dir := filepath.Dir(ref.Value)
		if filepath.Base(dir) == "logs" {
			parent := filepath.Dir(dir)
			if filepath.Base(parent) == ".system_generated" {
				sessionVal = filepath.Base(filepath.Dir(parent))
			}
		}
		return ref.Value, sessionVal
	}

	if ref.Kind == "id" && ref.Value != "" {
		target := filepath.Join(a.cfg.AgyBrainDir, ref.Value, ".system_generated", "logs", "transcript_full.jsonl")
		if _, err := os.Stat(target); err == nil {
			return target, ref.Value
		}
	}

	return "", ""
}
