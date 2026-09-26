package relay

import (
	"context"
	"encoding/json"
	"fmt"
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

	if status := websocket.CloseStatus(err); status != 4001 {
		t.Fatalf("got close err: %v (status %d), want close status 4001", err, status)
	}
}

func TestHub_ConnectAndReplace(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	defer store.Close()
	state := NewState()
	auth := NewAuthManager("valid-host-token", store, ClientIPPolicy{})
	hub := NewHub(auth, state, store, nil)

	s := httptest.NewServer(http.HandlerFunc(hub.ServeHost))
	defer s.Close()
	sub, events := state.Subscribe()
	defer state.Unsubscribe(sub)

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

	waitHostOnline(t, events, true, 5*time.Second)

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
	waitHostOnline(t, events, false, 5*time.Second)
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
	sub, events := state.Subscribe()
	defer state.Unsubscribe(sub)

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
	waitHostOnline(t, events, true, 5*time.Second)

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

// drainEvents returns the names of the events already queued on ch.
func drainEvents(ch <-chan sseEvent) []string {
	var names []string
	for {
		select {
		case ev := <-ch:
			names = append(names, ev.Name)
		default:
			return names
		}
	}
}

// Watches get a history event only for items the relay actually stored: a
// resent duplicate, or an item older than everything kept, is not news.
func TestHub_HistoryBroadcastOnlyWhenStored(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()
	state := NewState()
	hub := NewHub(NewAuthManager("valid-host-token", store, ClientIPPolicy{}), state, store, nil)
	sub, ch := state.Subscribe()
	defer state.Unsubscribe(sub)

	// Relative to now: items older than 7 days are pruned on every add.
	base := time.Now().UTC().Add(-time.Hour)
	at := func(d time.Duration) string { return base.Add(d).Format(time.RFC3339) }

	item := model.HistoryItem{ID: "h-1", PaneID: "w1:p1", CompletedAt: at(0)}
	hub.handleWireMessage(model.HistoryItemMsg{Type: model.WireHistoryItem, Item: item})
	hub.handleWireMessage(model.HistoryItemMsg{Type: model.WireHistoryItem, Item: item}) // bridge resend
	if got := drainEvents(ch); len(got) != 1 || got[0] != "history" {
		t.Fatalf("events after an item and its duplicate = %v, want one history", got)
	}

	// Fill the 200-item cap with newer items, then send an older one: it is
	// trimmed on arrival, so nobody should be told about it.
	for i := 0; i < maxHistoryTotal; i++ {
		store.AddHistory(model.HistoryItem{
			ID:          fmt.Sprintf("new-%d", i),
			PaneID:      fmt.Sprintf("pane-%d", i/maxHistoryPane),
			CompletedAt: at(time.Duration(i+1) * time.Second),
		})
	}
	old := model.HistoryItem{ID: "too-old", PaneID: "w9:p9", CompletedAt: at(-time.Hour)}
	hub.handleWireMessage(model.HistoryItemMsg{Type: model.WireHistoryItem, Item: old})
	if got := drainEvents(ch); len(got) != 0 {
		t.Fatalf("events after an item trimmed on arrival = %v, want none", got)
	}
	if items := store.GetHistory("w9:p9", 10); len(items) != 0 {
		t.Fatalf("trimmed item is stored: %v", items)
	}
}

// historyNotifier records the replies the hub reports.
type historyNotifier struct {
	NoopNotifier
	items []string
}

func (n *historyNotifier) OnHistoryItem(item model.HistoryItem) { n.items = append(n.items, item.ID) }

// The notifier hears about each new reply once: a "finished" push waits for it.
// A resent duplicate is not news.
func TestHub_NotifierGetsNewRepliesOnce(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()
	notifier := &historyNotifier{}
	hub := NewHub(NewAuthManager("valid-host-token", store, ClientIPPolicy{}), NewState(), store, notifier)

	item := model.HistoryItem{ID: "h-1", PaneID: "w1:p1", CompletedAt: time.Now().UTC().Format(time.RFC3339)}
	hub.handleWireMessage(model.HistoryItemMsg{Type: model.WireHistoryItem, Item: item})
	hub.handleWireMessage(model.HistoryItemMsg{Type: model.WireHistoryItem, Item: item}) // bridge resend
	if fmt.Sprint(notifier.items) != "[h-1]" {
		t.Fatalf("notified = %v, want [h-1]", notifier.items)
	}
}

// hubHarness is a Hub served over httptest, for host-lifecycle tests.
type hubHarness struct {
	state *State
	store *Store
	hub   *Hub
	wsURL string
}

func newHubHarness(t *testing.T) *hubHarness {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	state := NewState()
	auth := NewAuthManager("valid-host-token", store, ClientIPPolicy{})
	hub := NewHub(auth, state, store, nil)
	s := httptest.NewServer(http.HandlerFunc(hub.ServeHost))
	t.Cleanup(s.Close)
	return &hubHarness{state: state, store: store, hub: hub, wsURL: strings.Replace(s.URL, "http://", "ws://", 1)}
}

// connectHost dials the hub as a bridge and sends hello. The connection is
// closed at the end of the test.
func (hh *hubHarness) connectHost(t *testing.T, ctx context.Context, name string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, hh.wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer valid-host-token"}},
	})
	if err != nil {
		t.Fatalf("dial %s: %v", name, err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	hello, _ := json.Marshal(model.HelloMsg{Type: model.WireHello, Host: name, HerdrOnline: true})
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		t.Fatalf("%s hello: %v", name, err)
	}
	return conn
}

// waitEvent returns the next event named name from ch, skipping others.
func waitEvent(t *testing.T, ch <-chan sseEvent, name string, within time.Duration) sseEvent {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("subscriber closed while waiting for %q", name)
			}
			if ev.Name == name {
				return ev
			}
		case <-deadline:
			t.Fatalf("no %q event within %v", name, within)
		}
	}
}

// waitHostOnline waits for a "host" event with host_online == want.
func waitHostOnline(t *testing.T, ch <-chan sseEvent, want bool, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		ev := waitEvent(t, ch, "host", time.Until(deadline))
		var payload struct {
			HostOnline bool `json:"host_online"`
		}
		if err := json.Unmarshal(ev.Data, &payload); err != nil {
			t.Fatalf("decode host event: %v", err)
		}
		if payload.HostOnline == want {
			return
		}
	}
}

// readCommands forwards every command the fake host receives to the returned
// channel and never answers. It stops when the connection fails.
func readCommands(ctx context.Context, conn *websocket.Conn) <-chan model.CommandMsg {
	out := make(chan model.CommandMsg, 64)
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if msg, err := model.DecodeWire(data); err == nil {
				if cmd, ok := msg.(model.CommandMsg); ok {
					out <- cmd
				}
			}
		}
	}()
	return out
}

type commandOutcome struct {
	res model.CommandResultMsg
	err error
}

func startCommand(ctx context.Context, hub *Hub) <-chan commandOutcome {
	out := make(chan commandOutcome, 1)
	go func() {
		res, err := hub.Command(ctx, model.CommandMsg{Action: "cancel", PaneID: "w1:p1", ExpectedSeq: 1})
		out <- commandOutcome{res, err}
	}()
	return out
}

// A host that stops answering pings is dropped and watches see it go offline.
func TestHub_PingFailureDropsHost(t *testing.T) {
	hh := newHubHarness(t)
	hh.hub.SetPingInterval(20*time.Millisecond, 50*time.Millisecond)
	sub, ch := hh.state.Subscribe()
	defer hh.state.Unsubscribe(sub)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Never reads: pongs are only sent from inside Read, so pings go unanswered.
	hh.connectHost(t, ctx, "frozen-mac")

	waitHostOnline(t, ch, true, 5*time.Second)
	waitHostOnline(t, ch, false, 5*time.Second)

	res, err := hh.hub.Command(ctx, model.CommandMsg{Action: "cancel", PaneID: "w1:p1"})
	if err != nil || res.ErrorCode != string(model.ErrHostOffline) {
		t.Fatalf("command after the host was dropped = %+v, %v; want host_offline", res, err)
	}
}

// A host that answers pings stays online across many ping rounds.
func TestHub_PingKeepsResponsiveHost(t *testing.T) {
	hh := newHubHarness(t)
	hh.hub.SetPingInterval(10*time.Millisecond, 5*time.Second)
	pings := make(chan error, 64)
	hh.hub.setAfterPing(func(err error) {
		select {
		case pings <- err:
		default:
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn := hh.connectHost(t, ctx, "live-mac")
	readCommands(ctx, conn) // keeps reading, which answers pings

	for i := 0; i < 5; i++ {
		select {
		case err := <-pings:
			if err != nil {
				t.Fatalf("ping %d failed: %v", i+1, err)
			}
		case <-ctx.Done():
			t.Fatalf("only %d pings happened", i)
		}
	}
	if !hh.state.HostOnline() {
		t.Fatalf("responsive host went offline")
	}
}

// Commands in flight to a host that gets replaced fail at once with
// host_offline instead of waiting out the command budget.
func TestHub_ReplacedHostFailsInFlightCommandImmediately(t *testing.T) {
	hh := newHubHarness(t)
	hh.hub.SetCommandTimeout(time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sub, ch := hh.state.Subscribe()
	defer hh.state.Unsubscribe(sub)
	connA := hh.connectHost(t, ctx, "mac-a")
	cmdsA := readCommands(ctx, connA)
	waitHostOnline(t, ch, true, 5*time.Second)

	outcome := startCommand(ctx, hh.hub)
	select {
	case <-cmdsA:
	case <-ctx.Done():
		t.Fatalf("host A never received the command")
	}

	hh.connectHost(t, ctx, "mac-b")

	select {
	case o := <-outcome:
		if o.err != nil || o.res.OK || o.res.ErrorCode != string(model.ErrHostOffline) {
			t.Fatalf("command = %+v, %v; want host_offline", o.res, o.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("command to the replaced host is still waiting")
	}
}

// Commands in flight to a host that disconnects fail at once with host_offline.
func TestHub_DisconnectFailsInFlightCommandImmediately(t *testing.T) {
	hh := newHubHarness(t)
	hh.hub.SetCommandTimeout(time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sub, ch := hh.state.Subscribe()
	defer hh.state.Unsubscribe(sub)
	connA := hh.connectHost(t, ctx, "mac-a")
	cmdsA := readCommands(ctx, connA)
	waitHostOnline(t, ch, true, 5*time.Second)

	outcome := startCommand(ctx, hh.hub)
	select {
	case <-cmdsA:
	case <-ctx.Done():
		t.Fatalf("host never received the command")
	}
	_ = connA.CloseNow()

	select {
	case o := <-outcome:
		if o.err != nil || o.res.OK || o.res.ErrorCode != string(model.ErrHostOffline) {
			t.Fatalf("command = %+v, %v; want host_offline", o.res, o.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("command to the disconnected host is still waiting")
	}
	waitHostOnline(t, ch, false, 5*time.Second)
}

// Replacing a host that does not answer the close handshake must not delay
// the new host: its hello and snapshot are handled at once.
func TestHub_ReplacedHostClosedAsynchronously(t *testing.T) {
	hh := newHubHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sub, ch := hh.state.Subscribe()
	defer hh.state.Unsubscribe(sub)

	hh.connectHost(t, ctx, "frozen-mac") // never reads, so never answers the close frame
	waitHostOnline(t, ch, true, 5*time.Second)

	connB := hh.connectHost(t, ctx, "new-mac")
	snap, _ := json.Marshal(model.SnapshotMsg{
		Type:   model.WireSnapshot,
		Agents: []model.AgentState{{PaneID: "b:1", Status: model.StatusIdle}},
	})
	if err := connB.Write(ctx, websocket.MessageText, snap); err != nil {
		t.Fatalf("new host snapshot: %v", err)
	}
	// A synchronous close handshake alone waits 5 s for the frozen peer.
	waitEvent(t, ch, "snapshot", 2*time.Second)
	if !hh.state.HasPane("b:1") {
		t.Fatalf("new host's snapshot not applied")
	}
}

// A watch that gives up on a command (its request context is cancelled) must
// never tear down the host WebSocket.
func TestHub_CancelledCallerDoesNotKillHost(t *testing.T) {
	hh := newHubHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sub, ch := hh.state.Subscribe()
	defer hh.state.Unsubscribe(sub)

	conn := hh.connectHost(t, ctx, "mac")
	waitHostOnline(t, ch, true, 5*time.Second)
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if msg, err := model.DecodeWire(data); err == nil {
				if cmd, ok := msg.(model.CommandMsg); ok {
					res, _ := json.Marshal(model.CommandResultMsg{Type: model.WireCommandResult, RequestID: cmd.RequestID, OK: true})
					_ = conn.Write(ctx, websocket.MessageText, res)
				}
			}
		}
	}()

	gone, goneCancel := context.WithCancel(context.Background())
	goneCancel()
	for i := 0; i < 20; i++ {
		if _, err := hh.hub.Command(gone, model.CommandMsg{Action: "cancel", PaneID: "w1:p1"}); err == nil {
			t.Fatalf("command with a cancelled context returned no error")
		}
	}

	res, err := hh.hub.Command(ctx, model.CommandMsg{Action: "cancel", PaneID: "w1:p1"})
	if err != nil || !res.OK {
		t.Fatalf("command after cancelled callers = %+v, %v; want ok (host connection intact)", res, err)
	}
	if !hh.state.HostOnline() {
		t.Fatalf("host went offline after cancelled callers")
	}
}
