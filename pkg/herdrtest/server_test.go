package herdrtest_test

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/herdrtest"
)

func sendRaw(t *testing.T, sockPath string, req map[string]any) map[string]any {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal req failed: %v", err)
	}
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write req failed: %v", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read resp failed: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("unmarshal resp failed (%s): %v", string(line), err)
	}
	return resp
}

func sendRawString(t *testing.T, sockPath, raw string) map[string]any {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(raw + "\n")); err != nil {
		t.Fatalf("write raw failed: %v", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read resp failed: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("unmarshal resp failed (%s): %v", string(line), err)
	}
	return resp
}

func TestOneRequestPerConnection(t *testing.T) {
	srv := herdrtest.New(t)

	conn, err := net.Dial("unix", srv.SocketPath)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	// 1st request succeeds
	req := `{"id":"req-1","method":"ping","params":{}}` + "\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if len(line) == 0 {
		t.Fatalf("expected response line, got empty")
	}

	// 2nd request on the same connection should fail because the server closed it
	req2 := `{"id":"req-2","method":"ping","params":{}}` + "\n"
	_, _ = conn.Write([]byte(req2))
	_, err = reader.ReadBytes('\n')
	if err == nil {
		t.Errorf("expected EOF or error on 2nd request, got nil")
	}
}

func TestRejectNumericID(t *testing.T) {
	srv := herdrtest.New(t)

	resp := sendRawString(t, srv.SocketPath, `{"id":123,"method":"ping","params":{}}`)
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got: %v", resp)
	}

	if code, _ := errObj["code"].(string); code != "invalid_request" {
		t.Errorf("expected error code invalid_request, got %s", code)
	}
}

func TestPing(t *testing.T) {
	srv := herdrtest.New(t)

	resp := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "p1",
		"method": "ping",
		"params": map[string]any{},
	})

	if resp["id"] != "p1" {
		t.Errorf("expected id p1, got %v", resp["id"])
	}

	res, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %v", resp)
	}

	if res["type"] != "pong" {
		t.Errorf("expected type pong, got %v", res["type"])
	}
	if res["version"] != "0.9.1" {
		t.Errorf("expected version 0.9.1, got %v", res["version"])
	}
	if int(res["protocol"].(float64)) != 22 {
		t.Errorf("expected protocol 22, got %v", res["protocol"])
	}
}

func TestAgentListAndGet(t *testing.T) {
	srv := herdrtest.New(t)

	agents := []map[string]any{
		{
			"pane_id":          "w1:p1",
			"agent":            "claude",
			"agent_status":     "idle",
			"name":             "agent-one",
			"state_change_seq": 10,
		},
	}
	srv.SetAgents(agents)

	// List
	listResp := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "l1",
		"method": "agent.list",
		"params": map[string]any{},
	})
	res := listResp["result"].(map[string]any)
	if res["type"] != "agent_list" {
		t.Errorf("expected agent_list type, got %v", res["type"])
	}
	agentsList := res["agents"].([]any)
	if len(agentsList) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agentsList))
	}

	// Get existing
	getResp := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "g1",
		"method": "agent.get",
		"params": map[string]any{"target": "w1:p1"},
	})
	agentObj := getResp["result"].(map[string]any)
	if agentObj["pane_id"] != "w1:p1" {
		t.Errorf("expected pane_id w1:p1, got %v", agentObj["pane_id"])
	}

	// Get non-existent
	notFoundResp := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "g2",
		"method": "agent.get",
		"params": map[string]any{"target": "w0:none"},
	})
	errObj := notFoundResp["error"].(map[string]any)
	if errObj["code"] != "agent_not_found" {
		t.Errorf("expected agent_not_found code, got %v", errObj["code"])
	}
}

func TestAgentRead(t *testing.T) {
	srv := herdrtest.New(t)

	srv.SetScreen("w1:p1", "visible", "Visible Screen Content")
	srv.SetScreen("w1:p1", "recent_unwrapped", "Unwrapped Recent Content")

	// Valid visible source
	resp := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "r1",
		"method": "agent.read",
		"params": map[string]any{"target": "w1:p1", "source": "visible"},
	})
	res := resp["result"].(map[string]any)
	if res["type"] != "pane_read" {
		t.Errorf("expected pane_read, got %v", res["type"])
	}
	read := res["read"].(map[string]any)
	if read["text"] != "Visible Screen Content" {
		t.Errorf("expected 'Visible Screen Content', got %v", read["text"])
	}

	// Valid recent_unwrapped source
	resp2 := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "r2",
		"method": "agent.read",
		"params": map[string]any{"target": "w1:p1", "source": "recent_unwrapped"},
	})
	read2 := resp2["result"].(map[string]any)["read"].(map[string]any)
	if read2["text"] != "Unwrapped Recent Content" {
		t.Errorf("expected 'Unwrapped Recent Content', got %v", read2["text"])
	}

	// Hyphenated recent-unwrapped must be rejected with invalid_request
	respHyphen := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "r3",
		"method": "agent.read",
		"params": map[string]any{"target": "w1:p1", "source": "recent-unwrapped"},
	})
	errObj := respHyphen["error"].(map[string]any)
	if errObj["code"] != "invalid_request" {
		t.Errorf("expected invalid_request for hyphenated recent-unwrapped, got %v", errObj["code"])
	}

	// Other invalid source rejected
	respInvalid := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "r4",
		"method": "agent.read",
		"params": map[string]any{"target": "w1:p1", "source": "bogus"},
	})
	if respInvalid["error"].(map[string]any)["code"] != "invalid_request" {
		t.Errorf("expected invalid_request for bogus source, got %v", respInvalid["error"])
	}
}

func TestAgentSendKeysAndPrompt(t *testing.T) {
	srv := herdrtest.New(t)

	srv.SetAgents([]map[string]any{
		{"pane_id": "w1:idle", "agent_status": "idle"},
		{"pane_id": "w1:blocked", "agent_status": "blocked"},
	})

	// send_keys to idle agent -> ok
	respKeys := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "k1",
		"method": "agent.send_keys",
		"params": map[string]any{"target": "w1:idle", "keys": []string{"1"}},
	})
	if respKeys["result"].(map[string]any)["type"] != "ok" {
		t.Errorf("expected ok result, got %v", respKeys)
	}

	// send_keys to unknown target -> agent_not_found
	respKeysNotFound := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "k2",
		"method": "agent.send_keys",
		"params": map[string]any{"target": "w1:unknown", "keys": []string{"1"}},
	})
	if respKeysNotFound["error"].(map[string]any)["code"] != "agent_not_found" {
		t.Errorf("expected agent_not_found, got %v", respKeysNotFound["error"])
	}

	// prompt to idle agent -> ok
	respPrompt := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "p1",
		"method": "agent.prompt",
		"params": map[string]any{"target": "w1:idle", "text": "hello"},
	})
	if respPrompt["result"].(map[string]any)["type"] != "ok" {
		t.Errorf("expected ok result, got %v", respPrompt)
	}

	// prompt to blocked agent -> agent_blocked error
	respBlocked := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "p2",
		"method": "agent.prompt",
		"params": map[string]any{"target": "w1:blocked", "text": "hello"},
	})
	if respBlocked["error"].(map[string]any)["code"] != "agent_blocked" {
		t.Errorf("expected agent_blocked, got %v", respBlocked["error"])
	}

	// Calls recorded
	calls := srv.Calls()
	if len(calls) != 4 {
		t.Errorf("expected 4 calls recorded, got %d", len(calls))
	}
}

func TestEventsSubscribe(t *testing.T) {
	srv := herdrtest.New(t)

	// Missing pane_id for pane.agent_status_changed returns invalid_request
	respMissing := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "sub-err",
		"method": "events.subscribe",
		"params": map[string]any{
			"subscriptions": []map[string]any{
				{"type": "pane.agent_status_changed"},
			},
		},
	})
	if respMissing["error"].(map[string]any)["code"] != "invalid_request" {
		t.Fatalf("expected invalid_request for missing pane_id, got %v", respMissing["error"])
	}

	// Successful subscribe connection
	conn, err := net.Dial("unix", srv.SocketPath)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	subReq := map[string]any{
		"id":     "sub-1",
		"method": "events.subscribe",
		"params": map[string]any{
			"subscriptions": []map[string]any{
				{"type": "pane.agent_status_changed", "pane_id": "w1:target"},
				{"type": "pane.created"},
			},
		},
	}
	data, _ := json.Marshal(subReq)
	data = append(data, '\n')
	_, _ = conn.Write(data)

	reader := bufio.NewReader(conn)
	ackLine, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read ack failed: %v", err)
	}

	var ack map[string]any
	if err := json.Unmarshal(ackLine, &ack); err != nil {
		t.Fatalf("unmarshal ack failed: %v", err)
	}
	if ack["result"].(map[string]any)["type"] != "subscription_started" {
		t.Fatalf("expected subscription_started, got %v", ack)
	}

	// Second client subscribed to a different pane
	connOther, err := net.Dial("unix", srv.SocketPath)
	if err != nil {
		t.Fatalf("dial other failed: %v", err)
	}
	defer connOther.Close()

	subReqOther := map[string]any{
		"id":     "sub-2",
		"method": "events.subscribe",
		"params": map[string]any{
			"subscriptions": []map[string]any{
				{"type": "pane.agent_status_changed", "pane_id": "w1:other"},
			},
		},
	}
	dataOther, _ := json.Marshal(subReqOther)
	dataOther = append(dataOther, '\n')
	_, _ = connOther.Write(dataOther)
	readerOther := bufio.NewReader(connOther)
	_, _ = readerOther.ReadBytes('\n') // read ack

	// Emit status changed for w1:target
	srv.EmitStatusChanged("w1:target", "working")

	// Target conn receives the event
	_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	evtLine, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read event failed: %v", err)
	}

	var evt map[string]any
	if err := json.Unmarshal(evtLine, &evt); err != nil {
		t.Fatalf("unmarshal event failed: %v", err)
	}
	if evt["event"] != "pane_agent_status_changed" {
		t.Errorf("expected pane_agent_status_changed, got %v", evt["event"])
	}
	evtData := evt["data"].(map[string]any)
	if evtData["pane_id"] != "w1:target" || evtData["agent_status"] != "working" {
		t.Errorf("unexpected event payload: %v", evtData)
	}

	// Other conn should NOT receive this event
	_ = connOther.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_, err = readerOther.ReadBytes('\n')
	if err == nil {
		t.Errorf("expected no event on other subscriber, got event")
	}

	// Test EmitGlobal: both subscribed to global event
	srv.EmitGlobal("pane.created", map[string]any{"pane_id": "w1:new"})
	_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	globalLine, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read global event failed: %v", err)
	}
	var globalEvt map[string]any
	_ = json.Unmarshal(globalLine, &globalEvt)
	if globalEvt["event"] != "pane_created" {
		t.Errorf("expected pane_created event, got %v", globalEvt["event"])
	}
}

func TestFailNext(t *testing.T) {
	srv := herdrtest.New(t)

	srv.FailNext("agent.list", "server_error", "temporary failure")

	// First call fails with configured error
	resp1 := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "f1",
		"method": "agent.list",
		"params": map[string]any{},
	})
	errObj := resp1["error"].(map[string]any)
	if errObj["code"] != "server_error" {
		t.Errorf("expected server_error, got %v", errObj["code"])
	}

	// Second call succeeds
	resp2 := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "f2",
		"method": "agent.list",
		"params": map[string]any{},
	})
	if resp2["result"] == nil {
		t.Errorf("expected success on second call, got error: %v", resp2["error"])
	}
}

func TestStopAndStart(t *testing.T) {
	srv := herdrtest.New(t)

	// Stop server
	srv.Stop()

	// Dial should fail
	_, err := net.Dial("unix", srv.SocketPath)
	if err == nil {
		t.Errorf("expected dial to fail when server stopped, got nil")
	}

	// Restart server
	srv.Start()

	// Dial should succeed again
	resp := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "p1",
		"method": "ping",
		"params": map[string]any{},
	})
	if resp["result"] == nil {
		t.Errorf("expected ping to succeed after restart, got: %v", resp)
	}
}

func TestMalformedJSON(t *testing.T) {
	srv := herdrtest.New(t)

	resp := sendRawString(t, srv.SocketPath, `{not-json`)
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got: %v", resp)
	}
	if errObj["code"] != "invalid_request" {
		t.Errorf("expected invalid_request, got %v", errObj["code"])
	}
}

func TestUnknownMethod(t *testing.T) {
	srv := herdrtest.New(t)

	resp := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "u1",
		"method": "bogus.method",
		"params": map[string]any{},
	})
	errObj := resp["error"].(map[string]any)
	if errObj["code"] != "invalid_request" {
		t.Errorf("expected invalid_request, got %v", errObj["code"])
	}
}

func TestSessionSnapshot(t *testing.T) {
	srv := herdrtest.New(t)

	srv.SetAgents([]map[string]any{{"pane_id": "w1:p1"}})

	resp := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "s1",
		"method": "session.snapshot",
		"params": map[string]any{},
	})
	res := resp["result"].(map[string]any)
	if res["type"] != "session_snapshot" {
		t.Errorf("expected session_snapshot, got %v", res["type"])
	}
}
