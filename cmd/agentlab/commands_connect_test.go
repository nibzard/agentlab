package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectSavesDefaultContextAndPreservesLegacyConfig(t *testing.T) {
	useTempClientConfig(t)

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

	assert.Equal(t, "https://old.example", cfg.Endpoint)
	assert.Equal(t, "old-token", cfg.Token)
	assert.NotContains(t, string(data), "new-token")
	store, err := contextStore()
	require.NoError(t, err)
	selected, name, found, err := store.Resolve("")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "default", name)
	assert.Equal(t, server.URL, selected.Endpoint)
	assert.Equal(t, "new-token", selected.Token)
	assert.Equal(t, "jump.example", selected.JumpHost)
	assert.Equal(t, "jumpuser", selected.JumpUser)
	opts, _, err := parseGlobal([]string{"status"})
	require.NoError(t, err)
	assert.Equal(t, selected.Endpoint, opts.endpoint)
	assert.Equal(t, selected.Token, effectiveGlobalToken(t, opts))
	metadataPath := filepath.Join(store.Root, "contexts.json")
	info, err := os.Stat(metadataPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	metadata, err := os.ReadFile(metadataPath)
	require.NoError(t, err)
	assert.NotContains(t, string(metadata), "new-token")
}

func TestConnectRejectsEndpointWithPath(t *testing.T) {
	useTempClientConfig(t)

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
	useTempClientConfig(t)

	path, err := clientConfigPath()
	require.NoError(t, err)
	require.NoError(t, writeClientConfig(path, clientConfig{Endpoint: "http://example", Token: "token"}))

	base := commonFlags{jsonOutput: true, timeout: time.Second}
	err = runDisconnectCommand(context.Background(), []string{}, base)
	require.NoError(t, err)

	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr))
}
