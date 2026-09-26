package model

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// AgentStatus represents the lifecycle state of an agent in a herdr pane.
// Herdr's enum, copied verbatim: idle, working, blocked, done, unknown.
type AgentStatus string

const (
	StatusIdle    AgentStatus = "idle"
	StatusWorking AgentStatus = "working"
	StatusBlocked AgentStatus = "blocked"
	StatusDone    AgentStatus = "done"
	StatusUnknown AgentStatus = "unknown"
)

// Severity returns a sort weight: blocked=4, done=3, working=2, idle=1, unknown=0.
func (s AgentStatus) Severity() int {
	switch s {
	case StatusBlocked:
		return 4
	case StatusDone:
		return 3
	case StatusWorking:
		return 2
	case StatusIdle:
		return 1
	default:
		return 0
	}
}

// PromptKind classifies the kind of pending prompt.
type PromptKind string

const (
	PromptPermission PromptKind = "permission" // allow/deny style menu
	PromptQuestion   PromptKind = "question"   // multiple choice (AskUserQuestion, plan approval, …)
	PromptUnknown    PromptKind = "unknown"    // could not parse; RawTail is set
)

// OptionRole defines the semantic role of a prompt option.
type OptionRole string

const (
	RoleAllowOnce   OptionRole = "allow_once"
	RoleAllowAlways OptionRole = "allow_always"
	RoleDeny        OptionRole = "deny"
	RoleChoice      OptionRole = "choice"
)

// PromptOption is a single selectable choice in an approval or question prompt.
// Label is the option's first line; Description holds the lines printed under
// it (Claude and OpenCode questions describe each answer), if any.
type PromptOption struct {
	ID          string     `json:"id"`
	Label       string     `json:"label"`
	Description string     `json:"description,omitempty"`
	Role        OptionRole `json:"role"`
}

// Text is the option's full text: Label, then a space and Description when
// there is one. Roles are derived from it, and the fingerprint hashes it.
func (o PromptOption) Text() string {
	if o.Description == "" {
		return o.Label
	}
	return o.Label + " " + o.Description
}

// PendingPrompt is the parsed prompt awaiting user interaction on a blocked agent.
type PendingPrompt struct {
	Kind        PromptKind     `json:"kind"`
	Title       string         `json:"title"`
	Detail      string         `json:"detail,omitempty"`
	Options     []PromptOption `json:"options"`
	Fingerprint string         `json:"fingerprint"`
	RawTail     string         `json:"raw_tail,omitempty"`
}

// AgentState represents the state of a single agent pane.
type AgentState struct {
	PaneID         string         `json:"pane_id"`
	Agent          string         `json:"agent"`
	Label          string         `json:"label"`
	Name           string         `json:"name,omitempty"`
	CWD            string         `json:"cwd,omitempty"`
	WorkspaceID    string         `json:"workspace_id"`
	Workspace      string         `json:"workspace,omitempty"`
	Status         AgentStatus    `json:"status"`
	Focused        bool           `json:"focused"`
	StateChangeSeq uint64         `json:"state_change_seq"`
	Prompt         *PendingPrompt `json:"prompt,omitempty"`
	UpdatedAt      string         `json:"updated_at"`
}

// HistoryItem represents a single completed query-response turn for a pane.
type HistoryItem struct {
	ID          string `json:"id"`
	PaneID      string `json:"pane_id"`
	Agent       string `json:"agent"`
	Label       string `json:"label"`
	Query       string `json:"query,omitempty"`
	Response    string `json:"response"`
	Source      string `json:"source"` // "transcript" | "screen"
	CompletedAt string `json:"completed_at"`
}

// AgentsSnapshot is the full state snapshot sent to clients on connect.
type AgentsSnapshot struct {
	HostOnline  bool         `json:"host_online"`
	HerdrOnline bool         `json:"herdr_online"`
	Agents      []AgentState `json:"agents"`
	GeneratedAt string       `json:"generated_at"`
}

// SortAgents orders agents by Severity desc, then Label asc. Sorts in place.
func SortAgents(agents []AgentState) {
	sort.Slice(agents, func(i, j int) bool {
		sevI := agents[i].Status.Severity()
		sevJ := agents[j].Status.Severity()
		if sevI != sevJ {
			return sevI > sevJ
		}
		return agents[i].Label < agents[j].Label
	})
}

// Fingerprint implements contracts.md §1.3:
// first 16 hex characters of sha256(kind + "\n" + title + "\n" + detail + "\n" + text_1 + "\n" + … + text_n),
// where text_i is option i's full text (PromptOption.Text).
func Fingerprint(kind PromptKind, title, detail string, labels []string) string {
	var b strings.Builder
	b.WriteString(string(kind))
	b.WriteByte('\n')
	b.WriteString(title)
	b.WriteByte('\n')
	b.WriteString(detail)
	for _, l := range labels {
		b.WriteByte('\n')
		b.WriteString(l)
	}
	h := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(h[:8])
}

// HistoryID implements contracts.md §1.4:
// first 16 hex characters of sha256(pane_id + "\n" + session_value + "\n" + query + "\n" + response).
func HistoryID(paneID, sessionValue, query, response string) string {
	payload := paneID + "\n" + sessionValue + "\n" + query + "\n" + response
	h := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(h[:8])
}

// TruncateUTF8 cuts s to at most max bytes on a rune boundary and appends "\n\n…[truncated]" when cut.
func TruncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 0 {
		return "\n\n…[truncated]"
	}

	// Find the last valid rune boundary <= max
	cut := 0
	for i := range s {
		if i > max {
			break
		}
		cut = i
	}

	// In case the rune at cut fits within max
	if cut < len(s) {
		_, size := utf8.DecodeRuneInString(s[cut:])
		if cut+size <= max {
			cut += size
		}
	}

	return s[:cut] + "\n\n…[truncated]"
}

// Now returns the current UTC time in RFC 3339 (the only timestamp format allowed).
func Now() string {
	return time.Now().UTC().Format(time.RFC3339)
}
