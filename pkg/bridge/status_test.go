package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/relayclient"
)

// pollUntil polls cond every 5 ms until it holds or the timeout expires.
func pollUntil(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func addAgent(e *Engine, paneID, status string, seq uint64) {
	agent := "claude"
	e.OnChanges([]herdr.Change{{
		Kind: herdr.Added,
		Agent: herdr.AgentInfo{
			PaneID: paneID, WorkspaceID: "w1", TabID: "t1",
			Agent: &agent, AgentStatus: status, StateChangeSeq: seq,
		},
	}})
}

// status.json must carry the count of blocked agents: that is what the
// menu bar shows first.
func TestEngine_StatusCountsBlocked(t *testing.T) {
	h := newTestHarness(t)
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})
	addAgent(h.engine, "w1:p1", "blocked", 1)
	addAgent(h.engine, "w1:p2", "blocked", 2)
	addAgent(h.engine, "w1:p3", "idle", 3)
	addAgent(h.engine, "w1:p4", "working", 4)

	st := h.engine.Status()
	if st.Agents != 4 || st.Blocked != 2 {
		t.Errorf("agents=%d blocked=%d, want 4 and 2", st.Agents, st.Blocked)
	}
	if st.Version != "0.2.0" {
		t.Errorf("version = %q, want the bridge version", st.Version)
	}
}

// herdr going offline is reported in herdr_error and last_error, and cleared
// when it comes back.
func TestEngine_StatusReportsHerdrError(t *testing.T) {
	e := NewEngine(&herdr.Client{SocketPath: "/nonexistent/herdr.sock"}, nil, nil, nil, "0.2.0", "h", "", nil)

	e.OnHerdrOnline(false, herdr.Pong{})
	st := e.Status()
	if st.HerdrOnline || !strings.Contains(st.HerdrError, "/nonexistent/herdr.sock") {
		t.Errorf("offline: herdr_online=%v herdr_error=%q, want false and the socket path", st.HerdrOnline, st.HerdrError)
	}
	if !strings.Contains(st.LastError, st.HerdrError) {
		t.Errorf("last_error %q does not include herdr_error %q", st.LastError, st.HerdrError)
	}

	e.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})
	st = e.Status()
	if st.HerdrError != "" || st.LastError != "" {
		t.Errorf("online again: herdr_error=%q last_error=%q, want both empty", st.HerdrError, st.LastError)
	}
}

// A relay that rejects the host token shows up in relay_error and last_error.
func TestEngine_StatusReportsRelayAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	rc := &relayclient.Client{
		URL:        "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/host",
		Token:      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		MinBackoff: 10 * time.Millisecond,
		MaxBackoff: 20 * time.Millisecond,
	}
	e := NewEngine(nil, nil, nil, rc, "0.2.0", "h", "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = rc.Run(ctx) }()
	defer func() { cancel(); <-done }()

	pollUntil(t, 3*time.Second, "relay_error", func() bool {
		return strings.Contains(e.Status().RelayError, "401")
	})
	st := e.Status()
	if st.RelayConnected {
		t.Error("relay_connected = true with a rejected token")
	}
	if !strings.Contains(st.LastError, "401") {
		t.Errorf("last_error = %q, want the relay auth failure", st.LastError)
	}
}

// On a clean exit the status file must say the bridge is stopped, not keep
// the last "connected" snapshot until someone notices the pid is gone.
func TestEngine_StatusWriterMarksCleanExit(t *testing.T) {
	h := newTestHarness(t)
	h.engine.OnHerdrOnline(true, herdr.Pong{Version: "0.9.1", Protocol: 22})
	addAgent(h.engine, "w1:p1", "blocked", 1)
	path := filepath.Join(t.TempDir(), "state", "status.json")
	h.engine.StatusPath = path

	ctx, cancel := context.WithCancel(context.Background())
	done := h.engine.StartStatusWriter(ctx, 10*time.Millisecond)
	pollUntil(t, 3*time.Second, "a running status file", func() bool {
		st, err := ReadStatus(path)
		return err == nil && st.PID == os.Getpid() && st.RelayConnected && st.Blocked == 1
	})

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("status writer did not finish after cancel")
	}

	st, err := ReadStatus(path)
	if err != nil {
		t.Fatalf("read final status: %v", err)
	}
	if st.PID != 0 || st.RelayConnected || st.HerdrOnline || st.Agents != 0 || st.Blocked != 0 {
		t.Errorf("final status = %+v, want pid 0 and nothing connected", st)
	}
	if st.UpdatedAt == "" || st.Version != "0.2.0" {
		t.Errorf("final status lacks updated_at/version: %+v", st)
	}
}

// CheckConfig explains what is wrong with a config without echoing its
// contents (a TOML parse error can quote the token line).
func TestCheckConfig_SafeErrors(t *testing.T) {
	dir := t.TempDir()
	token := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	missing := filepath.Join(dir, "missing.toml")
	if _, err := CheckConfig(missing); err == nil || !strings.Contains(err.Error(), "configure") {
		t.Errorf("missing config: err = %v, want a hint to run configure", err)
	}

	broken := filepath.Join(dir, "broken.toml")
	if err := os.WriteFile(broken, []byte("relay_url = \"wss://r.example.com\"\nhost_token = \""+token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := CheckConfig(broken)
	if err == nil {
		t.Fatal("broken TOML accepted")
	}
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), token[:16]) {
		t.Errorf("parse error leaks the token: %v", err)
	}

	invalid := filepath.Join(dir, "invalid.toml")
	if err := SaveConfig(invalid, &Config{RelayURL: "http://r.example.com", HostToken: token}); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckConfig(invalid); err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Errorf("invalid URL: err = %v, want the validation message", err)
	}

	good := filepath.Join(dir, "good.toml")
	if err := SaveConfig(good, &Config{RelayURL: "wss://r.example.com", HostToken: token}); err != nil {
		t.Fatal(err)
	}
	if cfg, err := CheckConfig(good); err != nil || cfg.RelayURL != "wss://r.example.com" {
		t.Errorf("good config: cfg=%+v err=%v", cfg, err)
	}
}

// os.WriteFile(…, 0600) keeps an existing file's mode and MkdirAll(0700)
// leaves an existing directory alone: SaveConfig must tighten both.
func TestSaveConfig_TightensExistingModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "herdr-agent-watch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("relay_url = \"old\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{RelayURL: "wss://r.example.com", HostToken: strings.Repeat("ab", 32)}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %v (err %v), want 0600", fi.Mode().Perm(), err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("config dir mode = %v (err %v), want 0700", fi.Mode().Perm(), err)
	}
	got, err := LoadConfig(path)
	if err != nil || got.RelayURL != cfg.RelayURL || got.HostToken != cfg.HostToken {
		t.Errorf("reloaded config = %+v, %v", got, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("config dir holds %d entries, want only config.toml (no temp files)", len(entries))
	}
}

// Older readers (the first menu bar build) decode these keys unconditionally:
// they must always be present.
func TestStatusFile_KeepsLegacyKeys(t *testing.T) {
	data, err := json.Marshal(StatusFile{})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"pid", "relay_connected", "herdr_online", "agents", "last_error", "updated_at", "blocked"} {
		if _, ok := m[k]; !ok {
			t.Errorf("status.json lacks key %q", k)
		}
	}
}
