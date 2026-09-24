package agents

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"

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

func (o *opencodeAdapter) Name() string {
	return "opencode"
}

func (o *opencodeAdapter) PromptWhileWorking() bool {
	return true
}

func (o *opencodeAdapter) ParsePrompt(screen string) (Prompt, bool) {
	cleanScreen := stripANSI(screen)
	cleanScreen = strings.ReplaceAll(cleanScreen, "\r\n", "\n")
	cleanScreen = strings.ReplaceAll(cleanScreen, "\r", "\n")
	lines := strings.Split(cleanScreen, "\n")

	// OpenCode permission prompt renders a horizontal button bar:
	// "Allow once   Allow always   Reject"
	buttonBarIdx := -1
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		if strings.Contains(l, "Allow once") && strings.Contains(l, "Allow always") && strings.Contains(l, "Reject") {
			buttonBarIdx = i
			break
		}
	}

	if buttonBarIdx < 0 {
		// Fall back to generic numbered menu if any
		return o.genericAdapter.ParsePrompt(screen)
	}

	// Parse Title and Detail above button bar
	var title string
	var actionLine string
	var patternLine string

	for i := buttonBarIdx - 1; i >= 0; i-- {
		curr := cleanBoxChars(lines[i])
		if curr == "" {
			continue
		}

		if strings.Contains(curr, "Permission required") {
			title = "Permission required"
			break
		}

		if strings.HasPrefix(curr, "← ") {
			actionLine = strings.TrimPrefix(curr, "← ")
		} else if strings.HasPrefix(curr, "- ") && patternLine == "" {
			patternLine = strings.TrimPrefix(curr, "- ")
		}
	}

	if title == "" {
		title = "Permission required"
	}

	var detailParts []string
	if actionLine != "" {
		detailParts = append(detailParts, actionLine)
	}
	if patternLine != "" {
		detailParts = append(detailParts, "Patterns: "+patternLine)
	}
	detail := strings.Join(detailParts, "\n")

	opts := []model.PromptOption{
		{ID: "opt-1", Label: "Allow once", Role: model.RoleAllowOnce},
		{ID: "opt-2", Label: "Allow always", Role: model.RoleAllowAlways},
		{ID: "opt-3", Label: "Reject", Role: model.RoleDeny},
	}

	keys := map[string][]string{
		"opt-1": {"Enter"},
		"opt-2": {"Right", "Enter"},
		"opt-3": {"esc"},
	}

	kind := model.PromptPermission
	labels := []string{"Allow once", "Allow always", "Reject"}
	fp := model.Fingerprint(kind, title, detail, labels)

	return Prompt{
		Public: model.PendingPrompt{
			Kind:        kind,
			Title:       title,
			Detail:      detail,
			Options:     opts,
			Fingerprint: fp,
		},
		Keys:       keys,
		CancelKeys: []string{"esc"},
	}, true
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

	rows, err := db.QueryContext(ctx, `
		SELECT id, data FROM message
		WHERE session_id = ?
		ORDER BY time_created DESC, id DESC
		LIMIT 40;
	`, ref.Value)
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

	// Assistant response message is newer than user message, so index < userIdx
	assistIdx := -1
	for i := userIdx - 1; i >= 0; i-- {
		if messages[i].role == "assistant" {
			assistIdx = i
			break
		}
	}
	if assistIdx < 0 {
		return nil, ErrNoTranscript
	}

	// Read parts for user message
	query, err := o.readPartsText(ctx, db, messages[userIdx].id)
	if err != nil || query == "" {
		return nil, ErrNoTranscript
	}

	// Read parts for assistant message
	response, err := o.readPartsText(ctx, db, messages[assistIdx].id)
	if err != nil || response == "" {
		return nil, ErrNoTranscript
	}

	response = model.TruncateUTF8(response, 16384)

	return &model.HistoryItem{
		ID:       ref.Value,
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
