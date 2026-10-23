package relayclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

func TestHTTPSBase(t *testing.T) {
	cases := []struct {
		url  string
		want string
		err  bool
	}{
		{"wss://relay.example.com/v1/host", "https://relay.example.com", false},
		{"ws://localhost:8080/v1/host", "http://localhost:8080", false},
		{"wss://relay.example.com:8443/v1/host", "https://relay.example.com:8443", false},
		{"https://relay.example.com", "", true},
		{"://bad-url", "", true},
	}
	for _, tc := range cases {
		got, err := HTTPSBase(tc.url)
		if (err != nil) != tc.err {
			t.Errorf("HTTPSBase(%q) err=%v, want err=%v", tc.url, err, tc.err)
		}
		if got != tc.want {
			t.Errorf("HTTPSBase(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestNormalizeRelayURL(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"wss://relay.example.com", "wss://relay.example.com/v1/host"},
		{"wss://relay.example.com/", "wss://relay.example.com/v1/host"},
		{"wss://relay.example.com/v1/host", "wss://relay.example.com/v1/host"},
		{"ws://localhost:8080", "ws://localhost:8080/v1/host"},
	}
	for _, tc := range cases {
		got := NormalizeRelayURL(tc.url)
		if got != tc.want {
			t.Errorf("NormalizeRelayURL(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestHostStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/host/status" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"0.2.0"}`))
	}))
	defer srv.Close()

	relayURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/host"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 1. Success
	raw, err := HostStatus(ctx, srv.Client(), relayURL, "test-tok")
	if err != nil {
		t.Fatalf("HostStatus: %v", err)
	}
	if !strings.Contains(string(raw), "0.2.0") {
		t.Errorf("unexpected body: %s", string(raw))
	}

	// 2. Auth failure
	_, err = HostStatus(ctx, srv.Client(), relayURL, "wrong-tok")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 error, got %v", err)
	}

	// 3. Unreachable URL
	_, err = HostStatus(ctx, srv.Client(), "ws://127.0.0.1:1/v1/host", "test-tok")
	if err == nil {
		t.Error("expected dial error, got nil")
	}
}

func TestRequestPairCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/host/pair-code" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(model.ErrorResponse{
				Error: model.ErrorBody{Code: "unauthorized", Message: "token invalid"},
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(model.PairCodeResponse{
			Code:      "ABCDEF",
			ExpiresAt: "2026-10-23T12:00:00Z",
		})
	}))
	defer srv.Close()

	relayURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/host"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 1. Success
	res, err := RequestPairCode(ctx, srv.Client(), relayURL, "test-tok")
	if err != nil {
		t.Fatalf("RequestPairCode: %v", err)
	}
	if res.Code != "ABCDEF" || res.ExpiresAt == "" {
		t.Errorf("unexpected response: %+v", res)
	}

	// 2. Auth error with HTTPError
	_, err = RequestPairCode(ctx, srv.Client(), relayURL, "wrong-tok")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var httpErr *HTTPError
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 error, got %v", err)
	}
	_ = httpErr
}
