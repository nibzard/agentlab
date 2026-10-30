package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// RevocationChecker checks durable state on every authenticated SSH request.
type RevocationChecker interface {
	TokenRevoked(context.Context, string, string) (bool, error)
}

// RevocationID binds the public issuer and signed token ID. It is not a bearer
// credential. Empty legacy IDs cannot be revoked individually.
func RevocationID(issuer, tokenID string) string {
	if issuer == "" || tokenID == "" {
		return ""
	}
	digest := sha256.Sum256([]byte("agentlab-token-revocation-v1\x00" + issuer + "\x00" + tokenID))
	return hex.EncodeToString(digest[:])
}

func ValidRevocationTarget(issuer, id string) bool {
	if !strings.HasPrefix(issuer, "SHA256:") || len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" {
		return false
	}
	encoded := strings.TrimPrefix(issuer, "SHA256:")
	data, err := base64.RawStdEncoding.DecodeString(encoded)
	return err == nil && len(data) == sha256.Size && base64.RawStdEncoding.EncodeToString(data) == encoded
}
