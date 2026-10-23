package bridge

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

// Keys configure reads from agent-watch.env, the configuration file
// shared by the Makefile, the relay deploy script and the Wear OS build
// (agent-watch.env.example documents every key).
const (
	EnvRelayDomain = "AW_RELAY_DOMAIN"
	EnvHostToken   = "AW_HOST_TOKEN"
)

var (
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// A host name (or IPv4 address) with an optional port: what goes between
	// wss:// and /v1/host. No scheme, path, user or quotes.
	relayDomainRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$`)
)

// ReadEnvFile parses agent-watch.env with the rules every reader of the file
// shares (the Makefile includes it, so they are make's): one KEY=value per
// line; '#' starts a comment anywhere, so a value cannot contain it; the value
// is trimmed and taken literally (no quotes, escapes or `export`); the last
// assignment of a key wins. Errors give the line number but never quote the
// line: the file holds the host token.
func ReadEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("env file %s not found; create it with `make config`", path)
		}
		return nil, fmt.Errorf("read env file %s: %w", path, err)
	}
	vals := map[string]string{}
	for i, line := range strings.Split(string(data), "\n") {
		if j := strings.IndexByte(line, '#'); j >= 0 {
			line = line[:j]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !envKeyRe.MatchString(key) {
			return nil, fmt.Errorf("env file %s, line %d: expected KEY=value", path, i+1)
		}
		vals[key] = strings.TrimSpace(value)
	}
	return vals, nil
}

// RelayURLFromDomain derives the bridge's relay URL from AW_RELAY_DOMAIN.
func RelayURLFromDomain(domain string) (string, error) {
	if !relayDomainRe.MatchString(domain) {
		return "", fmt.Errorf("%s=%q is not a bare host name such as relay.example.com (no scheme, path or quotes)", EnvRelayDomain, domain)
	}
	return "wss://" + domain + "/v1/host", nil
}

// RelayFromEnvFile fills the relay URL and host token that no flag gave from
// the env file at path. It also returns a warning if the file permissions are overly permissive.
func RelayFromEnvFile(path, relayURL, hostToken string) (string, string, string, error) {
	vals, err := ReadEnvFile(path)
	if err != nil {
		return "", "", "", err
	}
	var warn string
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
		warn = fmt.Sprintf("warning: %s can be read by other users (mode %04o); run `chmod 600 %s`", path, fi.Mode().Perm(), path)
	}
	if relayURL == "" {
		domain := vals[EnvRelayDomain]
		if domain == "" {
			return "", "", "", fmt.Errorf("%s is not set in %s", EnvRelayDomain, path)
		}
		if relayURL, err = RelayURLFromDomain(domain); err != nil {
			return "", "", "", fmt.Errorf("%s: %w", path, err)
		}
	}
	if hostToken == "" {
		hostToken = vals[EnvHostToken]
		if hostToken == "" {
			return "", "", "", fmt.Errorf("%s is empty in %s; generate it with `make config`", EnvHostToken, path)
		}
	}
	return relayURL, hostToken, warn, nil
}

// ConfigureOptions holds settings passed to configure the bridge.
type ConfigureOptions struct {
	RelayURL         string
	HostToken        string
	EnvFile          string
	HostName         string
	ClaudeConfigDirs []string
	SessionRoots     []string
	SessionKinds     []string
}

// UpdateConfig merges flags, agent-watch.env, and preserved settings from an existing
// config at targetPath, validates the result, writes it atomically, and returns
// the written config, notes on dropped settings, and any permission warning.
func UpdateConfig(targetPath string, opts ConfigureOptions) (*Config, []string, string, error) {
	url, token := opts.RelayURL, opts.HostToken
	var warn string
	if opts.EnvFile != "" {
		var err error
		if url, token, warn, err = RelayFromEnvFile(opts.EnvFile, url, token); err != nil {
			return nil, nil, "", err
		}
	}
	cfg := &Config{
		RelayURL:         NormalizeRelayURL(url),
		HostToken:        token,
		HostName:         opts.HostName,
		ClaudeConfigDirs: opts.ClaudeConfigDirs,
		SessionRoots:     opts.SessionRoots,
		SessionKinds:     opts.SessionKinds,
	}
	if err := ValidateConfig(cfg); err != nil {
		return nil, nil, "", err
	}

	old, _ := LoadConfig(targetPath)
	if old != nil {
		cfg.PushPresence = old.PushPresence
	}
	if err := SaveConfig(targetPath, cfg); err != nil {
		return nil, nil, "", err
	}
	notes := NotesDropped(old, cfg)
	return cfg, notes, warn, nil
}

// NotesDropped reports which optional settings from a previous config were
// not given again, to warn the user about dropped configuration.
func NotesDropped(old, cur *Config) []string {
	if old == nil {
		return nil
	}
	var notes []string
	if old.HostName != "" && cur.HostName == "" {
		notes = append(notes, fmt.Sprintf("note: dropped host_name %q from the previous config; pass --host-name to keep it", old.HostName))
	}
	if len(old.ClaudeConfigDirs) > 0 && len(cur.ClaudeConfigDirs) == 0 {
		notes = append(notes, fmt.Sprintf("note: dropped claude_config_dirs %q from the previous config; pass --claude-config-dir once per directory to keep them", old.ClaudeConfigDirs))
	}
	if len(old.SessionRoots) > 0 && len(cur.SessionRoots) == 0 {
		notes = append(notes, fmt.Sprintf("note: dropped session_roots %q from the previous config; pass --session-root once per folder to keep them (the phone can no longer start sessions)", old.SessionRoots))
	}
	if len(old.SessionKinds) > 0 && len(cur.SessionKinds) == 0 {
		notes = append(notes, fmt.Sprintf("note: dropped session_kinds %q from the previous config; pass --session-kind once per kind to keep them (the defaults apply)", old.SessionKinds))
	}
	return notes
}
