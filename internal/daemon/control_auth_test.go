package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControlAuth_RequiresToken(t *testing.T) {
	auth, err := NewControlAuth("secret-token", nil)
	require.NoError(t, err)

	handler := auth.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.RemoteAddr = "100.64.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.NotContains(t, rec.Body.String(), "secret-token")
}

func TestControlAuth_AcceptsValidToken(t *testing.T) {
	auth, err := NewControlAuth("secret-token", nil)
	require.NoError(t, err)

	handler := auth.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	req.RemoteAddr = "100.64.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestControlAuth_RejectsInvalidToken(t *testing.T) {
	auth, err := NewControlAuth("secret-token", nil)
	require.NoError(t, err)

	handler := auth.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	req.RemoteAddr = "100.64.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.NotContains(t, rec.Body.String(), "secret-token")
}

func TestControlAuth_HealthzBypass(t *testing.T) {
	auth, err := NewControlAuth("secret-token", nil)
	require.NoError(t, err)

	handler := auth.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "100.64.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestControlAuth_Allowlist(t *testing.T) {
	auth, err := NewControlAuth("secret-token", []string{"100.64.0.0/10"})
	require.NoError(t, err)

	handler := auth.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	req.RemoteAddr = "192.168.1.5:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.True(t, strings.Contains(rec.Body.String(), "remote address"))

	allowedReq := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	allowedReq.Header.Set("Authorization", "Bearer secret-token")
	allowedReq.RemoteAddr = "100.64.12.5:1234"
	allowedRec := httptest.NewRecorder()
	handler.ServeHTTP(allowedRec, allowedReq)

	assert.Equal(t, http.StatusOK, allowedRec.Code)
}
