package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/connection"
	"github.com/agentlab/agentlab/internal/daemon"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/hostidentity"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func isolatedIdentityConfig(t *testing.T) connection.ContextStore {
	t.Helper()
	previous := clientConfigRootOverride
	clientConfigRootOverride = t.TempDir()
	t.Cleanup(func() { clientConfigRootOverride = previous })
	for _, key := range []string{"AGENTLAB_TOKEN", "AGENTLAB_ENDPOINT", "AGENTLAB_CONTEXT"} {
		t.Setenv(key, "")
	}
	t.Setenv("AGENTLAB_CREDENTIAL_STORE", "file")
	store, err := contextStore()
	require.NoError(t, err)
	return store
}

func identityCaps(identity string, feature bool) protocol.Capabilities {
	return protocol.Capabilities{ServerIdentity: identity, ProtocolVersion: 1, APISchemaVersion: 1, Features: map[string]bool{"request_server_identity": feature}}
}

type identityUnreadBody struct{ reads atomic.Int32 }

func (r *identityUnreadBody) Read([]byte) (int, error) { r.reads.Add(1); return 0, io.EOF }

func TestBoundClientBlocksBeforeSendingPayload(t *testing.T) {
	for _, condition := range []string{"different_identity", "old_server", "missing_capabilities", "redirect"} {
		t.Run(condition, func(t *testing.T) {
			var operations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "expected", r.Header.Get(hostidentity.RequestHeader))
				if r.URL.Path != "/v1/capabilities" {
					operations.Add(1)
					w.WriteHeader(200)
					return
				}
				switch condition {
				case "different_identity":
					writeJSON(t, w, 200, identityCaps("another", true))
				case "old_server":
					writeJSON(t, w, 200, identityCaps("expected", false))
				case "missing_capabilities":
					http.NotFound(w, r)
				case "redirect":
					w.Header().Set("Location", "https://must-not-contact.invalid")
					writeJSON(t, w, 307, identityCaps("expected", true))
				}
			}))
			defer server.Close()
			client := newAPIClient(clientOptions{Endpoint: server.URL, Token: "private-fixture", ServerIdentity: "expected", SocketPath: "/must-not-use"}, time.Second)
			body := &identityUnreadBody{}
			_, err := client.doRequest(context.Background(), http.MethodPost, "/v1/jobs", body, nil)
			require.Error(t, err)
			require.Zero(t, body.reads.Load())
			require.Zero(t, operations.Load())
			require.NotContains(t, err.Error(), "private-fixture")
			_, err = client.doJSON(context.Background(), http.MethodPost, "/v1/jobs", map[string]string{"task": "fixture"})
			require.Error(t, err)
			require.Zero(t, operations.Load())
		})
	}
}

func TestBoundClientKeepsIdentityAfterPreflight(t *testing.T) {
	var operations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			writeJSON(t, w, 200, identityCaps("expected", true))
			return
		}
		// The endpoint changes after its capability response. The mutation still
		// carries the original identity for the server to reject before dispatch.
		require.Equal(t, "expected", r.Header.Get(hostidentity.RequestHeader))
		if r.Header.Get(hostidentity.RequestHeader) != "replacement" {
			writeJSON(t, w, 409, map[string]string{"code": "v1/auth/server_identity_mismatch", "error": "request targets another server"})
			return
		}
		operations.Add(1)
	}))
	defer server.Close()
	client := newAPIClient(clientOptions{Endpoint: server.URL, ServerIdentity: "expected"}, time.Second)
	_, err := client.doJSON(context.Background(), http.MethodPost, "/v1/jobs", map[string]string{"task": "fixture"})
	var response apiResponseError
	require.ErrorAs(t, err, &response)
	require.Equal(t, "v1/auth/server_identity_mismatch", response.Code)
	require.Zero(t, operations.Load())
	_, err = client.doRequest(context.Background(), http.MethodGet, "/v1/jobs/artifact", nil, map[string]string{hostidentity.RequestHeader: "replacement"})
	var blocked *blockedRequirement
	require.ErrorAs(t, err, &blocked)
	require.Equal(t, "server_identity_override", blocked.Code)
	require.Zero(t, operations.Load())
}

func TestConnectPinsCapabilitiesWithoutHostPermission(t *testing.T) {
	store := isolatedIdentityConfig(t)
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
	middleware, err := auth.NewMiddlewareWithStore(keys, "", nil)
	require.NoError(t, err)
	server := httptest.NewServer(middleware.WithRevocationChecker(database).WrapNetwork(mux))
	defer server.Close()
	raw, err := auth.CreateToken(signer, auth.TokenCreateRequest{Commands: []string{"schema.read", "status.read"}, Scope: []string{"sandbox:1001"}, TTL: time.Hour})
	require.NoError(t, err)
	output := captureStdout(t, func() {
		require.NoError(t, runConnectCommand(context.Background(), []string{"--context", "task", "--endpoint", server.URL, "--token", raw}, commonFlags{jsonOutput: true, timeout: time.Second}))
	})
	var connected connectOutput
	require.NoError(t, json.Unmarshal([]byte(output), &connected))
	identity, err := hostidentity.Local()
	require.NoError(t, err)
	require.True(t, connected.IdentityVerified)
	require.Equal(t, "verified", connected.Compatibility)
	require.Equal(t, identity, connected.ServerIdentity)
	require.Nil(t, connected.Host)
	require.NotContains(t, output, raw)
	cfg, _, _, err := store.Resolve("task")
	require.NoError(t, err)
	require.Equal(t, identity, cfg.ServerIdentity)
	global, _, err := parseGlobal([]string{"--context", "task", "status"})
	require.NoError(t, err)
	client, err := apiClientFromFlags(global.common())
	require.NoError(t, err)
	require.Equal(t, identity, client.serverIdentity)
	require.NoError(t, store.Put("other", connection.Settings{Endpoint: "https://unrelated.invalid", Token: "other", ServerIdentity: "unrelated"}))
	require.NoError(t, store.Use("other"))
	_, err = client.doJSON(context.Background(), http.MethodGet, "/v1/status", nil)
	require.NoError(t, err)
}

func TestConnectPreservesConcurrentDestination(t *testing.T) {
	store := isolatedIdentityConfig(t)
	initial := connection.Settings{Endpoint: "https://working.example", Token: "working"}
	require.NoError(t, store.Put("task", initial))
	changed := connection.Settings{Endpoint: "https://concurrent.example", Token: "concurrent"}
	var once sync.Once
	changedResult := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			once.Do(func() { changedResult <- store.Put("task", changed) })
			writeJSON(t, w, 200, identityCaps("candidate", true))
		} else if r.URL.Path == "/v1/status" {
			writeJSON(t, w, 200, statusResponse{})
		} else {
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	err := runConnectCommand(context.Background(), []string{"--context", "task", "--endpoint", server.URL, "--token", "candidate-secret"}, commonFlags{jsonOutput: true, timeout: time.Second})
	require.Error(t, err)
	require.NoError(t, <-changedResult)
	cfg, _, _, err := store.Resolve("task")
	require.NoError(t, err)
	require.Equal(t, changed, cfg)
}

func TestConnectMarksLegacyIdentityUnverified(t *testing.T) {
	store := isolatedIdentityConfig(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			http.NotFound(w, r)
		case "/v1/status":
			writeJSON(t, w, 200, statusResponse{})
		case "/v1/host":
			writeJSON(t, w, 200, hostResponse{ServerIdentity: "unverified-host-claim"})
		}
	}))
	defer server.Close()
	output := captureStdout(t, func() {
		require.NoError(t, runConnectCommand(context.Background(), []string{"--endpoint", server.URL, "--token", "legacy-secret"}, commonFlags{jsonOutput: true, timeout: time.Second}))
	})
	var connected connectOutput
	require.NoError(t, json.Unmarshal([]byte(output), &connected))
	require.False(t, connected.IdentityVerified)
	require.Equal(t, "legacy_unverified", connected.Compatibility)
	cfg, _, _, err := store.Resolve("default")
	require.NoError(t, err)
	require.Empty(t, cfg.ServerIdentity)
	require.NotContains(t, output, "legacy-secret")
	require.True(t, strings.Contains(output, "identity_verified"))
}
