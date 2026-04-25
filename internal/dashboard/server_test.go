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

	// Wait for socket to be ready.
	time.Sleep(50 * time.Millisecond)

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
