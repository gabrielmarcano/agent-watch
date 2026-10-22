package agents

import (
	"context"
	"testing"
)

// turnEndAt is a Claude turn-end record with a chosen time.
func turnEndAt(uuid, ts string) string {
	return `{"type":"system","subtype":"turn_duration","durationMs":1200,"messageCount":4,"isMeta":false,"isSidechain":false,"uuid":"` +
		uuid + `","timestamp":"` + ts + `"}` + "\n"
}

// A finished Claude turn carries the transcript's times; a turn still being
// written is not returned by RecentTurns.
func TestClaudeRecentTurns(t *testing.T) {
	file := `{"type":"user","timestamp":"2026-10-05T10:00:00.500Z","message":{"role":"user","content":"first question"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-10-05T10:00:05.000Z","message":{"role":"assistant","content":[{"type":"text","text":"first answer"}]}}` + "\n" +
		turnEndAt("e1", "2026-10-05T10:00:06.000Z") +
		`{"type":"user","timestamp":"2026-10-05T10:01:00.000Z","message":{"role":"user","content":"second question"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-10-05T10:01:03.000Z","message":{"role":"assistant","content":[{"type":"text","text":"second answer"}]}}` + "\n" +
		turnEndAt("e2", "2026-10-05T10:01:04.000Z") +
		`{"type":"user","timestamp":"2026-10-05T10:02:00.000Z","message":{"role":"user","content":"still running"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-10-05T10:02:01.000Z","message":{"role":"assistant","content":[{"type":"text","text":"partial"}]}}` + "\n"
	path := writeFile(t, "h.jsonl", file)
	ad := newClaudeAdapter(Config{})

	items, err := ad.RecentTurns(context.Background(), SessionRef{Kind: "path", Value: path}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("turns = %d, want 2: %+v", len(items), items)
	}
	if items[0].Query != "second question" || items[0].Response != "second answer" ||
		items[0].AskedAt != "2026-10-05T10:01:00Z" || items[0].CompletedAt != "2026-10-05T10:01:04Z" {
		t.Errorf("newest = %+v", items[0])
	}
	if items[1].Query != "first question" || items[1].Response != "first answer" ||
		items[1].AskedAt != "2026-10-05T10:00:00Z" || items[1].CompletedAt != "2026-10-05T10:00:06Z" {
		t.Errorf("older = %+v", items[1])
	}

	one, err := ad.RecentTurns(context.Background(), SessionRef{Kind: "path", Value: path}, 1)
	if err != nil || len(one) != 1 || one[0].Query != "second question" {
		t.Errorf("max 1: %+v, %v", one, err)
	}
}

// Two agy turns: the answer is the last planner text before the next user
// input, not a tool-call step.
func TestAgyRecentTurns(t *testing.T) {
	file := `{"type":"USER_INPUT","created_at":"2026-09-24T00:58:27Z","content":"<USER_REQUEST>\nfirst\n</USER_REQUEST>"}` + "\n" +
		`{"type":"PLANNER_RESPONSE","created_at":"2026-09-24T00:58:28Z","content":"","tool_calls":[{"name":"run_command"}]}` + "\n" +
		`{"type":"PLANNER_RESPONSE","created_at":"2026-09-24T00:58:30Z","content":"first answer"}` + "\n" +
		`{"type":"USER_INPUT","created_at":"2026-09-24T01:05:51Z","content":"<USER_REQUEST>\nsecond\n</USER_REQUEST>"}` + "\n" +
		`{"type":"PLANNER_RESPONSE","created_at":"2026-09-24T01:05:55Z","content":"second answer"}` + "\n"
	path := writeFile(t, "a.jsonl", file)
	items, err := newAgyAdapter(Config{}).RecentTurns(context.Background(), SessionRef{Kind: "path", Value: path}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Query != "second" || items[1].Query != "first" {
		t.Fatalf("items = %+v", items)
	}
	if items[0].AskedAt != "2026-09-24T01:05:51Z" || items[0].CompletedAt != "2026-09-24T01:05:55Z" {
		t.Errorf("times = %q / %q", items[0].AskedAt, items[0].CompletedAt)
	}
}

// The V1 tables: each user message is paired with the newest assistant step
// before the next one.
func TestOpenCodeV1RecentTurns(t *testing.T) {
	dump := loadOpenCodeDump(t, "session-multistep.json")
	ad := newOpenCodeAdapter(Config{OpenCodeDBPath: writeOpenCodeDB(t, dump)})
	items, err := ad.RecentTurns(context.Background(), SessionRef{Kind: "id", Value: dump.SessionID}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("turns = %d, want 2", len(items))
	}
	if items[0].Response != "The file note.txt has 1 line." ||
		items[0].AskedAt != "2026-09-25T14:37:41Z" || items[0].CompletedAt != "2026-09-25T14:37:44Z" {
		t.Errorf("newest = %+v", items[0])
	}
	if items[1].Response != "You prefer green." {
		t.Errorf("older = %+v", items[1])
	}
}

// V2's session_message is read first, including turns whose answers come many
// steps after the question.
func TestOpenCodeV2RecentTurns(t *testing.T) {
	dump := loadOpenCodeV2Dump(t)
	ad := newOpenCodeAdapter(Config{OpenCodeDBPath: writeOpenCodeV2DB(t, dump, 0)})
	items, err := ad.RecentTurns(context.Background(), SessionRef{Kind: "id", Value: dump.SessionID}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("turns = %d, want 3", len(items))
	}
	if items[0].Query != "also say hi" || items[0].AskedAt != "2026-10-10T15:45:01Z" {
		t.Errorf("newest = %+v", items[0])
	}
	if items[1].Query[:10] != "Write a 40" {
		t.Errorf("second = %q", items[1].Query)
	}
	if items[2].Query[:10] != "Ask me wit" {
		t.Errorf("third = %q", items[2].Query)
	}
}
