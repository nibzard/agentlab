// ABOUTME: Per-sandbox secret storage for metadata and credential-proxy identity.
package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SandboxSecret stores the hashed per-sandbox endpoint secret.
//
// A sandbox receives its secret once, in the bootstrap response. The daemon
// keeps only the SHA-256 hash. The metadata and credential-proxy endpoints
// require the secret in addition to the source IP, so a neighbor that spoofs
// another sandbox's address cannot borrow its identity (review F4).
type SandboxSecret struct {
	VMID       int
	SecretHash string
	CreatedAt  time.Time
}

// HashSandboxSecret returns the SHA-256 hex digest of a sandbox secret.
//
// Secrets are hashed before storage so a database leak does not disclose
// them. Callers compare the digest of the presented secret against the
// stored digest in constant time.
func HashSandboxSecret(secret string) (string, error) {
	trimmed := strings.TrimSpace(secret)
	if trimmed == "" {
		return "", errors.New("sandbox secret is required")
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:]), nil
}

// UpsertSandboxSecret stores secretHash for vmid, replacing any earlier
// secret. Each bootstrap fetch rotates the secret, so the guest that fetched
// most recently holds the only valid value.
func (s *Store) UpsertSandboxSecret(ctx context.Context, vmid int, secretHash string) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	if vmid <= 0 {
		return errors.New("vmid must be positive")
	}
	secretHash = strings.TrimSpace(secretHash)
	if secretHash == "" {
		return errors.New("secret hash is required")
	}
	now := formatTime(time.Now().UTC())
	_, err := s.DB.ExecContext(ctx, `INSERT INTO sandbox_secrets (vmid, secret_hash, created_at)
		VALUES (?, ?, ?)
		ON CONFLICT(vmid) DO UPDATE SET secret_hash = excluded.secret_hash, created_at = excluded.created_at`,
		vmid,
		secretHash,
		now,
	)
	if err != nil {
		return fmt.Errorf("upsert sandbox secret for vmid %d: %w", vmid, err)
	}
	return nil
}

// GetSandboxSecretHash loads the stored secret hash for vmid. It returns
// sql.ErrNoRows when the sandbox has no secret, for example a sandbox that
// was bootstrapped before this table existed. Callers must treat that case
// as "no valid identity" and reject the request.
func (s *Store) GetSandboxSecretHash(ctx context.Context, vmid int) (string, error) {
	if s == nil || s.DB == nil {
		return "", errors.New("db store is nil")
	}
	if vmid <= 0 {
		return "", errors.New("vmid must be positive")
	}
	var secretHash string
	err := s.DB.QueryRowContext(ctx, `SELECT secret_hash FROM sandbox_secrets WHERE vmid = ?`, vmid).Scan(&secretHash)
	if err != nil {
		return "", err
	}
	return secretHash, nil
}

// DeleteSandboxSecret removes the stored secret for vmid. It is safe to call
// when no secret exists.
func (s *Store) DeleteSandboxSecret(ctx context.Context, vmid int) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	if vmid <= 0 {
		return errors.New("vmid must be positive")
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sandbox_secrets WHERE vmid = ?`, vmid)
	if err != nil {
		return fmt.Errorf("delete sandbox secret for vmid %d: %w", vmid, err)
	}
	return nil
}
