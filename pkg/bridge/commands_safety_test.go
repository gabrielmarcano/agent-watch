package bridge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// Regression tests for the command-safety review: every test asserts on the
// keys (or prompts) that actually reached the fake herdr, not only on the
// command_result code.

func loadFixture(t *testing.T, agent, name string) string {
	t.Helper()
	path := filepath.Join("..", "agents", "testdata", agent, name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return string(b)
}

func agentRow(paneID, agent, status string, seq uint64) map[string]any {
	return map[string]any{
		"pane_id":          paneID,
		"workspace_id":     "w1",
		"tab_id":           "t1",
		"agent":            agent,
		"agent_status":     status,
		"state_change_seq": seq,
	}
}

// sendKeysSince returns the key lists passed to agent.send_keys after call index from.
func sendKeysSince(h *testHarness, from int) [][]string {
	var out [][]string
	for _, c := range h.server.Calls()[from:] {
		if c.Method != "agent.send_keys" {
			continue
		}
		raw, _ := c.Params["keys"].([]any)
		keys := make([]string, 0, len(raw))
		for _, k := range raw {
			s, _ := k.(string)
			keys = append(keys, s)
		}
		out = append(out, keys)
	}
	return out
}

// callsSince counts calls to method after call index from.
func callsSince(h *testHarness, from int, method string) int {
	n := 0
	for _, c := range h.server.Calls()[from:] {
		if c.Method == method {
			n++
		}
	}
	return n
}

// waitMsg reads relay messages until pred matches or the timeout expires.
// It returns the matching message (nil on timeout) and every message seen.
func waitMsg(h *testHarness, timeout time.Duration, pred func(any) bool) (any, []any) {
	var seen []any
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-h.relayMsgs:
			seen = append(seen, msg)
			if pred(msg) {
				return msg, seen
			}
		case <-deadline:
			return nil, seen
		}
	}
}

func isResult(reqID string) func(any) bool {
	return func(msg any) bool {
		r, ok := msg.(model.CommandResultMsg)
		return ok && r.RequestID == reqID
	}
}

// isPromptUpdate matches an agent_update for paneID at seq carrying a prompt
// with fingerprint fp ("" matches any prompt).
func isPromptUpdate(paneID string, seq uint64, fp string) func(any) bool {
	return func(msg any) bool {
		u, ok := msg.(model.AgentUpdateMsg)
		if !ok || u.Agent.PaneID != paneID || u.Agent.StateChangeSeq != seq || u.Agent.Prompt == nil {
			return false
		}
		return fp == "" || u.Agent.Prompt.Fingerprint == fp
	}
}

// runCmd sends cmd through the fake relay and waits for its command_result.
func runCmd(t *testing.T, h *testHarness, cmd model.CommandMsg) (model.CommandResultMsg, []any) {
	t.Helper()
	cmd.Type = model.WireCommand
	h.sendRelayToBridge(t, cmd)
	msg, seen := waitMsg(h, 3*time.Second, isResult(cmd.RequestID))
	if msg == nil {
		t.Fatalf("timed out waiting for command_result of %s", cmd.RequestID)
	}
	return msg.(model.CommandResultMsg), seen
}

// syncAndAwaitPrompt makes the engine pick up the fake herdr state and waits
// until it publishes a prompt for paneID at seq (with fingerprint fp, or any if "").
func syncAndAwaitPrompt(t *testing.T, h *testHarness, paneID string, seq uint64, fp string) *model.PendingPrompt {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := h.syncer.Refresh(ctx); err != nil {
		t.Fatalf("syncer refresh: %v", err)
	}
	msg, _ := waitMsg(h, 3*time.Second, isPromptUpdate(paneID, seq, fp))
	if msg == nil {
		t.Fatalf("timed out waiting for published prompt on %s seq=%d fp=%q", paneID, seq, fp)
	}
	return msg.(model.AgentUpdateMsg).Agent.Prompt
}

func fingerprintOf(t *testing.T, h *testHarness, agent, screen string) string {
	t.Helper()
	p, ok := h.registry.For(agent).ParsePrompt(screen)
	if !ok {
		t.Fatalf("fixture for %s does not parse as a prompt", agent)
	}
	return p.Public.Fingerprint
}

// --- Bug 1: cancel must re-read the screen, never use the cached prompt ---

// Reproduces the review finding: agy cached a "Pending edit" prompt (cancel
// keys ["2"] = reject), then the screen switched to a bash permission menu at
// the same seq, where "2" means "Yes, and always allow".
func TestCommands_CancelRereadsScreenInsteadOfCachedPrompt(t *testing.T) {
	h := newTestHarness(t)
	edit := loadFixture(t, "agy", "permission-edit.txt")
	bash := loadFixture(t, "agy", "permission-bash.txt")

	h.server.SetAgents([]map[string]any{agentRow("w1:p1", "agy", "blocked", 100)})
	h.server.SetScreen("w1:p1", "visible", edit)
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})
	syncAndAwaitPrompt(t, h, "w1:p1", 100, fingerprintOf(t, h, "agy", edit))

	h.server.SetScreen("w1:p1", "visible", bash)
	before := len(h.server.Calls())

	// The relay sends cancel without a fingerprint (CancelRequest has none).
	res, _ := runCmd(t, h, model.CommandMsg{
		RequestID:   "req-cancel-switched",
		Action:      "cancel",
		PaneID:      "w1:p1",
		ExpectedSeq: 100,
	})
	if res.OK || res.ErrorCode != "prompt_changed" {
		t.Errorf("cancel after menu switch: ok=%v code=%q msg=%q, want prompt_changed", res.OK, res.ErrorCode, res.Message)
	}
	if keys := sendKeysSince(h, before); len(keys) != 0 {
		t.Errorf("cancel after menu switch sent keys %v, want none", keys)
	}
}

// An explicit fingerprint that does not match the fresh parse is rejected.
func TestCommands_CancelFingerprintMismatchSendsNothing(t *testing.T) {
	h := newTestHarness(t)
	edit := loadFixture(t, "agy", "permission-edit.txt")

	h.server.SetAgents([]map[string]any{agentRow("w1:p1", "agy", "blocked", 100)})
	h.server.SetScreen("w1:p1", "visible", edit)
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})
	syncAndAwaitPrompt(t, h, "w1:p1", 100, "")

	before := len(h.server.Calls())
	res, _ := runCmd(t, h, model.CommandMsg{
		RequestID:   "req-cancel-badfp",
		Action:      "cancel",
		PaneID:      "w1:p1",
		ExpectedSeq: 100,
		Fingerprint: "0000000000000000",
	})
	if res.OK || res.ErrorCode != "prompt_changed" {
		t.Errorf("cancel with wrong fingerprint: ok=%v code=%q, want prompt_changed", res.OK, res.ErrorCode)
	}
	if keys := sendKeysSince(h, before); len(keys) != 0 {
		t.Errorf("cancel with wrong fingerprint sent keys %v, want none", keys)
	}
}

// With no menu on screen, cancel must not send esc blindly (it would interrupt
// a running Claude turn).
func TestCommands_CancelWithoutMenuSendsNothing(t *testing.T) {
	h := newTestHarness(t)
	noMenu := loadFixture(t, "claude", "no-menu-working.txt")

	h.server.SetAgents([]map[string]any{agentRow("w1:p1", "claude", "blocked", 100)})
	h.server.SetScreen("w1:p1", "visible", noMenu)
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})
	// The engine falls back to an unknown prompt after its retries.
	syncAndAwaitPrompt(t, h, "w1:p1", 100, "")

	before := len(h.server.Calls())
	res, _ := runCmd(t, h, model.CommandMsg{
		RequestID:   "req-cancel-nomenu",
		Action:      "cancel",
		PaneID:      "w1:p1",
		ExpectedSeq: 100,
	})
	if res.OK || res.ErrorCode != "prompt_changed" {
		t.Errorf("cancel without menu: ok=%v code=%q, want prompt_changed", res.OK, res.ErrorCode)
	}
	if keys := sendKeysSince(h, before); len(keys) != 0 {
		t.Errorf("cancel without menu sent keys %v, want none", keys)
	}
}

// Happy path: the cancel keys come from the fresh parse (agy edit quirk → "2").
func TestCommands_CancelUsesFreshPromptCancelKeys(t *testing.T) {
	h := newTestHarness(t)
	edit := loadFixture(t, "agy", "permission-edit.txt")

	h.server.SetAgents([]map[string]any{agentRow("w1:p1", "agy", "blocked", 100)})
	h.server.SetScreen("w1:p1", "visible", edit)
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})
	syncAndAwaitPrompt(t, h, "w1:p1", 100, "")

	before := len(h.server.Calls())
	res, _ := runCmd(t, h, model.CommandMsg{
		RequestID:   "req-cancel-edit",
		Action:      "cancel",
		PaneID:      "w1:p1",
		ExpectedSeq: 100,
	})
	if !res.OK {
		t.Fatalf("cancel on edit prompt: code=%q msg=%q, want ok", res.ErrorCode, res.Message)
	}
	keys := sendKeysSince(h, before)
	if len(keys) != 1 || len(keys[0]) != 1 || keys[0][0] != "2" {
		t.Errorf("cancel on agy edit prompt sent %v, want [[2]]", keys)
	}
}

// A scheduled re-parse must drop the cached prompt, so a later update at the
// new seq cannot re-publish the previous prompt.
func TestEngine_ReparseClearsCachedPrompt(t *testing.T) {
	h := newTestHarness(t)
	bash := loadFixture(t, "claude", "permission-bash.txt")
	noMenu := loadFixture(t, "claude", "no-menu-working.txt")
	oldFP := fingerprintOf(t, h, "claude", bash)

	h.server.SetScreen("w1:p1", "visible", bash)
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})

	claude := "claude"
	info := func(seq uint64, focused bool) herdr.AgentInfo {
		return herdr.AgentInfo{
			PaneID: "w1:p1", WorkspaceID: "w1", TabID: "t1",
			Agent: &claude, AgentStatus: "blocked", StateChangeSeq: seq, Focused: focused,
		}
	}

	h.engine.OnChanges([]herdr.Change{{Kind: herdr.Added, Agent: info(100, false)}})
	if msg, _ := waitMsg(h, 3*time.Second, isPromptUpdate("w1:p1", 100, oldFP)); msg == nil {
		t.Fatal("timed out waiting for the seq=100 prompt")
	}

	// New blocked episode at seq 101: the menu is gone and the re-parse is slow.
	h.server.SetScreen("w1:p1", "visible", noMenu)
	h.engine.RetryDelay = 200 * time.Millisecond
	prev := info(100, false)
	h.engine.OnChanges([]herdr.Change{{Kind: herdr.Updated, Agent: info(101, false), Prev: &prev}})

	h.engine.mu.RLock()
	cached := h.engine.states["w1:p1"].prompt
	h.engine.mu.RUnlock()
	if cached != nil {
		t.Errorf("prompt still cached after scheduling a re-parse: fingerprint %s", cached.Public.Fingerprint)
	}

	// Another update at seq 101 (focus change) while the re-parse is pending.
	prev2 := info(101, false)
	h.engine.OnChanges([]herdr.Change{{Kind: herdr.Updated, Agent: info(101, true), Prev: &prev2}})

	if msg, _ := waitMsg(h, 150*time.Millisecond, isPromptUpdate("w1:p1", 101, oldFP)); msg != nil {
		t.Errorf("the seq=100 prompt was re-published at seq=101")
	}
}
