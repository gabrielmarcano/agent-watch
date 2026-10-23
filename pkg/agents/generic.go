package agents

import (
	"context"
	"strings"

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

func (g *genericAdapter) TaskTitle(rawTitle string) string {
	return strings.TrimSpace(rawTitle)
}

func (g *genericAdapter) ShowsConversation(title string) bool {
	return true
}

func (g *genericAdapter) ScreenTurn(screen string) *model.HistoryItem {
	return ScreenTurn(screen)
}

func (g *genericAdapter) LastTurn(ctx context.Context, ref SessionRef) (*model.HistoryItem, error) {
	return nil, ErrNoTranscript
}

func (g *genericAdapter) RecentTurns(ctx context.Context, ref SessionRef, max int) ([]model.HistoryItem, error) {
	return nil, ErrNoTranscript
}

func (g *genericAdapter) ObserveWorkingTurn(ctx context.Context, ref SessionRef) (TurnEnd, BackgroundTasks, bool, error) {
	return TurnEnd{}, BackgroundTasks{}, false, nil
}

func (g *genericAdapter) ObserveSettledTurn(ctx context.Context, ref SessionRef) (*model.HistoryItem, BackgroundTasks, error) {
	return nil, BackgroundTasks{}, ErrNoTranscript
}
