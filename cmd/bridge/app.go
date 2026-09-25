package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const pluginID = "herdr-agent-watch"

// realRunner runs launchctl/systemctl. Tests replace it with one that fails.
var realRunner commandRunner = execRunner

// app holds the CLI's dependencies so every subcommand can be tested with a
// temp home, a fake service runner and no network.
type app struct {
	stdout, stderr io.Writer
	getenv         func(string) string
	home           string
	exe            string // this binary, absolute, symlinks resolved
	svc            ServiceManager
	http           *http.Client
	now            func() time.Time
	pidAlive       func(pid int) bool
}

func newApp() (*app, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	return &app{
		stdout:   os.Stdout,
		stderr:   os.Stderr,
		getenv:   os.Getenv,
		home:     home,
		exe:      currentExecutable(),
		svc:      newServiceManager(home, realRunner),
		http:     &http.Client{Timeout: 5 * time.Second},
		now:      time.Now,
		pidAlive: pidAlive,
	}, nil
}

func currentExecutable() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}
	return exe
}

// defaults are the values used when nothing else says otherwise. They match
// bridge.DefaultConfigPath, bridge.DefaultStateDir and herdr.NewClient.
func (a *app) defaults() ServiceSpec {
	spec := ServiceSpec{
		Binary:     a.exe,
		ConfigPath: filepath.Join(a.home, ".config", "herdr", "plugins", "config", pluginID, "config.toml"),
		SocketPath: filepath.Join(a.home, ".config", "herdr", "herdr.sock"),
		StateDir:   filepath.Join(a.home, ".local", "state", "agent-watch"),
	}
	if a.svc.Kind() == "launchd" {
		spec.LogPath = filepath.Join(a.home, "Library", "Logs", "agent-watch-bridge.log")
	}
	return spec
}

// inPluginAction reports whether herdr runs this process as a plugin action:
// herdr sets the plugin dirs only then (a pane shell has HERDR_SOCKET_PATH).
func (a *app) inPluginAction() bool {
	return a.getenv("HERDR_PLUGIN_CONFIG_DIR") != "" || a.getenv("HERDR_PLUGIN_STATE_DIR") != ""
}

// envSpec holds the values herdr's environment pins explicitly.
func (a *app) envSpec() ServiceSpec {
	var s ServiceSpec
	if d := a.getenv("HERDR_PLUGIN_CONFIG_DIR"); d != "" {
		s.ConfigPath = filepath.Join(d, "config.toml")
	}
	s.SocketPath = a.getenv("HERDR_SOCKET_PATH")
	s.StateDir = a.getenv("HERDR_PLUGIN_STATE_DIR")
	if a.inPluginAction() {
		// A plugin action runs the plugin's own ./bin/agent-watch-bridge.
		s.Binary = a.exe
	}
	return s
}

// resolution is a resolved spec plus what the installed definition said.
type resolution struct {
	spec       ServiceSpec
	installed  *ServiceSpec // nil when no definition exists (or it is unreadable)
	installErr error        // the definition exists but cannot be read
	notes      []string     // decisions worth telling the user
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func absPath(p string) string {
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func absSpec(s ServiceSpec) ServiceSpec {
	return ServiceSpec{
		Binary:     absPath(s.Binary),
		ConfigPath: absPath(s.ConfigPath),
		SocketPath: absPath(s.SocketPath),
		StateDir:   absPath(s.StateDir),
		LogPath:    absPath(s.LogPath),
	}
}

func (a *app) loadInstalled() (*ServiceSpec, error) {
	spec, err := readInstalled(a.svc)
	if err != nil {
		return nil, err
	}
	return spec, nil
}

// resolveForStart picks what `start` installs, per value: an explicit flag,
// else herdr's environment, else the installed definition, else the
// default. Running `start` from a terminal or the menu bar therefore keeps
// the definition herdr installed instead of rewriting it with the caller's
// environment. The binary follows the same order, where "environment" means
// running as the herdr plugin action; an installed binary that no longer
// exists is replaced by this one.
func (a *app) resolveForStart(flags ServiceSpec) resolution {
	var r resolution
	r.installed, r.installErr = a.loadInstalled()
	if r.installErr != nil {
		r.notes = append(r.notes, fmt.Sprintf("ignoring the unreadable service definition: %v", r.installErr))
	}
	var inst ServiceSpec
	if r.installed != nil {
		inst = *r.installed
	}
	if inst.Binary != "" && !isExecutableFile(inst.Binary) {
		r.notes = append(r.notes, fmt.Sprintf("the installed binary %s is missing; using %s", inst.Binary, a.exe))
		inst.Binary = ""
	}
	env, def := a.envSpec(), a.defaults()
	r.spec = absSpec(ServiceSpec{
		Binary:     firstNonEmpty(flags.Binary, env.Binary, inst.Binary, def.Binary),
		ConfigPath: firstNonEmpty(flags.ConfigPath, env.ConfigPath, inst.ConfigPath, def.ConfigPath),
		SocketPath: firstNonEmpty(flags.SocketPath, env.SocketPath, inst.SocketPath, def.SocketPath),
		StateDir:   firstNonEmpty(flags.StateDir, env.StateDir, inst.StateDir, def.StateDir),
		LogPath:    firstNonEmpty(flags.LogPath, inst.LogPath, def.LogPath),
	})
	return r
}

// resolveForRead picks the paths `status` and `pair` read: those of the
// installed service first (that is where the running bridge writes), then
// herdr's environment, then the defaults.
func (a *app) resolveForRead(flags ServiceSpec) resolution {
	var r resolution
	r.installed, r.installErr = a.loadInstalled()
	var inst ServiceSpec
	if r.installed != nil {
		inst = *r.installed
	}
	env, def := a.envSpec(), a.defaults()
	r.spec = absSpec(ServiceSpec{
		Binary:     firstNonEmpty(flags.Binary, inst.Binary, def.Binary),
		ConfigPath: firstNonEmpty(flags.ConfigPath, inst.ConfigPath, env.ConfigPath, def.ConfigPath),
		SocketPath: firstNonEmpty(flags.SocketPath, env.SocketPath, inst.SocketPath, def.SocketPath),
		StateDir:   firstNonEmpty(flags.StateDir, inst.StateDir, env.StateDir, def.StateDir),
		LogPath:    firstNonEmpty(flags.LogPath, inst.LogPath, def.LogPath),
	})
	return r
}

// errExitStatus1 makes main exit with status 1 without printing an error:
// the command already said why.
var errExitStatus1 = errors.New("exit status 1")
