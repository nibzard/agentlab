package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/hostidentity"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestTokenRenewalPreservesScopeAndRevokesParent(t *testing.T) {
	store := newTestStore(t)
	signer := revocationSigner(t)
	keys, err := auth.ParseAuthorizedKeys(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	require.NoError(t, err)
	handler := revocationNetwork(t, store, keys)
	server, err := hostidentity.Local()
	require.NoError(t, err)
	request := auth.TokenCreateRequest{Commands: []string{"token.renew", "schema.read"}, Scope: []string{"sandbox:1001"}, Subject: "task", TTL: time.Hour}
	parent, err := auth.CreateToken(signer, request)
	require.NoError(t, err)
	candidate, err := auth.CreateToken(signer, request)
	require.NoError(t, err)
	parsed, err := auth.ParseToken(parent, keys)
	require.NoError(t, err)
	parentID := auth.RevocationID(parsed.Claims.Issuer, parsed.Claims.TokenID)
	prepared := revocationRequest(t, handler, parent, "POST", "/v1/token-renewals/prepare", protocol.TokenRenewalPrepare{CandidateToken: candidate, ServerIdentity: server})
	require.Equal(t, 200, prepared.Code, prepared.Body.String())
	require.NotContains(t, prepared.Body.String(), candidate)
	denied := revocationRequest(t, handler, parent, "POST", "/v1/token-renewals/prepare", protocol.TokenRenewalPrepare{CandidateToken: parent, ServerIdentity: server})
	require.Equal(t, 400, denied.Code)
	widened := request
	widened.Commands = []string{"*"}
	wide, err := auth.CreateToken(signer, widened)
	require.NoError(t, err)
	denied = revocationRequest(t, handler, parent, "POST", "/v1/token-renewals/prepare", protocol.TokenRenewalPrepare{CandidateToken: wide, ServerIdentity: server})
	require.Equal(t, 400, denied.Code)
	committed := revocationRequest(t, handler, candidate, "POST", "/v1/token-renewals/commit", protocol.TokenRenewalCommit{ParentID: parentID, ServerIdentity: server})
	require.Equal(t, 200, committed.Code, committed.Body.String())
	var receipt protocol.TokenRenewal
	require.NoError(t, json.Unmarshal(committed.Body.Bytes(), &receipt))
	require.Equal(t, "committed", receipt.State)
	require.Equal(t, request.Scope, receipt.Scope)
	restarted := revocationNetwork(t, store, keys)
	replay := revocationRequest(t, restarted, candidate, "POST", "/v1/token-renewals/commit", protocol.TokenRenewalCommit{ParentID: parentID, ServerIdentity: server})
	require.Equal(t, 200, replay.Code)
	require.JSONEq(t, committed.Body.String(), replay.Body.String())
	require.Equal(t, http.StatusUnauthorized, revocationRequest(t, restarted, parent, "GET", "/v1/capabilities", nil).Code)
	require.Equal(t, 200, revocationRequest(t, restarted, candidate, "GET", "/v1/capabilities", nil).Code)
	raw, err := store.GetTokenRenewal(context.Background(), receipt.Issuer, receipt.CandidateID)
	require.NoError(t, err)
	data, _ := json.Marshal(raw)
	require.NotContains(t, string(data), candidate)
	require.NotContains(t, string(data), parent)
}
func TestTokenRenewalHonorsRevocationAndPermissionDenial(t *testing.T) {
	for _, which := range []string{"parent", "candidate"} {
		t.Run(which, func(t *testing.T) {
			store := newTestStore(t)
			signer := revocationSigner(t)
			keys, err := auth.ParseAuthorizedKeys(ssh.MarshalAuthorizedKey(signer.PublicKey()))
			require.NoError(t, err)
			handler := revocationNetwork(t, store, keys)
			server, err := hostidentity.Local()
			require.NoError(t, err)
			request := auth.TokenCreateRequest{Commands: []string{"token.renew"}, TTL: time.Hour}
			parent, err := auth.CreateToken(signer, request)
			require.NoError(t, err)
			candidate, err := auth.CreateToken(signer, request)
			require.NoError(t, err)
			response := revocationRequest(t, handler, parent, "POST", "/v1/token-renewals/prepare", protocol.TokenRenewalPrepare{CandidateToken: candidate, ServerIdentity: server})
			require.Equal(t, 200, response.Code)
			var receipt protocol.TokenRenewal
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &receipt))
			id := receipt.ParentID
			if which == "candidate" {
				id = receipt.CandidateID
			}
			_, err = store.RevokeToken(context.Background(), protocol.TokenRevocation{Issuer: receipt.Issuer, RevocationID: id, Revoked: true, Actor: "operator", ServerIdentity: server, RevokedAt: time.Now().UTC().Format(time.RFC3339Nano)})
			require.NoError(t, err)
			response = revocationRequest(t, handler, candidate, "POST", "/v1/token-renewals/commit", protocol.TokenRenewalCommit{ParentID: receipt.ParentID, ServerIdentity: server})
			require.Equal(t, 401, response.Code)
			if which == "candidate" {
				// An authenticated identity can outlive a revocation. The transaction
				// must reject it without another middleware lookup.
				parsed, err := auth.ParseToken(candidate, keys)
				require.NoError(t, err)
				data, err := json.Marshal(protocol.TokenRenewalCommit{ParentID: receipt.ParentID, ServerIdentity: server})
				require.NoError(t, err)
				request := httptest.NewRequest("POST", "/v1/token-renewals/commit", bytes.NewReader(data))
				request = request.WithContext(auth.WithIdentity(request.Context(), &auth.RequestIdentity{Method: "ssh", Fingerprint: parsed.Claims.Issuer, Token: parsed}))
				rejected := httptest.NewRecorder()
				NewControlAPI(store, nil, nil, nil, nil, "", nil).handleTokenRenewalCommit(rejected, request)
				require.Equal(t, 401, rejected.Code)
				require.Contains(t, rejected.Body.String(), "renewal_credential_revoked")
			}
			saved, err := store.GetTokenRenewal(context.Background(), receipt.Issuer, receipt.CandidateID)
			require.NoError(t, err)
			require.Equal(t, "prepared", saved.State)
		})
	}
	store := newTestStore(t)
	signer := revocationSigner(t)
	keys, err := auth.ParseAuthorizedKeys(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	require.NoError(t, err)
	handler := revocationNetwork(t, store, keys)
	limited, err := auth.CreateToken(signer, auth.TokenCreateRequest{Commands: []string{"schema.read"}})
	require.NoError(t, err)
	for _, endpoint := range []string{"prepare", "commit"} {
		response := revocationRequest(t, handler, limited, "POST", "/v1/token-renewals/"+endpoint, map[string]string{})
		require.Equal(t, 403, response.Code)
	}
	response := revocationRequest(t, handler, "legacy-root", "POST", "/v1/token-renewals/prepare", map[string]string{"candidate_token": strings.Repeat("a", 10)})
	require.Equal(t, 403, response.Code)
}

func TestTokenRenewalAcceptsDelayedValidStagingButBoundsLifetime(t *testing.T) {
	signer := revocationSigner(t)
	keys, err := auth.ParseAuthorizedKeys(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	require.NoError(t, err)
	sign := func(id string, notBefore, expires int64) string {
		header, err := json.Marshal(auth.TokenHeader{Algorithm: signer.PublicKey().Type(), Type: "agentlab-token"})
		require.NoError(t, err)
		claims, err := json.Marshal(auth.TokenClaims{Issuer: auth.FingerprintForPublicKey(signer.PublicKey()), Commands: []string{"token.renew"}, TokenID: id, NotBefore: notBefore, ExpiresAt: expires})
		require.NoError(t, err)
		input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
		signature, err := signer.Sign(nil, []byte(input))
		require.NoError(t, err)
		return "agentlab." + input + "." + base64.RawURLEncoding.EncodeToString(ssh.Marshal(signature))
	}
	store := newTestStore(t)
	handler := revocationNetwork(t, store, keys)
	server, err := hostidentity.Local()
	require.NoError(t, err)
	now := time.Now()
	parent := sign("delayed-parent", now.Add(-48*time.Hour).Unix(), now.Add(time.Hour).Unix())
	candidate := sign("delayed-candidate", now.Add(-10*time.Minute).Unix(), now.Add(time.Hour).Unix())
	response := revocationRequest(t, handler, parent, "POST", "/v1/token-renewals/prepare", protocol.TokenRenewalPrepare{CandidateToken: candidate, ServerIdentity: server})
	require.Equal(t, 200, response.Code, response.Body.String())
	excessive := sign("excessive-candidate", now.Add(-25*time.Hour).Unix(), now.Add(time.Hour).Unix())
	response = revocationRequest(t, handler, parent, "POST", "/v1/token-renewals/prepare", protocol.TokenRenewalPrepare{CandidateToken: excessive, ServerIdentity: server})
	require.Equal(t, 400, response.Code)
}
