package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ServiceSpec is everything a service definition (LaunchAgent plist or
// systemd user unit) pins for `agent-watch-bridge run`.
type ServiceSpec struct {
	Binary     string `json:"binary"`
	ConfigPath string `json:"config_path"`
	SocketPath string `json:"socket_path"`
	StateDir   string `json:"state_dir"`
	LogPath    string `json:"log_path"` // "" for systemd (journald)
}

// ServiceInfo is what the service manager reports about the running service.
type ServiceInfo struct {
	Running bool
	PID     int
	Message string
}

// commandRunner runs an external command and returns its combined output.
// Tests inject a fake; nothing in tests may reach launchctl or systemctl.
type commandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// ServiceManager installs and controls the platform service.
type ServiceManager interface {
	// Kind is "launchd" or "systemd".
	Kind() string
	// DefinitionPath is where the plist or unit file lives.
	DefinitionPath() string
	// Render produces the definition for spec.
	Render(spec ServiceSpec) (string, error)
	// Parse reads a definition written by Render (or an older version).
	Parse(data []byte) (ServiceSpec, error)
	// Install writes the definition for spec and (re)loads the service.
	Install(ctx context.Context, spec ServiceSpec) error
	// Restart restarts the installed service without rewriting its definition.
	Restart(ctx context.Context) error
	// Stop stops (unloads) the service; the definition stays on disk.
	Stop(ctx context.Context) error
	// Status asks the service manager whether the service runs.
	Status(ctx context.Context) (ServiceInfo, error)
	// LogHint says where the service's output goes.
	LogHint(spec ServiceSpec) string
}

// newServiceManager returns the manager for this OS, rooted at home.
func newServiceManager(home string, run commandRunner) ServiceManager {
	switch runtime.GOOS {
	case "darwin":
		return &launchdManager{home: home, uid: os.Getuid(), run: run}
	case "linux":
		return &systemdManager{home: home, run: run}
	default:
		return unsupportedManager{}
	}
}

// errNotInstalled means no service definition exists yet.
var errNotInstalled = errors.New("the bridge service is not installed; run `agent-watch-bridge start`")

// readInstalled parses the installed definition. It returns (nil, nil) when
// there is none.
func readInstalled(m ServiceManager) (*ServiceSpec, error) {
	path := m.DefinitionPath()
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	spec, err := m.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &spec, nil
}

// writeFileAtomic writes data to path through a temp file in the same
// directory and chmods it to mode, whatever the old file's mode was.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after the rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// validateSpec rejects specs that would produce a service that cannot start.
func validateSpec(spec ServiceSpec) error {
	for name, p := range map[string]string{
		"binary": spec.Binary, "config": spec.ConfigPath,
		"socket": spec.SocketPath, "state dir": spec.StateDir,
	} {
		if p == "" || !filepath.IsAbs(p) {
			return fmt.Errorf("%s path %q must be absolute", name, p)
		}
	}
	for _, p := range []string{spec.Binary, spec.ConfigPath, spec.SocketPath, spec.StateDir, spec.LogPath} {
		if strings.IndexFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return fmt.Errorf("path %q contains a control character", p)
		}
	}
	if spec.LogPath != "" && !filepath.IsAbs(spec.LogPath) {
		return fmt.Errorf("log path %q must be absolute", spec.LogPath)
	}
	if !isExecutableFile(spec.Binary) {
		return fmt.Errorf("binary %s is missing or not executable", spec.Binary)
	}
	return nil
}

func isExecutableFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

type unsupportedManager struct{}

var errUnsupported = fmt.Errorf("service management is not supported on %s", runtime.GOOS)

func (unsupportedManager) Kind() string                               { return "" }
func (unsupportedManager) DefinitionPath() string                     { return "" }
func (unsupportedManager) Render(ServiceSpec) (string, error)         { return "", errUnsupported }
func (unsupportedManager) Parse([]byte) (ServiceSpec, error)          { return ServiceSpec{}, errUnsupported }
func (unsupportedManager) Install(context.Context, ServiceSpec) error { return errUnsupported }
func (unsupportedManager) Restart(context.Context) error              { return errUnsupported }
func (unsupportedManager) Stop(context.Context) error                 { return errUnsupported }
func (unsupportedManager) Status(context.Context) (ServiceInfo, error) {
	return ServiceInfo{}, errUnsupported
}
func (unsupportedManager) LogHint(ServiceSpec) string { return "" }
