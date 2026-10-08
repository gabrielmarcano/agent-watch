package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/gabrielmarcano/agent-monitor/deploy/launchd"
)

const launchdLabel = "com.gabrielmarcano.agent-watch-bridge"

// launchdManager installs the bridge as a LaunchAgent in the gui/<uid> domain.
type launchdManager struct {
	home  string
	uid   int
	run   commandRunner
	sleep func(time.Duration) // between bootstrap retries; nil means time.Sleep
}

func (m *launchdManager) Kind() string { return "launchd" }

func (m *launchdManager) DefinitionPath() string {
	return filepath.Join(m.home, "Library", "LaunchAgents", launchdLabel+".plist")
}

func (m *launchdManager) serviceTarget() string {
	return fmt.Sprintf("gui/%d/%s", m.uid, launchdLabel)
}

func (m *launchdManager) domainTarget() string {
	return fmt.Sprintf("gui/%d", m.uid)
}

func (m *launchdManager) LogHint(spec ServiceSpec) string { return spec.LogPath }

// Render fills the embedded plist template. Every value is XML-escaped: a
// path with "&" or "<" would otherwise produce a plist launchd rejects.
func (m *launchdManager) Render(spec ServiceSpec) (string, error) {
	if spec.LogPath == "" {
		return "", errors.New("launchd service needs a log path")
	}
	tmpl, err := template.New("launchd").Option("missingkey=error").Parse(launchd.Template)
	if err != nil {
		return "", fmt.Errorf("parse launchd template: %w", err)
	}
	data := struct {
		Binary, ConfigPath, SocketPath, StateDir, LogPath string
	}{
		Binary:     xmlEscape(spec.Binary),
		ConfigPath: xmlEscape(spec.ConfigPath),
		SocketPath: xmlEscape(spec.SocketPath),
		StateDir:   xmlEscape(spec.StateDir),
		LogPath:    xmlEscape(spec.LogPath),
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute launchd template: %w", err)
	}
	return buf.String(), nil
}

// Parse reads the values back from a LaunchAgent plist.
func (m *launchdManager) Parse(data []byte) (ServiceSpec, error) {
	v, err := decodePlist(data)
	if err != nil {
		return ServiceSpec{}, err
	}
	dict, ok := v.(map[string]any)
	if !ok {
		return ServiceSpec{}, errors.New("plist root is not a dict")
	}
	var spec ServiceSpec
	args, _ := dict["ProgramArguments"].([]any)
	if len(args) > 0 {
		spec.Binary, _ = args[0].(string)
	}
	for i := 1; i+1 < len(args); i++ {
		if a, _ := args[i].(string); a == "--config" {
			spec.ConfigPath, _ = args[i+1].(string)
		}
	}
	if env, ok := dict["EnvironmentVariables"].(map[string]any); ok {
		spec.SocketPath, _ = env["HERDR_SOCKET_PATH"].(string)
		spec.StateDir, _ = env["HERDR_PLUGIN_STATE_DIR"].(string)
	}
	spec.LogPath, _ = dict["StandardOutPath"].(string)
	if spec.Binary == "" {
		return ServiceSpec{}, errors.New("plist has no ProgramArguments")
	}
	return spec, nil
}

// Install writes the plist and reloads the agent (bootout + bootstrap).
func (m *launchdManager) Install(ctx context.Context, spec ServiceSpec) error {
	rendered, err := m.Render(spec)
	if err != nil {
		return err
	}
	path := m.DefinitionPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.MkdirAll(filepath.Dir(spec.LogPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(spec.LogPath), err)
	}
	if err := writeFileAtomic(path, []byte(rendered), 0o644); err != nil {
		return fmt.Errorf("write plist %s: %w", path, err)
	}

	// Unload a previous instance; "not loaded" is fine.
	_, _ = m.run(ctx, "launchctl", "bootout", m.serviceTarget())
	return m.bootstrap(ctx)
}

// bootstrap enables the agent (stop disabled it) and loads the installed
// plist. bootout is asynchronous, so a bootstrap right after it can fail
// briefly; retry for up to ~1.5 s.
func (m *launchdManager) bootstrap(ctx context.Context) error {
	sleep := m.sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	// A disabled agent cannot be bootstrapped; bootstrap reports it if this fails.
	_, _ = m.run(ctx, "launchctl", "enable", m.serviceTarget())
	var lastErr error
	var lastOut []byte
	for attempt := 0; attempt < 15; attempt++ {
		if attempt > 0 {
			sleep(100 * time.Millisecond)
		}
		out, err := m.run(ctx, "launchctl", "bootstrap", m.domainTarget(), m.DefinitionPath())
		if err == nil {
			return nil
		}
		lastErr, lastOut = err, out
		s := string(out)
		if !strings.Contains(s, "Input/output error") && !strings.Contains(s, "already bootstrapped") {
			break
		}
	}
	return fmt.Errorf("launchctl bootstrap: %w: %s", lastErr, strings.TrimSpace(string(lastOut)))
}

// Restart restarts the loaded agent in place (kickstart -k). An installed
// but unloaded agent (after `stop`) is loaded from its plist as is. The plist
// is never rewritten.
func (m *launchdManager) Restart(ctx context.Context) error {
	if _, err := os.Stat(m.DefinitionPath()); err != nil {
		return errNotInstalled
	}
	out, err := m.run(ctx, "launchctl", "kickstart", "-k", m.serviceTarget())
	if err == nil {
		return nil
	}
	if launchdNotLoaded(out) {
		return m.bootstrap(ctx)
	}
	return fmt.Errorf("launchctl kickstart: %w: %s", err, strings.TrimSpace(string(out)))
}

func launchdNotLoaded(out []byte) bool {
	s := string(out)
	return strings.Contains(s, "Could not find service") || strings.Contains(s, "No such process")
}

// Stop unloads the agent and disables it: launchd loads every plist in
// LaunchAgents at login, so without the disable a stopped bridge would start
// again at the next login. start enables it again.
func (m *launchdManager) Stop(ctx context.Context) error {
	out, err := m.run(ctx, "launchctl", "bootout", m.serviceTarget())
	if err != nil && !launchdNotLoaded(out) {
		return fmt.Errorf("launchctl bootout: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := m.run(ctx, "launchctl", "disable", m.serviceTarget()); err != nil {
		return fmt.Errorf("launchctl disable (the bridge would start again at login): %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

var launchdPIDRe = regexp.MustCompile(`(?m)^\s*pid = (\d+)`)

func (m *launchdManager) Status(ctx context.Context) (ServiceInfo, error) {
	out, err := m.run(ctx, "launchctl", "print", m.serviceTarget())
	if err != nil {
		return ServiceInfo{Message: "service not loaded"}, nil
	}
	s := string(out)
	info := ServiceInfo{Running: strings.Contains(s, "state = running"), Message: "loaded"}
	if info.Running {
		if mm := launchdPIDRe.FindStringSubmatch(s); len(mm) > 1 {
			info.PID, _ = strconv.Atoi(mm[1])
		}
	}
	return info, nil
}
