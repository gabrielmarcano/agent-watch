package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

// Keys `configure --env-file` reads from agent-watch.env, the configuration
// file shared by the Makefile, the relay deploy script and the Wear OS build
// (agent-watch.env.example documents every key).
const (
	envRelayDomain = "AW_RELAY_DOMAIN"
	envHostToken   = "AW_HOST_TOKEN"
)

var (
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// A host name (or IPv4 address) with an optional port: what goes between
	// wss:// and /v1/host. No scheme, path, user or quotes.
	relayDomainRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$`)
)

// readEnvFile parses agent-watch.env with the rules every reader of the file
// shares (the Makefile includes it, so they are make's): one KEY=value per
// line; '#' starts a comment anywhere, so a value cannot contain it; the value
// is trimmed and taken literally (no quotes, escapes or `export`); the last
// assignment of a key wins. Errors give the line number but never quote the
// line: the file holds the host token.
func readEnvFile(path string) (map[string]string, error) {
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

// relayURLFromDomain derives the bridge's relay URL from AW_RELAY_DOMAIN.
func relayURLFromDomain(domain string) (string, error) {
	if !relayDomainRe.MatchString(domain) {
		return "", fmt.Errorf("%s=%q is not a bare host name such as relay.example.com (no scheme, path or quotes)", envRelayDomain, domain)
	}
	return "wss://" + domain + "/v1/host", nil
}

// relayFromEnvFile fills the relay URL and host token that no flag gave from
// the env file at path.
func (a *app) relayFromEnvFile(path, relayURL, hostToken string) (string, string, error) {
	vals, err := readEnvFile(path)
	if err != nil {
		return "", "", err
	}
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
		fmt.Fprintf(a.stderr, "warning: %s can be read by other users (mode %04o); run `chmod 600 %s`\n", path, fi.Mode().Perm(), path)
	}
	if relayURL == "" {
		domain := vals[envRelayDomain]
		if domain == "" {
			return "", "", fmt.Errorf("%s is not set in %s", envRelayDomain, path)
		}
		if relayURL, err = relayURLFromDomain(domain); err != nil {
			return "", "", fmt.Errorf("%s: %w", path, err)
		}
	}
	if hostToken == "" {
		hostToken = vals[envHostToken]
		if hostToken == "" {
			return "", "", fmt.Errorf("%s is empty in %s; generate it with `make config`", envHostToken, path)
		}
	}
	return relayURL, hostToken, nil
}
