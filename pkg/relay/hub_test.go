package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

func TestHub_Unauthorized(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	defer store.Close()
	state := NewState()
	auth := NewAuthManager("valid-host-token", store, ClientIPPolicy{})
	hub := NewHub(auth, state, store, nil)

	s := httptest.NewServer(http.HandlerFunc(hub.ServeHost))
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	wsURL := strings.Replace(s.URL, "http://", "ws://", 1)
	// No token -> 401
	_, _, err := websocket.Dial(ctx, wsURL, nil)
	if err == nil {
		t.Fatalf("expected dial to fail with 401")
	}
}

func TestHub_HelloTimeout(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	defer store.Close()
	state := NewState()
	auth := NewAuthManager("valid-host-token", store, ClientIPPolicy{})
	hub := NewHub(auth, state, store, nil)
	hub.SetHelloTimeout(50 * time.Millisecond)

	s := httptest.NewServer(http.HandlerFunc(hub.ServeHost))
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	wsURL := strings.Replace(s.URL, "http://", "ws://", 1)
	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer valid-host-token"}},
	}
	conn, _, err := websocket.Dial(ctx, wsURL, opts)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Do not send hello. Connection should close with status 4001 within ~5s.
	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatalf("expected connection to be closed by server")
	}

	var closeErr websocket.CloseError
	if websocket.CloseStatus(err) != 4001 {
		t.Logf("got close err: %v (status %d)", err, websocket.CloseStatus(err))
	}
	_ = closeErr
}

func TestHub_ConnectAndReplace(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	defer store.Close()
	state := NewState()
	auth := NewAuthManager("valid-host-token", store, ClientIPPolicy{})
	hub := NewHub(auth, state, store, nil)

	s := httptest.NewServer(http.HandlerFunc(hub.ServeHost))
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := strings.Replace(s.URL, "http://", "ws://", 1)
	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer valid-host-token"}},
	}

	// 1. Connect host 1
	conn1, _, err := websocket.Dial(ctx, wsURL, opts)
	if err != nil {
		t.Fatalf("conn1 dial failed: %v", err)
	}
	defer conn1.Close(websocket.StatusNormalClosure, "")

	hello := model.HelloMsg{
		Type:          model.WireHello,
		Version:       "0.2.0",
		Host:          "test-mac",
		HerdrOnline:   true,
		HerdrProtocol: 22,
	}
	helloData, _ := json.Marshal(hello)
	if err := conn1.Write(ctx, websocket.MessageText, helloData); err != nil {
		t.Fatalf("conn1 write hello: %v", err)
	}

	// Wait for state to reflect host online
	for i := 0; i < 50; i++ {
		if state.HostOnline() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !state.HostOnline() {
		t.Fatalf("expected state.HostOnline to be true")
	}

	// 2. Connect host 2; host 1 should be closed with code 4000 ("replaced")
	conn2, _, err := websocket.Dial(ctx, wsURL, opts)
	if err != nil {
		t.Fatalf("conn2 dial failed: %v", err)
	}
	defer conn2.Close(websocket.StatusNormalClosure, "")

	hello2 := hello
	hello2.Host = "test-mac-2"
	helloData2, _ := json.Marshal(hello2)
	if err := conn2.Write(ctx, websocket.MessageText, helloData2); err != nil {
		t.Fatalf("conn2 write hello: %v", err)
	}

	// conn1 should receive close status 4000
	_, _, err = conn1.Read(ctx)
	if err == nil {
		t.Fatalf("expected conn1 to be closed after conn2 connected")
	}
	if status := websocket.CloseStatus(err); status != 4000 {
		t.Errorf("expected close status 4000, got %v", status)
	}

	// 3. Disconnect conn2; state should revert to HostOnline = false
	conn2.Close(websocket.StatusNormalClosure, "bye")
	for i := 0; i < 50; i++ {
		if !state.HostOnline() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if state.HostOnline() {
		t.Fatalf("expected hostOnline to be false after disconnect")
	}
}

func TestHub_CommandRoundTrip(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	defer store.Close()
	state := NewState()
	auth := NewAuthManager("valid-host-token", store, ClientIPPolicy{})
	hub := NewHub(auth, state, store, nil)

	s := httptest.NewServer(http.HandlerFunc(hub.ServeHost))
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := strings.Replace(s.URL, "http://", "ws://", 1)
	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer valid-host-token"}},
	}

	conn, _, err := websocket.Dial(ctx, wsURL, opts)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	hello := model.HelloMsg{
		Type:        model.WireHello,
		Host:        "test-host",
		HerdrOnline: true,
	}
	helloData, _ := json.Marshal(hello)
	if err := conn.Write(ctx, websocket.MessageText, helloData); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	// Wait for host connection to be ready
	for i := 0; i < 50; i++ {
		if state.HostOnline() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Host loop to reply to commands
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
				res := model.CommandResultMsg{
					Type:      model.WireCommandResult,
					RequestID: cmd.RequestID,
					OK:        true,
				}
				resData, _ := json.Marshal(res)
				_ = conn.Write(ctx, websocket.MessageText, resData)
			}
		}
	}()

	cmd := model.CommandMsg{
		Action:      "answer",
		PaneID:      "w1:p1",
		ExpectedSeq: 42,
		OptionID:    "opt-1",
	}

	res, err := hub.Command(ctx, cmd)
	if err != nil {
		t.Fatalf("hub.Command err: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected command result OK=true, got %+v", res)
	}
}
