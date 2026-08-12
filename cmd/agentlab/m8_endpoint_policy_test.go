// ABOUTME: Tests for the bare-endpoint scheme and insecure-HTTP policy (review M8).

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"127.0.0.1", true},
		{"127.1.2.3", true},
		{"::1", true},
		{"[::1]", true},
		{"", false},
		{"example.com", false},
		{"10.0.0.1", false},
		{"192.168.1.1", false},
		{"100.64.0.1", false}, // Tailscale CGNAT range is not loopback
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			assert.Equal(t, tt.want, isLoopbackHost(tt.host))
		})
	}
}

func TestValidateEndpointPolicy(t *testing.T) {
	tests := []struct {
		name              string
		endpoint          string
		allowInsecureHTTP bool
		wantErr           bool
	}{
		{"empty is no-op", "", false, false},
		{"https always allowed", "https://example.com", false, false},
		{"loopback http allowed", "http://127.0.0.1:8845", false, false},
		{"localhost http allowed", "http://localhost:8845", false, false},
		{"non-loopback http rejected without ack", "http://example.com:8845", false, true},
		{"non-loopback http allowed with ack", "http://example.com:8845", true, false},
		{"tailscale host rejected without ack", "http://host.tailnet.ts.net:8845", false, true},
		{"tailscale host allowed with ack", "http://host.tailnet.ts.net:8845", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEndpointPolicy(tt.endpoint, tt.allowInsecureHTTP)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "--allow-insecure-http")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestEnvBool covers the truthiness interpretation used for the
// AGENTLAB_ALLOW_INSECURE_HTTP acknowledgement.
func TestEnvBool(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"", false}, {"0", false}, {"false", false}, {"NO", false}, {" off ", false},
		{"1", true}, {"true", true}, {"yes", true}, {"TRUE", true}, {"on", true}, {"anything", true},
	} {
		assert.Equal(t, c.want, envBool(c.in), "input %q", c.in)
	}
}

// TestParseGlobal_PersistsBootstrapAcknowledgement verifies that a config
// written by bootstrap (allow_insecure_http=true) keeps the acknowledgement
// active for subsequent command parsing even without the env var or flag.
func TestParseGlobal_PersistsBootstrapAcknowledgement(t *testing.T) {
	useTempClientConfig(t)
	path, err := clientConfigPath()
	require.NoError(t, err)
	require.NoError(t, writeClientConfig(path, clientConfig{
		Endpoint:          "http://host.tailnet.ts.net:8845",
		Token:             "tok",
		AllowInsecureHTTP: true,
	}))

	opts, _, err := parseGlobal([]string{"status"})
	require.NoError(t, err)
	assert.True(t, opts.allowInsecureHTTP, "saved bootstrap acknowledgement should be honored")
}

// TestParseGlobal_NonLoopbackWithoutAcknowledgementRejected proves a plain
// saved non-loopback HTTP endpoint without the acknowledgement does not grant
// insecure access: the flag stays false.
func TestParseGlobal_NonLoopbackWithoutAcknowledgementRejected(t *testing.T) {
	useTempClientConfig(t)
	path, err := clientConfigPath()
	require.NoError(t, err)
	require.NoError(t, writeClientConfig(path, clientConfig{
		Endpoint: "http://host.tailnet.ts.net:8845",
		Token:    "tok",
	}))

	opts, _, err := parseGlobal([]string{"status"})
	require.NoError(t, err)
	assert.False(t, opts.allowInsecureHTTP, "no source granted insecure acknowledgement")
}
