package agents

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// The captures in testdata/opencode/focus are real `herdr agent read
// --source visible --format ansi` reads of OpenCode 1.18.32 dialogs (herdr
// 0.9.1), named <theme>-<stage>-<focused button>.ansi. The theme "system"
// follows the terminal palette; the other three are bundled themes. The
// v2-default-* captures are OpenCode 2.0.25 (herdr 0.9.3), default theme.

var sgrOnly = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// textRead turns an ansi capture into what the text read of the same screen
// returns: no escapes, no CR, trailing blanks trimmed per row. It is written
// independently of the adapter's own ANSI parser.
func textRead(ansi string) string {
	rows := strings.Split(strings.ReplaceAll(sgrOnly.ReplaceAllString(ansi, ""), "\r\n", "\n"), "\n")
	for i, r := range rows {
		rows[i] = strings.TrimRight(r, " ")
	}
	return strings.Join(rows, "\n")
}

func loadFocusCapture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "opencode", "focus", name))
	if err != nil {
		t.Fatalf("read capture %s: %v", name, err)
	}
	return string(b)
}

func focusGuard(t *testing.T) (Adapter, FocusGuard) {
	t.Helper()
	ad := NewRegistry(Config{}).For("opencode")
	fg, ok := ad.(FocusGuard)
	if !ok {
		t.Fatalf("the opencode adapter does not implement FocusGuard")
	}
	return ad, fg
}

// parseText parses the text read of an ansi capture: the prompt the bridge
// holds (and checks the fingerprint of) before it reads the ansi screen.
func parseText(t *testing.T, ad Adapter, ansi string) Prompt {
	t.Helper()
	p, ok := ad.ParsePrompt(textRead(ansi))
	if !ok {
		t.Fatalf("text read of the capture does not parse as a prompt")
	}
	return p
}

func optionByLabel(t *testing.T, p Prompt, label string) string {
	t.Helper()
	for _, o := range p.Public.Options {
		if o.Label == label {
			return o.ID
		}
	}
	t.Fatalf("no option %q in %+v", label, p.Public.Options)
	return ""
}

// Every capture: the keys of each option either do not depend on focus (esc)
// or are sent only when the focus is on the button they assume, "Allow once"
// on the first stage and "Confirm" on the second.
func TestOpenCodeFocusGuardOverCaptures(t *testing.T) {
	ad, fg := focusGuard(t)
	entries, err := os.ReadDir(filepath.Join("testdata", "opencode", "focus"))
	if err != nil {
		t.Fatal(err)
	}
	stages := map[string]struct {
		title   string
		focused map[string]string // file suffix → focused label
		home    string
	}{
		"permission": {"Permission required", map[string]string{"once": "Allow once", "always": "Allow always", "reject": "Reject"}, "Allow once"},
		"confirm":    {"Always allow", map[string]string{"confirm": "Confirm", "cancel": "Cancel"}, "Confirm"},
	}

	seen := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".ansi") {
			continue
		}
		seen++
		t.Run(strings.TrimSuffix(name, ".ansi"), func(t *testing.T) {
			parts := strings.Split(strings.TrimSuffix(name, ".ansi"), "-")
			stage, ok := stages[parts[len(parts)-2]]
			if !ok {
				t.Fatalf("unexpected capture name %s", name)
			}
			focused := stage.focused[parts[len(parts)-1]]
			ansi := loadFocusCapture(t, name)
			p := parseText(t, ad, ansi)
			if p.Public.Title != stage.title {
				t.Fatalf("title %q, want %q", p.Public.Title, stage.title)
			}

			for _, o := range p.Public.Options {
				keys := p.Keys[o.ID]
				escOnly := len(keys) == 1 && keys[0] == "esc"
				dependent := fg.FocusDependent(p, o.ID)
				err := fg.CheckFocus(ansi, p, o.ID)
				switch {
				case escOnly:
					if dependent || err != nil {
						t.Errorf("%s %v: dependent=%v err=%v; esc acts whatever the focus", o.Label, keys, dependent, err)
					}
				case focused == stage.home:
					if !dependent || err != nil {
						t.Errorf("%s %v with focus on %s: dependent=%v err=%v; want a checked pass", o.Label, keys, focused, dependent, err)
					}
				default:
					if !dependent || err == nil {
						t.Errorf("%s %v with focus on %s: dependent=%v err=%v; want a refusal", o.Label, keys, focused, dependent, err)
					}
				}
			}
		})
	}
	if seen < 20 {
		t.Errorf("found %d captures, want the 20 committed ones", seen)
	}
}

// Anything the guard cannot read with certainty is a refusal.
func TestOpenCodeFocusGuardFailsClosed(t *testing.T) {
	ad, fg := focusGuard(t)
	once := loadFocusCapture(t, "default-permission-once.ansi")
	p := parseText(t, ad, once)
	allowOnce := optionByLabel(t, p, "Allow once")
	allowAlways := optionByLabel(t, p, "Allow always")

	if err := fg.CheckFocus(once, p, allowOnce); err != nil {
		t.Fatalf("baseline: focus on Allow once refused: %v", err)
	}

	const warning = "245;167;66"
	buttonRow := func(s string) (int, []string) {
		rows := strings.Split(s, "\n")
		for i := len(rows) - 1; i >= 0; i-- {
			if strings.Contains(sgrOnly.ReplaceAllString(rows[i], ""), "Allow always") {
				return i, rows
			}
		}
		t.Fatalf("no button row")
		return 0, nil
	}
	editRow := func(s string, edit func(string) string) string {
		i, rows := buttonRow(s)
		rows[i] = edit(rows[i])
		return strings.Join(rows, "\n")
	}
	// The button row's own "┃" (its first styled glyph) in another colour.
	markerRecoloured := editRow(once, func(r string) string {
		return strings.Replace(r, "\x1b[38;2;"+warning+"m\x1b[48;2;20;20;20m┃", "\x1b[38;2;1;2;3m\x1b[48;2;20;20;20m┃", 1)
	})
	triangleRecoloured := strings.Replace(once, "\x1b[38;2;"+warning+"m\x1b[48;2;20;20;20m△", "\x1b[38;2;1;2;3m\x1b[48;2;20;20;20m△", 1)
	// "Allow always" painted with the focus colour too: two focused buttons.
	twoFocused := editRow(once, func(r string) string {
		return strings.Replace(r, "\x1b[48;2;30;30;30mAllow always", "\x1b[48;2;"+warning+"mAllow always", 1)
	})
	// Only "Allow once" left on the row, as if the buttons were stacked: the
	// ansi screen no longer shows the dialog the keys were parsed from.
	stacked := editRow(once, func(r string) string {
		r = strings.Replace(r, "Allow always", strings.Repeat(" ", len("Allow always")), 1)
		return strings.Replace(r, "Reject", strings.Repeat(" ", len("Reject")), 1)
	})
	inverse := editRow(once, func(r string) string {
		return strings.Replace(r, "\x1b[48;2;"+warning+"mAllow once", "\x1b[48;2;"+warning+"m\x1b[7mAllow once", 1)
	})
	duplicateLabel := editRow(once, func(r string) string {
		return strings.Replace(r, "fullscreen", "Allow once", 1)
	})
	confirm := loadFocusCapture(t, "default-confirm-confirm.ansi")

	for _, tc := range []struct {
		name, screen, option string
		reason               string // part of the error
	}{
		{"empty screen", "", allowOnce, "dialog changed"},
		{"plain text, no colours", textRead(once), allowOnce, "no colour of its own"},
		{"marker recoloured", markerRecoloured, allowOnce, "title glyph and the frame differ"},
		{"triangle recoloured", triangleRecoloured, allowOnce, "title glyph and the frame differ"},
		{"accent glyphs recoloured", strings.ReplaceAll(once, "38;2;"+warning, "38;2;1;2;3"), allowOnce, "0 buttons carry the focus colour"},
		{"two focused buttons", twoFocused, allowOnce, "2 buttons carry the focus colour"},
		{"buttons not on one row", stacked, allowOnce, "dialog changed"},
		{"inverse on the label", inverse, allowOnce, "no background of its own"},
		{"label twice on the row", duplicateLabel, allowOnce, "exactly once"},
		{"another dialog on the ansi screen", confirm, allowOnce, "dialog changed"},
		{"truncated escape", once + "\x1b[38;2;1", allowOnce, "unterminated"},
		{"lone escape", strings.Replace(once, "Allow once", "Allow\x1b once", 1), allowOnce, "other than CSI"},
		{"non-SGR escape", "\x1b[2J" + once, allowOnce, "other than SGR"},
		{"colon colour form", strings.Replace(once, "\x1b[38;2;"+warning+"m", "\x1b[38:2::"+strings.ReplaceAll(warning, ";", ":")+"m", 1), allowOnce, "colon colour form"},
		{"colour out of range", strings.Replace(once, "\x1b[38;2;"+warning+"m", "\x1b[38;2;300;0;0m", 1), allowOnce, "out of range"},
		{"control character", strings.Replace(once, "Reject", "Rej\tct", 1), allowOnce, "control character"},
		{"always, same checks", twoFocused, allowAlways, "2 buttons carry the focus colour"},
		{"unknown option", once, "opt-9", "not in the prompt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !fg.FocusDependent(p, tc.option) {
				t.Errorf("FocusDependent(%s) = false, want true", tc.option)
			}
			err := fg.CheckFocus(tc.screen, p, tc.option)
			if err == nil {
				t.Fatalf("CheckFocus accepted it; want an error")
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("CheckFocus refused with %q; want the reason to mention %q", err, tc.reason)
			}
		})
	}

	// Focus-dependent keys on a prompt that is not a known button dialog.
	odd := Prompt{
		Public: model.PendingPrompt{Kind: model.PromptPermission, Title: "Something else",
			Options: []model.PromptOption{{ID: "opt-1", Label: "Allow once", Role: model.RoleAllowOnce}}},
		Keys: map[string][]string{"opt-1": {"Enter"}},
	}
	if !fg.FocusDependent(odd, "opt-1") || fg.CheckFocus(once, odd, "opt-1") == nil {
		t.Errorf("Enter on an unknown dialog was not refused")
	}
}

// The question tool answers with digits, which pick an option by number
// whatever the cursor: no ansi read, nothing to check.
func TestOpenCodeFocusGuardSkipsDigitAnswers(t *testing.T) {
	ad, fg := focusGuard(t)
	b, err := os.ReadFile(filepath.Join("testdata", "opencode", "question-multiple.txt"))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := ad.ParsePrompt(string(b))
	if !ok {
		t.Fatal("question-multiple.txt does not parse")
	}
	for _, o := range p.Public.Options {
		if fg.FocusDependent(p, o.ID) {
			t.Errorf("%s %v reported focus-dependent", o.Label, p.Keys[o.ID])
		}
		if err := fg.CheckFocus("", p, o.ID); err != nil {
			t.Errorf("%s %v: %v", o.Label, p.Keys[o.ID], err)
		}
	}
}

// OpenCode themes may use palette colours; herdr then writes 38;5;n and
// 48;5;n. Derived from a real capture by rewriting the focus colour.
func TestOpenCodeFocusGuardPaletteColours(t *testing.T) {
	ad, fg := focusGuard(t)
	const warning = "2;245;167;66"
	toPalette := func(s string) string { return strings.ReplaceAll(s, warning, "5;214") }

	once := loadFocusCapture(t, "default-permission-once.ansi")
	p := parseText(t, ad, once)
	allowOnce := optionByLabel(t, p, "Allow once")
	if err := fg.CheckFocus(toPalette(once), p, allowOnce); err != nil {
		t.Errorf("palette colours, focus on Allow once: %v", err)
	}
	always := loadFocusCapture(t, "default-permission-always.ansi")
	if err := fg.CheckFocus(toPalette(always), parseText(t, ad, always), allowOnce); err == nil {
		t.Errorf("palette colours, focus on Allow always: accepted")
	}
	// Palette index 214 and the RGB it may stand for are not the same colour
	// as far as the guard can tell.
	mixed := strings.ReplaceAll(once, "38;"+warning, "38;5;214")
	if err := fg.CheckFocus(mixed, p, allowOnce); err == nil {
		t.Errorf("marker in palette colour, focus in RGB: accepted")
	}
}

// Only OpenCode needs the check: the other adapters answer with digits.
func TestFocusGuardOnlyOpenCode(t *testing.T) {
	reg := NewRegistry(Config{})
	for _, name := range []string{"claude", "agy", "some-other-agent"} {
		if _, ok := reg.For(name).(FocusGuard); ok {
			t.Errorf("%s implements FocusGuard; its keys do not depend on focus", name)
		}
	}
}
