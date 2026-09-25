package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gabrielmarcano/agent-monitor/pkg/bridge"
)

func TestCLI_Configure(t *testing.T) {
	ta := newTestApp(t, "launchd")
	cfgPath := filepath.Join(ta.home, "config.toml")

	// 1. Success case
	err := ta.cmdConfigure([]string{
		"--relay-url", "wss://relay.example.com/v1/host",
		"--host-token", testToken,
		"--host-name", "my-mac",
		"--claude-config-dir", "/path/one",
		"--claude-config-dir", "/path/two",
		"--config", cfgPath,
	})
	if err != nil {
		t.Fatalf("configure failed: %v", err)
	}

	fi, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat config failed: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 0600", fi.Mode().Perm())
	}

	loaded, err := bridge.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if loaded.RelayURL != "wss://relay.example.com/v1/host" {
		t.Errorf("relay_url = %q", loaded.RelayURL)
	}
	if loaded.HostName != "my-mac" {
		t.Errorf("host_name = %q", loaded.HostName)
	}
	if len(loaded.ClaudeConfigDirs) != 2 || loaded.ClaudeConfigDirs[0] != "/path/one" || loaded.ClaudeConfigDirs[1] != "/path/two" {
		t.Errorf("claude_config_dirs = %v", loaded.ClaudeConfigDirs)
	}
	if strings.Contains(ta.out.String()+ta.errOut.String(), testToken) {
		t.Error("configure printed the token")
	}

	// 2. Reject bad URL (ws on non-localhost)
	err = ta.cmdConfigure([]string{
		"--relay-url", "ws://relay.example.com/v1/host",
		"--host-token", testToken,
		"--config", filepath.Join(ta.home, "bad.toml"),
	})
	if err == nil {
		t.Error("expected error for ws on remote host, got nil")
	}

	// 3. Reject bad token
	err = ta.cmdConfigure([]string{
		"--relay-url", "wss://relay.example.com/v1/host",
		"--host-token", "not-a-valid-hex-token",
		"--config", filepath.Join(ta.home, "bad2.toml"),
	})
	if err == nil {
		t.Error("expected error for non-hex token, got nil")
	}
}

// Without --config, configure writes the config the installed service uses
// (not the default path of whatever environment it runs in).
func TestCLI_ConfigureTargetsInstalledConfig(t *testing.T) {
	ta := newTestApp(t, "launchd")
	cfg := filepath.Join(ta.home, "herdr-plugin-config", "config.toml")
	install(t, ta, ServiceSpec{
		Binary: ta.exe, ConfigPath: cfg, SocketPath: "/s.sock", StateDir: "/state",
		LogPath: filepath.Join(ta.home, "bridge.log"),
	})
	if err := ta.cmdConfigure([]string{"--relay-url", "wss://relay.example.com", "--host-token", testToken}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if _, err := bridge.CheckConfig(cfg); err != nil {
		t.Errorf("installed config not written: %v", err)
	}
	if !strings.Contains(ta.errOut.String(), "restart") {
		t.Errorf("no hint to restart the installed service: %q", ta.errOut)
	}
}
