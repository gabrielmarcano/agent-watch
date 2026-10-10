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
	"regexp"
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
	// Without AW_HOST_TOKEN and without a registered host, serve fails.
	t.Setenv("AW_DATA_DIR", shortTempDir(t))
	t.Setenv("AW_HOST_TOKEN", "")
	t.Setenv("AW_LISTEN", freeListenAddr(t))
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"serve"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "AW_HOST_TOKEN is required") {
		t.Fatalf("serve without AW_HOST_TOKEN or hosts = %v, want AW_HOST_TOKEN is required", err)
	}

	t.Setenv("AW_HOST_ID", "Not Valid")
	t.Setenv("AW_HOST_TOKEN", testHostToken)
	if err := run(context.Background(), []string{"serve"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "AW_HOST_ID") {
		t.Fatalf("serve with an invalid AW_HOST_ID = %v", err)
	}
}

// hostsCmd runs `agent-watch-relay hosts …` and returns stdout and stderr.
func hostsCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), append([]string{"hosts"}, args...), &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

// dialHost opens /v1/host with token and returns the connection, or the
// handshake's HTTP status.
func dialHost(t *testing.T, ctx context.Context, base, token string) (*websocket.Conn, int) {
	t.Helper()
	conn, resp, err := websocket.Dial(ctx, strings.Replace(base, "http://", "ws://", 1)+"/v1/host", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}},
	})
	if err != nil {
		if resp == nil {
			t.Fatalf("dial host: %v", err)
		}
		return nil, resp.StatusCode
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn, http.StatusSwitchingProtocols
}

// Hosts are managed with the relay stopped (store.json under the lock) and
// running (admin socket): a new token works at once, a revoked one is
// rejected and its connection closed, and the AW_HOST_TOKEN host is listed
// but managed in the environment.
func TestCLI_Hosts(t *testing.T) {
	dir := shortTempDir(t)
	t.Setenv("AW_DATA_DIR", dir)
	t.Setenv("AW_HOST_TOKEN", "")
	t.Setenv("AW_FCM_CREDENTIALS", "")
	t.Setenv("AW_NTFY_URL", "")
	t.Setenv("AW_NTFY_TOPIC", "")

	// Relay stopped: add a host, so the relay can start without AW_HOST_TOKEN.
	out, msg, err := hostsCmd(t, "add", "linux-1", "--name", "Build box")
	if err != nil {
		t.Fatalf("hosts add: %v", err)
	}
	linuxToken := strings.TrimSpace(out)
	if len(linuxToken) != 64 || !strings.Contains(msg, "relay not running") || strings.Contains(msg, linuxToken) {
		t.Fatalf("hosts add: stdout %q, stderr %q; want the token alone on stdout", out, msg)
	}
	if _, _, err := hostsCmd(t, "add", "linux-1"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("hosts add of an existing id = %v", err)
	}
	if _, _, err := hostsCmd(t, "add", "Bad_ID"); err == nil {
		t.Fatal("hosts add accepted an invalid id")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "store.json")); bytes.Contains(data, []byte(linuxToken)) {
		t.Fatal("store.json holds the raw host token")
	}

	t.Setenv("AW_HOST_TOKEN", testHostToken)
	t.Setenv("AW_HOST_NAME", "Mac")
	base, _ := startRelay(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	linux, status := dialHost(t, ctx, base, linuxToken)
	if linux == nil {
		t.Fatalf("handshake with the added host's token = %d, want 101", status)
	}
	hello := `{"type":"hello","version":"test","host":"build","herdr_online":true}`
	if err := linux.Write(ctx, websocket.MessageText, []byte(hello)); err != nil {
		t.Fatalf("hello: %v", err)
	}

	// Relay running: add through the admin socket; the token works at once.
	out, msg, err = hostsCmd(t, "add", "pi")
	if err != nil || !strings.Contains(msg, "by the running relay") {
		t.Fatalf("hosts add while running = %v, %q", err, msg)
	}
	if conn, status := dialHost(t, ctx, base, strings.TrimSpace(out)); conn == nil {
		t.Fatalf("handshake with a token added while running = %d, want 101", status)
	}
	if _, _, err := hostsCmd(t, "add", "main"); err == nil || !strings.Contains(err.Error(), "AW_HOST_TOKEN") {
		t.Fatalf("hosts add main (the AW_HOST_TOKEN host) = %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		out, _, err = hostsCmd(t, "list")
		if err != nil {
			t.Fatalf("hosts list: %v", err)
		}
		if strings.Contains(out, "linux-1") && regexpLine(out, `linux-1\s+Build box\s+yes`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("hosts list never showed linux-1 online:\n%s", out)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !regexpLine(out, `main\s+Mac\s+no\s+\(AW_HOST_TOKEN\)`) || !strings.Contains(out, "pi") {
		t.Fatalf("hosts list =\n%s", out)
	}

	// Revoke while running: the connection closes, the token is refused.
	out, _, err = hostsCmd(t, "revoke", "linux-1")
	if err != nil || !strings.Contains(out, "connection was closed") {
		t.Fatalf("hosts revoke = %v, %q", err, out)
	}
	if _, _, err := linux.Read(ctx); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("revoked host's connection: %v, want closed with 1008", err)
	}
	if _, status := dialHost(t, ctx, base, linuxToken); status != http.StatusUnauthorized {
		t.Fatalf("handshake with a revoked token = %d, want 401", status)
	}
	if _, _, err := hostsCmd(t, "revoke", "linux-1"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("revoking it again = %v, want not found", err)
	}
	if _, _, err := hostsCmd(t, "revoke", "main"); err == nil || !strings.Contains(err.Error(), "AW_HOST_TOKEN") {
		t.Fatalf("hosts revoke main = %v", err)
	}
}

// regexpLine reports whether some line of out matches pattern.
func regexpLine(out, pattern string) bool {
	re := regexp.MustCompile(pattern)
	for _, line := range strings.Split(out, "\n") {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

func TestCLI_UnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"unknown-cmd"}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected error for unknown command, got nil")
	}
}
