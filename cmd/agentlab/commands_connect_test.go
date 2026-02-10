package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectWritesConfigAndOverwrites(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, statusResponse{Sandboxes: map[string]int{}, Jobs: map[string]int{}})
	})
	mux.HandleFunc("/v1/host", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, hostResponse{TailscaleDNS: "host.tailnet.ts.net"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	path, err := clientConfigPath()
	require.NoError(t, err)
	require.NoError(t, writeClientConfig(path, clientConfig{Endpoint: "https://old.example", Token: "old-token"}))

	base := commonFlags{jsonOutput: true, timeout: time.Second}
	err = runConnectCommand(context.Background(), []string{
		"--endpoint", server.URL,
		"--token", "new-token",
		"--jump-host", "jump.example",
		"--jump-user", "jumpuser",
	}, base)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var cfg clientConfig
	require.NoError(t, json.Unmarshal(data, &cfg))

	normalized, err := normalizeEndpoint(server.URL)
	require.NoError(t, err)
	assert.Equal(t, normalized, cfg.Endpoint)
	assert.Equal(t, "new-token", cfg.Token)
	assert.Equal(t, "jump.example", cfg.JumpHost)
	assert.Equal(t, "jumpuser", cfg.JumpUser)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestConnectRejectsEndpointWithPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	base := commonFlags{jsonOutput: true, timeout: time.Second}
	err := runConnectCommand(context.Background(), []string{
		"--endpoint", "http://example.com/api",
		"--token", "secret-token",
	}, base)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "endpoint must not include a path")
	assert.NotContains(t, err.Error(), "secret-token")

	path, err := clientConfigPath()
	require.NoError(t, err)
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr))
}

func TestDisconnectRemovesConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	path, err := clientConfigPath()
	require.NoError(t, err)
	require.NoError(t, writeClientConfig(path, clientConfig{Endpoint: "http://example", Token: "token"}))

	base := commonFlags{jsonOutput: true, timeout: time.Second}
	err = runDisconnectCommand(context.Background(), []string{}, base)
	require.NoError(t, err)

	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr))
}
