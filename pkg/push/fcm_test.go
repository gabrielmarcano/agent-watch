package push

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFCM_Payload(t *testing.T) {
	var capturedReq *http.Request
	var capturedPayload fcmMessagePayload

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		_ = json.NewDecoder(r.Body).Decode(&capturedPayload)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"name":"projects/test-proj/messages/123"}`))
	}))
	defer server.Close()

	tokens := []string{"device-token-1"}
	fcm := &FCM{
		ProjectID: "test-proj",
		Endpoint:  server.URL,
		Tokens:    func() []string { return tokens },
		Client:    server.Client(),
	}

	msg := Message{
		Event:          EventBlocked,
		PaneID:         "w5:pAE",
		Agent:          "claude",
		Label:          "bizum",
		Title:          "bizum needs approval",
		Body:           "Bash command: go test ./...",
		StateChangeSeq: 334,
		Fingerprint:    "9f2c61d0a4b3e871",
		AllowOptionID:  "opt-1",
		DenyOptionID:   "opt-3",
	}

	err := fcm.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("fcm.Send failed: %v", err)
	}

	if capturedReq == nil {
		t.Fatalf("no request received by fake server")
	}

	expectedPath := "/v1/projects/test-proj/messages:send"
	if capturedReq.URL.Path != expectedPath {
		t.Errorf("Path = %q, want %q", capturedReq.URL.Path, expectedPath)
	}
	if capturedReq.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", capturedReq.Header.Get("Content-Type"))
	}

	// Verify message fields
	if capturedPayload.Message.Token != "device-token-1" {
		t.Errorf("Token = %q, want device-token-1", capturedPayload.Message.Token)
	}
	if capturedPayload.Message.Android.Priority != "high" {
		t.Errorf("Priority = %q, want high", capturedPayload.Message.Android.Priority)
	}
	if capturedPayload.Message.Android.TTL != "600s" {
		t.Errorf("TTL = %q, want 600s", capturedPayload.Message.Android.TTL)
	}

	// Verify all 10 data keys are present and are strings
	expectedData := map[string]string{
		"event":            "blocked",
		"pane_id":          "w5:pAE",
		"agent":            "claude",
		"label":            "bizum",
		"title":            "bizum needs approval",
		"body":             "Bash command: go test ./...",
		"state_change_seq": "334",
		"fingerprint":      "9f2c61d0a4b3e871",
		"allow_option_id":  "opt-1",
		"deny_option_id":   "opt-3",
	}

	for k, expectedVal := range expectedData {
		actualVal, exists := capturedPayload.Message.Data[k]
		if !exists {
			t.Errorf("missing data key %q", k)
		} else if actualVal != expectedVal {
			t.Errorf("data[%q] = %q, want %q", k, actualVal, expectedVal)
		}
	}
}

func TestFCM_DonePriorityNormal(t *testing.T) {
	var capturedPayload fcmMessagePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	fcm := &FCM{
		ProjectID: "test-proj",
		Endpoint:  server.URL,
		Tokens:    func() []string { return []string{"tok-1"} },
		Client:    server.Client(),
	}

	msg := Message{
		Event: EventDone,
		Title: "done",
		Body:  "Task finished",
	}

	if err := fcm.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if capturedPayload.Message.Android.Priority != "normal" {
		t.Errorf("Priority for done event = %q, want 'normal'", capturedPayload.Message.Android.Priority)
	}
}

func TestFCM_InvalidTokens(t *testing.T) {
	var deadTokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload fcmMessagePayload
		_ = json.NewDecoder(r.Body).Decode(&payload)

		if strings.Contains(payload.Message.Token, "404") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"Requested entity was not found"}}`))
			return
		}
		if strings.Contains(payload.Message.Token, "unregistered") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"The registration token is not registered","status":"UNREGISTERED"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tokens := []string{"token-404", "token-unregistered", "token-valid"}
	fcm := &FCM{
		ProjectID: "test-proj",
		Endpoint:  server.URL,
		Tokens:    func() []string { return tokens },
		Client:    server.Client(),
		OnInvalidToken: func(token string) {
			deadTokens = append(deadTokens, token)
		},
	}

	msg := Message{Event: EventDone, Title: "test"}
	_ = fcm.Send(context.Background(), msg)

	if len(deadTokens) != 2 {
		t.Fatalf("expected 2 dead tokens, got %d: %v", len(deadTokens), deadTokens)
	}
	if deadTokens[0] != "token-404" || deadTokens[1] != "token-unregistered" {
		t.Errorf("unexpected dead tokens: %v", deadTokens)
	}
}
