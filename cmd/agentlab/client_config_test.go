package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"empty", "", "", false},
		{"scheme preserved", "https://example.com:8845", "https://example.com:8845", false},
		{"trailing slash", "http://example.com/", "http://example.com", false},
		{"host port no scheme", "example.com:8845", "http://example.com:8845", false},
		{"host only", "example.com", "http://example.com", false},
		{"trim", "  https://example.com  ", "https://example.com", false},
		{"invalid scheme", "ftp://example.com", "", true},
		{"path not allowed", "http://example.com/api", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeEndpoint(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestClientConfigPrecedence(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := clientConfigPath()
	require.NoError(t, err)

	require.NoError(t, writeClientConfig(path, clientConfig{Endpoint: "https://cfg", Token: "cfg-token"}))

	opts, _, err := parseGlobal([]string{})
	require.NoError(t, err)
	assert.Equal(t, "https://cfg", opts.endpoint)
	assert.Equal(t, "cfg-token", opts.token)

	t.Setenv(envEndpoint, "https://env")
	t.Setenv(envToken, "env-token")
	opts, _, err = parseGlobal([]string{})
	require.NoError(t, err)
	assert.Equal(t, "https://env", opts.endpoint)
	assert.Equal(t, "env-token", opts.token)

	opts, _, err = parseGlobal([]string{"--endpoint", "https://cli", "--token", "cli-token"})
	require.NoError(t, err)
	assert.Equal(t, "https://cli", opts.endpoint)
	assert.Equal(t, "cli-token", opts.token)
}

func TestWriteClientConfigPermissions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := clientConfigPath()
	require.NoError(t, err)

	require.NoError(t, writeClientConfig(path, clientConfig{Endpoint: "http://example", Token: "secret"}))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	// Ensure we can load the config and that permissions remain strict.
	loaded, ok, err := loadClientConfig()
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "http://example", loaded.Endpoint)
	assert.Equal(t, "secret", loaded.Token)
	info, err = os.Stat(filepath.Clean(path))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}
