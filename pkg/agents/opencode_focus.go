package agents

import (
	"errors"
	"fmt"
	"strings"
)

// OpenCode's permission dialog is a button bar: Enter presses whichever
// button has focus, and the focus follows the arrow keys and the mouse (a
// hover is enough). The answer keys assume the focus the dialog gets when it
// mounts, so before sending them the bridge reads the screen in herdr's
// "ansi" format and this guard checks the focus is still there. It never
// plans keys from the focus it sees: a moved focus is a refusal.
//
// Focus rule (OpenCode 1.18.32, permission.tsx; checked in all bundled
// themes): the focused button's background is theme.warning, and the
// dialog's accent glyphs, the "┃" of its left border and the "△" before its
// title, are drawn with foreground theme.warning. The focused button is the
// one whose background equals that foreground. No fixed colour is assumed.

// ocButtonStage is a button-bar dialog: its title, its buttons in screen
// order, and the button focused when it mounts, which its keys assume.
type ocButtonStage struct {
	title  string
	labels []string
	home   string
}

var ocButtonStages = []ocButtonStage{
	{title: "Permission required", labels: []string{"Allow once", "Allow always", "Reject"}, home: "Allow once"},
	{title: "Always allow", labels: []string{"Confirm", "Cancel"}, home: "Confirm"},
}

// FocusDependent implements FocusGuard. esc (Reject, Cancel, dismiss) and
// digits (the question tool picks by number) act whatever the focus.
func (o *opencodeAdapter) FocusDependent(p Prompt, optionID string) bool {
	keys, ok := p.Keys[optionID]
	return !ok || !ocFocusFree(keys)
}

func ocFocusFree(keys []string) bool {
	if len(keys) == 0 {
		return false
	}
	for _, k := range keys {
		digit := len(k) == 1 && k[0] >= '1' && k[0] <= '9'
		if k != "esc" && !digit {
			return false
		}
	}
	return true
}

// CheckFocus implements FocusGuard.
func (o *opencodeAdapter) CheckFocus(ansiScreen string, p Prompt, optionID string) error {
	keys, ok := p.Keys[optionID]
	if !ok {
		return fmt.Errorf("option %q is not in the prompt", optionID)
	}
	if ocFocusFree(keys) {
		return nil
	}
	stage, ok := ocStageOf(p)
	if !ok {
		return fmt.Errorf("keys %v depend on the focus, but %q is not a known button dialog", keys, p.Public.Title)
	}

	rows, err := parseANSIScreen(ansiScreen)
	if err != nil {
		return fmt.Errorf("unreadable ansi screen: %w", err)
	}
	// The ansi read is a second read: it must show the dialog the keys are for.
	plain := make([]string, len(rows))
	for i, r := range rows {
		plain[i] = r.String()
	}
	if q, ok := o.ParsePrompt(strings.Join(plain, "\n")); !ok || q.Public.Fingerprint != p.Public.Fingerprint {
		return errors.New("the dialog changed between the text and the ansi read")
	}

	focused, err := ocFocusedButton(rows, plain, stage.labels)
	if err != nil {
		return err
	}
	if focused != stage.home {
		return fmt.Errorf("focus is on %q; the keys for %q assume %q", focused, optionID, stage.home)
	}
	return nil
}

// ocStageOf returns the button stage p was parsed from: same title, same
// buttons in the same order.
func ocStageOf(p Prompt) (ocButtonStage, bool) {
	for _, s := range ocButtonStages {
		if p.Public.Title != s.title || len(p.Public.Options) != len(s.labels) {
			continue
		}
		same := true
		for i, o := range p.Public.Options {
			same = same && o.Label == s.labels[i]
		}
		if same {
			return s, true
		}
	}
	return ocButtonStage{}, false
}

// ocFocusedButton returns the label of the focused button on the dialog's
// button row: the last framed row holding every label, as ocParseButtons
// finds it. Each label must appear on it exactly once in one style, and
// exactly one button may carry the accent colour as its background.
func ocFocusedButton(rows []styledRow, plain []string, labels []string) (string, error) {
	bar := -1
	for i := len(rows) - 1; i >= 0 && bar < 0; i-- {
		if _, framed := ocFrame(plain[i]); !framed {
			continue
		}
		all := true
		for _, l := range labels {
			all = all && strings.Contains(plain[i], l)
		}
		if all {
			bar = i
		}
	}
	if bar < 0 {
		return "", errors.New("no button row with every button on it")
	}

	accent, err := ocAccent(rows, plain, bar)
	if err != nil {
		return "", err
	}

	var focused []string
	for _, l := range labels {
		bg, err := ocLabelBackground(rows[bar], plain[bar], l)
		if err != nil {
			return "", err
		}
		if bg == accent {
			focused = append(focused, l)
		}
	}
	if len(focused) != 1 {
		return "", fmt.Errorf("%d buttons carry the focus colour, want exactly 1", len(focused))
	}
	return focused[0], nil
}

// ocAccent returns the foreground of the "┃" that frames the button row, and
// checks the "△" before the dialog's title (in the same framed block, above
// the row) has the same one.
func ocAccent(rows []styledRow, plain []string, bar int) (termColor, error) {
	marker, ok := ocGlyphStyle(rows[bar], '┃', true)
	if !ok {
		return termColor{}, errors.New("the button row has no frame glyph")
	}
	if marker.inverse || marker.fg.kind == colorDefault {
		return termColor{}, errors.New("the frame glyph has no colour of its own")
	}
	for i := bar - 1; i >= 0; i-- {
		inner, framed := ocFrame(plain[i])
		if !framed {
			break
		}
		if !strings.HasPrefix(strings.TrimSpace(inner), "△") {
			continue
		}
		tri, ok := ocGlyphStyle(rows[i], '△', false)
		if !ok || tri.inverse || tri.fg != marker.fg {
			return termColor{}, errors.New("the title glyph and the frame differ in colour")
		}
		return marker.fg, nil
	}
	return termColor{}, errors.New("no title glyph above the button row")
}

// ocGlyphStyle returns the style of the first non-space rune of row when it
// is g (first), or of the first g after the frame (not first).
func ocGlyphStyle(row styledRow, g rune, first bool) (cellStyle, bool) {
	for i, r := range row.text {
		switch {
		case r == g:
			return row.style[i], true
		case r == ' ':
		case first:
			return cellStyle{}, false
		case r != '┃':
			return cellStyle{}, false
		}
	}
	return cellStyle{}, false
}

// ocLabelBackground returns the background of label on row: it must occur
// once, every rune in one style, with a background of its own.
func ocLabelBackground(row styledRow, plain, label string) (termColor, error) {
	if strings.Count(plain, label) != 1 {
		return termColor{}, fmt.Errorf("%q is not on the button row exactly once", label)
	}
	start := len([]rune(plain[:strings.Index(plain, label)]))
	n := len([]rune(label))
	st := row.style[start]
	for _, s := range row.style[start : start+n] {
		if s != st {
			return termColor{}, fmt.Errorf("%q is not drawn in one style", label)
		}
	}
	if st.inverse || st.bg.kind == colorDefault {
		return termColor{}, fmt.Errorf("%q has no background of its own", label)
	}
	return st.bg, nil
}
