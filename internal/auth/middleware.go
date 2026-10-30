package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/agentlab/agentlab/internal/hostidentity"
)

// Middleware provides HTTP authentication middleware that accepts both
// bearer tokens (legacy control token) and SSH-signed API tokens.
type Middleware struct {
	keyStore    *KeyStore
	legacyToken string // optional pre-shared bearer token for backward compat
	allowCIDRs  []*net.IPNet
	revocations RevocationChecker
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
	AllowCIDRs  []string
	Revocations RevocationChecker
}

// NewMiddleware creates an authentication middleware from the given config.
func NewMiddleware(cfg MiddlewareConfig) (*Middleware, error) {
	m := &Middleware{
		legacyToken: strings.TrimSpace(cfg.LegacyToken),
		revocations: cfg.Revocations,
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
//
// This performs authentication only. It does NOT enforce a token's declared
// command/sandbox scopes — see WrapNetwork for the network path.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	if m == nil || next == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r == nil || r.URL == nil {
			writeAuthError(w, http.StatusBadRequest, "invalid request")
			return
		}
		identity, ok := m.authenticateRequest(w, r)
		if !ok {
			return
		}
		ctx := r.Context()
		if identity != nil {
			ctx = WithIdentity(ctx, identity)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// WrapNetwork returns a handler that authenticates /v1/* requests on the TCP
// control listener and injects the authenticated identity into the request
// context. Per-route command and sandbox-scope authorization is enforced by
// the daemon handlers via auth.FromContext + ControlAPI.authorize (see
// internal/daemon/authz.go); this wrapper performs authentication only.
//
// Scoped SSH-signed tokens are admitted here and constrained by the handlers.
// The trusted local Unix socket does not use this wrapper: it has no identity
// in context, so ControlAPI.authorize treats it as a trusted full-access path.
func (m *Middleware) WrapNetwork(next http.Handler) http.Handler {
	if m == nil || next == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r == nil || r.URL == nil {
			writeAuthError(w, http.StatusBadRequest, "invalid request")
			return
		}
		identity, ok := m.authenticateRequest(w, r)
		if !ok {
			return
		}
		if values := r.Header.Values(hostidentity.RequestHeader); len(values) > 0 && (r.URL.Path == "/v1" || strings.HasPrefix(r.URL.Path, "/v1/")) {
			actual, err := hostidentity.Local()
			if err != nil {
				writeServerIdentityError(w, http.StatusServiceUnavailable, "v1/auth/server_identity_unavailable", "The server cannot verify its identity. Restore its machine identity before retrying.")
				return
			}
			if len(values) != 1 || values[0] != actual {
				writeServerIdentityError(w, http.StatusConflict, "v1/auth/server_identity_mismatch", "The request targets another server. Verify the original context before retrying.")
				return
			}
		}
		ctx := r.Context()
		if identity != nil {
			ctx = WithIdentity(ctx, identity)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeServerIdentityError(w http.ResponseWriter, status int, code, action string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": action, "message": action, "code": code, "action": action, "state": "blocked", "requirement": "verified_server_identity"})
}

// authenticateRequest performs CIDR and bearer-token authentication for /v1/*
// requests, shared by Wrap and WrapNetwork. Health and non-v1 paths bypass
// authentication. On failure it writes the response and returns (nil, false).
// On bypass it returns (nil, true) so the caller continues without injecting
// an identity. On success it returns the identity and (nil-safe) true.
func (m *Middleware) authenticateRequest(w http.ResponseWriter, r *http.Request) (*RequestIdentity, bool) {
	path := r.URL.Path
	// Health checks and non-v1 paths are exempt.
	if path == "/healthz" || (path != "/v1" && !strings.HasPrefix(path, "/v1/")) {
		return nil, true
	}
	// Check CIDR allowlist.
	if !m.remoteAllowed(r.RemoteAddr) {
		writeAuthError(w, http.StatusForbidden, "remote address not allowed")
		return nil, false
	}
	// Extract token from Authorization header.
	tokenStr := extractBearerToken(r.Header.Get("Authorization"))
	if tokenStr == "" {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeAuthError(w, http.StatusUnauthorized, "missing bearer token")
		return nil, false
	}
	// Try SSH-signed token first, then fall back to legacy bearer token.
	identity, err := m.authenticate(tokenStr)
	if err != nil {
		w.Header().Set("WWW-Authenticate", "Bearer")
		if errors.Is(err, ErrTokenExpired) {
			writeCredentialStatusError(w, http.StatusUnauthorized, "v1/auth/token_expired", "token expired", "obtain a new authorized credential before retrying", false)
			return nil, false
		}
		writeAuthError(w, http.StatusUnauthorized, err.Error())
		return nil, false
	}
	if identity.Token != nil && m.revocations != nil {
		revoked, err := m.revocations.TokenRevoked(r.Context(), identity.Token.Claims.Issuer, identity.Token.Claims.TokenID)
		if err != nil {
			writeCredentialStatusError(w, http.StatusServiceUnavailable, "v1/auth/credential_status_unavailable", "credential status unavailable", "restore credential status storage before retrying the same request", true)
			return nil, false
		}
		if revoked {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeCredentialStatusError(w, http.StatusUnauthorized, "v1/auth/token_revoked", "token revoked", "obtain a new authorized credential; do not retry this token", false)
			return nil, false
		}
	}
	return identity, true
}

// WithRevocationChecker configures the checker before handlers start serving.
func (m *Middleware) WithRevocationChecker(checker RevocationChecker) *Middleware {
	m.revocations = checker
	return m
}

// isScopedToken reports whether tok advertises anything less than full access:
// a non-empty sandbox Scope, or a Commands claim that does not include "*".
func isScopedToken(tok *Token) bool {
	return !tok.IsFullAccess()
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

func writeCredentialStatusError(w http.ResponseWriter, status int, code, message, action string, retryable bool) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	requirement := "daemon_credential"
	if code == "v1/auth/credential_status_unavailable" {
		requirement = "credential_status_store"
	}
	_ = json.NewEncoder(w).Encode(struct {
		Error       string `json:"error"`
		Message     string `json:"message"`
		Code        string `json:"code"`
		Action      string `json:"action"`
		Retryable   bool   `json:"retryable"`
		State       string `json:"state"`
		Requirement string `json:"requirement"`
	}{Error: message, Message: message, Code: code, Action: action, Retryable: retryable, State: "blocked", Requirement: requirement})
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
