// Package offline provides utilities for air-gapped (offline) deployments.
//
// When offline mode is enabled, all outbound network calls to external
// addresses are blocked. Only private/local network destinations are
// allowed (loopback, link-local, private RFC-1918 ranges, Unix sockets).
//
// This enables AgentLab to run fully air-gapped without depending on
// external services, DNS, or internet connectivity.
package offline

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// ErrBlocked is returned when an external network call is blocked in offline mode.
type ErrBlocked struct {
	Host string
}

func (e ErrBlocked) Error() string {
	return fmt.Sprintf("offline mode: external network call blocked to %s", e.Host)
}

// IsPrivateHost reports whether a hostname resolves to a private/local address.
//
// Private addresses include:
//   - Loopback (127.0.0.0/8, ::1)
//   - Link-local (169.254.0.0/16, fe80::/10)
//   - RFC 1918 private (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)
//   - Unique local (fc00::/7)
//
// Hostnames that look like IPs are parsed directly. Other hostnames are
// resolved via the default resolver. Unix domain sockets (ending in .sock
// or containing "/") are always considered private.
func IsPrivateHost(host string) bool {
	// Trim brackets from IPv6 literals.
	h := strings.TrimPrefix(host, "[")
	h = strings.TrimSuffix(h, "]")
	h = strings.TrimSpace(h)

	// Unix socket paths.
	if strings.Contains(h, "/") || strings.HasSuffix(h, ".sock") {
		return true
	}

	// Try parsing as IP directly.
	if ip := net.ParseIP(h); ip != nil {
		return isPrivateIP(ip)
	}

	// "localhost" and variants.
	if h == "localhost" || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") {
		return true
	}

	// Resolve hostname and check all results.
	addrs, err := net.DefaultResolver.LookupIPAddr(context.Background(), h)
	if err != nil {
		// If resolution fails, treat as external (conservative).
		return false
	}
	for _, addr := range addrs {
		if !isPrivateIP(addr.IP) {
			return false
		}
	}
	return len(addrs) > 0
}

func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() {
		return true
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if ip.IsPrivate() {
		return true
	}
	return false
}

// IsPrivateURL checks whether a URL's host component is a private address.
func IsPrivateURL(rawURL string) bool {
	h := extractHost(rawURL)
	return h != "" && IsPrivateHost(h)
}

func extractHost(rawURL string) string {
	// Simple host extraction: strip scheme, then take everything before : or /.
	s := rawURL
	if idx := strings.Index(s, "://"); idx >= 0 {
		s = s[idx+3:]
	}
	if idx := strings.Index(s, "/"); idx >= 0 {
		s = s[:idx]
	}
	// Strip port.
	if idx := strings.LastIndex(s, ":"); idx >= 0 {
		// Don't strip if it's an IPv6 address (contains ]).
		if !strings.Contains(s[idx:], "]") {
			s = s[:idx]
		}
	}
	// Strip brackets from IPv6.
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	return s
}

// NewOfflineTransport returns an http.RoundTripper that blocks requests
// to external (non-private) addresses.
//
// When offline mode is enabled, any HTTP request to a public IP or
// external hostname will fail with ErrBlocked. Requests to private
// addresses (loopback, RFC-1918, link-local) are allowed through.
//
// The returned transport wraps the provided base transport, or uses
// http.DefaultTransport if base is nil.
func NewOfflineTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &offlineTransport{base: base}
}

type offlineTransport struct {
	base http.RoundTripper
}

func (t *offlineTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	if !IsPrivateHost(host) {
		return nil, ErrBlocked{Host: host}
	}
	return t.base.RoundTrip(req)
}
