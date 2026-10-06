package agents

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// A Claude turn that ends while background agents run leaves herdr's pane
// `working` (docs/reference/agents.md §3.4), so no working → done transition
// ever triggers its history. Claude marks every turn end in the transcript
// with {"type":"system","subtype":"turn_duration",...} right after the final
// answer; LastCompletedTurn reads the turn that ends there, and the
// background agents that record says are still running.

// turnEndCache remembers, per pane reference, the last result and the
// transcript's size and modification time it was read at: a periodic check
// of an unchanged file reads nothing.
type turnEndCache struct {
	mu      sync.Mutex
	entries map[SessionRef]turnEndEntry
}

type turnEndEntry struct {
	path  string
	size  int64
	mtime time.Time
	end   TurnEnd
}

// maxTurnEndCache bounds the cache; it is cleared when full (one entry per
// working pane is the normal size).
const maxTurnEndCache = 64

// LastCompletedTurn implements TurnEndReader.
func (c *claudeAdapter) LastCompletedTurn(ctx context.Context, ref SessionRef) (TurnEnd, error) {
	c.turnEnds.mu.Lock()
	cached, ok := c.turnEnds.entries[ref]
	c.turnEnds.mu.Unlock()
	if ok {
		if fi, err := os.Stat(cached.path); err == nil && fi.Size() == cached.size && fi.ModTime().Equal(cached.mtime) {
			return cached.end, nil
		}
	}

	path, err := c.sessionPath(ctx, ref)
	if err != nil {
		return TurnEnd{}, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return TurnEnd{}, ErrNoTranscript
	}
	end, err := c.completedTurn(ctx, path, fi.Size())
	if err != nil {
		return TurnEnd{}, err
	}

	c.turnEnds.mu.Lock()
	if c.turnEnds.entries == nil || len(c.turnEnds.entries) >= maxTurnEndCache {
		c.turnEnds.entries = map[SessionRef]turnEndEntry{}
	}
	c.turnEnds.entries[ref] = turnEndEntry{path: path, size: fi.Size(), mtime: fi.ModTime(), end: end}
	c.turnEnds.mu.Unlock()
	return end, nil
}

// completedTurn reads the turn that ends at the last turn_duration record of
// path, growing the tail read like LastTurn until it holds that record and
// the turn's user message. The tail always reaches the end of the file, so
// what follows the record tells whether a newer turn has started.
func (c *claudeAdapter) completedTurn(ctx context.Context, path string, size int64) (TurnEnd, error) {
	for i, window := range c.tailWindows {
		more := size > window && i < len(c.tailWindows)-1
		content, err := TailFile(path, window)
		if err != nil {
			return TurnEnd{}, ErrNoTranscript
		}
		rec := lastTurnEnd(content)
		if rec.start < 0 {
			if more {
				continue
			}
			return TurnEnd{}, nil
		}
		turn, err := claudeLastTurn(ctx, content[:rec.start])
		if err != nil {
			return TurnEnd{}, err
		}
		if !turn.found && more {
			continue
		}
		end := TurnEnd{Marker: rec.marker}
		if !turnStarted(content[rec.start:]) {
			end.Background = rec.pending
		}
		if turn.response != "" {
			end.Item = &model.HistoryItem{
				Query:    turn.query,
				Response: model.TruncateUTF8(turn.response, model.MaxResponseBytes),
				Source:   "transcript",
			}
		}
		return end, nil
	}
	return TurnEnd{}, nil
}

// turnEndRecord is the last main-chain turn_duration line of a tail.
type turnEndRecord struct {
	start   int    // byte offset where the line starts; -1 when there is none
	marker  string // its uuid, else its timestamp
	pending int    // its pendingBackgroundAgentCount (absent: 0)
}

// lastTurnEnd finds the last main-chain turn_duration line of content.
func lastTurnEnd(content string) turnEndRecord {
	end := len(content)
	for end > 0 {
		start := strings.LastIndexByte(content[:end], '\n') + 1
		line := content[start:end]
		if strings.Contains(line, `"turn_duration"`) {
			var row struct {
				Type        string `json:"type"`
				Subtype     string `json:"subtype"`
				UUID        string `json:"uuid"`
				Timestamp   string `json:"timestamp"`
				IsSidechain bool   `json:"isSidechain"`
				Pending     int    `json:"pendingBackgroundAgentCount"`
			}
			if json.Unmarshal([]byte(strings.TrimSpace(line)), &row) == nil &&
				row.Type == "system" && row.Subtype == "turn_duration" && !row.IsSidechain {
				marker := row.UUID
				if marker == "" {
					marker = row.Timestamp
				}
				if marker != "" {
					return turnEndRecord{start: start, marker: marker, pending: max(row.Pending, 0)}
				}
			}
		}
		end = start - 1
	}
	return turnEndRecord{start: -1}
}

// turnStarted reports whether content (from a turn end to the end of the
// transcript) holds the start of a newer turn: any main-chain assistant
// line, or a user line with text that is not a command wrapper (a local
// command such as /model, `!` shell input and its output start no turn).
// Claude Code's own lines (isMeta: a background agent's report) start turns
// too.
func turnStarted(content string) bool {
	for _, l := range strings.Split(content, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || (!strings.Contains(l, `"user"`) && !strings.Contains(l, `"assistant"`)) {
			continue
		}
		var row claudeLine
		if json.Unmarshal([]byte(l), &row) != nil || row.IsSidechain {
			continue
		}
		switch row.Type {
		case "assistant":
			return true
		case "user":
			if userStartsTurn(row.Message) {
				return true
			}
		}
	}
	return false
}

// userStartsTurn reports whether a user line's message starts a turn.
func userStartsTurn(raw json.RawMessage) bool {
	var msg claudeMessage
	if json.Unmarshal(raw, &msg) != nil {
		return false
	}
	var text string
	if json.Unmarshal(msg.Content, &text) != nil {
		var blocks []claudeContentBlock
		if json.Unmarshal(msg.Content, &blocks) != nil {
			return false
		}
		for _, b := range blocks {
			if b.Type == "tool_result" {
				return true // only inside a turn
			}
			if b.Type == "text" && text == "" {
				text = b.Text
			}
		}
	}
	t := strings.TrimSpace(text)
	return t != "" && !isCommandWrapper(t)
}
