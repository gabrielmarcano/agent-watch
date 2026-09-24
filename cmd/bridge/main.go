package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/agents"
	"github.com/gabrielmarcano/agent-monitor/pkg/bridge"
	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/relayclient"
)

var version = "0.2.0"

type stringSlice []string

func (s *stringSlice) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSlice) Set(val string) error {
	*s = append(*s, val)
	return nil
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	subcmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch subcmd {
	case "configure":
		err = runConfigure(args)
	case "run":
		err = runDaemon(args)
	case "start":
		err = runStart(args)
	case "stop":
		err = runStop(args)
	case "status":
		err = runStatus(args)
	case "pair":
		err = runPair(args)
	case "version":
		fmt.Println(version)
		return
	case "-h", "--help", "help":
		printUsage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", subcmd)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `agent-watch-bridge %s

Usage:
  agent-watch-bridge configure --relay-url URL --host-token TOKEN [options]
  agent-watch-bridge run [--config PATH]
  agent-watch-bridge start
  agent-watch-bridge stop
  agent-watch-bridge status
  agent-watch-bridge pair
  agent-watch-bridge version

`, version)
}

func defaultLogPath() string {
	home, err := os.UserHomeDir()
	if err == nil {
		if runtime.GOOS == "darwin" {
			return filepath.Join(home, "Library", "Logs", "agent-watch-bridge.log")
		}
		return filepath.Join(home, ".local", "state", "agent-watch", "bridge.log")
	}
	return "agent-watch-bridge.log"
}

func runConfigure(args []string) error {
	fs := flag.NewFlagSet("configure", flag.ContinueOnError)
	relayURL := fs.String("relay-url", "", "Relay WebSocket URL (wss://...)")
	hostToken := fs.String("host-token", "", "64-character hex host token")
	hostName := fs.String("host-name", "", "Optional host name override")
	configPath := fs.String("config", "", "Config file path")
	var claudeDirs stringSlice
	fs.Var(&claudeDirs, "claude-config-dir", "Claude config directory (can be repeated)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := &bridge.Config{
		RelayURL:         bridge.NormalizeRelayURL(*relayURL),
		HostToken:        *hostToken,
		HostName:         *hostName,
		ClaudeConfigDirs: claudeDirs,
	}

	if err := bridge.ValidateConfig(cfg); err != nil {
		return err
	}

	targetPath := *configPath
	if targetPath == "" {
		targetPath = bridge.DefaultConfigPath()
	}

	if err := bridge.SaveConfig(targetPath, cfg); err != nil {
		return err
	}

	fmt.Println(targetPath)
	return nil
}

func runDaemon(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	configPath := fs.String("config", "", "Config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	targetConfig := *configPath
	if targetConfig == "" {
		targetConfig = bridge.DefaultConfigPath()
	}

	cfg, err := bridge.LoadConfig(targetConfig)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := bridge.ValidateConfig(cfg); err != nil {
		return fmt.Errorf("validate config: %w", err)
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

	stateDir := bridge.DefaultStateDir()
	statusPath := filepath.Join(stateDir, "status.json")

	engine := bridge.NewEngine(hClient, syncer, reg, rClient, version, cfg.HostName, statusPath, logger)
	syncer.Listener = engine
	rClient.OnConnect = engine.ConnectMessages
	rClient.OnMessage = engine.HandleRelayMessage

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger.Info("starting agent-watch-bridge", "version", version, "socket", hClient.SocketPath, "config", targetConfig)

	engine.StartStatusWriter(ctx, 5*time.Second)

	go func() {
		if err := syncer.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Error("syncer error", "err", err)
		}
	}()

	if err := rClient.Run(ctx); err != nil && ctx.Err() == nil {
		return fmt.Errorf("relay client run: %w", err)
	}

	logger.Info("agent-watch-bridge stopped cleanly")
	return nil
}

func runStart(args []string) error {
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	binary, err := filepath.EvalSymlinks(bin)
	if err != nil {
		binary = bin
	}
	binary, _ = filepath.Abs(binary)

	configPath, _ := filepath.Abs(bridge.DefaultConfigPath())
	socketPath := herdr.NewClient("").SocketPath
	stateDir, _ := filepath.Abs(bridge.DefaultStateDir())
	logPath := defaultLogPath()

	mgr := newServiceManager()
	if err := mgr.Start(binary, configPath, socketPath, stateDir, logPath); err != nil {
		return err
	}

	fmt.Printf("started\nlog: %s\n", logPath)
	return nil
}

func runStop(args []string) error {
	mgr := newServiceManager()
	if err := mgr.Stop(); err != nil {
		return err
	}
	fmt.Println("stopped")
	return nil
}

func runStatus(args []string) error {
	mgr := newServiceManager()
	info, err := mgr.Status()
	if err != nil {
		return err
	}

	if !info.Running {
		fmt.Printf("service: not running (%s)\n", info.Message)
		os.Exit(1)
	}

	if info.PID > 0 {
		fmt.Printf("service: running (pid %d)\n", info.PID)
	} else {
		fmt.Println("service: running")
	}

	statusPath := filepath.Join(bridge.DefaultStateDir(), "status.json")
	if st, err := bridge.ReadStatus(statusPath); err == nil {
		stJSON, _ := json.MarshalIndent(st, "", "  ")
		fmt.Printf("\nlocal status:\n%s\n", string(stJSON))
	} else {
		fmt.Printf("\nlocal status: unavailable (%v)\n", err)
	}

	configPath := bridge.DefaultConfigPath()
	if cfg, err := bridge.LoadConfig(configPath); err == nil {
		if httpsBase, err := bridge.HTTPSBase(cfg.RelayURL); err == nil {
			relayStatusURL := httpsBase + "/v1/host/status"
			client := &http.Client{Timeout: 3 * time.Second}
			req, err := http.NewRequestWithContext(context.Background(), "GET", relayStatusURL, nil)
			if err == nil {
				req.Header.Set("Authorization", "Bearer "+cfg.HostToken)
				resp, err := client.Do(req)
				if err == nil {
					defer resp.Body.Close()
					body, _ := io.ReadAll(resp.Body)
					fmt.Printf("\nrelay status (%s):\n%s\n", relayStatusURL, string(body))
				} else {
					fmt.Printf("\nrelay status: could not connect (%v)\n", err)
				}
			}
		}
	}

	return nil
}

func runPair(args []string) error {
	configPath := bridge.DefaultConfigPath()
	cfg, err := bridge.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	httpsBase, err := bridge.HTTPSBase(cfg.RelayURL)
	if err != nil {
		return err
	}

	pairURL := httpsBase + "/v1/host/pair-code"
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), "POST", pairURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.HostToken)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request pair code from %s: %w", pairURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("relay returned status %d: %s", resp.StatusCode, string(body))
	}

	var pairResp struct {
		Code       string `json:"code"`
		ExpiresAt  string `json:"expires_at"`
		TTLSeconds int    `json:"ttl_seconds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pairResp); err != nil {
		return fmt.Errorf("decode pair response: %w", err)
	}

	code := pairResp.Code
	formattedCode := code
	if len(code) == 6 {
		formattedCode = fmt.Sprintf("%c %c %c · %c %c %c", code[0], code[1], code[2], code[3], code[4], code[5])
	}

	fmt.Println()
	fmt.Printf("Pairing code:  %s\n", formattedCode)
	fmt.Printf("Expires at:    %s (in %d seconds)\n", pairResp.ExpiresAt, pairResp.TTLSeconds)
	fmt.Printf("Relay URL:     %s\n", cfg.RelayURL)
	fmt.Println()

	// Show notification via herdr socket
	hClient := herdr.NewClient("")
	notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer notifyCancel()

	_ = hClient.Call(notifyCtx, "notification.show", map[string]any{
		"title": fmt.Sprintf("Agent Watch pairing code %s", code),
		"body":  "Enter it on your watch. Expires in 5 minutes.",
		"sound": "request",
	}, nil)

	return nil
}
