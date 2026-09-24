package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
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
// Returns only after receiving the "subscription_started" ack from herdr.
// The returned channel closes when ctx is cancelled or the connection drops.
func (c *Client) Subscribe(ctx context.Context, subs []Subscription) (<-chan Event, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("%w: dial %s: %w", ErrUnavailable, c.SocketPath, err)
	}

	reqID := nextID()
	req := map[string]any{
		"id":     reqID,
		"method": "events.subscribe",
		"params": map[string]any{
			"subscriptions": subs,
		},
	}

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("herdr write subscribe: %w", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("herdr read subscribe ack: %w", err)
	}

	var resp struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("herdr decode subscribe ack: %w", err)
	}

	if resp.Error != nil {
		_ = conn.Close()
		return nil, resp.Error
	}

	events := make(chan Event, 64)

	go func() {
		defer conn.Close()
		defer close(events)

		// Close connection on ctx cancellation to interrupt any blocking Read
		done := make(chan struct{})
		defer close(done)

		go func() {
			select {
			case <-ctx.Done():
				_ = conn.Close()
			case <-done:
			}
		}()

		scanner := bufio.NewScanner(conn)
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

			select {
			case events <- Event{Name: raw.Event, Data: raw.Data}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return events, nil
}
