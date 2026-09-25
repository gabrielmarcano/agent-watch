package relayclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

const (
	defaultMinBackoff = 1 * time.Second
	defaultMaxBackoff = 60 * time.Second
	// healthyAfter is how long a connection must last, counted from the
	// successful dial, before the reconnect backoff resets.
	healthyAfter = 60 * time.Second
	// historyCap bounds the history queue kept while the relay is unreachable.
	historyCap = 50
	// sendBuffer is how many frames may wait for the writer of a live connection.
	sendBuffer = 100
)

// Client manages an outbound WebSocket connection from the bridge to the cloud relay.
type Client struct {
	URL        string                          // wss://relay.example.com/v1/host
	Token      string                          // host bearer token
	OnConnect  func(ctx context.Context) []any // messages to send first: hello + snapshot
	OnMessage  func(msg any)                   // decoded with model.DecodeWire; called sequentially
	Logger     *slog.Logger
	MinBackoff time.Duration // optional initial backoff (defaults to 1s)
	MaxBackoff time.Duration // optional backoff ceiling (defaults to 60s)

	mu        sync.Mutex
	connected bool
	sendCh    chan outMsg   // frames for the live connection's writer
	flushCh   chan struct{} // wakes the writer to flush historyQ (cap 1)
	historyQ  []model.HistoryItemMsg
	lastErr   string
}

// outMsg is one encoded frame plus what the logs and the history queue need.
type outMsg struct {
	data []byte
	typ  string
	id   string                // pane_id or request_id, for logs
	hist *model.HistoryItemMsg // set for history_item: re-queued if never written
}

// Connected reports whether the client currently has an active connection to the relay.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// LastError describes why the relay is not connected (a rejected token, a
// failed dial, a dropped connection). It is empty while connected and before
// the first attempt. It never contains the token.
func (c *Client) LastError() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

func (c *Client) setLastError(msg string) {
	c.mu.Lock()
	c.lastErr = msg
	c.mu.Unlock()
}

// Send enqueues msg for transmission to the relay without blocking.
//
//   - Connected: the frame goes to the writer. If its buffer is full, a
//     history_item joins the history queue and the writer is woken to flush
//     it; anything else is dropped and logged (the next snapshot covers
//     agent state; the relay times out an unanswered command).
//   - Disconnected: history_item joins the bounded history queue (oldest
//     dropped), flushed after the next hello + snapshot. agent_update,
//     agent_removed, herdr_status and command_result are dropped.
func (c *Client) Send(msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		if c.Logger != nil {
			c.Logger.Error("failed to encode wire message", "err", err)
		}
		return
	}
	om := describe(msg)
	om.data = data

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected && c.sendCh != nil {
		select {
		case c.sendCh <- om:
			return
		default:
		}
		if om.hist != nil {
			c.enqueueHistoryLocked(*om.hist)
			if c.flushCh != nil {
				select {
				case c.flushCh <- struct{}{}:
				default:
				}
			}
			return
		}
		if c.Logger != nil {
			c.Logger.Warn("relay send buffer full; dropping message",
				"type", om.typ, "id", om.id, "buffer", cap(c.sendCh))
		}
		return
	}

	if om.hist != nil {
		c.enqueueHistoryLocked(*om.hist)
	}
}

// describe extracts the wire type and a loggable id from a known message.
func describe(msg any) outMsg {
	switch m := msg.(type) {
	case model.AgentUpdateMsg:
		return outMsg{typ: model.WireAgentUpdate, id: m.Agent.PaneID}
	case *model.AgentUpdateMsg:
		return outMsg{typ: model.WireAgentUpdate, id: m.Agent.PaneID}
	case model.AgentRemovedMsg:
		return outMsg{typ: model.WireAgentRemoved, id: m.PaneID}
	case *model.AgentRemovedMsg:
		return outMsg{typ: model.WireAgentRemoved, id: m.PaneID}
	case model.CommandResultMsg:
		return outMsg{typ: model.WireCommandResult, id: m.RequestID}
	case *model.CommandResultMsg:
		return outMsg{typ: model.WireCommandResult, id: m.RequestID}
	case model.HistoryItemMsg:
		h := m
		return outMsg{typ: model.WireHistoryItem, id: m.Item.PaneID, hist: &h}
	case *model.HistoryItemMsg:
		h := *m
		return outMsg{typ: model.WireHistoryItem, id: m.Item.PaneID, hist: &h}
	case model.HerdrStatusMsg, *model.HerdrStatusMsg:
		return outMsg{typ: model.WireHerdrStatus}
	case model.HelloMsg, *model.HelloMsg:
		return outMsg{typ: model.WireHello}
	case model.SnapshotMsg, *model.SnapshotMsg:
		return outMsg{typ: model.WireSnapshot}
	default:
		return outMsg{typ: fmt.Sprintf("%T", msg)}
	}
}

// enqueueHistoryLocked appends h to the bounded history queue. Callers hold c.mu.
func (c *Client) enqueueHistoryLocked(h model.HistoryItemMsg) {
	if len(c.historyQ) >= historyCap {
		c.historyQ = c.historyQ[len(c.historyQ)-historyCap+1:]
	}
	c.historyQ = append(c.historyQ, h)
}

// requeueUnsentLocked moves the history frames still buffered in ch (never
// written to a connection that has now ended) to the front of the history
// queue, so the next connection sends them. Other frames are dropped, as they
// would be while disconnected. Callers hold c.mu.
func (c *Client) requeueUnsentLocked(ch chan outMsg) {
	var unsent []model.HistoryItemMsg
	for {
		select {
		case om := <-ch:
			if om.hist != nil {
				unsent = append(unsent, *om.hist)
			}
			continue
		default:
		}
		break
	}
	if len(unsent) == 0 {
		return
	}
	merged := append(unsent, c.historyQ...)
	if len(merged) > historyCap {
		merged = merged[len(merged)-historyCap:]
	}
	c.historyQ = merged
}

// popHistory removes and returns the oldest queued history item.
func (c *Client) popHistory() (model.HistoryItemMsg, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.historyQ) == 0 {
		return model.HistoryItemMsg{}, false
	}
	h := c.historyQ[0]
	c.historyQ = c.historyQ[1:]
	return h, true
}

// pushHistoryFront puts back an item whose write failed.
func (c *Client) pushHistoryFront(h model.HistoryItemMsg) {
	c.mu.Lock()
	defer c.mu.Unlock()
	merged := append([]model.HistoryItemMsg{h}, c.historyQ...)
	if len(merged) > historyCap {
		merged = merged[len(merged)-historyCap:]
	}
	c.historyQ = merged
}

// Run executes the reconnect loop until ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	initialBackoff := defaultMinBackoff
	if c.MinBackoff > 0 {
		initialBackoff = c.MinBackoff
	}
	maxBackoff := defaultMaxBackoff
	if c.MaxBackoff > 0 {
		maxBackoff = c.MaxBackoff
	}
	backoff := initialBackoff

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		connectedAt, err := c.connectAndServe(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// The healthy clock starts at the successful dial, so a slow dial
		// followed by a short-lived connection does not reset the backoff.
		if !connectedAt.IsZero() && time.Since(connectedAt) >= healthyAfter {
			backoff = initialBackoff
		}
		if errors.Is(err, errInvalidToken) {
			backoff = maxBackoff
		}

		jitterFactor := 0.8 + rand.Float64()*0.4 // [0.8, 1.2]
		sleepDuration := time.Duration(float64(backoff) * jitterFactor)

		if c.Logger != nil {
			c.Logger.Info("reconnecting to relay", "backoff", sleepDuration, "err", err)
		}

		timer := time.NewTimer(sleepDuration)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

var errInvalidToken = errors.New("invalid host token")

// connectAndServe dials the relay and serves one connection until it ends.
// connectedAt is when the dial succeeded (zero if it failed).
func (c *Client) connectAndServe(ctx context.Context) (connectedAt time.Time, err error) {
	dialCtx, dialCancel := context.WithTimeout(ctx, 15*time.Second)
	defer dialCancel()

	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + c.Token},
		},
	}

	conn, resp, err := websocket.Dial(dialCtx, c.URL, opts)
	if err != nil {
		if ctx.Err() != nil {
			return time.Time{}, err
		}
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			if c.Logger != nil {
				c.Logger.Error("invalid host token rejected by relay", "status", resp.StatusCode)
			}
			c.setLastError(fmt.Sprintf("relay rejected the host token (HTTP %d); re-run configure with the relay's AW_HOST_TOKEN", resp.StatusCode))
			return time.Time{}, errInvalidToken
		}
		c.setLastError("relay unreachable: " + c.redact(err.Error()))
		return time.Time{}, fmt.Errorf("dial: %w", err)
	}
	connectedAt = time.Now()
	defer conn.Close(websocket.StatusNormalClosure, "closing")

	conn.SetReadLimit(1 << 20) // 1 MB limit

	sendCh := make(chan outMsg, sendBuffer)
	flushCh := make(chan struct{}, 1)

	c.mu.Lock()
	c.connected = true
	c.sendCh = sendCh
	c.flushCh = flushCh
	c.lastErr = ""
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.connected = false
		c.sendCh = nil
		c.flushCh = nil
		c.requeueUnsentLocked(sendCh)
		if ctx.Err() == nil && err != nil {
			c.lastErr = "relay connection lost: " + c.redact(err.Error())
		}
		c.mu.Unlock()
	}()

	// hello + snapshot go first, before anything queued.
	var sent []string
	if c.OnConnect != nil {
		for _, m := range c.OnConnect(ctx) {
			data, err := json.Marshal(m)
			if err != nil {
				return connectedAt, fmt.Errorf("encode on_connect msg: %w", err)
			}
			if err := c.write(ctx, conn, data); err != nil {
				return connectedAt, fmt.Errorf("write on_connect msg: %w", err)
			}
			sent = append(sent, describe(m).typ)
		}
	}

	flushed, err := c.flushHistory(ctx, conn)
	if err != nil {
		return connectedAt, err
	}

	if c.Logger != nil {
		c.Logger.Info("relay connected; hello sent",
			"url", c.URL, "sent", strings.Join(sent, ","), "history_flushed", flushed)
	}

	connDone := make(chan struct{})
	var closeOnce sync.Once
	signalClose := func() {
		closeOnce.Do(func() {
			close(connDone)
			_ = conn.Close(websocket.StatusNormalClosure, "disconnect")
		})
	}

	// Ping keepalive: every 30 s with a 10 s timeout.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-connDone:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Ping(pingCtx)
				pingCancel()
				if err != nil {
					if c.Logger != nil {
						c.Logger.Warn("ping timeout or error, reconnecting", "err", err)
					}
					signalClose()
					return
				}
			}
		}
	}()

	// Writer: buffered frames, plus history queued while the buffer was full.
	go func() {
		for {
			select {
			case <-connDone:
				return
			case <-ctx.Done():
				return
			case om := <-sendCh:
				if err := c.write(ctx, conn, om.data); err != nil {
					if om.hist != nil {
						c.pushHistoryFront(*om.hist)
					}
					if c.Logger != nil {
						c.Logger.Warn("websocket write error", "err", err, "type", om.typ)
					}
					signalClose()
					return
				}
			case <-flushCh:
				if _, err := c.flushHistory(ctx, conn); err != nil {
					if c.Logger != nil {
						c.Logger.Warn("websocket write error", "err", err, "type", model.WireHistoryItem)
					}
					signalClose()
					return
				}
			}
		}
	}()

	// Reader loop
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			signalClose()
			var closeErr websocket.CloseError
			if errors.As(err, &closeErr) && closeErr.Code == 4000 {
				if c.Logger != nil {
					c.Logger.Warn("relay closed connection: replaced by another host instance")
				}
				return connectedAt, errors.New("replaced by another bridge connected to the relay (close 4000)")
			}
			return connectedAt, err
		}

		if typ != websocket.MessageText {
			continue
		}

		msg, err := model.DecodeWire(data)
		if err != nil {
			if c.Logger != nil {
				c.Logger.Warn("failed to decode wire message from relay", "err", err)
			}
			continue
		}

		if c.OnMessage != nil {
			c.OnMessage(msg)
		}
	}
}

// write sends one text frame with a 10 s timeout.
func (c *Client) write(ctx context.Context, conn *websocket.Conn, data []byte) error {
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, data)
}

// flushHistory writes every queued history item, oldest first. An item whose
// write fails goes back to the front of the queue.
func (c *Client) flushHistory(ctx context.Context, conn *websocket.Conn) (int, error) {
	n := 0
	for {
		h, ok := c.popHistory()
		if !ok {
			return n, nil
		}
		data, err := json.Marshal(h)
		if err != nil {
			continue
		}
		if err := c.write(ctx, conn, data); err != nil {
			c.pushHistoryFront(h)
			return n, fmt.Errorf("flush history item: %w", err)
		}
		n++
	}
}

// redact removes the token from an error text, should a library ever echo it.
func (c *Client) redact(s string) string {
	if c.Token == "" {
		return s
	}
	return strings.ReplaceAll(s, c.Token, "[redacted]")
}
