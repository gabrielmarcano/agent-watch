package herdr

import (
	"bufio"
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

var (
	idCounter uint64
	idPrefix  = randomIDPrefix()
)

// randomIDPrefix returns 16 hex characters chosen once per process, so ids
// from two bridge processes (or a restart) never collide.
func randomIDPrefix() string {
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		binary.BigEndian.PutUint64(b[:], mrand.Uint64())
	}
	return hex.EncodeToString(b[:])
}

// nextID returns a request id: the random process prefix plus a counter,
// formatted as a string (herdr rejects numeric ids).
func nextID() string {
	return fmt.Sprintf("%s-%d", idPrefix, atomic.AddUint64(&idCounter, 1))
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
// Follows the one-connection-per-call contract. It is bounded by ctx's
// deadline, or by Timeout when ctx has none, and cancelling ctx abandons the
// call at once; either way the error wraps ctx's error (context.Canceled or
// context.DeadlineExceeded), never ErrUnavailable.
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
	if err := callCtx.Err(); err != nil {
		return fmt.Errorf("herdr %s: %w", method, err)
	}

	var d net.Dialer
	conn, err := d.DialContext(callCtx, "unix", c.SocketPath)
	if err != nil {
		if ctxErr := callCtx.Err(); ctxErr != nil {
			return fmt.Errorf("herdr %s: %w", method, ctxErr)
		}
		return fmt.Errorf("%w: dial %s: %w", ErrUnavailable, c.SocketPath, err)
	}
	defer conn.Close()

	// Closing the connection is what unblocks a pending write or read when
	// ctx is cancelled before its deadline.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-callCtx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()
	// ioErr maps an I/O failure to ctx's error when ctx caused it. The
	// connection deadline equals callCtx's, and can fire an instant before
	// callCtx notices its own.
	ioErr := func(op string, err error) error {
		if ctxErr := callCtx.Err(); ctxErr != nil {
			return fmt.Errorf("herdr %s %s: %w", method, op, ctxErr)
		}
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return fmt.Errorf("herdr %s %s: %w: %w", method, op, context.DeadlineExceeded, err)
		}
		return fmt.Errorf("herdr %s: %w", op, err)
	}

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
		return ioErr("write", err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return ioErr("read", err)
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

// ListWorkspaces retrieves the list of workspaces from herdr.
func (c *Client) ListWorkspaces(ctx context.Context) ([]WorkspaceInfo, error) {
	var res struct {
		Type       string          `json:"type"`
		Workspaces []WorkspaceInfo `json:"workspaces"`
	}
	if err := c.Call(ctx, "workspace.list", nil, &res); err != nil {
		return nil, err
	}
	return res.Workspaces, nil
}
