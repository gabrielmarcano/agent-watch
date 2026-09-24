package relay

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
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
	TrustCFIP      bool
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

	trustCF := true
	if val := os.Getenv("AW_TRUST_CF_IP"); val != "" {
		lower := strings.ToLower(strings.TrimSpace(val))
		if lower == "false" || lower == "0" || lower == "no" {
			trustCF = false
		}
	}

	cfg := &Config{
		ListenAddr:     listenAddr,
		HostToken:      hostToken,
		DataDir:        dataDir,
		FCMCredentials: os.Getenv("AW_FCM_CREDENTIALS"),
		NtfyURL:        os.Getenv("AW_NTFY_URL"),
		NtfyTopic:      os.Getenv("AW_NTFY_TOPIC"),
		NtfyToken:      os.Getenv("AW_NTFY_TOKEN"),
		TrustCFIP:      trustCF,
	}

	return cfg, nil
}
