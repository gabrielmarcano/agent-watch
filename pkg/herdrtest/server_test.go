package herdrtest_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
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

	if _, ok := req["jsonrpc"]; !ok {
		req["jsonrpc"] = "2.0"
	}

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

func errorKind(resp map[string]any) string {
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		return ""
	}
	if data, ok := errObj["data"].(map[string]any); ok {
		if k, ok := data["kind"].(string); ok {
			return k
		}
	}
	if c, ok := errObj["code"].(string); ok {
		return c
	}
	return ""
}

func TestOneRequestPerConnection(t *testing.T) {
	srv := herdrtest.New(t)

	conn, err := net.Dial("unix", srv.SocketPath)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	req := `{"jsonrpc":"2.0","id":"req-1","method":"hello","params":{}}` + "\n"
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

	// 2nd request on the same connection fails because the server closed it.
	req2 := `{"jsonrpc":"2.0","id":"req-2","method":"hello","params":{}}` + "\n"
	_, _ = conn.Write([]byte(req2))
	_, err = reader.ReadBytes('\n')
	if err == nil {
		t.Errorf("expected EOF or error on 2nd request, got nil")
	}
}

func TestRejectNumericID(t *testing.T) {
	srv := herdrtest.New(t)

	resp := sendRawString(t, srv.SocketPath, `{"jsonrpc":"2.0","id":123,"method":"hello","params":{}}`)
	if got := errorKind(resp); got != "invalid_request" {
		t.Errorf("expected error kind invalid_request, got %s (%v)", got, resp)
	}
}

func TestHello(t *testing.T) {
	srv := herdrtest.New(t)

	resp := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "p1",
		"method": "hello",
		"params": map[string]any{},
	})

	if resp["id"] != "p1" {
		t.Errorf("expected id p1, got %v", resp["id"])
	}

	res, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %v", resp)
	}

	if res["version"] != "0.1.14" {
		t.Errorf("expected version 0.1.14, got %v", res["version"])
	}
	if int(res["generation"].(float64)) != 1 {
		t.Errorf("expected generation 1, got %v", res["generation"])
	}
}

func TestTerminalListAndGet(t *testing.T) {
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

	listResp := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "l1",
		"method": "terminal.list",
		"params": map[string]any{},
	})
	res := listResp["result"].(map[string]any)
	termsList := res["terminals"].([]any)
	if len(termsList) != 1 {
		t.Fatalf("expected 1 terminal, got %d", len(termsList))
	}

	getResp := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "g1",
		"method": "terminal.get",
		"params": map[string]any{"terminal": "w1:p1"},
	})
	termObj := getResp["result"].(map[string]any)
	if termObj["address"] != "w1:p1" {
		t.Errorf("expected address w1:p1, got %v", termObj["address"])
	}

	notFoundResp := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "g2",
		"method": "terminal.get",
		"params": map[string]any{"terminal": "w0:none"},
	})
	if got := errorKind(notFoundResp); got != "not_found" {
		t.Errorf("expected not_found kind, got %v", got)
	}
}

func TestTerminalRead(t *testing.T) {
	srv := herdrtest.New(t)

	srv.SetScreen("w1:p1", "visible", "Visible Screen Content")
	srv.SetScreen("w1:p1", "recent_unwrapped", "Unwrapped Recent Content")

	resp := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "r1",
		"method": "terminal.read",
		"params": map[string]any{"terminal": "w1:p1"},
	})
	res := resp["result"].(map[string]any)
	if res["text"] != "Visible Screen Content" {
		t.Errorf("expected 'Visible Screen Content', got %v", res["text"])
	}

	resp2 := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "r2",
		"method": "terminal.read",
		"params": map[string]any{"terminal": "w1:p1", "lines": 100},
	})
	res2 := resp2["result"].(map[string]any)
	if res2["text"] != "Unwrapped Recent Content" {
		t.Errorf("expected 'Unwrapped Recent Content', got %v", res2["text"])
	}
}

func TestTerminalKeysAndSend(t *testing.T) {
	srv := herdrtest.New(t)

	srv.SetAgents([]map[string]any{
		{"pane_id": "w1:idle", "agent_status": "idle"},
		{"pane_id": "w1:blocked", "agent_status": "blocked"},
	})

	respKeys := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "k1",
		"method": "terminal.keys",
		"params": map[string]any{"terminal": "w1:idle", "keys": []string{"1"}},
	})
	if respKeys["result"] == nil {
		t.Errorf("expected result, got %v", respKeys)
	}

	respKeysNotFound := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "k2",
		"method": "terminal.keys",
		"params": map[string]any{"terminal": "w1:unknown", "keys": []string{"1"}},
	})
	if got := errorKind(respKeysNotFound); got != "not_found" {
		t.Errorf("expected not_found, got %v", got)
	}

	respSend := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "p1",
		"method": "terminal.send",
		"params": map[string]any{"terminal": "w1:idle", "text": "hello"},
	})
	if respSend["result"] == nil {
		t.Errorf("expected result, got %v", respSend)
	}

	respBlocked := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "p2",
		"method": "terminal.send",
		"params": map[string]any{"terminal": "w1:blocked", "text": "hello"},
	})
	if got := errorKind(respBlocked); got != "blocked" {
		t.Errorf("expected blocked, got %v", got)
	}

	calls := srv.Calls()
	if len(calls) != 4 {
		t.Errorf("expected 4 calls recorded, got %d", len(calls))
	}
}

func TestEventsSubscribe(t *testing.T) {
	srv := herdrtest.New(t)

	conn, reader := dialSubscribe(t, srv.SocketPath)
	ackLine, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read ack failed: %v", err)
	}

	var ack map[string]any
	if err := json.Unmarshal(ackLine, &ack); err != nil {
		t.Fatalf("unmarshal ack failed: %v", err)
	}
	res, ok := ack["result"].(map[string]any)
	if !ok || res["terminals"] == nil {
		t.Fatalf("expected SubscribeResult with terminals, got %v", ack)
	}

	srv.EmitStatusChanged("w1:target", "working")

	_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	evtLine, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read event failed: %v", err)
	}

	var evt map[string]any
	if err := json.Unmarshal(evtLine, &evt); err != nil {
		t.Fatalf("unmarshal event failed: %v", err)
	}
	if evt["method"] != "event" {
		t.Errorf("expected method event, got %v", evt["method"])
	}
	evtParams := evt["params"].(map[string]any)
	if evtParams["terminal"] != "w1:target" || evtParams["state"] != "working" {
		t.Errorf("unexpected event payload: %v", evtParams)
	}
}

func TestFailNext(t *testing.T) {
	srv := herdrtest.New(t)

	srv.FailNext("terminal.list", "server_error", "temporary failure")

	resp1 := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "f1",
		"method": "terminal.list",
		"params": map[string]any{},
	})
	if got := errorKind(resp1); got != "server_error" {
		t.Errorf("expected server_error, got %v", got)
	}

	resp2 := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "f2",
		"method": "terminal.list",
		"params": map[string]any{},
	})
	if resp2["result"] == nil {
		t.Errorf("expected success on second call, got error: %v", resp2["error"])
	}
}

func TestStopAndStart(t *testing.T) {
	srv := herdrtest.New(t)

	srv.Stop()

	_, err := net.Dial("unix", srv.SocketPath)
	if err == nil {
		t.Errorf("expected dial to fail when server stopped, got nil")
	}

	srv.Start()

	resp := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "p1",
		"method": "hello",
		"params": map[string]any{},
	})
	if resp["result"] == nil {
		t.Errorf("expected hello to succeed after restart, got: %v", resp)
	}
}

func TestMalformedJSON(t *testing.T) {
	srv := herdrtest.New(t)

	resp := sendRawString(t, srv.SocketPath, `{not-json`)
	if got := errorKind(resp); got != "invalid_request" {
		t.Errorf("expected invalid_request, got %v", got)
	}
}

func TestUnknownMethod(t *testing.T) {
	srv := herdrtest.New(t)

	resp := sendRaw(t, srv.SocketPath, map[string]any{
		"id":     "u1",
		"method": "bogus.method",
		"params": map[string]any{},
	})
	if got := errorKind(resp); got != "invalid_request" {
		t.Errorf("expected invalid_request, got %v", got)
	}
}

func dialSubscribe(t *testing.T, sockPath string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	data, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "sub-1",
		"method":  "events.subscribe",
		"params":  map[string]any{},
	})
	if _, err := conn.Write(append(data, '\n')); err != nil {
		t.Fatalf("write subscribe failed: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	return conn, bufio.NewReader(conn)
}

func expectClosed(t *testing.T, reader *bufio.Reader, what string) {
	t.Helper()
	line, err := reader.ReadBytes('\n')
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected %s to be closed (EOF), got %q, %v", what, line, err)
	}
}

func tryRaw(sockPath string, req map[string]any) (map[string]any, error) {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, ok := req["jsonrpc"]; !ok {
		req["jsonrpc"] = "2.0"
	}
	data, _ := json.Marshal(req)
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var resp map[string]any
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func TestCallsRecordRequestID(t *testing.T) {
	srv := herdrtest.New(t)

	sendRaw(t, srv.SocketPath, map[string]any{"id": "abc-7", "method": "hello", "params": map[string]any{}})

	calls := srv.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 recorded call, got %d", len(calls))
	}
	if calls[0].ID != "abc-7" {
		t.Errorf("recorded id = %q, want %q", calls[0].ID, "abc-7")
	}
}

func TestUnknownTargetRejectedWithoutTerminals(t *testing.T) {
	srv := herdrtest.New(t)

	for _, tc := range []struct {
		method string
		params map[string]any
	}{
		{"terminal.keys", map[string]any{"terminal": "w1:ghost", "keys": []string{"1"}}},
		{"terminal.send", map[string]any{"terminal": "w1:ghost", "text": "hi"}},
	} {
		resp := sendRaw(t, srv.SocketPath, map[string]any{"id": "x", "method": tc.method, "params": tc.params})
		if got := errorKind(resp); got != "not_found" {
			t.Errorf("%s: kind = %v, want not_found (%v)", tc.method, got, resp)
		}
	}
}

func TestInputCallsReadTerminalsUnderLock(t *testing.T) {
	srv := herdrtest.New(t)
	agents := []map[string]any{{"pane_id": "w1:p1", "agent_status": "idle"}}
	srv.SetAgents(agents)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				srv.SetAgents(agents)
			}
		}
	}()

	for i := 0; i < 30; i++ {
		sendRaw(t, srv.SocketPath, map[string]any{"id": "k", "method": "terminal.keys",
			"params": map[string]any{"terminal": "w1:ghost", "keys": []string{"1"}}})
		sendRaw(t, srv.SocketPath, map[string]any{"id": "p", "method": "terminal.send",
			"params": map[string]any{"terminal": "w1:ghost", "text": "hi"}})
	}
	close(stop)
	<-done
}

func TestHoldNextAnswersWithStateAtArrival(t *testing.T) {
	srv := herdrtest.New(t)
	srv.SetAgents([]map[string]any{{"pane_id": "w1:p1", "agent": "claude", "agent_status": "blocked"}})

	hold := srv.HoldNext("terminal.list")
	type result struct {
		resp map[string]any
		err  error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := tryRaw(srv.SocketPath, map[string]any{"id": "l1", "method": "terminal.list", "params": map[string]any{}})
		got <- result{resp, err}
	}()

	select {
	case <-hold.Received():
	case <-time.After(2 * time.Second):
		t.Fatal("held call never arrived")
	}

	srv.SetAgents([]map[string]any{{"pane_id": "w1:p1", "agent": "claude", "agent_status": "working"}})

	select {
	case r := <-got:
		t.Fatalf("held call answered before Release: %v %v", r.resp, r.err)
	default:
	}

	hold.Release()

	var r result
	select {
	case r = <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("held call not answered after Release")
	}
	if r.err != nil {
		t.Fatalf("held call failed: %v", r.err)
	}
	terms := r.resp["result"].(map[string]any)["terminals"].([]any)
	statusObj := terms[0].(map[string]any)["status"].(map[string]any)
	if state := statusObj["state"]; state != "blocked" {
		t.Errorf("held answer status = %v, want the state at arrival (blocked)", state)
	}

	if _, err := tryRaw(srv.SocketPath, map[string]any{"id": "l2", "method": "terminal.list", "params": map[string]any{}}); err != nil {
		t.Errorf("second terminal.list failed: %v", err)
	}
}

func TestHoldNextNeverReleasedEndsOnStop(t *testing.T) {
	srv := herdrtest.New(t)
	hold := srv.HoldNext("events.subscribe")

	conn, reader := dialSubscribe(t, srv.SocketPath)
	select {
	case <-hold.Received():
	case <-time.After(2 * time.Second):
		t.Fatal("held subscribe never arrived")
	}

	srv.Stop()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	expectClosed(t, reader, "the held connection on Stop")
}

func TestDropStreamsKeepsServerUp(t *testing.T) {
	srv := herdrtest.New(t)

	conn, reader := dialSubscribe(t, srv.SocketPath)
	if _, err := reader.ReadBytes('\n'); err != nil {
		t.Fatalf("read ack: %v", err)
	}

	srv.DropStreams()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	expectClosed(t, reader, "the stream after DropStreams")
	resp := sendRaw(t, srv.SocketPath, map[string]any{"id": "p", "method": "hello", "params": map[string]any{}})
	if resp["result"] == nil {
		t.Errorf("hello after DropStreams: %v", resp)
	}
}

func TestDropStreamsAfterAck(t *testing.T) {
	srv := herdrtest.New(t)
	srv.SetDropStreamsAfterAck(true)

	conn, reader := dialSubscribe(t, srv.SocketPath)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	ack, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if !strings.Contains(string(ack), `"terminals"`) {
		t.Fatalf("ack = %s", ack)
	}
	expectClosed(t, reader, "the stream right after the ack")

	srv.SetDropStreamsAfterAck(false)
	conn2, reader2 := dialSubscribe(t, srv.SocketPath)
	if _, err := reader2.ReadBytes('\n'); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	srv.EmitGlobal("created", map[string]any{"terminal": "w1:p9"})
	_ = conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := reader2.ReadBytes('\n'); err != nil {
		t.Errorf("stream should stay open once the knob is off: %v", err)
	}
}
