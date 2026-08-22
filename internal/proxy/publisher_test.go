package proxy

import (
	"context"
	"strings"
	"testing"
)

func TestSanitizeSubdomain(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"mybox", "mybox"},
		{"MyBox", "mybox"},
		{"my_box", "my-box"},
		{"my box", "my-box"},
		{"my-box", "my-box"},
		{"my.box", "mybox"},
		{"my@box!", "mybox"},
		{"  mybox  ", "mybox"},
		{"-leading", "leading"},
		{"trailing-", "trailing"},
		{"", "sandbox"},
		{"  ", "sandbox"},
		{"123sandbox", "123sandbox"},
		{"a-b_c.d!", "a-b-cd"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := SanitizeSubdomain(tc.input)
			if result != tc.expected {
				t.Errorf("SanitizeSubdomain(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

// TestValidateTargetIP covers the address classes an exposure may never
// publish to (review F2, task T06).
func TestValidateTargetIP(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{"agent subnet host", "10.77.0.5", ""},
		{"other private host", "192.168.1.10", ""},
		{"public host", "8.8.8.8", ""},
		{"ipv6 agent host", "fd00:77::5", ""},
		{"ipv4 loopback", "127.0.0.1", "loopback"},
		{"ipv6 loopback", "::1", "loopback"},
		{"ipv4 link-local", "169.254.169.254", "link-local"},
		{"ipv6 link-local", "fe80::1", "link-local"},
		{"unspecified v4", "0.0.0.0", "unspecified"},
		{"unspecified v6", "::", "unspecified"},
		{"multicast v4", "239.1.1.1", "multicast"},
		{"multicast v6", "ff0e::1", "multicast"},
		{"broadcast", "255.255.255.255", "broadcast"},
		{"invalid", "not-an-ip", "not a valid ip"},
		{"zoned", "fe80::1%eth0", "not a valid ip"},
		{"empty", "", "not a valid ip"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTargetIP(tc.input)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateTargetIP(%q) = %v, want nil", tc.input, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateTargetIP(%q) = nil, want error", tc.input)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("ValidateTargetIP(%q) error = %q, want it to mention %q", tc.input, err, tc.wantErr)
			}
		})
	}
}

// TestCaddyPublisherRejectsLocalTarget verifies the publisher refuses a
// daemon-local target before it touches DNS, certificates, or the Caddy API.
func TestCaddyPublisherRejectsLocalTarget(t *testing.T) {
	publisher, err := NewCaddyPublisher(ProxyConfig{Domain: "example.com"}, nil)
	if err != nil {
		t.Fatalf("NewCaddyPublisher: %v", err)
	}

	if _, err := publisher.Publish(context.Background(), "mybox", "127.0.0.1", 8080); err == nil {
		t.Fatal("Publish(127.0.0.1) = nil error, want rejection")
	} else if !strings.Contains(err.Error(), "loopback") {
		t.Errorf("Publish(127.0.0.1) error = %q, want it to mention loopback", err)
	}
}

func TestResolveProxyIP(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"loopback", "127.0.0.1", "127.0.0.1"},
		{"subnet_host", "10.77.0.5", "10.77.0.1"},
		{"class_c", "192.168.1.100", "192.168.1.1"},
		{"invalid", "not-an-ip", "127.0.0.1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := resolveProxyIP(tc.input)
			if result != tc.expected {
				t.Errorf("resolveProxyIP(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestRouteID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"mybox.agentlab.local", "mybox-agentlab-local"},
		{"simple", "simple"},
		{"a.b.c", "a-b-c"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := routeID(tc.input)
			if result != tc.expected {
				t.Errorf("routeID(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}
