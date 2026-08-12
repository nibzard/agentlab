package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClientConfigPathRespectsOverride proves the config root override
// isolates client-config resolution even when XDG_CONFIG_HOME is NOT honored
// by the platform (the Darwin failure mode: os.UserConfigDir ignores it). This
// is the H5 regression guard.
func TestClientConfigPathRespectsOverride(t *testing.T) {
	prev := clientConfigRootOverride
	t.Cleanup(func() { clientConfigRootOverride = prev })

	root := t.TempDir()
	clientConfigRootOverride = root
	// Deliberately do NOT rely on XDG_CONFIG_HOME — mirror Darwin.
	t.Setenv("XDG_CONFIG_HOME", "")

	got, err := clientConfigPath()
	require.NoError(t, err)
	rel, err := filepath.Rel(root, got)
	require.NoError(t, err)
	assert.False(t, strings.HasPrefix(rel, ".."),
		"clientConfigPath %q escapes override root %q", got, root)
	assert.True(t, strings.HasSuffix(got, filepath.Join("agentlab", "client.json")),
		"unexpected path %q", got)

	// defaultsFilePath must honor the same override.
	dpath, err := defaultsFilePath()
	require.NoError(t, err)
	drel, err := filepath.Rel(root, dpath)
	require.NoError(t, err)
	assert.False(t, strings.HasPrefix(drel, ".."),
		"defaultsFilePath %q escapes override root %q", dpath, root)

	// The guard must reject an external path while the override is active,
	// and accept the resolved one.
	require.Error(t, requireConfigPathSafe("/etc/agentlab/client.json"))
	require.NoError(t, requireConfigPathSafe(got))

	// Writing through the resolved path must land under the override root.
	require.NoError(t, writeClientConfig(got, clientConfig{Endpoint: "http://example", Token: "secret"}))
	_, ok, err := loadClientConfigFrom(got)
	require.NoError(t, err)
	assert.True(t, ok)
}

// TestRequireConfigPathSafeNoOverride is a no-op in production (no override).
func TestRequireConfigPathSafeNoOverride(t *testing.T) {
	prev := clientConfigRootOverride
	clientConfigRootOverride = ""
	t.Cleanup(func() { clientConfigRootOverride = prev })
	// With no override, any path is allowed (production behavior).
	assert.NoError(t, requireConfigPathSafe("/anywhere/client.json"))
}

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
		{"loopback http", "http://127.0.0.1:8845", "http://127.0.0.1:8845", false},
		{"host port no scheme rejected", "example.com:8845", "", true},
		{"host only no scheme rejected", "example.com", "", true},
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
	useTempClientConfig(t)
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
	useTempClientConfig(t)
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
