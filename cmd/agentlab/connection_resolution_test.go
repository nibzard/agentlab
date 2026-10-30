package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentlab/agentlab/internal/connection"
	"github.com/stretchr/testify/require"
)

func effectiveGlobalToken(t *testing.T, opts globalOptions) string {
	t.Helper()
	resolved, err := opts.common().clientOptions()
	require.NoError(t, err)
	return resolved.Token
}

func TestConnectionFlagsResolveAfterCommandParsing(t *testing.T) {
	useTempClientConfig(t)
	store, err := contextStore()
	require.NoError(t, err)
	require.NoError(t, store.Put("saved", connection.Settings{Endpoint: "https://saved.example", Token: "saved-secret", ServerIdentity: "saved-id", JumpHost: "saved-jump", AllowInsecureHTTP: true}))
	items, _, err := store.List()
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(store.Root, "credentials", items[0].CredentialRef+".json")))
	for _, tt := range []struct {
		name                    string
		args                    []string
		endpoint, token, socket string
	}{
		{"child token", []string{"status", "--token", "replacement"}, "https://saved.example", "replacement", ""},
		{"child endpoint", []string{"status", "--endpoint", "https://new.example"}, "https://new.example", "", ""},
		{"child socket", []string{"status", "--socket", "/tmp/selected.sock"}, "", "", "/tmp/selected.sock"},
		{"global token", []string{"--token", "replacement", "status"}, "https://saved.example", "replacement", ""},
		{"global socket", []string{"--socket", "/tmp/selected.sock", "status"}, "", "", "/tmp/selected.sock"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			global, args, err := parseGlobal(tt.args)
			require.NoError(t, err)
			require.NotEqual(t, "saved-secret", global.token)
			local := global.common()
			fs := newFlagSet("test")
			local.bind(fs)
			require.NoError(t, fs.Parse(args[1:]))
			resolved, err := local.resolvedConnection()
			require.NoError(t, err)
			require.Equal(t, tt.endpoint, resolved.Endpoint)
			require.Equal(t, tt.token, resolved.Token)
			if tt.socket != "" {
				require.Equal(t, tt.socket, resolved.SocketPath)
			}
			if tt.endpoint != "https://saved.example" {
				require.Empty(t, resolved.ServerIdentity)
				require.Empty(t, resolved.JumpHost)
				require.False(t, resolved.AllowInsecureHTTP)
			}
		})
	}
	global, _, err := parseGlobal([]string{"status"})
	require.NoError(t, err)
	_, err = global.common().clientOptions()
	require.Error(t, err)
}

func TestLocalCommandsIgnoreBrokenConnectionAndDefaults(t *testing.T) {
	useTempClientConfig(t)
	store, err := contextStore()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(store.Root, 0700))
	for _, name := range []string{"client.json", "contexts.json", "defaults.json"} {
		require.NoError(t, os.WriteFile(filepath.Join(store.Root, name), []byte("broken"), 0600))
	}
	for _, args := range [][]string{{}, {"version"}, {"--version"}, {"--help"}, {"status", "--help"}, {"token", "inspect", "--token-stdin"}, {"skill", "show"}, {"operation", "show", "op-test"}, {"repo", "snapshot"}, {"context", "credentials-pending"}} {
		opts, _, err := parseGlobal(args)
		if len(args) == 1 && args[0] == "--help" {
			require.ErrorIs(t, err, errHelp)
			continue
		}
		require.NoError(t, err, "%v", args)
		require.Nil(t, opts.selection)
	}
}

func TestConnectReplacementDoesNotLoadOldCredential(t *testing.T) {
	useTempClientConfig(t)
	store, err := contextStore()
	require.NoError(t, err)
	require.NoError(t, store.Put("saved", connection.Settings{Endpoint: "https://saved.example", Token: "saved-secret"}))
	items, _, err := store.List()
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(store.Root, "credentials", items[0].CredentialRef+".json")))
	global, args, err := parseGlobal([]string{"connect", "--endpoint", "https://replacement.example"})
	require.NoError(t, err)
	// Credential input blocks unattended execution before any network operation.
	err = runConnectCommand(context.Background(), args[1:], global.common())
	var blocked *blockedRequirement
	require.ErrorAs(t, err, &blocked)
	require.Equal(t, "credentials_required", blocked.Code)
}

func TestExplicitChildTransportRepairsUnselectedContext(t *testing.T) {
	useTempClientConfig(t)
	store, err := contextStore()
	require.NoError(t, err)
	require.NoError(t, store.Put("old", connection.Settings{Endpoint: "https://old.example", Token: "old-secret"}))
	require.NoError(t, store.Remove("old"))
	for _, args := range [][]string{{"--socket", "/tmp/local.sock"}, {"--endpoint", "https://new.example", "--token", "replacement"}} {
		global, _, err := parseGlobal([]string{"status"})
		require.NoError(t, err)
		opts := global.common()
		fs := newFlagSet("test")
		opts.bind(fs)
		require.NoError(t, fs.Parse(args))
		_, err = opts.clientOptions()
		require.NoError(t, err)
	}
	global, _, err := parseGlobal([]string{"status"})
	require.NoError(t, err)
	_, err = global.common().clientOptions()
	require.Error(t, err)
}
