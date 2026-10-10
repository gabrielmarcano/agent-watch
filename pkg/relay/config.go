package relay

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

// Default configuration values.
const (
	DefaultListenAddr = ":8080"
	DefaultDataDir    = "/var/lib/agent-watch-relay"
)

// DefaultPushPresenceIdle is AW_PUSH_PRESENCE_IDLE's default.
const DefaultPushPresenceIdle = 10 * time.Minute

// Config holds runtime configuration loaded from environment variables.
type Config struct {
	ListenAddr string
	// HostToken (AW_HOST_TOKEN) authenticates the host HostID. Optional once
	// a host is registered in store.json (`hosts add`); NewServer checks that.
	HostToken string
	// HostID (AW_HOST_ID, default DefaultHostID) is the id of HostToken's
	// host, and of the history a version 1 store.json held.
	HostID string
	// HostName (AW_HOST_NAME) is HostToken's host as the watch shows it.
	// Empty: HostID.
	HostName       string
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
	// PushPresenceIdle (AW_PUSH_PRESENCE_IDLE) is how long without input on
	// the host the owner counts as away; pushes wait while they are there.
	// 0 turns it off. Default DefaultPushPresenceIdle.
	PushPresenceIdle time.Duration

	// Version is the relay's own version with its commit, e.g.
	// "0.3.0 (c8aa72e)" (set by cmd/relay, not from the environment). Only
	// an authenticated bridge learns it (RelayVersionHeader).
	Version string
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

	// Optional here: whether a host can connect at all is known only once
	// store.json is read (NewServer).
	hostToken := strings.TrimSpace(os.Getenv("AW_HOST_TOKEN"))
	if hostToken != "" {
		if len(hostToken) != 64 {
			return nil, fmt.Errorf("AW_HOST_TOKEN must be exactly 64 hex characters (got length %d)", len(hostToken))
		}
		if _, err := hex.DecodeString(hostToken); err != nil {
			return nil, fmt.Errorf("AW_HOST_TOKEN must be valid hex: %w", err)
		}
	}

	hostID := strings.TrimSpace(os.Getenv("AW_HOST_ID"))
	if hostID == "" {
		hostID = DefaultHostID
	}
	if !ValidHostID(hostID) {
		return nil, fmt.Errorf("AW_HOST_ID %q: %w", hostID, ErrInvalidHostID)
	}
	hostName, err := NormalizeHostName(hostID, os.Getenv("AW_HOST_NAME"))
	if err != nil {
		return nil, fmt.Errorf("AW_HOST_NAME: %w", err)
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

	presenceIdle := DefaultPushPresenceIdle
	if v := strings.TrimSpace(os.Getenv("AW_PUSH_PRESENCE_IDLE")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return nil, fmt.Errorf("AW_PUSH_PRESENCE_IDLE must be a duration such as 10m, or 0 to turn it off, got %q", v)
		}
		presenceIdle = d
	}

	cfg := &Config{
		ListenAddr:       listenAddr,
		HostToken:        hostToken,
		HostID:           hostID,
		HostName:         hostName,
		DataDir:          dataDir,
		FCMCredentials:   os.Getenv("AW_FCM_CREDENTIALS"),
		NtfyURL:          os.Getenv("AW_NTFY_URL"),
		NtfyTopic:        os.Getenv("AW_NTFY_TOPIC"),
		NtfyToken:        os.Getenv("AW_NTFY_TOKEN"),
		TrustedProxies:   trustedProxies,
		ClientIPHeader:   clientIPHeader,
		PushResolved:     pushResolved,
		PushPresenceIdle: presenceIdle,
	}

	return cfg, nil
}
