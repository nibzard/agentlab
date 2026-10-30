package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/connection"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func inspectionToken(t *testing.T) (string, *auth.Token) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(private)
	require.NoError(t, err)
	raw, err := auth.CreateToken(signer, auth.TokenCreateRequest{Commands: []string{"schema.read"}})
	require.NoError(t, err)
	token, err := auth.ParseTokenUnverified(raw)
	require.NoError(t, err)
	return raw, token
}

func inspectStdin(t *testing.T, input string, args []string) (string, error) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "protected-input")
	require.NoError(t, os.WriteFile(file, []byte(input), 0600))
	reader, err := os.Open(file)
	require.NoError(t, err)
	original := os.Stdin
	os.Stdin = reader
	defer func() { os.Stdin = original; reader.Close() }()
	var commandErr error
	output := captureStdout(t, func() { commandErr = runTokenInspect(context.Background(), args, commonFlags{}) })
	return output, commandErr
}

func TestTokenInspectionUsesProtectedInputAndReturnsRevocationID(t *testing.T) {
	raw, token := inspectionToken(t)
	output, err := inspectStdin(t, raw+"\n", []string{"--token-stdin", "--json"})
	require.NoError(t, err)
	require.NotContains(t, output, raw)
	var result struct {
		Issuer   string `json:"issuer"`
		ID       string `json:"revocation_id"`
		Verified bool   `json:"verified"`
	}
	require.NoError(t, json.Unmarshal([]byte(output), &result))
	require.Equal(t, token.Claims.Issuer, result.Issuer)
	require.Equal(t, auth.RevocationID(token.Claims.Issuer, token.Claims.TokenID), result.ID)
	require.False(t, result.Verified)
	for _, input := range []string{"", strings.Repeat("private-value", 2000)} {
		output, err := inspectStdin(t, input, []string{"--token-stdin", "--json"})
		require.Error(t, err)
		require.Empty(t, output)
		require.NotContains(t, err.Error(), "private-value")
	}
}

func TestTokenRevocationNegotiatesAndPinsTheServer(t *testing.T) {
	_, token := inspectionToken(t)
	id := auth.RevocationID(token.Claims.Issuer, token.Claims.TokenID)
	for _, test := range []struct {
		feature   bool
		identity  string
		wantPosts int
	}{{false, "server", 0}, {true, "", 0}, {true, "server", 1}} {
		posts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/capabilities" {
				json.NewEncoder(w).Encode(protocol.Capabilities{ProtocolVersion: 1, APISchemaVersion: 1, ServerIdentity: test.identity, Features: map[string]bool{"token_revocation": test.feature}})
				return
			}
			if r.URL.Path != "/v1/token-revocations" || r.Method != "POST" {
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				w.WriteHeader(404)
				return
			}
			posts++
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			if request["issuer"] != token.Claims.Issuer || request["revocation_id"] != id || request["server_identity"] != test.identity {
				t.Error("revocation target changed")
			}
			json.NewEncoder(w).Encode(protocol.TokenRevocation{Issuer: token.Claims.Issuer, RevocationID: id, Revoked: true, ServerIdentity: test.identity, Actor: "fixture-operator"})
		}))
		var commandErr error
		output := captureStdout(t, func() {
			commandErr = runTokenRevocation(context.Background(), "revoke", []string{"--issuer", token.Claims.Issuer, id}, commonFlags{endpoint: server.URL, jsonOutput: true, timeout: time.Second})
		})
		server.Close()
		require.Equal(t, test.wantPosts, posts)
		if test.wantPosts == 0 {
			require.Error(t, commandErr)
			continue
		}
		require.NoError(t, commandErr)
		require.Contains(t, output, `"revoked":true`)
	}
}

func TestTokenRevocationDoesNotAcceptAnotherServerReceipt(t *testing.T) {
	_, token := inspectionToken(t)
	id := auth.RevocationID(token.Claims.Issuer, token.Claims.TokenID)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			json.NewEncoder(w).Encode(protocol.Capabilities{ProtocolVersion: 1, APISchemaVersion: 1, ServerIdentity: "expected", Features: map[string]bool{"token_revocation": true}})
			return
		}
		json.NewEncoder(w).Encode(protocol.TokenRevocation{Issuer: token.Claims.Issuer, RevocationID: id, Revoked: true, ServerIdentity: "different"})
	}))
	defer server.Close()
	err := runTokenRevocation(context.Background(), "revoke", []string{"--issuer", token.Claims.Issuer, id}, commonFlags{endpoint: server.URL, jsonOutput: true, timeout: time.Second})
	require.ErrorContains(t, err, "selected server and target")
}

func TestTokenRevocationPreservesPinnedContextIdentity(t *testing.T) {
	useTempClientConfig(t)
	_, token := inspectionToken(t)
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts++
			w.WriteHeader(500)
			return
		}
		json.NewEncoder(w).Encode(protocol.Capabilities{ProtocolVersion: 1, APISchemaVersion: 1, ServerIdentity: "replacement-server", Features: map[string]bool{"token_revocation": true}})
	}))
	defer server.Close()
	store, err := contextStore()
	require.NoError(t, err)
	require.NoError(t, store.Put("operator", connection.Settings{Endpoint: server.URL, ServerIdentity: "original-server", Token: "fixture-operator"}))
	err = runTokenRevocation(context.Background(), "revoke", []string{"--issuer", token.Claims.Issuer, auth.RevocationID(token.Claims.Issuer, token.Claims.TokenID)}, commonFlags{contextName: "operator", endpoint: server.URL, jsonOutput: true, timeout: time.Second})
	require.ErrorContains(t, err, "preserve the saved context")
	require.Zero(t, posts)
}

func TestTokenStatusUsesIdentifiersWithoutClaimingValidity(t *testing.T) {
	_, token := inspectionToken(t)
	id := auth.RevocationID(token.Claims.Issuer, token.Claims.TokenID)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			json.NewEncoder(w).Encode(protocol.Capabilities{ProtocolVersion: 1, APISchemaVersion: 1, ServerIdentity: "server", Features: map[string]bool{"token_revocation": true}})
			return
		}
		if r.Method != "GET" || r.URL.Path != "/v1/token-revocations/"+id || r.URL.Query().Get("issuer") != token.Claims.Issuer {
			t.Errorf("unexpected status lookup %s %s", r.Method, r.URL)
			w.WriteHeader(400)
			return
		}
		json.NewEncoder(w).Encode(protocol.TokenRevocation{Issuer: token.Claims.Issuer, RevocationID: id, Revoked: false, ServerIdentity: "server"})
	}))
	defer server.Close()
	var commandErr error
	output := captureStdout(t, func() {
		commandErr = runTokenRevocation(context.Background(), "status", []string{"--issuer", token.Claims.Issuer, id}, commonFlags{endpoint: server.URL, jsonOutput: true, timeout: time.Second})
	})
	require.NoError(t, commandErr)
	require.Contains(t, output, `"revoked":false`)
	require.NotContains(t, output, `"valid":`)
}

func TestCredentialStatusErrorsKeepDistinctBlockedRequirements(t *testing.T) {
	for _, code := range []string{"v1/auth/token_revoked", "v1/auth/token_expired", "v1/auth/credential_status_unavailable"} {
		retryable := code == "v1/auth/credential_status_unavailable"
		var output bytes.Buffer
		writeJSONError(&output, apiResponseError{Code: code, Status: 401, Message: "credential check failed", Action: "perform the corrective action", Retryable: &retryable})
		var payload map[string]any
		require.NoError(t, json.Unmarshal(output.Bytes(), &payload))
		require.Equal(t, "blocked", payload["state"])
		require.Equal(t, code, payload["code"])
		if retryable {
			require.Equal(t, "credential_status_store", payload["requirement"])
		} else {
			require.Equal(t, "daemon_credential", payload["requirement"])
		}
		require.Equal(t, retryable, payload["retryable"])
		require.Equal(t, "perform the corrective action", payload["action"])
	}
}
