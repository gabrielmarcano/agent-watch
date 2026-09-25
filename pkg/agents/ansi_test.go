package agents

import (
	"testing"
)

func TestParseANSIScreenStyles(t *testing.T) {
	rgb := func(r, g, b uint32) termColor { return termColor{kind: colorRGB, value: r<<16 | g<<8 | b} }
	idx := func(n uint32) termColor { return termColor{kind: colorIndexed, value: n} }
	def := termColor{}

	type cell struct {
		r       rune
		fg, bg  termColor
		inverse bool
	}
	for _, tc := range []struct {
		name string
		in   string
		want [][]cell
	}{
		{
			// herdr's shape: reset, then one sequence per attribute; rows end in \r\n.
			"herdr spans", "\x1b[0m\x1b[38;2;1;2;3m\x1b[48;5;7mab\x1b[0mc\r\nd",
			[][]cell{{{'a', rgb(1, 2, 3), idx(7), false}, {'b', rgb(1, 2, 3), idx(7), false}, {'c', def, def, false}}, {{'d', def, def, false}}},
		},
		{
			"basic and bright colours", "\x1b[31;42mx\x1b[91;102my\x1b[39;49mz",
			[][]cell{{{'x', idx(1), idx(2), false}, {'y', idx(9), idx(10), false}, {'z', def, def, false}}},
		},
		{
			"combined sequence", "\x1b[1;38;2;9;9;9;48;5;3mx\x1b[my",
			[][]cell{{{'x', rgb(9, 9, 9), idx(3), false}, {'y', def, def, false}}},
		},
		{
			// 58's colour values must not be read as attributes (7 = inverse).
			"underline colour consumed", "\x1b[58;2;7;7;7mx\x1b[58;5;7my\x1b[4:2mz",
			[][]cell{{{'x', def, def, false}, {'y', def, def, false}, {'z', def, def, false}}},
		},
		{
			"inverse on and off", "\x1b[7mx\x1b[27my",
			[][]cell{{{'x', def, def, true}, {'y', def, def, false}}},
		},
		{
			"style carries over rows", "\x1b[31ma\nb",
			[][]cell{{{'a', idx(1), def, false}}, {{'b', idx(1), def, false}}},
		},
		{
			"wide runes are one cell each", "\x1b[48;5;1m△┃",
			[][]cell{{{'△', def, idx(1), false}, {'┃', def, idx(1), false}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := parseANSIScreen(tc.in)
			if err != nil {
				t.Fatalf("parseANSIScreen: %v", err)
			}
			if len(rows) != len(tc.want) {
				t.Fatalf("%d rows, want %d", len(rows), len(tc.want))
			}
			for i, want := range tc.want {
				row := rows[i]
				if len(row.text) != len(want) || len(row.style) != len(want) {
					t.Fatalf("row %d: %d runes / %d styles, want %d", i, len(row.text), len(row.style), len(want))
				}
				for j, c := range want {
					got := cell{row.text[j], row.style[j].fg, row.style[j].bg, row.style[j].inverse}
					if got != c {
						t.Errorf("row %d cell %d = %+v, want %+v", i, j, got, c)
					}
				}
			}
		})
	}
}

// Anything but well-formed SGR and printable text is refused: the guard
// fails closed on a screen it cannot read exactly.
func TestParseANSIScreenRejects(t *testing.T) {
	for _, in := range []string{
		"x\x1b",                       // lone ESC at the end
		"\x1b[38;2;1;2mx",             // truecolor missing a component
		"\x1b[48;5mx",                 // palette missing its index
		"\x1b[38;3;1mx",               // unknown colour space
		"\x1b[38;5;256mx",             // index out of range
		"\x1b[48;2;1;2;256mx",         // component out of range
		"\x1b[38:2::1:2:3mx",          // colon colour form
		"\x1b[3am",                    // not a number
		"\x1b[99999999999999999999mx", // overflow
		"\x1b[2J",                     // not SGR
		"\x1b[?25h",                   // private CSI
		"\x1b]8;;http://x\x1b\\x",     // OSC
		"\x1b(Bx",                     // other escape
		"\x1b[31",                     // unterminated CSI
		"a\rb",                        // bare CR inside a row
		"a\tb",                        // control character
	} {
		if _, err := parseANSIScreen(in); err == nil {
			t.Errorf("parseANSIScreen(%q) accepted it", in)
		}
	}
}
