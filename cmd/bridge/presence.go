package main

import (
	"fmt"
	"runtime"

	"github.com/gabrielmarcano/agent-monitor/pkg/bridge"
)

// cmdPresence prints or sets "Only Notify When Away" (config push_presence,
// off by default): presence prints on or off, presence on|off sets it. The
// config must be valid; the rest of it is kept as it is.
func (a *app) cmdPresence(args []string) error {
	fs := a.flagSet("presence")
	configPath := fs.String("config", "", "config file (default: the installed service's)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("usage: agent-watch-bridge presence [--config PATH] [on|off]")
	}

	r := a.resolveForRead(ServiceSpec{ConfigPath: *configPath})
	cfg, err := bridge.CheckConfig(r.spec.ConfigPath)
	if err != nil {
		return err
	}
	if fs.NArg() == 1 {
		switch fs.Arg(0) {
		case "on":
			cfg.PushPresence = true
		case "off":
			cfg.PushPresence = false
		default:
			return fmt.Errorf("presence takes on or off, not %q", fs.Arg(0))
		}
		if err := bridge.SaveConfig(r.spec.ConfigPath, cfg); err != nil {
			return err
		}
		if cfg.PushPresence && runtime.GOOS != "darwin" {
			fmt.Fprintln(a.stderr, "note: presence is only measured on macOS for now; on this host the watch keeps getting every push.")
		}
	}
	state := "off"
	if cfg.PushPresence {
		state = "on"
	}
	fmt.Fprintln(a.stdout, state)
	return nil
}
