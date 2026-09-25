package bridge

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// promptTexts returns the text of every agent.prompt call after index from.
func promptTexts(h *testHarness, from int) []string {
	var out []string
	for _, c := range h.server.Calls()[from:] {
		if c.Method == "agent.prompt" {
			s, _ := c.Params["text"].(string)
			out = append(out, s)
		}
	}
	return out
}

func promptCmd(reqID, pane string, seq uint64, text string) model.CommandMsg {
	return model.CommandMsg{RequestID: reqID, Action: "prompt", PaneID: pane, ExpectedSeq: seq, Text: text}
}

// The contract limit is 4000 characters, not bytes: 4000 accented letters
// (8000 bytes) are valid, 4001 are not.
func TestCommands_PromptLimitCountsCharacters(t *testing.T) {
	h := newTestHarness(t)
	h.server.SetAgents([]map[string]any{agentRow("w1:p1", "claude", "idle", 5)})
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})

	atLimit := strings.Repeat("é", 4000)
	if utf8.RuneCountInString(atLimit) != 4000 || len(atLimit) != 8000 {
		t.Fatal("bad fixture")
	}
	before := len(h.server.Calls())
	if res, _ := runCmd(t, h, promptCmd("req-4000", "w1:p1", 5, atLimit)); !res.OK {
		t.Errorf("4000 characters: code=%q msg=%q, want ok", res.ErrorCode, res.Message)
	}
	if res, _ := runCmd(t, h, promptCmd("req-4001", "w1:p1", 5, atLimit+"é")); res.OK || res.ErrorCode != "invalid_request" {
		t.Errorf("4001 characters: ok=%v code=%q, want invalid_request", res.OK, res.ErrorCode)
	}
	if got := promptTexts(h, before); len(got) != 1 || got[0] != atLimit {
		t.Errorf("herdr got %d prompts, want exactly the 4000-character one", len(got))
	}
}

// A double tap sends the same prompt twice under two request ids: the
// second must not type the text again.
func TestCommands_DuplicatePromptRejected(t *testing.T) {
	h := newTestHarness(t)
	h.server.SetAgents([]map[string]any{agentRow("w1:p1", "claude", "idle", 7)})
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})

	before := len(h.server.Calls())
	if res, _ := runCmd(t, h, promptCmd("req-p1", "w1:p1", 7, "run the tests")); !res.OK {
		t.Fatalf("first prompt: code=%q msg=%q, want ok", res.ErrorCode, res.Message)
	}
	if res, _ := runCmd(t, h, promptCmd("req-p2", "w1:p1", 7, "run the tests")); res.OK || res.ErrorCode != "stale_state" {
		t.Errorf("repeat prompt: ok=%v code=%q, want stale_state", res.OK, res.ErrorCode)
	}
	// A different text at the same seq is a deliberate second prompt.
	if res, _ := runCmd(t, h, promptCmd("req-p3", "w1:p1", 7, "and lint")); !res.OK {
		t.Errorf("different prompt: code=%q msg=%q, want ok", res.ErrorCode, res.Message)
	}
	// Once herdr reports a new state, the same text may be sent again.
	h.server.SetAgents([]map[string]any{agentRow("w1:p1", "claude", "idle", 9)})
	if res, _ := runCmd(t, h, promptCmd("req-p4", "w1:p1", 9, "run the tests")); !res.OK {
		t.Errorf("same prompt at a new seq: code=%q msg=%q, want ok", res.ErrorCode, res.Message)
	}
	got := promptTexts(h, before)
	want := []string{"run the tests", "and lint", "run the tests"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("herdr prompts = %q, want %q", got, want)
	}
}

// When herdr refuses a prompt without typing it (agent_blocked), the
// duplicate guard must not hold it against a retry.
func TestCommands_RefusedPromptCanBeRetried(t *testing.T) {
	h := newTestHarness(t)
	h.server.SetAgents([]map[string]any{agentRow("w1:p1", "claude", "idle", 12)})
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})

	h.server.FailNext("agent.prompt", "agent_blocked", "agent is blocked")
	if res, _ := runCmd(t, h, promptCmd("req-r1", "w1:p1", 12, "retry me")); res.OK || res.ErrorCode != "agent_blocked" {
		t.Fatalf("refused prompt: ok=%v code=%q, want agent_blocked", res.OK, res.ErrorCode)
	}
	if res, _ := runCmd(t, h, promptCmd("req-r2", "w1:p1", 12, "retry me")); !res.OK {
		t.Errorf("retry after a refusal: code=%q msg=%q, want ok", res.ErrorCode, res.Message)
	}
}

// herdr answers agent_not_found when the pane closed between the list and
// the read or the send: that is unknown_pane, not herdr_offline or internal.
func TestCommands_AgentNotFoundIsUnknownPane(t *testing.T) {
	bash := func(t *testing.T) string { return loadFixture(t, "claude", "permission-bash.txt") }

	cases := []struct {
		name   string
		status string
		fail   string // herdr method that answers agent_not_found
		cmd    func(fp string) model.CommandMsg
	}{
		{"prompt read", "idle", "agent.read", func(string) model.CommandMsg {
			return promptCmd("req-nf-1", "w1:p1", 10, "hi")
		}},
		{"prompt send", "idle", "agent.prompt", func(string) model.CommandMsg {
			return promptCmd("req-nf-2", "w1:p1", 10, "hi")
		}},
		{"answer read", "blocked", "agent.read", func(fp string) model.CommandMsg {
			return model.CommandMsg{RequestID: "req-nf-3", Action: "answer", PaneID: "w1:p1", ExpectedSeq: 10, OptionID: "opt-1", Fingerprint: fp}
		}},
		{"answer send", "blocked", "agent.send_keys", func(fp string) model.CommandMsg {
			return model.CommandMsg{RequestID: "req-nf-4", Action: "answer", PaneID: "w1:p1", ExpectedSeq: 10, OptionID: "opt-1", Fingerprint: fp}
		}},
		{"cancel read", "blocked", "agent.read", func(fp string) model.CommandMsg {
			return model.CommandMsg{RequestID: "req-nf-5", Action: "cancel", PaneID: "w1:p1", ExpectedSeq: 10, Fingerprint: fp}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHarness(t)
			screen := bash(t)
			if tc.status != "blocked" {
				screen = "$ \n"
			}
			h.server.SetAgents([]map[string]any{agentRow("w1:p1", "claude", tc.status, 10)})
			h.server.SetScreen("w1:p1", "visible", screen)
			h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})
			fp := ""
			if tc.status == "blocked" {
				fp = fingerprintOf(t, h, "claude", screen)
			}

			h.server.FailNext(tc.fail, "agent_not_found", "agent target w1:p1 not found")
			res, _ := runCmd(t, h, tc.cmd(fp))
			if res.OK || res.ErrorCode != "unknown_pane" {
				t.Errorf("ok=%v code=%q msg=%q, want unknown_pane", res.OK, res.ErrorCode, res.Message)
			}
		})
	}
}

// A command that waited behind another one on the same pane past its
// deadline must not run: the relay has already answered the watch with a
// timeout. The deadline counts from when the command arrived.
func TestCommands_QueuedCommandExpiresAtDeadline(t *testing.T) {
	h := newTestHarness(t)
	bash := loadFixture(t, "claude", "permission-bash.txt")
	h.server.SetAgents([]map[string]any{agentRow("w1:p1", "claude", "blocked", 100)})
	h.server.SetScreen("w1:p1", "visible", bash)
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})
	h.engine.CommandTimeout = 150 * time.Millisecond

	// Another command holds the pane.
	unlock, err := h.engine.lockPane(context.Background(), "w1:p1")
	if err != nil {
		t.Fatal(err)
	}
	before := len(h.server.Calls())
	h.sendRelayToBridge(t, model.CommandMsg{
		Type: model.WireCommand, RequestID: "req-late", Action: "answer", PaneID: "w1:p1",
		ExpectedSeq: 100, OptionID: "opt-1", Fingerprint: fingerprintOf(t, h, "claude", bash),
	})

	msg, _ := waitMsg(h, 2*time.Second, isResult("req-late"))
	unlock()
	if msg == nil {
		t.Fatal("no command_result while the pane stayed locked past the deadline")
	}
	if res := msg.(model.CommandResultMsg); res.OK || res.ErrorCode != "timeout" {
		t.Errorf("ok=%v code=%q, want timeout", res.OK, res.ErrorCode)
	}

	pollUntil(t, 2*time.Second, "pane lock released", func() bool {
		h.engine.lockMu.Lock()
		defer h.engine.lockMu.Unlock()
		return len(h.engine.paneLocks) == 0
	})
	if n := callsSince(h, before, "agent.send_keys"); n != 0 {
		t.Errorf("expired command sent keys %d time(s)", n)
	}
	if n := callsSince(h, before, "agent.list"); n != 0 {
		t.Errorf("expired command still listed herdr %d time(s)", n)
	}
}
