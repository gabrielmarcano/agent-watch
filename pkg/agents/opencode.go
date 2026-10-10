package agents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
	_ "modernc.org/sqlite"
)

type opencodeAdapter struct {
	*genericAdapter
	cfg Config
}

func newOpenCodeAdapter(cfg Config) *opencodeAdapter {
	return &opencodeAdapter{
		genericAdapter: newGenericAdapter(),
		cfg:            cfg,
	}
}

// SplitScreenTurn implements ScreenTurnReader: OpenCode draws the user's
// message inside a "┃" frame, then the reply between a "Thought · …" line and
// a footer, neither of which is part of it: "▣  <mode> · <model> · <time>" in
// V1, "<mode> · <model> · <time> · <speed>" in V2. A sidebar on the right
// (session title, tokens, cost, LSP) shares the lines. V2 frames tool calls
// too ("$ ls -la", "← Edit note.txt"): those blocks are not the user's
// message, and they are left out of the reply.
func (o *opencodeAdapter) SplitScreenTurn(lines []string) (string, []string, bool) {
	var parts []string
	end := -1
	for i := len(lines) - 1; i >= 0 && end < 0; i-- {
		if !isFramedLine(strings.TrimSpace(lines[i])) {
			continue
		}
		start := i
		for start > 0 && isFramedLine(strings.TrimSpace(lines[start-1])) {
			start--
		}
		parts = parts[:0]
		for _, l := range lines[start : i+1] {
			if text := ocFramedText(l); text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 && ocStripIcon(parts[0]) == parts[0] {
			end = i
		}
		i = start
	}
	if end < 0 {
		return "", nil, false
	}
	var reply []string
	for _, l := range lines[end+1:] {
		if isFramedLine(strings.TrimSpace(l)) {
			continue
		}
		text := ocMainText(l)
		if t := strings.TrimSpace(text); strings.HasPrefix(t, "Thought · ") || strings.HasPrefix(t, "▣ ") || ocFooterV2.MatchString(t) {
			continue
		}
		if text == "" && len(reply) > 0 && reply[len(reply)-1] == "" {
			continue // a dropped tool block leaves its blank lines behind
		}
		reply = append(reply, text)
	}
	return strings.Join(parts, " "), trimBlankTail(reply), true
}

// ocFooterV2 is V2's footer under a reply: "Build · Muse Spark 1.3 Free ·
// 3.7s · 94.8 tok/s", "… · 2m 58s · …", "… · interrupted".
var ocFooterV2 = regexp.MustCompile(`^\p{L}[\p{L}\d -]* · .+ · (\d+h )?(\d+m )?\d+(\.\d+)?(ms|s|m)( · .*)?$`)

// ocFramedText returns the text of a "┃"-framed line, without the frame and
// the sidebar.
func ocFramedText(l string) string {
	frame := strings.Index(l, "┃")
	return strings.TrimSpace(ocMainText(l[frame+len("┃"):]))
}

// ocSidebarIndent: text that starts this far right is the sidebar's.
const ocSidebarIndent = 40

// ocMainText drops the sidebar from a line of OpenCode's screen: text after a
// gap of 4 or more spaces, or a line whose text starts at the sidebar's column.
func ocMainText(line string) string {
	indent := len(line) - len(strings.TrimLeft(line, " "))
	if indent >= ocSidebarIndent {
		return ""
	}
	if gap := strings.Index(line[indent:], "    "); gap >= 0 {
		line = line[:indent+gap]
	}
	return strings.TrimRight(line, " ")
}

func (o *opencodeAdapter) Name() string {
	return "opencode"
}

func (o *opencodeAdapter) PromptWhileWorking() bool {
	return true
}

// ParsePrompt reads OpenCode's dialogs (1.18.32). They are drawn inside a "┃"
// frame at the bottom of the screen, in place of the input box (which is
// closed by a "╹▀▀▀" line). Permission prompts are a button bar, not a
// numbered list; the question tool is a numbered list with an "esc dismiss"
// footer. A numbered list anywhere else is conversation text.
func (o *opencodeAdapter) ParsePrompt(screen string) (Prompt, bool) {
	lines := screenLines(screen)
	if p, ok := ocParseButtons(lines); ok {
		return p, true
	}
	return ocParseQuestion(lines)
}

// ocSidebarGap separates the dialog text from OpenCode's right-hand sidebar
// ("Context", "LSP", the cwd) on wide terminals.
var ocSidebarGap = regexp.MustCompile(` {8,}`)

// ocFrame returns the text inside OpenCode's "┃" frame, keeping its
// indentation and cutting off the sidebar, and whether the line is framed.
func ocFrame(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if !strings.HasPrefix(trimmed, "┃") {
		return "", false
	}
	inner := strings.TrimPrefix(trimmed, "┃")
	lead := len(inner) - len(strings.TrimLeft(inner, " "))
	if loc := ocSidebarGap.FindStringIndex(inner[lead:]); loc != nil {
		inner = inner[:lead+loc[0]]
	}
	return strings.TrimRight(inner, " "), true
}

// ocDialogStart returns the first line of the framed block that ends at
// anchor, provided the block is the open dialog: the input box ("╹") is not
// drawn below it.
func ocDialogStart(lines []string, anchor int) (int, bool) {
	for i := anchor + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimLeft(lines[i], " "), "╹") {
			return 0, false
		}
	}
	start := anchor
	for start > 0 {
		if _, framed := ocFrame(lines[start-1]); !framed {
			break
		}
		start--
	}
	return start, true
}

// ocStripIcon drops the one-rune icon OpenCode puts before a permission's
// action ("# Shell command", "→ Edit note.txt", "← Access external directory").
func ocStripIcon(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if size > 0 && !unicode.IsLetter(r) && !unicode.IsDigit(r) && strings.HasPrefix(s[size:], " ") {
		return strings.TrimSpace(s[size:])
	}
	return s
}

// ocParseButtons parses the permission dialog ("Allow once   Allow always
// Reject") and its "Always allow" confirmation stage ("Confirm   Cancel").
func ocParseButtons(lines []string) (Prompt, bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		inner, framed := ocFrame(lines[i])
		if !framed {
			continue
		}
		bar := strings.TrimSpace(inner)
		// V1 labels the second button "Allow always", V2 "Always allow".
		v2 := strings.Contains(bar, "Always allow")
		permission := strings.Contains(bar, "Allow once") && (v2 || strings.Contains(bar, "Allow always")) && strings.Contains(bar, "Reject")
		confirm := strings.HasPrefix(bar, "Confirm") && strings.Contains(bar, "Cancel")
		if !permission && !confirm {
			continue
		}
		start, ok := ocDialogStart(lines, i)
		if !ok {
			return Prompt{}, false
		}

		// "△ <title>" heads the dialog; the lines below it are the body.
		title := ""
		var body []string
		for _, l := range lines[start:i] {
			in, _ := ocFrame(l)
			c := strings.TrimSpace(in)
			if title == "" {
				if strings.HasPrefix(c, "△") {
					title = strings.TrimSpace(strings.TrimPrefix(c, "△"))
				}
				continue
			}
			if c != "" {
				body = append(body, c)
			}
		}
		if permission && title == "Permission required" {
			return ocPermissionPrompt(title, body, v2), true
		}
		if confirm && title == "Always allow" {
			return ocAlwaysPrompt(title, body), true
		}
		return Prompt{}, false
	}
	return Prompt{}, false
}

// ocPermissionPrompt builds the first stage. Keys assume the focus OpenCode
// gives the bar when it mounts ("Allow once"); see agents.md §5.1.
func ocPermissionPrompt(title string, body []string, v2 bool) Prompt {
	var action string
	var extra, patterns []string
	inPatterns := false
	for _, c := range body {
		switch {
		case action == "" && strings.HasPrefix(c, "$ "):
			action = c // V2 shows the shell command alone, without "# Shell command"
		case action == "":
			action = ocStripIcon(c)
		case c == "Patterns":
			inPatterns = true
		case inPatterns && strings.HasPrefix(c, "- "):
			patterns = append(patterns, strings.TrimPrefix(c, "- "))
		case strings.HasPrefix(c, "$ "), strings.HasPrefix(c, "Path: "):
			extra = append(extra, c) // the shell command the watch must show
		}
	}
	detail := ocJoinDetail(action, extra, patterns)

	if v2 {
		return ocPrompt(title, detail, []model.PromptOption{
			{ID: "opt-1", Label: "Allow once", Role: model.RoleAllowOnce},
			{ID: "opt-2", Label: "Always allow", Role: model.RoleAllowAlways},
			{ID: "opt-3", Label: "Reject", Role: model.RoleDeny},
		}, map[string][]string{
			"opt-1": {"Enter"},
			// V2 has no Confirm stage: Enter on "Always allow" applies it.
			"opt-2": {"Right", "Enter"},
			"opt-3": {"esc"},
		})
	}
	return ocPrompt(title, detail, []model.PromptOption{
		{ID: "opt-1", Label: "Allow once", Role: model.RoleAllowOnce},
		{ID: "opt-2", Label: "Allow always", Role: model.RoleAllowAlways},
		{ID: "opt-3", Label: "Reject", Role: model.RoleDeny},
	}, map[string][]string{
		"opt-1": {"Enter"},
		// "Allow always" opens a Confirm/Cancel stage with Confirm focused.
		"opt-2": {"Right", "Enter", "Enter"},
		"opt-3": {"esc"},
	})
}

// ocAlwaysPrompt builds the "Always allow" confirmation stage. Its esc goes
// back to the first stage (focus reset to "Allow once"); it does not reject.
func ocAlwaysPrompt(title string, body []string) Prompt {
	var text string
	var patterns []string
	for _, c := range body {
		switch {
		case strings.HasPrefix(c, "- "):
			patterns = append(patterns, strings.TrimPrefix(c, "- "))
		case text == "":
			text = c
		}
	}
	detail := ocJoinDetail(text, nil, patterns)

	return ocPrompt(title, detail, []model.PromptOption{
		{ID: "opt-1", Label: "Confirm", Role: model.RoleAllowAlways},
		{ID: "opt-2", Label: "Cancel", Role: classify("Cancel")},
	}, map[string][]string{
		"opt-1": {"Enter"},
		"opt-2": {"esc"},
	})
}

func ocJoinDetail(first string, extra, patterns []string) string {
	var parts []string
	if first != "" {
		parts = append(parts, first)
	}
	parts = append(parts, extra...)
	if len(patterns) > 0 {
		parts = append(parts, "Patterns: "+strings.Join(patterns, ", "))
	}
	return strings.Join(parts, "\n")
}

func ocPrompt(title, detail string, opts []model.PromptOption, keys map[string][]string) Prompt {
	detail = truncateRunes(detail, maxDetailRunes)
	kind := kindFor(opts)
	return Prompt{
		Public: model.PendingPrompt{
			Kind:        kind,
			Title:       title,
			Detail:      detail,
			Options:     opts,
			Fingerprint: model.Fingerprint(kind, title, detail, optionTexts(opts)),
		},
		Keys:       keys,
		CancelKeys: []string{"esc"},
	}
}

// ocParseQuestion parses the question tool's dialog: a numbered list whose
// footer is "↑↓ select  enter submit  esc dismiss". Digits pick an answer.
func ocParseQuestion(lines []string) (Prompt, bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		inner, framed := ocFrame(lines[i])
		if !framed || !strings.Contains(inner, "esc dismiss") {
			continue
		}
		start, ok := ocDialogStart(lines, i)
		if !ok {
			return Prompt{}, false
		}
		region := make([]string, 0, i-start)
		for _, l := range lines[start:i] {
			in, _ := ocFrame(l)
			region = append(region, in)
		}
		m, ok := findMenu(strings.Join(region, "\n"))
		if !ok {
			return Prompt{}, false
		}
		m.Title, m.Detail = extractTitleAndDetail(region, m.StartLine)
		// "Type your own answer" opens a text field the watch cannot fill.
		return withoutOptions(buildPrompt(m, digitKeys), func(o model.PromptOption) bool {
			return o.Label == "Type your own answer"
		}), true
	}
	return Prompt{}, false
}

type opencodeMessageData struct {
	Role string `json:"role"`
}

type opencodePartData struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (o *opencodeAdapter) LastTurn(ctx context.Context, ref SessionRef) (*model.HistoryItem, error) {
	if ref.Value == "" {
		return nil, ErrNoTranscript
	}

	dbPath := o.cfg.OpenCodeDBPath
	if _, err := os.Stat(dbPath); err != nil {
		return nil, ErrNoTranscript
	}

	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, ErrNoTranscript
	}
	defer db.Close()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// OpenCode V2 keeps the conversation in session_message; V1 in message
	// and part. A V2 database keeps the V1 tables, so V1 is read only when
	// V2 has no row for the session.
	item, err := o.lastTurnV2(ctx, db, ref.Value)
	if !errors.Is(err, errNoV2Session) {
		return item, err
	}
	return o.lastTurnV1(ctx, db, ref.Value)
}

// errNoV2Session: the database has no V2 session_message row for the session.
var errNoV2Session = errors.New("no V2 session")

// opencodeV2Message is the data of a V2 session_message row: a user message
// has its text in text, an assistant message (one per step) has content.
type opencodeV2Message struct {
	Text    string             `json:"text"`
	Content []opencodePartData `json:"content"`
}

func (o *opencodeAdapter) lastTurnV2(ctx context.Context, db *sql.DB, sessionID string) (*model.HistoryItem, error) {
	var tables int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'session_message'`).Scan(&tables); err != nil {
		return nil, ErrNoTranscript
	}
	if tables == 0 {
		return nil, errNoV2Session
	}

	// The query is the newest user message. A long turn has many assistant
	// steps after it, so it is looked up on its own, not within their window.
	var userSeq int64
	var userJSON string
	err := db.QueryRowContext(ctx, `
		SELECT seq, data FROM session_message
		WHERE session_id = ? AND type = 'user'
		ORDER BY seq DESC
		LIMIT 1;
	`, sessionID).Scan(&userSeq, &userJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNoV2Session
	}
	if err != nil {
		return nil, ErrNoTranscript
	}
	var user opencodeV2Message
	if err := json.Unmarshal([]byte(userJSON), &user); err != nil {
		return nil, ErrNoTranscript
	}
	query := strings.TrimSpace(user.Text)
	if query == "" {
		return nil, ErrNoTranscript
	}

	// The answer is the newest step after it with text: earlier steps are
	// tool calls, with or without a line of text before them.
	rows, err := db.QueryContext(ctx, `
		SELECT data FROM session_message
		WHERE session_id = ? AND type = 'assistant' AND seq > ?
		ORDER BY seq DESC
		LIMIT 40;
	`, sessionID, userSeq)
	if err != nil {
		return nil, ErrNoTranscript
	}
	defer rows.Close()
	var response string
	for response == "" && rows.Next() {
		var dataJSON string
		if err := rows.Scan(&dataJSON); err != nil {
			return nil, ErrNoTranscript
		}
		var m opencodeV2Message
		if err := json.Unmarshal([]byte(dataJSON), &m); err != nil {
			continue
		}
		response = ocJoinText(m.Content)
	}
	if err := rows.Err(); err != nil || response == "" {
		return nil, ErrNoTranscript
	}
	return &model.HistoryItem{
		Query:    query,
		Response: model.TruncateUTF8(response, model.MaxResponseBytes),
		Source:   "transcript",
	}, nil
}

// ocJoinText joins the text parts, leaving out reasoning and tool calls.
func ocJoinText(parts []opencodePartData) string {
	var texts []string
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.TrimSpace(strings.Join(texts, "\n"))
}

func (o *opencodeAdapter) lastTurnV1(ctx context.Context, db *sql.DB, sessionID string) (*model.HistoryItem, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, data FROM message
		WHERE session_id = ?
		ORDER BY time_created DESC, id DESC
		LIMIT 40;
	`, sessionID)
	if err != nil {
		return nil, ErrNoTranscript
	}
	defer rows.Close()

	type msgRecord struct {
		id   string
		role string
	}

	var messages []msgRecord
	for rows.Next() {
		var id string
		var dataJSON string
		if err := rows.Scan(&id, &dataJSON); err != nil {
			return nil, ErrNoTranscript
		}
		var mData opencodeMessageData
		if err := json.Unmarshal([]byte(dataJSON), &mData); err != nil {
			continue
		}
		messages = append(messages, msgRecord{id: id, role: mData.Role})
	}
	if err := rows.Err(); err != nil {
		return nil, ErrNoTranscript
	}

	// Find the newest user message and the newest assistant message after it
	// (messages are in DESC order, so newer messages appear earlier in the slice)
	userIdx := -1
	for i, m := range messages {
		if m.role == "user" {
			userIdx = i
			break
		}
	}
	if userIdx < 0 {
		return nil, ErrNoTranscript
	}

	// Read parts for user message
	query, err := o.readPartsText(ctx, db, messages[userIdx].id)
	if err != nil || query == "" {
		return nil, ErrNoTranscript
	}

	// OpenCode stores one assistant message per step of a turn, all newer than
	// the query (index < userIdx). The answer is the newest one with text:
	// earlier steps are tool calls, with or without a line of text before them.
	var response string
	for i := 0; i < userIdx && response == ""; i++ {
		if messages[i].role != "assistant" {
			continue
		}
		response, err = o.readPartsText(ctx, db, messages[i].id)
		if err != nil {
			return nil, ErrNoTranscript
		}
	}
	if response == "" {
		return nil, ErrNoTranscript
	}

	response = model.TruncateUTF8(response, model.MaxResponseBytes)

	return &model.HistoryItem{
		Query:    query,
		Response: response,
		Source:   "transcript",
	}, nil
}

func (o *opencodeAdapter) readPartsText(ctx context.Context, db *sql.DB, msgID string) (string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT data FROM part
		WHERE message_id = ?
		ORDER BY time_created, id
	`, msgID)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var textParts []string
	for rows.Next() {
		var dataJSON string
		if err := rows.Scan(&dataJSON); err != nil {
			return "", err
		}
		var pData opencodePartData
		if err := json.Unmarshal([]byte(dataJSON), &pData); err != nil {
			continue
		}
		if pData.Type == "text" && pData.Text != "" {
			textParts = append(textParts, pData.Text)
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	return strings.TrimSpace(strings.Join(textParts, "\n")), nil
}
