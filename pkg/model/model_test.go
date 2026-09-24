package model_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"testing"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

func TestGoldenAgentState(t *testing.T) {
	state := model.AgentState{
		PaneID:         "w5:pAE",
		Agent:          "claude",
		Label:          "bizum",
		CWD:            "/Users/me/Code/app",
		WorkspaceID:    "w5",
		Status:         model.StatusBlocked,
		Focused:        false,
		StateChangeSeq: 334,
		Prompt: &model.PendingPrompt{
			Kind:   model.PromptPermission,
			Title:  "Bash command",
			Detail: "go test ./...",
			Options: []model.PromptOption{
				{ID: "opt-1", Label: "Yes", Role: model.RoleAllowOnce},
				{ID: "opt-2", Label: "Yes, and don't ask again for go test commands", Role: model.RoleAllowAlways},
				{ID: "opt-3", Label: "No, and tell Claude what to do differently", Role: model.RoleDeny},
			},
			Fingerprint: "9f2c61d0a4b3e871",
		},
		UpdatedAt: "2026-09-23T17:04:05Z",
	}

	gotBytes, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent failed: %v", err)
	}
	gotBytes = append(gotBytes, '\n')

	wantBytes, err := os.ReadFile("testdata/agent_state.json")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	if !bytes.Equal(gotBytes, wantBytes) {
		t.Errorf("Golden JSON mismatch:\nGot:\n%s\nWant:\n%s", string(gotBytes), string(wantBytes))
	}
}

func TestOmittedFields(t *testing.T) {
	// Non-blocked state should omit prompt
	state := model.AgentState{
		PaneID:         "w1:p1",
		Agent:          "agy",
		Label:          "worker",
		WorkspaceID:    "w1",
		Status:         model.StatusWorking,
		StateChangeSeq: 10,
		UpdatedAt:      model.Now(),
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	if bytes.Contains(data, []byte(`"prompt"`)) {
		t.Errorf("Expected prompt to be omitted in non-blocked state, got: %s", string(data))
	}

	// Unknown prompt should serialize options as [] not null
	prompt := model.PendingPrompt{
		Kind:        model.PromptUnknown,
		Title:       "Unknown prompt",
		Options:     []model.PromptOption{},
		Fingerprint: "12345678abcdef01",
		RawTail:     "line 1\nline 2",
	}

	pData, err := json.Marshal(prompt)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	if !bytes.Contains(pData, []byte(`"options":[]`)) {
		t.Errorf("Expected options:[], got: %s", string(pData))
	}
}

func TestRoundTrip(t *testing.T) {
	structs := []any{
		model.AgentState{
			PaneID:         "p1",
			Agent:          "claude",
			Label:          "test",
			Status:         model.StatusIdle,
			StateChangeSeq: 1,
			UpdatedAt:      model.Now(),
		},
		model.PendingPrompt{
			Kind:        model.PromptPermission,
			Title:       "Title",
			Detail:      "Detail",
			Options:     []model.PromptOption{{ID: "opt-1", Label: "Yes", Role: model.RoleAllowOnce}},
			Fingerprint: "abcdef0123456789",
		},
		model.HistoryItem{
			ID:          "hist-1",
			PaneID:      "p1",
			Agent:       "claude",
			Label:       "test",
			Query:       "hello",
			Response:    "world",
			Source:      "transcript",
			CompletedAt: model.Now(),
		},
		model.AgentsSnapshot{
			HostOnline:  true,
			HerdrOnline: true,
			Agents:      []model.AgentState{},
			GeneratedAt: model.Now(),
		},
		model.PairRequest{Code: "123456", DeviceName: "Watch"},
		model.PairResponse{DeviceID: "dev1", DeviceToken: "tok1"},
		model.PairCodeResponse{Code: "654321", ExpiresAt: model.Now()},
		model.HostStatusResponse{HostOnline: true, HerdrOnline: true, Devices: 1, Agents: 2},
		model.HistoryResponse{Items: []model.HistoryItem{}},
		model.PromptRequest{Text: "ls", ExpectedSeq: 5},
		model.AnswerRequest{OptionID: "opt-1", ExpectedSeq: 5, Fingerprint: "fp"},
		model.CancelRequest{ExpectedSeq: 5},
		model.PushRegisterRequest{Platform: "fcm", Token: "tok"},
		model.CommandResponse{OK: true},
		model.ErrorResponse{Error: model.ErrorBody{Code: "invalid_request", Message: "bad"}},
		model.HelloMsg{
			Type:          model.WireHello,
			Version:       "0.2.0",
			Host:          "my-mac",
			HerdrVersion:  "0.9.1",
			HerdrProtocol: 22,
			HerdrOnline:   true,
		},
		model.HerdrStatusMsg{Type: model.WireHerdrStatus, HerdrOnline: false},
		model.SnapshotMsg{Type: model.WireSnapshot, Agents: []model.AgentState{}},
		model.AgentUpdateMsg{Type: model.WireAgentUpdate, Agent: model.AgentState{PaneID: "p1"}},
		model.AgentRemovedMsg{Type: model.WireAgentRemoved, PaneID: "p1"},
		model.HistoryItemMsg{Type: model.WireHistoryItem, Item: model.HistoryItem{ID: "h1"}},
		model.CommandMsg{
			Type:        model.WireCommand,
			RequestID:   "req-1",
			Action:      "prompt",
			PaneID:      "p1",
			ExpectedSeq: 2,
			Text:        "hello",
		},
		model.CommandResultMsg{
			Type:      model.WireCommandResult,
			RequestID: "req-1",
			OK:        true,
		},
		model.ResyncMsg{Type: model.WireResync},
	}

	for _, s := range structs {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("Marshal(%T) failed: %v", s, err)
		}
		target := reflect.New(reflect.TypeOf(s)).Interface()
		if err := json.Unmarshal(b, target); err != nil {
			t.Fatalf("Unmarshal(%T) failed: %v", s, err)
		}
		elem := reflect.ValueOf(target).Elem().Interface()
		if !reflect.DeepEqual(s, elem) {
			t.Errorf("Round-trip mismatch for %T:\nSent: %+v\nGot:  %+v", s, s, elem)
		}
	}
}

func TestDecodeWire(t *testing.T) {
	cases := []struct {
		json     string
		wantType reflect.Type
	}{
		{`{"type":"hello","version":"0.2.0","host":"mac","herdr_version":"0.9.1","herdr_protocol":22,"herdr_online":true}`, reflect.TypeOf(model.HelloMsg{})},
		{`{"type":"herdr_status","herdr_online":true}`, reflect.TypeOf(model.HerdrStatusMsg{})},
		{`{"type":"snapshot","agents":[]}`, reflect.TypeOf(model.SnapshotMsg{})},
		{`{"type":"agent_update","agent":{"pane_id":"p1","agent":"claude","label":"l","workspace_id":"w","status":"idle","focused":false,"state_change_seq":1,"updated_at":"2026-09-23T17:04:05Z"}}`, reflect.TypeOf(model.AgentUpdateMsg{})},
		{`{"type":"agent_removed","pane_id":"p1"}`, reflect.TypeOf(model.AgentRemovedMsg{})},
		{`{"type":"history_item","item":{"id":"h1","pane_id":"p1","agent":"claude","label":"l","response":"resp","source":"screen","completed_at":"2026-09-23T17:04:05Z"}}`, reflect.TypeOf(model.HistoryItemMsg{})},
		{`{"type":"command","request_id":"r1","action":"prompt","pane_id":"p1","expected_seq":1}`, reflect.TypeOf(model.CommandMsg{})},
		{`{"type":"command_result","request_id":"r1","ok":true}`, reflect.TypeOf(model.CommandResultMsg{})},
		{`{"type":"resync"}`, reflect.TypeOf(model.ResyncMsg{})},
	}

	for _, c := range cases {
		msg, err := model.DecodeWire([]byte(c.json))
		if err != nil {
			t.Errorf("DecodeWire(%s) failed: %v", c.json, err)
			continue
		}
		if reflect.TypeOf(msg) != c.wantType {
			t.Errorf("DecodeWire(%s) = %T, want %v", c.json, msg, c.wantType)
		}
	}

	// Unknown type should error with ErrUnknownType
	_, err := model.DecodeWire([]byte(`{"type":"nope"}`))
	if err == nil || !errors.Is(err, model.ErrUnknownType) {
		t.Errorf("DecodeWire(nope) expected ErrUnknownType, got: %v", err)
	}

	// Malformed JSON should error
	_, err = model.DecodeWire([]byte(`{not json`))
	if err == nil {
		t.Errorf("DecodeWire(invalid json) expected error, got nil")
	}
}

func TestFingerprint(t *testing.T) {
	fp1 := model.Fingerprint(model.PromptPermission, "Title", "Detail", []string{"Yes", "No"})
	fp2 := model.Fingerprint(model.PromptPermission, "Title", "Detail", []string{"Yes", "No"})
	if fp1 != fp2 {
		t.Errorf("Fingerprint should be deterministic, got %s != %s", fp1, fp2)
	}
	if len(fp1) != 16 {
		t.Errorf("Fingerprint length should be 16, got %d (%s)", len(fp1), fp1)
	}

	fp3 := model.Fingerprint(model.PromptPermission, "Title", "Detail", []string{"Yes", "No, cancel"})
	if fp1 == fp3 {
		t.Errorf("Fingerprint should change when label changes")
	}
}

func TestHistoryID(t *testing.T) {
	id1 := model.HistoryID("w1:p1", "session-uuid", "query", "response")
	id2 := model.HistoryID("w1:p1", "session-uuid", "query", "response")
	if id1 != id2 {
		t.Errorf("HistoryID should be deterministic, got %s != %s", id1, id2)
	}
	if len(id1) != 16 {
		t.Errorf("HistoryID length should be 16, got %d (%s)", len(id1), id1)
	}
}

func TestTruncateUTF8(t *testing.T) {
	s := "Hello World"
	if got := model.TruncateUTF8(s, 20); got != s {
		t.Errorf("TruncateUTF8 with max > len = %q, want %q", got, s)
	}

	// Multi-byte runes: ñ is 2 bytes (0xC3, 0xB1)
	// "ñññ" is 6 bytes.
	ñ3 := "ñññ"
	// Truncating to 5 bytes: must cut at 4 bytes (2 characters), not in middle of 3rd char
	cut := model.TruncateUTF8(ñ3, 5)
	want := "ññ\n\n…[truncated]"
	if cut != want {
		t.Errorf("TruncateUTF8 multi-byte = %q, want %q", cut, want)
	}
}

func TestSortAgents(t *testing.T) {
	agents := []model.AgentState{
		{Label: "Z", Status: model.StatusIdle},
		{Label: "B", Status: model.StatusBlocked},
		{Label: "A", Status: model.StatusBlocked},
		{Label: "D", Status: model.StatusDone},
		{Label: "W", Status: model.StatusWorking},
		{Label: "U", Status: model.StatusUnknown},
	}

	model.SortAgents(agents)

	// Expected order:
	// 1. Blocked: A, B (label asc)
	// 2. Done: D
	// 3. Working: W
	// 4. Idle: Z
	// 5. Unknown: U
	expectedLabels := []string{"A", "B", "D", "W", "Z", "U"}
	for i, a := range agents {
		if a.Label != expectedLabels[i] {
			t.Errorf("SortAgents[%d] = %s (status %s), want %s", i, a.Label, a.Status, expectedLabels[i])
		}
	}
}

func TestErrorCodeHTTPStatus(t *testing.T) {
	cases := []struct {
		code       model.ErrorCode
		wantStatus int
	}{
		{model.ErrInvalidRequest, http.StatusBadRequest},
		{model.ErrUnauthorized, http.StatusUnauthorized},
		{model.ErrPairCodeInvalid, http.StatusForbidden},
		{model.ErrUnknownPane, http.StatusNotFound},
		{model.ErrStaleState, http.StatusConflict},
		{model.ErrPromptChanged, http.StatusConflict},
		{model.ErrAgentBusy, http.StatusConflict},
		{model.ErrAgentBlocked, http.StatusConflict},
		{model.ErrAgentStateUnknown, http.StatusConflict},
		{model.ErrUnknownOption, http.StatusConflict},
		{model.ErrRateLimited, http.StatusTooManyRequests},
		{model.ErrHostOffline, http.StatusServiceUnavailable},
		{model.ErrHerdrOffline, http.StatusServiceUnavailable},
		{model.ErrTimeout, http.StatusGatewayTimeout},
		{model.ErrInternal, http.StatusInternalServerError},
	}

	for _, c := range cases {
		if got := c.code.HTTPStatus(); got != c.wantStatus {
			t.Errorf("ErrorCode(%s).HTTPStatus() = %d, want %d", c.code, got, c.wantStatus)
		}
	}
}
