package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/gabrielmarcano/agent-monitor/pkg/relay"
)

func TestCLI_Version(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"version"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run version failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "agent-watch-relay") {
		t.Errorf("expected version output, got %q", stdout.String())
	}
}

func TestCLI_DevicesListAndRevoke(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AW_DATA_DIR", dir)

	// 1. Empty list
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"devices", "list"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("devices list failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "No registered devices found") {
		t.Errorf("expected empty list message, got %q", stdout.String())
	}

	// 2. Add a device to store directly
	store, err := relay.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	dev, err := store.AddDevice("Test Watch", relay.Sha256Hex("some-token"))
	if err != nil {
		t.Fatalf("AddDevice failed: %v", err)
	}
	_ = store.Flush()
	_ = store.Close()

	// 3. List should now show the device
	stdout.Reset()
	stderr.Reset()
	err = run(context.Background(), []string{"devices", "list"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("devices list failed: %v", err)
	}
	if !strings.Contains(stdout.String(), dev.ID) || !strings.Contains(stdout.String(), "Test Watch") {
		t.Errorf("expected device %s in output, got %q", dev.ID, stdout.String())
	}

	// 4. Revoke non-existent device fails
	stdout.Reset()
	stderr.Reset()
	err = run(context.Background(), []string{"devices", "revoke", "nonexistent"}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected error revoking nonexistent device, got nil")
	}

	// 5. Revoke the real device succeeds
	stdout.Reset()
	stderr.Reset()
	err = run(context.Background(), []string{"devices", "revoke", dev.ID}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("devices revoke failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "revoked successfully") {
		t.Errorf("expected revoked message, got %q", stdout.String())
	}

	// 6. List is empty again
	stdout.Reset()
	stderr.Reset()
	_ = run(context.Background(), []string{"devices", "list"}, &stdout, &stderr)
	if !strings.Contains(stdout.String(), "No registered devices found") {
		t.Errorf("expected no devices after revocation, got %q", stdout.String())
	}
}

func TestCLI_ServeConfigValidation(t *testing.T) {
	// Missing AW_HOST_TOKEN should fail runServe
	t.Setenv("AW_HOST_TOKEN", "")
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"serve"}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected error running serve without AW_HOST_TOKEN, got nil")
	}
}

func TestCLI_UnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"unknown-cmd"}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected error for unknown command, got nil")
	}
}
