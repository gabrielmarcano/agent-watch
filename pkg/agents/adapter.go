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

// SessionRef is what herdr knows about the agent's session (already filtered by TrustedSession).
type SessionRef struct {
	Agent string // herdr agent id
	Kind  string // "id" | "path"
	Value string
	CWD   string // pane cwd; Claude uses it to build the project slug
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

// Config specifies host paths for reading agent transcripts.
type Config struct {
	ClaudeConfigDirs []string // expanded (no "~")
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

	// Default Claude config dirs if none provided
	if len(cfg.ClaudeConfigDirs) == 0 && home != "" {
		cfg.ClaudeConfigDirs = []string{filepath.Join(home, ".claude")}
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
