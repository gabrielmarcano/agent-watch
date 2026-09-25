package relay

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIPPolicy decides which address identifies the client of a request
// (used by the pairing rate limiter).
//
// Forwarding headers are client-controlled unless a proxy we trust wrote them,
// so they are read only when the TCP peer (RemoteAddr) is inside one of
// TrustedProxies. The zero value trusts nobody and always uses RemoteAddr.
type ClientIPPolicy struct {
	// TrustedProxies are the reverse proxies allowed to report the client IP.
	TrustedProxies []netip.Prefix
	// Header is an optional single-IP header (e.g. "CF-Connecting-IP") that a
	// trusted peer may set and that then wins over X-Forwarded-For. Empty means
	// no such header is honored.
	Header string
}

// ClientIP returns the client address for r:
//   - RemoteAddr when the peer is not a trusted proxy;
//   - otherwise Header (when configured and valid), then the rightmost
//     X-Forwarded-For entry that is not itself a trusted proxy, then
//     X-Real-IP, then RemoteAddr.
func (p ClientIPPolicy) ClientIP(r *http.Request) string {
	peer, ok := parseIP(r.RemoteAddr)
	if !ok {
		return strings.TrimSpace(r.RemoteAddr)
	}
	if !p.trusted(peer) {
		return peer.String()
	}
	if p.Header != "" {
		if ip, ok := parseIP(r.Header.Get(p.Header)); ok {
			return ip.String()
		}
	}
	if ip, ok := p.fromForwardedFor(r.Header.Values("X-Forwarded-For")); ok {
		return ip.String()
	}
	if ip, ok := parseIP(r.Header.Get("X-Real-IP")); ok {
		return ip.String()
	}
	return peer.String()
}

func (p ClientIPPolicy) trusted(ip netip.Addr) bool {
	for _, prefix := range p.TrustedProxies {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// fromForwardedFor walks the X-Forwarded-For hops from the right (the entry
// our own proxy appended) to the left and returns the first one that is not a
// trusted proxy. Entries left of it were supplied by the client and are never
// used. A malformed hop stops the walk: nothing left of it can be trusted.
func (p ClientIPPolicy) fromForwardedFor(values []string) (netip.Addr, bool) {
	var hops []string
	for _, v := range values {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		ip, ok := parseIP(hops[i])
		if !ok {
			return netip.Addr{}, false
		}
		if !p.trusted(ip) {
			return ip, true
		}
	}
	return netip.Addr{}, false
}

// parseIP accepts "ip", "ip:port", "[ipv6]" and "[ipv6]:port", and returns the
// address without zone and with IPv4-mapped IPv6 unmapped, so that it compares
// correctly against IPv4 prefixes.
func parseIP(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Addr{}, false
	}
	ip, err := netip.ParseAddr(s)
	if err != nil {
		host, _, splitErr := net.SplitHostPort(s)
		if splitErr != nil {
			host = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
		}
		if ip, err = netip.ParseAddr(host); err != nil {
			return netip.Addr{}, false
		}
	}
	return ip.WithZone("").Unmap(), true
}

// ParseTrustedProxies parses a comma-separated list of CIDRs. A bare address
// is accepted as a single-host prefix. Empty entries are skipped.
func ParseTrustedProxies(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, entry := range strings.Split(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			out = append(out, prefix.Masked())
			continue
		}
		if ip, err := netip.ParseAddr(entry); err == nil {
			ip = ip.WithZone("").Unmap()
			out = append(out, netip.PrefixFrom(ip, ip.BitLen()))
			continue
		}
		return nil, fmt.Errorf("invalid CIDR %q", entry)
	}
	return out, nil
}
