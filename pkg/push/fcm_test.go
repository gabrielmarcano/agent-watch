package push

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
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

// state_change_seq is a decimal string even when it is 0 (a digest, or an
// agent herdr has not counted yet): the app parses it as a number.
func TestFCM_ZeroStateChangeSeqIsSent(t *testing.T) {
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

	if err := fcm.Send(context.Background(), Message{Event: EventDigest, Title: "4 agents need you"}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	got, ok := capturedPayload.Message.Data["state_change_seq"]
	if !ok || got != "0" {
		t.Fatalf(`data["state_change_seq"] = %q (present: %t), want "0"`, got, ok)
	}
}

func TestFCM_InvalidTokens(t *testing.T) {
	var deadTokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload fcmMessagePayload
		_ = json.NewDecoder(r.Body).Decode(&payload)

		if strings.Contains(payload.Message.Token, "bare-404") {
			// A 404 without FCM's error code: e.g. a wrong project id.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(fcmErrBare404))
			return
		}
		if strings.Contains(payload.Message.Token, "404") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(fcmErrUnregistered))
			return
		}
		if strings.Contains(payload.Message.Token, "unregistered") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"The registration token is not registered","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tokens := []string{"token-404", "token-bare-404", "token-unregistered", "token-valid"}
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

// Real FCM v1 error bodies. Only errors about the token itself mean the token
// is dead; payload errors also come back as 400 INVALID_ARGUMENT.
const (
	fcmErrTTL             = `{"error":{"code":400,"message":"Invalid value at 'message.android.ttl' (type.googleapis.com/google.protobuf.Duration), Field 'ttl', Illegal duration format","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"message.android.ttl","description":"Invalid value at 'message.android.ttl'"}]}]}}`
	fcmErrTooBig          = `{"error":{"code":400,"message":"Request contains an invalid argument.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"INVALID_ARGUMENT"}]}}`
	fcmErrBadToken        = `{"error":{"code":400,"message":"The registration token is not a valid FCM registration token","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"INVALID_ARGUMENT"},{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"message.token","description":"The registration token is not a valid FCM registration token"}]}]}}`
	fcmErrUnregistered    = `{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`
	fcmErrUnregistered400 = `{"error":{"code":400,"message":"Requested entity was not found.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`
	fcmErrNotJSON         = `<html>Bad Request: INVALID_ARGUMENT</html>`
	// 404s that are not about the token: a wrong or deleted project id, or a
	// proxy in the way. They must never wipe every device's token.
	fcmErrBare404         = `{"error":{"code":404,"message":"Requested entity was not found."}}`
	fcmErrProjectNotFound = `{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND"}}`
	fcmErrUnregistered503 = `{"error":{"code":503,"message":"The service is currently unavailable.","status":"UNAVAILABLE","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`
)

// TestDispatcher_FCMRetriesPerToken checks that a failure on one token never
// re-sends the push to the tokens that already got it, and that only transient
// failures (5xx, 429, network) are retried, once.
func TestDispatcher_FCMRetriesPerToken(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p fcmMessagePayload
		_ = json.NewDecoder(r.Body).Decode(&p)
		tok := p.Message.Token
		mu.Lock()
		hits[tok]++
		n := hits[tok]
		mu.Unlock()

		switch {
		case tok == "tok-down":
			w.WriteHeader(http.StatusServiceUnavailable)
		case tok == "tok-flaky" && n == 1:
			w.WriteHeader(http.StatusInternalServerError)
		case tok == "tok-throttled" && n == 1:
			w.WriteHeader(http.StatusTooManyRequests)
		case tok == "tok-net" && n == 1:
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close() // network error on the client side
			}
		case tok == "tok-payload":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(fcmErrTTL))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	tokens := []string{"tok-good", "tok-down", "tok-flaky", "tok-throttled", "tok-net", "tok-payload", "tok-good-2"}
	fcm := &FCM{
		ProjectID:  "test-proj",
		Endpoint:   server.URL,
		Tokens:     func() []string { return tokens },
		Client:     server.Client(),
		RetryDelay: time.Millisecond,
	}
	d := NewDispatcher([]Sender{fcm}, nil, nil)

	d.OnAgentUpdate(nil, model.AgentState{PaneID: "p1", Label: "test", Status: model.StatusBlocked})
	d.Wait()

	want := map[string]int{
		"tok-good":      1, // delivered once, never duplicated
		"tok-good-2":    1,
		"tok-down":      2, // 503: one retry
		"tok-flaky":     2, // 500 then delivered
		"tok-throttled": 2, // 429 then delivered
		"tok-net":       2, // network error then delivered
		"tok-payload":   1, // 400 payload error: retrying cannot help
	}
	mu.Lock()
	defer mu.Unlock()
	for tok, n := range want {
		if hits[tok] != n {
			t.Errorf("%s: %d requests, want %d", tok, hits[tok], n)
		}
	}
}

func TestFCM_DeadTokenDetection(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantDead bool
	}{
		{"payload error: bad ttl", http.StatusBadRequest, fcmErrTTL, false},
		{"payload error: message too big", http.StatusBadRequest, fcmErrTooBig, false},
		{"400 without a JSON body", http.StatusBadRequest, fcmErrNotJSON, false},
		{"invalid token (field message.token)", http.StatusBadRequest, fcmErrBadToken, true},
		{"unregistered (404)", http.StatusNotFound, fcmErrUnregistered, true},
		{"unregistered errorCode on a 400", http.StatusBadRequest, fcmErrUnregistered400, true},
		{"bare 404 (no FCM error code)", http.StatusNotFound, fcmErrBare404, false},
		{"404 NOT_FOUND without UNREGISTERED (wrong project)", http.StatusNotFound, fcmErrProjectNotFound, false},
		{"404 without a JSON body", http.StatusNotFound, fcmErrNotJSON, false},
		{"UNREGISTERED on a 5xx", http.StatusServiceUnavailable, fcmErrUnregistered503, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			var dead []string
			fcm := &FCM{
				ProjectID:      "test-proj",
				Endpoint:       server.URL,
				Tokens:         func() []string { return []string{"tok-a", "tok-b"} },
				Client:         server.Client(),
				OnInvalidToken: func(token string) { dead = append(dead, token) },
				RetryDelay:     time.Millisecond, // the 5xx case retries
			}

			if err := fcm.Send(context.Background(), Message{Event: EventDone, Title: "t"}); err == nil {
				t.Fatalf("Send returned nil for a %d response", tt.status)
			}

			if tt.wantDead && len(dead) != 2 {
				t.Fatalf("expected both tokens reported dead, got %v", dead)
			}
			if !tt.wantDead && len(dead) != 0 {
				t.Fatalf("a %d that is not about the token must not unregister tokens, got %v", tt.status, dead)
			}
		})
	}
}
