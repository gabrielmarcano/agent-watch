package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/bridge"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestMain is a safety net: the owner's real bridge, launchd agent, config
// and herdr socket must never be touched by a test. HOME and every herdr
// variable point into a throwaway directory, and the real command runner
// refuses to run.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "aw-bridge-cli-home")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv("HOME", home)
	os.Setenv("HERDR_SOCKET_PATH", filepath.Join(home, "no-herdr.sock"))
	os.Unsetenv("HERDR_PLUGIN_CONFIG_DIR")
	os.Unsetenv("HERDR_PLUGIN_STATE_DIR")
	realRunner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("test tried to run the real %s %v", name, args)
	}
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// fakeRunner records commands instead of running launchctl/systemctl.
type fakeRunner struct {
	mu      sync.Mutex
	calls   []string
	respond func(cmdline string) ([]byte, error)
}

func (f *fakeRunner) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	f.mu.Lock()
	f.calls = append(f.calls, line)
	respond := f.respond
	f.mu.Unlock()
	if respond != nil {
		return respond(line)
	}
	return nil, nil
}

func (f *fakeRunner) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// noNetwork fails the test on any HTTP request.
type noNetwork struct{ t *testing.T }

func (n noNetwork) RoundTrip(r *http.Request) (*http.Response, error) {
	n.t.Errorf("unexpected network call: %s %s", r.Method, r.URL)
	return nil, errors.New("network disabled in this test")
}

type testApp struct {
	*app
	runner *fakeRunner
	out    *bytes.Buffer
	errOut *bytes.Buffer
	env    map[string]string
}

// newTestApp builds an app rooted in a temp home with a fake runner, no
// network, and an executable "current binary". kind is launchd or systemd.
func newTestApp(t *testing.T, kind string) *testApp {
	t.Helper()
	home := t.TempDir()
	exe := writeExecutable(t, filepath.Join(home, "worktree", "bin", "agent-watch-bridge"))
	fr := &fakeRunner{}
	var svc ServiceManager
	switch kind {
	case "launchd":
		svc = &launchdManager{home: home, uid: 501, run: fr.run, sleep: func(time.Duration) {}}
	case "systemd":
		svc = &systemdManager{home: home, run: fr.run}
	default:
		t.Fatalf("unknown kind %q", kind)
	}
	ta := &testApp{runner: fr, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}, env: map[string]string{}}
	ta.app = &app{
		stdout:   ta.out,
		stderr:   ta.errOut,
		getenv:   func(k string) string { return ta.env[k] },
		home:     home,
		exe:      exe,
		svc:      svc,
		http:     &http.Client{Transport: noNetwork{t}},
		now:      time.Now,
		pidAlive: func(pid int) bool { return pid == os.Getpid() },
	}
	return ta
}

func writeExecutable(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeConfig(t *testing.T, path, relayURL string) {
	t.Helper()
	if err := bridge.SaveConfig(path, &bridge.Config{RelayURL: relayURL, HostToken: testToken}); err != nil {
		t.Fatal(err)
	}
}

// install writes spec as the installed service definition, as a previous
// `start` would have.
func install(t *testing.T, ta *testApp, spec ServiceSpec) {
	t.Helper()
	rendered, err := ta.svc.Render(spec)
	if err != nil {
		t.Fatal(err)
	}
	path := ta.svc.DefinitionPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
}

func installedSpec(t *testing.T, ta *testApp) ServiceSpec {
	t.Helper()
	spec, err := readInstalled(ta.svc)
	if err != nil || spec == nil {
		t.Fatalf("read installed definition: %v (spec %v)", err, spec)
	}
	return *spec
}
