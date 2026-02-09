package daemon

import (
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"strings"
)

// ControlAuth enforces bearer token auth and optional CIDR allowlists for control traffic.
type ControlAuth struct {
	token      string
	allowCIDRs []*net.IPNet
}

// NewControlAuth creates a control auth middleware with an optional CIDR allowlist.
func NewControlAuth(token string, allowCIDRs []string) (*ControlAuth, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("control auth token is required")
	}
	nets, err := parseCIDRList(allowCIDRs)
	if err != nil {
		return nil, err
	}
	return &ControlAuth{
		token:      token,
		allowCIDRs: nets,
	}, nil
}

// Wrap returns a handler that enforces auth for /v1/* requests (healthz is exempt).
func (a *ControlAuth) Wrap(next http.Handler) http.Handler {
	if a == nil || next == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r == nil || r.URL == nil {
			writeError(w, http.StatusBadRequest, "invalid request")
			return
		}
		path := r.URL.Path
		if path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if path != "/v1" && !strings.HasPrefix(path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		if !a.remoteAllowed(r.RemoteAddr) {
			writeError(w, http.StatusForbidden, "remote address not allowed")
			return
		}
		token := bearerToken(r.Header.Get("Authorization"))
		if token == "" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(a.token)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *ControlAuth) remoteAllowed(remoteAddr string) bool {
	if a == nil || len(a.allowCIDRs) == 0 {
		return true
	}
	ip := parseRemoteIP(remoteAddr)
	if ip == nil {
		return false
	}
	for _, cidr := range a.allowCIDRs {
		if cidr != nil && cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func parseRemoteIP(remoteAddr string) net.IP {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if remoteAddr == "" {
		return nil
	}
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if idx := strings.LastIndex(host, "%"); idx >= 0 {
		host = host[:idx]
	}
	return net.ParseIP(host)
}

func parseCIDRList(values []string) ([]*net.IPNet, error) {
	if len(values) == 0 {
		return nil, nil
	}
	result := make([]*net.IPNet, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		_, cidr, err := net.ParseCIDR(value)
		if err != nil {
			return nil, err
		}
		result = append(result, cidr)
	}
	return result, nil
}
