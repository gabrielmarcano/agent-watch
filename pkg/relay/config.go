package relay

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

// Default configuration values.
const (
	DefaultListenAddr = ":8080"
	DefaultDataDir    = "/var/lib/agent-watch-relay"
)

// Config holds runtime configuration loaded from environment variables.
type Config struct {
	ListenAddr     string
	HostToken      string
	DataDir        string
	FCMCredentials string
	NtfyURL        string
	NtfyTopic      string
	NtfyToken      string

	// TrustedProxies (AW_TRUSTED_PROXIES) are the reverse proxies whose
	// forwarding headers identify the client. Empty: use RemoteAddr only.
	TrustedProxies []netip.Prefix
	// ClientIPHeader (AW_CLIENT_IP_HEADER) is an optional single-IP header,
	// e.g. CF-Connecting-IP, honored only from a trusted proxy.
	ClientIPHeader string
	// PushResolved (AW_PUSH_RESOLVED) enables the FCM "resolved" push that
	// withdraws an answered approval. Off by default: enable it only once the
	// installed watch app handles "resolved" (older builds show it as a bogus
	// approval).
	PushResolved bool
}

// ClientIPPolicy returns the policy the rate limiter uses to identify clients.
func (c *Config) ClientIPPolicy() ClientIPPolicy {
	return ClientIPPolicy{TrustedProxies: c.TrustedProxies, Header: c.ClientIPHeader}
}

// LoadConfig loads and validates configuration from environment variables.
func LoadConfig() (*Config, error) {
	listenAddr := os.Getenv("AW_LISTEN")
	if listenAddr == "" {
		listenAddr = DefaultListenAddr
	}

	hostToken := strings.TrimSpace(os.Getenv("AW_HOST_TOKEN"))
	if hostToken == "" {
		return nil, errors.New("AW_HOST_TOKEN is required")
	}
	if len(hostToken) != 64 {
		return nil, fmt.Errorf("AW_HOST_TOKEN must be exactly 64 hex characters (got length %d)", len(hostToken))
	}
	if _, err := hex.DecodeString(hostToken); err != nil {
		return nil, fmt.Errorf("AW_HOST_TOKEN must be valid hex: %w", err)
	}

	dataDir := os.Getenv("AW_DATA_DIR")
	if dataDir == "" {
		dataDir = DefaultDataDir
	}

	trustedProxies, err := ParseTrustedProxies(os.Getenv("AW_TRUSTED_PROXIES"))
	if err != nil {
		return nil, fmt.Errorf("AW_TRUSTED_PROXIES: %w", err)
	}
	clientIPHeader := strings.TrimSpace(os.Getenv("AW_CLIENT_IP_HEADER"))
	if clientIPHeader != "" {
		clientIPHeader = http.CanonicalHeaderKey(clientIPHeader)
		if len(trustedProxies) == 0 {
			return nil, fmt.Errorf("AW_CLIENT_IP_HEADER=%s is only honored from a trusted proxy: set AW_TRUSTED_PROXIES too", clientIPHeader)
		}
	}

	if _, set := os.LookupEnv("AW_TRUST_CF_IP"); set {
		slog.Warn("AW_TRUST_CF_IP is no longer supported and is ignored; " +
			"set AW_TRUSTED_PROXIES to your reverse proxy's CIDRs (the client IP then comes from X-Forwarded-For), " +
			"and AW_CLIENT_IP_HEADER=CF-Connecting-IP only if the origin accepts traffic from Cloudflare alone")
	}

	pushResolved := false
	if v := strings.TrimSpace(os.Getenv("AW_PUSH_RESOLVED")); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("AW_PUSH_RESOLVED must be a boolean (1/0, true/false), got %q", v)
		}
		pushResolved = b
	}

	cfg := &Config{
		ListenAddr:     listenAddr,
		HostToken:      hostToken,
		DataDir:        dataDir,
		FCMCredentials: os.Getenv("AW_FCM_CREDENTIALS"),
		NtfyURL:        os.Getenv("AW_NTFY_URL"),
		NtfyTopic:      os.Getenv("AW_NTFY_TOPIC"),
		NtfyToken:      os.Getenv("AW_NTFY_TOKEN"),
		TrustedProxies: trustedProxies,
		ClientIPHeader: clientIPHeader,
		PushResolved:   pushResolved,
	}

	return cfg, nil
}
