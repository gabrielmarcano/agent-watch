package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/bridge"
)

// serviceTimeout bounds one start/restart/stop, launchctl retries included.
const serviceTimeout = 30 * time.Second

type stringSlice []string

func (s *stringSlice) String() string     { return fmt.Sprint(*s) }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

func newFlagSet(name string) *flag.FlagSet {
	return flag.NewFlagSet(name, flag.ContinueOnError)
}

func (a *app) flagSet(name string) *flag.FlagSet {
	fs := newFlagSet(name)
	fs.SetOutput(a.stderr)
	return fs
}

// cmdConfigure writes config.toml. Without --config it targets the config
// herdr's environment names, else the installed service's, else the default.
// --env-file supplies the relay URL and host token that no flag gives, so the
// token never has to appear on a command line.
func (a *app) cmdConfigure(args []string) error {
	fs := a.flagSet("configure")
	relayURL := fs.String("relay-url", "", "Relay WebSocket URL (wss://...)")
	hostToken := fs.String("host-token", "", "64-character hex host token")
	envFile := fs.String("env-file", "", "agent-watch.env to read AW_RELAY_DOMAIN and AW_HOST_TOKEN from (a flag overrides it)")
	hostName := fs.String("host-name", "", "Optional host name override")
	configPath := fs.String("config", "", "Config file path")
	var claudeDirs stringSlice
	fs.Var(&claudeDirs, "claude-config-dir", "Claude config directory outside ~/.claude and ~/.claude-*, which are found without it (can be repeated)")
	var sessionRoots stringSlice
	fs.Var(&sessionRoots, "session-root", "Absolute folder where the phone may start new agent sessions (can be repeated; none: off)")
	var sessionKinds stringSlice
	fs.Var(&sessionKinds, "session-kind", "Agent kind the phone may start (can be repeated; none: claude, agy, opencode)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	r := a.resolveForStart(ServiceSpec{ConfigPath: *configPath})
	target := r.spec.ConfigPath

	_, notes, warn, err := bridge.UpdateConfig(target, bridge.ConfigureOptions{
		RelayURL:         *relayURL,
		HostToken:        *hostToken,
		EnvFile:          *envFile,
		HostName:         *hostName,
		ClaudeConfigDirs: claudeDirs,
		SessionRoots:     sessionRoots,
		SessionKinds:     sessionKinds,
	})
	if err != nil {
		return err
	}
	if warn != "" {
		fmt.Fprintln(a.stderr, warn)
	}
	fmt.Fprintln(a.stdout, target)
	for _, note := range notes {
		fmt.Fprintln(a.stderr, note)
	}
	if r.installed != nil && absPath(r.installed.ConfigPath) == target {
		fmt.Fprintln(a.stderr, "The bridge service uses this config: run `agent-watch-bridge restart` to apply it.")
	}
	return nil
}

func (a *app) specFlags(fs *flag.FlagSet) *ServiceSpec {
	var s ServiceSpec
	fs.StringVar(&s.Binary, "binary", "", "bridge binary the service runs (default: installed, else this one)")
	fs.StringVar(&s.ConfigPath, "config", "", "config file (default: installed, else default)")
	fs.StringVar(&s.SocketPath, "socket", "", "herdr socket (default: installed, else default)")
	fs.StringVar(&s.StateDir, "state-dir", "", "status.json directory (default: installed, else default)")
	fs.StringVar(&s.LogPath, "log", "", "log file (default: installed, else the platform default)")
	return &s
}

// cmdStart installs (or rewrites) the service definition and (re)loads it.
// Values not given as flags or herdr environment come from the installed
// definition. The config is validated first: a bad one never reaches launchd.
func (a *app) cmdStart(args []string) error {
	fs := a.flagSet("start")
	flags := a.specFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	r := a.resolveForStart(*flags)
	if _, err := bridge.CheckConfig(r.spec.ConfigPath); err != nil {
		return fmt.Errorf("not starting: %w", err)
	}
	if err := validateSpec(r.spec); err != nil {
		return fmt.Errorf("not starting: %w", err)
	}

	for _, n := range r.notes {
		fmt.Fprintln(a.stderr, "note:", n)
	}
	if r.installed != nil {
		for _, c := range specChanges(*r.installed, r.spec) {
			fmt.Fprintln(a.stderr, "changed:", c)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), serviceTimeout)
	defer cancel()
	if err := a.svc.Install(ctx, r.spec); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "started\nlog: %s\n", a.svc.LogHint(r.spec))
	return nil
}

// specChanges lists the values that differ between two specs.
func specChanges(old, cur ServiceSpec) []string {
	var out []string
	for _, f := range []struct{ name, old, cur string }{
		{"binary", old.Binary, cur.Binary},
		{"config", old.ConfigPath, cur.ConfigPath},
		{"socket", old.SocketPath, cur.SocketPath},
		{"state dir", old.StateDir, cur.StateDir},
		{"log", old.LogPath, cur.LogPath},
	} {
		if f.old != f.cur {
			out = append(out, fmt.Sprintf("%s %s -> %s", f.name, f.old, f.cur))
		}
	}
	return out
}

// cmdRestart restarts the installed service as defined, without rewriting
// the definition. It refuses if the config the service uses is invalid.
func (a *app) cmdRestart(args []string) error {
	fs := a.flagSet("restart")
	if err := fs.Parse(args); err != nil {
		return err
	}
	inst, err := a.loadInstalled()
	if err != nil {
		return err
	}
	if inst == nil {
		return errNotInstalled
	}
	if _, err := bridge.CheckConfig(inst.ConfigPath); err != nil {
		return fmt.Errorf("not restarting: %w", err)
	}
	if !isExecutableFile(inst.Binary) {
		return fmt.Errorf("not restarting: the installed binary %s is missing; rebuild it or run `agent-watch-bridge start`", inst.Binary)
	}

	ctx, cancel := context.WithTimeout(context.Background(), serviceTimeout)
	defer cancel()
	if err := a.svc.Restart(ctx); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "restarted\nlog: %s\n", a.svc.LogHint(*inst))
	return nil
}

func (a *app) cmdStop(args []string) error {
	fs := a.flagSet("stop")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), serviceTimeout)
	defer cancel()
	if err := a.svc.Stop(ctx); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, "stopped")
	return nil
}
