package daemon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/hostidentity"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func revocationSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(private)
	require.NoError(t, err)
	return signer
}

func revocationNetwork(t *testing.T, store *db.Store, keys *auth.KeyStore) http.Handler {
	t.Helper()
	api := NewControlAPI(store, nil, nil, nil, nil, "", nil)
	mux := http.NewServeMux()
	api.Register(mux)
	middleware, err := auth.NewMiddlewareWithStore(keys, "legacy-root", nil)
	require.NoError(t, err)
	return middleware.WithRevocationChecker(store).WrapNetwork(mux)
}

func revocationRequest(t *testing.T, handler http.Handler, token, method, path string, input any) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if input != nil {
		var err error
		data, err = json.Marshal(input)
		require.NoError(t, err)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	require.NotContains(t, w.Body.String(), token, "a credential entered the response")
	return w
}

func TestSSHTokenRevocationNetworkBoundaryAndRestart(t *testing.T) {
	store := newTestStore(t)
	admin, task := revocationSigner(t), revocationSigner(t)
	keys, err := auth.ParseAuthorizedKeys(append(ssh.MarshalAuthorizedKey(admin.PublicKey()), ssh.MarshalAuthorizedKey(task.PublicKey())...))
	require.NoError(t, err)
	adminToken, err := auth.CreateToken(admin, auth.TokenCreateRequest{Commands: []string{"schema.read", "token.revoke", "token.read"}})
	require.NoError(t, err)
	newTask := func() string {
		token, err := auth.CreateToken(task, auth.TokenCreateRequest{Commands: []string{"schema.read"}, Scope: []string{"sandbox:1001"}})
		require.NoError(t, err)
		return token
	}
	target, other := newTask(), newTask()
	parsed, err := auth.ParseTokenUnverified(target)
	require.NoError(t, err)
	id := auth.RevocationID(parsed.Claims.Issuer, parsed.Claims.TokenID)
	identity, err := hostidentity.Local()
	require.NoError(t, err)
	input := map[string]string{"issuer": parsed.Claims.Issuer, "revocation_id": id, "server_identity": identity}
	path := "/v1/token-revocations/" + id + "?issuer=" + url.QueryEscape(parsed.Claims.Issuer)
	handler := revocationNetwork(t, store, keys)
	require.Equal(t, 200, revocationRequest(t, handler, target, "GET", "/v1/capabilities", nil).Code)
	response := revocationRequest(t, handler, adminToken, "GET", path, nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"revoked":false`)
	response = revocationRequest(t, handler, adminToken, "POST", "/v1/token-revocations", input)
	require.Equal(t, 200, response.Code, response.Body.String())
	var first protocol.TokenRevocation
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &first))
	require.True(t, first.Revoked)
	require.Equal(t, "ssh-token:"+auth.FingerprintForPublicKey(admin.PublicKey()), first.Actor)
	require.NotContains(t, response.Body.String(), parsed.Claims.TokenID)
	replay := revocationRequest(t, handler, adminToken, "POST", "/v1/token-revocations", input)
	require.Equal(t, 200, replay.Code, replay.Body.String())
	require.JSONEq(t, response.Body.String(), replay.Body.String())
	require.Equal(t, 401, revocationRequest(t, handler, target, "GET", "/v1/capabilities", nil).Code)
	require.Equal(t, 200, revocationRequest(t, handler, other, "GET", "/v1/capabilities", nil).Code)
	require.NoError(t, store.Close())
	restarted, err := db.Open(store.Path)
	require.NoError(t, err)
	defer restarted.Close()
	handler = revocationNetwork(t, restarted, keys)
	require.Equal(t, 401, revocationRequest(t, handler, target, "GET", "/v1/capabilities", nil).Code)
	require.Equal(t, 200, revocationRequest(t, handler, other, "GET", "/v1/capabilities", nil).Code)
	response = revocationRequest(t, handler, adminToken, "GET", path, nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	var recovered protocol.TokenRevocation
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &recovered))
	require.Equal(t, first, recovered)
	require.NoError(t, restarted.Close())
	response = revocationRequest(t, handler, other, "GET", "/v1/capabilities", nil)
	require.Equal(t, 503, response.Code, response.Body.String())
}

func TestRevocationRejectsScopeAndServerChangesBeforeMutation(t *testing.T) {
	store := newTestStore(t)
	signer := revocationSigner(t)
	keys, err := auth.ParseAuthorizedKeys(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	require.NoError(t, err)
	identity, err := hostidentity.Local()
	require.NoError(t, err)
	issuer := auth.FingerprintForPublicKey(signer.PublicKey())
	id := auth.RevocationID(issuer, "target-nonce")
	handler := revocationNetwork(t, store, keys)
	for _, test := range []struct {
		commands, scope []string
		server          string
		status          int
	}{
		{[]string{"token.read"}, nil, identity, 403},
		{[]string{"*"}, []string{"sandbox:1001"}, identity, 403},
		{[]string{"token.revoke"}, nil, "changed-server", 409},
	} {
		token, err := auth.CreateToken(signer, auth.TokenCreateRequest{Commands: test.commands, Scope: test.scope})
		require.NoError(t, err)
		response := revocationRequest(t, handler, token, "POST", "/v1/token-revocations", map[string]string{"issuer": issuer, "revocation_id": id, "server_identity": test.server})
		require.Equal(t, test.status, response.Code, response.Body.String())
		_, err = store.GetTokenRevocation(context.Background(), issuer, id)
		require.ErrorIs(t, err, sql.ErrNoRows)
	}
}

func TestRevocationRejectsCredentialsAndMalformedBodies(t *testing.T) {
	store := newTestStore(t)
	signer := revocationSigner(t)
	keys, err := auth.ParseAuthorizedKeys(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	require.NoError(t, err)
	admin, err := auth.CreateToken(signer, auth.TokenCreateRequest{Commands: []string{"token.revoke"}, TTL: time.Hour})
	require.NoError(t, err)
	handler := revocationNetwork(t, store, keys)
	for _, body := range []string{`{"token":"must-not-echo-this-credential"}`, strings.Repeat("x", 17<<10), `{} {}`, `{"issuer":"bad","revocation_id":"bad"}`} {
		r := httptest.NewRequest("POST", "/v1/token-revocations", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+admin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		require.Equal(t, 400, w.Code, w.Body.String())
		require.NotContains(t, w.Body.String(), "must-not-echo-this-credential")
	}
}
