package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/herdrtest"
	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// pairRelay serves POST /v1/host/pair-code like the relay: a code that
// expires five minutes after now.
func pairRelay(t *testing.T, now time.Time, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/host/pair-code" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+testToken || r.URL.RawQuery != "" {
			status = http.StatusUnauthorized
		}
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(model.ErrorResponse{Error: model.ErrorBody{Code: "unauthorized", Message: "unknown host token"}})
			return
		}
		_ = json.NewEncoder(w).Encode(model.PairCodeResponse{
			Code:      "417293",
			ExpiresAt: now.Add(5 * time.Minute).UTC().Format(time.RFC3339),
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newPairApp(t *testing.T, status int) (*testApp, *herdrtest.Server) {
	t.Helper()
	ta := newTestApp(t, "launchd")
	now := time.Date(2026, 9, 25, 15, 0, 0, 0, time.UTC)
	ta.now = func() time.Time { return now }
	srv := pairRelay(t, now, status)
	ta.http = srv.Client()
	writeConfig(t, ta.defaults().ConfigPath, "ws://"+strings.TrimPrefix(srv.URL, "http://"))
	herdr := herdrtest.New(t)
	ta.env["HERDR_SOCKET_PATH"] = herdr.SocketPath
	return ta, herdr
}

// pair printed "in 0 seconds" (it read a ttl_seconds field the relay does
// not send). It must show the real expiry from expires_at, and work without
// the bridge running.
func TestPair_PrintsRealExpiry(t *testing.T) {
	ta, herdr := newPairApp(t, http.StatusOK)
	if err := ta.cmdPair(nil); err != nil {
		t.Fatalf("pair: %v", err)
	}
	out := ta.out.String()
	for _, want := range []string{"4 1 7 · 2 9 3", "in 5m0s"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "in 0 seconds") || strings.Contains(out, testToken) {
		t.Errorf("bad output:\n%s", out)
	}

	var notified bool
	for _, c := range herdr.Calls() {
		if c.Method == "notification.show" {
			notified = true
			title, _ := c.Params["title"].(string)
			body, _ := c.Params["body"].(string)
			if !strings.Contains(title, "417293") || !strings.Contains(body, "5 min") {
				t.Errorf("notification title=%q body=%q", title, body)
			}
		}
	}
	if !notified {
		t.Error("no herdr notification")
	}
}

func TestPair_JSON(t *testing.T) {
	ta, _ := newPairApp(t, http.StatusOK)
	if err := ta.cmdPair([]string{"--json"}); err != nil {
		t.Fatalf("pair --json: %v", err)
	}
	var got pairOutput
	if err := json.Unmarshal(ta.out.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", ta.out, err)
	}
	if got.Code != "417293" || got.ExpiresInSeconds != 300 || got.ExpiresAt == "" || !strings.HasPrefix(got.RelayHost, "127.0.0.1:") {
		t.Errorf("pair --json = %+v", got)
	}
}

func TestPair_RelayRejectsToken(t *testing.T) {
	ta, _ := newPairApp(t, http.StatusUnauthorized)
	err := ta.cmdPair(nil)
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "host token") {
		t.Errorf("err = %v, want a 401 host token error", err)
	}
}
