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
	"github.com/gabrielmarcano/agent-monitor/pkg/relayclient"
)

var hexTokenRe = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// Config defines settings for the agent-watch-bridge host daemon.
type Config struct {
	RelayURL         string   `toml:"relay_url"`
	HostToken        string   `toml:"host_token"`
	HostName         string   `toml:"host_name"`
	ClaudeConfigDirs []string `toml:"claude_config_dirs"`
	// PushPresence turns on "Only Notify When Away": the bridge reports the
	// host's presence so the relay holds back pushes while the owner is
	// there (contracts.md §4.3). Off unless the owner turns it on.
	PushPresence bool `toml:"push_presence,omitempty"`
	// SessionRoots are the absolute folders in which the phone may start new
	// agent sessions. Empty: the feature is off on this host.
	SessionRoots []string `toml:"session_roots,omitempty"`
	// SessionKinds are the agent kinds the phone may start there. Empty:
	// DefaultSessionKinds.
	SessionKinds []string `toml:"session_kinds,omitempty"`
}

// StatusFile is written by the bridge daemon every 5 s to report health.
//
// Every key is always written (no omitempty): the first menu bar build
// decodes the original six unconditionally. A PID of 0 means the bridge is
// not running: it exited cleanly (or failed to start, see LastError).
type StatusFile struct {
	PID            int    `json:"pid"`
	RelayConnected bool   `json:"relay_connected"`
	HerdrOnline    bool   `json:"herdr_online"`
	Agents         int    `json:"agents"`
	Blocked        int    `json:"blocked"`     // agents whose status is blocked
	LastError      string `json:"last_error"`  // relay_error and herdr_error joined; "" when healthy
	RelayError     string `json:"relay_error"` // why the relay is not connected
	HerdrError     string `json:"herdr_error"` // why herdr is offline
	Version        string `json:"version"`     // the running bridge, e.g. "0.3.0 (c8aa72e)"
	// RelayVersion is the relay's version from the last successful handshake
	// (kept while disconnected); "" before the first connection or from a
	// relay that does not report one.
	RelayVersion string `json:"relay_version"`
	UpdatedAt    string `json:"updated_at"`
}

// PluginID is herdr's identifier for the agent-watch bridge plugin.
const PluginID = "herdr-agent-watch"

// DefaultConfigPathFor computes the config path given an optional home directory
// and an environment lookup function.
func DefaultConfigPathFor(home string, getenv func(string) string) string {
	if getenv != nil {
		if p := getenv("HERDR_PLUGIN_CONFIG_DIR"); p != "" {
			return filepath.Join(p, "config.toml")
		}
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home != "" {
		return filepath.Join(home, ".config", "herdr", "plugins", "config", PluginID, "config.toml")
	}
	return "config.toml"
}

// DefaultStateDirFor computes the state directory given an optional home directory
// and an environment lookup function.
func DefaultStateDirFor(home string, getenv func(string) string) string {
	if getenv != nil {
		if p := getenv("HERDR_PLUGIN_STATE_DIR"); p != "" {
			return p
		}
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home != "" {
		return filepath.Join(home, ".local", "state", "agent-watch")
	}
	return "."
}

// DefaultSocketPathFor computes the herdr socket path given an optional home directory
// and an environment lookup function.
func DefaultSocketPathFor(home string, getenv func(string) string) string {
	if getenv != nil {
		if p := getenv("HERDR_SOCKET_PATH"); p != "" {
			return p
		}
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home != "" {
		return filepath.Join(home, ".config", "herdr", "herdr.sock")
	}
	return "herdr.sock"
}

// DefaultConfigPath returns the config file path according to herdr conventions:
// $HERDR_PLUGIN_CONFIG_DIR/config.toml, or ~/.config/herdr/plugins/config/herdr-agent-watch/config.toml.
func DefaultConfigPath() string {
	return DefaultConfigPathFor("", os.Getenv)
}

// DefaultStateDir returns the directory where status.json should be written:
// $HERDR_PLUGIN_STATE_DIR, or ~/.local/state/agent-watch.
func DefaultStateDir() string {
	return DefaultStateDirFor("", os.Getenv)
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

	for _, root := range cfg.SessionRoots {
		if !filepath.IsAbs(root) {
			return fmt.Errorf("session_roots must be absolute paths: %q", root)
		}
	}
	for _, kind := range cfg.SessionKinds {
		if strings.TrimSpace(kind) == "" {
			return errors.New("session_kinds must not contain blank values")
		}
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

// CheckConfig loads and validates the config at path. Its errors are safe to
// show and to store in status.json: they never quote the file's contents
// (a TOML parse error can echo the host_token line).
func CheckConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("config %s not found; run `agent-watch-bridge configure --relay-url URL --host-token TOKEN`", path)
		}
		return nil, fmt.Errorf("config %s cannot be read: %w", path, err)
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		var perr toml.ParseError
		if errors.As(err, &perr) {
			return nil, fmt.Errorf("config %s is not valid TOML (line %d); re-run configure", path, perr.Position.Line)
		}
		return nil, fmt.Errorf("config %s is not valid TOML; re-run configure", path)
	}
	if err := ValidateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("config %s is invalid: %w", path, err)
	}
	return &cfg, nil
}

// WriteFileAtomic writes data to path atomically by creating a temp file in
// the destination directory, writing the contents, setting perm, and renaming.
// If dirPerm is non-zero, the parent directory is created if it does not exist.
func WriteFileAtomic(path string, data []byte, perm os.FileMode, dirPerm os.FileMode) error {
	dir := filepath.Dir(path)
	if dirPerm != 0 {
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
		if err := os.Chmod(dir, dirPerm); err != nil {
			return fmt.Errorf("chmod dir %s: %w", dir, err)
		}
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	name := tmp.Name()
	defer os.Remove(name)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}

// SaveConfig writes a Config to path with mode 0600 in a directory with mode
// 0700. Both modes are enforced even when the file or directory already
// exists (os.WriteFile and os.MkdirAll only apply a mode on creation). The
// write is atomic: a temp file in the same directory, then a rename.
func SaveConfig(path string, cfg *Config) error {
	var buf strings.Builder
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := WriteFileAtomic(path, []byte(buf.String()), 0o600, 0o700); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

// HTTPSBase returns the HTTP/HTTPS base URL derived from a WebSocket relay URL.
// E.g. "wss://relay.example.com/v1/host" -> "https://relay.example.com".
func HTTPSBase(relayURL string) (string, error) {
	return relayclient.HTTPSBase(relayURL)
}

// NormalizeRelayURL ensures the WebSocket URL has the /v1/host path.
// E.g. "wss://relay.example.com" -> "wss://relay.example.com/v1/host".
func NormalizeRelayURL(raw string) string {
	return relayclient.NormalizeRelayURL(raw)
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

	if err := WriteFileAtomic(path, data, 0o644, 0o700); err != nil {
		return fmt.Errorf("write status %s: %w", path, err)
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
