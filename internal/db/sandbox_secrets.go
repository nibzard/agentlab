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
	err := s.DB.QueryRowContext(ctx, `SELECT secret_hash FROM sandbox_secrets WHERE vmid = ? AND (expires_at IS NULL OR expires_at > ?)`, vmid, formatTime(time.Now().UTC())).Scan(&secretHash)
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
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE guest_identities SET revoked=1,public_key=NULL WHERE vmid=?`, vmid)
	if err != nil {
		return err
	}
	for _, query := range []string{`DELETE FROM guest_challenges WHERE incarnation IN (SELECT incarnation FROM guest_identities WHERE vmid=?)`, `DELETE FROM guest_deliveries WHERE incarnation IN (SELECT incarnation FROM guest_identities WHERE vmid=?)`, `DELETE FROM sandbox_secrets WHERE vmid=?`} {
		if _, err = tx.ExecContext(ctx, query, vmid); err != nil {
			return err
		}
	}
	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("delete sandbox secret for vmid %d: %w", vmid, err)
	}
	return nil
}

// UpsertSandboxSecretForIncarnation prevents a delayed legacy bootstrap fetch
// from restoring credentials after destruction or numeric VMID reuse.
func (s *Store) UpsertSandboxSecretForIncarnation(ctx context.Context, vmid int, hash, incarnation string) error {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO sandbox_secrets(vmid,secret_hash,created_at) SELECT g.vmid,?,? FROM guest_identities g JOIN sandboxes s ON s.vmid=g.vmid WHERE g.vmid=? AND g.incarnation=? AND g.revoked=0 AND s.state NOT IN ('DESTROYED','DESTROYING') ON CONFLICT(vmid) DO UPDATE SET secret_hash=excluded.secret_hash,created_at=excluded.created_at`, hash, formatTime(time.Now().UTC()), vmid, incarnation)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrGuestProof
	}
	return nil
}
