package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gabrielmarcano/agent-monitor/pkg/bridge"
)

func TestPresenceCommand_OnOffKeepsTheRest(t *testing.T) {
	ta := newTestApp(t, "launchd")
	cfg := filepath.Join(ta.home, "cfg", "config.toml")
	if err := bridge.SaveConfig(cfg, &bridge.Config{
		RelayURL: "wss://relay.example.com/v1/host", HostToken: testToken, HostName: "mac",
		ClaudeConfigDirs: []string{"/c"},
	}); err != nil {
		t.Fatal(err)
	}

	for _, step := range []struct {
		args []string
		want bool
		out  string
	}{
		{[]string{"--config", cfg}, false, "off"},
		{[]string{"--config", cfg, "on"}, true, "on"},
		{[]string{"--config", cfg}, true, "on"},
		{[]string{"--config", cfg, "off"}, false, "off"},
	} {
		ta.out.Reset()
		if err := ta.cmdPresence(step.args); err != nil {
			t.Fatalf("presence %v: %v", step.args, err)
		}
		if got := strings.TrimSpace(ta.out.String()); got != step.out {
			t.Errorf("presence %v printed %q, want %q", step.args, got, step.out)
		}
		c, err := bridge.LoadConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if c.PushPresence != step.want || c.HostToken != testToken || c.HostName != "mac" || len(c.ClaudeConfigDirs) != 1 {
			t.Errorf("after presence %v: %+v", step.args, c)
		}
	}
	if strings.Contains(ta.out.String()+ta.errOut.String(), testToken) {
		t.Error("presence printed the host token")
	}
}

func TestPresenceCommand_Refuses(t *testing.T) {
	ta := newTestApp(t, "launchd")
	cfg := filepath.Join(ta.home, "cfg", "config.toml")
	if err := ta.cmdPresence([]string{"--config", cfg, "on"}); err == nil {
		t.Error("presence on without a config: want an error")
	}
	writeConfig(t, cfg, "wss://relay.example.com/v1/host")
	if err := ta.cmdPresence([]string{"--config", cfg, "maybe"}); err == nil {
		t.Error("presence maybe: want an error")
	}
	if err := os.WriteFile(cfg, []byte("relay_url = \"wss://x\"\nhost_token = \"short\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ta.cmdPresence([]string{"--config", cfg, "on"}); err == nil {
		t.Error("presence on with an invalid config: want an error")
	}
	if data, _ := os.ReadFile(cfg); strings.Contains(string(data), "push_presence") {
		t.Error("an invalid config was rewritten")
	}
}

func TestConfigure_KeepsPushPresence(t *testing.T) {
	ta := newTestApp(t, "launchd")
	cfg := filepath.Join(ta.home, "cfg", "config.toml")
	if err := bridge.SaveConfig(cfg, &bridge.Config{RelayURL: "wss://relay.example.com/v1/host", HostToken: testToken, PushPresence: true}); err != nil {
		t.Fatal(err)
	}
	if err := ta.cmdConfigure([]string{"--relay-url", "wss://other.example.com/v1/host", "--host-token", testToken, "--config", cfg}); err != nil {
		t.Fatal(err)
	}
	c, err := bridge.LoadConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !c.PushPresence {
		t.Error("configure dropped push_presence")
	}
}

func TestStatusLocal_ReportsPushPresence(t *testing.T) {
	ta := newTestApp(t, "launchd")
	cfg := filepath.Join(ta.home, "cfg", "config.toml")
	if err := bridge.SaveConfig(cfg, &bridge.Config{RelayURL: "wss://relay.example.com/v1/host", HostToken: testToken, PushPresence: true}); err != nil {
		t.Fatal(err)
	}
	install(t, ta, ServiceSpec{Binary: ta.exe, ConfigPath: cfg, SocketPath: "/herdr.sock",
		StateDir: filepath.Join(ta.home, "state"), LogPath: filepath.Join(ta.home, "bridge.log")})
	if ls := localStatus(t, ta); !ls.PushPresence {
		t.Error("status --local: push_presence false, want true")
	}
}
