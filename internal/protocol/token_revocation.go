package protocol

type TokenRevocationRequest struct {
	Issuer         string `json:"issuer"`
	RevocationID   string `json:"revocation_id"`
	ServerIdentity string `json:"server_identity"`
}

// TokenRevocation contains identifiers and the first revocation audit record.
// It never contains a token, signature, private key, or raw token ID.
type TokenRevocation struct {
	Issuer         string `json:"issuer"`
	RevocationID   string `json:"revocation_id"`
	Revoked        bool   `json:"revoked"`
	ServerIdentity string `json:"server_identity"`
	RevokedAt      string `json:"revoked_at,omitempty"`
	Actor          string `json:"actor,omitempty"`
}
