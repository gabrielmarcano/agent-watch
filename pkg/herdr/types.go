package herdr

import (
	"errors"
)

// ErrUnavailable indicates that herdr cannot be reached (e.g. socket does not exist or dial failed).
var ErrUnavailable = errors.New("herdr unavailable")

// AgentSession contains harness and session identifiers announced for an agent pane.
type AgentSession struct {
	Agent  string `json:"agent"`
	Kind   string `json:"kind"` // "id" | "path"
	Source string `json:"source"`
	Value  string `json:"value"`
}

// AgentInfo is herdr's view of an agent in a pane.
type AgentInfo struct {
	PaneID                string        `json:"pane_id"`
	WorkspaceID           string        `json:"workspace_id"`
	TabID                 string        `json:"tab_id"`
	Agent                 *string       `json:"agent"`
	AgentStatus           string        `json:"agent_status"`
	Name                  *string       `json:"name"`
	Focused               bool          `json:"focused"`
	CWD                   *string       `json:"cwd"`
	ForegroundCWD         *string       `json:"foreground_cwd"`
	TerminalTitleStripped *string       `json:"terminal_title_stripped"`
	StateChangeSeq        uint64        `json:"state_change_seq"`
	AgentSession          *AgentSession `json:"agent_session"`
}

// TrustedSession returns the session ref only when it belongs to the pane's current agent
// (herdr keeps stale refs after an agent is replaced). See herdr-socket-api.md §3.1.
func (a AgentInfo) TrustedSession() *AgentSession {
	if a.AgentSession == nil || a.Agent == nil {
		return nil
	}
	if a.AgentSession.Agent != *a.Agent {
		return nil
	}
	return a.AgentSession
}

// WorkspaceInfo represents a workspace returned by workspace.list.
type WorkspaceInfo struct {
	WorkspaceID string `json:"workspace_id"`
	Number      int    `json:"number"`
	Label       string `json:"label"`
}

// Pong is the response payload from herdr's "ping" method.
type Pong struct {
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
}

// ReadSource specifies which screen buffer to read from herdr.
type ReadSource string

const (
	SourceVisible         ReadSource = "visible"
	SourceRecent          ReadSource = "recent"
	SourceRecentUnwrapped ReadSource = "recent_unwrapped" // underscore! the CLI spelling fails on the socket
)

// Error is herdr's {"code","message"} error response; it implements error.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return "herdr " + e.Code + ": " + e.Message
}

// IsCode reports whether err is a herdr error with the specified code (uses errors.As).
func IsCode(err error, code string) bool {
	var he *Error
	if errors.As(err, &he) {
		return he.Code == code
	}
	return false
}
