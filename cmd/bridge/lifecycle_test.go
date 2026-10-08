package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The menu bar's worst bug: `start` run from a terminal or from the bar
// rewrote the LaunchAgent with its own environment (wrong state dir, default
// socket, whatever binary it was run from). It must reuse the installed values.
func TestStart_FromOutsideHerdrKeepsInstalledDefinition(t *testing.T) {
	ta := newTestApp(t, "launchd")
	pluginBin := writeExecutable(t, filepath.Join(ta.home, "plugin", "bin", "agent-watch-bridge"))
	cfg := filepath.Join(ta.home, ".config", "herdr", "plugins", "config", "herdr-agent-watch", "config.toml")
	writeConfig(t, cfg, "wss://relay.example.com")
	installedSpecV := ServiceSpec{
		Binary:     pluginBin,
		ConfigPath: cfg,
		SocketPath: filepath.Join(ta.home, ".config", "herdr", "herdr.sock"),
		StateDir:   filepath.Join(ta.home, ".local", "state", "herdr", "plugins", "herdr-agent-watch"),
		LogPath:    filepath.Join(ta.home, "Library", "Logs", "agent-watch-bridge.log"),
	}
	install(t, ta, installedSpecV)

	if err := ta.cmdStart(nil); err != nil {
		t.Fatalf("start: %v\n%s", err, ta.errOut)
	}
	if got := installedSpec(t, ta); got != installedSpecV {
		t.Errorf("start rewrote the definition:\n got %+v\nwant %+v", got, installedSpecV)
	}
	wantCalls := []string{
		"launchctl bootout gui/501/com.gabrielmarcano.agent-watch-bridge",
		"launchctl enable gui/501/com.gabrielmarcano.agent-watch-bridge",
		"launchctl bootstrap gui/501 " + ta.svc.DefinitionPath(),
	}
	if got := ta.runner.Calls(); strings.Join(got, "\n") != strings.Join(wantCalls, "\n") {
		t.Errorf("launchctl calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantCalls, "\n"))
	}
	if !strings.HasPrefix(ta.out.String(), "started\n") {
		t.Errorf("output = %q, want it to start with \"started\"", ta.out)
	}
}

// Run as the herdr plugin action, start takes herdr's directories and the
// plugin's binary even over an older definition.
func TestStart_FromHerdrPluginActionUsesItsEnvironment(t *testing.T) {
	ta := newTestApp(t, "launchd")
	plugCfgDir := filepath.Join(ta.home, "herdr-config")
	writeConfig(t, filepath.Join(plugCfgDir, "config.toml"), "wss://relay.example.com")
	install(t, ta, ServiceSpec{
		Binary:     "/gone/agent-watch-bridge",
		ConfigPath: filepath.Join(ta.home, "old", "config.toml"),
		SocketPath: "/old/herdr.sock",
		StateDir:   filepath.Join(ta.home, ".local", "state", "agent-watch"),
		LogPath:    filepath.Join(ta.home, "Library", "Logs", "agent-watch-bridge.log"),
	})
	ta.env = map[string]string{
		"HERDR_PLUGIN_CONFIG_DIR": plugCfgDir,
		"HERDR_PLUGIN_STATE_DIR":  filepath.Join(ta.home, "herdr-state"),
		"HERDR_SOCKET_PATH":       filepath.Join(ta.home, "herdr.sock"),
	}

	if err := ta.cmdStart(nil); err != nil {
		t.Fatalf("start: %v\n%s", err, ta.errOut)
	}
	want := ServiceSpec{
		Binary:     ta.exe,
		ConfigPath: filepath.Join(plugCfgDir, "config.toml"),
		SocketPath: filepath.Join(ta.home, "herdr.sock"),
		StateDir:   filepath.Join(ta.home, "herdr-state"),
		LogPath:    filepath.Join(ta.home, "Library", "Logs", "agent-watch-bridge.log"),
	}
	if got := installedSpec(t, ta); got != want {
		t.Errorf("definition:\n got %+v\nwant %+v", got, want)
	}
}

// A bad or missing config must not produce a crash-looping service: start
// refuses before writing or loading anything.
func TestStart_RefusesInvalidConfig(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T, ta *testApp){
		"missing": func(t *testing.T, ta *testApp) {},
		"invalid": func(t *testing.T, ta *testApp) {
			path := ta.defaults().ConfigPath
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("relay_url = \"http://x\"\nhost_token = \"short\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			ta := newTestApp(t, "launchd")
			setup(t, ta)
			err := ta.cmdStart(nil)
			if err == nil || !strings.Contains(err.Error(), "config") {
				t.Fatalf("start with a %s config: err = %v, want a config error", name, err)
			}
			if _, statErr := os.Stat(ta.svc.DefinitionPath()); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("a definition was written despite the bad config")
			}
			if calls := ta.runner.Calls(); len(calls) != 0 {
				t.Errorf("launchctl was called: %v", calls)
			}
		})
	}
}

func TestStart_FreshInstallUsesDefaults(t *testing.T) {
	ta := newTestApp(t, "launchd")
	writeConfig(t, ta.defaults().ConfigPath, "wss://relay.example.com")
	if err := ta.cmdStart(nil); err != nil {
		t.Fatalf("start: %v", err)
	}
	want := ServiceSpec{
		Binary:     ta.exe,
		ConfigPath: filepath.Join(ta.home, ".config", "herdr", "plugins", "config", "herdr-agent-watch", "config.toml"),
		SocketPath: filepath.Join(ta.home, ".config", "herdr", "herdr.sock"),
		StateDir:   filepath.Join(ta.home, ".local", "state", "agent-watch"),
		LogPath:    filepath.Join(ta.home, "Library", "Logs", "agent-watch-bridge.log"),
	}
	if got := installedSpec(t, ta); got != want {
		t.Errorf("definition:\n got %+v\nwant %+v", got, want)
	}
	if fi, err := os.Stat(ta.svc.DefinitionPath()); err != nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("plist mode = %v (%v), want 0644", fi.Mode().Perm(), err)
	}
}

func installForRestart(t *testing.T, ta *testApp) []byte {
	t.Helper()
	cfg := filepath.Join(ta.home, "cfg", "config.toml")
	writeConfig(t, cfg, "wss://relay.example.com")
	install(t, ta, ServiceSpec{
		Binary:     writeExecutable(t, filepath.Join(ta.home, "plugin", "agent-watch-bridge")),
		ConfigPath: cfg,
		SocketPath: "/herdr/herdr.sock",
		StateDir:   "/herdr/state",
		LogPath:    filepath.Join(ta.home, "bridge.log"),
	})
	data, err := os.ReadFile(ta.svc.DefinitionPath())
	if err != nil {
		t.Fatal(err)
	}
	// Make any rewrite visible through the mtime as well.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(ta.svc.DefinitionPath(), old, old); err != nil {
		t.Fatal(err)
	}
	return data
}

func assertDefinitionUntouched(t *testing.T, ta *testApp, before []byte) {
	t.Helper()
	after, err := os.ReadFile(ta.svc.DefinitionPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("restart rewrote the definition")
	}
	if fi, err := os.Stat(ta.svc.DefinitionPath()); err == nil && time.Since(fi.ModTime()) < 30*time.Minute {
		t.Error("restart touched the definition file")
	}
}

func TestRestart_KickstartsWithoutRewriting(t *testing.T) {
	ta := newTestApp(t, "launchd")
	before := installForRestart(t, ta)
	if err := ta.cmdRestart(nil); err != nil {
		t.Fatalf("restart: %v", err)
	}
	want := "launchctl kickstart -k gui/501/com.gabrielmarcano.agent-watch-bridge"
	if calls := ta.runner.Calls(); len(calls) != 1 || calls[0] != want {
		t.Errorf("calls = %v, want [%s]", calls, want)
	}
	assertDefinitionUntouched(t, ta, before)
	if !strings.HasPrefix(ta.out.String(), "restarted\n") {
		t.Errorf("output = %q", ta.out)
	}
}

// After `stop` the agent is unloaded and disabled: restart enables it and
// loads the existing plist as is.
func TestRestart_LoadsStoppedServiceWithoutRewriting(t *testing.T) {
	ta := newTestApp(t, "launchd")
	before := installForRestart(t, ta)
	ta.runner.respond = func(line string) ([]byte, error) {
		if strings.Contains(line, "kickstart") {
			return []byte("Could not find service \"com.gabrielmarcano.agent-watch-bridge\" in domain for user gui: 501\n"), errors.New("exit status 113")
		}
		return nil, nil
	}
	if err := ta.cmdRestart(nil); err != nil {
		t.Fatalf("restart: %v", err)
	}
	calls := ta.runner.Calls()
	if len(calls) != 3 || calls[1] != "launchctl enable gui/501/com.gabrielmarcano.agent-watch-bridge" ||
		calls[2] != "launchctl bootstrap gui/501 "+ta.svc.DefinitionPath() {
		t.Errorf("calls = %v, want kickstart, enable, then bootstrap of the existing plist", calls)
	}
	assertDefinitionUntouched(t, ta, before)
}

func TestRestart_NotInstalled(t *testing.T) {
	ta := newTestApp(t, "launchd")
	err := ta.cmdRestart(nil)
	if !errors.Is(err, errNotInstalled) {
		t.Errorf("err = %v, want errNotInstalled", err)
	}
	if calls := ta.runner.Calls(); len(calls) != 0 {
		t.Errorf("calls = %v, want none", calls)
	}
}

func TestRestart_RefusesInvalidConfig(t *testing.T) {
	ta := newTestApp(t, "launchd")
	installForRestart(t, ta)
	spec := installedSpec(t, ta)
	if err := os.Remove(spec.ConfigPath); err != nil {
		t.Fatal(err)
	}
	if err := ta.cmdRestart(nil); err == nil || !strings.Contains(err.Error(), "config") {
		t.Errorf("err = %v, want a config error", err)
	}
	if calls := ta.runner.Calls(); len(calls) != 0 {
		t.Errorf("calls = %v, want none", calls)
	}
}

func TestSystemd_StartAndRestart(t *testing.T) {
	ta := newTestApp(t, "systemd")
	writeConfig(t, ta.defaults().ConfigPath, "wss://relay.example.com")
	if err := ta.cmdStart(nil); err != nil {
		t.Fatalf("start: %v", err)
	}
	wantStart := []string{
		"systemctl --user daemon-reload",
		"systemctl --user enable agent-watch-bridge",
		"systemctl --user restart agent-watch-bridge",
	}
	if got := ta.runner.Calls(); strings.Join(got, "\n") != strings.Join(wantStart, "\n") {
		t.Errorf("start calls = %v, want %v", got, wantStart)
	}
	before, _ := os.ReadFile(ta.svc.DefinitionPath())
	if err := ta.cmdRestart(nil); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if got := ta.runner.Calls(); got[len(got)-1] != "systemctl --user restart agent-watch-bridge" || len(got) != 4 {
		t.Errorf("restart calls = %v", got)
	}
	after, _ := os.ReadFile(ta.svc.DefinitionPath())
	if !bytes.Equal(before, after) {
		t.Error("restart rewrote the unit")
	}
}

// stop unloads the agent and disables it, so launchd does not load its plist
// again at the next login; an agent already unloaded is still disabled.
func TestStop(t *testing.T) {
	ta := newTestApp(t, "launchd")
	ta.runner.respond = func(line string) ([]byte, error) {
		if strings.Contains(line, "bootout") {
			return []byte("Boot-out failed: 3: No such process\n"), errors.New("exit status 3")
		}
		return nil, nil
	}
	if err := ta.cmdStop(nil); err != nil {
		t.Fatalf("stop of an unloaded agent: %v", err)
	}
	if ta.out.String() != "stopped\n" {
		t.Errorf("output = %q", ta.out)
	}
	want := []string{
		"launchctl bootout gui/501/com.gabrielmarcano.agent-watch-bridge",
		"launchctl disable gui/501/com.gabrielmarcano.agent-watch-bridge",
	}
	if got := ta.runner.Calls(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("launchctl calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A stop whose disable fails says so: the agent would start again at login.
func TestStop_DisableFails(t *testing.T) {
	ta := newTestApp(t, "launchd")
	ta.runner.respond = func(line string) ([]byte, error) {
		if strings.Contains(line, "disable") {
			return []byte("Not privileged to disable service.\n"), errors.New("exit status 1")
		}
		return nil, nil
	}
	if err := ta.cmdStop(nil); err == nil || !strings.Contains(err.Error(), "disable") {
		t.Fatalf("stop = %v, want the disable error", err)
	}
}
