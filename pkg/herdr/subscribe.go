package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"time"
)

// Event is an incoming JSON-RPC 2.0 notification from herdr-core's
// events.subscribe stream (method "event" or "missed").
type Event struct {
	Name string
	Data json.RawMessage
}

// Subscribe opens a long-lived events.subscribe stream to herdr-core.
// Returns only after receiving the initial SubscribeResult ack (containing
// seq and terminals), or an error. The handshake (dial, request, ack) is
// bounded by ctx and by the client's Timeout; the stream itself has no deadline.
// The returned channel closes when ctx is cancelled or the connection drops.
func (c *Client) Subscribe(ctx context.Context) (<-chan Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("herdr subscribe: %w", err)
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	handshakeDeadline := time.Now().Add(timeout)
	deadlineFromCtx := false
	if dl, ok := ctx.Deadline(); ok && dl.Before(handshakeDeadline) {
		handshakeDeadline = dl
		deadlineFromCtx = true
	}

	var d net.Dialer
	dialCtx, cancelDial := context.WithDeadline(ctx, handshakeDeadline)
	conn, err := d.DialContext(dialCtx, "unix", c.SocketPath)
	dialExpired := dialCtx.Err() != nil
	cancelDial()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("herdr subscribe: %w", ctxErr)
		}
		if deadlineFromCtx && dialExpired {
			return nil, fmt.Errorf("herdr subscribe: %w", context.DeadlineExceeded)
		}
		return nil, fmt.Errorf("%w: dial %s: %w", ErrUnavailable, c.SocketPath, err)
	}

	// Watch ctx for the whole life of the connection, handshake included:
	// closing the connection is what unblocks a pending read.
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()
	fail := func(err error) (<-chan Event, error) {
		close(stop)
		_ = conn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("herdr subscribe: %w", ctxErr)
		}
		if deadlineFromCtx && errors.Is(err, os.ErrDeadlineExceeded) {
			return nil, fmt.Errorf("herdr subscribe: %w: %w", context.DeadlineExceeded, err)
		}
		return nil, err
	}

	if err := conn.SetDeadline(handshakeDeadline); err != nil {
		return fail(fmt.Errorf("herdr subscribe deadline: %w", err))
	}

	reqID := nextID()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"method":  "events.subscribe",
		"params":  map[string]any{},
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fail(fmt.Errorf("herdr write subscribe: %w", err))
	}

	// Notifications are read through this same reader: it may already hold
	// bytes that arrived together with the ack.
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return fail(fmt.Errorf("herdr read subscribe ack: %w", err))
	}

	var resp struct {
		ID     string `json:"id"`
		Result *struct {
			Terminals []json.RawMessage `json:"terminals"`
		} `json:"result"`
		Error *Error `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return fail(fmt.Errorf("herdr decode subscribe ack: %w", err))
	}
	if err := checkResponseID("events.subscribe", reqID, resp.ID, resp.Error); err != nil {
		return fail(err)
	}
	if resp.Error != nil {
		return fail(resp.Error)
	}
	if resp.Result == nil || resp.Result.Terminals == nil {
		return fail(fmt.Errorf("herdr subscribe: missing terminals in ack"))
	}

	// The stream has no deadline.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return fail(fmt.Errorf("herdr subscribe deadline: %w", err))
	}

	events := make(chan Event, 64)

	go func() {
		defer close(events)
		defer close(stop)
		defer conn.Close()

		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64*1024), 1<<20)

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			var raw struct {
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(line, &raw); err != nil {
				continue
			}
			if raw.Method == "" || raw.Method == "heartbeat" {
				continue
			}

			select {
			case events <- Event{Name: raw.Method, Data: raw.Params}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return events, nil
}
