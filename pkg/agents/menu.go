package agents

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

var (
	ansiRegex = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	reOption  = regexp.MustCompile(`^\s*(?:([❯›>▶●])\s*)?(\d+)[.)]\s+(.+?)\s*$`)
)

type menuOption struct {
	Number int
	Label  string
	Cursor bool
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
		role := classify(opt.Label)
		opts = append(opts, model.PromptOption{
			ID:    id,
			Label: opt.Label,
			Role:  role,
		})
		if keysFor != nil {
			keys[id] = keysFor(opt, idx, m)
		}
	}
	if opts == nil {
		opts = []model.PromptOption{}
	}

	kind := kindFor(opts)
	var labels []string
	for _, o := range opts {
		labels = append(labels, o.Label)
	}
	fp := model.Fingerprint(kind, m.Title, m.Detail, labels)

	return Prompt{
		Public: model.PendingPrompt{
			Kind:        kind,
			Title:       m.Title,
			Detail:      m.Detail,
			Options:     opts,
			Fingerprint: fp,
		},
		Keys: keys,
	}
}

// findMenu scans screen text for the LAST numbered block of >= 2 options per agents.md §2.
func findMenu(screen string) (parsedMenu, bool) {
	cleanScreen := stripANSI(screen)
	cleanScreen = strings.ReplaceAll(cleanScreen, "\r\n", "\n")
	cleanScreen = strings.ReplaceAll(cleanScreen, "\r", "\n")
	rawLines := strings.Split(cleanScreen, "\n")

	type rawBlock struct {
		startLine int
		endLine   int
		options   []menuOption
	}

	var blocks []rawBlock
	i := 0
	n := len(rawLines)

	for i < n {
		line := strings.TrimRight(rawLines[i], " \t")
		lineClean := strings.Trim(line, "│┃")

		match := reOption.FindStringSubmatch(lineClean)
		if match == nil {
			i++
			continue
		}

		// Found potential first option of a menu block
		startIdx := i
		var currentOpts []menuOption

		for i < n {
			currLine := strings.TrimRight(rawLines[i], " \t")
			currClean := strings.Trim(currLine, "│┃")

			sub := reOption.FindStringSubmatch(currClean)
			if sub != nil {
				num, _ := strconv.Atoi(sub[2])
				cursor := sub[1] != ""
				label := cleanBoxChars(sub[3])

				currentOpts = append(currentOpts, menuOption{
					Number: num,
					Label:  label,
					Cursor: cursor,
				})
				i++

				// Check up to 2 continuation lines following this option
				contCount := 0
				for i < n && contCount < 2 {
					nextLine := strings.TrimRight(rawLines[i], " \t")
					nextClean := cleanBoxChars(nextLine)

					// Stop continuation if empty, separator line, or next option
					if nextClean == "" || isBoxOrSepLine(nextLine) || reOption.MatchString(strings.Trim(nextLine, "│┃")) {
						break
					}

					// Continuation line must be indented
					trimmedLeft := strings.TrimLeft(nextLine, " │┃\t")
					leadingSpaces := len(nextLine) - len(trimmedLeft)
					if leadingSpaces >= 2 {
						currentOpts[len(currentOpts)-1].Label += " " + nextClean
						contCount++
						i++
					} else {
						break
					}
				}
				continue
			}

			// If current line is a separator line (like ─────), peek if next line is an option
			if isBoxOrSepLine(currLine) && i+1 < n && reOption.MatchString(strings.Trim(rawLines[i+1], "│┃")) {
				i++ // skip separator inside menu block
				continue
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

	detail := strings.Join(detailLines, "\n")
	if len(detail) > 400 {
		detail = detail[:400]
	}

	return title, detail
}
