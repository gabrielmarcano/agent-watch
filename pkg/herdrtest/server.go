package herdrtest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Call records an incoming JSON-RPC request to the fake server.
type Call struct {
	ID     string // the request id, as sent by the client
	Method string
	Params map[string]any
}

type failEntry struct {
	code    string
	message string
}

// Hold pauses one call until it is released. See Server.HoldNext.
type Hold struct {
	received chan struct{}
	release  chan struct{}
	once     sync.Once
}

// Received is closed once the held call has reached the server.
func (h *Hold) Received() <-chan struct{} { return h.received }

// Release lets the held call be answered. Calling it more than once is safe.
func (h *Hold) Release() { h.once.Do(func() { close(h.release) }) }

// wait blocks until the hold is released (true) or the server stops (false).
func (h *Hold) wait(stopped <-chan struct{}) bool {
	select {
	case <-h.release:
		return true
	case <-stopped:
		return false
	}
}

type subscriber struct {
	conn       net.Conn
	paneIDs    map[string]bool // immutable after creation
	eventTypes map[string]bool // immutable after creation
	mu         sync.Mutex      // serializes writes to conn
}

// Server is an in-process fake herdr socket server.
type Server struct {
	SocketPath   string
	dir          string
	t            testing.TB
	listener     net.Listener
	mu           sync.Mutex
	agents       []map[string]any
	workspaces   []map[string]any
	screens      map[string]map[string]string // paneID -> source -> text
	calls        []Call
	failNext     map[string]failEntry
	holds        map[string]*Hold
	dropAfterAck bool
	subscribers  []*subscriber
	stopped      chan struct{} // closed by Stop; a new one per Start
	running      bool
	closed       bool

	// Test hooks for the subscribe handshake (internal tests only).
	testHookBeforeRegister func()
	testHookAfterAck       func()
}

// New creates and starts a new in-process fake herdr socket server.
// Registers cleanup on t so the server stops when the test completes.
func New(t testing.TB) *Server {
	dir, err := os.MkdirTemp("", "hs")
	if err != nil {
		t.Fatalf("herdrtest: MkdirTemp failed: %v", err)
	}

	sockPath := filepath.Join(dir, "herdr.sock")
	s := &Server{
		SocketPath: sockPath,
		dir:        dir,
		t:          t,
		screens:    make(map[string]map[string]string),
		failNext:   make(map[string]failEntry),
		holds:      make(map[string]*Hold),
	}

	s.Start()

	t.Cleanup(func() {
		s.Close()
	})

	return s
}

// Start begins listening on SocketPath.
func (s *Server) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return
	}

	_ = os.Remove(s.SocketPath)

	l, err := net.Listen("unix", s.SocketPath)
	if err != nil {
		if s.t != nil {
			s.t.Fatalf("herdrtest: listen failed: %v", err)
		}
		return
	}

	s.listener = l
	s.stopped = make(chan struct{})
	s.running = true
	go s.serve(l)
}

// Stop closes the active listener, terminates all subscriber streams and
// abandons held calls, simulating herdr going offline.
func (s *Server) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stopped)
	l := s.listener
	s.listener = nil

	subs := s.subscribers
	s.subscribers = nil
	s.mu.Unlock()

	if l != nil {
		_ = l.Close()
	}

	for _, sub := range subs {
		_ = sub.conn.Close()
	}

	_ = os.Remove(s.SocketPath)
}

// Close permanently stops the server and cleans up the temporary socket directory.
func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	dir := s.dir
	s.mu.Unlock()

	s.Stop()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

// SetAgents replaces the agent.list payload.
func (s *Server) SetAgents(agents []map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents = make([]map[string]any, len(agents))
	copy(s.agents, agents)
}

// SetWorkspaces replaces the workspace.list payload.
func (s *Server) SetWorkspaces(workspaces []map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workspaces = make([]map[string]any, len(workspaces))
	copy(s.workspaces, workspaces)
}

// SetScreen sets the text returned by agent.read for a pane and source.
func (s *Server) SetScreen(paneID, source, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.screens[paneID] == nil {
		s.screens[paneID] = make(map[string]string)
	}
	s.screens[paneID][source] = text
}

// FailNext configures the next call to method to return code and message.
func (s *Server) FailNext(method, code, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext[method] = failEntry{code: code, message: message}
}

// HoldNext makes the next call to method wait before it is answered, to
// simulate a slow or hung herdr. The answer is computed when the request
// arrives, so it reflects the state at that moment, and is written only after
// Release. For events.subscribe the ack (and the stream) start only after
// Release. A call that is never released is dropped when the server stops.
func (s *Server) HoldNext(method string) *Hold {
	h := &Hold{received: make(chan struct{}), release: make(chan struct{})}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.holds[method] = h
	return h
}

// DropStreams closes every open events.subscribe stream while the server
// keeps answering, simulating a stream drop with herdr still up.
func (s *Server) DropStreams() {
	s.mu.Lock()
	subs := s.subscribers
	s.subscribers = nil
	s.mu.Unlock()
	for _, sub := range subs {
		_ = sub.conn.Close()
	}
}

// SetDropStreamsAfterAck makes every events.subscribe close its connection
// right after the ack (on), or stream normally again (off).
func (s *Server) SetDropStreamsAfterAck(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropAfterAck = on
}

// Calls returns a copy of all requests received so far.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Call, len(s.calls))
	copy(out, s.calls)
	return out
}

// EmitStatusChanged pushes a pane_agent_status_changed event to matching subscribers.
func (s *Server) EmitStatusChanged(paneID, status string) {
	s.mu.Lock()
	subs := make([]*subscriber, len(s.subscribers))
	copy(subs, s.subscribers)
	s.mu.Unlock()

	eventData := map[string]any{
		"event": "pane_agent_status_changed",
		"data": map[string]any{
			"pane_id":      paneID,
			"workspace_id": "w1",
			"agent_status": status,
		},
	}
	line, _ := json.Marshal(eventData)
	line = append(line, '\n')

	for _, sub := range subs {
		if sub.paneIDs[paneID] {
			sub.mu.Lock()
			_, err := sub.conn.Write(line)
			sub.mu.Unlock()
			if err != nil {
				s.removeSubscriber(sub)
			}
		}
	}
}

// EmitGlobal pushes an arbitrary event to subscribers.
func (s *Server) EmitGlobal(event string, data map[string]any) {
	s.mu.Lock()
	subs := make([]*subscriber, len(s.subscribers))
	copy(subs, s.subscribers)
	s.mu.Unlock()

	// Normalize event name to snake_case if passed as dot-form
	eventName := strings.ReplaceAll(event, ".", "_")
	dotEvent := strings.ReplaceAll(event, "_", ".")

	eventMsg := map[string]any{
		"event": eventName,
		"data":  data,
	}
	line, _ := json.Marshal(eventMsg)
	line = append(line, '\n')

	for _, sub := range subs {
		if sub.eventTypes[dotEvent] || sub.eventTypes[eventName] || len(sub.eventTypes) == 0 {
			sub.mu.Lock()
			_, err := sub.conn.Write(line)
			sub.mu.Unlock()
			if err != nil {
				s.removeSubscriber(sub)
			}
		}
	}
}

func (s *Server) removeSubscriber(target *subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = target.conn.Close()
	var remaining []*subscriber
	for _, sub := range s.subscribers {
		if sub != target {
			remaining = append(remaining, sub)
		}
	}
	s.subscribers = remaining
}

func (s *Server) serve(l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		_ = conn.Close()
		return
	}

	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		s.writeJSON(conn, errorResp("", "invalid_request", "malformed json"))
		_ = conn.Close()
		return
	}

	// Reject numeric or missing ids, like herdr does.
	idRaw, hasID := raw["id"]
	idStr, isStr := idRaw.(string)
	if !hasID || !isStr {
		s.writeJSON(conn, errorResp("", "invalid_request", "invalid id: expected string"))
		_ = conn.Close()
		return
	}

	method, _ := raw["method"].(string)
	params, _ := raw["params"].(map[string]any)
	if params == nil {
		params = make(map[string]any)
	}

	s.mu.Lock()
	s.calls = append(s.calls, Call{ID: idStr, Method: method, Params: params})
	stopped := s.stopped
	hold := s.holds[method]
	delete(s.holds, method)
	fail, failing := s.failNext[method]
	if failing {
		delete(s.failNext, method)
	}
	s.mu.Unlock()

	if hold != nil {
		close(hold.received)
	}

	switch {
	case failing:
		s.answer(conn, errorResp(idStr, fail.code, fail.message), hold, stopped)
	case method == "events.subscribe":
		s.handleSubscribe(conn, idStr, params, hold, stopped)
	default:
		s.answer(conn, s.respond(idStr, method, params), hold, stopped)
	}
}

// answer writes resp once the hold (if any) is released, then closes conn:
// one request per connection.
func (s *Server) answer(conn net.Conn, resp map[string]any, hold *Hold, stopped <-chan struct{}) {
	defer conn.Close()
	if hold != nil && !hold.wait(stopped) {
		return
	}
	s.writeJSON(conn, resp)
}

// respond computes the answer to a one-shot method from the current state.
func (s *Server) respond(id, method string, params map[string]any) map[string]any {
	switch method {
	case "ping":
		return resultResp(id, map[string]any{
			"type":         "pong",
			"version":      "0.9.1",
			"protocol":     22,
			"capabilities": map[string]any{},
		})

	case "agent.list":
		s.mu.Lock()
		agentsCopy := make([]map[string]any, len(s.agents))
		copy(agentsCopy, s.agents)
		s.mu.Unlock()
		return resultResp(id, map[string]any{"type": "agent_list", "agents": agentsCopy})

	case "workspace.list":
		s.mu.Lock()
		workspacesCopy := make([]map[string]any, len(s.workspaces))
		copy(workspacesCopy, s.workspaces)
		s.mu.Unlock()
		return resultResp(id, map[string]any{"type": "workspace_list", "workspaces": workspacesCopy})

	case "agent.get":
		target, _ := params["target"].(string)
		found := s.findAgent(target)
		if found == nil {
			return notFound(id, target)
		}
		return resultResp(id, found)

	case "agent.read":
		target, _ := params["target"].(string)
		source, _ := params["source"].(string)

		// Must be visible, recent, recent_unwrapped or detection (underscore, not hyphen)
		if source != "visible" && source != "recent" && source != "recent_unwrapped" && source != "detection" {
			return errorResp(id, "invalid_request", fmt.Sprintf("unknown variant of enum Source: %q", source))
		}

		s.mu.Lock()
		text := ""
		if s.screens[target] != nil {
			text = s.screens[target][source]
		}
		s.mu.Unlock()

		return resultResp(id, map[string]any{
			"type": "pane_read",
			"read": map[string]any{
				"pane_id":      target,
				"text":         text,
				"truncated":    false,
				"revision":     0,
				"source":       source,
				"format":       "text",
				"workspace_id": "w1",
				"tab_id":       "t1",
			},
		})

	case "agent.send_keys":
		target, _ := params["target"].(string)
		if s.findAgent(target) == nil {
			return notFound(id, target)
		}
		return resultResp(id, map[string]any{"type": "ok"})

	case "agent.prompt":
		target, _ := params["target"].(string)
		targetAgent := s.findAgent(target)
		if targetAgent == nil {
			return notFound(id, target)
		}
		if targetAgent["agent_status"] == "blocked" {
			return errorResp(id, "agent_blocked", "agent is currently blocked")
		}
		return resultResp(id, map[string]any{"type": "ok"})

	case "session.snapshot":
		s.mu.Lock()
		agentsCopy := make([]map[string]any, len(s.agents))
		copy(agentsCopy, s.agents)
		s.mu.Unlock()
		return resultResp(id, map[string]any{
			"type":       "session_snapshot",
			"workspaces": []any{},
			"agents":     agentsCopy,
		})

	default:
		return errorResp(id, "invalid_request", fmt.Sprintf("unknown method: %s", method))
	}
}

// findAgent returns the agent whose pane_id or name is target, or nil. It
// reads the agent list under the lock.
func (s *Server) findAgent(target string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.agents {
		if a["pane_id"] == target || a["name"] == target {
			return a
		}
	}
	return nil
}

func (s *Server) handleSubscribe(conn net.Conn, id string, params map[string]any, hold *Hold, stopped <-chan struct{}) {
	subsRaw, _ := params["subscriptions"].([]any)
	paneIDs := make(map[string]bool)
	eventTypes := make(map[string]bool)

	for _, subItem := range subsRaw {
		subMap, ok := subItem.(map[string]any)
		if !ok {
			continue
		}
		t, _ := subMap["type"].(string)
		eventTypes[t] = true
		if t == "pane.agent_status_changed" {
			paneID, _ := subMap["pane_id"].(string)
			if paneID == "" {
				s.answer(conn, errorResp(id, "invalid_request", "missing field `pane_id`"), hold, stopped)
				return
			}
			paneIDs[paneID] = true
		}
	}

	if hold != nil && !hold.wait(stopped) {
		_ = conn.Close()
		return
	}

	s.mu.Lock()
	dropAfterAck := s.dropAfterAck
	beforeRegister, afterAck := s.testHookBeforeRegister, s.testHookAfterAck
	s.mu.Unlock()

	ack := resultResp(id, map[string]any{"type": "subscription_started"})
	if dropAfterAck {
		s.writeJSON(conn, ack)
		_ = conn.Close()
		return
	}

	if beforeRegister != nil {
		beforeRegister()
	}

	// Register before writing the ack, and keep sub.mu until the ack is out:
	// an event emitted as soon as the client sees the ack is then neither lost
	// nor written ahead of the ack.
	sub := &subscriber{conn: conn, paneIDs: paneIDs, eventTypes: eventTypes}
	sub.mu.Lock()
	s.mu.Lock()
	select {
	case <-stopped:
		// The server stopped while this call was in flight: herdr is gone.
		s.mu.Unlock()
		sub.mu.Unlock()
		_ = conn.Close()
		return
	default:
	}
	s.subscribers = append(s.subscribers, sub)
	s.mu.Unlock()
	s.writeJSON(conn, ack)
	sub.mu.Unlock()

	if afterAck != nil {
		afterAck()
	}

	// Read loop to detect disconnect
	go func() {
		buf := make([]byte, 128)
		for {
			if _, err := conn.Read(buf); err != nil {
				s.removeSubscriber(sub)
				return
			}
		}
	}()
}

func (s *Server) writeJSON(conn net.Conn, v any) {
	data, _ := json.Marshal(v)
	data = append(data, '\n')
	_, _ = conn.Write(data)
}

func resultResp(id string, result map[string]any) map[string]any {
	return map[string]any{"id": id, "result": result}
}

func errorResp(id, code, message string) map[string]any {
	return map[string]any{
		"id": id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
}

func notFound(id, target string) map[string]any {
	return errorResp(id, "agent_not_found", fmt.Sprintf("agent target %s not found", target))
}
