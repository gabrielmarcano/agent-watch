package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gabrielmarcano/agent-monitor/pkg/buildinfo"
	"github.com/gabrielmarcano/agent-monitor/pkg/relay"
)

// version is RELAY_VERSION from VERSIONS, stamped by the Makefile
// (-ldflags "-X main.version=…"). Everything reports it through
// buildinfo.String, which adds the commit: "0.3.0 (c8aa72e)".
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
	case "hosts":
		return runHosts(ctx, args[1:], stdout, stderr)
	case "version":
		fmt.Fprintf(stdout, "agent-watch-relay %s\n", buildinfo.String(version))
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
  agent-watch-relay hosts add <id> [--name <name>]
                                           Register a host (one per machine running a
                                           bridge) and print its new token, once
  agent-watch-relay hosts list             List the hosts
  agent-watch-relay hosts revoke <id>      Revoke a host and close its connection
  agent-watch-relay version                Print version information

Environment variables:
  AW_LISTEN        Listen address (default ":8080")
  AW_HOST_TOKEN    64-character hex token shared with one bridge. Required until a
                   host is registered with "hosts add"
  AW_HOST_ID       Id of AW_HOST_TOKEN's host (default "main"); also the owner of
                   the history kept by a relay that predates hosts
  AW_HOST_NAME     Name the watch shows for that host (default: AW_HOST_ID)
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

"devices" and "hosts" work whether the relay is running or not. While it runs,
they go through its local admin socket ($AW_DATA_DIR/admin.sock): a revoked
device or host is rejected immediately and its open connections are closed.
Run them as the service user or as root, with the same AW_DATA_DIR (and
AW_HOST_ID, if set) as the service.
`, buildinfo.String(version))
}

func runServe(ctx context.Context) error {
	v := buildinfo.String(version)
	slog.Info("starting agent-watch-relay", "version", v)
	cfg, err := relay.LoadConfig()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	cfg.Version = v

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

func dataDirFromEnv() string {
	if dir := os.Getenv("AW_DATA_DIR"); dir != "" {
		return dir
	}
	return relay.DefaultDataDir
}

const hostsUsage = "usage: agent-watch-relay hosts <add <id> [--name <name>] | list | revoke <id>>"

// runHosts manages the registered hosts. "add" prints the new token alone on
// stdout (so it can be captured) and the rest on stderr.
func runHosts(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) < 1 {
		return errors.New(hostsUsage)
	}
	dataDir := dataDirFromEnv()

	switch args[0] {
	case "add":
		id, name, err := parseHostAdd(args[1:])
		if err != nil {
			return err
		}
		info, token, viaRelay, err := relay.AdminAddHost(ctx, dataDir, id, name)
		switch {
		case errors.Is(err, relay.ErrHostExists):
			return fmt.Errorf("host %q already exists (revoke it first to issue a new token)", id)
		case err != nil:
			return fmt.Errorf("add host %q: %w", id, err)
		}
		where := "by the running relay"
		if !viaRelay {
			where = "in store.json (relay not running)"
		}
		fmt.Fprintf(stderr, "Host %s (%q) registered %s. Its token, shown only this once (the relay keeps its hash):\n", info.ID, info.Name, where)
		fmt.Fprintln(stdout, token)
		fmt.Fprintln(stderr, "Configure that machine's bridge with it: AW_HOST_TOKEN in its agent-watch.env, then `make configure-bridge` there.")
		return nil
	case "list":
		hosts, err := relay.AdminListHosts(ctx, dataDir)
		if err != nil {
			return fmt.Errorf("list hosts in %s: %w", dataDir, err)
		}
		if len(hosts) == 0 {
			fmt.Fprintln(stdout, "No hosts found.")
			return nil
		}
		fmt.Fprintf(stdout, "%-33s %-24s %-7s %-25s %-25s\n", "ID", "NAME", "ONLINE", "CREATED", "LAST SEEN")
		for _, h := range hosts {
			created := h.CreatedAt
			if h.Env {
				created = "(AW_HOST_TOKEN)"
			}
			online := "no"
			if h.Online {
				online = "yes"
			}
			fmt.Fprintf(stdout, "%-33s %-24s %-7s %-25s %-25s\n", h.ID, h.Name, online, created, h.LastSeen)
		}
		return nil
	case "revoke":
		if len(args) != 2 {
			return errors.New("usage: agent-watch-relay hosts revoke <id>")
		}
		id := args[1]
		viaRelay, err := relay.AdminRevokeHost(ctx, dataDir, id)
		switch {
		case errors.Is(err, relay.ErrHostNotFound):
			return fmt.Errorf("host %q not found", id)
		case err != nil:
			return fmt.Errorf("revoke host %q: %w", id, err)
		}
		if viaRelay {
			fmt.Fprintf(stdout, "Host %s revoked by the running relay: its token is rejected and its connection was closed.\n", id)
		} else {
			fmt.Fprintf(stdout, "Host %s revoked (relay not running; store.json updated).\n", id)
		}
		return nil
	default:
		return fmt.Errorf("unknown hosts subcommand: %s (%s)", args[0], hostsUsage)
	}
}

// parseHostAdd reads "<id> [--name <name>]"; the flag may come first.
func parseHostAdd(args []string) (id, name string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--name" || a == "-name":
			if i+1 >= len(args) {
				return "", "", errors.New("--name needs a value")
			}
			name = args[i+1]
			i++
		case strings.HasPrefix(a, "--name="):
			name = strings.TrimPrefix(a, "--name=")
		case strings.HasPrefix(a, "-"):
			return "", "", fmt.Errorf("unknown flag %s (%s)", a, hostsUsage)
		case id == "":
			id = a
		default:
			return "", "", errors.New(hostsUsage)
		}
	}
	if id == "" {
		return "", "", errors.New(hostsUsage)
	}
	return id, name, nil
}

func runDevices(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) < 1 {
		return errors.New("usage: agent-watch-relay devices <list|revoke <id>>")
	}

	dataDir := dataDirFromEnv()

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
