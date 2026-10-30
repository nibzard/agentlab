package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestCapabilitiesPreservesScopedAuthorization(t *testing.T) {
	api := NewControlAPI(newTestStore(t), nil, nil, nil, nil, "", nil)
	claims := auth.TokenClaims{Commands: []string{"schema.read", "sandbox.read"}, Scope: []string{"sandbox:1001"}, ExpiresAt: 1234567890}
	actor := &auth.RequestIdentity{Method: "ssh-token", Fingerprint: "signer-fingerprint", Subject: "task", Token: &auth.Token{Claims: claims, Raw: "credential-must-not-appear"}}
	request := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	request = request.WithContext(auth.WithIdentity(request.Context(), actor))
	response := httptest.NewRecorder()
	api.handleCapabilities(response, request)
	require.Equal(t, 200, response.Code)
	var result protocol.Capabilities
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.True(t, result.Compatible())
	require.Equal(t, actor.Fingerprint, result.Authorization.Issuer)
	require.Equal(t, claims.Commands, result.Authorization.Commands)
	require.Equal(t, claims.Scope, result.Authorization.Scope)
	require.Equal(t, claims.ExpiresAt, result.Authorization.ExpiresAt)
	require.Equal(t, result.ServerIdentity != "", result.Features["idempotent_job_submission"])
	require.False(t, result.Features["idempotent_workspace_create"])
	require.NotContains(t, response.Body.String(), actor.Token.Raw)
	actor.Token.Claims.Commands = []string{"sandbox.read"}
	denied := httptest.NewRecorder()
	api.handleCapabilities(denied, request)
	require.Equal(t, 403, denied.Code)
}

func TestCapabilitiesAdvertisesDurableWorkspaceCreation(t *testing.T) {
	api, _, _, _ := workspaceSubmissionAPI(t)
	response := httptest.NewRecorder()
	api.handleCapabilities(response, httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil))
	require.Equal(t, 200, response.Code)
	var result protocol.Capabilities
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Equal(t, result.ServerIdentity != "", result.Features["idempotent_workspace_create"])
	require.False(t, result.Features["idempotent_branch_sessions"])
}
