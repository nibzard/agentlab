package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/agentlab/agentlab/internal/connection"
	"github.com/stretchr/testify/require"
)

func TestGlobalContextKeepsSelectedConnection(t *testing.T) {
	useTempClientConfig(t)
	t.Setenv("AGENTLAB_CONTEXT", "")
	store, err := contextStore()
	require.NoError(t, err)
	require.NoError(t, store.Put("one", connection.Settings{Endpoint: "https://one.example", Token: "one"}))
	require.NoError(t, store.Put("two", connection.Settings{Endpoint: "https://two.example", Token: "two"}))
	opts, _, err := parseGlobal([]string{"--context", "two", "status"})
	require.NoError(t, err)
	require.Equal(t, "https://two.example", opts.endpoint)
	require.Equal(t, "two", effectiveGlobalToken(t, opts))
	require.NoError(t, store.Use("one"))
	require.Equal(t, "https://two.example", opts.endpoint)
	_, _, err = parseGlobal([]string{"--context", "missing", "status"})
	require.Error(t, err)
	items, active, err := store.List()
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "one", active)
}

func TestConnectMissingCredentialIsStructuredBlocked(t *testing.T) {
	useTempClientConfig(t)
	err := runConnectCommand(context.Background(), []string{"--endpoint", "https://example.com"}, commonFlags{jsonOutput: true})
	var requirement *blockedRequirement
	require.True(t, errors.As(err, &requirement))
	var output bytes.Buffer
	writeJSONError(&output, err)
	var payload map[string]string
	require.NoError(t, json.Unmarshal(output.Bytes(), &payload))
	require.Equal(t, "blocked", payload["state"])
	require.Equal(t, "credentials_required", payload["code"])
}

func TestCredentialInputUsesExplicitStdin(t *testing.T) {
	read, write, err := os.Pipe()
	require.NoError(t, err)
	_, err = write.WriteString("secret-from-pipe\n")
	require.NoError(t, err)
	write.Close()
	original := os.Stdin
	os.Stdin = read
	t.Cleanup(func() { os.Stdin = original; read.Close() })
	token, err := readConnectionToken(true, true)
	require.NoError(t, err)
	require.Equal(t, "secret-from-pipe", token)
}
