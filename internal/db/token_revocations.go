package db

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/protocol"
)

func (s *Store) TokenRevoked(ctx context.Context, issuer, tokenID string) (bool, error) {
	if s == nil || s.DB == nil {
		return false, fmt.Errorf("revocation store unavailable")
	}
	id := auth.RevocationID(issuer, tokenID)
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM ssh_token_revocations WHERE issuer=? AND revocation_id=?`, issuer, id).Scan(&count)
	return count > 0, err
}

// RevokeToken preserves the first actor and timestamp across identical retries.
// Tombstones are retained after expiry, so reusing an ID cannot restore access.
func (s *Store) RevokeToken(ctx context.Context, record protocol.TokenRevocation) (protocol.TokenRevocation, error) {
	if s == nil || s.DB == nil {
		return record, fmt.Errorf("revocation store unavailable")
	}
	if !auth.ValidRevocationTarget(record.Issuer, record.RevocationID) || !record.Revoked || record.Actor == "" || record.ServerIdentity == "" || record.RevokedAt == "" {
		return record, fmt.Errorf("incomplete token revocation")
	}
	data, err := json.Marshal(record)
	if err != nil {
		return record, err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO ssh_token_revocations(issuer,revocation_id,record_json) VALUES(?,?,?) ON CONFLICT(issuer,revocation_id) DO NOTHING`, record.Issuer, record.RevocationID, string(data))
	if err != nil {
		return record, err
	}
	return s.GetTokenRevocation(ctx, record.Issuer, record.RevocationID)
}

func (s *Store) GetTokenRevocation(ctx context.Context, issuer, id string) (protocol.TokenRevocation, error) {
	var record protocol.TokenRevocation
	if s == nil || s.DB == nil {
		return record, fmt.Errorf("revocation store unavailable")
	}
	var data string
	err := s.DB.QueryRowContext(ctx, `SELECT record_json FROM ssh_token_revocations WHERE issuer=? AND revocation_id=?`, issuer, id).Scan(&data)
	if err != nil {
		return record, err
	}
	err = json.Unmarshal([]byte(data), &record)
	return record, err
}
