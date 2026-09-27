package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/agents"
	"github.com/gabrielmarcano/agent-monitor/pkg/bridge"
	"github.com/gabrielmarcano/agent-monitor/pkg/buildinfo"
	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/relayclient"
)

// version is BRIDGE_VERSION from VERSIONS, stamped by the Makefile and the
// plugin's [[build]] (-ldflags "-X main.version=…"); "dev" for a plain go build.
var version = "dev"

// fullVersion is what every output reports (version command, status.json,
// status --json, the hello to the relay): version plus the commit the binary
// was built from, e.g. "0.3.0 (c8aa72e)".
var fullVersion = buildinfo.String(version)

func main() {
	os.Exit(realMain(os.Args[1:]))
}

func realMain(argv []string) int {
	if len(argv) < 1 {
		printUsage()
		return 1
	}
	subcmd, args := argv[0], argv[1:]

	switch subcmd {
	case "version":
		fmt.Println(fullVersion)
		return 0
	case "-h", "--help", "help":
		printUsage()
		return 0
	case "run":
		return exitCode(runDaemon(args))
	}

	a, err := newApp()
	if err != nil {
		return exitCode(err)
	}
	var cmd func([]string) error
	switch subcmd {
	case "configure":
		cmd = a.cmdConfigure
	case "start":
		cmd = a.cmdStart
	case "restart":
		cmd = a.cmdRestart
	case "stop":
		cmd = a.cmdStop
	case "status":
		cmd = a.cmdStatus
	case "pair":
		cmd = a.cmdPair
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", subcmd)
		printUsage()
		return 1
	}
	return exitCode(cmd(args))
}

func exitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errExitStatus1):
		return 1
	default:
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `agent-watch-bridge %s

Usage:
  agent-watch-bridge configure --env-file agent-watch.env [--host-name N] [--claude-config-dir D]... [--config PATH]
  agent-watch-bridge configure --relay-url URL --host-token TOKEN [--host-name N] [--claude-config-dir D]... [--config PATH]
  agent-watch-bridge run [--config PATH]
  agent-watch-bridge start [--binary P] [--config P] [--socket P] [--state-dir D] [--log P]
  agent-watch-bridge restart
  agent-watch-bridge stop
  agent-watch-bridge status [--json] [--local]
  agent-watch-bridge pair [--json] [--config PATH]
  agent-watch-bridge version

configure --env-file takes AW_RELAY_DOMAIN and AW_HOST_TOKEN from the shared
agent-watch.env (see agent-watch.env.example), so the token never appears on
a command line; --relay-url and --host-token override it.
start installs the service (LaunchAgent on macOS, systemd --user unit on
Linux) and (re)loads it. A value not given as a flag or by herdr's plugin
environment is kept from the installed service definition.
restart restarts the installed service without rewriting it.
status --json --local reads local files only (no network).

`, fullVersion)
}

func runDaemon(args []string) error {
	fs := newFlagSet("run")
	configPath := fs.String("config", "", "Config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	targetConfig := *configPath
	if targetConfig == "" {
		targetConfig = bridge.DefaultConfigPath()
	}

	stateDir := bridge.DefaultStateDir()
	statusPath := filepath.Join(stateDir, "status.json")

	cfg, err := bridge.CheckConfig(targetConfig)
	if err != nil {
		// Leave the reason where status readers (the menu bar) look for it.
		_ = bridge.WriteStatus(statusPath, bridge.StoppedStatus(fullVersion, err.Error()))
		return err
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	hClient := herdr.NewClient("")
	syncer := &herdr.Syncer{
		Client: hClient,
		Logger: logger,
	}

	reg := agents.NewRegistry(agents.Config{
		ClaudeConfigDirs: cfg.ClaudeConfigDirs,
	})

	rClient := &relayclient.Client{
		URL:    bridge.NormalizeRelayURL(cfg.RelayURL),
		Token:  cfg.HostToken,
		Logger: logger,
	}

	engine := bridge.NewEngine(hClient, syncer, reg, rClient, fullVersion, cfg.HostName, statusPath, logger)
	syncer.Listener = engine
	rClient.OnConnect = engine.ConnectMessages
	rClient.OnMessage = engine.HandleRelayMessage

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger.Info("starting agent-watch-bridge", "version", fullVersion, "socket", hClient.SocketPath, "config", targetConfig, "status", statusPath)

	writerDone := engine.StartStatusWriter(ctx, 5*time.Second)

	go func() {
		if err := syncer.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Error("syncer error", "err", err)
		}
	}()

	runErr := rClient.Run(ctx)
	// Run only returns once ctx is done; make sure it is, then let the
	// writer mark the status file as stopped before the process exits.
	cancel()
	<-writerDone
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return fmt.Errorf("relay client run: %w", runErr)
	}

	logger.Info("agent-watch-bridge stopped cleanly")
	return nil
}
