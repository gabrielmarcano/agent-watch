package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gabrielmarcano/agent-monitor/pkg/buildinfo"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
	"github.com/gabrielmarcano/agent-monitor/pkg/relay"
)

const testHostToken = "1111222233334444555566667777888899990000aaaabbbbccccddddeeeeffff"

// shortTempDir returns a temp dir with a short path: Unix socket paths are
// limited to ~104 bytes on macOS, and t.TempDir() paths can exceed that.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "awr")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func freeListenAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// startRelay runs `agent-watch-relay serve` in-process and returns its base URL
// and a stop function that shuts it down and waits for run() to return.
func startRelay(t *testing.T) (string, func()) {
	t.Helper()
	addr := freeListenAddr(t)
	t.Setenv("AW_LISTEN", addr)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		done <- run(ctx, []string{"serve"}, &stdout, &stderr)
	}()

	base := "http://" + addr
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(base + "/v1/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		select {
		case err := <-done:
			cancel()
			t.Fatalf("relay exited during startup: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("relay did not become healthy at %s", base)
		}
		time.Sleep(20 * time.Millisecond)
	}

	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("relay serve returned error on shutdown: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Errorf("relay did not shut down")
		}
		http.DefaultClient.CloseIdleConnections()
	}
	t.Cleanup(stop)
	return base, stop
}

func deviceRequest(t *testing.T, method, url, token, body string) int {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// TestCLI_RevokeWhileRelayRunning is the end-to-end check for device revocation:
// revoking through the CLI while the relay runs must take effect in the running
// process immediately, close the device's SSE streams, never be undone by a later
// save, and survive a restart.
func TestCLI_RevokeWhileRelayRunning(t *testing.T) {
	dir := shortTempDir(t)
	t.Setenv("AW_DATA_DIR", dir)
	t.Setenv("AW_HOST_TOKEN", testHostToken)
	t.Setenv("AW_FCM_CREDENTIALS", "")
	t.Setenv("AW_NTFY_URL", "")
	t.Setenv("AW_NTFY_TOPIC", "")

	// Seed two devices and one history item while the relay is stopped.
	stolenToken, _ := relay.GenerateDeviceToken()
	keeperToken, _ := relay.GenerateDeviceToken()
	store, err := relay.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	stolen, _ := store.AddDevice("Stolen Watch", relay.Sha256Hex(stolenToken))
	keeper, _ := store.AddDevice("Keeper Watch", relay.Sha256Hex(keeperToken))
	store.AddHistory(model.HistoryItem{ID: "hist-keep", PaneID: "w1:p1", CompletedAt: model.Now()})
	if err := store.Close(); err != nil {
		t.Fatalf("store close: %v", err)
	}

	base, stop := startRelay(t)

	// The admin socket is local-only and owner-only.
	fi, err := os.Stat(filepath.Join(dir, "admin.sock"))
	if err != nil {
		t.Fatalf("admin socket missing while the relay runs: %v", err)
	}
	if fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0600 {
		t.Fatalf("admin socket mode = %v, want a 0600 Unix socket", fi.Mode())
	}

	if code := deviceRequest(t, http.MethodGet, base+"/v1/agents", stolenToken, ""); code != http.StatusOK {
		t.Fatalf("before revoke: GET /v1/agents = %d, want 200", code)
	}

	// Open an SSE stream for the device that is about to be revoked.
	sseCtx, sseCancel := context.WithCancel(context.Background())
	defer sseCancel()
	sseReq, _ := http.NewRequestWithContext(sseCtx, http.MethodGet, base+"/v1/events", nil)
	sseReq.Header.Set("Authorization", "Bearer "+stolenToken)
	sseResp, err := http.DefaultClient.Do(sseReq)
	if err != nil {
		t.Fatalf("GET /v1/events: %v", err)
	}
	defer sseResp.Body.Close()
	reader := bufio.NewReader(sseResp.Body)
	if line, _ := reader.ReadString('\n'); strings.TrimSpace(line) != "event: snapshot" {
		t.Fatalf("expected snapshot first, got %q", line)
	}
	sseClosed := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, reader)
		close(sseClosed)
	}()

	// `devices list` while running shows both devices.
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"devices", "list"}, &stdout, &stderr); err != nil {
		t.Fatalf("devices list: %v", err)
	}
	if !strings.Contains(stdout.String(), stolen.ID) || !strings.Contains(stdout.String(), keeper.ID) {
		t.Fatalf("devices list while running = %q, want both devices", stdout.String())
	}

	// Revoke through the CLI path while the relay is running.
	stdout.Reset()
	if err := run(context.Background(), []string{"devices", "revoke", stolen.ID}, &stdout, &stderr); err != nil {
		t.Fatalf("devices revoke: %v", err)
	}
	if !strings.Contains(stdout.String(), "revoked successfully") {
		t.Errorf("revoke output = %q", stdout.String())
	}

	// The running relay rejects the token on the very next request.
	if code := deviceRequest(t, http.MethodGet, base+"/v1/agents", stolenToken, ""); code != http.StatusUnauthorized {
		t.Errorf("after revoke: GET /v1/agents = %d, want 401", code)
	}
	if code := deviceRequest(t, http.MethodPost, base+"/v1/push/register", stolenToken, `{"platform":"fcm","token":"x"}`); code != http.StatusUnauthorized {
		t.Errorf("after revoke: POST /v1/push/register = %d, want 401", code)
	}
	// The other device is untouched.
	if code := deviceRequest(t, http.MethodGet, base+"/v1/agents", keeperToken, ""); code != http.StatusOK {
		t.Errorf("after revoke: keeper GET /v1/agents = %d, want 200", code)
	}

	// The revoked device's open SSE stream is closed by the relay.
	select {
	case <-sseClosed:
	case <-time.After(3 * time.Second):
		t.Errorf("SSE stream of the revoked device is still open")
		sseCancel()
	}

	// `devices list` while running no longer shows it.
	stdout.Reset()
	if err := run(context.Background(), []string{"devices", "list"}, &stdout, &stderr); err != nil {
		t.Fatalf("devices list: %v", err)
	}
	if strings.Contains(stdout.String(), stolen.ID) {
		t.Errorf("devices list after revoke still shows %s: %q", stolen.ID, stdout.String())
	}

	stop()

	// On disk: the revoked device is gone, everything else survived.
	data, err := os.ReadFile(filepath.Join(dir, "store.json"))
	if err != nil {
		t.Fatalf("read store.json: %v", err)
	}
	if strings.Contains(string(data), stolen.ID) {
		t.Errorf("store.json still contains revoked device %s after shutdown", stolen.ID)
	}
	for _, want := range []string{keeper.ID, "hist-keep"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("store.json lost %q", want)
		}
	}

	// After a restart the device stays revoked.
	base, stop = startRelay(t)
	if code := deviceRequest(t, http.MethodGet, base+"/v1/agents", stolenToken, ""); code != http.StatusUnauthorized {
		t.Errorf("after restart: GET /v1/agents = %d, want 401", code)
	}
	if code := deviceRequest(t, http.MethodGet, base+"/v1/agents", keeperToken, ""); code != http.StatusOK {
		t.Errorf("after restart: keeper GET /v1/agents = %d, want 200", code)
	}
	stop()
}

// TestCLI_SecondRelayRefusesSameDataDir checks the data-dir lock: two relays on
// one store would overwrite each other's saves.
func TestCLI_SecondRelayRefusesSameDataDir(t *testing.T) {
	dir := shortTempDir(t)
	t.Setenv("AW_DATA_DIR", dir)
	t.Setenv("AW_HOST_TOKEN", testHostToken)

	_, stop := startRelay(t)
	defer stop()

	t.Setenv("AW_LISTEN", freeListenAddr(t))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	err := run(ctx, []string{"serve"}, &stdout, &stderr)
	if err == nil || !strings.Contains(fmt.Sprint(err), "already") {
		t.Fatalf("second relay on the same data dir: err = %v, want 'already running' error", err)
	}
}

// setVersion stamps the package version as -ldflags would, for one test.
func setVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

func TestCLI_Version(t *testing.T) {
	setVersion(t, "0.3.0")
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"version"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run version failed: %v", err)
	}
	// Test binaries carry no VCS stamp: buildinfo adds no commit here.
	if want := "agent-watch-relay " + buildinfo.String("0.3.0") + "\n"; stdout.String() != want {
		t.Errorf("version output = %q, want %q", stdout.String(), want)
	}
}

// serve hands its version (with the commit) to the bridge in the /v1/host
// handshake response.
func TestServe_SendsVersionToHost(t *testing.T) {
	setVersion(t, "0.3.0-test")
	t.Setenv("AW_DATA_DIR", shortTempDir(t))
	t.Setenv("AW_HOST_TOKEN", testHostToken)
	base, _ := startRelay(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/v1/host", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + testHostToken}},
	})
	if err != nil {
		t.Fatalf("dial /v1/host: %v", err)
	}
	defer conn.CloseNow()
	if got, want := resp.Header.Get(relay.RelayVersionHeader), buildinfo.String("0.3.0-test"); got != want {
		t.Errorf("%s = %q, want %q", relay.RelayVersionHeader, got, want)
	}
}

func TestCLI_DevicesListAndRevoke(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AW_DATA_DIR", dir)

	// 1. Empty list
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"devices", "list"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("devices list failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "No registered devices found") {
		t.Errorf("expected empty list message, got %q", stdout.String())
	}

	// 2. Add a device to store directly
	store, err := relay.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	dev, err := store.AddDevice("Test Watch", relay.Sha256Hex("some-token"))
	if err != nil {
		t.Fatalf("AddDevice failed: %v", err)
	}
	_ = store.Flush()
	_ = store.Close()

	// 3. List should now show the device
	stdout.Reset()
	stderr.Reset()
	err = run(context.Background(), []string{"devices", "list"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("devices list failed: %v", err)
	}
	if !strings.Contains(stdout.String(), dev.ID) || !strings.Contains(stdout.String(), "Test Watch") {
		t.Errorf("expected device %s in output, got %q", dev.ID, stdout.String())
	}

	// 4. Revoke non-existent device fails
	stdout.Reset()
	stderr.Reset()
	err = run(context.Background(), []string{"devices", "revoke", "nonexistent"}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected error revoking nonexistent device, got nil")
	}

	// 5. Revoke the real device succeeds
	stdout.Reset()
	stderr.Reset()
	err = run(context.Background(), []string{"devices", "revoke", dev.ID}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("devices revoke failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "revoked successfully") {
		t.Errorf("expected revoked message, got %q", stdout.String())
	}

	// 6. List is empty again
	stdout.Reset()
	stderr.Reset()
	_ = run(context.Background(), []string{"devices", "list"}, &stdout, &stderr)
	if !strings.Contains(stdout.String(), "No registered devices found") {
		t.Errorf("expected no devices after revocation, got %q", stdout.String())
	}
}

func TestCLI_ServeConfigValidation(t *testing.T) {
	// Missing AW_HOST_TOKEN should fail runServe
	t.Setenv("AW_HOST_TOKEN", "")
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"serve"}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected error running serve without AW_HOST_TOKEN, got nil")
	}
}

func TestCLI_UnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"unknown-cmd"}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected error for unknown command, got nil")
	}
}
