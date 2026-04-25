package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
)

// Middleware provides HTTP authentication middleware that accepts both
// bearer tokens (legacy control token) and SSH-signed API tokens.
type Middleware struct {
	keyStore   *KeyStore
	legacyToken string // optional pre-shared bearer token for backward compat
	allowCIDRs []*net.IPNet
}

// MiddlewareConfig holds configuration for creating an auth middleware.
type MiddlewareConfig struct {
	// AuthorizedKeysPath is the path to the SSH authorized_keys file.
	// If empty, SSH key auth is disabled (only legacy token auth works).
	AuthorizedKeysPath string

	// LegacyToken is the pre-shared bearer token for backward compatibility.
	// If empty, legacy token auth is disabled.
	LegacyToken string

	// AllowCIDRs restricts access to specific source IP ranges.
	// Empty means all IPs are allowed.
	AllowCIDRs []string
}

// NewMiddleware creates an authentication middleware from the given config.
func NewMiddleware(cfg MiddlewareConfig) (*Middleware, error) {
	m := &Middleware{
		legacyToken: strings.TrimSpace(cfg.LegacyToken),
	}

	if strings.TrimSpace(cfg.AuthorizedKeysPath) != "" {
		store, err := LoadAuthorizedKeys(cfg.AuthorizedKeysPath)
		if err != nil {
			return nil, err
		}
		m.keyStore = store
	}

	cidrs, err := parseCIDRs(cfg.AllowCIDRs)
	if err != nil {
		return nil, err
	}
	m.allowCIDRs = cidrs

	return m, nil
}

// NewMiddlewareWithStore creates middleware with a pre-loaded key store.
func NewMiddlewareWithStore(store *KeyStore, legacyToken string, allowCIDRs []string) (*Middleware, error) {
	cidrs, err := parseCIDRs(allowCIDRs)
	if err != nil {
		return nil, err
	}
	return &Middleware{
		keyStore:    store,
		legacyToken: strings.TrimSpace(legacyToken),
		allowCIDRs:  cidrs,
	}, nil
}

// Wrap returns a handler that enforces authentication for /v1/* requests.
// Health and non-v1 endpoints are passed through without auth.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	if m == nil || next == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r == nil || r.URL == nil {
			writeAuthError(w, http.StatusBadRequest, "invalid request")
			return
		}
		path := r.URL.Path
		// Health checks and non-v1 paths are exempt.
		if path == "/healthz" || (path != "/v1" && !strings.HasPrefix(path, "/v1/")) {
			next.ServeHTTP(w, r)
			return
		}
		// Check CIDR allowlist.
		if !m.remoteAllowed(r.RemoteAddr) {
			writeAuthError(w, http.StatusForbidden, "remote address not allowed")
			return
		}
		// Extract token from Authorization header.
		tokenStr := extractBearerToken(r.Header.Get("Authorization"))
		if tokenStr == "" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeAuthError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		// Try SSH-signed token first, then fall back to legacy bearer token.
		identity, err := m.authenticate(tokenStr)
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeAuthError(w, http.StatusUnauthorized, err.Error())
			return
		}
		// Store identity and token in request context for downstream handlers.
		ctx := r.Context()
		ctx = WithIdentity(ctx, identity)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// authenticate attempts to authenticate the given token string.
func (m *Middleware) authenticate(tokenStr string) (*RequestIdentity, error) {
	// Try SSH-signed token.
	if strings.HasPrefix(tokenStr, tokenPrefix) && m.keyStore != nil {
		tok, err := ParseToken(tokenStr, m.keyStore)
		if err != nil {
			return nil, err
		}
		return &RequestIdentity{
			Fingerprint: tok.Claims.Issuer,
			Subject:     tok.Claims.Subject,
			Token:       tok,
			Method:      "ssh-token",
		}, nil
	}

	// Fall back to legacy bearer token.
	if m.legacyToken != "" {
		if subtleConstantCompare(tokenStr, m.legacyToken) {
			return &RequestIdentity{
				Fingerprint: "legacy",
				Subject:     "legacy-token",
				Method:      "legacy-token",
			}, nil
		}
	}

	return nil, errors.New("invalid bearer token")
}

// remoteAllowed checks whether the remote address falls within an allowed CIDR.
func (m *Middleware) remoteAllowed(remoteAddr string) bool {
	if len(m.allowCIDRs) == 0 {
		return true
	}
	ip := parseRemoteIP(remoteAddr)
	if ip == nil {
		return false
	}
	for _, cidr := range m.allowCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// KeyStore returns the loaded SSH key store (may be nil if SSH auth is disabled).
func (m *Middleware) KeyStore() *KeyStore {
	return m.keyStore
}

// --- Request identity context ---

type contextKey string

const identityKey contextKey = "auth-identity"

// RequestIdentity holds the authenticated identity from a request.
type RequestIdentity struct {
	Fingerprint string // SSH key fingerprint or "legacy"
	Subject     string // Token subject / label
	Token       *Token // Parsed SSH-signed token (nil for legacy auth)
	Method      string // "ssh-token" or "legacy-token"
}

// IsCommandAllowed checks if the authenticated identity allows a command.
func (id *RequestIdentity) IsCommandAllowed(command string) bool {
	if id.Token == nil {
		return true // Legacy token has full access.
	}
	return id.Token.IsCommandAllowed(command)
}

// IsSandboxAllowed checks if the authenticated identity allows access to a sandbox.
func (id *RequestIdentity) IsSandboxAllowed(vmid int) bool {
	if id.Token == nil {
		return true // Legacy token has full access.
	}
	return id.Token.IsSandboxAllowed(vmid)
}

// WithIdentity stores the request identity in the context.
func WithIdentity(ctx context.Context, id *RequestIdentity) context.Context {
	return context.WithValue(ctx, identityKey, id)
}

// FromContext retrieves the request identity from the context.
// Returns nil if no identity is set.
func FromContext(ctx context.Context) *RequestIdentity {
	if ctx == nil {
		return nil
	}
	val := ctx.Value(identityKey)
	if val == nil {
		return nil
	}
	id, ok := val.(*RequestIdentity)
	if !ok {
		return nil
	}
	return id
}

// --- helpers ---

func extractBearerToken(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	lower := strings.ToLower(header)
	if !strings.HasPrefix(lower, "bearer ") {
		return ""
	}
	return strings.TrimSpace(header[len("bearer "):])
}

func writeAuthError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
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

func parseCIDRs(values []string) ([]*net.IPNet, error) {
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

func subtleConstantCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	result := 0
	for i := 0; i < len(a); i++ {
		result |= int(a[i]) ^ int(b[i])
	}
	return result == 0
}
