package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/connection"
	"github.com/agentlab/agentlab/internal/daemon"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/hostidentity"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestCredentialRenewalRecoversLostCommitWithoutSigningKey(t *testing.T) {
	previous := clientConfigRootOverride
	clientConfigRootOverride = t.TempDir()
	t.Cleanup(func() { clientConfigRootOverride = previous })
	t.Setenv("AGENTLAB_CREDENTIAL_STORE", "file")
	t.Setenv("AGENTLAB_TOKEN", "")
	t.Setenv("AGENTLAB_ENDPOINT", "")
	_, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(private)
	require.NoError(t, err)
	keys, err := auth.ParseAuthorizedKeys(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	require.NoError(t, err)
	database, err := db.Open(filepath.Join(t.TempDir(), "daemon.db"))
	require.NoError(t, err)
	defer database.Close()
	mux := http.NewServeMux()
	daemon.NewControlAPI(database, nil, nil, nil, nil, "", nil).Register(mux)
	middleware, err := auth.NewMiddlewareWithStore(keys, "management", nil)
	require.NoError(t, err)
	handler := middleware.WithRevocationChecker(database).WrapNetwork(mux)
	lost := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/token-renewals/commit" && !lost {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, r)
			if response.Code == 200 {
				lost = true
				w.WriteHeader(503)
				_, _ = w.Write([]byte(`{"error":"lost reply","code":"fixture_lost_reply"}`))
				return
			}
			for key, values := range response.Header() {
				w.Header()[key] = values
			}
			w.WriteHeader(response.Code)
			_, _ = w.Write(response.Body.Bytes())
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	identity, err := hostidentity.Local()
	require.NoError(t, err)
	parent, err := auth.CreateToken(signer, auth.TokenCreateRequest{Commands: []string{"schema.read", "token.renew"}, Scope: []string{"sandbox:1001"}, Subject: "worker", TTL: time.Hour})
	require.NoError(t, err)
	store, err := contextStore()
	require.NoError(t, err)
	require.NoError(t, store.Put("task", connection.Settings{Endpoint: server.URL, Token: parent, ServerIdentity: identity}))
	block, err := ssh.MarshalPrivateKey(private, "")
	require.NoError(t, err)
	keyPath := filepath.Join(t.TempDir(), "operator-key")
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(block), 0600))
	args := []string{"--context", "task", "--request-id", "rotation-1", "--key", keyPath, "--json"}
	captureStdout(t, func() {
		err := runTokenRenew(context.Background(), args, commonFlags{})
		var pending *renewalRequirement
		require.ErrorAs(t, err, &pending)
		require.Equal(t, "staged", pending.Record.State)
	})
	require.True(t, lost)
	require.NoError(t, os.Remove(keyPath))
	output := captureStdout(t, func() {
		require.NoError(t, runTokenRenew(context.Background(), []string{"--context", "task", "--request-id", "rotation-1", "--json"}, commonFlags{}))
	})
	var record credentialRenewal
	require.NoError(t, json.Unmarshal([]byte(output), &record))
	require.Equal(t, "published", record.State)
	require.Equal(t, "verified", record.Cleanup)
	cfg, _, _, err := store.Resolve("task")
	require.NoError(t, err)
	require.NotEqual(t, parent, cfg.Token)
	require.NotContains(t, output, parent)
	require.NotContains(t, output, cfg.Token)
	candidate, err := auth.VerifyTokenSignature(cfg.Token, signer.PublicKey())
	require.NoError(t, err)
	require.Equal(t, []string{"sandbox:1001"}, candidate.Claims.Scope)
	require.Equal(t, []string{"schema.read", "token.renew"}, candidate.Claims.Commands)
	req := httptest.NewRequest("GET", "/v1/capabilities", nil)
	req.Header.Set("Authorization", "Bearer "+parent)
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, req)
	require.Equal(t, 401, denied.Code)
	_, _, stageFound, err := store.LookupContext(context.Background(), record.Stage.Name, true)
	require.NoError(t, err)
	require.False(t, stageFound)
	replay := captureStdout(t, func() {
		require.NoError(t, runTokenRenew(context.Background(), []string{"--context", "task", "--request-id", "rotation-1", "--json"}, commonFlags{}))
	})
	require.JSONEq(t, output, replay)
	// Enrollment verifies the new credential over the authenticated remote path.
	enrolled := captureStdout(t, func() { // Recreate only the operator file for this separate enrollment.
		require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(block), 0600))
		require.NoError(t, runTokenEnroll(context.Background(), []string{"--save-context", "delegate", "--key", keyPath, "--cmds", "schema.read,token.renew", "--scope", "sandbox:1001", "--json"}, commonFlags{endpoint: server.URL, token: "management"}))
	})
	delegated, _, _, err := store.Resolve("delegate")
	require.NoError(t, err)
	require.NotContains(t, enrolled, delegated.Token)
	// An unregistered signing key cannot replace the verified destination.
	_, foreign, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	foreignBlock, err := ssh.MarshalPrivateKey(foreign, "")
	require.NoError(t, err)
	foreignPath := filepath.Join(t.TempDir(), "unregistered-key")
	require.NoError(t, os.WriteFile(foreignPath, pem.EncodeToMemory(foreignBlock), 0600))
	captureStdout(t, func() {
		require.Error(t, runTokenEnroll(context.Background(), []string{"--save-context", "delegate", "--key", foreignPath, "--cmds", "schema.read", "--json"}, commonFlags{endpoint: server.URL, token: "management"}))
	})
	preserved, _, _, err := store.Resolve("delegate")
	require.NoError(t, err)
	require.Equal(t, delegated.Token, preserved.Token)
	metadata, err := os.ReadFile(filepath.Join(store.Root, "credential-renewals", credentialHash("task")+"-"+credentialHash("rotation-1")+".json"))
	require.NoError(t, err)
	require.NotContains(t, string(metadata), parent)
	require.NotContains(t, string(metadata), cfg.Token)
	require.NotContains(t, string(metadata), "PRIVATE KEY")
	// A later rotation cannot reuse or erase an earlier request receipt.
	captureStdout(t, func() {
		require.NoError(t, runTokenRenew(context.Background(), []string{"--context", "task", "--request-id", "rotation-2", "--key", keyPath, "--json"}, commonFlags{}))
	})
	historical := captureStdout(t, func() {
		require.NoError(t, runTokenRenew(context.Background(), []string{"--context", "task", "--request-id", "rotation-1", "--json"}, commonFlags{}))
	})
	require.JSONEq(t, output, historical)
	require.False(t, strings.Contains(output, "agentlab."))
}
