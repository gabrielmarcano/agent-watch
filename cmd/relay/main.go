package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/gabrielmarcano/agent-monitor/pkg/relay"
)

var version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) < 1 {
		printUsage(stderr)
		return errors.New("subcommand required")
	}

	cmd := args[0]
	switch cmd {
	case "serve":
		return runServe(ctx)
	case "devices":
		return runDevices(ctx, args[1:], stdout)
	case "version":
		fmt.Fprintf(stdout, "agent-watch-relay %s\n", version)
		return nil
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `agent-watch-relay %s

Usage:
  agent-watch-relay serve                  Run the relay server
  agent-watch-relay devices list           List registered devices
  agent-watch-relay devices revoke <id>    Revoke a registered device
  agent-watch-relay version                Print version information

Environment variables:
  AW_LISTEN        Listen address (default ":8080")
  AW_HOST_TOKEN    64-character hex token shared with the bridge (required)
  AW_DATA_DIR      Directory holding store.json, relay.lock and admin.sock
                   (default "/var/lib/agent-watch-relay")
  AW_TRUSTED_PROXIES
                   Comma-separated CIDRs of the reverse proxies allowed to report
                   the client IP (X-Forwarded-For, then X-Real-IP). Default empty:
                   the TCP peer address is the client IP
  AW_CLIENT_IP_HEADER
                   Optional single-IP header (e.g. CF-Connecting-IP) honored from
                   a trusted proxy before X-Forwarded-For. Needs AW_TRUSTED_PROXIES
  AW_FCM_CREDENTIALS
                   Path to the Firebase service-account JSON. Unset: no FCM push
  AW_PUSH_RESOLVED Boolean (default false). Sends the FCM "resolved" push that
                   withdraws an answered approval. Enable only once the installed
                   Wear OS app handles "resolved"
  AW_NTFY_URL      ntfy server, e.g. https://ntfy.sh. Unset: no ntfy push
  AW_NTFY_TOPIC    Random, unguessable ntfy topic (required with AW_NTFY_URL)
  AW_NTFY_TOKEN    Optional ntfy access token

"devices list|revoke" work whether the relay is running or not. While it runs,
they go through its local admin socket ($AW_DATA_DIR/admin.sock): a revoked
device is rejected immediately and its open streams are closed. Run them as the
service user or as root, with the same AW_DATA_DIR as the service.
`, version)
}

func runServe(ctx context.Context) error {
	cfg, err := relay.LoadConfig()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}

	server, err := relay.NewServer(cfg)
	if err != nil {
		return fmt.Errorf("server init: %w", err)
	}

	if err := server.Run(ctx); err != nil {
		slog.Error("server exited with error", "err", err)
		return err
	}
	return nil
}

func runDevices(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) < 1 {
		return errors.New("usage: agent-watch-relay devices <list|revoke <id>>")
	}

	dataDir := os.Getenv("AW_DATA_DIR")
	if dataDir == "" {
		dataDir = relay.DefaultDataDir
	}

	sub := args[0]
	switch sub {
	case "list":
		devices, err := relay.AdminListDevices(ctx, dataDir)
		if err != nil {
			return fmt.Errorf("list devices in %s: %w", dataDir, err)
		}
		if len(devices) == 0 {
			fmt.Fprintln(stdout, "No registered devices found.")
			return nil
		}
		fmt.Fprintf(stdout, "%-18s %-24s %-25s %-25s\n", "ID", "NAME", "CREATED", "LAST SEEN")
		for _, d := range devices {
			fmt.Fprintf(stdout, "%-18s %-24s %-25s %-25s\n", d.ID, d.Name, d.CreatedAt, d.LastSeen)
		}
		return nil
	case "revoke":
		if len(args) < 2 {
			return errors.New("usage: agent-watch-relay devices revoke <device_id>")
		}
		deviceID := args[1]
		viaRelay, err := relay.AdminRevokeDevice(ctx, dataDir, deviceID)
		if errors.Is(err, relay.ErrDeviceNotFound) {
			return fmt.Errorf("device %q not found", deviceID)
		}
		if err != nil {
			return fmt.Errorf("revoke device %q: %w", deviceID, err)
		}
		if viaRelay {
			fmt.Fprintf(stdout, "Device %s revoked successfully by the running relay: its token is rejected and its open streams were closed.\n", deviceID)
		} else {
			fmt.Fprintf(stdout, "Device %s revoked successfully (relay not running; store.json updated).\n", deviceID)
		}
		return nil
	default:
		return fmt.Errorf("unknown devices subcommand: %s", sub)
	}
}
