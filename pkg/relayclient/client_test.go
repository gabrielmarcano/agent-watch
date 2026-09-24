package relayclient

import (
	"context"
	"fmt"
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
