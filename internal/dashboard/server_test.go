package dashboard

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testServer(socketPath string) *Server {
	return NewServer(Config{Listen: ":0", SocketPath: socketPath}, log.New(io.Discard, "", 0))
}

func TestContentType(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/index.html", "text/html; charset=utf-8"},
		{"/assets/style.css", "text/css; charset=utf-8"},
		{"/assets/app.js", "application/javascript; charset=utf-8"},
		{"/icon.svg", "image/svg+xml"},
		{"/unknown.bin", "application/octet-stream"},
	}
	for _, tt := range tests {
		got := contentType(tt.path)
		if got != tt.want {
			t.Errorf("contentType(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestIsLoopbackListen(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"localhost:8080", true},
		{"[::1]:8080", true},
		{":8080", false},       // all interfaces — NOT loopback
		{"0.0.0.0:8080", false},
		{"[::]:8080", false},
		{"10.0.0.5:8080", false},
	}
	for _, tc := range cases {
		if got := isLoopbackListen(tc.addr); got != tc.want {
			t.Errorf("isLoopbackListen(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

func TestDaemonPath(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"/api/v1/status", "/v1/status"},
		{"/api/v1/sandboxes", "/v1/sandboxes"},
		{"/api/v1/sandboxes/1001/start", "/v1/sandboxes/1001/start"},
		{"/v1/status", "/v1/status"}, // no /api prefix, passes through
	}
	for _, tt := range tests {
		got := daemonPath(tt.in)
		if got != tt.want {
			t.Errorf("daemonPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDefaultSocketPath(t *testing.T) {
	orig := os.Getenv("AGENTLABD_SOCKET")
	defer os.Setenv("AGENTLABD_SOCKET", orig)

	os.Setenv("AGENTLABD_SOCKET", "")
	if got := DefaultSocketPath(); got != "/run/agentlab/agentlabd.sock" {
		t.Errorf("DefaultSocketPath() = %q, want default", got)
	}

	os.Setenv("AGENTLABD_SOCKET", "/tmp/test.sock")
	if got := DefaultSocketPath(); got != "/tmp/test.sock" {
		t.Errorf("DefaultSocketPath() = %q, want /tmp/test.sock", got)
	}
}

func TestHandleStaticIndex(t *testing.T) {
	srv := testServer("/nonexistent")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.handleStatic(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET / returned %d, want 200", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body := w.Body.String()
	if body == "" {
		t.Error("body is empty")
	}
}

func TestHandleStaticCSS(t *testing.T) {
	srv := testServer("/nonexistent")
	req := httptest.NewRequest(http.MethodGet, "/assets/style.css", nil)
	w := httptest.NewRecorder()
	srv.handleStatic(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /assets/style.css returned %d, want 200", w.Code)
	}
}

func TestHandleStaticNotFound(t *testing.T) {
	srv := testServer("/nonexistent")
	req := httptest.NewRequest(http.MethodGet, "/nonexistent-page", nil)
	w := httptest.NewRecorder()
	srv.handleStatic(w, req)

	// Should fall back to index.html (SPA routing).
	if w.Code != http.StatusOK {
		t.Errorf("GET /nonexistent-page returned %d, want 200 (index fallback)", w.Code)
	}
}

func TestProxyForward(t *testing.T) {
	// Create a fake daemon server listening on a Unix socket.
	tmpDir, err := os.MkdirTemp("", "agentlab-dashboard-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	socketPath := fmt.Sprintf("%s/test.sock", tmpDir)

	// Fake daemon that returns a simple JSON response.
	fakeDaemon := http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/status" {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"sandboxes": map[string]int{"running": 2},
					"jobs":      map[string]int{"queued": 1},
				})
				return
			}
			if r.URL.Path == "/v1/sandboxes" && r.Method == http.MethodPost {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"vmid":  1001,
					"name":  body["name"],
					"state": "requested",
				})
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}),
	}

	unixListener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	go fakeDaemon.Serve(unixListener)
	defer fakeDaemon.Close()

	// Wait for the Unix socket to accept connections (readiness poll, not a
	// fixed sleep — the listener is bound, but Serve must be accepting).
	deadline := time.Now().Add(2 * time.Second)
	for {
		if c, derr := net.Dial("unix", socketPath); derr == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake daemon socket %s not ready within 2s", socketPath)
		}
		time.Sleep(5 * time.Millisecond)
	}

	srv := testServer(socketPath)

	// Test GET proxy.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	srv.proxyGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("proxyGet /v1/status returned %d, want 200", w.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	sandboxes, ok := resp["sandboxes"].(map[string]any)
	if !ok {
		t.Fatal("response missing sandboxes field")
	}
	if sandboxes["running"].(float64) != 2 {
		t.Errorf("sandboxes.running = %v, want 2", sandboxes["running"])
	}

	// Test POST proxy.
	body := `{"name":"test-sandbox","profile":"default"}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", io.NopCloser(strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.proxySandboxes(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("proxySandboxes POST returned %d, want 200", w.Code)
	}

	// Test method not allowed.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/status", nil)
	w = httptest.NewRecorder()
	srv.proxyGet(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("proxyGet DELETE returned %d, want 405", w.Code)
	}
}

func TestProxyForwardDaemonDown(t *testing.T) {
	srv := testServer("/nonexistent-socket")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	srv.proxyGet(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("proxyGet with daemon down returned %d, want 502", w.Code)
	}
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, map[string]string{"hello": "world"})

	if w.Code != http.StatusOK {
		t.Errorf("writeJSON status = %d, want 200", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp["hello"] != "world" {
		t.Errorf("hello = %q, want world", resp["hello"])
	}
}

// gatedHandler wraps a recording handler with the dashboard's inbound
// middleware, for exercising browser-token and CSRF/Origin checks in isolation.
func gatedHandler(t *testing.T, cfg Config) (http.Handler, *bool) {
	t.Helper()
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	srv := NewServer(cfg, log.New(io.Discard, "", 0))
	return srv.inboundMiddleware(inner), &called
}

func TestValidateConfig_LoopbackNoTokenOK(t *testing.T) {
	// Loopback binds may omit the browser token (trusted local browser).
	srv := NewServer(Config{Listen: "127.0.0.1:8080"}, log.New(io.Discard, "", 0))
	if err := srv.validateConfig(); err != nil {
		t.Errorf("loopback without token: got %v, want nil", err)
	}
}

func TestValidateConfig_NonLoopbackRequiresToken(t *testing.T) {
	cases := []struct{ addr string }{
		{":8080"},      // all interfaces
		{"0.0.0.0:8080"},
		{"[::]:8080"},
		{"10.0.0.5:8080"},
	}
	for _, tc := range cases {
		srv := NewServer(Config{Listen: tc.addr}, log.New(io.Discard, "", 0))
		if err := srv.validateConfig(); err == nil {
			t.Errorf("non-loopback %q without browser token: want error, got nil", tc.addr)
		}
		// Supplying a token clears the failure.
		srv = NewServer(Config{Listen: tc.addr, BrowserToken: "sekrit"}, log.New(io.Discard, "", 0))
		if err := srv.validateConfig(); err != nil {
			t.Errorf("non-loopback %q with token: got %v, want nil", tc.addr, err)
		}
	}
}

func TestInboundMiddleware_StaticPassthrough(t *testing.T) {
	// Static UI assets must load without the token (otherwise the login page
	// could not be presented).
	h, called := gatedHandler(t, Config{Listen: "127.0.0.1:8080", BrowserToken: "sekrit"})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !*called {
		t.Errorf("GET / with token configured: code=%d called=%v, want 200/true (static passthrough)", w.Code, *called)
	}
}

func TestInboundMiddleware_AnonymousRejectedWhenTokenConfigured(t *testing.T) {
	h, called := gatedHandler(t, Config{Listen: "127.0.0.1:8080", BrowserToken: "sekrit"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous /api request: code=%d, want 401", w.Code)
	}
	if *called {
		t.Error("backend handler ran for an unauthenticated request")
	}
}

func TestInboundMiddleware_TokenAccepted(t *testing.T) {
	// The token may arrive via Authorization, X-Dashboard-Token, or cookie.
	h, called := gatedHandler(t, Config{Listen: "127.0.0.1:8080", BrowserToken: "sekrit"})

	cases := []struct {
		name string
		mod  func(*http.Request)
	}{
		{"authorization bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer sekrit") }},
		{"x-dashboard-token", func(r *http.Request) { r.Header.Set("X-Dashboard-Token", "sekrit") }},
		{"cookie", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "dashboard_token", Value: "sekrit"}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			*called = false
			req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
			tc.mod(req)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("code=%d, want 200", w.Code)
			}
			if !*called {
				t.Error("backend handler did not run for an authenticated request")
			}
		})
	}
}

func TestInboundMiddleware_WrongTokenRejected(t *testing.T) {
	h, called := gatedHandler(t, Config{Listen: "127.0.0.1:8080", BrowserToken: "sekrit"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: code=%d, want 401", w.Code)
	}
	if *called {
		t.Error("backend handler ran for a wrong-token request")
	}
}

func TestInboundMiddleware_CSRF_BlocksCrossSitePOST(t *testing.T) {
	// No browser token (loopback trusted). A cross-site POST must still be
	// blocked by the Origin/X-Requested-With check.
	h, called := gatedHandler(t, Config{Listen: "127.0.0.1:8080"})

	// No X-Requested-With, no Origin → rejected.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", strings.NewReader("{}"))
	req.Host = "127.0.0.1:8080"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || *called {
		t.Errorf("POST without CSRF headers: code=%d called=%v, want 403/false", w.Code, *called)
	}

	// X-Requested-With present but cross-site Origin → rejected.
	*called = false
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", strings.NewReader("{}"))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || *called {
		t.Errorf("POST with cross-site Origin: code=%d called=%v, want 403/false", w.Code, *called)
	}
}

func TestInboundMiddleware_CSRF_AllowsSameOriginPOST(t *testing.T) {
	h, called := gatedHandler(t, Config{Listen: "127.0.0.1:8080"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", strings.NewReader("{}"))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !*called {
		t.Errorf("same-origin POST: code=%d called=%v, want 200/true", w.Code, *called)
	}
}

func TestInboundMiddleware_GETNeedsNoCSRFHeader(t *testing.T) {
	// Safe methods must not require X-Requested-With/Origin.
	h, called := gatedHandler(t, Config{Listen: "127.0.0.1:8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !*called {
		t.Errorf("GET without CSRF headers: code=%d called=%v, want 200/true", w.Code, *called)
	}
}

func TestHostEqual(t *testing.T) {
	cases := []struct{ a, b string; want bool }{
		{"127.0.0.1:8080", "127.0.0.1:8080", true},
		{"127.0.0.1:80", "127.0.0.1", true},   // default port stripped
		{"127.0.0.1:8080", "127.0.0.1:9090", false},
		{"127.0.0.1:8080", "evil.example", false},
		{"LOCALhost:8080", "localhost:8080", true}, // case-insensitive
	}
	for _, tc := range cases {
		if got := hostEqual(tc.a, tc.b); got != tc.want {
			t.Errorf("hostEqual(%q,%q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestReadBoundedBody_OversizedReturns413 verifies the dashboard caps buffered
// bodies and returns 413 without emitting the body (review M4).
func TestReadBoundedBody_OversizedReturns413(t *testing.T) {
	srv := testServer("")
	oversized := strings.Repeat("x", maxForwardBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", strings.NewReader(oversized))
	w := httptest.NewRecorder()
	body, ok := srv.readBoundedBody(w, req)
	if ok {
		t.Fatal("expected ok=false for oversized body")
	}
	if body != nil {
		t.Fatal("expected no body returned for oversized request")
	}
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
}

// TestReadBoundedBody_ChunkedOversizedReturns413 verifies the cap applies to
// chunked transfer encoding too (review M4).
func TestReadBoundedBody_ChunkedOversizedReturns413(t *testing.T) {
	srv := testServer("")
	oversized := strings.Repeat("x", maxForwardBytes+1024)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", strings.NewReader(oversized))
	req.TransferEncoding = []string{"chunked"}
	w := httptest.NewRecorder()
	if _, ok := srv.readBoundedBody(w, req); ok {
		t.Fatal("expected ok=false for chunked oversized body")
	}
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
}

// TestReadBoundedBody_ReadErrorReturns400 verifies a non-overflow body read
// failure yields 400 (review M4).
func TestReadBoundedBody_ReadErrorReturns400(t *testing.T) {
	srv := testServer("")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", errReader{err: io.ErrUnexpectedEOF})
	w := httptest.NewRecorder()
	if _, ok := srv.readBoundedBody(w, req); ok {
		t.Fatal("expected ok=false on read error")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// TestProxyPost_OversizedDoesNotForward verifies an oversized body short-
// circuits before the daemon is contacted (review M4).
func TestProxyPost_OversizedDoesNotForward(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agentlab-dashboard-m4-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	socketPath := tmpDir + "/test.sock"

	var forwarded int32
	fakeDaemon := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&forwarded, 1)
		w.WriteHeader(http.StatusOK)
	})}
	unixListener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	go fakeDaemon.Serve(unixListener)
	defer fakeDaemon.Close()
	for deadline := time.Now().Add(2 * time.Second); ; {
		if c, derr := net.Dial("unix", socketPath); derr == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake daemon not ready")
		}
		time.Sleep(5 * time.Millisecond)
	}

	srv := testServer(socketPath)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", strings.NewReader(strings.Repeat("x", maxForwardBytes+1)))
	w := httptest.NewRecorder()
	srv.proxyPost(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
	if got := atomic.LoadInt32(&forwarded); got != 0 {
		t.Fatalf("daemon was forwarded %d times; expected none", got)
	}
}

type errReader struct{ err error }

func (r errReader) Read(p []byte) (int, error) { return 0, r.err }
