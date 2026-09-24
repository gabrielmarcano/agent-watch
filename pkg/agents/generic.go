package agents

import (
	"context"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

type genericAdapter struct{}

func newGenericAdapter() *genericAdapter {
	return &genericAdapter{}
}

func (g *genericAdapter) Name() string {
	return "generic"
}

func (g *genericAdapter) ParsePrompt(screen string) (Prompt, bool) {
	m, ok := findMenu(screen)
	if !ok {
		return Prompt{}, false
	}
	p := buildPrompt(m, digitKeys)
	return p, true
}

func (g *genericAdapter) CancelKeys() []string {
	return []string{"esc"}
}

func (g *genericAdapter) PromptWhileWorking() bool {
	return false
}

func (g *genericAdapter) LastTurn(ctx context.Context, ref SessionRef) (*model.HistoryItem, error) {
	return nil, ErrNoTranscript
}
