package relayclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// Client manages an outbound WebSocket connection from the bridge to the cloud relay.
type Client struct {
	URL        string                          // wss://relay.example.com/v1/host
	Token      string                          // host bearer token
	OnConnect  func(ctx context.Context) []any // messages to send first: hello + snapshot
	OnMessage  func(msg any)                   // decoded with model.DecodeWire; called sequentially
	Logger     *slog.Logger
	MinBackoff time.Duration // optional initial backoff (defaults to 1s)

	mu          sync.Mutex
	conn        *websocket.Conn
	connected   bool
	sendCh      chan []byte
	historyQ    []model.HistoryItemMsg // bounded queue of up to 50
	activeCmds  map[string]struct{}    // currently active command request IDs for connection
	healthyOnce sync.Once
}

// Connected reports whether the client currently has an active connection to the relay.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Send enqueues msg for transmission to the relay without blocking.
// If disconnected, updates/status/command_results are dropped; history items are queued (up to 50).
func (c *Client) Send(msg any) {
	wireBytes, err := json.Marshal(msg)
	if err != nil {
		if c.Logger != nil {
			c.Logger.Error("failed to encode wire message", "err", err)
		}
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected && c.sendCh != nil {
		select {
		case c.sendCh <- wireBytes:
			return
		default:
			if c.Logger != nil {
				c.Logger.Warn("send channel buffer full, dropping message")
			}
		}
	}

	// While disconnected, handle queuing rules
	switch m := msg.(type) {
	case model.HistoryItemMsg:
		if len(c.historyQ) >= 50 {
			// Drop oldest
			c.historyQ = c.historyQ[1:]
		}
		c.historyQ = append(c.historyQ, m)
	case *model.HistoryItemMsg:
		if len(c.historyQ) >= 50 {
			c.historyQ = c.historyQ[1:]
		}
		c.historyQ = append(c.historyQ, *m)
	default:
		// agent_update, agent_removed, herdr_status, command_result are dropped while disconnected
	}
}

// Run executes the reconnect loop until ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	initialBackoff := 1 * time.Second
	if c.MinBackoff > 0 {
		initialBackoff = c.MinBackoff
	}
	backoff := initialBackoff
	maxBackoff := 60 * time.Second

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		connectedAt := time.Now()
		err := c.connectAndServe(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if time.Since(connectedAt) >= 60*time.Second {
			backoff = initialBackoff
		}

		if errors.Is(err, errInvalidToken) {
			backoff = maxBackoff
		}

		// Calculate backoff with +/- 20% jitter
		jitterFactor := 0.8 + rand.Float64()*0.4 // [0.8, 1.2]
		sleepDuration := time.Duration(float64(backoff) * jitterFactor)

		if c.Logger != nil {
			c.Logger.Info("reconnecting to relay", "backoff", sleepDuration, "err", err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleepDuration):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

var errInvalidToken = errors.New("invalid host token")

func (c *Client) connectAndServe(ctx context.Context) error {
	dialCtx, dialCancel := context.WithTimeout(ctx, 15*time.Second)
	defer dialCancel()

	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + c.Token},
		},
	}

	conn, resp, err := websocket.Dial(dialCtx, c.URL, opts)
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			if c.Logger != nil {
				c.Logger.Error("invalid host token rejected by relay", "status", resp.StatusCode)
			}
			return errInvalidToken
		}
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "closing")

	conn.SetReadLimit(1 << 20) // 1 MB limit

	sendCh := make(chan []byte, 100)

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.sendCh = sendCh
	c.activeCmds = make(map[string]struct{})
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.connected = false
		c.conn = nil
		c.sendCh = nil
		c.activeCmds = nil
		c.mu.Unlock()
	}()

	// Send initial OnConnect messages first in order
	if c.OnConnect != nil {
		initMsgs := c.OnConnect(ctx)
		for _, m := range initMsgs {
			bytes, err := json.Marshal(m)
			if err != nil {
				return fmt.Errorf("encode on_connect msg: %w", err)
			}
			writeCtx, writeCancel := context.WithTimeout(ctx, 10*time.Second)
			err = conn.Write(writeCtx, websocket.MessageText, bytes)
			writeCancel()
			if err != nil {
				return fmt.Errorf("write on_connect msg: %w", err)
			}
		}
	}

	// Flush queued history items, keeping unwritten items if connection drops
	for {
		c.mu.Lock()
		if len(c.historyQ) == 0 {
			c.mu.Unlock()
			break
		}
		h := c.historyQ[0]
		c.mu.Unlock()

		bytes, err := json.Marshal(h)
		if err != nil {
			c.mu.Lock()
			if len(c.historyQ) > 0 {
				c.historyQ = c.historyQ[1:]
			}
			c.mu.Unlock()
			continue
		}

		writeCtx, writeCancel := context.WithTimeout(ctx, 10*time.Second)
		err = conn.Write(writeCtx, websocket.MessageText, bytes)
		writeCancel()
		if err != nil {
			return fmt.Errorf("flush history item: %w", err)
		}

		c.mu.Lock()
		if len(c.historyQ) > 0 {
			c.historyQ = c.historyQ[1:]
		}
		c.mu.Unlock()
	}

	// Channel to signal internal disconnect
	connDone := make(chan struct{})
	var closeOnce sync.Once
	signalClose := func() {
		closeOnce.Do(func() {
			close(connDone)
			_ = conn.Close(websocket.StatusNormalClosure, "disconnect")
		})
	}

	// Ping keepalive goroutine: every 30s with 10s timeout
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

	// Writer goroutine
	go func() {
		for {
			select {
			case <-connDone:
				return
			case <-ctx.Done():
				return
			case data, ok := <-sendCh:
				if !ok {
					return
				}
				writeCtx, writeCancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Write(writeCtx, websocket.MessageText, data)
				writeCancel()
				if err != nil {
					if c.Logger != nil {
						c.Logger.Warn("websocket write error", "err", err)
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
			}
			return err
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
