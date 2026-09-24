package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/herdrtest"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

func TestCommands_AllErrorCodes(t *testing.T) {
	h := newTestHarness(t)

	// Fixture screen from claude
	fixturePath := filepath.Join("..", "agents", "testdata", "claude", "permission-bash.txt")
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixturePath, err)
	}

	h.server.SetAgents([]map[string]any{
		{
			"pane_id":          "w1:p1",
			"workspace_id":     "w1",
			"tab_id":           "t1",
			"agent":            "claude",
			"agent_status":     "blocked",
			"state_change_seq": 100,
		},
		{
			"pane_id":          "w1:p2",
			"workspace_id":     "w1",
			"tab_id":           "t1",
			"agent":            "generic",
			"agent_status":     "working",
			"state_change_seq": 50,
		},
		{
			"pane_id":          "w1:p3",
			"workspace_id":     "w1",
			"tab_id":           "t1",
			"agent":            "claude",
			"agent_status":     "unknown",
			"state_change_seq": 10,
		},
		{
			"pane_id":          "w1:p4_noagent",
			"workspace_id":     "w1",
			"tab_id":           "t1",
			"agent":            nil,
			"agent_status":     "idle",
			"state_change_seq": 1,
		},
	})
	h.server.SetScreen("w1:p1", "visible", string(fixture))
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})
	h.engine.herdrOnline = true

	// Helper to send command and wait for result
	sendCmd := func(cmd model.CommandMsg) model.CommandResultMsg {
		initialCallCount := len(h.server.Calls())
		h.sendRelayToBridge(t, cmd)

		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case msg := <-h.relayMsgs:
				if res, ok := msg.(model.CommandResultMsg); ok && res.RequestID == cmd.RequestID {
					return res
				}
			case <-time.After(20 * time.Millisecond):
			}
		}
		t.Fatalf("timed out waiting for command result for request_id=%s, calls so far=%d", cmd.RequestID, initialCallCount)
		return model.CommandResultMsg{}
	}

	assertNoSendKeys := func(callCountBefore int, label string) {
		calls := h.server.Calls()
		for _, c := range calls[callCountBefore:] {
			if c.Method == "agent.send_keys" {
				t.Errorf("%s: agent.send_keys was unexpectedly called: %+v", label, c.Params)
			}
		}
	}

	// 1. herdr_offline
	h.engine.herdrOnline = false
	res := sendCmd(model.CommandMsg{
		Type:        model.WireCommand,
		RequestID:   "req-offline",
		Action:      "prompt",
		PaneID:      "w1:p1",
		ExpectedSeq: 100,
		Text:        "hello",
	})
	if res.OK || res.ErrorCode != "herdr_offline" {
		t.Errorf("expected herdr_offline, got ok=%v code=%s msg=%s", res.OK, res.ErrorCode, res.Message)
	}
	h.engine.herdrOnline = true

	// 2. unknown_pane (non-existent pane)
	res = sendCmd(model.CommandMsg{
		Type:        model.WireCommand,
		RequestID:   "req-unknown-pane",
		Action:      "prompt",
		PaneID:      "w1:nonexistent",
		ExpectedSeq: 100,
		Text:        "hello",
	})
	if res.OK || res.ErrorCode != "unknown_pane" {
		t.Errorf("expected unknown_pane, got ok=%v code=%s msg=%s", res.OK, res.ErrorCode, res.Message)
	}

	// 3. unknown_pane (pane without detected agent)
	res = sendCmd(model.CommandMsg{
		Type:        model.WireCommand,
		RequestID:   "req-no-agent",
		Action:      "prompt",
		PaneID:      "w1:p4_noagent",
		ExpectedSeq: 1,
		Text:        "hello",
	})
	if res.OK || res.ErrorCode != "unknown_pane" {
		t.Errorf("expected unknown_pane for unassigned agent pane, got ok=%v code=%s msg=%s", res.OK, res.ErrorCode, res.Message)
	}

	// 4. stale_state (sequence mismatch)
	res = sendCmd(model.CommandMsg{
		Type:        model.WireCommand,
		RequestID:   "req-stale-seq",
		Action:      "prompt",
		PaneID:      "w1:p1",
		ExpectedSeq: 99, // actual is 100
		Text:        "hello",
	})
	if res.OK || res.ErrorCode != "stale_state" {
		t.Errorf("expected stale_state for seq mismatch, got ok=%v code=%s msg=%s", res.OK, res.ErrorCode, res.Message)
	}

	// 5. invalid_request (empty prompt text)
	res = sendCmd(model.CommandMsg{
		Type:        model.WireCommand,
		RequestID:   "req-empty-text",
		Action:      "prompt",
		PaneID:      "w1:p1",
		ExpectedSeq: 100,
		Text:        "   ",
	})
	if res.OK || res.ErrorCode != "invalid_request" {
		t.Errorf("expected invalid_request for empty prompt, got ok=%v code=%s msg=%s", res.OK, res.ErrorCode, res.Message)
	}

	// 6. invalid_request (prompt text > 4000 bytes)
	longText := strings.Repeat("x", 4001)
	res = sendCmd(model.CommandMsg{
		Type:        model.WireCommand,
		RequestID:   "req-long-text",
		Action:      "prompt",
		PaneID:      "w1:p1",
		ExpectedSeq: 100,
		Text:        longText,
	})
	if res.OK || res.ErrorCode != "invalid_request" {
		t.Errorf("expected invalid_request for >4000 text, got ok=%v code=%s msg=%s", res.OK, res.ErrorCode, res.Message)
	}

	// 7. agent_blocked (attempting prompt while blocked)
	res = sendCmd(model.CommandMsg{
		Type:        model.WireCommand,
		RequestID:   "req-prompt-blocked",
		Action:      "prompt",
		PaneID:      "w1:p1",
		ExpectedSeq: 100,
		Text:        "hello",
	})
	if res.OK || res.ErrorCode != "agent_blocked" {
		t.Errorf("expected agent_blocked, got ok=%v code=%s msg=%s", res.OK, res.ErrorCode, res.Message)
	}

	// 8. agent_state_unknown (prompt while status == unknown)
	res = sendCmd(model.CommandMsg{
		Type:        model.WireCommand,
		RequestID:   "req-prompt-unknown",
		Action:      "prompt",
		PaneID:      "w1:p3",
		ExpectedSeq: 10,
		Text:        "hello",
	})
	if res.OK || res.ErrorCode != "agent_state_unknown" {
		t.Errorf("expected agent_state_unknown, got ok=%v code=%s msg=%s", res.OK, res.ErrorCode, res.Message)
	}

	// 9. agent_busy (prompt while working to generic agent which doesn't support PromptWhileWorking)
	res = sendCmd(model.CommandMsg{
		Type:        model.WireCommand,
		RequestID:   "req-prompt-busy",
		Action:      "prompt",
		PaneID:      "w1:p2",
		ExpectedSeq: 50,
		Text:        "hello",
	})
	if res.OK || res.ErrorCode != "agent_busy" {
		t.Errorf("expected agent_busy, got ok=%v code=%s msg=%s", res.OK, res.ErrorCode, res.Message)
	}

	// 10. stale_state on answer to non-blocked agent
	res = sendCmd(model.CommandMsg{
		Type:        model.WireCommand,
		RequestID:   "req-answer-nonblocked",
		Action:      "answer",
		PaneID:      "w1:p2", // working
		ExpectedSeq: 50,
		OptionID:    "opt-1",
		Fingerprint: "any",
	})
	if res.OK || res.ErrorCode != "stale_state" {
		t.Errorf("expected stale_state for answer to working agent, got ok=%v code=%s msg=%s", res.OK, res.ErrorCode, res.Message)
	}

	// 11. prompt_changed (fingerprint mismatch)
	beforeCalls := len(h.server.Calls())
	res = sendCmd(model.CommandMsg{
		Type:        model.WireCommand,
		RequestID:   "req-bad-fp",
		Action:      "answer",
		PaneID:      "w1:p1",
		ExpectedSeq: 100,
		OptionID:    "opt-1",
		Fingerprint: "wrong_fingerprint_here",
	})
	if res.OK || res.ErrorCode != "prompt_changed" {
		t.Errorf("expected prompt_changed for bad fingerprint, got ok=%v code=%s msg=%s", res.OK, res.ErrorCode, res.Message)
	}
	assertNoSendKeys(beforeCalls, "prompt_changed (bad fp)")

	// 12. unknown_option (option_id not in parsed prompt)
	// First calculate the correct fingerprint from the fixture prompt
	ad := h.registry.For("claude")
	parsedPrompt, ok := ad.ParsePrompt(string(fixture))
	if !ok {
		t.Fatalf("failed to parse fixture prompt")
	}
	correctFP := parsedPrompt.Public.Fingerprint

	beforeCalls = len(h.server.Calls())
	res = sendCmd(model.CommandMsg{
		Type:        model.WireCommand,
		RequestID:   "req-unknown-opt",
		Action:      "answer",
		PaneID:      "w1:p1",
		ExpectedSeq: 100,
		OptionID:    "opt-nonexistent",
		Fingerprint: correctFP,
	})
	if res.OK || res.ErrorCode != "unknown_option" {
		t.Errorf("expected unknown_option, got ok=%v code=%s msg=%s", res.OK, res.ErrorCode, res.Message)
	}
	assertNoSendKeys(beforeCalls, "unknown_option")
}

func TestCommands_AnswerDispatchesGoldenKeys(t *testing.T) {
	h := newTestHarness(t)

	// Fixture screen from claude: bash permission
	fixturePath := filepath.Join("..", "agents", "testdata", "claude", "permission-bash.txt")
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixturePath, err)
	}

	h.server.SetAgents([]map[string]any{
		{
			"pane_id":          "w1:p1",
			"workspace_id":     "w1",
			"tab_id":           "t1",
			"agent":            "claude",
			"agent_status":     "blocked",
			"state_change_seq": 100,
		},
	})
	h.server.SetScreen("w1:p1", "visible", string(fixture))
	h.engine.herdrOnline = true

	ad := h.registry.For("claude")
	p, ok := ad.ParsePrompt(string(fixture))
	if !ok {
		t.Fatalf("failed to parse fixture")
	}

	// Option 1 is "Yes" -> keys = ["1"]
	opt1 := p.Public.Options[0]
	goldenKeys := p.Keys[opt1.ID]
	if len(goldenKeys) == 0 {
		t.Fatalf("no keys for opt1")
	}

	beforeCalls := len(h.server.Calls())

	h.sendRelayToBridge(t, model.CommandMsg{
		Type:        model.WireCommand,
		RequestID:   "req-answer-yes",
		Action:      "answer",
		PaneID:      "w1:p1",
		ExpectedSeq: 100,
		OptionID:    opt1.ID,
		Fingerprint: p.Public.Fingerprint,
	})

	var res *model.CommandResultMsg
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case msg := <-h.relayMsgs:
			if r, ok := msg.(model.CommandResultMsg); ok && r.RequestID == "req-answer-yes" {
				res = &r
				break
			}
		case <-time.After(20 * time.Millisecond):
		}
		if res != nil {
			break
		}
	}

	if res == nil || !res.OK {
		t.Fatalf("expected successful answer result, got %+v", res)
	}

	// Verify agent.send_keys was called on herdr with golden keys
	calls := h.server.Calls()
	var sentKeysCall *herdrtest.Call
	for _, c := range calls[beforeCalls:] {
		if c.Method == "agent.send_keys" {
			sentKeysCall = &c
			break
		}
	}

	if sentKeysCall == nil {
		t.Fatal("agent.send_keys was not called on herdr")
	}

	target := sentKeysCall.Params["target"].(string)
	if target != "w1:p1" {
		t.Errorf("send_keys target = %q, want w1:p1", target)
	}

	rawKeys := sentKeysCall.Params["keys"].([]any)
	if len(rawKeys) != len(goldenKeys) {
		t.Fatalf("send_keys keys length mismatch: got %v, want %v", rawKeys, goldenKeys)
	}
	for i, k := range rawKeys {
		if k.(string) != goldenKeys[i] {
			t.Errorf("keys[%d] = %q, want %q", i, k, goldenKeys[i])
		}
	}
}

func TestCommands_CancelDispatchesCancelKeys(t *testing.T) {
	h := newTestHarness(t)

	h.server.SetAgents([]map[string]any{
		{
			"pane_id":          "w1:p1",
			"workspace_id":     "w1",
			"tab_id":           "t1",
			"agent":            "claude",
			"agent_status":     "blocked",
			"state_change_seq": 100,
		},
	})
	h.engine.herdrOnline = true

	beforeCalls := len(h.server.Calls())

	h.sendRelayToBridge(t, model.CommandMsg{
		Type:        model.WireCommand,
		RequestID:   "req-cancel-1",
		Action:      "cancel",
		PaneID:      "w1:p1",
		ExpectedSeq: 100,
	})

	var res *model.CommandResultMsg
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case msg := <-h.relayMsgs:
			if r, ok := msg.(model.CommandResultMsg); ok && r.RequestID == "req-cancel-1" {
				res = &r
				break
			}
		case <-time.After(20 * time.Millisecond):
		}
		if res != nil {
			break
		}
	}

	if res == nil || !res.OK {
		t.Fatalf("expected successful cancel result, got %+v", res)
	}

	// Verify agent.send_keys was called with ["esc"]
	calls := h.server.Calls()
	var sentKeysCall *herdrtest.Call
	for _, c := range calls[beforeCalls:] {
		if c.Method == "agent.send_keys" {
			sentKeysCall = &c
			break
		}
	}

	if sentKeysCall == nil {
		t.Fatal("agent.send_keys was not called for cancel")
	}

	rawKeys := sentKeysCall.Params["keys"].([]any)
	if len(rawKeys) != 1 || rawKeys[0].(string) != "esc" {
		t.Errorf("cancel keys = %v, want [\"esc\"]", rawKeys)
	}
}

func TestCommands_PromptWhileWorking(t *testing.T) {
	h := newTestHarness(t)

	// claude agent supports PromptWhileWorking
	h.server.SetAgents([]map[string]any{
		{
			"pane_id":          "w1:p1",
			"workspace_id":     "w1",
			"tab_id":           "t1",
			"agent":            "claude",
			"agent_status":     "working",
			"state_change_seq": 100,
		},
	})
	h.engine.herdrOnline = true

	beforeCalls := len(h.server.Calls())

	h.sendRelayToBridge(t, model.CommandMsg{
		Type:        model.WireCommand,
		RequestID:   "req-prompt-working",
		Action:      "prompt",
		PaneID:      "w1:p1",
		ExpectedSeq: 100,
		Text:        "please run the tests next",
	})

	var res *model.CommandResultMsg
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case msg := <-h.relayMsgs:
			if r, ok := msg.(model.CommandResultMsg); ok && r.RequestID == "req-prompt-working" {
				res = &r
				break
			}
		case <-time.After(20 * time.Millisecond):
		}
		if res != nil {
			break
		}
	}

	if res == nil || !res.OK {
		t.Fatalf("expected successful prompt result for prompt-while-working, got %+v", res)
	}

	calls := h.server.Calls()
	var promptCall *herdrtest.Call
	for _, c := range calls[beforeCalls:] {
		if c.Method == "agent.prompt" {
			promptCall = &c
			break
		}
	}

	if promptCall == nil {
		t.Fatal("agent.prompt was not called on herdr")
	}

	if promptCall.Params["text"].(string) != "please run the tests next" {
		t.Errorf("prompt text = %q, want %q", promptCall.Params["text"], "please run the tests next")
	}
}
