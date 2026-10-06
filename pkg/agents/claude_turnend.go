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
// answer; LastCompletedTurn reads the turn that ends there.

// turnEndCache remembers, per pane reference, the last result and the
// transcript's size and modification time it was read at: a periodic check
// of an unchanged file reads nothing.
type turnEndCache struct {
	mu      sync.Mutex
	entries map[SessionRef]turnEndEntry
}

type turnEndEntry struct {
	path   string
	size   int64
	mtime  time.Time
	item   *model.HistoryItem
	marker string
}

// maxTurnEndCache bounds the cache; it is cleared when full (one entry per
// working pane is the normal size).
const maxTurnEndCache = 64

// LastCompletedTurn implements TurnEndReader.
func (c *claudeAdapter) LastCompletedTurn(ctx context.Context, ref SessionRef) (*model.HistoryItem, string, error) {
	c.turnEnds.mu.Lock()
	cached, ok := c.turnEnds.entries[ref]
	c.turnEnds.mu.Unlock()
	if ok {
		if fi, err := os.Stat(cached.path); err == nil && fi.Size() == cached.size && fi.ModTime().Equal(cached.mtime) {
			return cached.item, cached.marker, nil
		}
	}

	path, err := c.sessionPath(ctx, ref)
	if err != nil {
		return nil, "", err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, "", ErrNoTranscript
	}
	item, marker, err := c.completedTurn(ctx, path, fi.Size())
	if err != nil {
		return nil, "", err
	}

	c.turnEnds.mu.Lock()
	if c.turnEnds.entries == nil || len(c.turnEnds.entries) >= maxTurnEndCache {
		c.turnEnds.entries = map[SessionRef]turnEndEntry{}
	}
	c.turnEnds.entries[ref] = turnEndEntry{path: path, size: fi.Size(), mtime: fi.ModTime(), item: item, marker: marker}
	c.turnEnds.mu.Unlock()
	return item, marker, nil
}

// completedTurn reads the turn that ends at the last turn_duration record of
// path, growing the tail read like LastTurn until it holds that record and
// the turn's user message.
func (c *claudeAdapter) completedTurn(ctx context.Context, path string, size int64) (*model.HistoryItem, string, error) {
	for i, window := range c.tailWindows {
		more := size > window && i < len(c.tailWindows)-1
		content, err := TailFile(path, window)
		if err != nil {
			return nil, "", ErrNoTranscript
		}
		end, marker := lastTurnEnd(content)
		if end < 0 {
			if more {
				continue
			}
			return nil, "", nil
		}
		turn, err := claudeLastTurn(ctx, content[:end])
		if err != nil {
			return nil, "", err
		}
		if !turn.found && more {
			continue
		}
		if turn.response == "" {
			return nil, marker, nil
		}
		return &model.HistoryItem{
			Query:    turn.query,
			Response: model.TruncateUTF8(turn.response, model.MaxResponseBytes),
			Source:   "transcript",
		}, marker, nil
	}
	return nil, "", nil
}

// lastTurnEnd returns the byte offset where the last main-chain
// turn_duration line of content starts, and a marker for it (its uuid, else
// its timestamp); -1 when there is none.
func lastTurnEnd(content string) (int, string) {
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
			}
			if json.Unmarshal([]byte(strings.TrimSpace(line)), &row) == nil &&
				row.Type == "system" && row.Subtype == "turn_duration" && !row.IsSidechain {
				marker := row.UUID
				if marker == "" {
					marker = row.Timestamp
				}
				if marker != "" {
					return start, marker
				}
			}
		}
		end = start - 1
	}
	return -1, ""
}
