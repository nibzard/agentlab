// Package dashboard implements a web dashboard for AgentLab sandbox management.
//
// It provides a browser-based UI for viewing and managing sandboxes, jobs,
// workspaces, and exposures. The dashboard connects to the agentlabd daemon
// via its Unix socket and proxies API requests to the daemon's ControlAPI.
//
// The dashboard is an optional component started with:
//
//	agentlab-dashboard --listen :8080 --socket /run/agentlab/agentlabd.sock
package dashboard

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

//go:embed all:static
var staticFiles embed.FS

// Server is the dashboard HTTP server.
type Server struct {
	listen     string
	socketPath string
	token      string
	logger     *log.Logger
}

// Config holds dashboard server configuration.
type Config struct {
	// Listen is the address to bind (e.g. ":8080").
	Listen string

	// SocketPath is the path to the agentlabd Unix socket.
	SocketPath string

	// Token is the bearer token for daemon authentication.
	Token string
}

// NewServer creates a new dashboard server.
func NewServer(cfg Config, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	return &Server{
		listen:     cfg.Listen,
		socketPath: cfg.SocketPath,
		token:      cfg.Token,
		logger:     logger,
	}
}

// ListenAndServe starts the dashboard HTTP server.
func (s *Server) ListenAndServe(ctx context.Context) error {
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

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	listener, err := net.Listen("tcp", s.listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.listen, err)
	}

	s.logger.Printf("dashboard: listening on %s (socket=%s)", s.listen, s.socketPath)

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

// proxyPost forwards a POST request to the daemon.
func (s *Server) proxyPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	body, _ := io.ReadAll(r.Body)
	s.forward(w, r.Method, daemonPath(r.URL.Path), body)
}

// proxySandboxes handles GET (list) and POST (create) for /api/v1/sandboxes.
func (s *Server) proxySandboxes(w http.ResponseWriter, r *http.Request) {
	// Strip the /api prefix to get /v1/sandboxes.
	switch r.Method {
	case http.MethodGet:
		s.forward(w, r.Method, "/v1/sandboxes", nil)
	case http.MethodPost:
		body, _ := io.ReadAll(r.Body)
		s.forward(w, r.Method, "/v1/sandboxes", body)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// proxySandboxAction forwards requests to /v1/sandboxes/{vmid}/...
func (s *Server) proxySandboxAction(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.forward(w, r.Method, daemonPath(r.URL.Path), body)
}

// proxyJobs handles GET (list) and POST (create) for /api/v1/jobs.
func (s *Server) proxyJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.forward(w, r.Method, "/v1/jobs", nil)
	case http.MethodPost:
		body, _ := io.ReadAll(r.Body)
		s.forward(w, r.Method, "/v1/jobs", body)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// proxyWorkspaceAction forwards requests to /v1/workspaces/{id}/...
func (s *Server) proxyWorkspaceAction(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.forward(w, r.Method, daemonPath(r.URL.Path), body)
}

// proxySessionAction forwards requests to /v1/sessions/{id}/...
func (s *Server) proxySessionAction(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.forward(w, r.Method, daemonPath(r.URL.Path), body)
}

// proxyExposures handles GET (list), POST (create), DELETE for /api/v1/exposures.
func (s *Server) proxyExposures(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.forward(w, r.Method, daemonPath(r.URL.Path), body)
}

// proxyDelete forwards a DELETE request to the daemon.
func (s *Server) proxyDelete(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.forward(w, r.Method, daemonPath(r.URL.Path), body)
}

// proxyMessages handles GET (list) and POST (create) for /api/v1/messages.
func (s *Server) proxyMessages(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
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
