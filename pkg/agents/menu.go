package agents

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

var (
	ansiRegex = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	// reOption matches the content of an option line once lineContent has
	// removed its indentation and any box border around it.
	reOption = regexp.MustCompile(`^(?:([❯›>▶●])\s*)?(\d+)[.)]\s+(.+?)\s*$`)
)

// maxDetailRunes caps PendingPrompt.Detail (agents.md §2: max 400 chars).
const maxDetailRunes = 400

// maxDialogTail is how many non-empty lines may follow a menu that is still
// the open dialog: footers and hints, never the agent's input box.
const maxDialogTail = 6

// verticalBorders are the box characters that may frame a line on the left
// or right ("│ 1. Yes │", OpenCode's "┃").
const verticalBorders = "│┃║"

// screenLines normalises a captured screen: ANSI stripped, CR/CRLF turned
// into LF, split into lines.
func screenLines(screen string) []string {
	s := stripANSI(screen)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(s, "\n")
}

// lineContent returns the column (in runes) where a line's content starts,
// counting spaces, tabs and vertical borders on the left as blank, and the
// content with blanks and borders trimmed from both ends.
func lineContent(line string) (int, string) {
	indent := 0
	rest := line
	for len(rest) > 0 {
		r, size := utf8.DecodeRuneInString(rest)
		if r != ' ' && r != '\t' && !strings.ContainsRune(verticalBorders, r) {
			break
		}
		indent++
		rest = rest[size:]
	}
	return indent, strings.TrimRight(rest, " \t"+verticalBorders)
}

// matchOption parses an option line. labelCol is the column where the label
// text starts; continuation lines of that option are indented to it.
func matchOption(line string) (opt menuOption, labelCol int, ok bool) {
	indent, content := lineContent(line)
	loc := reOption.FindStringSubmatchIndex(content)
	if loc == nil {
		return menuOption{}, 0, false
	}
	num, _ := strconv.Atoi(content[loc[4]:loc[5]])
	opt = menuOption{
		Number: num,
		Label:  cleanBoxChars(content[loc[6]:loc[7]]),
		Cursor: loc[2] >= 0,
	}
	return opt, indent + utf8.RuneCountInString(content[:loc[6]]), true
}

// isKeyHint reports whether s is a key hint the TUI prints under an option
// (Claude's "shift+tab to approve with this feedback"), not label text.
func isKeyHint(s string) bool {
	l := strings.ToLower(strings.TrimSpace(s))
	for _, p := range []string{"shift+", "ctrl+", "alt+", "tab to ", "esc to ", "enter to ", "↑/↓"} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

// truncateRunes cuts s to at most max runes, never inside a UTF-8 sequence.
func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return s[:i]
		}
		n++
	}
	return s
}

// dialogAtBottom reports whether the menu is still the open dialog: nothing
// but a few footer lines may follow it, and none of them may be the agent's
// input line (a line starting with inputMarker, e.g. Claude's "❯", agy's ">").
// Agents draw their input box below the conversation when idle or working
// and hide it while a dialog is open, so a numbered list in an answer always
// has the input box after it.
func dialogAtBottom(lines []string, m parsedMenu, inputMarker string) bool {
	tail := 0
	for i := m.EndLine + 1; i < len(lines); i++ {
		_, content := lineContent(lines[i])
		if content == "" {
			continue
		}
		tail++
		if tail > maxDialogTail {
			return false
		}
		if content == inputMarker || strings.HasPrefix(content, inputMarker+" ") {
			return false
		}
	}
	return true
}

type menuOption struct {
	Number int
	Label  string
	// Description holds the option's continuation lines (the description a
	// question prints under each answer, or a label that wrapped).
	Description string
	Cursor      bool
}

type parsedMenu struct {
	Title     string
	Detail    string
	Options   []menuOption
	StartLine int
	EndLine   int
}

// stripANSI removes ANSI escape codes from s.
func stripANSI(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

// cleanBoxChars trims box-drawing characters and surrounding whitespace.
func cleanBoxChars(s string) string {
	s = strings.TrimLeft(s, " │┌┐└┘├┤┬┴┼╭╮╯╰═║╔╗╚╝╠╣╦╩╬╌╍╎╏┃\t")
	s = strings.TrimRight(s, " │┌┐└┘├┤┬┴┼╭╮╯╰═║╔╗╚╝╠╣╦╩╬╌╍╎╏┃\t")
	return strings.TrimSpace(s)
}

// isBoxOrSepLine returns true if s consists entirely of box-drawing, rule, or whitespace chars.
func isBoxOrSepLine(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	for _, r := range trimmed {
		if !strings.ContainsRune("─│┌┐└┘├┤┬┴┼╭╮╯╰═║╔╗╚╝╠╣╦╩╬╌╍╎╏┃-_=~ ", r) {
			return false
		}
	}
	return true
}

// classify maps an option label to a semantic model.OptionRole based on keywords per agents.md §2.
// Never maps by position.
func classify(label string) model.OptionRole {
	l := strings.ToLower(label)
	// Claude writes "don’t" with a typographic apostrophe in some menus.
	l = strings.NewReplacer("’", "'", "‘", "'").Replace(l)

	// allow_always checked first
	if strings.Contains(l, "don't ask again") ||
		strings.Contains(l, "always") ||
		strings.Contains(l, "for this session") ||
		strings.Contains(l, "all edits") ||
		strings.Contains(l, "for all") ||
		strings.Contains(l, "auto mode") ||
		strings.Contains(l, "auto-approve") {
		return model.RoleAllowAlways
	}

	// allow_once
	if l == "yes" || strings.HasPrefix(l, "yes ") || strings.HasPrefix(l, "yes,") || strings.HasPrefix(l, "yes.") || strings.HasPrefix(l, "yes-") ||
		strings.Contains(l, "allow") ||
		strings.Contains(l, "approve") ||
		strings.Contains(l, "proceed") ||
		strings.Contains(l, "accept") {
		return model.RoleAllowOnce
	}

	// deny
	if l == "no" || strings.HasPrefix(l, "no ") || strings.HasPrefix(l, "no,") || strings.HasPrefix(l, "no.") || strings.HasPrefix(l, "no-") ||
		strings.Contains(l, "deny") ||
		strings.Contains(l, "reject") ||
		strings.Contains(l, "cancel") ||
		strings.Contains(l, "decline") {
		return model.RoleDeny
	}

	// Default fallback: choice (multiple-choice questions, plan approval, etc.)
	return model.RoleChoice
}

// kindFor determines the model.PromptKind from option roles per contracts.md §1.3.
func kindFor(options []model.PromptOption) model.PromptKind {
	if len(options) == 0 {
		return model.PromptUnknown
	}
	hasAllow := false
	hasDeny := false
	for _, opt := range options {
		if opt.Role == model.RoleAllowOnce || opt.Role == model.RoleAllowAlways {
			hasAllow = true
		} else if opt.Role == model.RoleDeny {
			hasDeny = true
		}
	}
	if hasAllow && hasDeny {
		return model.PromptPermission
	}
	return model.PromptQuestion
}

// digitKeys returns ["<number>"] for digit selection TUIs.
func digitKeys(o menuOption, _ int, _ parsedMenu) []string {
	return []string{fmt.Sprint(o.Number)}
}

// arrowKeys calculates Up/Down relative to the focused cursor option, ending with "Enter".
func arrowKeys(o menuOption, idx int, m parsedMenu) []string {
	cursorIdx := 0
	for i, opt := range m.Options {
		if opt.Cursor {
			cursorIdx = i
			break
		}
	}

	diff := idx - cursorIdx
	var keys []string
	if diff > 0 {
		for i := 0; i < diff; i++ {
			keys = append(keys, "Down")
		}
	} else if diff < 0 {
		for i := 0; i < -diff; i++ {
			keys = append(keys, "Up")
		}
	}
	keys = append(keys, "Enter")
	return keys
}

// buildPrompt constructs a Prompt struct with Public model and private key map.
func buildPrompt(m parsedMenu, keysFor func(o menuOption, idx int, m parsedMenu) []string) Prompt {
	var opts []model.PromptOption
	keys := make(map[string][]string)

	for idx, opt := range m.Options {
		id := fmt.Sprintf("opt-%d", opt.Number)
		if opt.Number == 0 {
			id = fmt.Sprintf("opt-%d", idx+1)
		}
		public := model.PromptOption{ID: id, Label: opt.Label, Description: opt.Description}
		// The role comes from the full text: "don't ask again" may sit on a continuation line.
		public.Role = classify(public.Text())
		opts = append(opts, public)
		if keysFor != nil {
			keys[id] = keysFor(opt, idx, m)
		}
	}
	if opts == nil {
		opts = []model.PromptOption{}
	}

	kind := kindFor(opts)
	detail := truncateRunes(m.Detail, maxDetailRunes)
	fp := model.Fingerprint(kind, m.Title, detail, optionTexts(opts))

	return Prompt{
		Public: model.PendingPrompt{
			Kind:        kind,
			Title:       m.Title,
			Detail:      detail,
			Options:     opts,
			Fingerprint: fp,
		},
		Keys: keys,
	}
}

// optionTexts lists the options' full texts, as the fingerprint hashes them.
func optionTexts(opts []model.PromptOption) []string {
	texts := make([]string, len(opts))
	for i, o := range opts {
		texts[i] = o.Text()
	}
	return texts
}

// withoutOptions drops the options drop matches (answers the watch cannot
// give, such as a free-text entry) and recomputes the kind and fingerprint of
// what remains. The other options keep their ids and keys.
func withoutOptions(p Prompt, drop func(model.PromptOption) bool) Prompt {
	kept := []model.PromptOption{}
	for _, o := range p.Public.Options {
		if drop(o) {
			delete(p.Keys, o.ID)
			continue
		}
		kept = append(kept, o)
	}
	p.Public.Options = kept
	p.Public.Kind = kindFor(kept)
	p.Public.Fingerprint = model.Fingerprint(p.Public.Kind, p.Public.Title, p.Public.Detail, optionTexts(kept))
	return p
}

// findMenu scans screen text for the LAST numbered block of >= 2 options per agents.md §2.
// It knows nothing about dialogs: adapters check that the block is really an
// open dialog (see dialogAtBottom) before trusting it.
func findMenu(screen string) (parsedMenu, bool) {
	rawLines := screenLines(screen)

	type rawBlock struct {
		startLine int
		endLine   int
		options   []menuOption
	}

	var blocks []rawBlock
	i := 0
	n := len(rawLines)

	for i < n {
		if _, _, ok := matchOption(rawLines[i]); !ok {
			i++
			continue
		}

		// Found potential first option of a menu block
		startIdx := i
		var currentOpts []menuOption

		for i < n {
			currLine := strings.TrimRight(rawLines[i], " \t")

			if opt, labelCol, ok := matchOption(currLine); ok {
				currentOpts = append(currentOpts, opt)
				i++

				// Up to 2 continuation lines, indented to the label column
				// (a little deeper is tolerated). A footer at the menu's own
				// indentation ("  Esc to cancel" under "  2. No") is not one.
				contCount := 0
				for i < n && contCount < 2 {
					nextLine := strings.TrimRight(rawLines[i], " \t")
					indent, content := lineContent(nextLine)

					if content == "" || isBoxOrSepLine(nextLine) {
						break
					}
					if _, _, isOpt := matchOption(nextLine); isOpt {
						break
					}
					if indent < labelCol || indent > labelCol+4 {
						break
					}
					if !isKeyHint(content) {
						last := &currentOpts[len(currentOpts)-1]
						last.Description = strings.TrimSpace(last.Description + " " + cleanBoxChars(content))
					}
					contCount++
					i++
				}
				continue
			}

			// If current line is a separator line (like ─────), peek if next line is an option
			if isBoxOrSepLine(currLine) && i+1 < n {
				if _, _, ok := matchOption(rawLines[i+1]); ok {
					i++ // skip separator inside menu block
					continue
				}
			}

			// Not an option and not an internal separator -> block ends
			break
		}

		if len(currentOpts) >= 2 {
			blocks = append(blocks, rawBlock{
				startLine: startIdx,
				endLine:   i - 1,
				options:   currentOpts,
			})
		}
	}

	if len(blocks) == 0 {
		return parsedMenu{}, false
	}

	// Pick the LAST block
	lastBlock := blocks[len(blocks)-1]

	// Check cursor: if none has cursor, assume first
	hasCursor := false
	for _, opt := range lastBlock.options {
		if opt.Cursor {
			hasCursor = true
			break
		}
	}
	if !hasCursor && len(lastBlock.options) > 0 {
		lastBlock.options[0].Cursor = true
	}

	// Extract Title and Detail above the menu block
	title, detail := extractTitleAndDetail(rawLines, lastBlock.startLine)

	return parsedMenu{
		Title:     title,
		Detail:    detail,
		Options:   lastBlock.options,
		StartLine: lastBlock.startLine,
		EndLine:   lastBlock.endLine,
	}, true
}

// extractTitleAndDetail extracts Title and Detail from lines preceding menuStartLine.
func extractTitleAndDetail(lines []string, menuStartLine int) (string, string) {
	// Look backwards from menuStartLine
	end := menuStartLine - 1
	for end >= 0 && strings.TrimSpace(lines[end]) == "" {
		end--
	}
	if end < 0 {
		return "", ""
	}

	// Check if there is a horizontal separator line above the menu
	sepIdx := -1
	for j := end; j >= 0 && j >= menuStartLine-15; j-- {
		if isBoxOrSepLine(lines[j]) {
			sepIdx = j
			break
		}
	}

	var title string
	var detailLines []string

	if sepIdx >= 0 {
		// Case 1: Title right above the separator (e.g. Agy: Command \n ───────)
		aboveSep := sepIdx - 1
		for aboveSep >= 0 && strings.TrimSpace(lines[aboveSep]) == "" {
			aboveSep--
		}

		// Case 2: Title right below the separator (e.g. Claude: ─────── \n Bash command)
		belowSep := sepIdx + 1
		for belowSep <= end && strings.TrimSpace(lines[belowSep]) == "" {
			belowSep++
		}

		if aboveSep >= 0 && !isBoxOrSepLine(lines[aboveSep]) && strings.TrimSpace(lines[aboveSep]) != "" {
			title = cleanBoxChars(lines[aboveSep])
			for k := sepIdx + 1; k <= end; k++ {
				cleaned := cleanBoxChars(lines[k])
				if cleaned != "" && !isBoxOrSepLine(lines[k]) {
					detailLines = append(detailLines, cleaned)
				}
			}
		} else if belowSep <= end {
			title = cleanBoxChars(lines[belowSep])
			for k := belowSep + 1; k <= end; k++ {
				cleaned := cleanBoxChars(lines[k])
				if cleaned != "" && !isBoxOrSepLine(lines[k]) {
					detailLines = append(detailLines, cleaned)
				}
			}
		}
	}

	// Fallback if no separator or title wasn't found
	if title == "" {
		// Collect contiguous non-empty lines above menu
		var block []string
		for j := end; j >= 0; j-- {
			cleaned := cleanBoxChars(lines[j])
			if cleaned == "" || isBoxOrSepLine(lines[j]) {
				if len(block) > 0 {
					break
				}
				continue
			}
			block = append([]string{cleaned}, block...)
		}

		if len(block) > 0 {
			title = block[0]
			detailLines = block[1:]
		}
	}

	return title, truncateRunes(strings.Join(detailLines, "\n"), maxDetailRunes)
}
