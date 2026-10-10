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
	// A revoked host's connection closes with a policy violation; its next
	// handshake gets 401.
	wsCloseCodeRevoked = websocket.StatusPolicyViolation

	helloTimeout = 5 * time.Second
	// commandTimeout sits between the bridge's 6 s command budget and the
	// watch's 8 s call timeout: inner layers give up first, so a timeout
	// reported to the watch means the bridge has already stopped.
	commandTimeout = 7 * time.Second
	pingInterval   = 30 * time.Second
	pingTimeout    = 10 * time.Second
)

// RelayVersionHeader carries the relay's version in the /v1/host WebSocket
// handshake response, sent only once the host token is verified. The bridge
// (pkg/relayclient) reads the same name; contracts.md §3.
const RelayVersionHeader = "X-Agent-Watch-Relay-Version"

// Notifier receives agent update notifications for push dispatching. Agents
// and history items carry their host (stamped by the hub).
type Notifier interface {
	OnAgentUpdate(prev *model.AgentState, cur model.AgentState)
	// OnHistoryItem reports a pane's new reply (never a resent duplicate), so
	// a "finished" push can show it.
	OnHistoryItem(item model.HistoryItem)
	// OnHostPresence reports a host's input idle time and screen lock
	// (host_presence, contracts.md §3): pushes wait while the owner is there.
	OnHostPresence(host string, idle time.Duration, locked bool)
	// OnHostOffline reports that a host disconnected, was dropped or was
	// revoked (not replaced by a new connection of the same host).
	OnHostOffline(host string)
}

// NoopNotifier is a placeholder notifier that does nothing.
type NoopNotifier struct{}

// OnAgentUpdate is a no-op implementation.
func (NoopNotifier) OnAgentUpdate(prev *model.AgentState, cur model.AgentState) {}

// OnHistoryItem is a no-op implementation.
func (NoopNotifier) OnHistoryItem(item model.HistoryItem) {}

// OnHostPresence is a no-op implementation.
func (NoopNotifier) OnHostPresence(host string, idle time.Duration, locked bool) {}

// OnHostOffline is a no-op implementation.
func (NoopNotifier) OnHostOffline(host string) {}

// maxPresenceIdle caps a reported idle time: anything longer is just "away",
// and the cap keeps the conversion to time.Duration from overflowing.
const maxPresenceIdle = 365 * 24 * time.Hour

// hostConnection is one bridge WebSocket of the host id. gone is closed
// exactly once, as soon as the connection stops being its host's current one
// (replaced, disconnected, dropped for missing pings, or revoked); commands
// waiting on it fail at that moment.
type hostConnection struct {
	id       string
	conn     *websocket.Conn
	gone     chan struct{}
	goneOnce sync.Once
	// applyMu is held while a message from this connection changes the
	// relay's state; see apply.
	applyMu sync.Mutex
}

func newHostConnection(id string, conn *websocket.Conn) *hostConnection {
	return &hostConnection{id: id, conn: conn, gone: make(chan struct{})}
}

// apply runs fn unless the connection is gone, and reports whether it ran.
// Whoever marks a connection gone and then takes applyMu once knows that no
// message of it changes the state afterwards (a revoked host must not come
// back through a message it sent just before).
func (c *hostConnection) apply(fn func()) bool {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	if c.isGone() {
		return false
	}
	fn()
	return true
}

// retire marks the connection gone and waits for a message being applied.
func (c *hostConnection) retire() {
	c.markGone()
	c.applyMu.Lock()
	// An empty critical section on purpose: it only waits for apply.
	c.applyMu.Unlock()
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

// pendingCommand is a command waiting for its result from host.
type pendingCommand struct {
	ch   chan model.CommandResultMsg
	host *hostConnection
}

// Hub coordinates the hosts' WebSocket connections, one per host id, and
// routes each command to its agent's host.
type Hub struct {
	auth     *AuthManager
	state    *State
	store    *Store
	notifier Notifier

	mu             sync.Mutex
	conns          map[string]*hostConnection // host id -> its current connection
	pending        map[string]pendingCommand
	helloTimeout   time.Duration
	commandTimeout time.Duration
	pingInterval   time.Duration
	pingTimeout    time.Duration
	afterPing      func(err error) // test hook, see setAfterPing
	version        string          // sent to authenticated hosts (RelayVersionHeader)

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
		conns:          make(map[string]*hostConnection),
		pending:        make(map[string]pendingCommand),
		helloTimeout:   helloTimeout,
		commandTimeout: commandTimeout,
		pingInterval:   pingInterval,
		pingTimeout:    pingTimeout,
	}
}

// SetCommandTimeout overrides the 7-second budget for a command round trip
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

// SetVersion sets the version sent to authenticated hosts in the handshake
// response (RelayVersionHeader). Empty: no header.
func (h *Hub) SetVersion(v string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.version = v
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
	ident, ok := h.auth.VerifyHostToken(token)
	if !ok {
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
	version := h.version
	h.mu.Unlock()
	defer h.handlers.Done()

	// Only here, past the host-token check: the version is for the bridge,
	// not for anyone probing the relay.
	if version != "" {
		w.Header().Set(RelayVersionHeader, version)
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		slog.Error("accept host websocket", "err", err)
		return
	}
	conn.SetReadLimit(1 << 20)

	host := newHostConnection(ident.ID, conn)

	// One connection per host: a new one with the same host's token replaces
	// the old one. Other hosts are not affected.
	h.mu.Lock()
	// Checked again under mu: a revocation removes the token from the store
	// and then drops the host's connection under mu, so a handshake that
	// raced with it is refused here or dropped there.
	if again, ok := h.auth.VerifyHostToken(token); !ok || again.ID != ident.ID {
		h.mu.Unlock()
		_ = conn.Close(wsCloseCodeRevoked, "host revoked")
		return
	}
	oldHost := h.conns[ident.ID]
	h.conns[ident.ID] = host
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
		wasCurrent := h.conns[ident.ID] == host
		if wasCurrent {
			delete(h.conns, ident.ID)
			// Under mu: a new connection of this host registers only after
			// this, so its online flag is never overwritten by this one.
			h.state.SetHost(ident.ID, false, false)
		}
		h.mu.Unlock()
		if wasCurrent {
			h.notifier.OnHostOffline(ident.ID)
			if h.store != nil {
				h.store.TouchHost(ident.ID)
			}
		}
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

	if !host.apply(func() {
		h.state.AddHost(ident.ID, ident.Name)
		h.state.SetHost(ident.ID, true, hello.HerdrOnline)
	}) {
		return // replaced or revoked while saying hello
	}
	slog.Info("host connected", "host", ident.ID, "hello_host", hello.Host, "version", hello.Version, "herdr_online", hello.HerdrOnline)
	if h.store != nil {
		h.store.TouchHost(ident.ID)
	}

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

		if !host.apply(func() { h.handleWireMessage(host, wireMsg) }) {
			break
		}
	}
}

// RevokeHost drops a revoked host's connection, if any: its in-flight
// commands fail with host_offline and nothing it sent after this point
// changes the state. It reports whether the host was connected.
func (h *Hub) RevokeHost(id string) bool {
	h.mu.Lock()
	host := h.conns[id]
	delete(h.conns, id)
	h.mu.Unlock()

	if host == nil {
		return false
	}
	host.retire()
	go func() { _ = host.conn.Close(wsCloseCodeRevoked, "host revoked") }()
	h.notifier.OnHostOffline(id)
	return true
}

// Shutdown stops accepting hosts, drops the connected ones (watches see them go
// offline and their in-flight commands fail with host_offline) and waits until
// every host handler has returned, or ctx ends.
func (h *Hub) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	h.closed = true
	hosts := make([]*hostConnection, 0, len(h.conns))
	for _, host := range h.conns {
		hosts = append(hosts, host)
	}
	h.mu.Unlock()

	for _, host := range hosts {
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

// handleWireMessage applies one message from host. The relay stamps the
// host's id on everything it sends; the bridge's own host fields are ignored.
func (h *Hub) handleWireMessage(host *hostConnection, msg any) {
	id := host.id
	switch m := msg.(type) {
	case model.SnapshotMsg:
		h.state.ReplaceAll(id, m.Agents)
	case model.AgentUpdateMsg:
		m.Agent.Host = id
		prev := h.state.Upsert(m.Agent)
		h.notifier.OnAgentUpdate(prev, m.Agent)
	case model.AgentRemovedMsg:
		h.state.Remove(id, m.PaneID)
	case model.HistoryItemMsg:
		m.Item.Host = id
		// Duplicates (the bridge resends after a reconnect) are not news.
		if h.store.AddHistory(m.Item) {
			h.state.BroadcastHistory(m.Item)
			h.notifier.OnHistoryItem(m.Item)
		}
	case model.HerdrStatusMsg:
		h.state.SetHost(id, true, m.HerdrOnline)
	case model.HostPresenceMsg:
		idle := maxPresenceIdle
		if m.IdleSeconds < uint64(maxPresenceIdle/time.Second) {
			idle = time.Duration(m.IdleSeconds) * time.Second
		}
		h.notifier.OnHostPresence(id, idle, m.Locked)
	case model.CommandResultMsg:
		// Only the connection the command went to may answer it.
		h.mu.Lock()
		p, ok := h.pending[m.RequestID]
		if ok && p.host == host {
			delete(h.pending, m.RequestID)
		} else {
			ok = false
		}
		h.mu.Unlock()

		if ok {
			p.ch <- m
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

// Command sends a command to the host hostID and waits for its result.
// Writing the command and waiting for the answer share one budget (7 s by
// default).
//
// It returns host_offline when that host is not connected or goes away before
// answering (at once, not after the budget), and timeout when the budget runs
// out. A non-nil error means ctx ended first (the caller left).
func (h *Hub) Command(ctx context.Context, hostID string, cmd model.CommandMsg) (model.CommandResultMsg, error) {
	h.mu.Lock()
	host := h.conns[hostID]
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
	h.pending[reqID] = pendingCommand{ch: ch, host: host}
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
