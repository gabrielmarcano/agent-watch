package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Which Claude conversation does a pane show? (docs/reference/agents.md §3.3)
//
// herdr's agent_session can name another conversation than the one on screen:
// Claude Code's agents view switches conversations in a pane without telling
// herdr, and its background workers report to whichever pane they inherited.
// The pane's terminal title is the shown conversation's title, so the adapter
// checks herdr's transcript against it and, on a mismatch, looks the title up
// in Claude's session index (<config dir>/sessions/<pid>.json) of the profile
// that holds herdr's session. It never reads daemon/roster.json (it carries
// auth material) nor scans projects/ for a "latest" transcript.

// claudeUntitled is the terminal title of a conversation without a title yet.
const claudeUntitled = "Claude Code"

// claudeAgentsView reports whether a pane title is the one Claude Code shows
// in its agents view: "claude agents", or "<n> awaiting input · claude agents".
func claudeAgentsView(title string) bool {
	t := strings.TrimSpace(title)
	return t == "claude agents" || strings.HasSuffix(t, " · claude agents")
}

// ShowsConversation implements ViewDetector: false while the pane shows
// Claude Code's agents view.
func (c *claudeAdapter) ShowsConversation(title string) bool {
	return !claudeAgentsView(title)
}

// ConversationScreen implements ViewDetector: false for a capture of the
// agents view, recognised by its input box and key hints at the bottom of
// the screen. Only the last few lines count, so a reply that quotes them is
// still a conversation.
func (c *claudeAdapter) ConversationScreen(screen string) bool {
	lines := trimBlankTail(strings.Split(strings.ReplaceAll(screen, "\r\n", "\n"), "\n"))
	seen := 0
	for i := len(lines) - 1; i >= 0 && seen < 4; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		seen++
		if l == "❯ describe a task for a new session" ||
			strings.Contains(l, "· space to reply ·") ||
			strings.Contains(l, "ctrl+x to delete") ||
			l == "ctrl+x to confirm" {
			return false
		}
	}
	return true
}

// Bounds on the session index: entries read per profile and the size of one.
const (
	maxIndexEntries   = 256
	maxIndexEntrySize = 64 << 10
)

// claudeIndexEntry is the part of a session index entry the adapter uses.
type claudeIndexEntry struct {
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
}

// sessionPath returns the transcript of the conversation the pane shows. An
// error wraps ErrNoTranscript with the reason (ids only, never titles) when
// that conversation cannot be told for sure: the bridge then captures the
// screen, which shows the pane's conversation by construction.
func (c *claudeAdapter) sessionPath(ctx context.Context, ref SessionRef) (string, error) {
	path := c.resolvePath(ref)
	want := strings.TrimSpace(ref.Title)

	var herdrTitles claudeTitles
	if path != "" {
		t, err := c.transcriptTitles(ctx, path)
		if err != nil {
			return "", err
		}
		herdrTitles = t
		if want == "" || herdrTitles.has(want) {
			return path, nil
		}
	} else if want == "" {
		return "", ErrNoTranscript
	}

	cfgDir := c.profileOf(ref, path)
	if cfgDir == "" {
		return "", fmt.Errorf("%w: session %s is in no Claude profile", ErrNoTranscript, shortID(ref.Value))
	}
	matches, err := c.sessionsTitled(ctx, cfgDir, want)
	if err != nil {
		return "", err
	}
	if len(matches) > 1 {
		// Two conversations with the pane's title: the one in the pane's cwd.
		var here []string
		for p, cwd := range matches {
			if ref.CWD != "" && filepath.Clean(cwd) == filepath.Clean(ref.CWD) {
				here = append(here, p)
			}
		}
		if len(here) != 1 {
			return "", fmt.Errorf("%w: %d sessions in the index of session %s match the pane's title", ErrNoTranscript, len(matches), shortID(ref.Value))
		}
		return here[0], nil
	}
	for p := range matches {
		return p, nil
	}

	// No session in the index has the pane's title.
	switch {
	case path == "":
		return "", fmt.Errorf("%w: session %s has no transcript and no session in its index matches the pane's title", ErrNoTranscript, shortID(ref.Value))
	case want == claudeUntitled && herdrTitles.any():
		// The pane shows a conversation without a title, herdr names one with
		// a title: not the same one.
		return "", fmt.Errorf("%w: the pane shows an untitled conversation, session %s has a title", ErrNoTranscript, shortID(ref.Value))
	}
	// The title is none of Claude's (terminal titles turned off, a title set
	// by something else, or one that changed since herdr read it): nothing
	// contradicts herdr.
	return path, nil
}

// profileOf returns the Claude config dir that holds herdr's session: the one
// above its transcript (<dir>/projects/<slug>/<id>.jsonl), else the profile
// whose session index lists the session (a background worker that has not
// written a transcript yet). "" when neither does.
func (c *claudeAdapter) profileOf(ref SessionRef, path string) string {
	if path != "" {
		projects := filepath.Dir(filepath.Dir(path))
		if filepath.Base(projects) == "projects" {
			return filepath.Dir(projects)
		}
		return ""
	}
	if ref.Kind != "id" || !validSessionID(ref.Value) {
		return ""
	}
	for _, dir := range c.configDirs() {
		for _, e := range readSessionIndex(dir) {
			if e.SessionID == ref.Value {
				return dir
			}
		}
	}
	return ""
}

// sessionsTitled returns, by transcript path, the conversations in cfgDir's
// session index whose latest title is title, with the cwd of their entry. An
// entry is followed through its continued-in pointers, so a stale entry and
// the session that continues it count once.
func (c *claudeAdapter) sessionsTitled(ctx context.Context, cfgDir, title string) (map[string]string, error) {
	matches := map[string]string{}
	seen := map[string]bool{}
	for _, e := range readSessionIndex(cfgDir) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !validSessionID(e.SessionID) {
			continue
		}
		p := findSessionIn([]string{cfgDir}, e.SessionID, e.CWD)
		if p == "" {
			continue
		}
		p = c.followContinuation(p, e.CWD)
		if seen[p] {
			continue
		}
		seen[p] = true
		t, err := c.transcriptTitles(ctx, p)
		if err != nil {
			return nil, err
		}
		if t.has(title) {
			matches[p] = e.CWD
		}
	}
	return matches, nil
}

// readSessionIndex reads Claude's session index of a profile: one
// <pid>.json per running Claude process (interactive or background). Other
// files there (the <pid>.<hash>.key files) are never opened. Unreadable or
// oversized entries are skipped.
func readSessionIndex(cfgDir string) []claudeIndexEntry {
	dir := filepath.Join(cfgDir, "sessions")
	names, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []claudeIndexEntry
	for _, n := range names {
		if len(out) >= maxIndexEntries {
			break
		}
		name := n.Name()
		if !n.Type().IsRegular() || !isIndexName(name) {
			continue
		}
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, maxIndexEntrySize+1))
		f.Close()
		if err != nil || len(data) > maxIndexEntrySize {
			continue
		}
		var e claudeIndexEntry
		if json.Unmarshal(data, &e) == nil && e.SessionID != "" {
			out = append(out, e)
		}
	}
	return out
}

// isIndexName reports a session index entry's file name: digits + ".json".
func isIndexName(name string) bool {
	pid, ok := strings.CutSuffix(name, ".json")
	if !ok || pid == "" {
		return false
	}
	for _, r := range pid {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// claudeTitles holds a transcript's latest titles: aiTitle (the generated
// title) and agentName (a background session's name, the same text so far).
type claudeTitles struct{ ai, agent string }

func (t claudeTitles) any() bool { return t.ai != "" || t.agent != "" }

func (t claudeTitles) has(title string) bool {
	return title != "" && (strings.TrimSpace(t.ai) == title || strings.TrimSpace(t.agent) == title)
}

// transcriptTitles reads the latest ai-title and agent-name records of a
// transcript, growing the tail read like LastTurn: Claude writes them at
// every user message, so a long turn can put them megabytes before the end.
// A file it cannot read has no titles.
func (c *claudeAdapter) transcriptTitles(ctx context.Context, path string) (claudeTitles, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return claudeTitles{}, nil
	}
	for _, window := range c.tailWindows {
		if err := ctx.Err(); err != nil {
			return claudeTitles{}, err
		}
		content, err := TailFile(path, window)
		if err != nil {
			return claudeTitles{}, nil
		}
		t := lastTitles(content)
		if t.any() || fi.Size() <= window {
			return t, nil
		}
	}
	return claudeTitles{}, nil
}

func lastTitles(content string) claudeTitles {
	var t claudeTitles
	lines := strings.Split(content, "\n")
	for i := len(lines) - 1; i >= 0 && (t.ai == "" || t.agent == ""); i-- {
		l := lines[i]
		if !strings.Contains(l, `"ai-title"`) && !strings.Contains(l, `"agent-name"`) {
			continue
		}
		var row struct {
			Type      string `json:"type"`
			AITitle   string `json:"aiTitle"`
			AgentName string `json:"agentName"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(l)), &row) != nil {
			continue
		}
		switch {
		case row.Type == "ai-title" && t.ai == "":
			t.ai = row.AITitle
		case row.Type == "agent-name" && t.agent == "":
			t.agent = row.AgentName
		}
	}
	return t
}

// shortID is the first 8 characters of a session id, for logs.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
