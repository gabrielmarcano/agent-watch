package agents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// session-v2.json is a real OpenCode 2.0.25 session (session_message rows),
// captured in the sandbox: two shell turns, a declined edit, a question, a
// poem and a prompt queued while it was being written ("also say hi").
type openCodeV2Dump struct {
	SessionID string `json:"session_id"`
	Messages  []struct {
		ID          string          `json:"id"`
		Seq         int64           `json:"seq"`
		Type        string          `json:"type"`
		TimeCreated int64           `json:"time_created"`
		Data        json.RawMessage `json:"data"`
	} `json:"messages"`
}

func loadOpenCodeV2Dump(t *testing.T) openCodeV2Dump {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "opencode", "session-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	var dump openCodeV2Dump
	if err := json.Unmarshal(b, &dump); err != nil {
		t.Fatal(err)
	}
	return dump
}

// writeOpenCodeV2DB builds a V2 database holding the rows of dump up to and
// including seq last (all when last is 0), plus the V1 tables, which a V2
// database keeps.
func writeOpenCodeV2DB(t *testing.T, dump openCodeV2Dump, last int64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", "file:"+path+"?mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT);
		CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT);
		CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, type TEXT NOT NULL, seq INTEGER NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL);
	`); err != nil {
		t.Fatal(err)
	}
	for _, m := range dump.Messages {
		if last > 0 && m.Seq > last {
			break
		}
		if _, err := db.Exec("INSERT INTO session_message(id, session_id, type, seq, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?, ?)",
			m.ID, dump.SessionID, m.Type, m.Seq, m.TimeCreated, m.TimeCreated, string(m.Data)); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestOpenCodeV2LastTurn(t *testing.T) {
	dump := loadOpenCodeV2Dump(t)
	ref := SessionRef{Kind: "id", Value: dump.SessionID}
	cases := []struct {
		name          string
		last          int64
		query, prefix string
	}{
		// A queued prompt is a turn of its own.
		{"whole session", 0, "also say hi", "Hi"},
		// The answer is the newest step with text, not the step before the tool.
		{"tool turn", 25, "Run the shell command ls -la and tell me what you see", "I ran `ls -la` in `/tmp/aw-sandbox`:"},
		// The declined edit's last step has no text: the step before it answers.
		{"declined edit", 62, "Append the line world to note.txt", "Appending that line for you."},
		{"question", 93, "Ask me with a multiple-choice question which color I prefer: red, green or blue", "You chose Green."},
	}
	for _, c := range cases {
		ad := newOpenCodeAdapter(Config{OpenCodeDBPath: writeOpenCodeV2DB(t, dump, c.last)})
		item, err := ad.LastTurn(context.Background(), ref)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if item.Query != c.query || !strings.HasPrefix(item.Response, c.prefix) || item.Source != "transcript" {
			t.Errorf("%s: got %q → %q (%s)", c.name, item.Query, item.Response, item.Source)
		}
		if strings.Contains(item.Response, "Running ls -la for you.") {
			t.Errorf("%s: the reply holds the step before the tool: %q", c.name, item.Response)
		}
	}
}

// A turn still without an answer (the user's message is the newest row) and
// a user message without text give ErrNoTranscript, so the bridge falls back
// to the screen.
func TestOpenCodeV2LastTurn_NoAnswer(t *testing.T) {
	dump := loadOpenCodeV2Dump(t)
	ref := SessionRef{Kind: "id", Value: dump.SessionID}
	ad := newOpenCodeAdapter(Config{OpenCodeDBPath: writeOpenCodeV2DB(t, dump, 96)})
	if _, err := ad.LastTurn(context.Background(), ref); !errors.Is(err, ErrNoTranscript) {
		t.Errorf("unanswered turn: err = %v, want ErrNoTranscript", err)
	}

	path := writeOpenCodeV2DB(t, dump, 0)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO session_message VALUES ('m-files', ?, 'user', 999, 0, 0, '{"text":"","files":[{"name":"a.png"}]}')`, dump.SessionID); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := newOpenCodeAdapter(Config{OpenCodeDBPath: path}).LastTurn(context.Background(), ref); !errors.Is(err, ErrNoTranscript) {
		t.Errorf("user message without text: err = %v, want ErrNoTranscript", err)
	}
}

// A V2 database without rows for the session reads the V1 tables.
func TestOpenCodeV2LastTurn_FallsBackToV1Tables(t *testing.T) {
	v1 := loadOpenCodeDump(t, "session-multistep.json")
	path := writeOpenCodeDB(t, v1)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, type TEXT NOT NULL, seq INTEGER NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	b, err := os.ReadFile(filepath.Join("testdata", "opencode", "transcript-multistep.expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want model.HistoryItem
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	item, err := newOpenCodeAdapter(Config{OpenCodeDBPath: path}).LastTurn(context.Background(), SessionRef{Kind: "id", Value: v1.SessionID})
	if err != nil || item.Query != want.Query || item.Response != want.Response {
		t.Errorf("V1 fallback: item %+v, err %v", item, err)
	}
}

func TestOpenCodeV2LastTurn_Truncates(t *testing.T) {
	dump := loadOpenCodeV2Dump(t)
	path := writeOpenCodeV2DB(t, dump, 0)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	long, _ := json.Marshal(map[string]any{"content": []map[string]string{{"type": "text", "text": strings.Repeat("é", model.MaxResponseBytes)}}})
	if _, err := db.Exec(`INSERT INTO session_message VALUES ('m-long', ?, 'assistant', 999, 0, 0, ?)`, dump.SessionID, string(long)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	item, err := newOpenCodeAdapter(Config{OpenCodeDBPath: path}).LastTurn(context.Background(), SessionRef{Kind: "id", Value: dump.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Response) > model.MaxResponseBytes+len("\n\n…[truncated]") || !strings.HasSuffix(item.Response, "…[truncated]") {
		t.Errorf("response of %d bytes not truncated", len(item.Response))
	}
}

// A turn of more than 40 steps (the owner's real session had 43) still finds
// its user message.
func TestOpenCodeV2LastTurn_LongTurn(t *testing.T) {
	dump := loadOpenCodeV2Dump(t)
	path := writeOpenCodeV2DB(t, dump, 0)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO session_message VALUES ('m-user', ?, 'user', 1000, 0, 0, '{"text":"Fix the plugins"}')`, dump.SessionID); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 45; i++ {
		data := `{"content":[{"type":"tool","name":"shell"}]}`
		if i == 45 {
			data = `{"content":[{"type":"reasoning","text":"thinking"},{"type":"text","text":"Fixed."}]}`
		}
		if _, err := db.Exec(`INSERT INTO session_message VALUES (?, ?, 'assistant', ?, 0, 0, ?)`, fmt.Sprintf("m-step-%d", i), dump.SessionID, 1000+i, data); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	item, err := newOpenCodeAdapter(Config{OpenCodeDBPath: path}).LastTurn(context.Background(), SessionRef{Kind: "id", Value: dump.SessionID})
	if err != nil || item.Query != "Fix the plugins" || item.Response != "Fixed." {
		t.Errorf("long turn: item %+v, err %v", item, err)
	}
}
