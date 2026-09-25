package relay

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func mustPrefixes(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			t.Fatalf("parse %q: %v", c, err)
		}
		out = append(out, p)
	}
	return out
}

func TestClientIP(t *testing.T) {
	docker := ClientIPPolicy{TrustedProxies: mustPrefixes(t, "172.16.0.0/12")}
	dockerCF := ClientIPPolicy{TrustedProxies: mustPrefixes(t, "172.16.0.0/12"), Header: "Cf-Connecting-Ip"}
	loopback6 := ClientIPPolicy{TrustedProxies: mustPrefixes(t, "::1/128", "127.0.0.0/8")}

	tests := []struct {
		name    string
		policy  ClientIPPolicy
		remote  string
		headers [][2]string
		want    string
	}{
		{
			name:   "no trusted proxies: headers are ignored",
			policy: ClientIPPolicy{},
			remote: "198.51.100.7:4242",
			headers: [][2]string{
				{"CF-Connecting-IP", "203.0.113.1"},
				{"X-Forwarded-For", "203.0.113.2"},
				{"X-Real-IP", "203.0.113.3"},
			},
			want: "198.51.100.7",
		},
		{
			name:   "untrusted peer: spoofed headers are ignored even with the header opt-in",
			policy: dockerCF,
			remote: "198.51.100.7:4242",
			headers: [][2]string{
				{"CF-Connecting-IP", "203.0.113.1"},
				{"X-Forwarded-For", "203.0.113.2"},
				{"X-Real-IP", "203.0.113.3"},
			},
			want: "198.51.100.7",
		},
		{
			name:    "trusted peer: rightmost XFF entry",
			policy:  docker,
			remote:  "172.17.0.1:5555",
			headers: [][2]string{{"X-Forwarded-For", "203.0.113.9"}},
			want:    "203.0.113.9",
		},
		{
			name:    "trusted peer: client-supplied entries left of the real client are ignored",
			policy:  docker,
			remote:  "172.17.0.1:5555",
			headers: [][2]string{{"X-Forwarded-For", "10.9.9.9, 1.1.1.1, 203.0.113.9"}},
			want:    "203.0.113.9",
		},
		{
			name:    "trusted peer: trusted hops on the right are skipped",
			policy:  docker,
			remote:  "172.17.0.1:5555",
			headers: [][2]string{{"X-Forwarded-For", "1.1.1.1, 203.0.113.9, 172.18.0.5, 172.20.0.2"}},
			want:    "203.0.113.9",
		},
		{
			name:   "trusted peer: several XFF header lines form one list",
			policy: docker,
			remote: "172.17.0.1:5555",
			headers: [][2]string{
				{"X-Forwarded-For", "1.1.1.1"},
				{"X-Forwarded-For", "203.0.113.9, 172.18.0.5"},
			},
			want: "203.0.113.9",
		},
		{
			name:    "trusted peer: CF-Connecting-IP without the opt-in is ignored",
			policy:  docker,
			remote:  "172.17.0.1:5555",
			headers: [][2]string{{"CF-Connecting-IP", "1.1.1.1"}, {"X-Forwarded-For", "203.0.113.9"}},
			want:    "203.0.113.9",
		},
		{
			name:    "trusted peer: CF-Connecting-IP with the opt-in wins",
			policy:  dockerCF,
			remote:  "172.17.0.1:5555",
			headers: [][2]string{{"CF-Connecting-IP", "198.51.100.44"}, {"X-Forwarded-For", "203.0.113.9"}},
			want:    "198.51.100.44",
		},
		{
			name:    "trusted peer: invalid opted-in header falls back to XFF",
			policy:  dockerCF,
			remote:  "172.17.0.1:5555",
			headers: [][2]string{{"CF-Connecting-IP", "not-an-ip"}, {"X-Forwarded-For", "203.0.113.9"}},
			want:    "203.0.113.9",
		},
		{
			name:    "trusted peer: all XFF entries trusted falls back to X-Real-IP",
			policy:  docker,
			remote:  "172.17.0.1:5555",
			headers: [][2]string{{"X-Forwarded-For", "172.18.0.5"}, {"X-Real-IP", "203.0.113.50"}},
			want:    "203.0.113.50",
		},
		{
			name:    "trusted peer: malformed rightmost XFF entry falls back to X-Real-IP",
			policy:  docker,
			remote:  "172.17.0.1:5555",
			headers: [][2]string{{"X-Forwarded-For", "203.0.113.9, garbage"}, {"X-Real-IP", "203.0.113.50"}},
			want:    "203.0.113.50",
		},
		{
			name:   "trusted peer: no usable header falls back to RemoteAddr",
			policy: docker,
			remote: "172.17.0.1:5555",
			want:   "172.17.0.1",
		},
		{
			name:    "IPv4-mapped IPv6 entries are normalized before the trust check",
			policy:  docker,
			remote:  "172.17.0.1:5555",
			headers: [][2]string{{"X-Forwarded-For", "203.0.113.9, ::ffff:172.18.0.5"}},
			want:    "203.0.113.9",
		},
		{
			name:    "IPv6 peer and entries, with ports",
			policy:  loopback6,
			remote:  "[::1]:5555",
			headers: [][2]string{{"X-Forwarded-For", "[2001:db8::7]:443"}},
			want:    "2001:db8::7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/pair", nil)
			req.RemoteAddr = tt.remote
			for _, h := range tt.headers {
				req.Header.Add(h[0], h[1])
			}
			if got := tt.policy.ClientIP(req); got != tt.want {
				t.Fatalf("ClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseTrustedProxies(t *testing.T) {
	got, err := ParseTrustedProxies(" 172.16.0.0/12, 10.0.0.1 ,,2001:db8::/32,192.168.1.77/24")
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}
	want := []string{"172.16.0.0/12", "10.0.0.1/32", "2001:db8::/32", "192.168.1.0/24"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Fatalf("entry %d = %s, want %s", i, got[i], want[i])
		}
	}

	if got, err := ParseTrustedProxies(""); err != nil || len(got) != 0 {
		t.Fatalf("empty: got %v, %v; want no prefixes", got, err)
	}
	for _, bad := range []string{"172.16.0.0/33", "docker", "1.2.3.4/abc"} {
		if _, err := ParseTrustedProxies(bad); err == nil {
			t.Errorf("ParseTrustedProxies(%q) succeeded, want an error", bad)
		}
	}
}

// Behind a trusted proxy, each real client gets its own budget, and the
// budget follows the client, not the proxy.
func TestAPI_PairRateLimitPerRealClientBehindTrustedProxy(t *testing.T) {
	_, ts := setupTestServerWith(t, func(cfg *Config) {
		// httptest serves on loopback: the test client plays the proxy.
		cfg.TrustedProxies = mustPrefixes(t, "127.0.0.0/8", "::1/128")
	})

	clientA := map[string]string{"X-Forwarded-For": "10.1.1.1, 198.51.100.1"}
	for i := 0; i < 5; i++ {
		// The spoofed leftmost entry changes every time; the real client does not.
		clientA["X-Forwarded-For"] = "10.1.1." + string(rune('1'+i)) + ", 198.51.100.1"
		if code := pairAttempt(t, ts, clientA); code != http.StatusForbidden {
			t.Fatalf("client A attempt %d: status %d, want 403", i+1, code)
		}
	}
	if code := pairAttempt(t, ts, clientA); code != http.StatusTooManyRequests {
		t.Fatalf("client A 6th attempt: status %d, want 429", code)
	}
	clientB := map[string]string{"X-Forwarded-For": "198.51.100.2"}
	if code := pairAttempt(t, ts, clientB); code != http.StatusForbidden {
		t.Fatalf("client B behind the same proxy: status %d, want 403 (own budget)", code)
	}
}
