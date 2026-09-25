package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const systemdUnit = "agent-watch-bridge"

// systemdManager installs the bridge as a systemd --user unit.
type systemdManager struct {
	home string
	run  commandRunner
}

func (m *systemdManager) Kind() string { return "systemd" }

func (m *systemdManager) DefinitionPath() string {
	return filepath.Join(m.home, ".config", "systemd", "user", systemdUnit+".service")
}

func (m *systemdManager) LogHint(ServiceSpec) string {
	return "journalctl --user -u " + systemdUnit
}

// Render writes the unit. Every path is quoted and escaped for systemd:
// quotes and backslashes, "%" (specifiers) and, in ExecStart, "$" (variable
// expansion). Output goes to the journal.
func (m *systemdManager) Render(spec ServiceSpec) (string, error) {
	for _, v := range []string{spec.Binary, spec.ConfigPath, spec.SocketPath, spec.StateDir} {
		if strings.ContainsAny(v, "\n\r") {
			return "", fmt.Errorf("path %q contains a line break", v)
		}
	}
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=Agent Watch Bridge\nAfter=network.target\n\n[Service]\n")
	fmt.Fprintf(&b, "ExecStart=%s run --config %s\n", systemdQuote(spec.Binary, true), systemdQuote(spec.ConfigPath, true))
	fmt.Fprintf(&b, "Environment=%s\n", systemdQuote("HERDR_SOCKET_PATH="+spec.SocketPath, false))
	fmt.Fprintf(&b, "Environment=%s\n", systemdQuote("HERDR_PLUGIN_STATE_DIR="+spec.StateDir, false))
	b.WriteString("Restart=always\nRestartSec=5\n\n[Install]\nWantedBy=default.target\n")
	return b.String(), nil
}

// systemdQuote returns s as one double-quoted systemd word.
func systemdQuote(s string, exec bool) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "%", "%%")
	if exec {
		s = strings.ReplaceAll(s, "$", "$$")
	}
	return `"` + s + `"`
}

// systemdWords splits a systemd value into words, honouring double and
// single quotes and backslash escapes, then undoes "%%" (and "$$" for
// ExecStart).
func systemdWords(line string, exec bool) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	var quote rune
	escaped := false
	for _, r := range line {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped, inWord = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, inWord = r, true
		case r == ' ' || r == '\t':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 || escaped {
		return nil, fmt.Errorf("unterminated quote or escape in %q", line)
	}
	if inWord {
		words = append(words, cur.String())
	}
	for i, w := range words {
		w = strings.ReplaceAll(w, "%%", "%")
		if exec {
			w = strings.ReplaceAll(w, "$$", "$")
		}
		words[i] = w
	}
	return words, nil
}

// Parse reads the values back from a unit written by Render (quoted) or by
// older versions (unquoted).
func (m *systemdManager) Parse(data []byte) (ServiceSpec, error) {
	var spec ServiceSpec
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "ExecStart="):
			words, err := systemdWords(strings.TrimPrefix(line, "ExecStart="), true)
			if err != nil {
				return ServiceSpec{}, err
			}
			if len(words) > 0 {
				spec.Binary = words[0]
			}
			for i := 1; i+1 < len(words); i++ {
				if words[i] == "--config" {
					spec.ConfigPath = words[i+1]
				}
			}
		case strings.HasPrefix(line, "Environment="):
			words, err := systemdWords(strings.TrimPrefix(line, "Environment="), false)
			if err != nil {
				return ServiceSpec{}, err
			}
			for _, w := range words {
				k, v, ok := strings.Cut(w, "=")
				if !ok {
					continue
				}
				switch k {
				case "HERDR_SOCKET_PATH":
					spec.SocketPath = v
				case "HERDR_PLUGIN_STATE_DIR":
					spec.StateDir = v
				}
			}
		}
	}
	if spec.Binary == "" {
		return ServiceSpec{}, errors.New("unit has no ExecStart")
	}
	return spec, nil
}

// Install writes the unit, reloads systemd, enables it and restarts it (a
// plain `enable --now` would keep an already running old binary).
func (m *systemdManager) Install(ctx context.Context, spec ServiceSpec) error {
	rendered, err := m.Render(spec)
	if err != nil {
		return err
	}
	path := m.DefinitionPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(path, []byte(rendered), 0o644); err != nil {
		return fmt.Errorf("write unit %s: %w", path, err)
	}
	for _, args := range [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", systemdUnit},
		{"--user", "restart", systemdUnit},
	} {
		if out, err := m.run(ctx, "systemctl", args...); err != nil {
			return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// Restart restarts the installed unit without rewriting it.
func (m *systemdManager) Restart(ctx context.Context) error {
	if _, err := os.Stat(m.DefinitionPath()); err != nil {
		return errNotInstalled
	}
	if out, err := m.run(ctx, "systemctl", "--user", "restart", systemdUnit); err != nil {
		return fmt.Errorf("systemctl --user restart: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *systemdManager) Stop(ctx context.Context) error {
	if out, err := m.run(ctx, "systemctl", "--user", "stop", systemdUnit); err != nil && !strings.Contains(string(out), "not loaded") {
		return fmt.Errorf("systemctl --user stop: %w: %s", err, strings.TrimSpace(string(out)))
	}
	_, _ = m.run(ctx, "systemctl", "--user", "disable", systemdUnit)
	return nil
}

func (m *systemdManager) Status(ctx context.Context) (ServiceInfo, error) {
	out, _ := m.run(ctx, "systemctl", "--user", "is-active", systemdUnit)
	state := strings.TrimSpace(string(out))
	info := ServiceInfo{Running: state == "active", Message: state}
	if info.Running {
		if pidOut, err := m.run(ctx, "systemctl", "--user", "show", "--property", "MainPID", "--value", systemdUnit); err == nil {
			info.PID, _ = strconv.Atoi(strings.TrimSpace(string(pidOut)))
		}
	}
	return info, nil
}
