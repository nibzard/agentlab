package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentlab/agentlab/internal/hostidentity"
	"github.com/stretchr/testify/require"
)

func TestNetworkRequestBindsServerBeforeHandler(t *testing.T) {
	middleware, signer := testAuthMiddleware(t)
	signed, err := CreateToken(signer, TokenCreateRequest{Commands: []string{"*"}})
	require.NoError(t, err)
	identity, err := hostidentity.Local()
	require.NoError(t, err)
	for _, token := range []string{signed, "legacy-secret"} {
		for _, test := range []struct {
			name    string
			headers []string
			status  int
		}{
			{"omitted", nil, 200}, {"matching", []string{identity}, 200},
			{"changed", []string{"another-server"}, 409}, {"empty", []string{""}, 409},
			{"duplicate", []string{identity, identity}, 409},
		} {
			t.Run(test.name, func(t *testing.T) {
				entered := false
				handler := middleware.WrapNetwork(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { entered = true; w.WriteHeader(200) }))
				request := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", nil)
				request.Header.Set("Authorization", "Bearer "+token)
				for _, value := range test.headers {
					request.Header.Add(hostidentity.RequestHeader, value)
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				require.Equal(t, test.status, response.Code)
				require.Equal(t, test.status == 200, entered)
				require.NotContains(t, response.Body.String(), token)
				if test.status == 409 {
					require.Contains(t, response.Body.String(), "v1/auth/server_identity_mismatch")
				}
			})
		}
	}
}
