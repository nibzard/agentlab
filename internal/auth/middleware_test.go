package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func testAuthMiddleware(t *testing.T) (*Middleware, ssh.Signer) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)

	store := NewKeyStore()
	pubKey := signer.PublicKey()
	fp := ssh.FingerprintSHA256(pubKey)
	store.keys[fp] = &KeyIdentity{
		Fingerprint: fp,
		Comment:     "test-user",
		PublicKey:   pubKey,
	}

	mw, err := NewMiddlewareWithStore(store, "legacy-secret", nil)
	require.NoError(t, err)
	return mw, signer
}

func TestMiddleware_HealthzBypass(t *testing.T) {
	mw, _ := testAuthMiddleware(t)

	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestMiddleware_NonV1Bypass(t *testing.T) {
	mw, _ := testAuthMiddleware(t)

	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/other", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestMiddleware_RequiresToken(t *testing.T) {
	mw, _ := testAuthMiddleware(t)

	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"))
}

func TestMiddleware_LegacyToken(t *testing.T) {
	mw, _ := testAuthMiddleware(t)

	var capturedID *RequestIdentity
	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Authorization", "Bearer legacy-secret")
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, capturedID)
	assert.Equal(t, "legacy-token", capturedID.Method)
}

func TestMiddleware_InvalidLegacyToken(t *testing.T) {
	mw, _ := testAuthMiddleware(t)

	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestMiddleware_SSHToken(t *testing.T) {
	mw, signer := testAuthMiddleware(t)

	tokenStr, err := CreateToken(signer, TokenCreateRequest{
		Commands: []string{"*"},
		TTL:      1 * time.Hour,
		Subject:  "ssh-test",
	})
	require.NoError(t, err)

	var capturedID *RequestIdentity
	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, capturedID)
	assert.Equal(t, "ssh-token", capturedID.Method)
	assert.Equal(t, "ssh-test", capturedID.Subject)
}

func TestMiddleware_ExpiredSSHToken(t *testing.T) {
	mw, signer := testAuthMiddleware(t)

	tokenStr, err := CreateToken(signer, TokenCreateRequest{
		Commands: []string{"*"},
		TTL:      -1 * time.Hour, // Already expired.
	})
	require.NoError(t, err)

	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestMiddleware_CIDRAllowlist(t *testing.T) {
	mw, _ := testAuthMiddleware(t)

	// Re-create with CIDR restriction.
	mw2, err := NewMiddlewareWithStore(mw.keyStore, "legacy-secret", []string{"10.0.0.0/8"})
	require.NoError(t, err)

	handler := mw2.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Allowed IP.
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Authorization", "Bearer legacy-secret")
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Blocked IP.
	req2 := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req2.Header.Set("Authorization", "Bearer legacy-secret")
	req2.RemoteAddr = "192.168.1.1:1234"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusForbidden, rec2.Code)
}

func TestRequestIdentity_CommandAllowed(t *testing.T) {
	mw, signer := testAuthMiddleware(t)

	tokenStr, err := CreateToken(signer, TokenCreateRequest{
		Commands: []string{"sandbox"},
		Scope:    []string{"sandbox:1001"},
		TTL:      1 * time.Hour,
	})
	require.NoError(t, err)

	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := FromContext(r.Context())
		require.NotNil(t, id)

		// Command permission.
		assert.True(t, id.IsCommandAllowed("sandbox.list"))
		assert.False(t, id.IsCommandAllowed("job.run"))

		// Scope permission.
		assert.True(t, id.IsSandboxAllowed(1001))
		assert.False(t, id.IsSandboxAllowed(2002))

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequestIdentity_LegacyFullAccess(t *testing.T) {
	mw, _ := testAuthMiddleware(t)

	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := FromContext(r.Context())
		require.NotNil(t, id)

		// Legacy token has full access.
		assert.True(t, id.IsCommandAllowed("anything"))
		assert.True(t, id.IsSandboxAllowed(99999))

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Authorization", "Bearer legacy-secret")
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestFromContext_Nil(t *testing.T) {
	assert.Nil(t, FromContext(nil))
}
