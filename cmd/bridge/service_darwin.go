//go:build darwin

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/gabrielmarcano/agent-monitor/deploy/launchd"
)

const darwinServiceName = "com.gabrielmarcano.agent-watch-bridge"

type darwinServiceManager struct{}

func newServiceManager() ServiceManager {
	return &darwinServiceManager{}
}

func (m *darwinServiceManager) plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", darwinServiceName+".plist"), nil
}

func (m *darwinServiceManager) RenderTemplate(binary, configPath, socketPath, stateDir, logPath string) (string, error) {
	tmpl, err := template.New("launchd").Parse(launchd.Template)
	if err != nil {
		return "", fmt.Errorf("parse launchd template: %w", err)
	}

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

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute launchd template: %w", err)
	}

	return buf.String(), nil
}

func (m *darwinServiceManager) Start(binary, configPath, socketPath, stateDir, logPath string) error {
	rendered, err := m.RenderTemplate(binary, configPath, socketPath, stateDir, logPath)
	if err != nil {
		return err
	}

	plistPath, err := m.plistPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(plistPath), err)
	}

	if err := os.WriteFile(plistPath, []byte(rendered), 0o644); err != nil {
		return fmt.Errorf("write plist %s: %w", plistPath, err)
	}

	uid := os.Getuid()
	target := fmt.Sprintf("gui/%d/%s", uid, darwinServiceName)
	domainTarget := fmt.Sprintf("gui/%d", uid)

	// Bootout previous instance if loaded (ignore errors)
	_ = exec.Command("launchctl", "bootout", target).Run()

	// launchctl bootout is asynchronous; retry bootstrap briefly to allow teardown to finish
	var lastErr error
	var lastOut []byte
	for attempt := 0; attempt < 15; attempt++ {
		if attempt > 0 {
			time.Sleep(100 * time.Millisecond)
		}
		cmd := exec.Command("launchctl", "bootstrap", domainTarget, plistPath)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = err
		lastOut = out
		if !strings.Contains(string(out), "Input/output error") && !strings.Contains(string(out), "already bootstrapped") {
			break
		}
	}

	return fmt.Errorf("launchctl bootstrap: %w: %s", lastErr, string(lastOut))
}

func (m *darwinServiceManager) Stop() error {
	uid := os.Getuid()
	target := fmt.Sprintf("gui/%d/%s", uid, darwinServiceName)
	cmd := exec.Command("launchctl", "bootout", target)
	if out, err := cmd.CombinedOutput(); err != nil {
		outStr := string(out)
		if strings.Contains(outStr, "Could not find service") || strings.Contains(outStr, "No such process") {
			return nil
		}
		return fmt.Errorf("launchctl bootout: %w: %s", err, outStr)
	}
	return nil
}

func (m *darwinServiceManager) Status() (ServiceInfo, error) {
	uid := os.Getuid()
	target := fmt.Sprintf("gui/%d/%s", uid, darwinServiceName)
	cmd := exec.Command("launchctl", "print", target)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ServiceInfo{Running: false, Message: "service not running or not loaded"}, nil
	}

	outStr := string(out)
	running := strings.Contains(outStr, "state = running")
	pid := 0
	if running {
		re := regexp.MustCompile(`pid = (\d+)`)
		if matches := re.FindStringSubmatch(outStr); len(matches) > 1 {
			pid, _ = strconv.Atoi(matches[1])
		}
	}

	return ServiceInfo{
		Running: running,
		PID:     pid,
		Message: outStr,
	}, nil
}
