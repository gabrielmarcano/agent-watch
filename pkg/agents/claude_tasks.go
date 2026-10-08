package agents

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

// Claude writes no count of the shell commands and monitors it runs in the
// background (only pendingBackgroundAgentCount, for agents), so
// BackgroundTasks derives it from the transcript: the tasks started in the
// last backgroundTaskWindow bytes, minus those a later record ends
// (docs/reference/agents.md §3.4).

// backgroundTaskWindow is the transcript tail BackgroundTasks reads. Replayed
// on the owner's transcripts (2026-10-07), it gave the same counts as the
// whole file at every one of 17 391 turn ends; 4 MiB missed the start of a
// running task at 2.7% of the turn ends that had one.
const backgroundTaskWindow = 16 << 20

// taskCache remembers, per pane reference, the tasks found and the
// transcript's size and modification time they were read at.
type taskCache struct {
	mu      sync.Mutex
	entries map[SessionRef]taskEntry
}

type taskEntry struct {
	path  string
	size  int64
	mtime time.Time
	tasks map[string]bgTask
}

// bgTask is a background task still running at the end of the read.
type bgTask struct {
	monitor bool
	// deadline is when a monitor with a timeout stops on its own; zero for
	// shells and persistent monitors.
	deadline time.Time
}

// BackgroundTasks implements BackgroundTaskReader.
func (c *claudeAdapter) BackgroundTasks(ctx context.Context, ref SessionRef) (BackgroundTasks, error) {
	c.tasks.mu.Lock()
	cached, ok := c.tasks.entries[ref]
	c.tasks.mu.Unlock()
	if ok {
		if fi, err := os.Stat(cached.path); err == nil && fi.Size() == cached.size && fi.ModTime().Equal(cached.mtime) {
			return c.countTasks(cached.tasks), nil
		}
	}

	path, err := c.sessionPath(ctx, ref)
	if err != nil {
		return BackgroundTasks{}, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return BackgroundTasks{}, ErrNoTranscript
	}
	content, err := TailFile(path, c.taskWindow)
	if err != nil {
		return BackgroundTasks{}, ErrNoTranscript
	}
	tasks, err := runningTasks(ctx, content)
	if err != nil {
		return BackgroundTasks{}, err
	}

	c.tasks.mu.Lock()
	if c.tasks.entries == nil || len(c.tasks.entries) >= maxTurnEndCache {
		c.tasks.entries = map[SessionRef]taskEntry{}
	}
	c.tasks.entries[ref] = taskEntry{path: path, size: fi.Size(), mtime: fi.ModTime(), tasks: tasks}
	c.tasks.mu.Unlock()
	return c.countTasks(tasks), nil
}

// countTasks counts the tasks, leaving out monitors past their deadline.
func (c *claudeAdapter) countTasks(tasks map[string]bgTask) BackgroundTasks {
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	var n BackgroundTasks
	for _, t := range tasks {
		switch {
		case !t.monitor:
			n.Shells++
		case t.deadline.IsZero() || now.Before(t.deadline):
			n.Monitors++
		}
	}
	return n
}

// taskLine holds the fields of a transcript line that start or end a
// background task.
type taskLine struct {
	Type        string          `json:"type"`
	IsSidechain bool            `json:"isSidechain"`
	Timestamp   string          `json:"timestamp"`
	Content     json.RawMessage `json:"content"` // queue-operation
	Attachment  struct {
		Type   string `json:"type"`
		Prompt string `json:"prompt"`
	} `json:"attachment"`
	Message       claudeMessage `json:"message"`
	ToolUseResult *struct {
		BackgroundTaskID string `json:"backgroundTaskId"` // a Bash command in the background
		TaskID           string `json:"taskId"`           // a Monitor…
		TimeoutMs        *int64 `json:"timeoutMs"`        // …with its timeout
		Persistent       bool   `json:"persistent"`
		StoppedTaskID    string `json:"task_id"` // TaskStop
		Message          string `json:"message"`
	} `json:"toolUseResult"`
}

// runningTasks replays content (a transcript tail) and returns the
// background tasks it starts and does not end. Only the main chain counts: a
// sub-agent's tasks live in its own transcript.
func runningTasks(ctx context.Context, content string) (map[string]bgTask, error) {
	tasks := map[string]bgTask{}
	for len(content) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := content
		if i := strings.IndexByte(content, '\n'); i >= 0 {
			line, content = content[:i], content[i+1:]
		} else {
			content = ""
		}
		if !strings.Contains(line, "backgroundTaskId") && !strings.Contains(line, `"taskId"`) &&
			!strings.Contains(line, `"task_id"`) && !strings.Contains(line, "task-notification") {
			continue
		}
		var row taskLine
		if json.Unmarshal([]byte(line), &row) != nil || row.IsSidechain {
			continue
		}
		if r := row.ToolUseResult; r != nil && row.Type == "user" {
			switch {
			case r.BackgroundTaskID != "":
				tasks[r.BackgroundTaskID] = bgTask{}
			case r.TaskID != "" && r.TimeoutMs != nil:
				t := bgTask{monitor: true}
				if start, err := time.Parse(time.RFC3339, row.Timestamp); err == nil && !r.Persistent && *r.TimeoutMs > 0 {
					t.deadline = start.Add(time.Duration(*r.TimeoutMs) * time.Millisecond)
				}
				tasks[r.TaskID] = t
			case r.StoppedTaskID != "" && strings.HasPrefix(r.Message, "Successfully stopped"):
				delete(tasks, r.StoppedTaskID)
			}
		}
		for _, id := range endedTasks(notificationText(row)) {
			delete(tasks, id)
		}
	}
	return tasks, nil
}

// notificationText is the text of a line that can carry a task notification
// to Claude: a queued one (queue-operation, queued_command) or a user line
// whose content is a string. Tool output that quotes one is not.
func notificationText(row taskLine) string {
	switch row.Type {
	case "queue-operation":
		var s string
		if json.Unmarshal(row.Content, &s) == nil {
			return s
		}
	case "attachment":
		if row.Attachment.Type == "queued_command" {
			return row.Attachment.Prompt
		}
	case "user":
		var s string
		if json.Unmarshal(row.Message.Content, &s) == nil {
			return s
		}
	}
	return ""
}

// endedTasks returns the ids of the tasks the notifications in t end: those
// with a <status> (completed, failed, killed, stopped; one notification can
// list several ids), and monitors whose event says they expired.
func endedTasks(t string) []string {
	var ids []string
	for _, block := range strings.Split(t, taskNotificationTag)[1:] {
		block, _, _ = strings.Cut(block, "</task-notification>")
		_, event, hasEvent := strings.Cut(block, "<event>")
		if !strings.Contains(block, "<status>") && !(hasEvent && strings.HasPrefix(event, "[Monitor expired")) {
			continue
		}
		for rest := block; ; {
			_, after, ok := strings.Cut(rest, "<task-id>")
			if !ok {
				break
			}
			id, tail, _ := strings.Cut(after, "</task-id>")
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
			rest = tail
		}
	}
	return ids
}
