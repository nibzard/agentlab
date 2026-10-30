package dashboard

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestRemoteRevocationStopsDashboardProxyRequests(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(private)
	require.NoError(t, err)
	keys, err := auth.ParseAuthorizedKeys(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	require.NoError(t, err)
	store, err := db.Open(filepath.Join(t.TempDir(), "dashboard.db"))
	require.NoError(t, err)
	defer store.Close()
	raw, err := auth.CreateToken(signer, auth.TokenCreateRequest{Commands: []string{"status.read"}})
	require.NoError(t, err)
	token, err := auth.ParseTokenUnverified(raw)
	require.NoError(t, err)
	middleware, err := auth.NewMiddlewareWithStore(keys, "", nil)
	require.NoError(t, err)
	remote := httptest.NewServer(middleware.WithRevocationChecker(store).WrapNetwork(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })))
	defer remote.Close()
	server := NewServer(Config{Endpoint: remote.URL, SocketPath: "/must-not-use", Token: raw}, log.New(io.Discard, "", 0))
	defer server.upstream.CloseIdleConnections()
	forward := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		server.forward(w, http.MethodGet, "/v1/status", nil)
		require.NotContains(t, w.Body.String(), raw)
		return w
	}
	require.Equal(t, 200, forward().Code)
	_, err = store.RevokeToken(context.Background(), protocol.TokenRevocation{Issuer: token.Claims.Issuer, RevocationID: auth.RevocationID(token.Claims.Issuer, token.Claims.TokenID), Revoked: true, Actor: "fixture-operator", ServerIdentity: "fixture-server", RevokedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	require.NoError(t, err)
	response := forward()
	require.Equal(t, 401, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "token revoked")
	require.NoError(t, store.Close())
	require.Equal(t, 503, forward().Code)
}
