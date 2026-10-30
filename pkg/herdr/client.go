package herdr

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// ErrUnavailable indicates that the herdr-core socket could not be reached.
var ErrUnavailable = errors.New("herdr unavailable")

// Error represents a JSON-RPC 2.0 error returned by herdr-core.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// UnmarshalJSON decodes both herdr-core's JSON-RPC 2.0 error object
// ({"code":-32000,"message":"...","data":{"kind":"not_found","hint":"..."}})
// and string-coded errors ({"code":"not_found","message":"..."}).
func (e *Error) UnmarshalJSON(b []byte) error {
	var wire struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
		Data    *struct {
			Kind string `json:"kind"`
			Hint string `json:"hint"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return err
	}
	e.Message = wire.Message
	if wire.Data != nil {
		e.Code = wire.Data.Kind
		e.Hint = wire.Data.Hint
	}
	if e.Code == "" {
		if s, ok := wire.Code.(string); ok {
			e.Code = s
		} else if wire.Message != "" {
			e.Code = wire.Message
		}
	}
	return nil
}

func (e *Error) Error() string {
	return fmt.Sprintf("herdr error (%s): %s", e.Code, e.Message)
}

// IsCode reports whether err is a *herdr.Error with the given code.
func IsCode(err error, code string) bool {
	var hErr *Error
	if errors.As(err, &hErr) {
		return hErr.Code == code
	}
	return false
}

// Client is a JSON-RPC 2.0 client over a Unix domain socket for herdr-core.
type Client struct {
	SocketPath string
	Timeout    time.Duration
}

// DefaultSocketPath resolves the herdr-core Unix socket path from the
// environment ($HC_TERMINAL_SOCKET, $HERDR_SOCKET_PATH, $XDG_STATE_HOME,
// $HC_SESSION) or ~/.local/state/herdr-core/default/default.sock.
// If $HERDR_SOCKET_PATH points to the legacy ~/.config/herdr/herdr.sock path
// (e.g. from an older LaunchAgent plist) and the herdr-core socket exists,
// the herdr-core socket is preferred.
func DefaultSocketPath() string {
	if p := strings.TrimSpace(os.Getenv("HC_TERMINAL_SOCKET")); p != "" {
		return p
	}
	session := strings.TrimSpace(os.Getenv("HC_SESSION"))
	if session == "" {
		session = "default"
	}
	stateDir := strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
	if stateDir == "" {
		home, _ := os.UserHomeDir()
		stateDir = filepath.Join(home, ".local", "state")
	}
	coreSock := filepath.Join(stateDir, "herdr-core", session, "default.sock")
	if p := strings.TrimSpace(os.Getenv("HERDR_SOCKET_PATH")); p != "" {
		if strings.HasSuffix(p, filepath.Join(".config", "herdr", "herdr.sock")) {
			if _, err := os.Stat(coreSock); err == nil {
				return coreSock
			}
		}
		return p
	}
	return coreSock
}

// NewClient creates a new herdr-core client. If socketPath is empty, it falls
// back to DefaultSocketPath().
func NewClient(socketPath string) *Client {
	if socketPath == "" {
		socketPath = DefaultSocketPath()
	}
	return &Client{
		SocketPath: socketPath,
		Timeout:    5 * time.Second,
	}
}

var (
	reqPrefix  = randomPrefix()
	reqCounter atomic.Uint64
)

func randomPrefix() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%016x", uint64(os.Getpid()))
	}
	return hex.EncodeToString(b[:])
}

func nextID() string {
	seq := reqCounter.Add(1)
	return fmt.Sprintf("%s-%d", reqPrefix, seq)
}

// checkResponseID rejects a reply whose id does not match reqID, except when
// herdr-core returns a parse/request error with an empty id.
func checkResponseID(method, reqID, respID string, respErr *Error) error {
	if respID == reqID {
		return nil
	}
	if respID == "" && respErr != nil {
		return respErr
	}
	return fmt.Errorf("herdr %s: response id %q does not match request id %q", method, respID, reqID)
}

// Call executes a single request/response cycle over a new Unix socket connection.
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("herdr %s: %w", method, err)
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}

	var d net.Dialer
	dialCtx, cancelDial := context.WithDeadline(ctx, deadline)
	conn, err := d.DialContext(dialCtx, "unix", c.SocketPath)
	dialExpired := dialCtx.Err() != nil
	cancelDial()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("herdr %s: %w", method, ctxErr)
		}
		if dialExpired {
			return fmt.Errorf("herdr %s: %w: dial %s: %w", method, context.DeadlineExceeded, c.SocketPath, err)
		}
		return fmt.Errorf("%w: dial %s: %w", ErrUnavailable, c.SocketPath, err)
	}
	defer conn.Close()

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()
	wrapErr := func(stage string, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("herdr %s %s: %w", stage, method, ctxErr)
		}
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return fmt.Errorf("herdr %s %s: %w: %w", stage, method, context.DeadlineExceeded, err)
		}
		return fmt.Errorf("herdr %s %s: %w", stage, method, err)
	}

	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("herdr set deadline: %w", err)
	}

	if params == nil {
		params = map[string]any{}
	}

	reqID := nextID()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"method":  method,
		"params":  params,
	}

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return wrapErr("write", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return wrapErr("read", err)
	}

	var resp struct {
		ID     string           `json:"id"`
		Result *json.RawMessage `json:"result"`
		Error  *Error           `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("herdr decode %s: %w", method, err)
	}
	if err := checkResponseID(method, reqID, resp.ID, resp.Error); err != nil {
		return err
	}
	if resp.Error != nil {
		return resp.Error
	}
	if resp.Result == nil || string(*resp.Result) == "null" {
		return fmt.Errorf("herdr %s: response had neither result nor error", method)
	}

	if out != nil {
		if err := json.Unmarshal(*resp.Result, out); err != nil {
			return fmt.Errorf("herdr unmarshal result %s: %w", method, err)
		}
	}

	return nil
}

// Ping calls herdr-core's "hello" method and returns its version and generation.
func (c *Client) Ping(ctx context.Context) (Pong, error) {
	var res struct {
		Version    string `json:"version"`
		Generation int    `json:"generation"`
	}
	if err := c.Call(ctx, "hello", map[string]any{}, &res); err != nil {
		return Pong{}, err
	}
	if res.Version == "" {
		return Pong{}, fmt.Errorf("herdr hello: missing version in result")
	}
	return Pong{
		Version:  res.Version,
		Protocol: res.Generation,
	}, nil
}

// ListTerminals calls "terminal.list" and returns all terminals reported by herdr-core.
func (c *Client) ListTerminals(ctx context.Context) ([]Terminal, error) {
	var res struct {
		Terminals []Terminal `json:"terminals"`
	}
	if err := c.Call(ctx, "terminal.list", map[string]any{}, &res); err != nil {
		return nil, err
	}
	if res.Terminals == nil {
		return nil, fmt.Errorf("herdr terminal.list: missing terminals list")
	}
	return res.Terminals, nil
}

// ListAgents calls "terminal.list" and converts local live terminals into AgentInfo.
func (c *Client) ListAgents(ctx context.Context) ([]AgentInfo, error) {
	terminals, err := c.ListTerminals(ctx)
	if err != nil {
		return nil, err
	}

	childCounts := make(map[string]int)
	for _, t := range terminals {
		if strings.Contains(t.Address, "/") {
			continue
		}
		if t.State != "" && t.State != "live" {
			continue
		}
		if t.Parent != nil && *t.Parent != "" {
			childCounts[*t.Parent]++
		}
	}

	agents := make([]AgentInfo, 0, len(terminals))
	for _, t := range terminals {
		if strings.Contains(t.Address, "/") {
			continue
		}
		if t.State != "" && t.State != "live" {
			continue
		}
		// Background sub-agent terminals spawned via hc run --bg have a parent
		// and no herdr.space label; they count toward their parent's subagent_count.
		if t.Parent != nil && *t.Parent != "" && t.Labels["herdr.space"] == "" {
			continue
		}
		info := t.ToAgentInfo()
		if n := childCounts[t.Address]; n > 0 && info.SubagentCount == nil {
			count := n
			info.SubagentCount = &count
		}
		agents = append(agents, info)
	}
	return agents, nil
}

// Read calls "terminal.read" to fetch screen or scrollback text for target.
func (c *Client) Read(ctx context.Context, target string, source ReadSource, lines int) (string, error) {
	params := map[string]any{
		"terminal": target,
	}
	if lines > 0 {
		params["lines"] = lines
	} else if source == SourceRecentUnwrapped {
		params["lines"] = 120
	}

	var res struct {
		Text *string `json:"text"`
	}
	if err := c.Call(ctx, "terminal.read", params, &res); err != nil {
		return "", err
	}
	if res.Text == nil {
		return "", fmt.Errorf("herdr terminal.read: missing text in result")
	}
	return *res.Text, nil
}

// Prompt sends text and Enter to target via "terminal.send".
func (c *Client) Prompt(ctx context.Context, target, text string) error {
	params := map[string]any{
		"terminal": target,
		"text":     text,
	}
	return c.Call(ctx, "terminal.send", params, nil)
}

// SendKeys sends named keystrokes to target via "terminal.keys".
func (c *Client) SendKeys(ctx context.Context, target string, keys []string) error {
	params := map[string]any{
		"terminal": target,
		"keys":     keys,
	}
	return c.Call(ctx, "terminal.keys", params, nil)
}

// RunTerminal starts a new terminal in herdr-core via "terminal.run".
func (c *Client) RunTerminal(ctx context.Context, p RunParams) (Terminal, error) {
	var t Terminal
	if err := c.Call(ctx, "terminal.run", p, &t); err != nil {
		return Terminal{}, err
	}
	if t.Address == "" {
		return Terminal{}, fmt.Errorf("herdr terminal.run: missing address in result")
	}
	return t, nil
}
