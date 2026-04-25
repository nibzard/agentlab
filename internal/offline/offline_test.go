package offline

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsPrivateHost(t *testing.T) {
	tests := []struct {
		host     string
		expected bool
	}{
		// Loopback
		{"127.0.0.1", true},
		{"127.0.0.100", true},
		{"::1", true},
		{"localhost", true},
		// RFC 1918 private
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"192.168.0.100", true},
		// Link-local
		{"169.254.169.254", true},
		{"169.254.0.1", true},
		// Unique local IPv6
		{"fc00::1", true},
		{"fd00::1", true},
		// Link-local IPv6
		{"fe80::1", true},
		// Unix socket paths
		{"/var/run/docker.sock", true},
		{"/run/agentlab/agentlabd.sock", true},
		{"unix:///var/run/docker.sock", true}, // contains / so treated as path
		// Local domains
		{"myhost.local", true},
		{"sandbox.internal", true},
		// Public / external (not resolvable in test, treated as external)
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"203.0.113.1", false},
		// Empty
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := IsPrivateHost(tt.host)
			if got != tt.expected {
				t.Errorf("IsPrivateHost(%q) = %v, want %v", tt.host, got, tt.expected)
			}
		})
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip       string
		expected bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"::1", true},
		{"fc00::1", true},
		{"fe80::1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"203.0.113.1", false},
		{"104.16.0.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP %q", tt.ip)
			}
			got := isPrivateIP(ip)
			if got != tt.expected {
				t.Errorf("isPrivateIP(%s) = %v, want %v", tt.ip, got, tt.expected)
			}
		})
	}
}

func TestIsPrivateURL(t *testing.T) {
	tests := []struct {
		url      string
		expected bool
	}{
		{"http://localhost:8080/api", true},
		{"http://127.0.0.1:8844/v1/identity", true},
		{"http://10.77.0.1:8844/v1/identity", true},
		{"http://169.254.169.254/proxy/llm/v1/chat/completions", true},
		{"http://192.168.1.100:3000/dashboard", true},
		{"https://api.openai.com/v1/chat/completions", false},
		{"https://api.anthropic.com/v1/messages", false},
		{"http://8.8.8.8:80/query", false},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := IsPrivateURL(tt.url)
			if got != tt.expected {
				t.Errorf("IsPrivateURL(%q) = %v, want %v", tt.url, got, tt.expected)
			}
		})
	}
}

func TestOfflineTransport_BlocksExternal(t *testing.T) {
	transport := NewOfflineTransport(http.DefaultTransport)

	req := httptest.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
	resp, err := transport.RoundTrip(req)

	if err == nil {
		t.Error("expected error for external URL, got nil")
		if resp != nil {
			resp.Body.Close()
		}
		return
	}
	blocked, ok := err.(ErrBlocked)
	if !ok {
		t.Errorf("expected ErrBlocked, got %T: %v", err, err)
		return
	}
	if blocked.Host != "api.openai.com" {
		t.Errorf("ErrBlocked.Host = %q, want %q", blocked.Host, "api.openai.com")
	}
}

func TestOfflineTransport_AllowsLocal(t *testing.T) {
	// Start a local test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	transport := NewOfflineTransport(http.DefaultTransport)

	req := httptest.NewRequest(http.MethodGet, server.URL, nil)
	resp, err := transport.RoundTrip(req)

	if err != nil {
		t.Errorf("expected no error for local server, got: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestErrBlocked(t *testing.T) {
	err := ErrBlocked{Host: "api.example.com"}
	msg := err.Error()
	if msg != "offline mode: external network call blocked to api.example.com" {
		t.Errorf("unexpected error message: %q", msg)
	}
}

func TestExtractHost(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"http://localhost:8080/api", "localhost"},
		{"https://api.openai.com/v1/models", "api.openai.com"},
		{"http://10.0.0.1:8844/v1/identity", "10.0.0.1"},
		{"http://169.254.169.254/proxy/llm", "169.254.169.254"},
		{"http://[::1]:8080/api", "::1"},
		{"http://example.com/path?q=1", "example.com"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := extractHost(tt.url)
			if got != tt.expected {
				t.Errorf("extractHost(%q) = %q, want %q", tt.url, got, tt.expected)
			}
		})
	}
}
