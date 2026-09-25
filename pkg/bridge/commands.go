package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gabrielmarcano/agent-monitor/pkg/agents"
	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// commandTimeout bounds one command end to end, counted from its arrival:
// waiting for the pane lock, list, read, send. Timeouts nest from the inside
// out: bridge 6 s < relay 7 s < watch 8 s. The layer that presses keys gives up
// first, so when the relay or the watch reports "timeout" the bridge has
// already stopped and no keystroke can land afterwards.
const commandTimeout = 6 * time.Second

// maxPromptChars is the contract limit for prompt text, in characters.
const maxPromptChars = 4000

// HandleRelayMessage processes inbound messages from the relay.
func (e *Engine) HandleRelayMessage(msg any) {
	switch m := msg.(type) {
	case model.CommandMsg:
		e.startCommand(m)
	case *model.CommandMsg:
		e.startCommand(*m)
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

// startCommand starts the command's clock now, on arrival, and runs it in its
// own goroutine.
func (e *Engine) startCommand(cmd model.CommandMsg) {
	timeout := e.CommandTimeout
	if timeout <= 0 {
		timeout = commandTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	go func() {
		defer cancel()
		e.executeCommand(ctx, cmd)
	}()
}

// herdrErrorCode maps a failed herdr call to a contract error code. fallback
// covers errors that say nothing more specific.
func herdrErrorCode(ctx context.Context, err error, fallback string) string {
	switch {
	case herdr.IsCode(err, "agent_not_found"):
		return string(model.ErrUnknownPane) // the pane closed after the list
	case herdr.IsCode(err, "agent_blocked"):
		return string(model.ErrAgentBlocked)
	case errors.Is(err, herdr.ErrUnavailable):
		return string(model.ErrHerdrOffline)
	case ctx.Err() != nil:
		return string(model.ErrTimeout)
	default:
		return fallback
	}
}

// promptClaim is the replay-guard key for a prompt: a hash, so the text
// itself is never kept.
func promptClaim(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "prompt:" + hex.EncodeToString(sum[:])
}

func (e *Engine) executeCommand(ctx context.Context, cmd model.CommandMsg) {
	reply := func(ok bool, code, errMsg, agentName string) {
		textLen := utf8.RuneCountInString(cmd.Text)
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

	unlock, err := e.lockPane(ctx, cmd.PaneID)
	if err != nil {
		reply(false, string(model.ErrTimeout), "waited too long behind another command on this pane", "")
		return
	}
	defer unlock()

	// Checked under the pane lock, so a replay waits for the original to finish.
	if e.requests.observe(cmd.RequestID) {
		reply(false, "stale_state", "duplicate request_id", "")
		return
	}

	if !e.isHerdrOnline() {
		reply(false, "herdr_offline", "herdr is offline", "")
		return
	}

	// ctx is the whole command's budget, counted from its arrival; each herdr
	// call gets its own 3 s slice of it.
	if e.Herdr == nil {
		reply(false, "herdr_offline", "herdr client is nil", "")
		return
	}

	// Validate against the list this command fetched itself. Syncer.Refresh
	// returns the shared snapshot, which a concurrent, older refresh can
	// overwrite between its fetch and its return.
	listCtx, listCancel := context.WithTimeout(ctx, 3*time.Second)
	agentsList, err := e.Herdr.ListAgents(listCtx)
	listCancel()
	if err != nil {
		reply(false, herdrErrorCode(ctx, err, "herdr_offline"), fmt.Sprintf("herdr list: %v", err), "")
		return
	}
	// Whatever the outcome, let the engine catch up so the watch sees the
	// state this command acted on (or was rejected against).
	defer e.refreshSoon()
	e.pruneConsumed(agentsList)

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
		if trimmed == "" || utf8.RuneCountInString(cmd.Text) > maxPromptChars {
			reply(false, "invalid_request", fmt.Sprintf("prompt text must be 1-%d characters", maxPromptChars), agentName)
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
			reply(false, herdrErrorCode(ctx, err, "herdr_offline"), fmt.Sprintf("read screen: %v", err), agentName)
			return
		}
		if _, menu := ad.ParsePrompt(screen); menu {
			reply(false, "agent_blocked", "a menu is open on the pane; answer or cancel it first", agentName)
			return
		}

		// A double tap sends the same text twice under two request ids,
		// before herdr reports the new state: type it once. Claimed before
		// the send, like key presses: typing twice is the worse outcome.
		claim := promptClaim(cmd.Text)
		if !e.claimPrompt(cmd.PaneID, info.StateChangeSeq, claim) {
			reply(false, "stale_state", "this prompt was already sent; waiting for herdr to report a new state", agentName)
			return
		}

		promptCtx, promptCancel := context.WithTimeout(ctx, 3*time.Second)
		defer promptCancel()

		if err := e.Herdr.Prompt(promptCtx, cmd.PaneID, cmd.Text); err != nil {
			// Nothing was typed only when the dial failed or herdr refused
			// a blocked agent; anything else may have reached the pane.
			if errors.Is(err, herdr.ErrUnavailable) || herdr.IsCode(err, "agent_blocked") {
				e.releasePrompt(cmd.PaneID, info.StateChangeSeq, claim)
			}
			reply(false, herdrErrorCode(ctx, err, "internal"), err.Error(), agentName)
			return
		}

		reply(true, "", "", agentName)

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
			e.adoptPrompt(cmd.PaneID, info.StateChangeSeq, p)
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

	case "cancel":
		if status != model.StatusBlocked {
			reply(false, "stale_state", "agent is not blocked", agentName)
			return
		}

		// Never trust the cached prompt: re-read the screen and take the cancel
		// keys from what is on it right now (agy's edit prompt cancels with "2",
		// which means "always allow" in its bash prompt).
		screen, err := e.readVisible(ctx, cmd.PaneID)
		if err != nil {
			reply(false, herdrErrorCode(ctx, err, "herdr_offline"), fmt.Sprintf("read screen: %v", err), agentName)
			return
		}
		p, menu := ad.ParsePrompt(screen)
		if !menu {
			// No parseable menu. herdr (this command's own list) says blocked at
			// expected_seq; cancel only if the watch was showing an unknown
			// prompt, and then with the adapter's default keys, never cached
			// per-prompt ones. A menu that simply vanished is prompt_changed.
			if !e.cancelTargetsUnknown(cmd.PaneID, info.StateChangeSeq, cmd.Fingerprint) {
				reply(false, "prompt_changed", "no menu on the visible screen", agentName)
				return
			}
			if code, msg := e.pressPromptKeys(ctx, cmd.PaneID, info.StateChangeSeq, unknownFingerprint, ad.CancelKeys()); code != "" {
				reply(false, code, msg, agentName)
				return
			}
			reply(true, "", "", agentName)
			return
		}

		// The relay forwards the fingerprint the watch sent with cancel; it is
		// optional (contracts §2.2), and older watches send none. Without one,
		// fall back to the prompt this bridge published for this seq: it is
		// what the watch showed.
		expected := cmd.Fingerprint
		if expected == "" {
			expected = e.publishedFingerprint(cmd.PaneID, info.StateChangeSeq)
		}
		if expected == "" {
			e.adoptPrompt(cmd.PaneID, info.StateChangeSeq, p)
			reply(false, "prompt_changed", "no published prompt for this state", agentName)
			return
		}
		if p.Public.Fingerprint != expected {
			e.adoptPrompt(cmd.PaneID, info.StateChangeSeq, p)
			reply(false, "prompt_changed", fmt.Sprintf("fingerprint mismatch: expected %s, got %s", expected, p.Public.Fingerprint), agentName)
			return
		}

		if code, msg := e.pressPromptKeys(ctx, cmd.PaneID, info.StateChangeSeq, p.Public.Fingerprint, p.EffectiveCancelKeys(ad)); code != "" {
			reply(false, code, msg, agentName)
			return
		}

		reply(true, "", "", agentName)

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
		return herdrErrorCode(ctx, err, "internal"), err.Error()
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
		return agents.Prompt{}, herdrErrorCode(ctx, err, "herdr_offline"), fmt.Sprintf("read screen: %v", err)
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

// unknownFingerprint is the fingerprint every unknown-kind prompt carries
// (agents.UnknownPrompt builds it from the kind alone).
var unknownFingerprint = agents.UnknownPrompt("").Public.Fingerprint

// cancelTargetsUnknown reports whether a cancel that finds no menu on screen
// is aimed at an unknown-kind prompt: the command names the unknown
// fingerprint, or it names none and the prompt this bridge published for
// paneID at seq is of kind unknown. A command naming any other fingerprint
// saw a menu that is gone.
func (e *Engine) cancelTargetsUnknown(paneID string, seq uint64, cmdFP string) bool {
	if cmdFP != "" {
		return cmdFP == unknownFingerprint
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	st, ok := e.states[paneID]
	return ok && st.prompt != nil &&
		st.info.StateChangeSeq == seq &&
		st.public.Status == model.StatusBlocked &&
		st.prompt.Public.Kind == model.PromptUnknown
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
