package bridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/agents"
	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// commandTimeout bounds one command end to end (list, read, send). It stays
// below the relay's 10 s wait so the watch gets our answer, not a timeout.
const commandTimeout = 9 * time.Second

// HandleRelayMessage processes inbound messages from the relay.
func (e *Engine) HandleRelayMessage(msg any) {
	switch m := msg.(type) {
	case model.CommandMsg:
		go e.executeCommand(m)
	case *model.CommandMsg:
		go e.executeCommand(*m)
	case model.ResyncMsg, *model.ResyncMsg:
		if e.Relay != nil {
			msgs := e.ConnectMessages(context.Background())
			for _, initMsg := range msgs {
				e.Relay.Send(initMsg)
			}
		}
	default:
		// Forward compatibility: ignore unknown messages
	}
}

func (e *Engine) executeCommand(cmd model.CommandMsg) {
	unlock := e.lockPane(cmd.PaneID)
	defer unlock()

	reply := func(ok bool, code, errMsg, agentName string) {
		textLen := len(cmd.Text)
		if e.Logger != nil {
			if ok {
				e.Logger.Info("bridge executed command successfully",
					"action", cmd.Action,
					"pane_id", cmd.PaneID,
					"agent", agentName,
					"text_len", textLen,
				)
			} else {
				e.Logger.Info("bridge command rejected or failed",
					"action", cmd.Action,
					"pane_id", cmd.PaneID,
					"agent", agentName,
					"code", code,
					"error", errMsg,
					"text_len", textLen,
				)
			}
		}

		if e.Relay != nil {
			e.Relay.Send(model.CommandResultMsg{
				Type:      model.WireCommandResult,
				RequestID: cmd.RequestID,
				OK:        ok,
				ErrorCode: code,
				Message:   errMsg,
			})
		}
	}

	// Checked under the pane lock, so a replay waits for the original to finish.
	if e.requests.observe(cmd.RequestID) {
		reply(false, "stale_state", "duplicate request_id", "")
		return
	}

	if !e.isHerdrOnline() {
		reply(false, "herdr_offline", "herdr is offline", "")
		return
	}

	// One budget for the whole command, below the relay's 10 s wait; each herdr
	// call gets its own 3 s slice of it.
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	if e.Syncer == nil {
		reply(false, "herdr_offline", "syncer is nil", "")
		return
	}

	listCtx, listCancel := context.WithTimeout(ctx, 3*time.Second)
	agentsList, err := e.Syncer.Refresh(listCtx)
	listCancel()
	if err != nil {
		if errors.Is(err, herdr.ErrUnavailable) {
			reply(false, "herdr_offline", err.Error(), "")
			return
		}
		reply(false, "herdr_offline", fmt.Sprintf("herdr refresh: %v", err), "")
		return
	}

	var info *herdr.AgentInfo
	for i := range agentsList {
		if agentsList[i].PaneID == cmd.PaneID {
			info = &agentsList[i]
			break
		}
	}

	if info == nil || info.Agent == nil || *info.Agent == "" {
		reply(false, "unknown_pane", "pane not found or no agent detected", "")
		return
	}

	e.pruneConsumed(agentsList)

	agentName := *info.Agent
	if info.StateChangeSeq != cmd.ExpectedSeq {
		reply(false, "stale_state", fmt.Sprintf("state sequence mismatch: expected %d, got %d", cmd.ExpectedSeq, info.StateChangeSeq), agentName)
		return
	}

	ad := e.Agents.For(agentName)
	status := model.AgentStatus(info.AgentStatus)

	switch cmd.Action {
	case "prompt":
		trimmed := strings.TrimSpace(cmd.Text)
		if trimmed == "" || len(cmd.Text) > 4000 {
			reply(false, "invalid_request", "prompt text must be 1-4000 characters", agentName)
			return
		}

		// Allowlist: anything herdr reports that is not known to be safe
		// (including "" or a future value) is refused.
		switch status {
		case model.StatusIdle, model.StatusDone:
		case model.StatusWorking:
			if !ad.PromptWhileWorking() {
				reply(false, "agent_busy", "agent cannot queue prompts while working", agentName)
				return
			}
		case model.StatusBlocked:
			reply(false, "agent_blocked", "cannot prompt blocked agent; use answer or cancel", agentName)
			return
		default:
			reply(false, "agent_state_unknown", fmt.Sprintf("agent state %q does not accept prompts", status), agentName)
			return
		}

		// herdr can report a pane as done while a menu is open (Antigravity's
		// permission dialog under herdr 0.9.1): the text plus Enter would land
		// in the menu, and a leading "1" would pick an option. herdr's status
		// is kept as reported; the prompt is just refused. Fail closed if the
		// screen cannot be read.
		screen, err := e.readVisible(ctx, cmd.PaneID)
		if err != nil {
			reply(false, "herdr_offline", fmt.Sprintf("read screen: %v", err), agentName)
			return
		}
		if _, menu := ad.ParsePrompt(screen); menu {
			reply(false, "agent_blocked", "a menu is open on the pane; answer or cancel it first", agentName)
			return
		}

		promptCtx, promptCancel := context.WithTimeout(ctx, 3*time.Second)
		defer promptCancel()

		if err := e.Herdr.Prompt(promptCtx, cmd.PaneID, cmd.Text); err != nil {
			if herdr.IsCode(err, "agent_blocked") {
				reply(false, "agent_blocked", err.Error(), agentName)
				return
			}
			if errors.Is(err, herdr.ErrUnavailable) {
				reply(false, "herdr_offline", err.Error(), agentName)
				return
			}
			reply(false, "internal", err.Error(), agentName)
			return
		}

		reply(true, "", "", agentName)
		e.refreshSoon()

	case "answer":
		if status != model.StatusBlocked {
			reply(false, "stale_state", "agent is not blocked", agentName)
			return
		}

		p, code, msg := e.readFreshPrompt(ctx, cmd.PaneID, ad)
		if code != "" {
			reply(false, code, msg, agentName)
			return
		}

		if p.Public.Fingerprint != cmd.Fingerprint {
			reply(false, "prompt_changed", fmt.Sprintf("fingerprint mismatch: expected %s, got %s", cmd.Fingerprint, p.Public.Fingerprint), agentName)
			return
		}

		keys, ok := p.Keys[cmd.OptionID]
		if !ok {
			reply(false, "unknown_option", fmt.Sprintf("option %q not found in current prompt", cmd.OptionID), agentName)
			return
		}

		if code, msg := e.pressPromptKeys(ctx, cmd.PaneID, info.StateChangeSeq, p.Public.Fingerprint, keys); code != "" {
			reply(false, code, msg, agentName)
			return
		}

		reply(true, "", "", agentName)
		e.refreshSoon()

	case "cancel":
		if status != model.StatusBlocked {
			reply(false, "stale_state", "agent is not blocked", agentName)
			return
		}

		// Never trust the cached prompt: re-read the screen and take the cancel
		// keys from what is on it right now (agy's edit prompt cancels with "2",
		// which means "always allow" in its bash prompt).
		p, code, msg := e.readFreshPrompt(ctx, cmd.PaneID, ad)
		if code != "" {
			reply(false, code, msg, agentName)
			return
		}

		// The relay's cancel carries no fingerprint (contracts §2.2), so fall back
		// to the prompt this bridge published for this seq: it is what the watch showed.
		expected := cmd.Fingerprint
		if expected == "" {
			expected = e.publishedFingerprint(cmd.PaneID, info.StateChangeSeq)
		}
		if expected == "" {
			reply(false, "prompt_changed", "no published prompt for this state", agentName)
			return
		}
		if p.Public.Fingerprint != expected {
			reply(false, "prompt_changed", fmt.Sprintf("fingerprint mismatch: expected %s, got %s", expected, p.Public.Fingerprint), agentName)
			return
		}

		if code, msg := e.pressPromptKeys(ctx, cmd.PaneID, info.StateChangeSeq, p.Public.Fingerprint, p.EffectiveCancelKeys(ad)); code != "" {
			reply(false, code, msg, agentName)
			return
		}

		reply(true, "", "", agentName)
		e.refreshSoon()

	default:
		reply(false, "invalid_request", fmt.Sprintf("unsupported command action: %q", cmd.Action), agentName)
	}
}

// pressPromptKeys sends keys for the prompt fp shown at seq, at most once.
// A prompt already answered or cancelled at that seq is rejected with
// stale_state until herdr reports a new seq. The claim happens before the
// send: a send that fails after reaching herdr (e.g. a timeout) cannot be told
// apart from one that never arrived, and pressing twice is the worse outcome.
func (e *Engine) pressPromptKeys(ctx context.Context, paneID string, seq uint64, fp string, keys []string) (string, string) {
	if !e.claimPrompt(paneID, seq, fp) {
		return "stale_state", "prompt already answered; waiting for herdr to report a new state"
	}
	sendCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	err := e.Herdr.SendKeys(sendCtx, paneID, keys)
	cancel()
	if err != nil {
		if errors.Is(err, herdr.ErrUnavailable) {
			return "herdr_offline", err.Error()
		}
		return "internal", err.Error()
	}
	return "", ""
}

// refreshSoon re-lists herdr in the background so the watch sees the effect
// of a command quickly.
func (e *Engine) refreshSoon() {
	if e.Syncer == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = e.Syncer.Refresh(ctx)
	}()
}

// readFreshPrompt re-reads the pane's visible screen and parses it with the
// agent's adapter, right before keys are sent. It returns a contract error code
// (and message) when the screen cannot be read or shows no menu.
func (e *Engine) readFreshPrompt(ctx context.Context, paneID string, ad agents.Adapter) (agents.Prompt, string, string) {
	screen, err := e.readVisible(ctx, paneID)
	if err != nil {
		return agents.Prompt{}, "herdr_offline", fmt.Sprintf("read screen: %v", err)
	}
	p, ok := ad.ParsePrompt(screen)
	if !ok {
		return agents.Prompt{}, "prompt_changed", "no menu on the visible screen"
	}
	return p, "", ""
}

// readVisible reads the pane's visible screen with a 3 s slice of ctx.
func (e *Engine) readVisible(ctx context.Context, paneID string) (string, error) {
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return e.Herdr.Read(readCtx, paneID, herdr.SourceVisible, 0)
}

// publishedFingerprint returns the fingerprint of the prompt this bridge
// published for paneID at seq, or "" if none is published for that state.
func (e *Engine) publishedFingerprint(paneID string, seq uint64) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	st, ok := e.states[paneID]
	if !ok || st.prompt == nil || st.info.StateChangeSeq != seq || st.public.Status != model.StatusBlocked {
		return ""
	}
	return st.prompt.Public.Fingerprint
}
