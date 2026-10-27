package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/bridge"
	"github.com/gabrielmarcano/agent-monitor/pkg/relayclient"
)

// localStatus assembles the bridge's health from local files only: the
// installed service definition, the config, status.json and a pid check.
// It never touches the network or the service manager.
func (a *app) localStatus() bridge.LocalStatus {
	r := a.resolveForRead(ServiceSpec{})
	statusPath := filepath.Join(r.spec.StateDir, "status.json")
	var defErr string
	if r.installErr != nil {
		defErr = r.installErr.Error()
	}
	return bridge.EvaluateLocalStatus(bridge.LocalStatusParams{
		Installed:       r.installed != nil || r.installErr != nil,
		Service:         a.svc.Kind(),
		DefinitionPath:  a.svc.DefinitionPath(),
		DefinitionError: defErr,
		Binary:          r.spec.Binary,
		ConfigPath:      r.spec.ConfigPath,
		StateDir:        r.spec.StateDir,
		StatusPath:      statusPath,
		LogPath:         a.svc.LogHint(r.spec),
		Version:         fullVersion,
		Now:             a.now(),
		PIDAlive:        a.pidAlive,
	})
}

// cmdStatus prints the bridge's health.
//
//	--local  no network and no service-manager call (what the menu bar polls)
//	--json   print JSON (bridge.LocalStatus, plus relay_status without --local)
//
// The human form exits with status 1 when the bridge is not running.
func (a *app) cmdStatus(args []string) error {
	fs := a.flagSet("status")
	asJSON := fs.Bool("json", false, "print JSON")
	local := fs.Bool("local", false, "local files only: no network, no launchctl/systemctl")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ls := a.localStatus()

	var svc *ServiceInfo
	if !*local {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		info, err := a.svc.Status(ctx)
		cancel()
		if err == nil {
			svc = &info
		}
	}

	var relayBody json.RawMessage
	relayErr := ""
	if !*local && ls.Configured {
		relayBody, relayErr = a.relayStatus(ls.ConfigPath)
	}

	if *asJSON {
		out := struct {
			bridge.LocalStatus
			RelayStatus      json.RawMessage `json:"relay_status,omitempty"`
			RelayStatusError string          `json:"relay_status_error,omitempty"`
		}{ls, relayBody, relayErr}
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	a.printHumanStatus(ls, svc, relayBody, relayErr, *local)
	running := ls.Running
	if svc != nil {
		running = svc.Running
	}
	if !running {
		return errExitStatus1
	}
	return nil
}

func (a *app) printHumanStatus(ls bridge.LocalStatus, svc *ServiceInfo, relayBody json.RawMessage, relayErr string, local bool) {
	w := a.stdout
	line := func(k, v string) { fmt.Fprintf(w, "%-9s %s\n", k+":", v) }

	state := "not running"
	if ls.Running {
		state = fmt.Sprintf("running (pid %d)", ls.PID)
	}
	switch {
	case !ls.Installed:
		state += "; service not installed"
	case svc != nil && svc.Running && svc.PID > 0:
		state += fmt.Sprintf("; %s: running (pid %d)", ls.Service, svc.PID)
	case svc != nil && svc.Running:
		state += "; " + ls.Service + ": running"
	case svc != nil:
		state += "; " + ls.Service + ": " + svc.Message
	}
	line("bridge", state)

	switch {
	case !ls.Running:
	case ls.Stale:
		line("status", fmt.Sprintf("stale: not updated for %ds", ls.AgeSeconds))
	case ls.RelayConnected:
		line("relay", "connected to "+ls.RelayHost)
	default:
		line("relay", "not connected")
	}
	if ls.Running {
		herdr := "online"
		if !ls.HerdrOnline {
			herdr = "offline"
		}
		line("herdr", herdr)
		line("agents", fmt.Sprintf("%d (%d blocked)", ls.Agents, ls.Blocked))
	}
	if ls.LastError != "" {
		line("error", ls.LastError)
	}
	if !ls.Configured {
		line("config", ls.ConfigError)
	} else {
		line("config", ls.ConfigPath)
	}
	line("status", ls.StatusPath)
	if ls.LogPath != "" {
		line("log", ls.LogPath)
	}
	if ls.DaemonVersion != "" && ls.DaemonVersion != ls.Version && ls.Running {
		line("version", fmt.Sprintf("running %s, this binary is %s: restart to update", ls.DaemonVersion, ls.Version))
	}
	if !local {
		switch {
		case relayErr != "":
			line("relay api", relayErr)
		case relayBody != nil:
			line("relay api", strings.TrimSpace(string(relayBody)))
		}
	}
}

// relayStatus calls GET /v1/host/status with the host token.
func (a *app) relayStatus(configPath string) (json.RawMessage, string) {
	cfg, err := bridge.CheckConfig(configPath)
	if err != nil {
		return nil, err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	body, err := relayclient.HostStatus(ctx, a.http, cfg.RelayURL, cfg.HostToken)
	if err != nil {
		return nil, err.Error()
	}
	return body, ""
}
