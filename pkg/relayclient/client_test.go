package relayclient

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

func TestClient_ConnectAndAuth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	token := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	var authHeader string
	var queryStr string
	receivedMsgs := make(chan any, 10)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		queryStr = r.URL.RawQuery

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		for {
			typ, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if typ == websocket.MessageText {
				msg, err := model.DecodeWire(data)
				if err == nil {
					receivedMsgs <- msg
				}
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/host"

	client := &Client{
		URL:   wsURL,
		Token: token,
		OnConnect: func(ctx context.Context) []any {
			return []any{
				model.HelloMsg{Type: "hello", Version: "0.2.0", Host: "test-host"},
				model.SnapshotMsg{Type: "snapshot", Agents: []model.AgentState{}},
			}
		},
	}

	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	go func() {
		_ = client.Run(runCtx)
	}()

	// Wait for connected
	for i := 0; i < 50; i++ {
		if client.Connected() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !client.Connected() {
		t.Fatal("client failed to connect within timeout")
	}

	// Verify Auth header
	expectedAuth := "Bearer " + token
	if authHeader != expectedAuth {
		t.Errorf("Authorization header = %q, want %q", authHeader, expectedAuth)
	}
	if queryStr != "" {
		t.Errorf("query string was not empty: %q", queryStr)
	}

	// Verify OnConnect ordering: hello first, then snapshot
	select {
	case m := <-receivedMsgs:
		hello, ok := m.(model.HelloMsg)
		if !ok || hello.Host != "test-host" {
			t.Errorf("expected HelloMsg first, got %T: %+v", m, m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for hello msg")
	}

	select {
	case m := <-receivedMsgs:
		snapshot, ok := m.(model.SnapshotMsg)
		if !ok || snapshot.Type != "snapshot" {
			t.Errorf("expected SnapshotMsg second, got %T: %+v", m, m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for snapshot msg")
	}
}

func TestClient_OfflineQueueingAndReconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var connCount int32
	var mu sync.Mutex
	conn1Msgs := make([]string, 0)
	conn2Msgs := make([]string, 0)
	var conn1Closed sync.Once
	conn1Done := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&connCount, 1)

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		if count == 1 {
			// Read until we get history_item or error
			for {
				typ, data, err := conn.Read(r.Context())
				if err != nil {
					return
				}
				if typ == websocket.MessageText {
					msg, err := model.DecodeWire(data)
					if err == nil {
						mu.Lock()
						switch m := msg.(type) {
						case model.HelloMsg:
							conn1Msgs = append(conn1Msgs, m.Type)
						case model.SnapshotMsg:
							conn1Msgs = append(conn1Msgs, m.Type)
						case model.HistoryItemMsg:
							conn1Msgs = append(conn1Msgs, m.Item.ID)
						case model.AgentUpdateMsg:
							conn1Msgs = append(conn1Msgs, m.Type)
						}
						mu.Unlock()

						if _, ok := msg.(model.HistoryItemMsg); ok {
							// Close connection 1 with 4000
							conn1Closed.Do(func() {
								_ = conn.Close(websocket.StatusCode(4000), "replaced")
								close(conn1Done)
							})
							return
						}
					}
				}
			}
		}

		// Connection 2: read messages
		for {
			typ, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if typ == websocket.MessageText {
				msg, err := model.DecodeWire(data)
				if err == nil {
					mu.Lock()
					switch m := msg.(type) {
					case model.HelloMsg:
						conn2Msgs = append(conn2Msgs, m.Type)
					case model.SnapshotMsg:
						conn2Msgs = append(conn2Msgs, m.Type)
					case model.HistoryItemMsg:
						conn2Msgs = append(conn2Msgs, m.Item.ID)
					case model.AgentUpdateMsg:
						conn2Msgs = append(conn2Msgs, m.Type)
					}
					mu.Unlock()
				}
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/host"

	client := &Client{
		URL:        wsURL,
		Token:      "test-token",
		MinBackoff: 50 * time.Millisecond,
		OnConnect: func(ctx context.Context) []any {
			return []any{
				model.HelloMsg{Type: "hello", Version: "0.2.0"},
				model.SnapshotMsg{Type: "snapshot"},
			}
		},
	}

	// While disconnected before Run:
	// 1. Send an update (should be dropped)
	client.Send(model.AgentUpdateMsg{Type: "agent_update", Agent: model.AgentState{PaneID: "p1"}})
	// 2. Send history item (should be queued)
	client.Send(model.HistoryItemMsg{Type: "history_item", Item: model.HistoryItem{ID: "hist1"}})

	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	go func() {
		_ = client.Run(runCtx)
	}()

	// Wait for connection 1 to close
	select {
	case <-conn1Done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for connection 1 to finish")
	}

	// Verify connection 1 received hello, snapshot, hist1 (and no agent_update)
	mu.Lock()
	if len(conn1Msgs) < 3 {
		t.Fatalf("conn1 expected at least 3 messages, got: %v", conn1Msgs)
	}
	if conn1Msgs[0] != "hello" || conn1Msgs[1] != "snapshot" || conn1Msgs[2] != "hist1" {
		t.Errorf("conn1 unexpected sequence: %v", conn1Msgs)
	}
	for _, m := range conn1Msgs {
		if m == "agent_update" {
			t.Errorf("conn1 received agent_update, but it should have been dropped")
		}
	}
	mu.Unlock()

	// Wait for client to report disconnected
	for i := 0; i < 50; i++ {
		if !client.Connected() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// While disconnected during reconnect:
	// Send another update (should be dropped)
	client.Send(model.AgentUpdateMsg{Type: "agent_update", Agent: model.AgentState{PaneID: "p2"}})
	// Send another history item (should be queued and flushed on conn2)
	client.Send(model.HistoryItemMsg{Type: "history_item", Item: model.HistoryItem{ID: "hist2"}})

	// Wait for conn2 to receive hist2
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		hasHist2 := false
		for _, m := range conn2Msgs {
			if m == "hist2" {
				hasHist2 = true
				break
			}
		}
		mu.Unlock()
		if hasHist2 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()

	hasUpdate2 := false
	hasHist2 := false
	for _, m := range conn2Msgs {
		if m == "agent_update" {
			hasUpdate2 = true
		}
		if m == "hist2" {
			hasHist2 = true
		}
	}

	if hasUpdate2 {
		t.Errorf("conn2 received agent_update, but it should have been dropped")
	}
	if !hasHist2 {
		t.Errorf("conn2 did not receive hist2; conn2Msgs: %v", conn2Msgs)
	}
}

func TestClient_HistoryQueueBounded(t *testing.T) {
	client := &Client{}
	// Enqueue 55 history items while offline
	for i := 1; i <= 55; i++ {
		client.Send(model.HistoryItemMsg{
			Type: "history_item",
			Item: model.HistoryItem{ID: fmt.Sprintf("hist-%02d", i)},
		})
	}

	client.mu.Lock()
	defer client.mu.Unlock()

	if len(client.historyQ) != 50 {
		t.Fatalf("expected queue length 50, got %d", len(client.historyQ))
	}
	// Oldest 5 (hist-01 .. hist-05) should have been dropped; first should be hist-06
	if client.historyQ[0].Item.ID != "hist-06" {
		t.Errorf("expected first item to be hist-06, got %s", client.historyQ[0].Item.ID)
	}
	if client.historyQ[49].Item.ID != "hist-55" {
		t.Errorf("expected last item to be hist-55, got %s", client.historyQ[49].Item.ID)
	}
}

// syncBuffer is a goroutine-safe log sink for slog in tests.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func testLogger(b *syncBuffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// waitFor polls cond every 5 ms until it holds or the timeout expires.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// fakeRelay accepts WebSocket connections and records every decoded message
// with the index of the connection it arrived on.
type fakeRelay struct {
	srv   *httptest.Server
	mu    sync.Mutex
	conns int
	msgs  []recordedMsg
	// reject, when it returns a non-zero status, refuses the handshake with it.
	reject func() int
}

type recordedMsg struct {
	conn int
	msg  any
}

func newFakeRelay(t *testing.T) *fakeRelay {
	t.Helper()
	f := &fakeRelay{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		reject := f.reject
		f.mu.Unlock()
		if reject != nil {
			if code := reject(); code != 0 {
				w.WriteHeader(code)
				return
			}
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		f.mu.Lock()
		f.conns++
		idx := f.conns
		f.mu.Unlock()
		for {
			typ, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			if msg, err := model.DecodeWire(data); err == nil {
				f.mu.Lock()
				f.msgs = append(f.msgs, recordedMsg{conn: idx, msg: msg})
				f.mu.Unlock()
			}
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRelay) setReject(fn func() int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reject = fn
}

func (f *fakeRelay) url() string {
	return "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/v1/host"
}

func (f *fakeRelay) snapshot() (int, []recordedMsg) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conns, append([]recordedMsg(nil), f.msgs...)
}

func (f *fakeRelay) hasHistory(id string) bool {
	_, msgs := f.snapshot()
	for _, m := range msgs {
		if h, ok := m.msg.(model.HistoryItemMsg); ok && h.Item.ID == id {
			return true
		}
	}
	return false
}

func helloSnapshot(ctx context.Context) []any {
	return []any{
		model.HelloMsg{Type: model.WireHello, Version: "0.2.0", Host: "test-host"},
		model.SnapshotMsg{Type: model.WireSnapshot, Agents: []model.AgentState{}},
	}
}

// runClient runs c until the test ends, and waits for Run to return.
func runClient(t *testing.T, c *Client) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = c.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// The phase DoD checks the log for the hello: a successful connect must say so.
func TestClient_LogsSuccessfulConnectWithHello(t *testing.T) {
	relay := newFakeRelay(t)
	var logs syncBuffer
	token := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	c := &Client{URL: relay.url(), Token: token, OnConnect: helloSnapshot, Logger: testLogger(&logs), MinBackoff: 10 * time.Millisecond}
	runClient(t, c)

	waitFor(t, 3*time.Second, "connect log", func() bool {
		return strings.Contains(logs.String(), "relay connected")
	})
	out := logs.String()
	if !strings.Contains(out, "hello") {
		t.Errorf("connect log does not mention the hello:\n%s", out)
	}
	if strings.Contains(out, token) {
		t.Errorf("log leaks the token:\n%s", out)
	}
}

// While connected, a full send buffer drops agent_update and command_result:
// that loss must be logged with the message type and id.
func TestClient_LogsDropsWhileConnected(t *testing.T) {
	var logs syncBuffer
	c := &Client{Logger: testLogger(&logs)}
	c.connected = true
	c.sendCh = make(chan outMsg, 1)
	c.flushCh = make(chan struct{}, 1)
	c.sendCh <- outMsg{typ: "filler"}

	c.Send(model.AgentUpdateMsg{Type: model.WireAgentUpdate, Agent: model.AgentState{PaneID: "w1:p1"}})
	c.Send(model.CommandResultMsg{Type: model.WireCommandResult, RequestID: "req-42"})

	out := logs.String()
	for _, want := range []string{"agent_update", "w1:p1", "command_result", "req-42"} {
		if !strings.Contains(out, want) {
			t.Errorf("drop log is missing %q:\n%s", want, out)
		}
	}
}

// A history item that finds the send buffer full while connected is queued
// and flushed on the same connection, not held until the next reconnect.
func TestClient_HistoryQueuedWhileConnectedIsFlushedWithoutReconnect(t *testing.T) {
	relay := newFakeRelay(t)
	c := &Client{URL: relay.url(), Token: "tok", OnConnect: helloSnapshot, MinBackoff: 10 * time.Millisecond}
	runClient(t, c)
	waitFor(t, 3*time.Second, "connect", func() bool {
		n, msgs := relay.snapshot()
		return n == 1 && len(msgs) >= 2
	})

	// Make Send see a full buffer: point it at a channel nobody reads. The
	// connection's writer keeps its own channel and the flush signal.
	c.mu.Lock()
	full := make(chan outMsg, 1)
	full <- outMsg{typ: "filler"}
	c.sendCh = full
	c.mu.Unlock()

	c.Send(model.HistoryItemMsg{Type: model.WireHistoryItem, Item: model.HistoryItem{ID: "hist-live"}})

	waitFor(t, 3*time.Second, "hist-live on the first connection", func() bool {
		return relay.hasHistory("hist-live")
	})
	if n, _ := relay.snapshot(); n != 1 {
		t.Errorf("history flushed after %d connections, want 1 (no reconnect)", n)
	}
}

// The 60 s "healthy connection" clock starts when the dial succeeds, not
// before a dial that may itself take up to 15 s.
func TestClient_ConnectedAtIsAfterTheDial(t *testing.T) {
	gate := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-gate
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = conn.Close(websocket.StatusNormalClosure, "bye")
	}))
	defer srv.Close()

	c := &Client{URL: "ws" + strings.TrimPrefix(srv.URL, "http"), Token: "tok"}
	start := time.Now()
	const handshakeDelay = 80 * time.Millisecond
	time.AfterFunc(handshakeDelay, func() { close(gate) })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connectedAt, _ := c.connectAndServe(ctx)
	if connectedAt.IsZero() {
		t.Fatal("connectAndServe reported no successful dial")
	}
	if got := connectedAt.Sub(start); got < handshakeDelay {
		t.Errorf("connectedAt is %v after start, before the handshake finished (%v)", got, handshakeDelay)
	}
}

// A rejected host token is reported through LastError (for status.json) and
// cleared once a connection succeeds.
func TestClient_LastErrorReportsAuthFailureAndClears(t *testing.T) {
	relay := newFakeRelay(t)
	var allow atomic.Bool
	relay.setReject(func() int {
		if allow.Load() {
			return 0
		}
		return http.StatusUnauthorized
	})
	token := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	c := &Client{URL: relay.url(), Token: token, OnConnect: helloSnapshot, MinBackoff: 5 * time.Millisecond, MaxBackoff: 20 * time.Millisecond}
	runClient(t, c)

	waitFor(t, 3*time.Second, "auth error in LastError", func() bool {
		return strings.Contains(c.LastError(), "401")
	})
	if strings.Contains(c.LastError(), token) {
		t.Errorf("LastError leaks the token: %q", c.LastError())
	}
	allow.Store(true)
	waitFor(t, 3*time.Second, "connect and cleared error", func() bool {
		return c.Connected() && c.LastError() == ""
	})
}

// History frames still sitting in the send buffer when a connection ends go
// back to the offline queue instead of being lost.
func TestClient_RequeuesUnsentHistoryOnDisconnect(t *testing.T) {
	c := &Client{}
	ch := make(chan outMsg, 4)
	h := model.HistoryItemMsg{Type: model.WireHistoryItem, Item: model.HistoryItem{ID: "unsent"}}
	ch <- outMsg{typ: model.WireAgentUpdate}
	ch <- outMsg{typ: model.WireHistoryItem, hist: &h}

	c.mu.Lock()
	c.historyQ = []model.HistoryItemMsg{{Type: model.WireHistoryItem, Item: model.HistoryItem{ID: "queued"}}}
	c.requeueUnsentLocked(ch)
	got := make([]string, 0, len(c.historyQ))
	for _, q := range c.historyQ {
		got = append(got, q.Item.ID)
	}
	c.mu.Unlock()

	if strings.Join(got, ",") != "unsent,queued" {
		t.Errorf("history queue = %v, want [unsent queued]", got)
	}
}

// The relay's version comes from each connection's handshake response. It
// stays while the relay is unreachable, and the next connection replaces it
// ("" from a relay that sends none).
func TestClient_RelayVersionFromHandshake(t *testing.T) {
	const v1 = "0.3.0 (c8aa72e)"
	var version atomic.Value
	version.Store(v1)
	var reject atomic.Bool
	var conns atomic.Int32
	kick := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reject.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if v := version.Load().(string); v != "" {
			w.Header().Set(RelayVersionHeader, v)
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		conns.Add(1)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		go func() { // discard hello, snapshot and the rest
			defer cancel()
			for {
				if _, _, err := conn.Read(ctx); err != nil {
					return
				}
			}
		}()
		select {
		case <-kick:
		case <-ctx.Done():
		}
	}))
	t.Cleanup(srv.Close)

	token := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	c := &Client{
		URL: "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/host", Token: token,
		OnConnect: helloSnapshot, MinBackoff: 5 * time.Millisecond, MaxBackoff: 20 * time.Millisecond,
	}
	if got := c.RelayVersion(); got != "" {
		t.Fatalf("RelayVersion before any connection = %q, want empty", got)
	}
	runClient(t, c)
	waitFor(t, 3*time.Second, "first connection", c.Connected)
	if got := c.RelayVersion(); got != v1 {
		t.Errorf("RelayVersion = %q, want %q", got, v1)
	}

	reject.Store(true)
	kick <- struct{}{}
	waitFor(t, 3*time.Second, "a refused reconnect", func() bool {
		return !c.Connected() && strings.Contains(c.LastError(), "503")
	})
	if got := c.RelayVersion(); got != v1 {
		t.Errorf("RelayVersion while disconnected = %q, want the last relay's %q", got, v1)
	}

	version.Store("") // an older relay: no header
	reject.Store(false)
	waitFor(t, 3*time.Second, "second connection", func() bool { return c.Connected() && conns.Load() == 2 })
	if got := c.RelayVersion(); got != "" {
		t.Errorf("RelayVersion from a relay without the header = %q, want empty", got)
	}
}
