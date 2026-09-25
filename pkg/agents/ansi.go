package agents

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// A screen read with herdr's "ansi" format (herdr-socket-api.md §2.1) is the
// visible screen with SGR sequences before each styled span. parseANSIScreen
// turns it into rows of runes with the colours of each one. It is strict: the
// only guard that uses it must fail closed, so anything but printable text
// and well-formed SGR is an error rather than a guess.

// colorKind says how a cell colour was given.
type colorKind uint8

const (
	colorDefault colorKind = iota // the terminal's default fg or bg (never equal to a set colour)
	colorIndexed                  // a palette index: 30–37, 90–97, 38;5;n …
	colorRGB                      // truecolor: 38;2;r;g;b …
)

// termColor is one cell colour: value is the palette index, or 0xRRGGBB.
// A palette index and the RGB it may render as are different colours here.
type termColor struct {
	kind  colorKind
	value uint32
}

type cellStyle struct {
	fg, bg  termColor
	inverse bool // SGR 7: fg and bg are drawn swapped
}

// styledRow is one screen row: a style per rune.
type styledRow struct {
	text  []rune
	style []cellStyle
}

func (r styledRow) String() string { return string(r.text) }

// parseANSIScreen splits an "ansi" screen into rows ("\n", with an optional
// "\r" before it). The SGR state carries across rows, as on a terminal.
func parseANSIScreen(s string) ([]styledRow, error) {
	var (
		rows []styledRow
		cur  styledRow
		st   cellStyle
	)
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\x1b':
			n, err := applyEscape(s[i:], &st)
			if err != nil {
				return nil, fmt.Errorf("byte %d: %w", i, err)
			}
			i += n
		case c == '\n':
			rows = append(rows, cur)
			cur = styledRow{}
			i++
		case c == '\r':
			if i+1 >= len(s) || s[i+1] != '\n' {
				return nil, fmt.Errorf("byte %d: carriage return inside a row", i)
			}
			i++
		case c < 0x20 || c == 0x7f:
			return nil, fmt.Errorf("byte %d: control character %#x", i, c)
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r >= 0x80 && r <= 0x9f { // C1 controls
				return nil, fmt.Errorf("byte %d: control character %U", i, r)
			}
			cur.text = append(cur.text, r)
			cur.style = append(cur.style, st)
			i += size
		}
	}
	return append(rows, cur), nil
}

// applyEscape applies the escape sequence at the start of s (s[0] == ESC)
// and returns its length. Only SGR ("ESC [ params m") is accepted.
func applyEscape(s string, st *cellStyle) (int, error) {
	if len(s) < 2 || s[1] != '[' {
		return 0, errors.New("escape sequence other than CSI")
	}
	for j := 2; j < len(s); j++ {
		b := s[j]
		switch {
		case b >= '0' && b <= '9', b == ';', b == ':':
			continue
		case b == 'm':
			if err := applySGR(s[2:j], st); err != nil {
				return 0, err
			}
			return j + 1, nil
		default:
			return 0, fmt.Errorf("CSI sequence other than SGR (%q)", s[:j+1])
		}
	}
	return 0, errors.New("unterminated CSI sequence")
}

// applySGR applies the parameters of one SGR sequence. Attributes that do not
// affect colours (bold, underline style "4:3" …) are skipped; the colour
// operands of 38/48/58 are always consumed so they are never read as
// attributes.
func applySGR(params string, st *cellStyle) error {
	if params == "" {
		*st = cellStyle{}
		return nil
	}
	ps := strings.Split(params, ";")
	for i := 0; i < len(ps); i++ {
		p := ps[i]
		if head, _, colon := strings.Cut(p, ":"); colon {
			// Sub-parameters: only the underline style ("4:2") is expected.
			if head == "38" || head == "48" || head == "58" {
				return fmt.Errorf("colon colour form %q", p)
			}
			continue
		}
		n, err := sgrNumber(p)
		if err != nil {
			return err
		}
		switch {
		case n == 0:
			*st = cellStyle{}
		case n == 7:
			st.inverse = true
		case n == 27:
			st.inverse = false
		case n >= 30 && n <= 37:
			st.fg = termColor{colorIndexed, n - 30}
		case n >= 90 && n <= 97:
			st.fg = termColor{colorIndexed, n - 90 + 8}
		case n == 39:
			st.fg = termColor{}
		case n >= 40 && n <= 47:
			st.bg = termColor{colorIndexed, n - 40}
		case n >= 100 && n <= 107:
			st.bg = termColor{colorIndexed, n - 100 + 8}
		case n == 49:
			st.bg = termColor{}
		case n == 38 || n == 48 || n == 58:
			col, used, err := sgrExtendedColor(ps[i+1:])
			if err != nil {
				return fmt.Errorf("SGR %d: %w", n, err)
			}
			i += used
			switch n {
			case 38:
				st.fg = col
			case 48:
				st.bg = col
			} // 58 is the underline colour: consumed, not tracked
		}
	}
	return nil
}

// sgrExtendedColor reads the operands after 38/48/58: "5;n" or "2;r;g;b".
// It returns the colour and how many parameters it used.
func sgrExtendedColor(ps []string) (termColor, int, error) {
	if len(ps) == 0 {
		return termColor{}, 0, errors.New("missing colour space")
	}
	space, err := sgrNumber(ps[0])
	if err != nil {
		return termColor{}, 0, err
	}
	switch space {
	case 5:
		if len(ps) < 2 {
			return termColor{}, 0, errors.New("missing palette index")
		}
		v, err := sgrByte(ps[1])
		if err != nil {
			return termColor{}, 0, err
		}
		return termColor{colorIndexed, v}, 2, nil
	case 2:
		if len(ps) < 4 {
			return termColor{}, 0, errors.New("missing RGB component")
		}
		var rgb uint32
		for _, c := range ps[1:4] {
			v, err := sgrByte(c)
			if err != nil {
				return termColor{}, 0, err
			}
			rgb = rgb<<8 | v
		}
		return termColor{colorRGB, rgb}, 4, nil
	default:
		return termColor{}, 0, fmt.Errorf("unknown colour space %d", space)
	}
}

// sgrNumber parses one SGR parameter; an empty one is 0.
func sgrNumber(p string) (uint32, error) {
	if p == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(p, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("SGR parameter %q", p)
	}
	return uint32(n), nil
}

// sgrByte parses a colour operand, which must be 0–255.
func sgrByte(p string) (uint32, error) {
	n, err := sgrNumber(p)
	if err != nil || n > 255 || p == "" {
		return 0, fmt.Errorf("colour value %q out of range", p)
	}
	return n, nil
}
