package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/connection"
	"github.com/stretchr/testify/require"
)

func TestContextCredentialStorageMigrationIsLocalAndPreservesSelection(t *testing.T) {
	useTempClientConfig(t)
	store, err := contextStore()
	require.NoError(t, err)
	require.NoError(t, store.Put("operator", connection.Settings{Endpoint: "https://offline.invalid", Token: "private-operator-token", ServerIdentity: "pinned-server", JumpHost: "jump.example"}))
	old, _, err := store.List()
	require.NoError(t, err)
	output := captureStdout(t, func() {
		require.NoError(t, runContextCommand([]string{"credentials", "operator", "--credential-store", "file", "--json"}, commonFlags{jsonOutput: true}))
	})
	require.NotContains(t, output, "private-operator-token")
	var item connection.Context
	require.NoError(t, json.Unmarshal([]byte(output), &item))
	require.Equal(t, "file", item.CredentialStorage)
	require.Equal(t, "pinned-server", item.ServerIdentity)
	require.NotEqual(t, old[0].CredentialRef, item.CredentialRef)
	_, err = os.Stat(filepath.Join(store.Root, "credentials", old[0].CredentialRef+".json"))
	require.True(t, os.IsNotExist(err))
	cfg, selected, found, err := store.Resolve("")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "operator", selected)
	require.Equal(t, "private-operator-token", cfg.Token)
	require.Equal(t, "jump.example", cfg.JumpHost)
	require.NoError(t, runContextCommand([]string{"credentials-cleanup"}, commonFlags{jsonOutput: true}))
}

func TestConnectRepairsAnActiveContextWithAMissingCredential(t *testing.T) {
	useTempClientConfig(t)
	store, err := contextStore()
	require.NoError(t, err)
	require.NoError(t, store.Put("operator", connection.Settings{Endpoint: "https://old.example", Token: "old-fixture-token"}))
	items, _, err := store.List()
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(store.Root, "credentials", items[0].CredentialRef+".json")))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/status" {
			writeJSON(t, w, 200, statusResponse{})
		} else if r.URL.Path == "/v1/host" {
			writeJSON(t, w, 200, hostResponse{})
		} else {
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	output := captureStdout(t, func() {
		require.NoError(t, runConnectCommand(context.Background(), []string{"--endpoint", server.URL, "--token", "replacement-fixture-token", "--credential-store", "file"}, commonFlags{jsonOutput: true, timeout: time.Second}))
	})
	require.NotContains(t, output, "replacement-fixture-token")
	cfg, selected, found, err := store.Resolve("")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "operator", selected)
	require.Equal(t, server.URL, cfg.Endpoint)
	require.Equal(t, "replacement-fixture-token", cfg.Token)
	items, _, err = store.List()
	require.NoError(t, err)
	require.Len(t, items, 1)
}

func TestConnectUnavailableNativeStorePreservesWorkingContext(t *testing.T) {
	useTempClientConfig(t)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	store, err := contextStore()
	require.NoError(t, err)
	require.NoError(t, store.Put("operator", connection.Settings{Endpoint: "https://working.example", Token: "working-fixture-token"}))
	before, err := os.ReadFile(filepath.Join(store.Root, "contexts.json"))
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/status" {
			writeJSON(t, w, 200, statusResponse{})
		} else if r.URL.Path == "/v1/host" {
			writeJSON(t, w, 200, hostResponse{})
		} else {
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	err = runConnectCommand(context.Background(), []string{"--endpoint", server.URL, "--token", "replacement-fixture-token", "--credential-store", "os"}, commonFlags{jsonOutput: true, timeout: time.Second})
	var requirement *connection.CredentialError
	require.True(t, errors.As(err, &requirement))
	require.Equal(t, "credential_store_unavailable", requirement.Code)
	var output bytes.Buffer
	writeJSONError(&output, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &payload))
	require.Equal(t, "blocked", payload["state"])
	require.Equal(t, "credential_store", payload["requirement"])
	require.Equal(t, false, payload["context_metadata_saved"])
	require.NotContains(t, output.String(), "replacement-fixture-token")
	after, err := os.ReadFile(filepath.Join(store.Root, "contexts.json"))
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestCredentialRecoveryCommandsIgnoreBrokenConnectionSettings(t *testing.T) {
	useTempClientConfig(t)
	store, err := contextStore()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(store.Root, 0700))
	for _, name := range []string{"client.json", "defaults.json"} {
		require.NoError(t, os.WriteFile(filepath.Join(store.Root, name), []byte("broken-settings"), 0600))
	}
	t.Setenv("AGENTLAB_ENDPOINT", "https://must-not-contact.invalid")
	t.Setenv("AGENTLAB_TOKEN", "must-not-read-token")
	t.Setenv("AGENTLAB_CONTEXT", "missing-context")
	t.Setenv("AGENTLAB_CREDENTIAL_STORE", "invalid")
	interrupted := connection.ContextStore{Root: store.Root, CredentialMode: "os", Vault: interruptedCLIFileVault{root: store.Root}}
	err = interrupted.Put("operator", connection.Settings{Endpoint: "https://fixture.invalid", Token: "pending-fixture-token"})
	var pendingError *connection.CredentialError
	require.True(t, errors.As(err, &pendingError))
	require.NotNil(t, pendingError.Transaction)
	ref, id := pendingError.Transaction.CredentialRef, pendingError.Transaction.ID
	data, err := os.ReadFile(filepath.Join(store.Root, "credential-transactions", ref+".json"))
	require.NoError(t, err)
	var record map[string]any
	require.NoError(t, json.Unmarshal(data, &record))
	for _, command := range []string{"credentials-pending", "credentials-cleanup"} {
		if command == "credentials-cleanup" {
			require.NoError(t, os.Chmod(filepath.Join(store.Root, "credentials", ref+".json"), 0600))
		}
		_, _, err := parseGlobal([]string{"--json", "context", command})
		require.NoError(t, err)
		opts, args, err := parseGlobal([]string{"context", "--json", command})
		require.NoError(t, err)
		require.Empty(t, opts.endpoint)
		require.Empty(t, opts.token)
		output := captureStdout(t, func() { require.NoError(t, runContextCommand(args[1:], commonFlags{jsonOutput: true})) })
		require.NotContains(t, output, "must-not-read-token")
		require.NotContains(t, output, "pending-fixture-token")
		for _, field := range []string{"payload_sha256", "intent_sha256", "configuration"} {
			require.NotContains(t, output, record[field].(string))
		}
		if command == "credentials-pending" {
			require.Contains(t, output, id)
			require.Contains(t, output, ref)
		}
	}
	_, err = os.Lstat(filepath.Join(store.Root, "contexts.json"))
	require.True(t, os.IsNotExist(err))
	for _, name := range []string{"client.json", "defaults.json"} {
		data, err := os.ReadFile(filepath.Join(store.Root, name))
		require.NoError(t, err)
		require.Equal(t, "broken-settings", string(data))
	}
}

// A lost response and unreadable record leave a real intent, without a native
// service or a dependency on its private journal representation.
type interruptedCLIFileVault struct{ root string }

func (interruptedCLIFileVault) Name() string { return "file" }
func (v interruptedCLIFileVault) Save(_ context.Context, ref string, data []byte) error {
	directory := filepath.Join(v.root, "credentials")
	if err := os.Mkdir(directory, 0700); err != nil {
		return err
	}
	path := filepath.Join(directory, ref+".json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	if err := os.Chmod(path, 0644); err != nil {
		return err
	}
	return &connection.CredentialError{Code: "credential_store_unavailable", Action: "fixture acknowledgement was lost"}
}
func (interruptedCLIFileVault) Load(context.Context, string) ([]byte, error) {
	return nil, &connection.CredentialError{Code: "credential_store_unavailable", Action: "fixture read is unavailable"}
}
func (interruptedCLIFileVault) Delete(context.Context, string) error {
	return &connection.CredentialError{Code: "credential_store_unavailable", Action: "fixture deletion is unavailable"}
}
