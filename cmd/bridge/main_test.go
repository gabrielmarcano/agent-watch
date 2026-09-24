package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gabrielmarcano/agent-monitor/pkg/bridge"
)

func TestCLI_Configure(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	validToken := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	// 1. Success case
	err := runConfigure([]string{
		"--relay-url", "wss://relay.example.com/v1/host",
		"--host-token", validToken,
		"--host-name", "my-mac",
		"--claude-config-dir", "/path/one",
		"--claude-config-dir", "/path/two",
		"--config", cfgPath,
	})
	if err != nil {
		t.Fatalf("runConfigure failed: %v", err)
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

	// 2. Reject bad URL (ws on non-localhost)
	err = runConfigure([]string{
		"--relay-url", "ws://relay.example.com/v1/host",
		"--host-token", validToken,
		"--config", filepath.Join(tmpDir, "bad.toml"),
	})
	if err == nil {
		t.Error("expected error for ws on remote host, got nil")
	}

	// 3. Reject bad token
	err = runConfigure([]string{
		"--relay-url", "wss://relay.example.com/v1/host",
		"--host-token", "not-a-valid-hex-token",
		"--config", filepath.Join(tmpDir, "bad2.toml"),
	})
	if err == nil {
		t.Error("expected error for non-hex token, got nil")
	}
}

func TestCLI_RenderPlistTemplate(t *testing.T) {
	mgr := newServiceManager()

	binary := "/usr/local/bin/agent-watch-bridge"
	configPath := "/Users/test/.config/herdr/plugins/config/herdr-agent-watch/config.toml"
	socketPath := "/Users/test/.config/herdr/herdr.sock"
	stateDir := "/Users/test/.local/state/agent-watch"
	logPath := "/Users/test/Library/Logs/agent-watch-bridge.log"

	rendered, err := mgr.RenderTemplate(binary, configPath, socketPath, stateDir, logPath)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	// Verify required plist keys and values
	checks := []string{
		"<key>Label</key><string>com.gabrielmarcano.agent-watch-bridge</string>",
		"<string>" + binary + "</string>",
		"<string>run</string>",
		"<string>--config</string>",
		"<string>" + configPath + "</string>",
		"<key>HERDR_SOCKET_PATH</key><string>" + socketPath + "</string>",
		"<key>HERDR_PLUGIN_STATE_DIR</key><string>" + stateDir + "</string>",
		"<key>RunAtLoad</key><true/>",
		"<key>KeepAlive</key><true/>",
		"<key>ThrottleInterval</key><integer>10</integer>",
		"<key>StandardOutPath</key><string>" + logPath + "</string>",
		"<key>StandardErrorPath</key><string>" + logPath + "</string>",
	}

	for _, check := range checks {
		if !strings.Contains(rendered, check) {
			t.Errorf("rendered plist missing expected content: %s\nFull rendered:\n%s", check, rendered)
		}
	}
}
