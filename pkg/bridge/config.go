package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

var hexTokenRe = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// Config defines settings for the agent-watch-bridge host daemon.
type Config struct {
	RelayURL         string   `toml:"relay_url"`
	HostToken        string   `toml:"host_token"`
	HostName         string   `toml:"host_name"`
	ClaudeConfigDirs []string `toml:"claude_config_dirs"`
}

// StatusFile is written by the bridge daemon periodically to report health and status.
type StatusFile struct {
	PID            int    `json:"pid"`
	RelayConnected bool   `json:"relay_connected"`
	HerdrOnline    bool   `json:"herdr_online"`
	Agents         int    `json:"agents"`
	LastError      string `json:"last_error"`
	UpdatedAt      string `json:"updated_at"`
}

// DefaultConfigPath returns the config file path according to herdr conventions:
// $HERDR_PLUGIN_CONFIG_DIR/config.toml, or ~/.config/herdr/plugins/config/herdr-agent-watch/config.toml.
func DefaultConfigPath() string {
	if p := os.Getenv("HERDR_PLUGIN_CONFIG_DIR"); p != "" {
		return filepath.Join(p, "config.toml")
	}
	home, err := os.UserHomeDir()
	if err == nil {
		return filepath.Join(home, ".config", "herdr", "plugins", "config", "herdr-agent-watch", "config.toml")
	}
	return "config.toml"
}

// DefaultStateDir returns the directory where status.json should be written:
// $HERDR_PLUGIN_STATE_DIR, or ~/.local/state/agent-watch.
func DefaultStateDir() string {
	if p := os.Getenv("HERDR_PLUGIN_STATE_DIR"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err == nil {
		return filepath.Join(home, ".local", "state", "agent-watch")
	}
	return "."
}

// ValidateConfig checks that RelayURL and HostToken meet security and protocol requirements.
func ValidateConfig(cfg *Config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	if cfg.RelayURL == "" {
		return errors.New("relay_url is required")
	}

	u, err := url.Parse(cfg.RelayURL)
	if err != nil {
		return fmt.Errorf("invalid relay_url: %w", err)
	}

	isLocal := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"
	if u.Scheme == "ws" {
		if !isLocal {
			return errors.New("relay_url with ws:// scheme is only permitted for localhost/127.0.0.1")
		}
	} else if u.Scheme != "wss" {
		return errors.New("relay_url scheme must be wss:// (or ws:// for localhost)")
	}

	if !hexTokenRe.MatchString(cfg.HostToken) {
		return errors.New("host_token must be exactly 64 hexadecimal characters")
	}

	return nil
}

// LoadConfig reads and parses a Config from the given path.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	return &cfg, nil
}

// SaveConfig writes a Config to path with mode 0600, creating parent directories with mode 0700.
func SaveConfig(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir %s: %w", dir, err)
	}

	var buf strings.Builder
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	if err := os.WriteFile(path, []byte(buf.String()), 0o600); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}

	return nil
}

// HTTPSBase returns the HTTP/HTTPS base URL derived from a WebSocket relay URL.
// E.g. "wss://relay.example.com/v1/host" -> "https://relay.example.com".
func HTTPSBase(relayURL string) (string, error) {
	u, err := url.Parse(relayURL)
	if err != nil {
		return "", fmt.Errorf("invalid relay_url: %w", err)
	}

	var scheme string
	switch u.Scheme {
	case "wss":
		scheme = "https"
	case "ws":
		scheme = "http"
	default:
		return "", fmt.Errorf("unsupported scheme %q; expected ws or wss", u.Scheme)
	}

	return fmt.Sprintf("%s://%s", scheme, u.Host), nil
}

// NormalizeRelayURL ensures the WebSocket URL has the /v1/host path.
// E.g. "wss://relay.example.com" -> "wss://relay.example.com/v1/host".
func NormalizeRelayURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/v1/host"
	}
	return u.String()
}

// WriteStatus writes the status file atomically with 0644 mode.
func WriteStatus(path string, s StatusFile) error {
	if s.UpdatedAt == "" {
		s.UpdatedAt = model.Now()
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode status: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state dir %s: %w", dir, err)
	}

	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp status: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename status: %w", err)
	}

	return nil
}

// ReadStatus reads and decodes the status file.
func ReadStatus(path string) (StatusFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return StatusFile{}, fmt.Errorf("read status %s: %w", path, err)
	}

	var s StatusFile
	if err := json.Unmarshal(data, &s); err != nil {
		return StatusFile{}, fmt.Errorf("decode status: %w", err)
	}

	return s, nil
}
