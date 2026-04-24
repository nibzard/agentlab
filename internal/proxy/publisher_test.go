package proxy

import (
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
			result := sanitizeSubdomain(tc.input)
			if result != tc.expected {
				t.Errorf("sanitizeSubdomain(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
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
