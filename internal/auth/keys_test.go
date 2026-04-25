package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestParseAuthorizedKeys(t *testing.T) {
	// Generate two ed25519 key pairs and build an authorized_keys string.
	_, priv1, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	_, priv2, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	pub1, err := ssh.NewPublicKey(priv1.Public())
	require.NoError(t, err)
	pub2, err := ssh.NewPublicKey(priv2.Public())
	require.NoError(t, err)

	authorizedKeys := "# This is a comment\n" +
		strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub1))) + " alice@example.com\n" +
		strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub2))) + " bob@example.com\n"

	store, err := ParseAuthorizedKeys([]byte(authorizedKeys))
	require.NoError(t, err)
	assert.Equal(t, 2, store.Count())

	identities := store.Identities()
	assert.Len(t, identities, 2)

	found := 0
	for _, id := range identities {
		if id.Comment == "alice@example.com" {
			assert.Equal(t, "ssh-ed25519", id.PublicKey.Type())
			assert.True(t, store.HasKey(id.Fingerprint))
			found++
		}
		if id.Comment == "bob@example.com" {
			assert.Equal(t, "ssh-ed25519", id.PublicKey.Type())
			assert.True(t, store.HasKey(id.Fingerprint))
			found++
		}
	}
	assert.Equal(t, 2, found)
}

func TestParseAuthorizedKeys_SkipsBadLines(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	pub, err := ssh.NewPublicKey(priv.Public())
	require.NoError(t, err)

	data := string(ssh.MarshalAuthorizedKey(pub)) + " valid@example.com\n" +
		"this is not a valid key line\n" +
		"also-bad\n"

	store, err := ParseAuthorizedKeys([]byte(data))
	require.NoError(t, err)
	assert.Equal(t, 1, store.Count())
	assert.True(t, store.HasKey(store.Identities()[0].Fingerprint))
}

func TestParseAuthorizedKeys_Empty(t *testing.T) {
	store, err := ParseAuthorizedKeys([]byte(""))
	require.NoError(t, err)
	assert.Equal(t, 0, store.Count())
}

func TestKeyStore_Lookup_NotFound(t *testing.T) {
	store := NewKeyStore()
	assert.Nil(t, store.Lookup("SHA256:nonexistent"))
	assert.False(t, store.HasKey("SHA256:nonexistent"))
}

func TestFingerprintForPublicKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	pub, err := ssh.NewPublicKey(priv.Public())
	require.NoError(t, err)

	fp := FingerprintForPublicKey(pub)
	assert.Contains(t, fp, "SHA256:")
	assert.Equal(t, ssh.FingerprintSHA256(pub), fp)
}
