// Package dashboard implements a web dashboard for AgentLab sandbox management.
//
// It provides a browser-based UI for viewing and managing sandboxes, jobs,
// workspaces, and exposures. The dashboard connects to the agentlabd daemon
// via its Unix socket and proxies API requests to the daemon's ControlAPI.
//
// # Inbound security model (review C2)
//
// By default the dashboard binds to loopback only, where it trusts the local
// browser. A non-loopback bind requires an inbound browser token
// (--browser-token); the server refuses to start otherwise, because anyone who
// can reach it could otherwise drive its destructive proxy (create/destroy
// sandboxes, stop_all, prune, exposures).
//
// Two protections apply to /api/* regardless of bind address:
//
//   - Browser token: when configured, every /api/* request must carry it
//     (Authorization: Bearer, X-Dashboard-Token, or dashboard_token cookie).
//     The frontend obtains it from the user at runtime and keeps it in
//     sessionStorage; it is never embedded in the shipped JavaScript.
//
//   - CSRF/Origin: state-changing requests (POST/PUT/PATCH/DELETE) must carry
//     a same-origin Origin (or Referer) header and the custom X-Requested-With
//     header, which a plain cross-site form submission cannot set.
//
// The dashboard-to-daemon hop is over the local Unix socket (a trusted path)
// and uses the outbound --token; that token is not what protects the browser
// connection.
package dashboard

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

//go:embed all:static
var staticFiles embed.FS

// Server is the dashboard HTTP server.
type Server struct {
	listen       string
	socketPath   string
	token        string
	browserToken string
	logger       *log.Logger
}

// Config holds dashboard server configuration.
type Config struct {
	// Listen is the address to bind (e.g. "127.0.0.1:8080").
	Listen string

	// SocketPath is the path to the agentlabd Unix socket.
	SocketPath string

	// Token is the bearer token for daemon (outbound) authentication.
	Token string

	// BrowserToken, when set, gates every /api/* request: the browser must
	// supply it (Authorization: Bearer, X-Dashboard-Token, or
	// dashboard_token cookie). It is required for non-loopback binds; loopback
	// binds may omit it (review C2).
	BrowserToken string
}

// NewServer creates a new dashboard server.
func NewServer(cfg Config, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	return &Server{
		listen:       cfg.Listen,
		socketPath:   cfg.SocketPath,
		token:        cfg.Token,
		browserToken: strings.TrimSpace(cfg.BrowserToken),
		logger:       logger,
	}
}

// validateConfig enforces the inbound trust model before the server binds. A
// non-loopback listener without a browser token is refused: the dashboard
// proxies destructive operations and has no other inbound gate. Loopback binds
// may omit the token (review C2).
func (s *Server) validateConfig() error {
	if !isLoopbackListen(s.listen) && s.browserToken == "" {
		return fmt.Errorf("dashboard: --browser-token is required for non-loopback bind %q "+
			"(loopback binds may omit it; otherwise place the dashboard behind an authenticating "+
			"encrypted reverse proxy)", s.listen)
	}
	return nil
}

// ListenAndServe starts the dashboard HTTP server.
func (s *Server) ListenAndServe(ctx context.Context) error {
	if err := s.validateConfig(); err != nil {
		return err
	}

	mux := http.NewServeMux()

	// Serve embedded static files.
	mux.HandleFunc("/", s.handleStatic)

	// API proxy endpoints — forward to daemon.
	mux.HandleFunc("/api/v1/status", s.proxyGet)
	mux.HandleFunc("/api/v1/sandboxes/inventory", s.proxyGet)
	mux.HandleFunc("/api/v1/sandboxes/reconcile", s.proxyPost)
	mux.HandleFunc("/api/v1/sandboxes/validate-plan", s.proxyPost)
	mux.HandleFunc("/api/v1/sandboxes/stop_all", s.proxyPost)
	mux.HandleFunc("/api/v1/sandboxes/prune", s.proxyPost)
	mux.HandleFunc("/api/v1/sandboxes", s.proxySandboxes)
	mux.HandleFunc("/api/v1/sandboxes/", s.proxySandboxAction)
	mux.HandleFunc("/api/v1/jobs/validate-plan", s.proxyPost)
	mux.HandleFunc("/api/v1/jobs", s.proxyJobs)
	mux.HandleFunc("/api/v1/jobs/", s.proxyGet)
	mux.HandleFunc("/api/v1/workspaces", s.proxyGet)
	mux.HandleFunc("/api/v1/workspaces/", s.proxyWorkspaceAction)
	mux.HandleFunc("/api/v1/profiles", s.proxyGet)
	mux.HandleFunc("/api/v1/sessions", s.proxyGet)
	mux.HandleFunc("/api/v1/sessions/", s.proxySessionAction)
	mux.HandleFunc("/api/v1/exposures", s.proxyExposures)
	mux.HandleFunc("/api/v1/exposures/", s.proxyDelete)
	mux.HandleFunc("/api/v1/messages", s.proxyMessages)
	mux.HandleFunc("/api/v1/host", s.proxyGet)
	mux.HandleFunc("/api/v1/pool/status", s.proxyGet)

	srv := &http.Server{
		Handler:           s.inboundMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	listener, err := net.Listen("tcp", s.listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.listen, err)
	}

	s.logger.Printf("dashboard: listening on %s (socket=%s)", s.listen, s.socketPath)
	if !isLoopbackListen(s.listen) {
		// validateConfig already guaranteed browserToken is set for non-loopback.
		s.logger.Printf("dashboard: %s binds to a non-loopback interface; inbound browser token is enabled. "+
			"Ensure TLS or a trusted encrypted tunnel terminates in front of it, since the token travels "+
			"in cleartext over HTTP otherwise (see docs/review-2026-08-11.md C2).", s.listen)
	} else if s.browserToken != "" {
		s.logger.Printf("dashboard: inbound browser token is enabled")
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(listener) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// handleStatic serves embedded static files or index.html.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}

	// Serve from embedded static directory.
	data, err := staticFiles.ReadFile("static" + path)
	if err != nil {
		// Fallback to index.html for SPA routing.
		data, err = staticFiles.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
	}

	w.Header().Set("Content-Type", contentType(path))
	w.Write(data)
}

// proxyGet forwards a GET request to the daemon.
func (s *Server) proxyGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	s.forward(w, r.Method, daemonPath(r.URL.Path), nil)
}

// maxForwardBytes caps the size of a request body the dashboard buffers before
// forwarding it to the daemon (review M4). It bounds per-request memory; the
// server ReadTimeout separately bounds slow-body connection exhaustion.
const maxForwardBytes = 2 << 20 // 2 MiB

// readBoundedBody reads up to maxForwardBytes from r.Body. It writes a 413
// response (without forwarding) when the body exceeds the cap, and a 400 on any
// other read failure; ok is false in both cases (review M4).
func (s *Server) readBoundedBody(w http.ResponseWriter, r *http.Request) (body []byte, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxForwardBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
			return nil, false
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read request body"})
		return nil, false
	}
	return body, true
}

// proxyPost forwards a POST request to the daemon.
func (s *Server) proxyPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	body, ok := s.readBoundedBody(w, r)
	if !ok {
		return
	}
	s.forward(w, r.Method, daemonPath(r.URL.Path), body)
}

// proxySandboxes handles GET (list) and POST (create) for /api/v1/sandboxes.
func (s *Server) proxySandboxes(w http.ResponseWriter, r *http.Request) {
	// Strip the /api prefix to get /v1/sandboxes.
	switch r.Method {
	case http.MethodGet:
		s.forward(w, r.Method, "/v1/sandboxes", nil)
	case http.MethodPost:
		body, ok := s.readBoundedBody(w, r)
		if !ok {
			return
		}
		s.forward(w, r.Method, "/v1/sandboxes", body)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// proxySandboxAction forwards requests to /v1/sandboxes/{vmid}/...
func (s *Server) proxySandboxAction(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readBoundedBody(w, r)
	if !ok {
		return
	}
	s.forward(w, r.Method, daemonPath(r.URL.Path), body)
}

// proxyJobs handles GET (list) and POST (create) for /api/v1/jobs.
func (s *Server) proxyJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.forward(w, r.Method, "/v1/jobs", nil)
	case http.MethodPost:
		body, ok := s.readBoundedBody(w, r)
		if !ok {
			return
		}
		s.forward(w, r.Method, "/v1/jobs", body)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// proxyWorkspaceAction forwards requests to /v1/workspaces/{id}/...
func (s *Server) proxyWorkspaceAction(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readBoundedBody(w, r)
	if !ok {
		return
	}
	s.forward(w, r.Method, daemonPath(r.URL.Path), body)
}

// proxySessionAction forwards requests to /v1/sessions/{id}/...
func (s *Server) proxySessionAction(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readBoundedBody(w, r)
	if !ok {
		return
	}
	s.forward(w, r.Method, daemonPath(r.URL.Path), body)
}

// proxyExposures handles GET (list), POST (create), DELETE for /api/v1/exposures.
func (s *Server) proxyExposures(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readBoundedBody(w, r)
	if !ok {
		return
	}
	s.forward(w, r.Method, daemonPath(r.URL.Path), body)
}

// proxyDelete forwards a DELETE request to the daemon.
func (s *Server) proxyDelete(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readBoundedBody(w, r)
	if !ok {
		return
	}
	s.forward(w, r.Method, daemonPath(r.URL.Path), body)
}

// proxyMessages handles GET (list) and POST (create) for /api/v1/messages.
func (s *Server) proxyMessages(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readBoundedBody(w, r)
	if !ok {
		return
	}
	s.forward(w, r.Method, daemonPath(r.URL.Path), body)
}

// daemonPath strips the /api prefix from the URL path, mapping dashboard
// paths to daemon paths: /api/v1/status -> /v1/status.
func daemonPath(p string) string {
	return strings.TrimPrefix(p, "/api")
}

// forward sends a request to the daemon via Unix socket.
func (s *Server) forward(w http.ResponseWriter, method, path string, body []byte) {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", s.socketPath)
			},
		},
		Timeout: 30 * time.Second,
	}

	url := "http://unix" + path
	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = strings.NewReader(string(body))
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		s.logger.Printf("dashboard: error creating request: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		s.logger.Printf("dashboard: error forwarding request: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "daemon connection failed: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	// Copy response headers.
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// DefaultSocketPath returns the default daemon socket path.
func DefaultSocketPath() string {
	if p := os.Getenv("AGENTLABD_SOCKET"); p != "" {
		return p
	}
	return "/run/agentlab/agentlabd.sock"
}

// isLoopbackListen reports whether addr binds only to the loopback interface.
// An empty host (":8080") or a wildcard ("0.0.0.0", "[::]") is NOT loopback.
func isLoopbackListen(addr string) bool {
	host := strings.TrimSpace(addr)
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = strings.TrimSpace(h)
	}
	switch host {
	case "", "0.0.0.0", "[::]", "::":
		return false
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	return net.ParseIP(host).IsLoopback()
}

// inboundMiddleware gates /api/* requests with the browser token (when
// configured) and CSRF/Origin checks for state-changing methods. Static UI
// assets (the HTML/JS/CSS needed to even present a login) are served without
// inbound auth.
func (s *Server) inboundMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api" && !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if s.browserToken != "" && !s.requestHasBrowserToken(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "dashboard token required"})
			return
		}
		if isStateChanging(r.Method) && !s.csrfOK(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-site request rejected"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isStateChanging reports whether the HTTP method can mutate server state and
// therefore requires CSRF protection.
func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// csrfOK validates that a state-changing request originated from the
// dashboard's own origin. It requires the custom X-Requested-With header
// (which a plain cross-site form submission cannot set) AND a same-origin
// Origin or Referer header. This defends even loopback binds, which are
// otherwise reachable by cross-site requests driven from a malicious page.
func (s *Server) csrfOK(r *http.Request) bool {
	if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return hostEqual(u.Host, r.Host)
}

// hostEqual compares two host[:port] strings case-insensitively, treating
// missing and default ports as equivalent (so :80/443 match a bare host).
func hostEqual(a, b string) bool {
	return canonicalHost(a) == canonicalHost(b)
}

// canonicalHost lowercases the host and strips a default port for the scheme
// implied by nothing here — we only normalize the well-known defaults so that
// "127.0.0.1:80" and "127.0.0.1" compare equal.
func canonicalHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	host, port, err := net.SplitHostPort(h)
	if err != nil {
		return h
	}
	switch port {
	case "80", "443":
		return host
	}
	return h
}

// requestHasBrowserToken reports whether the request carries the configured
// inbound browser token in any of the accepted locations. Comparison is
// constant-time.
func (s *Server) requestHasBrowserToken(r *http.Request) bool {
	want := []byte(s.browserToken)
	if len(want) == 0 {
		return false
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(h), "bearer ") {
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(h[len("Bearer "):])), want) == 1 {
			return true
		}
	}
	if h := r.Header.Get("X-Dashboard-Token"); h != "" {
		if subtle.ConstantTimeCompare([]byte(h), want) == 1 {
			return true
		}
	}
	if c, err := r.Cookie("dashboard_token"); err == nil && c.Value != "" {
		if subtle.ConstantTimeCompare([]byte(c.Value), want) == 1 {
			return true
		}
	}
	return false
}

// contentType returns the Content-Type for a file path.
func contentType(path string) string {
	switch {
	case strings.HasSuffix(path, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(path, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(path, ".js"):
		return "application/javascript; charset=utf-8"
	case strings.HasSuffix(path, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(path, ".png"):
		return "image/png"
	case strings.HasSuffix(path, ".ico"):
		return "image/x-icon"
	default:
		return "application/octet-stream"
	}
}
