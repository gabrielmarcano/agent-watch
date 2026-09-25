package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

const testHostToken = "1111222233334444555566667777888899990000aaaabbbbccccddddeeeeffff"

func setupTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	return setupTestServerWith(t, nil)
}

// setupTestServerWith is setupTestServer with a hook to adjust the config.
func setupTestServerWith(t *testing.T, configure func(*Config)) (*Server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	cfg := &Config{
		ListenAddr: ":0",
		HostToken:  testHostToken,
		DataDir:    dir,
	}
	if configure != nil {
		configure(cfg)
	}

	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ts := httptest.NewServer(server.Handler())
	t.Cleanup(func() {
		ts.Close()
		_ = server.Close()
	})

	return server, ts
}

func TestAPI_PairingHappyPath(t *testing.T) {
	_, ts := setupTestServer(t)

	// 1. Request pair code using host token
	req, _ := http.NewRequest("POST", ts.URL+"/v1/host/pair-code", nil)
	req.Header.Set("Authorization", "Bearer "+testHostToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST pair-code: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for pair-code, got %d", resp.StatusCode)
	}

	var codeResp model.PairCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&codeResp); err != nil {
		t.Fatalf("decode pair-code: %v", err)
	}
	if len(codeResp.Code) != 6 {
		t.Fatalf("expected 6-digit code, got %s", codeResp.Code)
	}

	// 2. Watch pairs with that code
	pairBody, _ := json.Marshal(model.PairRequest{
		Code:       codeResp.Code,
		DeviceName: "Pixel Watch 2",
	})
	pairReq, _ := http.NewRequest("POST", ts.URL+"/v1/pair", bytes.NewReader(pairBody))
	pairResp, err := http.DefaultClient.Do(pairReq)
	if err != nil {
		t.Fatalf("POST pair: %v", err)
	}
	defer pairResp.Body.Close()
	if pairResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for pair, got %d", pairResp.StatusCode)
	}

	var pairData model.PairResponse
	if err := json.NewDecoder(pairResp.Body).Decode(&pairData); err != nil {
		t.Fatalf("decode pair response: %v", err)
	}
	if pairData.DeviceID == "" || len(pairData.DeviceToken) != 64 {
		t.Fatalf("unexpected pair data: %+v", pairData)
	}

	// 3. Authenticate with device token against /v1/agents
	agentsReq, _ := http.NewRequest("GET", ts.URL+"/v1/agents", nil)
	agentsReq.Header.Set("Authorization", "Bearer "+pairData.DeviceToken)
	agentsResp, err := http.DefaultClient.Do(agentsReq)
	if err != nil {
		t.Fatalf("GET agents: %v", err)
	}
	defer agentsResp.Body.Close()
	if agentsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for agents with device token, got %d", agentsResp.StatusCode)
	}
}

func TestAPI_PairingFailures(t *testing.T) {
	_, ts := setupTestServer(t)

	// 1. Wrong code -> 403
	pairBody, _ := json.Marshal(model.PairRequest{
		Code:       "000000",
		DeviceName: "Watch",
	})
	req, _ := http.NewRequest("POST", ts.URL+"/v1/pair", bytes.NewReader(pairBody))
	req.RemoteAddr = "10.0.0.1:1234"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST pair: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for wrong code, got %d", resp.StatusCode)
	}

	var errResp model.ErrorResponse
	_ = json.NewDecoder(resp.Body).Decode(&errResp)
	if errResp.Error.Code != string(model.ErrPairCodeInvalid) {
		t.Fatalf("expected code pair_code_invalid, got %s", errResp.Error.Code)
	}

	// 2. Exhaust attempts (5 attempts per IP; the one above already counted)
	for i := 0; i < 4; i++ {
		req, _ := http.NewRequest("POST", ts.URL+"/v1/pair", bytes.NewReader(pairBody))
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST pair: %v", err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusForbidden {
			t.Fatalf("attempt %d: expected 403, got %d", i+2, r.StatusCode)
		}
	}

	// 6th attempt from the same client IP should be 429
	req, _ = http.NewRequest("POST", ts.URL+"/v1/pair", bytes.NewReader(pairBody))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST pair: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for rate limited IP, got %d", resp.StatusCode)
	}
}

func TestAPI_CommandRoundTripAndErrors(t *testing.T) {
	server, ts := setupTestServer(t)

	// Create and register a device
	devToken, _ := GenerateDeviceToken()
	_, _ = server.Store().AddDevice("Test Watch", Sha256Hex(devToken))

	// Connect fake host
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) + "/v1/host"
	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + testHostToken}},
	}
	conn, _, err := websocket.Dial(ctx, wsURL, opts)
	if err != nil {
		t.Fatalf("dial host: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Send Hello
	hello, _ := json.Marshal(model.HelloMsg{
		Type:        model.WireHello,
		Host:        "mac",
		HerdrOnline: true,
	})
	_ = conn.Write(ctx, websocket.MessageText, hello)

	// Send initial snapshot containing pane w5:pAW
	snap, _ := json.Marshal(model.SnapshotMsg{
		Type: model.WireSnapshot,
		Agents: []model.AgentState{
			{
				PaneID:         "w5:pAW",
				Agent:          "claude",
				Label:          "test-agent",
				Status:         model.StatusBlocked,
				StateChangeSeq: 10,
			},
		},
	})
	_ = conn.Write(ctx, websocket.MessageText, snap)

	for i := 0; i < 50; i++ {
		if server.State().HasPane("w5:pAW") && server.State().HostOnline() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 1. Unknown pane -> 404
	ansBody, _ := json.Marshal(model.AnswerRequest{
		OptionID:    "opt-1",
		ExpectedSeq: 10,
	})
	req, _ := http.NewRequest("POST", ts.URL+"/v1/agents/unknown-pane/answer", bytes.NewReader(ansBody))
	req.Header.Set("Authorization", "Bearer "+devToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST answer unknown pane: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown pane, got %d", resp.StatusCode)
	}

	// 2. URL-encoded pane_id + Happy path command
	// Host goroutine answering commands
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			msg, err := model.DecodeWire(data)
			if err != nil {
				continue
			}
			if cmd, ok := msg.(model.CommandMsg); ok {
				var res model.CommandResultMsg
				if cmd.Action == "answer" && cmd.OptionID == "opt-1" {
					res = model.CommandResultMsg{
						Type:      model.WireCommandResult,
						RequestID: cmd.RequestID,
						OK:        true,
					}
				} else if cmd.Action == "answer" && cmd.OptionID == "stale-opt" {
					res = model.CommandResultMsg{
						Type:      model.WireCommandResult,
						RequestID: cmd.RequestID,
						OK:        false,
						ErrorCode: string(model.ErrStaleState),
						Message:   "state changed",
					}
				}
				resData, _ := json.Marshal(res)
				_ = conn.Write(ctx, websocket.MessageText, resData)
			}
		}
	}()

	// URL-encoded pane_id "w5%3ApAW"
	req, _ = http.NewRequest("POST", ts.URL+"/v1/agents/w5%3ApAW/answer", bytes.NewReader(ansBody))
	req.Header.Set("Authorization", "Bearer "+devToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST answer: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for answer with URL-encoded pane_id, got %d", resp.StatusCode)
	}

	// 3. Stale state error -> 409
	staleBody, _ := json.Marshal(model.AnswerRequest{
		OptionID:    "stale-opt",
		ExpectedSeq: 9,
	})
	req, _ = http.NewRequest("POST", ts.URL+"/v1/agents/w5%3ApAW/answer", bytes.NewReader(staleBody))
	req.Header.Set("Authorization", "Bearer "+devToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST answer stale state: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for stale state, got %d", resp.StatusCode)
	}

	// 4. Host offline -> 503
	conn.Close(websocket.StatusNormalClosure, "")
	for i := 0; i < 50; i++ {
		if !server.State().HostOnline() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/v1/agents/w5%3ApAW/answer", bytes.NewReader(ansBody))
	req.Header.Set("Authorization", "Bearer "+devToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST answer host offline: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for host offline, got %d", resp.StatusCode)
	}
}

func TestAPI_SSEStreaming(t *testing.T) {
	server, ts := setupTestServer(t)
	server.SetKeepAliveInterval(50 * time.Millisecond)

	devToken, _ := GenerateDeviceToken()
	_, _ = server.Store().AddDevice("Watch", Sha256Hex(devToken))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+devToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/events: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)

	// First event must be snapshot
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read first line: %v", err)
	}
	if strings.TrimSpace(line) != "event: snapshot" {
		t.Fatalf("expected 'event: snapshot', got %q", line)
	}

	// Trigger agent update event
	server.State().Upsert(model.AgentState{
		PaneID: "p1",
		Status: model.StatusWorking,
	})

	// Trigger agent_removed event
	server.State().Remove("p1")

	// Trigger host event
	server.State().SetHost(true, true)

	// Trigger history event
	server.State().BroadcastHistory(model.HistoryItem{
		ID:          "hist-sse",
		CompletedAt: model.Now(),
	})

	eventsSeen := make(map[string]bool)
	eventsSeen["snapshot"] = true

	// Read events and keepalive ticks
	for i := 0; i < 30; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "event: ") {
			evName := strings.TrimPrefix(trimmed, "event: ")
			eventsSeen[evName] = true
		} else if trimmed == ":" {
			eventsSeen[":"] = true
		}

		if eventsSeen["agent"] && eventsSeen["agent_removed"] && eventsSeen["host"] && eventsSeen["history"] && eventsSeen[":"] {
			break
		}
	}

	for _, expected := range []string{"snapshot", "agent", "agent_removed", "host", "history", ":"} {
		if !eventsSeen[expected] {
			t.Errorf("expected SSE stream to contain %q, events seen: %+v", expected, eventsSeen)
		}
	}
}

func TestAPI_HistoryQuery(t *testing.T) {
	server, ts := setupTestServer(t)

	devToken, _ := GenerateDeviceToken()
	_, _ = server.Store().AddDevice("Watch", Sha256Hex(devToken))

	for i := 0; i < 10; i++ {
		server.Store().AddHistory(model.HistoryItem{
			ID:          fmt.Sprintf("item-%d", i),
			PaneID:      "w1:p1",
			CompletedAt: fmt.Sprintf("2026-09-24T00:00:%02dZ", i),
		})
	}

	// Query with limit=5
	req, _ := http.NewRequest("GET", ts.URL+"/v1/history?pane_id=w1:p1&limit=5", nil)
	req.Header.Set("Authorization", "Bearer "+devToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET history: %v", err)
	}
	defer resp.Body.Close()

	var histResp model.HistoryResponse
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &histResp); err != nil {
		t.Fatalf("unmarshal history response: %v", err)
	}

	if len(histResp.Items) != 5 {
		t.Fatalf("expected 5 items, got %d", len(histResp.Items))
	}
	// Newest first -> item-9
	if histResp.Items[0].ID != "item-9" {
		t.Fatalf("expected newest item item-9 first, got %s", histResp.Items[0].ID)
	}
}

// pairAttempt posts a wrong pairing code with the given extra headers and
// returns the status code.
func pairAttempt(t *testing.T, ts *httptest.Server, headers map[string]string) int {
	t.Helper()
	body, _ := json.Marshal(model.PairRequest{Code: "000000", DeviceName: "Watch"})
	req, _ := http.NewRequest("POST", ts.URL+"/v1/pair", bytes.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST pair: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// A client talking to the relay directly (no trusted proxy in between) must
// not escape the per-IP limit by inventing a new client IP in a header.
func TestAPI_PairRateLimitIgnoresSpoofedHeaders(t *testing.T) {
	_, ts := setupTestServer(t)

	for i := 0; i < 5; i++ {
		spoofed := fmt.Sprintf("203.0.113.%d", i+1)
		code := pairAttempt(t, ts, map[string]string{
			"CF-Connecting-IP": spoofed,
			"X-Forwarded-For":  spoofed,
			"X-Real-IP":        spoofed,
		})
		if code != http.StatusForbidden {
			t.Fatalf("attempt %d: status %d, want 403", i+1, code)
		}
	}
	code := pairAttempt(t, ts, map[string]string{
		"CF-Connecting-IP": "203.0.113.99",
		"X-Forwarded-For":  "203.0.113.99",
		"X-Real-IP":        "203.0.113.99",
	})
	if code != http.StatusTooManyRequests {
		t.Fatalf("6th attempt from the same peer with a spoofed IP header: status %d, want 429", code)
	}
}
