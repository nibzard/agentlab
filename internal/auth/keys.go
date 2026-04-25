// Package auth implements SSH key-based authentication and scoped API tokens.
//
// Design (ref: IMPROVEMENT_PLAN.md §4):
//   - SSH keys are the sole identity mechanism; no passwords needed.
//   - API tokens are signed with SSH keys locally (no server-side secret storage).
//   - Tokens carry granular permissions: cmds, exp, nbf, scope.
//   - Keys ARE the identity — fingerprints serve as user IDs.
package auth

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
)

// KeyIdentity represents a user identified by their SSH public key.
type KeyIdentity struct {
	// Fingerprint is the SHA-256 fingerprint of the SSH public key (e.g., "SHA256:abc123...").
	Fingerprint string

	// Comment is the comment field from the authorized_keys line (if present).
	Comment string

	// PublicKey is the parsed SSH public key.
	PublicKey ssh.PublicKey
}

// KeyStore loads and holds authorized SSH public keys.
type KeyStore struct {
	keys map[string]*KeyIdentity // fingerprint -> identity
}

// NewKeyStore creates an empty key store.
func NewKeyStore() *KeyStore {
	return &KeyStore{keys: make(map[string]*KeyIdentity)}
}

// LoadAuthorizedKeys reads an OpenSSH authorized_keys file and loads all valid keys.
// Blank lines and lines starting with '#' are skipped.
func LoadAuthorizedKeys(path string) (*KeyStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read authorized_keys %s: %w", path, err)
	}
	return ParseAuthorizedKeys(data)
}

// ParseAuthorizedKeys parses authorized_keys formatted data.
func ParseAuthorizedKeys(data []byte) (*KeyStore, error) {
	store := NewKeyStore()
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pubKey, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			// Skip unparseable lines rather than failing entirely.
			continue
		}
		fp := ssh.FingerprintSHA256(pubKey)
		store.keys[fp] = &KeyIdentity{
			Fingerprint: fp,
			Comment:     comment,
			PublicKey:   pubKey,
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return store, nil
}

// Lookup finds a key identity by its SHA-256 fingerprint.
// Returns nil if the fingerprint is not found.
func (ks *KeyStore) Lookup(fingerprint string) *KeyIdentity {
	return ks.keys[fingerprint]
}

// Identities returns all loaded key identities.
func (ks *KeyStore) Identities() []*KeyIdentity {
	result := make([]*KeyIdentity, 0, len(ks.keys))
	for _, id := range ks.keys {
		result = append(result, id)
	}
	return result
}

// HasKey returns true if the store contains a key with the given fingerprint.
func (ks *KeyStore) HasKey(fingerprint string) bool {
	_, ok := ks.keys[fingerprint]
	return ok
}

// Count returns the number of loaded keys.
func (ks *KeyStore) Count() int {
	return len(ks.keys)
}

// FingerprintForPublicKey computes the SHA-256 fingerprint for an SSH public key.
func FingerprintForPublicKey(pubKey ssh.PublicKey) string {
	return ssh.FingerprintSHA256(pubKey)
}
