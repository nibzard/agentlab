package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/agentlab/agentlab/internal/protocol"
)

var ErrRenewalConflict = errors.New("credential renewal conflicts with an existing candidate")
var ErrRenewalRevoked = errors.New("credential renewal involves a revoked credential")

func (s *Store) GetTokenRenewal(ctx context.Context, issuer, candidate string) (protocol.TokenRenewal, error) {
	var record protocol.TokenRenewal
	var data string
	err := s.DB.QueryRowContext(ctx, `SELECT record_json FROM ssh_token_renewals WHERE issuer=? AND candidate_id=?`, issuer, candidate).Scan(&data)
	if err != nil {
		return record, err
	}
	err = json.Unmarshal([]byte(data), &record)
	return record, err
}
func renewalRevoked(ctx context.Context, tx *sql.Tx, issuer, id string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ssh_token_revocations WHERE issuer=? AND revocation_id=?`, issuer, id).Scan(&n)
	return n > 0, err
}
func (s *Store) PrepareTokenRenewal(ctx context.Context, record protocol.TokenRenewal) (protocol.TokenRenewal, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return record, err
	}
	defer tx.Rollback()
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT record_json FROM ssh_token_renewals WHERE issuer=? AND parent_id=?`, record.Issuer, record.ParentID).Scan(&previous)
	if err == nil {
		var saved protocol.TokenRenewal
		if json.Unmarshal([]byte(previous), &saved) != nil {
			return record, fmt.Errorf("invalid renewal record")
		}
		if saved.CandidateID != record.CandidateID || saved.CandidateSHA256 != record.CandidateSHA256 || saved.ServerIdentity != record.ServerIdentity {
			return saved, ErrRenewalConflict
		}
		return saved, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return record, err
	}
	var existingCandidate int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ssh_token_renewals WHERE issuer=? AND candidate_id=?`, record.Issuer, record.CandidateID).Scan(&existingCandidate); err != nil {
		return record, err
	}
	if existingCandidate != 0 {
		return record, ErrRenewalConflict
	}
	for _, id := range []string{record.ParentID, record.CandidateID} {
		revoked, err := renewalRevoked(ctx, tx, record.Issuer, id)
		if err != nil {
			return record, err
		}
		if revoked {
			return record, ErrRenewalRevoked
		}
	}
	record.State = "prepared"
	record.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(record)
	if err != nil {
		return record, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ssh_token_renewals(issuer,parent_id,candidate_id,record_json) VALUES(?,?,?,?)`, record.Issuer, record.ParentID, record.CandidateID, string(data))
	if err != nil {
		return record, err
	}
	return record, tx.Commit()
}
func (s *Store) CommitTokenRenewal(ctx context.Context, issuer, parent, candidate, digest, server string) (protocol.TokenRenewal, error) {
	var record protocol.TokenRenewal
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return record, err
	}
	defer tx.Rollback()
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT record_json FROM ssh_token_renewals WHERE issuer=? AND candidate_id=?`, issuer, candidate).Scan(&raw)
	if err != nil {
		return record, err
	}
	if json.Unmarshal([]byte(raw), &record) != nil || record.Issuer != issuer || record.CandidateID != candidate || (record.State != "prepared" && record.State != "committed") {
		return record, fmt.Errorf("invalid renewal record")
	}
	if record.ParentID != parent || record.CandidateSHA256 != digest || record.ServerIdentity != server {
		return record, ErrRenewalConflict
	}
	revoked, err := renewalRevoked(ctx, tx, issuer, candidate)
	if err != nil {
		return record, err
	}
	if revoked {
		return record, ErrRenewalRevoked
	}
	if record.State == "committed" {
		return record, nil
	}
	revoked, err = renewalRevoked(ctx, tx, issuer, parent)
	if err != nil {
		return record, err
	}
	if revoked {
		return record, ErrRenewalRevoked
	}
	record.State = "committed"
	record.CommittedAt = time.Now().UTC().Format(time.RFC3339Nano)
	revocation := protocol.TokenRevocation{Issuer: issuer, RevocationID: parent, Revoked: true, Actor: "renewal:" + issuer, ServerIdentity: server, RevokedAt: record.CommittedAt}
	data, err := json.Marshal(revocation)
	if err != nil {
		return record, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ssh_token_revocations(issuer,revocation_id,record_json) VALUES(?,?,?)`, issuer, parent, string(data))
	if err != nil {
		return record, err
	}
	data, err = json.Marshal(record)
	if err != nil {
		return record, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE ssh_token_renewals SET record_json=? WHERE issuer=? AND candidate_id=?`, string(data), issuer, candidate)
	if err != nil {
		return record, err
	}
	return record, tx.Commit()
}
