package bridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

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
	lock := e.getPaneLock(cmd.PaneID)
	lock.Lock()
	defer lock.Unlock()

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

	if !e.isHerdrOnline() {
		reply(false, "herdr_offline", "herdr is offline", "")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if e.Syncer == nil {
		reply(false, "herdr_offline", "syncer is nil", "")
		return
	}

	agentsList, err := e.Syncer.Refresh(ctx)
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

		switch status {
		case model.StatusBlocked:
			reply(false, "agent_blocked", "cannot prompt blocked agent; use answer or cancel", agentName)
			return
		case model.StatusUnknown:
			reply(false, "agent_state_unknown", "agent state is unknown", agentName)
			return
		case model.StatusWorking:
			if !ad.PromptWhileWorking() {
				reply(false, "agent_busy", "agent cannot queue prompts while working", agentName)
				return
			}
		}

		promptCtx, promptCancel := context.WithTimeout(context.Background(), 3*time.Second)
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
		go func() {
			refCtx, refCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer refCancel()
			_, _ = e.Syncer.Refresh(refCtx)
		}()

	case "answer":
		if status != model.StatusBlocked {
			reply(false, "stale_state", "agent is not blocked", agentName)
			return
		}

		readCtx, readCancel := context.WithTimeout(context.Background(), 3*time.Second)
		screen, err := e.Herdr.Read(readCtx, cmd.PaneID, herdr.SourceVisible, 0)
		readCancel()
		if err != nil {
			if errors.Is(err, herdr.ErrUnavailable) {
				reply(false, "herdr_offline", err.Error(), agentName)
				return
			}
			reply(false, "herdr_offline", fmt.Sprintf("read screen: %v", err), agentName)
			return
		}

		p, ok := ad.ParsePrompt(screen)
		if !ok {
			reply(false, "prompt_changed", "failed to parse prompt on visible screen", agentName)
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

		sendCtx, sendCancel := context.WithTimeout(context.Background(), 3*time.Second)
		err = e.Herdr.SendKeys(sendCtx, cmd.PaneID, keys)
		sendCancel()
		if err != nil {
			if errors.Is(err, herdr.ErrUnavailable) {
				reply(false, "herdr_offline", err.Error(), agentName)
				return
			}
			reply(false, "internal", err.Error(), agentName)
			return
		}

		reply(true, "", "", agentName)
		go func() {
			refCtx, refCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer refCancel()
			_, _ = e.Syncer.Refresh(refCtx)
		}()

	case "cancel":
		if status != model.StatusBlocked {
			reply(false, "stale_state", "agent is not blocked", agentName)
			return
		}

		cancelKeys := ad.CancelKeys()
		e.mu.RLock()
		if st, ok := e.states[cmd.PaneID]; ok && st.prompt != nil && len(st.prompt.CancelKeys) > 0 {
			cancelKeys = st.prompt.CancelKeys
		}
		e.mu.RUnlock()

		sendCtx, sendCancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := e.Herdr.SendKeys(sendCtx, cmd.PaneID, cancelKeys)
		sendCancel()
		if err != nil {
			if errors.Is(err, herdr.ErrUnavailable) {
				reply(false, "herdr_offline", err.Error(), agentName)
				return
			}
			reply(false, "internal", err.Error(), agentName)
			return
		}

		reply(true, "", "", agentName)
		go func() {
			refCtx, refCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer refCancel()
			_, _ = e.Syncer.Refresh(refCtx)
		}()

	default:
		reply(false, "invalid_request", fmt.Sprintf("unsupported command action: %q", cmd.Action), agentName)
	}
}
