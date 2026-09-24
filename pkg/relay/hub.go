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
)

// Notifier receives agent update notifications for push dispatching.
type Notifier interface {
	OnAgentUpdate(prev *model.AgentState, cur model.AgentState)
}

// NoopNotifier is a placeholder notifier that does nothing.
type NoopNotifier struct{}

// OnAgentUpdate is a no-op implementation.
func (NoopNotifier) OnAgentUpdate(prev *model.AgentState, cur model.AgentState) {}

type hostConnection struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
	done    chan struct{}
}

func (c *hostConnection) writeMessage(ctx context.Context, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.Write(ctx, websocket.MessageText, data)
}

// Hub coordinates the WebSocket connection from the bridge host and command routing.
type Hub struct {
	auth     *AuthManager
	state    *State
	store    *Store
	notifier Notifier

	mu           sync.Mutex
	currentHost  *hostConnection
	pending      map[string]chan model.CommandResultMsg
	helloTimeout time.Duration
}

// NewHub initializes a new host Hub.
func NewHub(auth *AuthManager, state *State, store *Store, notifier Notifier) *Hub {
	if notifier == nil {
		notifier = NoopNotifier{}
	}
	return &Hub{
		auth:         auth,
		state:        state,
		store:        store,
		notifier:     notifier,
		pending:      make(map[string]chan model.CommandResultMsg),
		helloTimeout: helloTimeout,
	}
}

// SetHelloTimeout overrides the default 5-second hello handshake timeout (useful for tests).
func (h *Hub) SetHelloTimeout(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.helloTimeout = d
}

// ServeHost handles GET /v1/host WebSocket connections.
func (h *Hub) ServeHost(w http.ResponseWriter, r *http.Request) {
	token := ExtractBearerToken(r)
	if !h.auth.VerifyHostToken(token) {
		writeError(w, model.ErrUnauthorized, "invalid or missing host token")
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		slog.Error("accept host websocket", "err", err)
		return
	}
	conn.SetReadLimit(1 << 20)

	hostConn := &hostConnection{
		conn: conn,
		done: make(chan struct{}),
	}

	// Enforce 1 active host connection: close old connection with 4000 replaced
	h.mu.Lock()
	oldHost := h.currentHost
	h.currentHost = hostConn
	h.mu.Unlock()

	if oldHost != nil {
		_ = oldHost.conn.Close(wsCloseCodeReplaced, "replaced")
	}

	defer func() {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		close(hostConn.done)

		h.mu.Lock()
		if h.currentHost == hostConn {
			h.currentHost = nil
			h.failAllPendingLocked("host disconnected")
			h.state.SetHost(false, false)
		}
		h.mu.Unlock()
	}()

	h.mu.Lock()
	timeout := h.helloTimeout
	h.mu.Unlock()
	if timeout <= 0 {
		timeout = helloTimeout
	}
	helloCtx, helloCancel := context.WithTimeout(r.Context(), timeout)
	_, helloData, err := conn.Read(helloCtx)
	helloCancel()
	if err != nil {
		_ = conn.Close(wsCloseCodeHelloTimeout, "hello timeout")
		return
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

	slog.Info("host connected", "host", hello.Host, "version", hello.Version, "herdr_online", hello.HerdrOnline)
	h.state.SetHost(true, hello.HerdrOnline)

	// Message read loop
	for {
		_, data, err := conn.Read(r.Context())
		if err != nil {
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
		h.store.AddHistory(m.Item)
		h.state.BroadcastHistory(m.Item)
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

// Command sends a command to the host and awaits the result with a 10-second timeout.
func (h *Hub) Command(ctx context.Context, cmd model.CommandMsg) (model.CommandResultMsg, error) {
	h.mu.Lock()
	host := h.currentHost
	if host == nil {
		h.mu.Unlock()
		return model.CommandResultMsg{
			Type:      model.WireCommandResult,
			OK:        false,
			ErrorCode: string(model.ErrHostOffline),
			Message:   "host offline",
		}, nil
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

	if err := host.writeMessage(ctx, data); err != nil {
		return model.CommandResultMsg{
			Type:      model.WireCommandResult,
			RequestID: reqID,
			OK:        false,
			ErrorCode: string(model.ErrHostOffline),
			Message:   "failed to send command to host",
		}, nil
	}

	timer := time.NewTimer(commandTimeout)
	defer timer.Stop()

	select {
	case res := <-ch:
		return res, nil
	case <-timer.C:
		return model.CommandResultMsg{
			Type:      model.WireCommandResult,
			RequestID: reqID,
			OK:        false,
			ErrorCode: string(model.ErrTimeout),
			Message:   "bridge did not answer within 10s",
		}, nil
	case <-ctx.Done():
		return model.CommandResultMsg{
			Type:      model.WireCommandResult,
			RequestID: reqID,
			OK:        false,
			ErrorCode: string(model.ErrTimeout),
			Message:   ctx.Err().Error(),
		}, ctx.Err()
	}
}

// failAllPendingLocked fails all currently pending commands with host_offline.
func (h *Hub) failAllPendingLocked(msg string) {
	for reqID, ch := range h.pending {
		ch <- model.CommandResultMsg{
			Type:      model.WireCommandResult,
			RequestID: reqID,
			OK:        false,
			ErrorCode: string(model.ErrHostOffline),
			Message:   msg,
		}
		delete(h.pending, reqID)
	}
}
