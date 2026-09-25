package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gabrielmarcano/agent-monitor/pkg/bridge"
)

// writeEnvFile writes an agent-watch.env with mode 0600 and returns its path.
func writeEnvFile(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "agent-watch.env")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadEnvFile(t *testing.T) {
	p := writeEnvFile(t, t.TempDir(), strings.Join([]string{
		"# Agent Watch",
		"",
		"AW_RELAY_DOMAIN=relay.example.com",
		"  AW_RELAY_SSH =  root@relay.example.com  ",
		"AW_RELAY_SSH_OPTS=-i ~/.ssh/key -o Port=2222 # inline comment",
		"AW_EMPTY=",
		"AW_TWICE=first",
		"AW_TWICE=second",
		"   # indented comment",
		"AW_LAST=no-trailing-newline",
	}, "\n"))

	got, err := readEnvFile(p)
	if err != nil {
		t.Fatalf("readEnvFile: %v", err)
	}
	want := map[string]string{
		"AW_RELAY_DOMAIN":   "relay.example.com",
		"AW_RELAY_SSH":      "root@relay.example.com",
		"AW_RELAY_SSH_OPTS": "-i ~/.ssh/key -o Port=2222",
		"AW_EMPTY":          "",
		"AW_TWICE":          "second",
		"AW_LAST":           "no-trailing-newline",
	}
	if len(got) != len(want) {
		t.Errorf("got %d keys %v, want %d", len(got), got, len(want))
	}
	for k, v := range want {
		if gv, ok := got[k]; !ok || gv != v {
			t.Errorf("%s = %q (set %v), want %q", k, gv, ok, v)
		}
	}
}

// A malformed line is an error that names the line number but never quotes
// the line: the file holds the host token.
func TestReadEnvFile_RejectsMalformedLinesWithoutQuotingThem(t *testing.T) {
	for _, line := range []string{
		"AW_HOST_TOKEN " + testToken, // no '='
		"export AW_HOST_TOKEN=" + testToken,
		"=" + testToken,
		"9AW=" + testToken,
	} {
		p := writeEnvFile(t, t.TempDir(), "# ok\n"+line+"\n")
		_, err := readEnvFile(p)
		if err == nil {
			t.Errorf("%q: no error", line)
			continue
		}
		if !strings.Contains(err.Error(), "line 2") {
			t.Errorf("%q: error %q does not name line 2", line, err)
		}
		if strings.Contains(err.Error(), testToken) {
			t.Errorf("%q: error quotes the token: %q", line, err)
		}
	}
}

func TestReadEnvFile_Missing(t *testing.T) {
	_, err := readEnvFile(filepath.Join(t.TempDir(), "agent-watch.env"))
	if err == nil || !strings.Contains(err.Error(), "make config") {
		t.Errorf("err = %v, want a hint to run make config", err)
	}
}

func TestRelayURLFromDomain(t *testing.T) {
	for _, tc := range []struct{ domain, want string }{
		{"relay.example.com", "wss://relay.example.com/v1/host"},
		{"relay.example.com:8443", "wss://relay.example.com:8443/v1/host"},
		{"RELAY-1.example.com", "wss://RELAY-1.example.com/v1/host"},
	} {
		got, err := relayURLFromDomain(tc.domain)
		if err != nil || got != tc.want {
			t.Errorf("%q → %q, %v; want %q", tc.domain, got, err, tc.want)
		}
	}
	for _, bad := range []string{
		"",
		"https://relay.example.com",
		"wss://relay.example.com",
		"relay.example.com/",
		"relay.example.com/v1/host",
		"relay example.com",
		`"relay.example.com"`,
		"user@relay.example.com",
		"relay.example.com:",
		"relay.example.com:port",
		"-relay.example.com",
	} {
		if got, err := relayURLFromDomain(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

func TestCLI_ConfigureFromEnvFile(t *testing.T) {
	ta := newTestApp(t, "launchd")
	cfgPath := filepath.Join(ta.home, "config.toml")
	envPath := writeEnvFile(t, ta.home, strings.Join([]string{
		"AW_RELAY_DOMAIN=relay.example.com",
		"AW_HOST_TOKEN=" + testToken,
		"AW_RELAY_SSH=root@relay.example.com",
		"AW_NTFY_TOKEN=unrelated-relay-secret",
	}, "\n")+"\n")

	if err := ta.cmdConfigure([]string{"--env-file", envPath, "--host-name", "my-mac", "--config", cfgPath}); err != nil {
		t.Fatalf("configure --env-file: %v", err)
	}
	cfg, err := bridge.CheckConfig(cfgPath)
	if err != nil {
		t.Fatalf("written config: %v", err)
	}
	if cfg.RelayURL != "wss://relay.example.com/v1/host" {
		t.Errorf("relay_url = %q", cfg.RelayURL)
	}
	if cfg.HostToken != testToken {
		t.Error("host_token is not the file's AW_HOST_TOKEN")
	}
	if cfg.HostName != "my-mac" {
		t.Errorf("host_name = %q", cfg.HostName)
	}
	if strings.Contains(ta.out.String()+ta.errOut.String(), testToken) {
		t.Error("configure printed the token")
	}
}

// Explicit flags override the file, value by value.
func TestCLI_ConfigureFlagsOverrideEnvFile(t *testing.T) {
	ta := newTestApp(t, "launchd")
	cfgPath := filepath.Join(ta.home, "config.toml")
	otherToken := strings.Repeat("ab", 32)
	envPath := writeEnvFile(t, ta.home, "AW_RELAY_DOMAIN=relay.example.com\nAW_HOST_TOKEN="+testToken+"\n")

	if err := ta.cmdConfigure([]string{"--env-file", envPath, "--relay-url", "ws://localhost:8080", "--config", cfgPath}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	cfg, err := bridge.CheckConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RelayURL != "ws://localhost:8080/v1/host" || cfg.HostToken != testToken {
		t.Errorf("relay_url = %q, token from file = %v", cfg.RelayURL, cfg.HostToken == testToken)
	}

	if err := ta.cmdConfigure([]string{"--env-file", envPath, "--host-token", otherToken, "--config", cfgPath}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if cfg, err = bridge.CheckConfig(cfgPath); err != nil {
		t.Fatal(err)
	}
	if cfg.RelayURL != "wss://relay.example.com/v1/host" || cfg.HostToken != otherToken {
		t.Errorf("relay_url = %q, token from flag = %v", cfg.RelayURL, cfg.HostToken == otherToken)
	}
}

// Every refusal leaves the existing config alone and never echoes the token.
func TestCLI_ConfigureEnvFileErrors(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		args          []string
		want          string
	}{
		{"no domain", "AW_HOST_TOKEN=" + testToken + "\n", nil, "AW_RELAY_DOMAIN"},
		{"empty domain", "AW_RELAY_DOMAIN=\nAW_HOST_TOKEN=" + testToken + "\n", nil, "AW_RELAY_DOMAIN"},
		{"domain with scheme", "AW_RELAY_DOMAIN=https://relay.example.com\nAW_HOST_TOKEN=" + testToken + "\n", nil, "AW_RELAY_DOMAIN"},
		{"no token", "AW_RELAY_DOMAIN=relay.example.com\n", nil, "AW_HOST_TOKEN"},
		{"empty token", "AW_RELAY_DOMAIN=relay.example.com\nAW_HOST_TOKEN=\n", nil, "AW_HOST_TOKEN"},
		{"short token", "AW_RELAY_DOMAIN=relay.example.com\nAW_HOST_TOKEN=" + testToken[:40] + "\n", nil, "host_token"},
		{"malformed line", "AW_RELAY_DOMAIN relay.example.com\nAW_HOST_TOKEN=" + testToken + "\n", nil, "line 1"},
		{"no domain, bad flag URL", "AW_HOST_TOKEN=" + testToken + "\n", []string{"--relay-url", "ws://relay.example.com"}, "ws://"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ta := newTestApp(t, "launchd")
			cfgPath := filepath.Join(ta.home, "config.toml")
			writeConfig(t, cfgPath, "wss://old.example.com/v1/host")
			before, _ := os.ReadFile(cfgPath)

			envPath := writeEnvFile(t, ta.home, tc.content)
			err := ta.cmdConfigure(append([]string{"--env-file", envPath, "--config", cfgPath}, tc.args...))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
			if strings.Contains(err.Error(), testToken[:40]) {
				t.Errorf("error quotes the token: %v", err)
			}
			if after, _ := os.ReadFile(cfgPath); string(after) != string(before) {
				t.Error("a refused configure rewrote the config")
			}
		})
	}
}

func TestCLI_ConfigureEnvFileMissing(t *testing.T) {
	ta := newTestApp(t, "launchd")
	err := ta.cmdConfigure([]string{"--env-file", filepath.Join(ta.home, "nope.env"), "--config", filepath.Join(ta.home, "c.toml")})
	if err == nil || !strings.Contains(err.Error(), "nope.env") {
		t.Errorf("err = %v, want it to name the missing file", err)
	}
}

// Like ssh with a private key: an env file other users can read still works,
// with a warning to fix its mode.
func TestCLI_ConfigureWarnsAboutReadableEnvFile(t *testing.T) {
	ta := newTestApp(t, "launchd")
	envPath := writeEnvFile(t, ta.home, "AW_RELAY_DOMAIN=relay.example.com\nAW_HOST_TOKEN="+testToken+"\n")
	if err := os.Chmod(envPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ta.cmdConfigure([]string{"--env-file", envPath, "--config", filepath.Join(ta.home, "c.toml")}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if !strings.Contains(ta.errOut.String(), "chmod 600") {
		t.Errorf("no warning about the file mode: %q", ta.errOut)
	}

	ta.errOut.Reset()
	if err := os.Chmod(envPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ta.cmdConfigure([]string{"--env-file", envPath, "--config", filepath.Join(ta.home, "c.toml")}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if strings.Contains(ta.errOut.String(), "chmod") {
		t.Errorf("warning for a 0600 file: %q", ta.errOut)
	}
}

// configure rewrites the whole config: settings the new call does not give
// are dropped, and it says so instead of losing them silently.
func TestCLI_ConfigureNotesDroppedSettings(t *testing.T) {
	ta := newTestApp(t, "launchd")
	cfgPath := filepath.Join(ta.home, "config.toml")
	if err := bridge.SaveConfig(cfgPath, &bridge.Config{
		RelayURL: "wss://relay.example.com/v1/host", HostToken: testToken,
		HostName: "old-name", ClaudeConfigDirs: []string{"/claude/work"},
	}); err != nil {
		t.Fatal(err)
	}
	envPath := writeEnvFile(t, ta.home, "AW_RELAY_DOMAIN=relay.example.com\nAW_HOST_TOKEN="+testToken+"\n")

	if err := ta.cmdConfigure([]string{"--env-file", envPath, "--config", cfgPath}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	errOut := ta.errOut.String()
	for _, want := range []string{"host_name", "--host-name", "claude_config_dirs", "--claude-config-dir"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr does not mention %q: %q", want, errOut)
		}
	}

	// Given again, nothing is dropped and nothing is said.
	again := []string{"--env-file", envPath, "--config", cfgPath, "--host-name", "new-name", "--claude-config-dir", "/claude/work"}
	for i := 0; i < 2; i++ {
		ta.errOut.Reset()
		if err := ta.cmdConfigure(again); err != nil {
			t.Fatalf("configure: %v", err)
		}
	}
	if strings.Contains(ta.errOut.String(), "dropped") {
		t.Errorf("unexpected note: %q", ta.errOut)
	}
}
