package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuth_ExtractBearerToken(t *testing.T) {
	// Query parameter should be completely ignored
	req := httptest.NewRequest("GET", "/v1/agents?token=secret123", nil)
	if tok := ExtractBearerToken(req); tok != "" {
		t.Fatalf("expected query param token to be ignored, got %q", tok)
	}

	// Valid Bearer
	req = httptest.NewRequest("GET", "/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer my-secret-token")
	if tok := ExtractBearerToken(req); tok != "my-secret-token" {
		t.Fatalf("expected 'my-secret-token', got %q", tok)
	}

	// Case-insensitive bearer
	req = httptest.NewRequest("GET", "/v1/agents", nil)
	req.Header.Set("Authorization", "bearer my-other-token")
	if tok := ExtractBearerToken(req); tok != "my-other-token" {
		t.Fatalf("expected 'my-other-token', got %q", tok)
	}

	// Invalid scheme
	req = httptest.NewRequest("GET", "/v1/agents", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	if tok := ExtractBearerToken(req); tok != "" {
		t.Fatalf("expected empty for Basic auth, got %q", tok)
	}
}

func TestAuth_PairingCodes(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	auth := NewAuthManager("test-host-token", store, ClientIPPolicy{})

	// Generate 3 codes
	c1, _, err := auth.GeneratePairCode()
	if err != nil {
		t.Fatalf("c1 err: %v", err)
	}
	c2, _, err := auth.GeneratePairCode()
	if err != nil {
		t.Fatalf("c2 err: %v", err)
	}
	c3, _, err := auth.GeneratePairCode()
	if err != nil {
		t.Fatalf("c3 err: %v", err)
	}

	// Generate a 4th code; c1 should be evicted (max 3 active)
	c4, _, err := auth.GeneratePairCode()
	if err != nil {
		t.Fatalf("c4 err: %v", err)
	}

	if auth.ConsumePairCode(c1) {
		t.Fatalf("c1 should have been evicted")
	}

	// c2 should be valid and single-use
	if !auth.ConsumePairCode(c2) {
		t.Fatalf("c2 should be valid")
	}
	// Reused code should fail
	if auth.ConsumePairCode(c2) {
		t.Fatalf("c2 should not be reusable")
	}

	// Non-existent code fails
	if auth.ConsumePairCode("999999") {
		t.Fatalf("random code should fail")
	}

	// Verify c3 and c4 are still valid
	if !auth.ConsumePairCode(c3) || !auth.ConsumePairCode(c4) {
		t.Fatalf("c3 and c4 should be valid")
	}
}

func TestAuth_RateLimiting(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	auth := NewAuthManager("test-host-token", store, ClientIPPolicy{})

	// Test 5 attempts per IP limit
	ip := "192.168.1.100"
	for i := 0; i < 5; i++ {
		if !auth.CheckAndRecordAttempt(ip) {
			t.Fatalf("attempt %d for %s should be allowed", i+1, ip)
		}
	}

	// 6th attempt should be blocked
	if auth.CheckAndRecordAttempt(ip) {
		t.Fatalf("6th attempt for %s should be blocked", ip)
	}

	// Different IP should still be allowed until global limit (20)
	otherIP := "192.168.1.101"
	if !auth.CheckAndRecordAttempt(otherIP) {
		t.Fatalf("first attempt for %s should be allowed", otherIP)
	}

	// Hit global limit: 5 from IP1, 1 from IP2. Let's record 14 more from various IPs.
	for i := 0; i < 14; i++ {
		tempIP := "10.0.0." + string(rune('0'+i))
		if !auth.CheckAndRecordAttempt(tempIP) {
			t.Fatalf("attempt %d should be within global limit", i+7)
		}
	}

	// Now total global is 20. The 21st attempt across any IP should be blocked.
	freshIP := "172.16.0.1"
	if auth.CheckAndRecordAttempt(freshIP) {
		t.Fatalf("attempt exceeding global limit (20) should be blocked")
	}
}

func TestAuth_ClientIP(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	authTrusted := NewAuthManager("test-host-token", store, ClientIPPolicy{
		TrustedProxies: mustPrefixes(t, "10.0.0.0/8"),
		Header:         "Cf-Connecting-Ip",
	})
	authDefault := NewAuthManager("test-host-token", store, ClientIPPolicy{})

	req, _ := http.NewRequest("POST", "/v1/pair", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("CF-Connecting-IP", "203.0.113.195")

	if ip := authTrusted.ClientIP(req); ip != "203.0.113.195" {
		t.Fatalf("expected CF-Connecting-IP from a trusted peer with the header opt-in, got %q", ip)
	}

	if ip := authDefault.ClientIP(req); ip != "10.0.0.1" {
		t.Fatalf("expected RemoteAddr host with the default policy, got %q", ip)
	}
}

func TestAuth_PairCodeExpiry(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	auth := NewAuthManager("test-host-token", store, ClientIPPolicy{})

	auth.mu.Lock()
	// Manually insert an expired code
	auth.pairCodes = append(auth.pairCodes, pairCodeEntry{
		code:      "123456",
		expiresAt: time.Now().Add(-time.Minute),
	})
	auth.mu.Unlock()

	if auth.ConsumePairCode("123456") {
		t.Fatalf("expired pair code should not be consumed")
	}
}
