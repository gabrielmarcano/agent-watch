package relay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

const (
	wsCloseCodeReplaced     websocket.StatusCode = 4000
	wsCloseCodeHelloTimeout websocket.StatusCode = 4001

	helloTimeout   = 5 * time.Second
	commandTimeout = 10 * time.Second
	pingInterval   = 30 * time.Second
	pingTimeout    = 10 * time.Second
)

// Notifier receives agent update notifications for push dispatching.
type Notifier interface {
	OnAgentUpdate(prev *model.AgentState, cur model.AgentState)
}

// NoopNotifier is a placeholder notifier that does nothing.
type NoopNotifier struct{}

// OnAgentUpdate is a no-op implementation.
func (NoopNotifier) OnAgentUpdate(prev *model.AgentState, cur model.AgentState) {}

// hostConnection is one bridge WebSocket. gone is closed exactly once, as soon
// as the connection stops being the current host (replaced, disconnected or
// dropped for missing pings); commands waiting on it fail at that moment.
type hostConnection struct {
	conn     *websocket.Conn
	gone     chan struct{}
	goneOnce sync.Once
}

func newHostConnection(conn *websocket.Conn) *hostConnection {
	return &hostConnection{conn: conn, gone: make(chan struct{})}
}

func (c *hostConnection) markGone() {
	c.goneOnce.Do(func() { close(c.gone) })
}

func (c *hostConnection) isGone() bool {
	select {
	case <-c.gone:
		return true
	default:
		return false
	}
}

// Hub coordinates the WebSocket connection from the bridge host and command routing.
type Hub struct {
	auth     *AuthManager
	state    *State
	store    *Store
	notifier Notifier

	mu             sync.Mutex
	currentHost    *hostConnection
	pending        map[string]chan model.CommandResultMsg
	helloTimeout   time.Duration
	commandTimeout time.Duration
	pingInterval   time.Duration
	pingTimeout    time.Duration
	afterPing      func(err error) // test hook, see setAfterPing

	closed   bool           // set by Shutdown: no new hosts
	handlers sync.WaitGroup // running ServeHost calls; Add only under mu while !closed
}

// NewHub initializes a new host Hub.
func NewHub(auth *AuthManager, state *State, store *Store, notifier Notifier) *Hub {
	if notifier == nil {
		notifier = NoopNotifier{}
	}
	return &Hub{
		auth:           auth,
		state:          state,
		store:          store,
		notifier:       notifier,
		pending:        make(map[string]chan model.CommandResultMsg),
		helloTimeout:   helloTimeout,
		commandTimeout: commandTimeout,
		pingInterval:   pingInterval,
		pingTimeout:    pingTimeout,
	}
}

// SetCommandTimeout overrides the 10-second budget for a command round trip
// (useful for tests).
func (h *Hub) SetCommandTimeout(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.commandTimeout = d
}

// SetPingInterval overrides how often the host is pinged (30 s) and how long
// a pong may take (10 s) (useful for tests).
func (h *Hub) SetPingInterval(interval, timeout time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pingInterval = interval
	h.pingTimeout = timeout
}

// SetHelloTimeout overrides the default 5-second hello handshake timeout (useful for tests).
func (h *Hub) SetHelloTimeout(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.helloTimeout = d
}

// setAfterPing installs a hook called after every host ping (tests only).
func (h *Hub) setAfterPing(fn func(err error)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.afterPing = fn
}

// ServeHost handles GET /v1/host WebSocket connections.
func (h *Hub) ServeHost(w http.ResponseWriter, r *http.Request) {
	token := ExtractBearerToken(r)
	if !h.auth.VerifyHostToken(token) {
		writeError(w, model.ErrUnauthorized, "invalid or missing host token")
		return
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		http.Error(w, "relay shutting down", http.StatusServiceUnavailable)
		return
	}
	h.handlers.Add(1)
	h.mu.Unlock()
	defer h.handlers.Done()

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		slog.Error("accept host websocket", "err", err)
		return
	}
	conn.SetReadLimit(1 << 20)

	host := newHostConnection(conn)

	// Enforce 1 active host connection.
	h.mu.Lock()
	oldHost := h.currentHost
	h.currentHost = host
	helloWait, pingEvery, pingWait, afterPing := h.helloTimeout, h.pingInterval, h.pingTimeout, h.afterPing
	h.mu.Unlock()

	if oldHost != nil {
		// Its in-flight commands fail now. The close handshake runs in the
		// background: a peer that never answers it would otherwise hold up
		// this host's hello and snapshot for seconds.
		oldHost.markGone()
		go func() { _ = oldHost.conn.Close(wsCloseCodeReplaced, "replaced") }()
	}

	defer func() {
		host.markGone()
		h.mu.Lock()
		if h.currentHost == host {
			h.currentHost = nil
			h.state.SetHost(false, false)
		}
		h.mu.Unlock()
		// Only now: closing a dead peer can take seconds, and watches must
		// see the host go offline at once.
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()

	if helloWait <= 0 {
		helloWait = helloTimeout
	}
	// Close from a timer rather than with a read deadline: an expiring read
	// context makes the websocket library drop the TCP connection at once,
	// and the bridge would never see the 4001 close frame.
	helloTimer := time.AfterFunc(helloWait, func() {
		_ = conn.Close(wsCloseCodeHelloTimeout, "hello timeout")
	})
	_, helloData, err := conn.Read(r.Context())
	if !helloTimer.Stop() || err != nil {
		return // timed out (the timer is closing with 4001) or the read failed
	}

	decodedHello, err := model.DecodeWire(helloData)
	if err != nil {
		_ = conn.Close(wsCloseCodeHelloTimeout, "invalid hello payload")
		return
	}

	hello, ok := decodedHello.(model.HelloMsg)
	if !ok {
		_ = conn.Close(wsCloseCodeHelloTimeout, "expected hello message")
		return
	}

	if host.isGone() {
		return // replaced while saying hello
	}
	slog.Info("host connected", "host", hello.Host, "version", hello.Version, "herdr_online", hello.HerdrOnline)
	h.state.SetHost(true, hello.HerdrOnline)

	pingCtx, stopPing := context.WithCancel(r.Context())
	defer stopPing()
	go pingHost(pingCtx, host, pingEvery, pingWait, afterPing)

	// Message read loop
	for {
		_, data, err := conn.Read(r.Context())
		if err != nil || host.isGone() {
			// A replaced host may still be flushing messages: ignore them.
			break
		}

		wireMsg, err := model.DecodeWire(data)
		if err != nil {
			if errors.Is(err, model.ErrUnknownType) {
				// Ignore unknown types for forward compatibility
				continue
			}
			slog.Warn("decode wire message", "err", err)
			continue
		}

		h.handleWireMessage(wireMsg)
	}
}

// Shutdown stops accepting hosts, drops the current one (watches see it go
// offline and its in-flight commands fail with host_offline) and waits until
// every host handler has returned, or ctx ends.
func (h *Hub) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	h.closed = true
	host := h.currentHost
	h.mu.Unlock()

	if host != nil {
		_ = host.conn.CloseNow()
	}
	done := make(chan struct{})
	go func() {
		h.handlers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// pingHost pings the host every interval. A ping without a pong within
// timeout means the host is gone (a dead peer or a half-open TCP connection
// never errors on its own): the connection is closed, which ends ServeHost's
// read loop and broadcasts host_online=false.
func pingHost(ctx context.Context, host *hostConnection, interval, timeout time.Duration, afterPing func(error)) {
	if interval <= 0 {
		interval = pingInterval
	}
	if timeout <= 0 {
		timeout = pingTimeout
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-host.gone:
			return
		case <-ticker.C:
		}

		pctx, cancel := context.WithTimeout(ctx, timeout)
		err := host.conn.Ping(pctx)
		cancel()
		if afterPing != nil {
			afterPing(err)
		}
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("host did not answer ping, dropping it", "timeout", timeout, "err", err)
			}
			_ = host.conn.CloseNow()
			return
		}
	}
}

func (h *Hub) handleWireMessage(msg any) {
	switch m := msg.(type) {
	case model.SnapshotMsg:
		h.state.ReplaceAll(m.Agents)
	case model.AgentUpdateMsg:
		prev := h.state.Upsert(m.Agent)
		h.notifier.OnAgentUpdate(prev, m.Agent)
	case model.AgentRemovedMsg:
		h.state.Remove(m.PaneID)
	case model.HistoryItemMsg:
		// Duplicates (the bridge resends after a reconnect) are not news.
		if h.store.AddHistory(m.Item) {
			h.state.BroadcastHistory(m.Item)
		}
	case model.HerdrStatusMsg:
		h.state.SetHost(true, m.HerdrOnline)
	case model.CommandResultMsg:
		h.mu.Lock()
		ch, ok := h.pending[m.RequestID]
		if ok {
			delete(h.pending, m.RequestID)
		}
		h.mu.Unlock()

		if ok {
			ch <- m
		} else {
			slog.Debug("dropped late or unknown command result", "request_id", m.RequestID)
		}
	default:
		// Ignore any unhandled message type
	}
}

func commandFailure(reqID string, code model.ErrorCode, msg string) model.CommandResultMsg {
	return model.CommandResultMsg{
		Type:      model.WireCommandResult,
		RequestID: reqID,
		OK:        false,
		ErrorCode: string(code),
		Message:   msg,
	}
}

// Command sends a command to the host and waits for its result. Writing the
// command and waiting for the answer share one budget (10 s by default).
//
// It returns host_offline when no host is connected or the host goes away
// before answering (at once, not after the budget), and timeout when the
// budget runs out. A non-nil error means ctx ended first (the caller left).
func (h *Hub) Command(ctx context.Context, cmd model.CommandMsg) (model.CommandResultMsg, error) {
	h.mu.Lock()
	host := h.currentHost
	budget := h.commandTimeout
	if host == nil {
		h.mu.Unlock()
		return commandFailure("", model.ErrHostOffline, "host offline"), nil
	}

	var reqIDBytes [8]byte
	if _, err := rand.Read(reqIDBytes[:]); err != nil {
		h.mu.Unlock()
		return model.CommandResultMsg{}, fmt.Errorf("generate request id: %w", err)
	}
	reqID := hex.EncodeToString(reqIDBytes[:])
	cmd.Type = model.WireCommand
	cmd.RequestID = reqID

	ch := make(chan model.CommandResultMsg, 1)
	h.pending[reqID] = ch
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.pending, reqID)
		h.mu.Unlock()
	}()

	data, err := json.Marshal(cmd)
	if err != nil {
		return model.CommandResultMsg{}, fmt.Errorf("marshal command: %w", err)
	}

	deadline := time.Now().Add(budget)
	timedOut := commandFailure(reqID, model.ErrTimeout, fmt.Sprintf("bridge did not answer within %v", budget))

	// The write is bounded by the budget but not by the caller: the websocket
	// library closes the whole connection when a write's context ends
	// mid-write, and a watch hanging up must not take the host down with it.
	writeCtx, cancelWrite := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	err = host.conn.Write(writeCtx, websocket.MessageText, data)
	writeTimedOut := errors.Is(writeCtx.Err(), context.DeadlineExceeded)
	cancelWrite()
	if err != nil {
		if writeTimedOut {
			return timedOut, nil
		}
		return commandFailure(reqID, model.ErrHostOffline, "failed to send command to host"), nil
	}

	waitCtx, cancelWait := context.WithDeadline(ctx, deadline)
	defer cancelWait()

	select {
	case res := <-ch:
		return res, nil
	case <-host.gone:
		if res, ok := resultIfReady(ch); ok {
			return res, nil
		}
		return commandFailure(reqID, model.ErrHostOffline, "host disconnected before answering"), nil
	case <-waitCtx.Done():
		if res, ok := resultIfReady(ch); ok {
			return res, nil
		}
		if err := ctx.Err(); err != nil {
			return commandFailure(reqID, model.ErrTimeout, err.Error()), err
		}
		return timedOut, nil
	}
}

// resultIfReady returns a result that arrived at the same time as another
// select case fired, so a delivered answer always wins.
func resultIfReady(ch <-chan model.CommandResultMsg) (model.CommandResultMsg, bool) {
	select {
	case res := <-ch:
		return res, true
	default:
		return model.CommandResultMsg{}, false
	}
}
