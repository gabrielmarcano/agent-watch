package relayclient_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
	"github.com/gabrielmarcano/agent-monitor/pkg/relay"
	"github.com/gabrielmarcano/agent-monitor/pkg/relayclient"
)

// The bridge and the real relay agree on the version header: a client
// connected to pkg/relay reports the relay's version.
func TestClient_ReadsRealRelayVersion(t *testing.T) {
	if relayclient.RelayVersionHeader != relay.RelayVersionHeader {
		t.Fatalf("header names differ: client %q, relay %q", relayclient.RelayVersionHeader, relay.RelayVersionHeader)
	}
	const token = "1111222233334444555566667777888899990000aaaabbbbccccddddeeeeffff"
	const version = "0.3.0 (c8aa72e)"
	srv, err := relay.NewServer(&relay.Config{HostToken: token, DataDir: t.TempDir(), Version: version})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		_ = srv.Close()
	})

	c := &relayclient.Client{
		URL:   "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/host",
		Token: token,
		OnConnect: func(context.Context) []any {
			return []any{
				model.HelloMsg{Type: model.WireHello, Version: "0.3.0", Host: "test-host"},
				model.SnapshotMsg{Type: model.WireSnapshot, Agents: []model.AgentState{}},
			}
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	deadline := time.Now().Add(3 * time.Second)
	for !c.Connected() {
		if time.Now().After(deadline) {
			t.Fatalf("not connected to the relay: %s", c.LastError())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := c.RelayVersion(); got != version {
		t.Errorf("RelayVersion = %q, want %q", got, version)
	}
}
