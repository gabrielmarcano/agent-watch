package relay

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// captureLogs sends the default slog logger to a buffer for the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func setBaseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AW_HOST_TOKEN", testHostToken)
	t.Setenv("AW_LISTEN", "")
	t.Setenv("AW_DATA_DIR", "")
	t.Setenv("AW_TRUSTED_PROXIES", "")
	t.Setenv("AW_CLIENT_IP_HEADER", "")
	// Setenv registers the restore; Unsetenv makes the variable really absent.
	t.Setenv("AW_TRUST_CF_IP", "")
	os.Unsetenv("AW_TRUST_CF_IP")
}

func TestLoadConfig_ClientIPDefaults(t *testing.T) {
	setBaseEnv(t)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.TrustedProxies) != 0 || cfg.ClientIPHeader != "" {
		t.Fatalf("default client IP policy = %v / %q, want no trusted proxies and no header", cfg.TrustedProxies, cfg.ClientIPHeader)
	}
}

func TestLoadConfig_TrustedProxiesAndHeader(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("AW_TRUSTED_PROXIES", "172.16.0.0/12,127.0.0.1")
	t.Setenv("AW_CLIENT_IP_HEADER", " cf-connecting-ip ")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[0].String() != "172.16.0.0/12" || cfg.TrustedProxies[1].String() != "127.0.0.1/32" {
		t.Fatalf("TrustedProxies = %v", cfg.TrustedProxies)
	}
	if cfg.ClientIPHeader != "Cf-Connecting-Ip" {
		t.Fatalf("ClientIPHeader = %q, want the canonical form", cfg.ClientIPHeader)
	}
}

func TestLoadConfig_InvalidTrustedProxies(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("AW_TRUSTED_PROXIES", "172.16.0.0/12,not-a-cidr")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "AW_TRUSTED_PROXIES") {
		t.Fatalf("LoadConfig err = %v, want an AW_TRUSTED_PROXIES error", err)
	}
}

// A client IP header can only ever be honored from a trusted proxy, so setting
// one without AW_TRUSTED_PROXIES is a configuration mistake.
func TestLoadConfig_HeaderWithoutTrustedProxies(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("AW_CLIENT_IP_HEADER", "CF-Connecting-IP")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "AW_TRUSTED_PROXIES") {
		t.Fatalf("LoadConfig err = %v, want an error pointing at AW_TRUSTED_PROXIES", err)
	}
}

// AW_TRUST_CF_IP is gone: it is ignored, with one warning saying what to use.
func TestLoadConfig_LegacyTrustCFIPWarns(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("AW_TRUST_CF_IP", "true")
	logs := captureLogs(t)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.TrustedProxies) != 0 || cfg.ClientIPHeader != "" {
		t.Fatalf("AW_TRUST_CF_IP changed the policy: %v / %q", cfg.TrustedProxies, cfg.ClientIPHeader)
	}
	out := logs.String()
	if n := strings.Count(out, "AW_TRUST_CF_IP"); n != 1 {
		t.Fatalf("want exactly one warning mentioning AW_TRUST_CF_IP, got %d in %q", n, out)
	}
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "AW_TRUSTED_PROXIES") || !strings.Contains(out, "AW_CLIENT_IP_HEADER") {
		t.Fatalf("warning must be WARN level and name the replacements: %q", out)
	}
}

func TestLoadConfig_NoLegacyWarningWhenUnset(t *testing.T) {
	setBaseEnv(t)
	logs := captureLogs(t)
	if _, err := LoadConfig(); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if strings.Contains(logs.String(), "AW_TRUST_CF_IP") {
		t.Fatalf("unexpected legacy warning: %q", logs.String())
	}
}
