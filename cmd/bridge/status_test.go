package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/bridge"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

func writeStatus(t *testing.T, dir string, st bridge.StatusFile) {
	t.Helper()
	if err := bridge.WriteStatus(filepath.Join(dir, "status.json"), st); err != nil {
		t.Fatal(err)
	}
}

func localStatus(t *testing.T, ta *testApp) bridge.LocalStatus {
	t.Helper()
	ta.out.Reset()
	if err := ta.cmdStatus([]string{"--json", "--local"}); err != nil {
		t.Fatalf("status --json --local: %v", err)
	}
	var ls bridge.LocalStatus
	if err := json.Unmarshal(ta.out.Bytes(), &ls); err != nil {
		t.Fatalf("decode %q: %v", ta.out, err)
	}
	return ls
}

// status --json --local reads the state dir from the installed definition,
// not from the caller's environment, checks the pid, and makes no network
// call and no launchctl call.
func TestStatusLocal_ReadsInstalledStateDir(t *testing.T) {
	ta := newTestApp(t, "launchd")
	stateDir := filepath.Join(ta.home, ".local", "state", "herdr", "plugins", "herdr-agent-watch")
	cfg := filepath.Join(ta.home, "cfg", "config.toml")
	writeConfig(t, cfg, "wss://relay.example.com/v1/host")
	install(t, ta, ServiceSpec{
		Binary:     ta.exe,
		ConfigPath: cfg,
		SocketPath: "/herdr.sock",
		StateDir:   stateDir,
		LogPath:    filepath.Join(ta.home, "bridge.log"),
	})
	writeStatus(t, stateDir, bridge.StatusFile{
		PID: os.Getpid(), RelayConnected: true, HerdrOnline: true, Agents: 5, Blocked: 2,
		Version: "0.3.0 (c8aa72e)", RelayVersion: "0.3.0 (5a32851)", UpdatedAt: model.Now(),
	})
	// A decoy at the default state dir must be ignored.
	writeStatus(t, ta.defaults().StateDir, bridge.StatusFile{PID: os.Getpid(), Agents: 99, UpdatedAt: model.Now()})

	ls := localStatus(t, ta)
	if !ls.Installed || !ls.Configured || !ls.Running || ls.Stale {
		t.Errorf("installed=%v configured=%v running=%v stale=%v", ls.Installed, ls.Configured, ls.Running, ls.Stale)
	}
	if ls.Agents != 5 || ls.Blocked != 2 || !ls.RelayConnected || !ls.HerdrOnline {
		t.Errorf("agents=%d blocked=%d relay=%v herdr=%v", ls.Agents, ls.Blocked, ls.RelayConnected, ls.HerdrOnline)
	}
	if ls.StateDir != stateDir || ls.Binary != ta.exe || ls.RelayHost != "relay.example.com" {
		t.Errorf("state_dir=%q binary=%q relay_host=%q", ls.StateDir, ls.Binary, ls.RelayHost)
	}
	if ls.Service != "launchd" || ls.LogPath != filepath.Join(ta.home, "bridge.log") {
		t.Errorf("service=%q log=%q", ls.Service, ls.LogPath)
	}
	if ls.DaemonVersion != "0.3.0 (c8aa72e)" || ls.RelayVersion != "0.3.0 (5a32851)" || ls.Version != fullVersion {
		t.Errorf("daemon_version=%q relay_version=%q version=%q", ls.DaemonVersion, ls.RelayVersion, ls.Version)
	}
	if strings.Contains(ta.out.String(), testToken) {
		t.Error("status output contains the host token")
	}
	if calls := ta.runner.Calls(); len(calls) != 0 {
		t.Errorf("status --local ran %v", calls)
	}
}

func TestStatusLocal_NotInstalledFallsBackToEnvironment(t *testing.T) {
	ta := newTestApp(t, "launchd")
	stateDir := filepath.Join(ta.home, "herdr-state")
	ta.env["HERDR_PLUGIN_STATE_DIR"] = stateDir
	writeStatus(t, stateDir, bridge.StoppedStatus("0.2.0", ""))

	ls := localStatus(t, ta)
	if ls.Installed || ls.Configured || ls.Running {
		t.Errorf("installed=%v configured=%v running=%v, want all false", ls.Installed, ls.Configured, ls.Running)
	}
	if !strings.Contains(ls.ConfigError, "configure") {
		t.Errorf("config_error = %q, want a hint to run configure", ls.ConfigError)
	}
	if ls.StateDir != stateDir {
		t.Errorf("state_dir = %q, want %q", ls.StateDir, stateDir)
	}
}

func TestStatusLocal_StaleAndStopped(t *testing.T) {
	ta := newTestApp(t, "launchd")
	stateDir := ta.defaults().StateDir

	writeStatus(t, stateDir, bridge.StatusFile{
		PID: os.Getpid(), RelayConnected: true,
		UpdatedAt: time.Now().Add(-30 * time.Second).UTC().Format(time.RFC3339),
	})
	if ls := localStatus(t, ta); !ls.Running || !ls.Stale || ls.AgeSeconds < 29 {
		t.Errorf("old status: running=%v stale=%v age=%d, want running, stale, ~30", ls.Running, ls.Stale, ls.AgeSeconds)
	}

	writeStatus(t, stateDir, bridge.StoppedStatus("0.2.0", "config missing"))
	if ls := localStatus(t, ta); ls.Running || ls.RelayConnected || ls.LastError != "config missing" {
		t.Errorf("stopped: running=%v relay=%v last_error=%q", ls.Running, ls.RelayConnected, ls.LastError)
	}
}

// The human `status --local` keeps exit status 1 when the bridge is not
// running, without contacting launchd or the relay.
func TestStatusHumanLocal_ExitsNonZeroWhenStopped(t *testing.T) {
	ta := newTestApp(t, "launchd")
	err := ta.cmdStatus([]string{"--local"})
	if !errors.Is(err, errExitStatus1) {
		t.Errorf("err = %v, want errExitStatus1", err)
	}
	if !strings.Contains(ta.out.String(), "not running") {
		t.Errorf("output = %q", ta.out)
	}
	if calls := ta.runner.Calls(); len(calls) != 0 {
		t.Errorf("status --local ran %v", calls)
	}
}
