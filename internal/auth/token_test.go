package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// testKeyStore creates a KeyStore with the given SSH signer's public key loaded.
func testKeyStore(t *testing.T, signer ssh.Signer) *KeyStore {
	t.Helper()
	store := NewKeyStore()
	pubKey := signer.PublicKey()
	fp := ssh.FingerprintSHA256(pubKey)
	store.keys[fp] = &KeyIdentity{
		Fingerprint: fp,
		Comment:     "test",
		PublicKey:   pubKey,
	}
	return store
}

// generateTestSigner creates an SSH signer from an ed25519 key pair.
func generateTestSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)
	return signer
}

func TestCreateAndParseToken(t *testing.T) {
	signer := generateTestSigner(t)
	store := testKeyStore(t, signer)

	tokenStr, err := CreateToken(signer, TokenCreateRequest{
		Commands: []string{"sandbox.list", "sandbox.show"},
		Scope:    []string{"sandbox:1001"},
		TTL:      1 * time.Hour,
		Subject:  "test-token",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, tokenStr)
	assert.Contains(t, tokenStr, "agentlab.")

	tok, err := ParseToken(tokenStr, store)
	require.NoError(t, err)
	assert.Equal(t, "test-token", tok.Claims.Subject)
	assert.Contains(t, tok.Claims.Commands, "sandbox.list")
	assert.Contains(t, tok.Claims.Scope, "sandbox:1001")
	assert.WithinDuration(t, time.Now().Add(1*time.Hour), time.Unix(tok.Claims.ExpiresAt, 0), 5*time.Second)
}

func TestCreateToken_AllCommands(t *testing.T) {
	signer := generateTestSigner(t)
	store := testKeyStore(t, signer)

	tokenStr, err := CreateToken(signer, TokenCreateRequest{
		Commands: []string{"*"},
		TTL:      30 * time.Minute,
	})
	require.NoError(t, err)

	tok, err := ParseToken(tokenStr, store)
	require.NoError(t, err)
	assert.True(t, tok.IsCommandAllowed("sandbox.list"))
	assert.True(t, tok.IsCommandAllowed("job.run"))
	assert.True(t, tok.IsCommandAllowed("anything"))
}

func TestCreateToken_NoCommands(t *testing.T) {
	signer := generateTestSigner(t)
	_, err := CreateToken(signer, TokenCreateRequest{
		Commands: []string{},
	})
	assert.ErrorIs(t, err, ErrEmptyCommands)
}

func TestCreateToken_NilSigner(t *testing.T) {
	_, err := CreateToken(nil, TokenCreateRequest{
		Commands: []string{"*"},
	})
	assert.Error(t, err)
}

func TestParseToken_InvalidFormat(t *testing.T) {
	store := NewKeyStore()

	_, err := ParseToken("not-a-token", store)
	assert.ErrorIs(t, err, ErrInvalidTokenFormat)

	_, err = ParseToken("agentlab.too.few", store)
	assert.ErrorIs(t, err, ErrInvalidTokenFormat)

	_, err = ParseToken("agentlab.invalid.invalid.invalid", store)
	assert.ErrorIs(t, err, ErrInvalidTokenFormat)
}

func TestParseToken_UnknownIssuer(t *testing.T) {
	signer := generateTestSigner(t)
	tokenStr, err := CreateToken(signer, TokenCreateRequest{
		Commands: []string{"*"},
		TTL:      1 * time.Hour,
	})
	require.NoError(t, err)

	// Use an empty store — the issuer won't be found.
	emptyStore := NewKeyStore()
	_, err = ParseToken(tokenStr, emptyStore)
	assert.ErrorIs(t, err, ErrInvalidSignature)
}

func TestParseToken_Expired(t *testing.T) {
	signer := generateTestSigner(t)
	store := testKeyStore(t, signer)

	// Create a token that expired 1 hour ago.
	tokenStr, err := CreateToken(signer, TokenCreateRequest{
		Commands: []string{"*"},
		TTL:      -1 * time.Hour, // Negative TTL creates an expired token.
	})
	require.NoError(t, err)

	_, err = ParseToken(tokenStr, store)
	assert.ErrorIs(t, err, ErrTokenExpired)
}

func TestParseToken_WrongKey(t *testing.T) {
	signer1 := generateTestSigner(t)
	signer2 := generateTestSigner(t)

	tokenStr, err := CreateToken(signer1, TokenCreateRequest{
		Commands: []string{"*"},
		TTL:      1 * time.Hour,
	})
	require.NoError(t, err)

	// Verify with a store containing only signer2's key.
	store2 := testKeyStore(t, signer2)
	_, err = ParseToken(tokenStr, store2)
	assert.ErrorIs(t, err, ErrInvalidSignature)
}

func TestToken_CommandAllowed(t *testing.T) {
	signer := generateTestSigner(t)
	store := testKeyStore(t, signer)

	tokenStr, err := CreateToken(signer, TokenCreateRequest{
		Commands: []string{"sandbox", "job.show"},
		TTL:      1 * time.Hour,
	})
	require.NoError(t, err)

	tok, err := ParseToken(tokenStr, store)
	require.NoError(t, err)

	// "sandbox" prefix should match all sandbox commands.
	assert.True(t, tok.IsCommandAllowed("sandbox"))
	assert.True(t, tok.IsCommandAllowed("sandbox.list"))
	assert.True(t, tok.IsCommandAllowed("sandbox.show"))
	assert.True(t, tok.IsCommandAllowed("sandbox.new"))

	// "job.show" should match only exact.
	assert.True(t, tok.IsCommandAllowed("job.show"))

	// Not allowed.
	assert.False(t, tok.IsCommandAllowed("job.run"))
	assert.False(t, tok.IsCommandAllowed("workspace.list"))
}

func TestToken_SandboxAllowed(t *testing.T) {
	signer := generateTestSigner(t)
	store := testKeyStore(t, signer)

	tokenStr, err := CreateToken(signer, TokenCreateRequest{
		Commands: []string{"*"},
		Scope:    []string{"sandbox:1001", "sandbox:1002"},
		TTL:      1 * time.Hour,
	})
	require.NoError(t, err)

	tok, err := ParseToken(tokenStr, store)
	require.NoError(t, err)

	assert.True(t, tok.IsSandboxAllowed(1001))
	assert.True(t, tok.IsSandboxAllowed(1002))
	assert.False(t, tok.IsSandboxAllowed(1003))
	assert.False(t, tok.IsSandboxAllowed(9999))
}

func TestToken_SandboxAllowed_NoScope(t *testing.T) {
	signer := generateTestSigner(t)
	store := testKeyStore(t, signer)

	tokenStr, err := CreateToken(signer, TokenCreateRequest{
		Commands: []string{"*"},
		// No scope — all sandboxes allowed.
		TTL: 1 * time.Hour,
	})
	require.NoError(t, err)

	tok, err := ParseToken(tokenStr, store)
	require.NoError(t, err)

	assert.True(t, tok.IsSandboxAllowed(1))
	assert.True(t, tok.IsSandboxAllowed(9999))
}

func TestParseTokenUnverified(t *testing.T) {
	signer := generateTestSigner(t)

	tokenStr, err := CreateToken(signer, TokenCreateRequest{
		Commands: []string{"*"},
		Subject:  "inspect-me",
		TTL:      1 * time.Hour,
	})
	require.NoError(t, err)

	// Parse without verification.
	tok, err := ParseTokenUnverified(tokenStr)
	require.NoError(t, err)
	assert.Equal(t, "inspect-me", tok.Claims.Subject)
	assert.Nil(t, tok.Signer) // Signer is not populated in unverified parse.
}

func TestParseTokenUnverified_Invalid(t *testing.T) {
	_, err := ParseTokenUnverified("not-a-token")
	assert.ErrorIs(t, err, ErrInvalidTokenFormat)
}

func TestToken_DefaultTTL(t *testing.T) {
	signer := generateTestSigner(t)
	store := testKeyStore(t, signer)

	tokenStr, err := CreateToken(signer, TokenCreateRequest{
		Commands: []string{"*"},
		// TTL is zero — should default to 1 hour.
	})
	require.NoError(t, err)

	tok, err := ParseToken(tokenStr, store)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(1*time.Hour), time.Unix(tok.Claims.ExpiresAt, 0), 5*time.Second)
}
