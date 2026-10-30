package protocol

// TokenRenewal contains public identifiers and the exact admitted permissions.
// The signed credential is never included in a receipt or audit record.
type TokenRenewal struct {
	Issuer             string   `json:"issuer"`
	ParentID           string   `json:"parent_revocation_id"`
	CandidateID        string   `json:"candidate_revocation_id"`
	CandidateSHA256    string   `json:"candidate_sha256"`
	ServerIdentity     string   `json:"server_identity"`
	State              string   `json:"state"`
	Subject            string   `json:"subject,omitempty"`
	Commands           []string `json:"commands"`
	Scope              []string `json:"scope,omitempty"`
	CreatedAt          string   `json:"created_at"`
	CommittedAt        string   `json:"committed_at,omitempty"`
	CandidateExpiresAt int64    `json:"candidate_expires_at"`
}
type TokenRenewalPrepare struct {
	CandidateToken string `json:"candidate_token"`
	ServerIdentity string `json:"server_identity"`
}
type TokenRenewalCommit struct {
	ParentID       string `json:"parent_revocation_id"`
	ServerIdentity string `json:"server_identity"`
}
