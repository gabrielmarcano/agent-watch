package herdr

import (
	"path/filepath"
	"strings"
)

// Terminal is a terminal entry returned by herdr-core's terminal.list,
// terminal.get, and terminal.run methods.
type Terminal struct {
	Address      string            `json:"address"`
	Name         *string           `json:"name,omitempty"`
	Title        string            `json:"title,omitempty"`
	State        string            `json:"state,omitempty"` // "live" | "finished"
	CWD          string            `json:"cwd,omitempty"`
	Argv         []string          `json:"argv,omitempty"`
	Foreground   *Foreground       `json:"foreground,omitempty"`
	Status       *TerminalStatus   `json:"status,omitempty"`
	StatusMS     *uint64           `json:"status_ms,omitempty"`
	Turn         uint64            `json:"turn"`
	Reported     bool              `json:"reported"`
	Parent       *string           `json:"parent,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	AgentSession *AgentSession     `json:"agent_session,omitempty"` // optional test fixture override
	Focused      bool              `json:"focused,omitempty"`
	Subagents    *int              `json:"subagent_count,omitempty"`
	BgShells     *int              `json:"background_shell_count,omitempty"`
	BgMonitors   *int              `json:"background_monitor_count,omitempty"`
}

// Foreground describes the foreground process group of a live terminal.
type Foreground struct {
	PID  int      `json:"pid"`
	Name string   `json:"name"`
	Argv []string `json:"argv"`
	CWD  string   `json:"cwd"`
}

// TerminalStatus is the OSC 7501 status report attached to a terminal.
type TerminalStatus struct {
	State    string  `json:"state"`              // "idle" | "working" | "done" | "blocked" | "error"
	App      string  `json:"app,omitempty"`      // e.g. "claude-code", "antigravity", "opencode"
	Kind     *string `json:"kind,omitempty"`     // "permission" | "question" | "auth"
	Msg      string  `json:"msg,omitempty"`      // human-readable status or blocker detail
	Title    string  `json:"title,omitempty"`    // status title
	Progress *uint8  `json:"progress,omitempty"` // 0-100
}

// RunParams are the parameters for herdr-core's terminal.run method.
type RunParams struct {
	Argv     []string          `json:"argv"`
	CWD      string            `json:"cwd,omitempty"`
	Name     string            `json:"name,omitempty"`
	Keep     bool              `json:"keep,omitempty"`
	NoParent bool              `json:"no_parent,omitempty"`
	NoNotify bool              `json:"no_notify,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
}

// AgentInfo represents a live agent terminal normalized from herdr-core's
// terminal.list response.
type AgentInfo struct {
	PaneID                 string        `json:"pane_id"`
	WorkspaceID            string        `json:"workspace_id"`
	WorkspaceLabel         string        `json:"workspace_label,omitempty"`
	TabID                  string        `json:"tab_id"`
	TabLabel               string        `json:"tab_label,omitempty"`
	Agent                  *string       `json:"agent"`
	AgentStatus            string        `json:"agent_status"`
	StatusKind             *string       `json:"status_kind,omitempty"`
	StatusMsg              string        `json:"status_msg,omitempty"`
	StatusTitle            string        `json:"status_title,omitempty"`
	AgentSession           *AgentSession `json:"agent_session"`
	Name                   *string       `json:"name"`
	Title                  *string       `json:"title"`
	CWD                    *string       `json:"cwd"`
	ForegroundPID          int           `json:"foreground_pid,omitempty"`
	ForegroundArgv         []string      `json:"foreground_argv,omitempty"`
	Argv                   []string      `json:"argv,omitempty"`
	Parent                 *string       `json:"parent,omitempty"`
	Focused                bool          `json:"focused"`
	StateChangeSeq         uint64        `json:"state_change_seq"`
	SubagentCount          *int          `json:"subagent_count"`
	BackgroundShellCount   *int          `json:"background_shell_count"`
	BackgroundMonitorCount *int          `json:"background_monitor_count"`
}

// AgentSession represents the session reference for an agent.
type AgentSession struct {
	Agent  string `json:"agent"`
	Kind   string `json:"kind"` // "id" or "path"
	Value  string `json:"value"`
	Source string `json:"source"`
}

// TrustedSession returns the AgentSession if and only if both Agent and
// AgentSession are non-nil and AgentSession.Agent matches *Agent.
func (a AgentInfo) TrustedSession() *AgentSession {
	if a.Agent == nil || a.AgentSession == nil {
		return nil
	}
	if a.AgentSession.Agent != *a.Agent {
		return nil
	}
	return a.AgentSession
}

// Pong represents the version handshake returned by herdr-core's hello method.
type Pong struct {
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
}

// ReadSource specifies how much of a terminal's buffer to read.
type ReadSource string

const (
	// SourceVisible reads only the currently visible screen (lines: 0).
	SourceVisible ReadSource = "visible"
	// SourceRecentUnwrapped reads the screen plus recent scrollback history.
	SourceRecentUnwrapped ReadSource = "recent_unwrapped"
)

// ToAgentInfo converts a herdr-core Terminal into an AgentInfo.
func (t Terminal) ToAgentInfo() AgentInfo {
	info := AgentInfo{
		PaneID:                 t.Address,
		Name:                   t.Name,
		Parent:                 t.Parent,
		Focused:                t.Focused,
		SubagentCount:          t.Subagents,
		BackgroundShellCount:   t.BgShells,
		BackgroundMonitorCount: t.BgMonitors,
		Argv:                   append([]string(nil), t.Argv...),
	}
	if t.Title != "" {
		title := t.Title
		info.Title = &title
	}
	if t.Labels != nil {
		info.WorkspaceID = t.Labels["herdr.space"]
		info.TabID = t.Labels["herdr.tab"]
		if where := strings.TrimSpace(t.Labels["where"]); where != "" {
			if ws, tab, ok := strings.Cut(where, " / "); ok {
				info.WorkspaceLabel = strings.TrimSpace(ws)
				info.TabLabel = strings.TrimSpace(tab)
			} else {
				info.WorkspaceLabel = where
			}
		}
		if info.WorkspaceLabel == "" {
			info.WorkspaceLabel = strings.TrimSpace(t.Labels["herdr.workspace_label"])
		}
		if info.TabLabel == "" {
			info.TabLabel = strings.TrimSpace(t.Labels["herdr.tab_label"])
		}
	}

	if t.Foreground != nil {
		info.ForegroundPID = t.Foreground.PID
		info.ForegroundArgv = append([]string(nil), t.Foreground.Argv...)
		if t.Foreground.CWD != "" {
			cwd := t.Foreground.CWD
			info.CWD = &cwd
		}
	}
	if info.CWD == nil && t.CWD != "" {
		cwd := t.CWD
		info.CWD = &cwd
	}

	if agent := detectAgent(t); agent != "" {
		info.Agent = &agent
	}

	if t.Status != nil {
		info.StatusKind = t.Status.Kind
		info.StatusMsg = t.Status.Msg
		info.StatusTitle = t.Status.Title
		if t.Status.State == "error" {
			info.AgentStatus = "unknown"
		} else {
			info.AgentStatus = t.Status.State
		}
	} else if info.Agent != nil {
		info.AgentStatus = "idle"
	} else {
		info.AgentStatus = "unknown"
	}

	if t.StatusMS != nil && *t.StatusMS > 0 {
		info.StateChangeSeq = *t.StatusMS + t.Turn
	} else {
		info.StateChangeSeq = t.Turn
	}

	if t.AgentSession != nil {
		info.AgentSession = t.AgentSession
	} else if info.Agent != nil {
		if sid := extractResumeSessionID(info.ForegroundArgv); sid != "" {
			info.AgentSession = &AgentSession{
				Agent:  *info.Agent,
				Kind:   "id",
				Value:  sid,
				Source: "argv",
			}
		} else if sid := extractResumeSessionID(info.Argv); sid != "" {
			info.AgentSession = &AgentSession{
				Agent:  *info.Agent,
				Kind:   "id",
				Value:  sid,
				Source: "argv",
			}
		}
	}

	return info
}

func detectAgent(t Terminal) string {
	if t.Status != nil {
		if name := normalizeAgentName(t.Status.App); name != "" {
			return name
		}
	}
	if t.Foreground != nil {
		if name := agentFromArgv(t.Foreground.Argv); name != "" {
			return name
		}
		if name := normalizeAgentName(t.Foreground.Name); name != "" {
			return name
		}
	}
	return agentFromArgv(t.Argv)
}

func agentFromArgv(argv []string) string {
	for _, arg := range argv {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" || trimmed == "env" || strings.HasPrefix(trimmed, "-") {
			continue
		}
		if strings.Contains(trimmed, "=") && !strings.HasPrefix(trimmed, "/") {
			continue
		}
		return normalizeAgentName(trimmed)
	}
	return ""
}

func normalizeAgentName(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	s = filepath.Base(s)
	switch s {
	case "claude-code", "claude":
		return "claude"
	case "antigravity", "agy":
		return "agy"
	case "opencode":
		return "opencode"
	case "", ".", "/", "zsh", "bash", "sh", "fish", "dash", "ksh", "tcsh", "csh", "tmux", "hc", "herdr-core", "env", "login", "sleep":
		return ""
	}
	if s[0] >= '0' && s[0] <= '9' {
		return ""
	}
	return s
}

func extractResumeSessionID(argv []string) string {
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if (arg == "--resume" || arg == "-r" || arg == "--session-id") && i+1 < len(argv) {
			val := strings.TrimSpace(argv[i+1])
			if val != "" && !strings.HasPrefix(val, "-") {
				return val
			}
		}
		for _, prefix := range []string{"--resume=", "--session-id="} {
			if strings.HasPrefix(arg, prefix) {
				val := strings.TrimSpace(strings.TrimPrefix(arg, prefix))
				if val != "" {
					return val
				}
			}
		}
	}
	return ""
}
