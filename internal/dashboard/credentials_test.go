package dashboard

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

type dashboardRefreshFixture struct {
	store               connection.ContextStore
	database            *db.Store
	server              *Server
	remote              *httptest.Server
	signer, otherSigner ssh.Signer
	request             auth.TokenCreateRequest
	settings            connection.Settings
	changedIdentity     atomic.Bool
	forwarded           atomic.Int32
}

func newDashboardRefreshFixture(t *testing.T) *dashboardRefreshFixture {
	t.Helper()
	f := &dashboardRefreshFixture{}
	for _, destination := range []*ssh.Signer{&f.signer, &f.otherSigner} {
		_, private, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		*destination, err = ssh.NewSignerFromKey(private)
		require.NoError(t, err)
	}
	keys, err := auth.ParseAuthorizedKeys(append(ssh.MarshalAuthorizedKey(f.signer.PublicKey()), ssh.MarshalAuthorizedKey(f.otherSigner.PublicKey())...))
	require.NoError(t, err)
	f.database, err = db.Open(filepath.Join(t.TempDir(), "daemon.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, f.database.Close()) })
	mux := http.NewServeMux()
	daemon.NewControlAPI(f.database, nil, nil, nil, nil, "", nil).Register(mux)
	middleware, err := auth.NewMiddlewareWithStore(keys, "", nil)
	require.NoError(t, err)
	handler := middleware.WithRevocationChecker(f.database).WrapNetwork(mux)
	f.remote = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/capabilities" {
			f.forwarded.Add(1)
		}
		if f.changedIdentity.Load() && r.URL.Path == "/v1/capabilities" {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, r)
			var caps protocol.Capabilities
			if response.Code == 200 && json.Unmarshal(response.Body.Bytes(), &caps) == nil {
				caps.ServerIdentity = "another-server"
				writeJSON(w, 200, caps)
				return
			}
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(f.remote.Close)
	identity, err := hostidentity.Local()
	require.NoError(t, err)
	f.request = auth.TokenCreateRequest{Commands: []string{"schema.read", "status.read", "token.renew"}, Scope: []string{"sandbox:1001"}, Subject: "task", TTL: time.Hour}
	parent, err := auth.CreateToken(f.signer, f.request)
	require.NoError(t, err)
	f.settings = connection.Settings{Endpoint: f.remote.URL, Token: parent, ServerIdentity: identity}
	f.store = connection.ContextStore{Root: t.TempDir(), CredentialMode: "file"}
	require.NoError(t, f.store.Put("task", f.settings))
	selection, err := connection.Prepare(context.Background(), connection.PrepareOptions{Root: f.store.Root, Context: "task"})
	require.NoError(t, err)
	resolved, err := selection.Resolve(context.Background(), connection.Overrides{})
	require.NoError(t, err)
	require.NotNil(t, resolved.CredentialBinding)
	f.server = NewServer(Config{Endpoint: resolved.Endpoint, Token: resolved.Token, CredentialBinding: resolved.CredentialBinding, SocketPath: "/must-not-use"}, log.New(io.Discard, "", 0))
	t.Cleanup(f.server.upstream.CloseIdleConnections)
	require.Equal(t, 200, f.forward(t, http.MethodGet).Code)
	return f
}

func (f *dashboardRefreshFixture) forward(t *testing.T, method string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	f.server.forward(response, method, "/v1/status", nil)
	require.NotContains(t, response.Body.String(), f.settings.Token)
	return response
}

func (f *dashboardRefreshFixture) replace(t *testing.T, signer ssh.Signer, request auth.TokenCreateRequest) string {
	t.Helper()
	raw, err := auth.CreateToken(signer, request)
	require.NoError(t, err)
	cfg := f.settings
	cfg.Token = raw
	require.NoError(t, f.store.Put("task", cfg))
	return raw
}

func TestDashboardRefreshFollowsRenewalAndPreservesSelectedContext(t *testing.T) {
	f := newDashboardRefreshFixture(t)
	candidate, err := auth.CreateToken(f.signer, f.request)
	require.NoError(t, err)
	parent, err := auth.ParseTokenUnverified(f.settings.Token)
	require.NoError(t, err)
	call := func(raw, path string, body any) *http.Response {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		req, err := http.NewRequest(http.MethodPost, f.remote.URL+path, bytes.NewReader(data))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+raw)
		response, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		return response
	}
	require.Equal(t, 200, call(f.settings.Token, "/v1/token-renewals/prepare", protocol.TokenRenewalPrepare{CandidateToken: candidate, ServerIdentity: f.settings.ServerIdentity}).StatusCode)
	require.Equal(t, 200, call(candidate, "/v1/token-renewals/commit", protocol.TokenRenewalCommit{ParentID: auth.RevocationID(parent.Claims.Issuer, parent.Claims.TokenID), ServerIdentity: f.settings.ServerIdentity}).StatusCode)
	// The parent is revoked before local publication. Do not retry it silently.
	require.Equal(t, 401, f.forward(t, http.MethodGet).Code)
	cfg := f.settings
	cfg.Token = candidate
	require.NoError(t, f.store.Put("task", cfg))
	other := cfg
	other.ServerIdentity = "unrelated-server"
	require.NoError(t, f.store.Put("other", other))
	require.NoError(t, f.store.Use("other"))
	response := f.forward(t, http.MethodGet)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), candidate)
	// A mutation carries the server binding through authentication middleware.
	require.Equal(t, http.StatusMethodNotAllowed, f.forward(t, http.MethodPost).Code)
	loaded, err := f.server.credentials.token(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, candidate, loaded)
	// Concurrent readers use the same authority baseline after publication.
	for range 8 {
		t.Run("reader", func(t *testing.T) {
			t.Parallel()
			require.Equal(t, 200, f.forward(t, http.MethodGet).Code)
		})
	}
}

func TestDashboardRefreshRejectsChangedAuthority(t *testing.T) {
	for _, field := range []string{"issuer", "subject", "commands", "scope", "unbounded", "long_lifetime"} {
		t.Run(field, func(t *testing.T) {
			f := newDashboardRefreshFixture(t)
			request, signer := f.request, f.signer
			switch field {
			case "issuer":
				signer = f.otherSigner
			case "subject":
				request.Subject = "another-task"
			case "commands":
				request.Commands = []string{"*"}
			case "scope":
				request.Scope = []string{}
			case "unbounded":
				request.TTL = 0
			case "long_lifetime":
				request.TTL = 48 * time.Hour
			}
			raw := f.replace(t, signer, request)
			if field == "unbounded" {
				parts := strings.Split(strings.TrimPrefix(raw, "agentlab."), ".")
				data, err := base64.RawURLEncoding.DecodeString(parts[1])
				require.NoError(t, err)
				var claims auth.TokenClaims
				require.NoError(t, json.Unmarshal(data, &claims))
				claims.ExpiresAt = 0
				data, err = json.Marshal(claims)
				require.NoError(t, err)
				input := parts[0] + "." + base64.RawURLEncoding.EncodeToString(data)
				signature, err := signer.Sign(rand.Reader, []byte(input))
				require.NoError(t, err)
				raw = "agentlab." + input + "." + base64.RawURLEncoding.EncodeToString(ssh.Marshal(signature))
				cfg := f.settings
				cfg.Token = raw
				require.NoError(t, f.store.Put("task", cfg))
			}
			before := f.forwarded.Load()
			response := f.forward(t, http.MethodPost)
			require.Equal(t, 409, response.Code, response.Body.String())
			require.Equal(t, before, f.forwarded.Load())
			require.NotContains(t, response.Body.String(), raw)
			require.NoError(t, f.store.Put("task", f.settings))
			require.Equal(t, 200, f.forward(t, http.MethodGet).Code)
		})
	}
}

func TestDashboardRefreshBlocksMissingStoreAndRetargeting(t *testing.T) {
	for _, change := range []string{"missing_secret", "removed_context", "endpoint", "identity", "transport_policy", "remote_identity"} {
		t.Run(change, func(t *testing.T) {
			f := newDashboardRefreshFixture(t)
			trap := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("credential reached another endpoint") }))
			defer trap.Close()
			cfg := f.settings
			switch change {
			case "missing_secret":
				item, _, _, err := f.store.LookupContext(context.Background(), "task", false)
				require.NoError(t, err)
				require.NoError(t, os.Remove(filepath.Join(f.store.Root, "credentials", item.CredentialRef+".json")))
			case "removed_context":
				require.NoError(t, f.store.Remove("task"))
			case "endpoint":
				cfg.Endpoint = trap.URL
			case "identity":
				cfg.ServerIdentity = "another-server"
			case "transport_policy":
				cfg.AllowInsecureHTTP = true
			case "remote_identity":
				f.changedIdentity.Store(true)
			}
			if change == "endpoint" || change == "identity" || change == "transport_policy" {
				require.NoError(t, f.store.Put("task", cfg))
			}
			before := f.forwarded.Load()
			response := f.forward(t, http.MethodPost)
			require.GreaterOrEqual(t, response.Code, 400)
			require.Equal(t, before, f.forwarded.Load())
		})
	}
}

func TestDashboardRefreshNeverFallsBackAfterCandidateRevocation(t *testing.T) {
	f := newDashboardRefreshFixture(t)
	candidate := f.replace(t, f.signer, f.request)
	require.Equal(t, 200, f.forward(t, http.MethodGet).Code)
	parsed, err := auth.ParseTokenUnverified(candidate)
	require.NoError(t, err)
	_, err = f.database.RevokeToken(context.Background(), protocol.TokenRevocation{Issuer: parsed.Claims.Issuer, RevocationID: auth.RevocationID(parsed.Claims.Issuer, parsed.Claims.TokenID), Revoked: true, Actor: "fixture-operator", ServerIdentity: f.settings.ServerIdentity, RevokedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	require.NoError(t, err)
	// The old credential still works, so a fallback could hide the revocation.
	request, err := http.NewRequest(http.MethodGet, f.remote.URL+"/v1/status", nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+f.settings.Token)
	parent, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	require.Equal(t, 200, parent.StatusCode)
	require.NoError(t, parent.Body.Close())
	before := f.forwarded.Load()
	blocked := f.forward(t, http.MethodPost)
	require.Equal(t, 409, blocked.Code)
	require.Contains(t, blocked.Body.String(), "dashboard_credential_rejected")
	require.Equal(t, before, f.forwarded.Load())
	require.Equal(t, 401, f.forward(t, http.MethodGet).Code)
}

func TestFixedDashboardCredentialRetainsServerIdentity(t *testing.T) {
	f := newDashboardRefreshFixture(t)
	server := NewServer(Config{Endpoint: f.settings.Endpoint, Token: f.settings.Token, ServerIdentity: f.settings.ServerIdentity}, log.New(io.Discard, "", 0))
	defer server.upstream.CloseIdleConnections()
	require.Nil(t, server.credentials)
	response := httptest.NewRecorder()
	server.forward(response, http.MethodGet, "/v1/status", nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	// Changing saved credentials cannot change an explicit startup credential.
	f.replace(t, f.otherSigner, f.request)
	response = httptest.NewRecorder()
	server.forward(response, http.MethodGet, "/v1/status", nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	f.changedIdentity.Store(true)
	before := f.forwarded.Load()
	response = httptest.NewRecorder()
	server.forward(response, http.MethodPost, "/v1/status", nil)
	require.Equal(t, 409, response.Code)
	require.Equal(t, before, f.forwarded.Load())
	require.NotContains(t, response.Body.String(), f.settings.Token)
}
