// ABOUTME: Bootstrap token database operations for VM authentication.
package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// HashBootstrapToken returns the SHA-256 hex digest of a bootstrap token.
//
// Bootstrap tokens are one-time use credentials delivered to VMs at boot
// via cloud-init. Hashing ensures tokens are stored securely in the database
// for validation without storing the plaintext value.
func HashBootstrapToken(token string) (string, error) {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return "", errors.New("token is required")
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:]), nil
}

// CreateBootstrapToken inserts a bootstrap token record keyed by hash.
//
// The token hash (from HashBootstrapToken) is stored along with the VMID
// it's valid for and an expiration time. Tokens can only be used once
// (see ConsumeBootstrapToken) and must be consumed before expiration.
func (s *Store) CreateBootstrapToken(ctx context.Context, tokenHash string, vmid int, expiresAt time.Time) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	tokenHash = strings.TrimSpace(tokenHash)
	if tokenHash == "" {
		return errors.New("token hash is required")
	}
	if vmid <= 0 {
		return errors.New("vmid must be positive")
	}
	if expiresAt.IsZero() {
		return errors.New("expires_at is required")
	}
	now := formatTime(time.Now().UTC())
	_, err := s.DB.ExecContext(ctx, `INSERT INTO bootstrap_tokens (token, vmid, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		tokenHash,
		vmid,
		formatTime(expiresAt),
		now,
	)
	if err != nil {
		return fmt.Errorf("insert bootstrap token for vmid %d: %w", vmid, err)
	}
	return nil
}

// ValidateBootstrapToken reports whether a token is valid and unconsumed.
//
// Returns true if the token hash exists for the given VMID, has not been
// consumed yet, and has not expired. Returns false otherwise or on error.
func (s *Store) ValidateBootstrapToken(ctx context.Context, tokenHash string, vmid int, now time.Time) (bool, error) {
	if s == nil || s.DB == nil {
		return false, errors.New("db store is nil")
	}
	tokenHash = strings.TrimSpace(tokenHash)
	if tokenHash == "" {
		return false, errors.New("token hash is required")
	}
	if vmid <= 0 {
		return false, errors.New("vmid must be positive")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	timestamp := formatTime(now)
	row := s.DB.QueryRowContext(ctx, `SELECT 1 FROM bootstrap_tokens
		WHERE token = ? AND vmid = ? AND consumed_at IS NULL AND expires_at > ?`,
		tokenHash,
		vmid,
		timestamp,
	)
	var exists int
	if err := row.Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("validate bootstrap token for vmid %d: %w", vmid, err)
	}
	return true, nil
}

// ConsumeBootstrapToken marks a token as consumed if it is valid and unexpired.
//
// Returns true if the token was successfully consumed (i.e., it existed,
// was unconsumed, and unexpired). Returns false if the token was not found
// or was already consumed/expired. This is a one-time operation per token.
func (s *Store) ConsumeBootstrapToken(ctx context.Context, tokenHash string, vmid int, now time.Time) (bool, error) {
	if s == nil || s.DB == nil {
		return false, errors.New("db store is nil")
	}
	tokenHash = strings.TrimSpace(tokenHash)
	if tokenHash == "" {
		return false, errors.New("token hash is required")
	}
	if vmid <= 0 {
		return false, errors.New("vmid must be positive")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	timestamp := formatTime(now)
	res, err := s.DB.ExecContext(ctx, `UPDATE bootstrap_tokens
		SET consumed_at = ?
		WHERE token = ? AND vmid = ? AND consumed_at IS NULL AND expires_at > ?`,
		timestamp,
		tokenHash,
		vmid,
		timestamp,
	)
	if err != nil {
		return false, fmt.Errorf("consume bootstrap token for vmid %d: %w", vmid, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected consume bootstrap token: %w", err)
	}
	return affected > 0, nil
}
