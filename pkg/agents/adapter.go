package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// ErrNoTranscript is returned when an agent transcript cannot be found, read, or parsed.
// The bridge uses this to fall back to screen-capture history (ScreenTurn).
var ErrNoTranscript = errors.New("no transcript available")

// ErrNoReply is returned when the pane's last turn has no reply to publish
// yet (it waits on a question, for instance). The bridge then publishes
// nothing, not even a screen capture: the screen would show no reply either.
var ErrNoReply = errors.New("no reply yet")

// SessionRef is what herdr knows about the agent's session (already filtered by TrustedSession).
type SessionRef struct {
	Agent string // herdr agent id
	Kind  string // "id" | "path"
	Value string
	CWD   string // pane cwd; Claude uses it to build the project slug
	// Title is the pane's terminal title (herdr's terminal_title_stripped).
	// Claude uses it to check that the session is the one the pane shows.
	Title string
}

// TurnEndReader is implemented by adapters that can tell, from the agent's
// own transcript, that a turn ended while herdr still reports the pane
// working (Claude with background agents running: herdr then never reports
// done). The bridge asks it periodically, only for panes herdr reports
// working.
type TurnEndReader interface {
	// LastCompletedTurn returns the last turn that has ended (TurnEnd). A
	// turn still being written is never returned. Reads are bounded and
	// cheap when nothing changed.
	LastCompletedTurn(ctx context.Context, ref SessionRef) (TurnEnd, error)
}

// TurnEnd is what a TurnEndReader found at the last turn end.
type TurnEnd struct {
	// Item is that turn's reply; nil when it has none.
	Item *model.HistoryItem
	// Marker changes whenever a newer turn ends; "" while none has.
	Marker string
	// Background counts the background agents that turn left running, while
	// the agent only waits on them: 0 once a newer turn has started (the
	// agent is generating again) or when none run.
	Background int
}

// BackgroundTaskReader is implemented by adapters that can count, from the
// agent's own transcript, the shell commands and monitors the agent still
// runs in the background (Claude). The bridge asks it on each working →
// done|idle transition and in the periodic check while the pane is working.
type BackgroundTaskReader interface {
	// BackgroundTasks counts the tasks still running. Reads are bounded and
	// cheap when nothing changed.
	BackgroundTasks(ctx context.Context, ref SessionRef) (BackgroundTasks, error)
}

// BackgroundTasks is what a BackgroundTaskReader counted.
type BackgroundTasks struct {
	Shells   int
	Monitors int
}

// ViewDetector is implemented by adapters whose TUI can fill the pane with
// something other than a conversation (Claude Code's agents view). While a
// pane shows such a view, the bridge publishes no history for it, and it
// captures the pane's last reply again when the pane is back on a
// conversation.
type ViewDetector interface {
	// ShowsConversation reports whether a pane with this terminal title
	// shows a conversation. Unknown titles count as a conversation.
	ShowsConversation(title string) bool
	// ConversationScreen reports whether a screen capture shows a
	// conversation, the same way.
	ConversationScreen(screen string) bool
}

// Prompt = public model + private key map. Keys never leave the Mac.
type Prompt struct {
	Public     model.PendingPrompt
	Keys       map[string][]string // option id → keys
	CancelKeys []string            // optional per-prompt override (e.g. Antigravity edit prompt quirk)
}

// EffectiveCancelKeys returns the prompt's CancelKeys override if non-empty,
// or falls back to the adapter's CancelKeys().
func (p Prompt) EffectiveCancelKeys(a Adapter) []string {
	if len(p.CancelKeys) > 0 {
		return p.CancelKeys
	}
	return a.CancelKeys()
}

// Adapter defines the agent-specific capabilities for a coding agent.
type Adapter interface {
	Name() string
	// ParsePrompt returns the menu of the dialog open on the visible screen.
	// ok is false when no dialog is open, even if the screen shows a numbered
	// list: the bridge also uses it to refuse dictation while a menu is up.
	ParsePrompt(screen string) (Prompt, bool)
	CancelKeys() []string
	PromptWhileWorking() bool
	// LastTurn fills Query, Response and Source="transcript" only. ID, PaneID,
	// Agent, Label and CompletedAt belong to the caller, which computes ID
	// with model.HistoryID using ref.Value as the session value.
	LastTurn(ctx context.Context, ref SessionRef) (*model.HistoryItem, error)
}

// FocusGuard is implemented by adapters whose answer keys act on whichever
// button has focus, which a text screen cannot show (OpenCode's button bar:
// Enter presses the focused button). The bridge consults it right before
// sending an answer; adapters whose keys pick an option by itself (digits)
// do not implement it.
type FocusGuard interface {
	// FocusDependent reports whether the keys for optionID in p act on the
	// focused button, so that the bridge must read the screen in herdr's
	// "ansi" format and call CheckFocus before sending them. It is true for
	// an option that is not in p (CheckFocus then refuses it).
	FocusDependent(p Prompt, optionID string) bool
	// CheckFocus returns nil when the keys for optionID in p do not depend on
	// focus, or when ansiScreen (the visible screen, format "ansi") shows p's
	// dialog with the focus on the button those keys assume. Anything else,
	// including a screen it cannot read with certainty, is an error: the
	// caller must then send nothing.
	CheckFocus(ansiScreen string, p Prompt, optionID string) error
}

// Config specifies host paths for reading agent transcripts.
type Config struct {
	// Home is where Claude profiles are discovered (~/.claude, ~/.claude-*);
	// NewRegistry defaults it to the user's home, "" discovers none.
	Home             string
	ClaudeConfigDirs []string // extra Claude profiles, searched first; expanded (no "~")
	OpenCodeDBPath   string   // default ~/.local/share/opencode/opencode.db
	AgyBrainDir      string   // default ~/.gemini/antigravity-cli/brain
}

// Registry maps agent names to adapters, falling back to genericAdapter.
type Registry struct {
	adapters map[string]Adapter
	generic  Adapter
}

// NewRegistry initializes an agent registry with configured paths and default adapters.
func NewRegistry(cfg Config) *Registry {
	home, _ := os.UserHomeDir()
	if cfg.Home == "" {
		cfg.Home = home
	}
	// Default OpenCode DB path if not provided
	if cfg.OpenCodeDBPath == "" && home != "" {
		cfg.OpenCodeDBPath = filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	}
	// Default Antigravity brain dir if not provided
	if cfg.AgyBrainDir == "" && home != "" {
		cfg.AgyBrainDir = filepath.Join(home, ".gemini", "antigravity-cli", "brain")
	}

	gen := newGenericAdapter()
	return &Registry{
		adapters: map[string]Adapter{
			"claude":   newClaudeAdapter(cfg),
			"agy":      newAgyAdapter(cfg),
			"opencode": newOpenCodeAdapter(cfg),
		},
		generic: gen,
	}
}

// For returns the adapter for the named agent, or the generic adapter if unknown.
func (r *Registry) For(agent string) Adapter {
	if r == nil {
		return newGenericAdapter()
	}
	if a, ok := r.adapters[agent]; ok {
		return a
	}
	return r.generic
}

// UnknownPrompt builds the Kind=unknown prompt with RawTail (last 12 non-empty lines, right-trimmed).
func UnknownPrompt(screen string) Prompt {
	lines := strings.Split(strings.ReplaceAll(screen, "\r\n", "\n"), "\n")
	var nonEmpty []string
	for _, l := range lines {
		trimmed := strings.TrimRight(l, " \t")
		if trimmed != "" {
			nonEmpty = append(nonEmpty, trimmed)
		}
	}

	start := 0
	if len(nonEmpty) > 12 {
		start = len(nonEmpty) - 12
	}
	rawTail := strings.Join(nonEmpty[start:], "\n")

	return Prompt{
		Public: model.PendingPrompt{
			Kind:        model.PromptUnknown,
			Title:       "",
			Detail:      "",
			Options:     []model.PromptOption{},
			Fingerprint: model.Fingerprint(model.PromptUnknown, "", "", nil),
			RawTail:     rawTail,
		},
		Keys: make(map[string][]string),
	}
}
