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
	Method string
	Params map[string]any
}

type failEntry struct {
	code    string
	message string
}

type subscriber struct {
	conn       net.Conn
	paneIDs    map[string]bool
	eventTypes map[string]bool
	mu         sync.Mutex
}

// Server is an in-process fake herdr socket server.
type Server struct {
	SocketPath  string
	dir         string
	t           testing.TB
	listener    net.Listener
	mu          sync.Mutex
	agents      []map[string]any
	screens     map[string]map[string]string // paneID -> source -> text
	calls       []Call
	failNext    map[string]failEntry
	subscribers []*subscriber
	running     bool
	closed      bool
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
	s.running = true
	go s.serve(l)
}

// Stop closes the active listener and terminates all subscriber streams,
// simulating herdr going offline.
func (s *Server) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
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
		s.writeError(conn, "", "invalid_request", "malformed json")
		_ = conn.Close()
		return
	}

	// 2. Reject numeric id or non-string id
	idRaw, hasID := raw["id"]
	idStr, isStr := idRaw.(string)
	if !hasID || !isStr {
		s.writeError(conn, "", "invalid_request", "invalid id: expected string")
		_ = conn.Close()
		return
	}

	method, _ := raw["method"].(string)
	params, _ := raw["params"].(map[string]any)
	if params == nil {
		params = make(map[string]any)
	}

	s.mu.Lock()
	s.calls = append(s.calls, Call{Method: method, Params: params})

	// Check FailNext
	if fail, ok := s.failNext[method]; ok {
		delete(s.failNext, method)
		s.mu.Unlock()
		s.writeError(conn, idStr, fail.code, fail.message)
		_ = conn.Close()
		return
	}
	s.mu.Unlock()

	switch method {
	case "ping":
		resp := map[string]any{
			"id": idStr,
			"result": map[string]any{
				"type":         "pong",
				"version":      "0.9.1",
				"protocol":     22,
				"capabilities": map[string]any{},
			},
		}
		s.writeJSON(conn, resp)
		_ = conn.Close()

	case "agent.list":
		s.mu.Lock()
		agentsCopy := make([]map[string]any, len(s.agents))
		copy(agentsCopy, s.agents)
		s.mu.Unlock()

		resp := map[string]any{
			"id": idStr,
			"result": map[string]any{
				"type":   "agent_list",
				"agents": agentsCopy,
			},
		}
		s.writeJSON(conn, resp)
		_ = conn.Close()

	case "agent.get":
		target, _ := params["target"].(string)
		s.mu.Lock()
		var found map[string]any
		for _, a := range s.agents {
			if a["pane_id"] == target || a["name"] == target {
				found = a
				break
			}
		}
		s.mu.Unlock()

		if found != nil {
			s.writeJSON(conn, map[string]any{"id": idStr, "result": found})
		} else {
			s.writeError(conn, idStr, "agent_not_found", fmt.Sprintf("agent target %s not found", target))
		}
		_ = conn.Close()

	case "agent.read":
		target, _ := params["target"].(string)
		source, _ := params["source"].(string)

		// Must be visible, recent, recent_unwrapped or detection (underscore, not hyphen)
		if source != "visible" && source != "recent" && source != "recent_unwrapped" && source != "detection" {
			s.writeError(conn, idStr, "invalid_request", fmt.Sprintf("unknown variant of enum Source: %q", source))
			_ = conn.Close()
			return
		}

		s.mu.Lock()
		text := ""
		if s.screens[target] != nil {
			text = s.screens[target][source]
		}
		s.mu.Unlock()

		resp := map[string]any{
			"id": idStr,
			"result": map[string]any{
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
			},
		}
		s.writeJSON(conn, resp)
		_ = conn.Close()

	case "agent.send_keys":
		target, _ := params["target"].(string)
		s.mu.Lock()
		exists := false
		for _, a := range s.agents {
			if a["pane_id"] == target || a["name"] == target {
				exists = true
				break
			}
		}
		s.mu.Unlock()

		if !exists && len(s.agents) > 0 {
			s.writeError(conn, idStr, "agent_not_found", fmt.Sprintf("agent target %s not found", target))
			_ = conn.Close()
			return
		}

		s.writeJSON(conn, map[string]any{"id": idStr, "result": map[string]any{"type": "ok"}})
		_ = conn.Close()

	case "agent.prompt":
		target, _ := params["target"].(string)
		s.mu.Lock()
		var targetAgent map[string]any
		for _, a := range s.agents {
			if a["pane_id"] == target || a["name"] == target {
				targetAgent = a
				break
			}
		}
		s.mu.Unlock()

		if targetAgent == nil && len(s.agents) > 0 {
			s.writeError(conn, idStr, "agent_not_found", fmt.Sprintf("agent target %s not found", target))
			_ = conn.Close()
			return
		}

		if targetAgent != nil && targetAgent["agent_status"] == "blocked" {
			s.writeError(conn, idStr, "agent_blocked", "agent is currently blocked")
			_ = conn.Close()
			return
		}

		s.writeJSON(conn, map[string]any{"id": idStr, "result": map[string]any{"type": "ok"}})
		_ = conn.Close()

	case "session.snapshot":
		s.mu.Lock()
		agentsCopy := make([]map[string]any, len(s.agents))
		copy(agentsCopy, s.agents)
		s.mu.Unlock()

		resp := map[string]any{
			"id": idStr,
			"result": map[string]any{
				"type":       "session_snapshot",
				"workspaces": []any{},
				"agents":     agentsCopy,
			},
		}
		s.writeJSON(conn, resp)
		_ = conn.Close()

	case "events.subscribe":
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
					s.writeError(conn, idStr, "invalid_request", "missing field `pane_id`")
					_ = conn.Close()
					return
				}
				paneIDs[paneID] = true
			}
		}

		// Acknowledge subscription
		ack := map[string]any{
			"id": idStr,
			"result": map[string]any{
				"type": "subscription_started",
			},
		}
		s.writeJSON(conn, ack)

		// Keep connection open for event streaming
		sub := &subscriber{
			conn:       conn,
			paneIDs:    paneIDs,
			eventTypes: eventTypes,
		}

		s.mu.Lock()
		s.subscribers = append(s.subscribers, sub)
		s.mu.Unlock()

		// Read loop to detect disconnect
		go func() {
			buf := make([]byte, 128)
			for {
				_, err := conn.Read(buf)
				if err != nil {
					s.removeSubscriber(sub)
					return
				}
			}
		}()

	default:
		s.writeError(conn, idStr, "invalid_request", fmt.Sprintf("unknown method: %s", method))
		_ = conn.Close()
	}
}

func (s *Server) writeJSON(conn net.Conn, v any) {
	data, _ := json.Marshal(v)
	data = append(data, '\n')
	_, _ = conn.Write(data)
}

func (s *Server) writeError(conn net.Conn, id, code, message string) {
	resp := map[string]any{
		"id": id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
	s.writeJSON(conn, resp)
}
