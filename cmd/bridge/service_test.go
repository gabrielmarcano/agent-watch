package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Paths with spaces, "&", "<", quotes, "%" and "$" must survive a render and
// parse unchanged.
var awkwardSpec = ServiceSpec{
	Binary:     `/Users/t/My Code & <stuff>/"x"/agent-watch-bridge`,
	ConfigPath: `/Users/t/.config/herdr/plugins/config/herdr agent's & co/config.toml`,
	SocketPath: `/Users/t/100% $HOME/herdr.sock`,
	StateDir:   `/Users/t/state dir/<herdr>`,
	LogPath:    `/Users/t/Library/Logs/a&b <c>.log`,
}

func TestLaunchd_RenderEscapesAndRoundTrips(t *testing.T) {
	m := &launchdManager{home: t.TempDir(), uid: 501}
	rendered, err := m.Render(awkwardSpec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered, "& <") {
		t.Fatalf("raw & or < in the plist:\n%s", rendered)
	}
	got, err := m.Parse([]byte(rendered))
	if err != nil {
		t.Fatalf("parse rendered plist: %v\n%s", err, rendered)
	}
	if got != awkwardSpec {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, awkwardSpec)
	}

	// Cross-check with Apple's own parser when it is available.
	plutil, err := exec.LookPath("plutil")
	if err != nil {
		t.Skip("plutil not available")
	}
	path := filepath.Join(t.TempDir(), "test.plist")
	if err := os.WriteFile(path, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(plutil, "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("plutil -lint: %v: %s", err, out)
	}
	for key, want := range map[string]string{
		"ProgramArguments.0":                     awkwardSpec.Binary,
		"ProgramArguments.3":                     awkwardSpec.ConfigPath,
		"EnvironmentVariables.HERDR_SOCKET_PATH": awkwardSpec.SocketPath,
		"EnvironmentVariables.AW_STATE_DIR":      awkwardSpec.StateDir,
		"StandardErrorPath":                      awkwardSpec.LogPath,
	} {
		out, err := exec.Command(plutil, "-extract", key, "raw", "-o", "-", path).Output()
		if err != nil {
			t.Fatalf("plutil -extract %s: %v", key, err)
		}
		if got := strings.TrimRight(string(out), "\n"); got != want {
			t.Errorf("plutil sees %s = %q, want %q", key, got, want)
		}
	}
}

func TestLaunchd_RenderHasEveryKey(t *testing.T) {
	m := &launchdManager{home: t.TempDir(), uid: 501}
	spec := ServiceSpec{
		Binary:     "/usr/local/bin/agent-watch-bridge",
		ConfigPath: "/Users/test/.config/agent-watch/config.toml",
		SocketPath: "/Users/test/.local/state/herdr-core/default/default.sock",
		StateDir:   "/Users/test/.local/state/agent-watch",
		LogPath:    "/Users/test/Library/Logs/agent-watch-bridge.log",
	}
	rendered, err := m.Render(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<key>Label</key><string>com.gabrielmarcano.agent-watch-bridge</string>",
		"<string>" + spec.Binary + "</string>",
		"<string>run</string>",
		"<string>--config</string>",
		"<string>" + spec.ConfigPath + "</string>",
		"<key>HERDR_SOCKET_PATH</key><string>" + spec.SocketPath + "</string>",
		"<key>AW_STATE_DIR</key><string>" + spec.StateDir + "</string>",
		"<key>RunAtLoad</key><true/>",
		"<key>KeepAlive</key><true/>",
		"<key>ThrottleInterval</key><integer>10</integer>",
		"<key>StandardOutPath</key><string>" + spec.LogPath + "</string>",
		"<key>StandardErrorPath</key><string>" + spec.LogPath + "</string>",
		"<key>AssociatedBundleIdentifiers</key><array><string>com.gabrielmarcano.AgentWatchBar</string></array>",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("plist lacks %s", want)
		}
	}
}

// The format installed by earlier versions parses.
func TestLaunchd_ParsesPreviouslyInstalledPlist(t *testing.T) {
	const installed = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.gabrielmarcano.agent-watch-bridge</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Users/u/Code/agent-watch/bin/agent-watch-bridge</string>
    <string>run</string>
    <string>--config</string>
    <string>/Users/u/.config/agent-watch/config.toml</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HERDR_SOCKET_PATH</key><string>/Users/u/.local/state/herdr-core/default/default.sock</string>
    <key>AW_STATE_DIR</key><string>/Users/u/.local/state/agent-watch</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>10</integer>
  <key>StandardOutPath</key><string>/Users/u/Library/Logs/agent-watch-bridge.log</string>
  <key>StandardErrorPath</key><string>/Users/u/Library/Logs/agent-watch-bridge.log</string>
</dict>
</plist>
`
	got, err := (&launchdManager{}).Parse([]byte(installed))
	if err != nil {
		t.Fatal(err)
	}
	want := ServiceSpec{
		Binary:     "/Users/u/Code/agent-watch/bin/agent-watch-bridge",
		ConfigPath: "/Users/u/.config/agent-watch/config.toml",
		SocketPath: "/Users/u/.local/state/herdr-core/default/default.sock",
		StateDir:   "/Users/u/.local/state/agent-watch",
		LogPath:    "/Users/u/Library/Logs/agent-watch-bridge.log",
	}
	if got != want {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestSystemd_RenderQuotesAndRoundTrips(t *testing.T) {
	m := &systemdManager{home: t.TempDir()}
	spec := awkwardSpec
	spec.Binary = `/home/t/My Code/back\slash/agent-watch-bridge`
	spec.LogPath = "" // journald
	rendered, err := m.Render(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, `ExecStart="/home/t/My Code/back\\slash/agent-watch-bridge" run --config "`) {
		t.Errorf("ExecStart is not quoted:\n%s", rendered)
	}
	if !strings.Contains(rendered, `100%% $HOME`) {
		t.Errorf("%% not escaped in Environment=:\n%s", rendered)
	}
	got, err := m.Parse([]byte(rendered))
	if err != nil {
		t.Fatal(err)
	}
	if got != spec {
		t.Errorf("round trip:\n got %+v\nwant %+v\n%s", got, spec, rendered)
	}
	if _, err := m.Render(ServiceSpec{Binary: "/a\nb", ConfigPath: "/c", SocketPath: "/s", StateDir: "/d"}); err == nil {
		t.Error("a line break in a path was accepted")
	}
}

// Units written by earlier versions (unquoted) still parse.
func TestSystemd_ParsesUnquotedUnit(t *testing.T) {
	unit := "[Service]\nExecStart=/opt/aw/agent-watch-bridge run --config /home/u/.config/aw/config.toml\n" +
		"Environment=HERDR_SOCKET_PATH=/home/u/.local/state/herdr-core/default/default.sock\n" +
		"Environment=AW_STATE_DIR=/home/u/.local/state/aw\n"
	got, err := (&systemdManager{}).Parse([]byte(unit))
	if err != nil {
		t.Fatal(err)
	}
	want := ServiceSpec{
		Binary: "/opt/aw/agent-watch-bridge", ConfigPath: "/home/u/.config/aw/config.toml",
		SocketPath: "/home/u/.local/state/herdr-core/default/default.sock", StateDir: "/home/u/.local/state/aw",
	}
	if got != want {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

// start resolves each value from, in order: flag, environment,
// the installed definition, the default. An installed binary that no
// longer exists is not reused.
func TestResolveForStart(t *testing.T) {
	ta := newTestApp(t, "launchd")
	otherBin := writeExecutable(t, filepath.Join(ta.home, "bin", "agent-watch-bridge"))
	installedAll := ServiceSpec{
		Binary:     otherBin,
		ConfigPath: "/inst/config.toml",
		SocketPath: "/inst/herdr.sock",
		StateDir:   "/inst/state",
		LogPath:    "/inst/bridge.log",
	}
	defaults := ta.defaults()

	cases := []struct {
		name      string
		installed *ServiceSpec
		env       map[string]string
		flags     ServiceSpec
		want      ServiceSpec
	}{
		{
			name: "fresh install from a terminal uses defaults",
			want: ServiceSpec{Binary: ta.exe, ConfigPath: defaults.ConfigPath, SocketPath: defaults.SocketPath, StateDir: defaults.StateDir, LogPath: defaults.LogPath},
		},
		{
			name:      "terminal or menu bar reuses every installed value",
			installed: &installedAll,
			want:      installedAll,
		},
		{
			name:      "a herdr pane only pins the socket",
			installed: &installedAll,
			env:       map[string]string{"HERDR_SOCKET_PATH": "/pane/herdr.sock"},
			want:      ServiceSpec{Binary: otherBin, ConfigPath: "/inst/config.toml", SocketPath: "/pane/herdr.sock", StateDir: "/inst/state", LogPath: "/inst/bridge.log"},
		},
		{
			name:      "AW_* environment overrides config and state",
			installed: &installedAll,
			env: map[string]string{
				"AW_CONFIG_DIR":      "/custom/config",
				"AW_STATE_DIR":       "/custom/state",
				"HC_TERMINAL_SOCKET": "/custom/hc.sock",
			},
			want: ServiceSpec{Binary: otherBin, ConfigPath: "/custom/config/config.toml", SocketPath: "/custom/hc.sock", StateDir: "/custom/state", LogPath: "/inst/bridge.log"},
		},
		{
			name:      "flags win",
			installed: &installedAll,
			env:       map[string]string{"AW_STATE_DIR": "/custom/state"},
			flags:     ServiceSpec{Binary: otherBin, ConfigPath: "/f/c.toml", SocketPath: "/f/s.sock", StateDir: "/f/state", LogPath: "/f/l.log"},
			want:      ServiceSpec{Binary: otherBin, ConfigPath: "/f/c.toml", SocketPath: "/f/s.sock", StateDir: "/f/state", LogPath: "/f/l.log"},
		},
		{
			name: "a missing installed binary is replaced by this one",
			installed: &ServiceSpec{
				Binary: "/gone/agent-watch-bridge", ConfigPath: "/inst/config.toml",
				SocketPath: "/inst/herdr.sock", StateDir: "/inst/state", LogPath: "/inst/bridge.log",
			},
			want: ServiceSpec{Binary: ta.exe, ConfigPath: "/inst/config.toml", SocketPath: "/inst/herdr.sock", StateDir: "/inst/state", LogPath: "/inst/bridge.log"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(ta.svc.DefinitionPath())
			if tc.installed != nil {
				install(t, ta, *tc.installed)
			}
			ta.env = tc.env
			if ta.env == nil {
				ta.env = map[string]string{}
			}
			r := ta.resolveForStart(tc.flags)
			if r.spec != tc.want {
				t.Errorf("resolved\n got %+v\nwant %+v", r.spec, tc.want)
			}
		})
	}
}
