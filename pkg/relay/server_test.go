package relay

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// The ntfy topic is a secret (anyone who knows it can read the pushes), so it
// must never reach the logs.
func TestNewServer_DoesNotLogNtfyTopic(t *testing.T) {
	const topic = "secret-topic-4f9c2a7e1b"

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	server, err := NewServer(&Config{
		ListenAddr: ":0",
		HostToken:  testHostToken,
		DataDir:    t.TempDir(),
		NtfyURL:    "https://ntfy.example.com",
		NtfyTopic:  topic,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	logs := buf.String()
	if !strings.Contains(logs, "ntfy push enabled") {
		t.Fatalf("expected the ntfy startup log, got %q", logs)
	}
	if strings.Contains(logs, topic) {
		t.Fatalf("startup logs leak the ntfy topic: %q", logs)
	}
}
