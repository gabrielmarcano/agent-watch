package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

var idCounter uint64

func nextID() string {
	return fmt.Sprintf("req-%d-%d", time.Now().UnixNano(), atomic.AddUint64(&idCounter, 1))
}

// Client communicates with the herdr socket over NDJSON RPC.
type Client struct {
	SocketPath string        // path to herdr.sock
	Timeout    time.Duration // per call; default 5s when zero
}

// NewClient resolves the socket path: explicit arg, else $HERDR_SOCKET_PATH, else ~/.config/herdr/herdr.sock.
func NewClient(socketPath string) *Client {
	p := socketPath
	if p == "" {
		p = os.Getenv("HERDR_SOCKET_PATH")
	}
	if p == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			p = filepath.Join(home, ".config", "herdr", "herdr.sock")
		} else {
			p = "/tmp/herdr.sock"
		}
	}
	return &Client{
		SocketPath: p,
		Timeout:    5 * time.Second,
	}
}

// Call executes a single JSON-RPC method call on the herdr socket.
// Follows the one-connection-per-call contract.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	callCtx := ctx
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		callCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	var d net.Dialer
	conn, err := d.DialContext(callCtx, "unix", c.SocketPath)
	if err != nil {
		return fmt.Errorf("%w: dial %s: %w", ErrUnavailable, c.SocketPath, err)
	}
	defer conn.Close()

	if dl, ok := callCtx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	if params == nil {
		params = struct{}{}
	}

	reqID := nextID()
	req := map[string]any{
		"id":     reqID,
		"method": method,
		"params": params,
	}

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("herdr write: %w", err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return fmt.Errorf("herdr read: %w", err)
	}

	var resp struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("herdr decode: %w", err)
	}

	if resp.Error != nil {
		return resp.Error
	}

	if out != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("herdr decode result: %w", err)
		}
	}

	return nil
}

// Ping queries herdr's health, protocol, and version.
func (c *Client) Ping(ctx context.Context) (Pong, error) {
	var pong Pong
	err := c.Call(ctx, "ping", nil, &pong)
	return pong, err
}

// ListAgents retrieves the authoritative list of agents from herdr.
func (c *Client) ListAgents(ctx context.Context) ([]AgentInfo, error) {
	var res struct {
		Type   string      `json:"type"`
		Agents []AgentInfo `json:"agents"`
	}
	if err := c.Call(ctx, "agent.list", nil, &res); err != nil {
		return nil, err
	}
	return res.Agents, nil
}

// Read extracts text from a pane's screen buffer.
func (c *Client) Read(ctx context.Context, paneID string, src ReadSource, lines int) (string, error) {
	params := map[string]any{
		"target": paneID,
		"source": string(src),
		"format": "text",
	}
	if lines > 0 {
		params["lines"] = lines
	}

	var res struct {
		Type string `json:"type"`
		Read struct {
			Text string `json:"text"`
		} `json:"read"`
	}
	if err := c.Call(ctx, "agent.read", params, &res); err != nil {
		return "", err
	}
	return res.Read.Text, nil
}

// SendKeys dispatches keystrokes to an agent pane.
func (c *Client) SendKeys(ctx context.Context, paneID string, keys []string) error {
	params := map[string]any{
		"target": paneID,
		"keys":   keys,
	}
	return c.Call(ctx, "agent.send_keys", params, nil)
}

// Prompt submits user input to an agent pane. Never passes the blocking "wait" parameter.
func (c *Client) Prompt(ctx context.Context, paneID, text string) error {
	params := map[string]any{
		"target": paneID,
		"text":   text,
	}
	return c.Call(ctx, "agent.prompt", params, nil)
}
