package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// The ntfy topic is a secret (anyone who knows it can read the pushes), so it
// must never reach the logs.
func TestNewServer_DoesNotLogNtfyTopic(t *testing.T) {
	const topic = "secret-topic-4f9c2a7e1b"

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	server, err := NewServer(&Config{
		ListenAddr: ":0",
		HostToken:  testHostToken,
		DataDir:    t.TempDir(),
		NtfyURL:    "https://ntfy.example.com",
		NtfyTopic:  topic,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	logs := buf.String()
	if !strings.Contains(logs, "ntfy push enabled") {
		t.Fatalf("expected the ntfy startup log, got %q", logs)
	}
	if strings.Contains(logs, topic) {
		t.Fatalf("startup logs leak the ntfy topic: %q", logs)
	}
}

// shortTempDir returns a temp dir with a short path: Unix socket paths (the
// admin socket) are limited to ~104 bytes on macOS.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "awr")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// serving is a Server running Serve on a loopback listener.
type serving struct {
	server *Server
	addr   string
	stop   context.CancelFunc
	done   chan struct{} // closed when Serve returned; err is then set
	err    error
}

func startServing(t *testing.T, configure func(*Config)) *serving {
	t.Helper()
	cfg := &Config{HostToken: testHostToken, DataDir: shortTempDir(t)}
	if configure != nil {
		configure(cfg)
	}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = server.Close()
		t.Fatalf("listen: %v", err)
	}
	ctx, stop := context.WithCancel(context.Background())
	sv := &serving{server: server, addr: ln.Addr().String(), stop: stop, done: make(chan struct{})}
	go func() {
		sv.err = server.Serve(ctx, ln)
		close(sv.done)
	}()
	t.Cleanup(func() {
		stop()
		select {
		case <-sv.done:
		case <-time.After(15 * time.Second):
			t.Errorf("server did not stop")
		}
	})
	return sv
}

// A stop/restart with a watch streaming and the bridge connected must end
// every stream, flush the store and return nil (exit 0) quickly, instead of
// waiting out the 10 s shutdown budget and failing.
func TestServer_ShutdownEndsStreamsAndExitsCleanly(t *testing.T) {
	sv := startServing(t, nil)
	devToken, _ := GenerateDeviceToken()
	_, _ = sv.server.Store().AddDevice("Watch", Sha256Hex(devToken))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+sv.addr+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+devToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/events: %v", err)
	}
	defer resp.Body.Close()
	stream := bufio.NewReader(resp.Body)
	if name, _ := nextSSEEvent(t, stream); name != "snapshot" {
		t.Fatalf("first event = %q, want snapshot", name)
	}

	host, _, err := websocket.Dial(ctx, "ws://"+sv.addr+"/v1/host", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + testHostToken}},
	})
	if err != nil {
		t.Fatalf("dial host: %v", err)
	}
	defer host.CloseNow()
	hello, _ := json.Marshal(model.HelloMsg{Type: model.WireHello, Host: "mac", HerdrOnline: true})
	item, _ := json.Marshal(model.HistoryItemMsg{Type: model.WireHistoryItem, Item: model.HistoryItem{
		ID: "hist-shutdown", PaneID: "w1:p1", CompletedAt: model.Now(),
	}})
	for _, msg := range [][]byte{hello, item} {
		if err := host.Write(ctx, websocket.MessageText, msg); err != nil {
			t.Fatalf("host write: %v", err)
		}
	}
	for {
		if name, _ := nextSSEEvent(t, stream); name == "history" {
			break
		}
	}

	start := time.Now()
	sv.stop()
	select {
	case <-sv.done:
		if sv.err != nil {
			t.Fatalf("Serve returned %v after %v, want nil", sv.err, time.Since(start))
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("shutdown still running after 5s")
	}

	// The SSE stream was ended by the server, not by our deadline.
	_, _ = io.Copy(io.Discard, stream)
	if ctx.Err() != nil {
		t.Fatalf("SSE stream still open after shutdown")
	}
	// The bridge's socket was closed.
	if _, _, err := host.Read(ctx); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("host socket after shutdown: read err = %v, want closed", err)
	}
	// The store was flushed and the data-dir lock released.
	data, err := os.ReadFile(filepath.Join(sv.server.cfg.DataDir, "store.json"))
	if err != nil || !strings.Contains(string(data), "hist-shutdown") {
		t.Fatalf("store.json after shutdown lacks the history item (err %v)", err)
	}
	lock, err := lockDataDir(sv.server.cfg.DataDir)
	if err != nil {
		t.Fatalf("data-dir lock still held after shutdown: %v", err)
	}
	lock.release()
}
