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

// Event is an incoming notification from herdr's events.subscribe stream.
type Event struct {
	Name string // snake_case, e.g. "pane_agent_status_changed"
	Data json.RawMessage
}

// Subscription defines an event category to subscribe to.
type Subscription struct {
	Type   string `json:"type"`
	PaneID string `json:"pane_id,omitempty"`
}

// Subscribe opens a long-lived events.subscribe stream.
// Returns only after receiving the "subscription_started" ack from herdr, or
// an error. The handshake (dial, request, ack) is bounded by ctx and by the
// client's Timeout; the stream itself has no deadline.
// The returned channel closes when ctx is cancelled or the connection drops.
func (c *Client) Subscribe(ctx context.Context, subs []Subscription) (<-chan Event, error) {
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
		// The connection deadline can fire an instant before ctx notices
		// its own, identical deadline.
		if deadlineFromCtx && errors.Is(err, os.ErrDeadlineExceeded) {
			return nil, fmt.Errorf("herdr subscribe: %w: %w", context.DeadlineExceeded, err)
		}
		return nil, err
	}

	if err := conn.SetDeadline(handshakeDeadline); err != nil {
		return fail(fmt.Errorf("herdr subscribe deadline: %w", err))
	}

	req := map[string]any{
		"id":     nextID(),
		"method": "events.subscribe",
		"params": map[string]any{
			"subscriptions": subs,
		},
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fail(fmt.Errorf("herdr write subscribe: %w", err))
	}

	// Events are read through this same reader: it may already hold bytes
	// that arrived together with the ack.
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return fail(fmt.Errorf("herdr read subscribe ack: %w", err))
	}

	var resp struct {
		Result *struct {
			Type string `json:"type"`
		} `json:"result"`
		Error *Error `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return fail(fmt.Errorf("herdr decode subscribe ack: %w", err))
	}
	if resp.Error != nil {
		return fail(resp.Error)
	}
	if resp.Result == nil || resp.Result.Type != "subscription_started" {
		got := ""
		if resp.Result != nil {
			got = resp.Result.Type
		}
		return fail(fmt.Errorf("herdr subscribe: unexpected ack type %q", got))
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
				Event string          `json:"event"`
				Data  json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(line, &raw); err != nil {
				continue
			}
			if raw.Event == "" {
				continue
			}

			// scanner.Bytes is reused by the next Scan; json.RawMessage
			// from Unmarshal is already a copy.
			select {
			case events <- Event{Name: raw.Event, Data: raw.Data}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return events, nil
}
