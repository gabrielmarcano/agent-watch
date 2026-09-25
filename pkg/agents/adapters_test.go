package agents

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
	_ "modernc.org/sqlite"
)

type goldenFile struct {
	Prompt       *model.PendingPrompt `json:"prompt"`
	Keys         map[string][]string  `json:"keys"`
	CancelKeys   []string             `json:"cancel_keys"`
	VerifiedKeys bool                 `json:"verified_keys"`
}

func TestGoldenFixtures(t *testing.T) {
	registry := NewRegistry(Config{})

	agents := []string{"claude", "agy", "opencode"}
	for _, agent := range agents {
		agentDir := filepath.Join("testdata", agent)
		entries, err := os.ReadDir(agentDir)
		if err != nil {
			t.Fatalf("read agent dir %s: %v", agentDir, err)
		}

		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".txt") {
				continue
			}

			caseName := strings.TrimSuffix(e.Name(), ".txt")
			txtPath := filepath.Join(agentDir, e.Name())
			goldenPath := filepath.Join(agentDir, caseName+".golden.json")

			goldenBytes, err := os.ReadFile(goldenPath)
			if err != nil {
				// No golden file for this txt (e.g. .missing.md)
				continue
			}

			t.Run(agent+"/"+caseName, func(t *testing.T) {
				var golden goldenFile
				if err := json.Unmarshal(goldenBytes, &golden); err != nil {
					t.Fatalf("unmarshal golden JSON %s: %v", goldenPath, err)
				}

				screenBytes, err := os.ReadFile(txtPath)
				if err != nil {
					t.Fatalf("read txt file %s: %v", txtPath, err)
				}

				adapter := registry.For(agent)
				prompt, ok := adapter.ParsePrompt(string(screenBytes))

				if golden.Prompt == nil {
					if ok {
						t.Fatalf("expected prompt=null (ok=false), got ok=true: %+v", prompt)
					}
					return
				}

				if !ok {
					t.Fatalf("expected ok=true for %s, got false", txtPath)
				}

				// Compare Kind
				if prompt.Public.Kind != golden.Prompt.Kind {
					t.Errorf("Kind mismatch: got %v, want %v", prompt.Public.Kind, golden.Prompt.Kind)
				}

				// Compare Title
				if prompt.Public.Title != golden.Prompt.Title {
					t.Errorf("Title mismatch: got %q, want %q", prompt.Public.Title, golden.Prompt.Title)
				}

				// Compare Detail
				if prompt.Public.Detail != golden.Prompt.Detail {
					t.Errorf("Detail mismatch: got %q, want %q", prompt.Public.Detail, golden.Prompt.Detail)
				}

				// Compare Options
				if len(prompt.Public.Options) != len(golden.Prompt.Options) {
					t.Fatalf("Options len mismatch: got %d, want %d", len(prompt.Public.Options), len(golden.Prompt.Options))
				}

				var goldenLabels []string
				for i, wantOpt := range golden.Prompt.Options {
					gotOpt := prompt.Public.Options[i]
					goldenLabels = append(goldenLabels, wantOpt.Label)

					if gotOpt.ID != wantOpt.ID {
						t.Errorf("Option[%d].ID mismatch: got %q, want %q", i, gotOpt.ID, wantOpt.ID)
					}
					if gotOpt.Label != wantOpt.Label {
						t.Errorf("Option[%d].Label mismatch: got %q, want %q", i, gotOpt.Label, wantOpt.Label)
					}
					if gotOpt.Role != wantOpt.Role {
						t.Errorf("Option[%d].Role mismatch: got %v, want %v", i, gotOpt.Role, wantOpt.Role)
					}
				}

				// Compare Keys
				if !reflect.DeepEqual(prompt.Keys, golden.Keys) {
					t.Errorf("Keys mismatch: got %v, want %v", prompt.Keys, golden.Keys)
				}

				// Compare CancelKeys
				gotCancel := prompt.EffectiveCancelKeys(adapter)
				if !reflect.DeepEqual(gotCancel, golden.CancelKeys) {
					t.Errorf("CancelKeys mismatch: got %v, want %v", gotCancel, golden.CancelKeys)
				}

				// Compare Fingerprint
				expectedFP := model.Fingerprint(golden.Prompt.Kind, golden.Prompt.Title, golden.Prompt.Detail, goldenLabels)
				if prompt.Public.Fingerprint != expectedFP {
					t.Errorf("Fingerprint mismatch: got %q, want %q", prompt.Public.Fingerprint, expectedFP)
				}
			})
		}
	}
}

type expectedTranscript struct {
	Query    string `json:"query"`
	Response string `json:"response"`
}

func TestClaudeLastTurn(t *testing.T) {
	ctx := context.Background()

	// 1. Path resolution on captured testdata
	txtExpectedBytes, err := os.ReadFile(filepath.Join("testdata", "claude", "transcript.expected.json"))
	if err != nil {
		t.Fatalf("read transcript.expected.json: %v", err)
	}
	var expected expectedTranscript
	if err := json.Unmarshal(txtExpectedBytes, &expected); err != nil {
		t.Fatalf("unmarshal transcript.expected.json: %v", err)
	}

	transcriptPath := filepath.Join("testdata", "claude", "transcript.jsonl")
	adapter := newClaudeAdapter(Config{})

	item, err := adapter.LastTurn(ctx, SessionRef{
		Agent: "claude",
		Kind:  "path",
		Value: transcriptPath,
	})
	if err != nil {
		t.Fatalf("LastTurn path error: %v", err)
	}
	if item.Query != expected.Query {
		t.Errorf("query mismatch: got %q, want %q", item.Query, expected.Query)
	}
	if item.Response != expected.Response {
		t.Errorf("response mismatch: got %q, want %q", item.Response, expected.Response)
	}
	if item.Source != "transcript" {
		t.Errorf("source mismatch: got %q, want %q", item.Source, "transcript")
	}

	// 2. ID resolution with slug
	tmpDir := t.TempDir()
	slug := "-tmp-aw-sandbox"
	projDir := filepath.Join(tmpDir, "projects", slug)
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}
	testUUID := "test-session-uuid"
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projDir, testUUID+".jsonl"), data, 0644); err != nil {
		t.Fatalf("write temp jsonl: %v", err)
	}

	adapterID := newClaudeAdapter(Config{
		ClaudeConfigDirs: []string{tmpDir},
	})
	itemID, err := adapterID.LastTurn(ctx, SessionRef{
		Agent: "claude",
		Kind:  "id",
		Value: testUUID,
		CWD:   "/tmp/aw-sandbox",
	})
	if err != nil {
		t.Fatalf("LastTurn id error: %v", err)
	}
	if itemID.ID != testUUID {
		t.Errorf("expected sessionValue=%q, got %q", testUUID, itemID.ID)
	}
	if itemID.Response != expected.Response {
		t.Errorf("response mismatch: got %q, want %q", itemID.Response, expected.Response)
	}

	// 3. Synthetic case: tool_result skipped, sidechain skipped
	synthPath := filepath.Join(tmpDir, "synth.jsonl")
	synthContent := `{"type":"user","message":{"content":"initial prompt"},"isSidechain":false}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"1"}]},"isSidechain":false}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"1","content":"tool output"}]},"isSidechain":false}
{"type":"user","message":{"content":"sidechain prompt"},"isSidechain":true}
{"type":"assistant","message":{"content":[{"type":"text","text":"sidechain answer"}]},"isSidechain":true}
{"type":"assistant","message":{"content":[{"type":"text","text":"final answer"}]},"isSidechain":false}
`
	if err := os.WriteFile(synthPath, []byte(synthContent), 0644); err != nil {
		t.Fatalf("write synth: %v", err)
	}

	synthItem, err := adapter.LastTurn(ctx, SessionRef{
		Agent: "claude",
		Kind:  "path",
		Value: synthPath,
	})
	if err != nil {
		t.Fatalf("LastTurn synth error: %v", err)
	}
	if synthItem.Query != "initial prompt" {
		t.Errorf("expected Query='initial prompt', got %q", synthItem.Query)
	}
	if synthItem.Response != "final answer" {
		t.Errorf("expected Response='final answer', got %q", synthItem.Response)
	}

	// 4. Missing file -> ErrNoTranscript
	_, err = adapter.LastTurn(ctx, SessionRef{
		Agent: "claude",
		Kind:  "path",
		Value: filepath.Join(tmpDir, "nonexistent.jsonl"),
	})
	if err != ErrNoTranscript {
		t.Errorf("expected ErrNoTranscript for missing file, got %v", err)
	}
}

func TestAgyLastTurn(t *testing.T) {
	ctx := context.Background()

	txtExpectedBytes, err := os.ReadFile(filepath.Join("testdata", "agy", "transcript.expected.json"))
	if err != nil {
		t.Fatalf("read transcript.expected.json: %v", err)
	}
	var expected expectedTranscript
	if err := json.Unmarshal(txtExpectedBytes, &expected); err != nil {
		t.Fatalf("unmarshal transcript.expected.json: %v", err)
	}

	transcriptPath := filepath.Join("testdata", "agy", "transcript_full.jsonl")
	adapter := newAgyAdapter(Config{})

	item, err := adapter.LastTurn(ctx, SessionRef{
		Agent: "agy",
		Kind:  "path",
		Value: transcriptPath,
	})
	if err != nil {
		t.Fatalf("LastTurn path error: %v", err)
	}
	if item.Query != expected.Query {
		t.Errorf("query mismatch: got %q, want %q", item.Query, expected.Query)
	}
	if item.Response != expected.Response {
		t.Errorf("response mismatch: got %q, want %q", item.Response, expected.Response)
	}
	if item.Source != "transcript" {
		t.Errorf("source mismatch: got %q, want %q", item.Source, "transcript")
	}

	// Synthetic case: PLANNER_RESPONSE with tool_calls skipped
	tmpDir := t.TempDir()
	synthPath := filepath.Join(tmpDir, "synth_agy.jsonl")
	synthContent := `{"type":"USER_INPUT","content":"how are you?"}
{"type":"PLANNER_RESPONSE","content":"","tool_calls":[{"name":"check"}]}
{"type":"PLANNER_RESPONSE","content":"I am good!","tool_calls":[]}
`
	if err := os.WriteFile(synthPath, []byte(synthContent), 0644); err != nil {
		t.Fatalf("write synth: %v", err)
	}

	synthItem, err := adapter.LastTurn(ctx, SessionRef{
		Agent: "agy",
		Kind:  "path",
		Value: synthPath,
	})
	if err != nil {
		t.Fatalf("LastTurn synth error: %v", err)
	}
	if synthItem.Query != "how are you?" {
		t.Errorf("expected Query='how are you?', got %q", synthItem.Query)
	}
	if synthItem.Response != "I am good!" {
		t.Errorf("expected Response='I am good!', got %q", synthItem.Response)
	}

	// Missing file -> ErrNoTranscript
	_, err = adapter.LastTurn(ctx, SessionRef{
		Agent: "agy",
		Kind:  "path",
		Value: filepath.Join(tmpDir, "nonexistent.jsonl"),
	})
	if err != ErrNoTranscript {
		t.Errorf("expected ErrNoTranscript for missing file, got %v", err)
	}
}

func TestAgyUserRequest(t *testing.T) {
	cases := map[string]string{
		// Antigravity 1.2.10, first prompt of a session (settings block appended)
		"<USER_REQUEST>\nWithout using any tools, reply with a numbered list.\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-09-25T10:25:53-04:00.\n</ADDITIONAL_METADATA>\n<USER_SETTINGS_CHANGE>\nThe user changed setting `Model Selection`\n</USER_SETTINGS_CHANGE>": "Without using any tools, reply with a numbered list.",
		"<USER_REQUEST>\nline one\nline two\n</USER_REQUEST>":         "line one\nline two",
		"plain text without the wrapper\n":                            "plain text without the wrapper",
		"<USER_REQUEST>\nunterminated request\n<ADDITIONAL_METADATA>": "unterminated request\n<ADDITIONAL_METADATA>",
	}
	for in, want := range cases {
		if got := agyUserRequest(in); got != want {
			t.Errorf("agyUserRequest(%q) = %q, want %q", in, got, want)
		}
	}
}

type openCodeSessionDump struct {
	SessionID string `json:"session_id"`
	Messages  []struct {
		ID          string          `json:"id"`
		TimeCreated int64           `json:"time_created"`
		Data        json.RawMessage `json:"data"`
	} `json:"messages"`
	Parts []struct {
		ID          string          `json:"id"`
		MessageID   string          `json:"message_id"`
		TimeCreated int64           `json:"time_created"`
		Data        json.RawMessage `json:"data"`
	} `json:"parts"`
}

func TestOpenCodeLastTurn(t *testing.T) {
	ctx := context.Background()

	// Load session.json
	sessBytes, err := os.ReadFile(filepath.Join("testdata", "opencode", "session.json"))
	if err != nil {
		t.Fatalf("read session.json: %v", err)
	}
	var dump openCodeSessionDump
	if err := json.Unmarshal(sessBytes, &dump); err != nil {
		t.Fatalf("unmarshal session.json: %v", err)
	}

	// Build SQLite database in temp file
	tmpDB := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", "file:"+tmpDB+"?mode=rwc")
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT);
		CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT);
	`)
	if err != nil {
		t.Fatalf("create tables: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	for _, m := range dump.Messages {
		_, err := tx.Exec("INSERT INTO message(id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)",
			m.ID, dump.SessionID, m.TimeCreated, m.TimeCreated, string(m.Data))
		if err != nil {
			t.Fatalf("insert message: %v", err)
		}
	}
	for _, p := range dump.Parts {
		_, err := tx.Exec("INSERT INTO part(id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)",
			p.ID, p.MessageID, dump.SessionID, p.TimeCreated, p.TimeCreated, string(p.Data))
		if err != nil {
			t.Fatalf("insert part: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit tx: %v", err)
	}

	adapter := newOpenCodeAdapter(Config{
		OpenCodeDBPath: tmpDB,
	})

	item, err := adapter.LastTurn(ctx, SessionRef{
		Agent: "opencode",
		Kind:  "id",
		Value: dump.SessionID,
	})
	if err != nil {
		t.Fatalf("LastTurn error: %v", err)
	}

	txtExpectedBytes, err := os.ReadFile(filepath.Join("testdata", "opencode", "transcript.expected.json"))
	if err != nil {
		t.Fatalf("read transcript.expected.json: %v", err)
	}
	var expected expectedTranscript
	if err := json.Unmarshal(txtExpectedBytes, &expected); err != nil {
		t.Fatalf("unmarshal expected: %v", err)
	}

	if item.Query != expected.Query {
		t.Errorf("query mismatch: got %q, want %q", item.Query, expected.Query)
	}
	if item.Response != expected.Response {
		t.Errorf("response mismatch: got %q, want %q", item.Response, expected.Response)
	}

	// Missing DB -> ErrNoTranscript
	adapterMissing := newOpenCodeAdapter(Config{
		OpenCodeDBPath: filepath.Join(t.TempDir(), "nonexistent.db"),
	})
	_, err = adapterMissing.LastTurn(ctx, SessionRef{
		Agent: "opencode",
		Kind:  "id",
		Value: dump.SessionID,
	})
	if err != ErrNoTranscript {
		t.Errorf("expected ErrNoTranscript for missing db, got %v", err)
	}
}

// loadOpenCodeDump reads a session exported from the sandbox OpenCode DB.
func loadOpenCodeDump(t *testing.T, name string) openCodeSessionDump {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "opencode", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var dump openCodeSessionDump
	if err := json.Unmarshal(b, &dump); err != nil {
		t.Fatalf("unmarshal %s: %v", name, err)
	}
	return dump
}

// writeOpenCodeDB builds a temp SQLite DB with OpenCode's message/part tables
// from dump and returns its path.
func writeOpenCodeDB(t *testing.T, dump openCodeSessionDump) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", "file:"+path+"?mode=rwc")
	if err != nil {
		t.Fatalf("create sqlite db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT);
		CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT);
	`); err != nil {
		t.Fatalf("create tables: %v", err)
	}
	for _, m := range dump.Messages {
		if _, err := db.Exec("INSERT INTO message(id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)",
			m.ID, dump.SessionID, m.TimeCreated, m.TimeCreated, string(m.Data)); err != nil {
			t.Fatalf("insert message: %v", err)
		}
	}
	for _, p := range dump.Parts {
		if _, err := db.Exec("INSERT INTO part(id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)",
			p.ID, p.MessageID, dump.SessionID, p.TimeCreated, p.TimeCreated, string(p.Data)); err != nil {
			t.Fatalf("insert part: %v", err)
		}
	}
	return path
}

// truncateDump keeps the messages (and their parts) up to and including the
// message with id last, as if the session had ended there.
func truncateDump(dump openCodeSessionDump, last string) openCodeSessionDump {
	out := dump
	out.Messages = nil
	keep := map[string]bool{}
	for _, m := range dump.Messages {
		out.Messages = append(out.Messages, m)
		keep[m.ID] = true
		if m.ID == last {
			break
		}
	}
	out.Parts = nil
	for _, p := range dump.Parts {
		if keep[p.MessageID] {
			out.Parts = append(out.Parts, p)
		}
	}
	return out
}

// A real sandbox session where a turn has several assistant messages (one per
// step): the response is the final answer, not the step closest to the query.
func TestOpenCodeLastTurn_MultiStep(t *testing.T) {
	ctx := context.Background()
	dump := loadOpenCodeDump(t, "session-multistep.json")

	b, err := os.ReadFile(filepath.Join("testdata", "opencode", "transcript-multistep.expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var expected expectedTranscript
	if err := json.Unmarshal(b, &expected); err != nil {
		t.Fatal(err)
	}

	// Last turn: step 1 is a tool call with no text, step 2 is the answer.
	adapter := newOpenCodeAdapter(Config{OpenCodeDBPath: writeOpenCodeDB(t, dump)})
	item, err := adapter.LastTurn(ctx, SessionRef{Agent: "opencode", Kind: "id", Value: dump.SessionID})
	if err != nil {
		t.Fatalf("last turn with a tool-only first step: %v", err)
	}
	if item.Query != expected.Query || item.Response != expected.Response {
		t.Errorf("got query %q response %q, want %q / %q", item.Query, item.Response, expected.Query, expected.Response)
	}

	// Session cut after its first turn: step 1 says "Running wc -l on
	// note.txt." before the tool call, step 2 has the result.
	first := truncateDump(dump, "msg_0d8fb20810010zrFJm2Mn911dJ")
	if n := len(first.Messages); n != 3 {
		t.Fatalf("truncated dump has %d messages, want 3", n)
	}
	adapter = newOpenCodeAdapter(Config{OpenCodeDBPath: writeOpenCodeDB(t, first)})
	item, err = adapter.LastTurn(ctx, SessionRef{Agent: "opencode", Kind: "id", Value: dump.SessionID})
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if want := "Result: `1 note.txt`"; item.Response != want {
		t.Errorf("first turn response = %q, want the final step %q", item.Response, want)
	}
}
