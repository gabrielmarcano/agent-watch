package relay

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const testRelayVersion = "0.3.0 (c8aa72e)"

// The relay tells an authenticated bridge its version in the WebSocket
// handshake response, and nobody else: not a caller without the host token,
// not /v1/healthz.
func TestServer_VersionOnlyToAuthenticatedHost(t *testing.T) {
	_, ts := setupTestServerWith(t, func(c *Config) { c.Version = testRelayVersion })
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/host"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for name, header := range map[string]http.Header{
		"no token":    nil,
		"wrong token": {"Authorization": []string{"Bearer " + strings.Repeat("0", 64)}},
	} {
		_, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: header})
		if err == nil {
			t.Fatalf("%s: dial succeeded, want 401", name)
		}
		if resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: response %v, want 401", name, resp)
		}
		if v := resp.Header.Get(RelayVersionHeader); v != "" {
			t.Errorf("%s: %s = %q on a rejected handshake, want none", name, RelayVersionHeader, v)
		}
	}

	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + testHostToken}},
	})
	if err != nil {
		t.Fatalf("dial with the host token: %v", err)
	}
	defer conn.CloseNow()
	if got := resp.Header.Get(RelayVersionHeader); got != testRelayVersion {
		t.Errorf("%s = %q, want %q", RelayVersionHeader, got, testRelayVersion)
	}

	hresp, err := http.Get(ts.URL + "/v1/healthz")
	if err != nil {
		t.Fatalf("GET /v1/healthz: %v", err)
	}
	defer hresp.Body.Close()
	body, _ := io.ReadAll(hresp.Body)
	if strings.TrimSpace(string(body)) != `{"ok":true}` {
		t.Errorf("healthz body = %q, want {\"ok\":true}", body)
	}
	if v := hresp.Header.Get(RelayVersionHeader); v != "" {
		t.Errorf("healthz exposes the version: %s = %q", RelayVersionHeader, v)
	}
}

// A relay built without a version sends no header (the bridge reports "").
func TestHub_NoVersionNoHeader(t *testing.T) {
	hh := newHubHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, hh.wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer valid-host-token"}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	if _, ok := resp.Header[http.CanonicalHeaderKey(RelayVersionHeader)]; ok {
		t.Errorf("%s sent by a hub without a version", RelayVersionHeader)
	}

	hh.hub.SetVersion(testRelayVersion)
	conn2, resp2, err := websocket.Dial(ctx, hh.wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer valid-host-token"}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn2.CloseNow()
	if got := resp2.Header.Get(RelayVersionHeader); got != testRelayVersion {
		t.Errorf("after SetVersion: %s = %q, want %q", RelayVersionHeader, got, testRelayVersion)
	}
}
