package agents

import "strings"

// Box-drawing pieces of a table grid, in the light, rounded, heavy and double
// styles TUIs draw tables with (Claude Code draws markdown tables as ┌─┬─┐).
const (
	boxHorizontal  = "─━═"
	boxVertical    = "│┃║"
	boxTopLeft     = "┌╭┏╔"
	boxTopRight    = "┐╮┓╗"
	boxTopJoin     = "┬┳╦"
	boxMidLeft     = "├┣╠"
	boxMidRight    = "┤┫╣"
	boxMidJoin     = "┼╋╬"
	boxBottomLeft  = "└╰┗╚"
	boxBottomRight = "┘╯┛╝"
	boxBottomJoin  = "┴┻╩"
)

// boxTables rewrites every table drawn with box characters as a markdown pipe
// table, so a screen capture carries a table the way a transcript does and
// clients handle one form only. A row wrapped over several lines becomes one
// row. A grid that does not parse cleanly (a one-column dialog box, a row
// whose cells do not match the border) stays as it was.
func boxTables(lines []string) []string {
	var out []string
	for i := 0; i < len(lines); {
		if cols := borderColumns(lines[i], boxTopLeft, boxTopRight, boxTopJoin); cols >= 2 {
			if table, next, ok := parseBoxTable(lines, i+1, cols); ok {
				indent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " "))]
				for _, l := range table {
					out = append(out, indent+l)
				}
				i = next
				continue
			}
		}
		out = append(out, lines[i])
		i++
	}
	return out
}

// parseBoxTable reads the grid below a top border of cols columns and returns
// its markdown lines and the index after it. The bottom border may be missing
// (the input-box cut can take it): the table then ends at its last row line.
func parseBoxTable(lines []string, start, cols int) ([]string, int, bool) {
	var groups [][][]string // lines between separators, each split into cells
	var group [][]string
	i := start
	for ; i < len(lines); i++ {
		if borderColumns(lines[i], boxBottomLeft, boxBottomRight, boxBottomJoin) == cols {
			i++
			break
		}
		if borderColumns(lines[i], boxMidLeft, boxMidRight, boxMidJoin) == cols {
			groups = append(groups, group)
			group = nil
			continue
		}
		cells, ok := rowCells(lines[i], cols)
		if !ok {
			break
		}
		group = append(group, cells)
	}
	groups = append(groups, group)
	if len(groups[0]) == 0 {
		return nil, 0, false
	}

	// Claude Code draws a separator under every row, so each group is a row.
	// With one separator (under the header) or none, each line is a row, and a
	// line whose first cell is empty continues the row above.
	var header []string
	var rows [][]string
	switch {
	case len(groups) > 2:
		header = joinCells(groups[0], cols)
		for _, g := range groups[1:] {
			if len(g) > 0 {
				rows = append(rows, joinCells(g, cols))
			}
		}
	case len(groups) == 2:
		header = joinCells(groups[0], cols)
		rows = linesToRows(groups[1], cols)
	default:
		header = joinCells(groups[0][:1], cols)
		rows = linesToRows(groups[0][1:], cols)
	}

	md := []string{pipeRow(header), pipeRow(repeated("---", cols))}
	for _, r := range rows {
		md = append(md, pipeRow(r))
	}
	return md, i, true
}

func linesToRows(lines [][]string, cols int) [][]string {
	var rows [][]string
	for _, line := range lines {
		if strings.TrimSpace(line[0]) == "" && len(rows) > 0 {
			rows[len(rows)-1] = joinCells([][]string{rows[len(rows)-1], line}, cols)
			continue
		}
		rows = append(rows, joinCells([][]string{line}, cols))
	}
	return rows
}

// borderColumns returns how many columns a border line spans (junctions + 1)
// when l is a border with the given corners and junctions, else 0.
func borderColumns(l, left, right, join string) int {
	r := []rune(strings.TrimSpace(l))
	if len(r) < 3 || !strings.ContainsRune(left, r[0]) || !strings.ContainsRune(right, r[len(r)-1]) {
		return 0
	}
	cols := 1
	for _, c := range r[1 : len(r)-1] {
		switch {
		case strings.ContainsRune(join, c):
			cols++
		case !strings.ContainsRune(boxHorizontal, c):
			return 0
		}
	}
	return cols
}

// rowCells splits a "│ a │ b │" line into its cells; ok only with cols cells.
func rowCells(l string, cols int) ([]string, bool) {
	r := []rune(strings.TrimSpace(l))
	if len(r) < 2 || !strings.ContainsRune(boxVertical, r[0]) || !strings.ContainsRune(boxVertical, r[len(r)-1]) {
		return nil, false
	}
	var cells []string
	var b strings.Builder
	for _, c := range r[1 : len(r)-1] {
		if strings.ContainsRune(boxVertical, c) {
			cells = append(cells, b.String())
			b.Reset()
			continue
		}
		b.WriteRune(c)
	}
	cells = append(cells, b.String())
	return cells, len(cells) == cols
}

// joinCells merges the lines of one row: each column's pieces joined by a space.
func joinCells(lines [][]string, cols int) []string {
	out := make([]string, cols)
	for c := range out {
		var parts []string
		for _, line := range lines {
			if p := strings.Join(strings.Fields(line[c]), " "); p != "" {
				parts = append(parts, p)
			}
		}
		out[c] = strings.Join(parts, " ")
	}
	return out
}

func pipeRow(cells []string) string {
	escaped := make([]string, len(cells))
	for i, c := range cells {
		escaped[i] = strings.ReplaceAll(c, "|", `\|`)
	}
	return "| " + strings.Join(escaped, " | ") + " |"
}

func repeated(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}
