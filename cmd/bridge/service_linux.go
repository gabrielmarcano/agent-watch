//go:build linux

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
)

const linuxServiceName = "agent-watch-bridge"

const linuxSystemdTemplate = `[Unit]
Description=Agent Watch Bridge
After=network.target

[Service]
ExecStart={{.Binary}} run --config {{.ConfigPath}}
Environment=HERDR_SOCKET_PATH={{.SocketPath}}
Environment=HERDR_PLUGIN_STATE_DIR={{.StateDir}}
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`

type linuxServiceManager struct{}

func newServiceManager() ServiceManager {
	return &linuxServiceManager{}
}

func (m *linuxServiceManager) RenderTemplate(binary, configPath, socketPath, stateDir, logPath string) (string, error) {
	tmpl, err := template.New("systemd").Parse(linuxSystemdTemplate)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	data := struct {
		Binary     string
		ConfigPath string
		SocketPath string
		StateDir   string
		LogPath    string
	}{
		Binary:     binary,
		ConfigPath: configPath,
		SocketPath: socketPath,
		StateDir:   stateDir,
		LogPath:    logPath,
	}
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (m *linuxServiceManager) unitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user", linuxServiceName+".service"), nil
}

func (m *linuxServiceManager) Start(binary, configPath, socketPath, stateDir, logPath string) error {
	rendered, err := m.RenderTemplate(binary, configPath, socketPath, stateDir, logPath)
	if err != nil {
		return err
	}

	unitPath, err := m.unitPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return err
	}

	if err := os.WriteFile(unitPath, []byte(rendered), 0o644); err != nil {
		return err
	}

	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	cmd := exec.Command("systemctl", "--user", "enable", "--now", linuxServiceName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl enable --now: %w: %s", err, string(out))
	}
	return nil
}

func (m *linuxServiceManager) Stop() error {
	cmd := exec.Command("systemctl", "--user", "stop", linuxServiceName)
	_ = cmd.Run()
	cmd = exec.Command("systemctl", "--user", "disable", linuxServiceName)
	_ = cmd.Run()
	return nil
}

func (m *linuxServiceManager) Status() (ServiceInfo, error) {
	cmd := exec.Command("systemctl", "--user", "is-active", linuxServiceName)
	out, _ := cmd.CombinedOutput()
	running := strings.TrimSpace(string(out)) == "active"

	pid := 0
	if running {
		pidCmd := exec.Command("systemctl", "--user", "show", "--property", "MainPID", "--value", linuxServiceName)
		if pidOut, err := pidCmd.Output(); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(pidOut)))
		}
	}

	return ServiceInfo{
		Running: running,
		PID:     pid,
		Message: strings.TrimSpace(string(out)),
	}, nil
}
