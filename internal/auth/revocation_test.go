package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

type revocationCheckFunc func(context.Context, string, string) (bool, error)

func (f revocationCheckFunc) TokenRevoked(ctx context.Context, issuer, id string) (bool, error) {
	return f(ctx, issuer, id)
}

func TestRevocationIDBindsIssuerAndSignedNonce(t *testing.T) {
	signer := generateTestSigner(t)
	issuer := FingerprintForPublicKey(signer.PublicKey())
	id := RevocationID(issuer, "private-signed-nonce")
	require.True(t, ValidRevocationTarget(issuer, id))
	require.Len(t, id, 64)
	require.Equal(t, id, RevocationID(issuer, "private-signed-nonce"))
	require.NotEqual(t, id, RevocationID("other-issuer", "private-signed-nonce"))
	require.NotEqual(t, id, RevocationID(issuer, "other-nonce"))
	require.NotContains(t, id, "private-signed-nonce")
	require.Empty(t, RevocationID(issuer, ""))
	require.False(t, ValidRevocationTarget("SHA256:invalid", id))
	require.False(t, ValidRevocationTarget(issuer, "A"+id[1:]))
}

func TestRevocationCheckedOnEverySSHRequestWithoutFallback(t *testing.T) {
	for _, network := range []bool{false, true} {
		mw, signer := testAuthMiddleware(t)
		raw, err := CreateToken(signer, TokenCreateRequest{Commands: []string{"*"}})
		require.NoError(t, err)
		parsed, err := ParseTokenUnverified(raw)
		require.NoError(t, err)
		calls := 0
		revoked := false
		broken := false
		mw.WithRevocationChecker(revocationCheckFunc(func(ctx context.Context, issuer, id string) (bool, error) {
			calls++
			require.Equal(t, parsed.Claims.Issuer, issuer)
			require.Equal(t, parsed.Claims.TokenID, id)
			if broken {
				return false, errors.New("private-store-detail")
			}
			return revoked, nil
		}))
		// Even an identical configured legacy value must not undo SSH revocation.
		mw.legacyToken = raw
		admitted := 0
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { admitted++; w.WriteHeader(200) })
		var handler http.Handler = mw.Wrap(next)
		if network {
			handler = mw.WrapNetwork(next)
		}
		request := func() *httptest.ResponseRecorder {
			r := httptest.NewRequest("GET", "/v1/status", nil)
			r.Header.Set("Authorization", "Bearer "+raw)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			return w
		}
		require.Equal(t, 200, request().Code)
		revoked = true
		response := request()
		require.Equal(t, 401, response.Code)
		require.Equal(t, "Bearer", response.Header().Get("WWW-Authenticate"))
		require.Contains(t, response.Body.String(), `"code":"v1/auth/token_revoked"`)
		require.NotContains(t, response.Body.String(), raw)
		broken = true
		response = request()
		require.Equal(t, 503, response.Code)
		require.Contains(t, response.Body.String(), `"code":"v1/auth/credential_status_unavailable"`)
		require.NotContains(t, response.Body.String(), "private-store-detail")
		require.Equal(t, 3, calls)
		require.Equal(t, 1, admitted)
	}
}

func TestLegacyBearerAndHealthDoNotUseSSHDenylist(t *testing.T) {
	mw, _ := testAuthMiddleware(t)
	mw.WithRevocationChecker(revocationCheckFunc(func(context.Context, string, string) (bool, error) {
		t.Error("SSH denylist used for another authentication method")
		return true, nil
	}))
	handler := mw.WrapNetwork(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	for _, path := range []string{"/healthz", "/v1/status"} {
		r := httptest.NewRequest("GET", path, nil)
		if path != "/healthz" {
			r.Header.Set("Authorization", "Bearer legacy-secret")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		require.Equal(t, 200, w.Code)
	}
}

func TestSSHTokenExpiryRejectsTheExpirySecond(t *testing.T) {
	signer := generateTestSigner(t)
	raw, err := CreateToken(signer, TokenCreateRequest{Commands: []string{"schema.read"}, TTL: time.Hour})
	require.NoError(t, err)
	parsed, err := ParseTokenUnverified(raw)
	require.NoError(t, err)
	store := testKeyStore(t, signer)
	_, err = parseTokenAt(raw, store, func() time.Time { return time.Unix(parsed.Claims.ExpiresAt-1, 0) })
	require.NoError(t, err)
	_, err = parseTokenAt(raw, store, func() time.Time { return time.Unix(parsed.Claims.ExpiresAt, 0) })
	require.ErrorIs(t, err, ErrTokenExpired)
}

func TestAdmittedRequestCanFinishAfterRevocation(t *testing.T) {
	mw, signer := testAuthMiddleware(t)
	raw, err := CreateToken(signer, TokenCreateRequest{Commands: []string{"*"}})
	require.NoError(t, err)
	var revoked atomic.Bool
	mw.WithRevocationChecker(revocationCheckFunc(func(context.Context, string, string) (bool, error) { return revoked.Load(), nil }))
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	handler := mw.WrapNetwork(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(200) }))
	request := func() *http.Request {
		r := httptest.NewRequest("GET", "/v1/status", nil)
		r.Header.Set("Authorization", "Bearer "+raw)
		return r
	}
	before := httptest.NewRecorder()
	go func() { defer close(done); handler.ServeHTTP(before, request()) }()
	select {
	case <-entered:
	case <-done:
		t.Fatal("request did not reach the authenticated handler")
	case <-time.After(5 * time.Second):
		t.Fatal("request admission timed out")
	}
	revoked.Store(true)
	after := httptest.NewRecorder()
	handler.ServeHTTP(after, request())
	require.Equal(t, 401, after.Code)
	close(release)
	<-done
	require.Equal(t, 200, before.Code, "revocation does not assert cancellation of already admitted work")
}

func TestSigningKeyRetirementRequiresReloadingTheTrustedKeySet(t *testing.T) {
	signer := generateTestSigner(t)
	raw, err := CreateToken(signer, TokenCreateRequest{Commands: []string{"schema.read"}})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "authorized_keys")
	require.NoError(t, os.WriteFile(path, ssh.MarshalAuthorizedKey(signer.PublicKey()), 0600))
	configuration := MiddlewareConfig{AuthorizedKeysPath: path}
	original, err := NewMiddleware(configuration)
	require.NoError(t, err)
	status := func(middleware *Middleware) int {
		r := httptest.NewRequest("GET", "/v1/capabilities", nil)
		r.Header.Set("Authorization", "Bearer "+raw)
		w := httptest.NewRecorder()
		middleware.WrapNetwork(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(w, r)
		return w.Code
	}
	require.Equal(t, 200, status(original))
	require.NoError(t, os.WriteFile(path, nil, 0600))
	require.Equal(t, 200, status(original), "the trusted key set is loaded at startup")
	restarted, err := NewMiddleware(configuration)
	require.NoError(t, err)
	require.Equal(t, 401, status(restarted))
}
