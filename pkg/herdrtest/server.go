package herdrtest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Call records a single incoming RPC call to the fake herdr-core server.
type Call struct {
	ID     string
	Method string
	Params map[string]any
}

type failRule struct {
	code    string
	message string
}

// Hold parks the next call to a method after it has been read, until Release
// is called or the server stops.
type Hold struct {
	received chan struct{}
	release  chan struct{}
	once     sync.Once
}

// Received closes as soon as the server has read the held request.
func (h *Hold) Received() <-chan struct{} { return h.received }

// Release lets the held call proceed and answer. Safe to call more than once.
func (h *Hold) Release() { h.once.Do(func() { close(h.release) }) }

type subscriber struct {
	conn net.Conn
	mu   sync.Mutex // serializes writes after the ack
}

// Server is an in-process fake herdr-core Unix socket server for testing.
type Server struct {
	t          testing.TB
	SocketPath string

	mu           sync.RWMutex
	listener     net.Listener
	stopped      bool
	stopCh       chan struct{}
	activeConns  map[net.Conn]struct{}
	subscribers  map[*subscriber]struct{}
	dropAfterAck bool
	emitTimeout  time.Duration
	calls        []Call
	terminals    []map[string]any
	screens      map[string]map[string]string // target -> source -> text
	failNext     map[string]failRule
	holdNext     map[string]*Hold
	nextSeq      int

	// Internal test hooks (used only by server_internal_test.go).
	testHookBeforeRegister func()
	testHookAfterAck       func()
}

// New creates and starts a new fake herdr-core server bound to a short temporary socket path.
func New(t testing.TB) *Server {
	t.Helper()

	dir, err := os.MkdirTemp("", "ht")
	if err != nil {
		t.Fatalf("herdrtest: failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})

	sockPath := filepath.Join(dir, "s.sock")

	s := &Server{
		t:           t,
		SocketPath:  sockPath,
		emitTimeout: time.Second,
		activeConns: make(map[net.Conn]struct{}),
		subscribers: make(map[*subscriber]struct{}),
		screens:     make(map[string]map[string]string),
		failNext:    make(map[string]failRule),
		holdNext:    make(map[string]*Hold),
		terminals:   []map[string]any{},
	}

	s.Start()
	t.Cleanup(func() {
		s.Stop()
	})

	return s
}

// Start begins listening on the Unix socket.
func (s *Server) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listener != nil {
		return
	}

	_ = os.Remove(s.SocketPath)

	l, err := net.Listen("unix", s.SocketPath)
	if err != nil {
		s.t.Fatalf("herdrtest: failed to listen on %s: %v", s.SocketPath, err)
	}

	s.listener = l
	s.stopped = false
	s.stopCh = make(chan struct{})

	go s.acceptLoop(l)
}

// Stop closes the listener and all active connections.
func (s *Server) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	if s.stopCh != nil {
		close(s.stopCh)
	}
	if s.listener != nil {
		_ = s.listener.Close()
		s.listener = nil
	}
	for c := range s.activeConns {
		_ = c.Close()
	}
	s.activeConns = make(map[net.Conn]struct{})
	s.subscribers = make(map[*subscriber]struct{})
	s.mu.Unlock()

	_ = os.Remove(s.SocketPath)
}

// DropStreams closes every open events.subscribe connection while leaving the
// listener up.
func (s *Server) DropStreams() {
	s.mu.Lock()
	subs := make([]*subscriber, 0, len(s.subscribers))
	for sub := range s.subscribers {
		subs = append(subs, sub)
	}
	s.subscribers = make(map[*subscriber]struct{})
	s.mu.Unlock()

	for _, sub := range subs {
		_ = sub.conn.Close()
	}
}

// SetDropStreamsAfterAck makes events.subscribe write its ack and immediately
// close the connection.
func (s *Server) SetDropStreamsAfterAck(drop bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropAfterAck = drop
}

// HoldNext parks the next call to method after its request has been read.
func (s *Server) HoldNext(method string) *Hold {
	h := &Hold{
		received: make(chan struct{}),
		release:  make(chan struct{}),
	}
	s.mu.Lock()
	s.holdNext[method] = h
	s.mu.Unlock()
	return h
}

// SetTerminals replaces the terminal.list payload with normalized herdr-core Terminal maps.
func (s *Server) SetTerminals(terminals []map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]any, 0, len(terminals))
	for _, t := range terminals {
		out = append(out, normalizeTerminalRow(t))
	}
	s.terminals = out
}

// SetAgents replaces the terminal.list payload (accepting either herdr-core
// Terminal maps or test shorthand maps with pane_id/agent/agent_status/state_change_seq).
func (s *Server) SetAgents(agents []map[string]any) {
	s.SetTerminals(agents)
}

func normalizeTerminalRow(row map[string]any) map[string]any {
	out := make(map[string]any, len(row)+6)
	for k, v := range row {
		out[k] = v
	}

	if addr := stringVal(out["address"]); addr == "" {
		if pid := stringVal(out["pane_id"]); pid != "" {
			out["address"] = pid
		}
	}
	if stringVal(out["state"]) == "" {
		out["state"] = "live"
	}

	if title := stringVal(out["title"]); title != "" {
		out["title"] = title
	} else {
		delete(out, "title")
	}
	if cwd := stringVal(out["cwd"]); cwd != "" {
		out["cwd"] = cwd
	} else {
		delete(out, "cwd")
	}

	labels := make(map[string]string)
	switch raw := out["labels"].(type) {
	case map[string]string:
		for k, v := range raw {
			labels[k] = v
		}
	case map[string]any:
		for k, v := range raw {
			if sv := stringVal(v); sv != "" {
				labels[k] = sv
			}
		}
	}
	if ws := stringVal(out["workspace_id"]); ws != "" && labels["herdr.space"] == "" {
		labels["herdr.space"] = ws
	}
	if tab := stringVal(out["tab_id"]); tab != "" && labels["herdr.tab"] == "" {
		labels["herdr.tab"] = tab
	}
	if wsLabel := stringVal(out["workspace_label"]); wsLabel != "" && labels["herdr.workspace_label"] == "" {
		labels["herdr.workspace_label"] = wsLabel
	}
	if tabLabel := stringVal(out["tab_label"]); tabLabel != "" && labels["herdr.tab_label"] == "" {
		labels["herdr.tab_label"] = tabLabel
	}
	if len(labels) > 0 {
		out["labels"] = labels
	}

	if _, hasTurn := out["turn"]; !hasTurn {
		if seq, ok := out["state_change_seq"]; ok {
			out["turn"] = seq
		}
	}

	if _, hasStatus := out["status"].(map[string]any); !hasStatus {
		agentName := stringVal(out["agent"])
		agentStatus := stringVal(out["agent_status"])
		_, hasAgentStatusKey := out["agent_status"]
		if agentName != "" || agentStatus != "" || hasAgentStatusKey {
			if agentStatus == "" && !hasAgentStatusKey {
				if agentName != "" {
					agentStatus = "idle"
				} else {
					agentStatus = "unknown"
				}
			}
			st := map[string]any{
				"state": agentStatus,
			}
			if agentName != "" {
				st["app"] = agentName
			}
			if kind := stringVal(out["status_kind"]); kind != "" {
				st["kind"] = kind
			}
			if msg := stringVal(out["status_msg"]); msg != "" {
				st["msg"] = msg
			}
			out["status"] = st
		}
	}

	return out
}

func stringVal(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case *string:
		if x != nil {
			return *x
		}
	}
	return ""
}

// SetScreen sets the text returned by terminal.read for a target and source
// ("visible" or "recent_unwrapped").
func (s *Server) SetScreen(paneID, source, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.screens[paneID] == nil {
		s.screens[paneID] = make(map[string]string)
	}
	s.screens[paneID][source] = text
}

// FailNext configures the server to fail the next call to method with the given error code and message.
func (s *Server) FailNext(method, code, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext[method] = failRule{code: code, message: message}
}

// Calls returns a snapshot of all recorded RPC calls since server creation.
func (s *Server) Calls() []Call {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]Call, len(s.calls))
	copy(res, s.calls)
	return res
}

// EmitStatusChanged pushes a status event notification to all subscribers.
func (s *Server) EmitStatusChanged(paneID, status string) {
	s.EmitEvent("status", map[string]any{
		"terminal": paneID,
		"state":    status,
	})
}

// EmitGlobal pushes an event notification to all subscribers.
func (s *Server) EmitGlobal(event string, data map[string]any) {
	s.EmitEvent(event, data)
}

// EmitEvent pushes a JSON-RPC 2.0 "event" notification to all subscribers.
func (s *Server) EmitEvent(kind string, data map[string]any) {
	s.mu.RLock()
	timeout := s.emitTimeout
	targets := make([]*subscriber, 0, len(s.subscribers))
	for sub := range s.subscribers {
		targets = append(targets, sub)
	}
	s.mu.RUnlock()

	params := make(map[string]any, len(data)+1)
	for k, v := range data {
		params[k] = v
	}
	if _, ok := params["kind"]; !ok {
		params["kind"] = kind
	}

	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "event",
		"params":  params,
	})
	if err != nil {
		return
	}
	payload = append(payload, '\n')

	for _, sub := range targets {
		s.writeToSubscriber(sub, payload, timeout)
	}
}

func (s *Server) writeToSubscriber(sub *subscriber, payload []byte, timeout time.Duration) {
	sub.mu.Lock()
	_ = sub.conn.SetWriteDeadline(time.Now().Add(timeout))
	_, err := sub.conn.Write(payload)
	_ = sub.conn.SetWriteDeadline(time.Time{})
	sub.mu.Unlock()
	if err != nil {
		_ = sub.conn.Close()
		s.mu.Lock()
		delete(s.subscribers, sub)
		s.mu.Unlock()
	}
}

func (s *Server) acceptLoop(l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}

		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			_ = conn.Close()
			return
		}
		s.activeConns[conn] = struct{}{}
		s.mu.Unlock()

		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	keepOpen := false
	defer func() {
		if !keepOpen {
			s.mu.Lock()
			delete(s.activeConns, conn)
			s.mu.Unlock()
			_ = conn.Close()
		}
	}()

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return
	}

	var rawReq struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params map[string]any  `json:"params"`
	}
	if err := json.Unmarshal(line, &rawReq); err != nil {
		s.writeError(conn, "", "invalid_request", "malformed JSON request")
		return
	}

	var reqID string
	if len(rawReq.ID) == 0 || string(rawReq.ID) == "null" {
		s.writeError(conn, "", "invalid_request", "id must be a string")
		return
	}
	if err := json.Unmarshal(rawReq.ID, &reqID); err != nil {
		s.writeError(conn, "", "invalid_request", "id must be a string, not a number or object")
		return
	}

	if rawReq.Params == nil {
		rawReq.Params = make(map[string]any)
	}

	s.mu.Lock()
	if rawReq.Method != "hello" && rawReq.Method != "ping" {
		s.calls = append(s.calls, Call{
			ID:     reqID,
			Method: rawReq.Method,
			Params: rawReq.Params,
		})
	} else {
		s.calls = append(s.calls, Call{
			ID:     reqID,
			Method: rawReq.Method,
			Params: rawReq.Params,
		})
	}
	hold := s.holdNext[rawReq.Method]
	delete(s.holdNext, rawReq.Method)
	stopCh := s.stopCh
	fail, shouldFail := s.failNext[rawReq.Method]
	if shouldFail {
		delete(s.failNext, rawReq.Method)
	}
	var heldTerminals []map[string]any
	if hold != nil {
		heldTerminals = make([]map[string]any, len(s.terminals))
		copy(heldTerminals, s.terminals)
	}
	s.mu.Unlock()

	if hold != nil {
		close(hold.received)
		select {
		case <-hold.release:
		case <-stopCh:
			return
		}
	}

	if shouldFail {
		s.writeError(conn, reqID, fail.code, fail.message)
		return
	}

	switch rawReq.Method {
	case "hello", "ping":
		s.writeResult(conn, reqID, map[string]any{
			"generation": 1,
			"version":    "0.1.14",
			"framed":     false,
			"core": map[string]any{
				"id":   "test-core-id",
				"name": "test-host",
			},
			"features": []string{"events", "status", "shell"},
		})

	case "terminal.list":
		terminals := heldTerminals
		if terminals == nil {
			s.mu.RLock()
			terminals = make([]map[string]any, len(s.terminals))
			copy(terminals, s.terminals)
			s.mu.RUnlock()
		}
		s.writeResult(conn, reqID, map[string]any{
			"terminals":       terminals,
			"hidden_finished": 0,
			"machines":        []any{},
		})

	case "terminal.get":
		target := stringVal(rawReq.Params["terminal"])
		if target == "" {
			target = stringVal(rawReq.Params["target"])
		}
		s.mu.RLock()
		found := s.findTerminalLocked(target)
		s.mu.RUnlock()

		if found == nil {
			s.writeError(conn, reqID, "not_found", fmt.Sprintf("no terminal %q", target))
			return
		}
		s.writeResult(conn, reqID, found)

	case "terminal.read":
		target := stringVal(rawReq.Params["terminal"])
		if target == "" {
			target = stringVal(rawReq.Params["target"])
		}
		if target == "" {
			s.writeError(conn, reqID, "invalid_params", "missing terminal parameter")
			return
		}
		lines := 0
		if rawLines, ok := rawReq.Params["lines"].(float64); ok {
			lines = int(rawLines)
		}

		s.mu.RLock()
		var text string
		if targetScreens, ok := s.screens[target]; ok {
			if lines > 0 {
				if u, ok := targetScreens["recent_unwrapped"]; ok {
					text = u
				} else {
					text = targetScreens["visible"]
				}
			} else {
				if v, ok := targetScreens["visible"]; ok {
					text = v
				} else {
					text = targetScreens["recent_unwrapped"]
				}
			}
		}
		s.mu.RUnlock()

		s.writeResult(conn, reqID, map[string]any{
			"text":     text,
			"complete": true,
			"how":      "screen",
		})

	case "terminal.keys":
		target := stringVal(rawReq.Params["terminal"])
		if target == "" {
			target = stringVal(rawReq.Params["target"])
		}
		s.mu.RLock()
		found := s.findTerminalLocked(target)
		s.mu.RUnlock()
		if found == nil {
			s.writeError(conn, reqID, "not_found", fmt.Sprintf("no terminal %q", target))
			return
		}
		s.writeResult(conn, reqID, map[string]any{
			"turn":   found["turn"],
			"status": terminalStatusState(found),
		})

	case "terminal.send":
		target := stringVal(rawReq.Params["terminal"])
		if target == "" {
			target = stringVal(rawReq.Params["target"])
		}
		s.mu.RLock()
		found := s.findTerminalLocked(target)
		s.mu.RUnlock()
		if found == nil {
			s.writeError(conn, reqID, "not_found", fmt.Sprintf("no terminal %q", target))
			return
		}
		force, _ := rawReq.Params["force"].(bool)
		if !force && terminalStatusState(found) == "blocked" {
			s.writeError(conn, reqID, "blocked", "terminal is blocked on a prompt")
			return
		}
		s.writeResult(conn, reqID, map[string]any{
			"turn":    found["turn"],
			"status":  terminalStatusState(found),
			"started": true,
		})

	case "terminal.run":
		var argv []string
		if rawArgv, ok := rawReq.Params["argv"].([]any); ok {
			for _, a := range rawArgv {
				if str, ok := a.(string); ok {
					argv = append(argv, str)
				}
			}
		}
		if len(argv) == 0 {
			s.writeError(conn, reqID, "invalid_params", "argv is required")
			return
		}
		cwd := stringVal(rawReq.Params["cwd"])
		name := stringVal(rawReq.Params["name"])
		labels := make(map[string]string)
		if rawLabels, ok := rawReq.Params["labels"].(map[string]any); ok {
			for k, v := range rawLabels {
				if sv := stringVal(v); sv != "" {
					labels[k] = sv
				}
			}
		}

		s.mu.Lock()
		s.nextSeq++
		addr := fmt.Sprintf("t%d", s.nextSeq)
		if labels["herdr.space"] != "" && labels["herdr.tab"] == "" {
			labels["herdr.tab"] = fmt.Sprintf("tb%d", s.nextSeq)
		}
		term := map[string]any{
			"address": addr,
			"state":   "live",
			"argv":    argv,
			"cwd":     cwd,
			"turn":    0,
			"labels":  labels,
			"foreground": map[string]any{
				"pid":  1000 + s.nextSeq,
				"name": argv[0],
				"argv": argv,
				"cwd":  cwd,
			},
			"status": map[string]any{
				"state": "idle",
				"app":   argv[0],
			},
		}
		if name != "" {
			term["name"] = name
		}
		s.terminals = append(s.terminals, term)
		s.mu.Unlock()

		s.writeResult(conn, reqID, term)

	case "events.subscribe":
		sub := &subscriber{conn: conn}

		s.mu.RLock()
		beforeHook := s.testHookBeforeRegister
		afterHook := s.testHookAfterAck
		s.mu.RUnlock()
		if beforeHook != nil {
			beforeHook()
		}

		sub.mu.Lock()
		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			sub.mu.Unlock()
			return
		}
		drop := s.dropAfterAck
		if !drop {
			s.subscribers[sub] = struct{}{}
			keepOpen = true
		}
		terminals := make([]map[string]any, len(s.terminals))
		copy(terminals, s.terminals)
		s.mu.Unlock()

		s.writeResult(conn, reqID, map[string]any{
			"seq":       1,
			"terminals": terminals,
		})
		sub.mu.Unlock()

		if afterHook != nil {
			afterHook()
		}

	default:
		s.writeError(conn, reqID, "invalid_request", fmt.Sprintf("unknown method: %s", rawReq.Method))
	}
}

func (s *Server) findTerminalLocked(target string) map[string]any {
	for _, t := range s.terminals {
		if stringVal(t["address"]) == target || stringVal(t["pane_id"]) == target {
			return t
		}
	}
	return nil
}

func terminalStatusState(t map[string]any) string {
	if st, ok := t["status"].(map[string]any); ok {
		if state := stringVal(st["state"]); state != "" {
			return state
		}
	}
	return stringVal(t["agent_status"])
}

func (s *Server) writeResult(conn net.Conn, id string, result any) {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = conn.Write(data)
}

func (s *Server) writeError(conn net.Conn, id, code, message string) {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    -32000,
			"message": message,
			"data": map[string]any{
				"kind": code,
			},
		},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = conn.Write(data)
}
