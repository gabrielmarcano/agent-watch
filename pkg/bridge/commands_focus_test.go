package bridge

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// OpenCode answers with Enter on a button bar, so the bridge checks the
// focus on the pane's ansi screen before sending them (agents.md §5.1). The
// screens are real captures from pkg/agents/testdata/opencode/focus.

var sgrSeq = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// focusScreens returns a capture in both forms herdr serves: the ansi read
// and the text read (no escapes, no CR, trailing blanks trimmed).
func focusScreens(t *testing.T, name string) (text, ansi string) {
	t.Helper()
	ansi = loadFixture(t, "opencode", "focus/"+name)
	rows := strings.Split(strings.ReplaceAll(sgrSeq.ReplaceAllString(ansi, ""), "\r\n", "\n"), "\n")
	for i, r := range rows {
		rows[i] = strings.TrimRight(r, " ")
	}
	return strings.Join(rows, "\n"), ansi
}

// setFocusScreen serves capture name as pane w1:p1's visible screen.
func setFocusScreen(t *testing.T, h *testHarness, name string) (text string) {
	t.Helper()
	text, ansi := focusScreens(t, name)
	h.server.SetScreen("w1:p1", "visible", text)
	h.server.SetANSIScreen("w1:p1", "visible", ansi)
	return text
}

// ansiReadsSince counts agent.read calls in the ansi format after call index from.
func ansiReadsSince(h *testHarness, from int) int {
	n := 0
	for _, c := range h.server.Calls()[from:] {
		if c.Method == "agent.read" && c.Params["format"] == "ansi" {
			n++
		}
	}
	return n
}

func newOpenCodeBlocked(t *testing.T, screen string) (*testHarness, string) {
	t.Helper()
	h := newTestHarness(t)
	h.server.SetAgents([]map[string]any{agentRow("w1:p1", "opencode", "blocked", 100)})
	text := setFocusScreen(t, h, screen)
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})
	return h, fingerprintOf(t, h, "opencode", text)
}

func answer(reqID, optionID, fp string) model.CommandMsg {
	return model.CommandMsg{RequestID: reqID, Action: "answer", PaneID: "w1:p1", ExpectedSeq: 100, OptionID: optionID, Fingerprint: fp}
}

// Focus moved to "Allow always" on the Mac: Enter would press it. The watch's
// Allow once and Allow always are both refused with prompt_changed and no
// key; the refusal does not consume the prompt, so once the focus is back on
// Allow once the same answer goes through, once.
func TestCommands_OpenCodeFocusMovedRefusesWithoutConsuming(t *testing.T) {
	h, fp := newOpenCodeBlocked(t, "default-permission-always.ansi")

	for _, opt := range []string{"opt-1", "opt-2"} {
		before := len(h.server.Calls())
		res, _ := runCmd(t, h, answer("req-moved-"+opt, opt, fp))
		if res.OK || res.ErrorCode != "prompt_changed" {
			t.Errorf("%s with focus moved: ok=%v code=%q msg=%q, want prompt_changed", opt, res.OK, res.ErrorCode, res.Message)
		}
		if !strings.Contains(res.Message, "answer it on the Mac") {
			t.Errorf("%s: message %q does not tell the user to answer on the Mac", opt, res.Message)
		}
		if keys := sendKeysSince(h, before); len(keys) != 0 {
			t.Errorf("%s with focus moved sent keys %v, want none", opt, keys)
		}
		if n := ansiReadsSince(h, before); n != 1 {
			t.Errorf("%s: %d ansi reads, want 1", opt, n)
		}
	}

	// Same screen text, focus back on Allow once: same fingerprint, same seq.
	if text := setFocusScreen(t, h, "default-permission-once.ansi"); fingerprintOf(t, h, "opencode", text) != fp {
		t.Fatalf("the two captures differ in text; the test needs the same prompt")
	}
	before := len(h.server.Calls())
	res, _ := runCmd(t, h, answer("req-back", "opt-1", fp))
	if !res.OK {
		t.Fatalf("answer with focus back on Allow once: code=%q msg=%q, want ok", res.ErrorCode, res.Message)
	}
	if keys := sendKeysSince(h, before); !reflect.DeepEqual(keys, [][]string{{"Enter"}}) {
		t.Errorf("sent %v, want [[Enter]] once", keys)
	}
}

// Focus on Allow once (as the dialog mounts): the keys go out once, and a
// second tap is still caught by the replay guard.
func TestCommands_OpenCodeFocusOnDefaultSendsOnce(t *testing.T) {
	h, fp := newOpenCodeBlocked(t, "tokyonight-permission-once.ansi")

	before := len(h.server.Calls())
	res, _ := runCmd(t, h, answer("req-always", "opt-2", fp))
	if !res.OK {
		t.Fatalf("Allow always with focus on Allow once: code=%q msg=%q, want ok", res.ErrorCode, res.Message)
	}
	res, _ = runCmd(t, h, answer("req-always-again", "opt-2", fp))
	if res.OK || res.ErrorCode != "stale_state" {
		t.Errorf("second tap: ok=%v code=%q, want stale_state", res.OK, res.ErrorCode)
	}
	if keys := sendKeysSince(h, before); !reflect.DeepEqual(keys, [][]string{{"Right", "Enter", "Enter"}}) {
		t.Errorf("sent %v, want [[Right Enter Enter]] once", keys)
	}
}

// esc acts whatever the focus: Reject and cancel are sent without an ansi read.
func TestCommands_OpenCodeRejectAndCancelSkipFocusCheck(t *testing.T) {
	h, fp := newOpenCodeBlocked(t, "default-permission-reject.ansi")

	before := len(h.server.Calls())
	res, _ := runCmd(t, h, answer("req-reject", "opt-3", fp))
	if !res.OK {
		t.Fatalf("Reject: code=%q msg=%q, want ok", res.ErrorCode, res.Message)
	}
	if keys := sendKeysSince(h, before); !reflect.DeepEqual(keys, [][]string{{"esc"}}) {
		t.Errorf("Reject sent %v, want [[esc]]", keys)
	}
	if n := ansiReadsSince(h, before); n != 0 {
		t.Errorf("Reject made %d ansi reads, want 0", n)
	}

	// Cancel on the always stage with the focus moved to Cancel.
	h2, fp2 := newOpenCodeBlocked(t, "default-confirm-cancel.ansi")
	before = len(h2.server.Calls())
	res, _ = runCmd(t, h2, model.CommandMsg{RequestID: "req-cancel", Action: "cancel", PaneID: "w1:p1", ExpectedSeq: 100, Fingerprint: fp2})
	if !res.OK {
		t.Fatalf("cancel: code=%q msg=%q, want ok", res.ErrorCode, res.Message)
	}
	if keys := sendKeysSince(h2, before); !reflect.DeepEqual(keys, [][]string{{"esc"}}) {
		t.Errorf("cancel sent %v, want [[esc]]", keys)
	}
	if n := ansiReadsSince(h2, before); n != 0 {
		t.Errorf("cancel made %d ansi reads, want 0", n)
	}
}

// The always stage: Confirm is Enter, so it needs the focus on Confirm.
func TestCommands_OpenCodeConfirmStageChecksFocus(t *testing.T) {
	h, fp := newOpenCodeBlocked(t, "default-confirm-cancel.ansi")
	before := len(h.server.Calls())
	res, _ := runCmd(t, h, answer("req-confirm-moved", "opt-1", fp))
	if res.OK || res.ErrorCode != "prompt_changed" {
		t.Errorf("Confirm with focus on Cancel: ok=%v code=%q, want prompt_changed", res.OK, res.ErrorCode)
	}
	if keys := sendKeysSince(h, before); len(keys) != 0 {
		t.Errorf("sent %v, want none", keys)
	}

	h2, fp2 := newOpenCodeBlocked(t, "lucent-orng-confirm-confirm.ansi")
	before = len(h2.server.Calls())
	res, _ = runCmd(t, h2, answer("req-confirm", "opt-1", fp2))
	if !res.OK {
		t.Fatalf("Confirm with focus on Confirm: code=%q msg=%q, want ok", res.ErrorCode, res.Message)
	}
	if keys := sendKeysSince(h2, before); !reflect.DeepEqual(keys, [][]string{{"Enter"}}) {
		t.Errorf("sent %v, want [[Enter]]", keys)
	}
}

// No ansi screen from herdr (an empty read): the focus cannot be seen, so
// nothing is sent.
func TestCommands_OpenCodeUnreadableFocusSendsNothing(t *testing.T) {
	h, fp := newOpenCodeBlocked(t, "default-permission-once.ansi")
	h.server.SetANSIScreen("w1:p1", "visible", "")

	before := len(h.server.Calls())
	res, _ := runCmd(t, h, answer("req-blind", "opt-1", fp))
	if res.OK || res.ErrorCode != "prompt_changed" {
		t.Errorf("empty ansi screen: ok=%v code=%q, want prompt_changed", res.OK, res.ErrorCode)
	}
	if keys := sendKeysSince(h, before); len(keys) != 0 {
		t.Errorf("sent %v, want none", keys)
	}
}

// Agents that answer with digits never pay for an ansi read.
func TestCommands_DigitAnswersSkipFocusCheck(t *testing.T) {
	h := newTestHarness(t)
	bash := loadFixture(t, "claude", "permission-bash.txt")
	h.server.SetAgents([]map[string]any{agentRow("w1:p1", "claude", "blocked", 100)})
	h.server.SetScreen("w1:p1", "visible", bash)
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})

	before := len(h.server.Calls())
	res, _ := runCmd(t, h, answer("req-claude", "opt-1", fingerprintOf(t, h, "claude", bash)))
	if !res.OK {
		t.Fatalf("claude answer: code=%q msg=%q, want ok", res.ErrorCode, res.Message)
	}
	if n := ansiReadsSince(h, before); n != 0 {
		t.Errorf("claude answer made %d ansi reads, want 0", n)
	}
}
