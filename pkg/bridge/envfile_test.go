package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testEnvToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func writeTestEnvFile(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "agent-watch.env")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadEnvFile(t *testing.T) {
	p := writeTestEnvFile(t, t.TempDir(), strings.Join([]string{
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

	got, err := ReadEnvFile(p)
	if err != nil {
		t.Fatalf("ReadEnvFile: %v", err)
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

func TestReadEnvFile_RejectsMalformedLinesWithoutQuotingThem(t *testing.T) {
	for _, line := range []string{
		"AW_HOST_TOKEN " + testEnvToken, // no '='
		"export AW_HOST_TOKEN=" + testEnvToken,
		"=" + testEnvToken,
		"9AW=" + testEnvToken,
	} {
		p := writeTestEnvFile(t, t.TempDir(), "# ok\n"+line+"\n")
		_, err := ReadEnvFile(p)
		if err == nil {
			t.Errorf("%q: no error", line)
			continue
		}
		if !strings.Contains(err.Error(), "line 2") {
			t.Errorf("%q: error %q does not name line 2", line, err)
		}
		if strings.Contains(err.Error(), testEnvToken) {
			t.Errorf("%q: error quotes the token: %q", line, err)
		}
	}
}

func TestReadEnvFile_Missing(t *testing.T) {
	_, err := ReadEnvFile(filepath.Join(t.TempDir(), "agent-watch.env"))
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
		got, err := RelayURLFromDomain(tc.domain)
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
		if got, err := RelayURLFromDomain(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

func TestUpdateConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	envPath := writeTestEnvFile(t, dir, "AW_RELAY_DOMAIN=relay.example.com\nAW_HOST_TOKEN="+testEnvToken+"\n")

	// 1. Initial write
	cfg, notes, warn, err := UpdateConfig(cfgPath, ConfigureOptions{
		EnvFile:          envPath,
		HostName:         "host-one",
		ClaudeConfigDirs: []string{"/claude/dir"},
		SessionRoots:     []string{"/work/dir"},
		SessionKinds:     []string{"claude"},
	})
	if err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	if warn != "" {
		t.Errorf("unexpected warn: %q", warn)
	}
	if len(notes) != 0 {
		t.Errorf("unexpected notes on initial write: %v", notes)
	}
	if cfg.RelayURL != "wss://relay.example.com/v1/host" || cfg.HostToken != testEnvToken {
		t.Errorf("unexpected cfg: %+v", cfg)
	}

	// 2. Rewrite dropping optional fields notes what was dropped
	cfg2, notes2, _, err := UpdateConfig(cfgPath, ConfigureOptions{
		EnvFile: envPath,
	})
	if err != nil {
		t.Fatalf("UpdateConfig 2: %v", err)
	}
	if len(notes2) != 4 {
		t.Errorf("got %d notes, want 4 for dropped fields: %v", len(notes2), notes2)
	}
	if cfg2.HostName != "" || len(cfg2.SessionRoots) != 0 {
		t.Errorf("dropped fields remained in cfg: %+v", cfg2)
	}
}
