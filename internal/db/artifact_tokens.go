// ABOUTME: Artifact token database operations for file upload authentication.
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

// ArtifactToken stores hashed upload token metadata.
//
// Artifact tokens are one-time use credentials generated when a job starts
// that authorize sandboxes to upload output files. The hash ensures tokens
// are stored securely without exposing the plaintext value.
type ArtifactToken struct {
	TokenHash  string
	JobID      string
	VMID       *int
	ExpiresAt  time.Time
	CreatedAt  time.Time
	LastUsedAt time.Time
}

// HashArtifactToken returns the SHA-256 hex digest of an artifact token.
//
// Tokens are hashed before storage to prevent plaintext token leakage
// from the database. The hash is used for token validation during
// artifact upload operations.
func HashArtifactToken(token string) (string, error) {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return "", errors.New("token is required")
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:]), nil
}

// CreateArtifactToken inserts an artifact token record keyed by hash.
//
// Creates a token associated with a specific job and optional VMID.
// The token can be used to upload artifacts until it expires.
// Tokens are tied to jobs for cleanup via foreign key constraints.
func (s *Store) CreateArtifactToken(ctx context.Context, tokenHash, jobID string, vmid int, expiresAt time.Time) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	tokenHash = strings.TrimSpace(tokenHash)
	if tokenHash == "" {
		return errors.New("token hash is required")
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return errors.New("job id is required")
	}
	if vmid <= 0 {
		return errors.New("vmid must be positive")
	}
	if expiresAt.IsZero() {
		return errors.New("expires_at is required")
	}
	now := formatTime(time.Now().UTC())
	_, err := s.DB.ExecContext(ctx, `INSERT INTO artifact_tokens (token, job_id, vmid, expires_at, created_at) VALUES (?, ?, ?, ?, ?)`,
		tokenHash,
		jobID,
		vmid,
		formatTime(expiresAt),
		now,
	)
	if err != nil {
		return fmt.Errorf("insert artifact token for job %s: %w", jobID, err)
	}
	return nil
}

// GetArtifactToken loads an artifact token by hash.
//
// Retrieves the token metadata including job association, expiration,
// and usage tracking. Returns an error if the token doesn't exist.
func (s *Store) GetArtifactToken(ctx context.Context, tokenHash string) (ArtifactToken, error) {
	if s == nil || s.DB == nil {
		return ArtifactToken{}, errors.New("db store is nil")
	}
	tokenHash = strings.TrimSpace(tokenHash)
	if tokenHash == "" {
		return ArtifactToken{}, errors.New("token hash is required")
	}
	row := s.DB.QueryRowContext(ctx, `SELECT token, job_id, vmid, expires_at, created_at, last_used_at
		FROM artifact_tokens WHERE token = ?`, tokenHash)
	return scanArtifactTokenRow(row)
}

// TouchArtifactToken updates last_used_at for a token.
//
// Updates the timestamp tracking when a token was last used for
// upload operations. This is useful for token lifecycle management
// and cleanup decisions.
func (s *Store) TouchArtifactToken(ctx context.Context, tokenHash string, now time.Time) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	tokenHash = strings.TrimSpace(tokenHash)
	if tokenHash == "" {
		return errors.New("token hash is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	timestamp := formatTime(now)
	_, err := s.DB.ExecContext(ctx, `UPDATE artifact_tokens SET last_used_at = ? WHERE token = ?`, timestamp, tokenHash)
	if err != nil {
		return fmt.Errorf("touch artifact token: %w", err)
	}
	return nil
}

func scanArtifactTokenRow(scanner interface{ Scan(dest ...any) error }) (ArtifactToken, error) {
	var token ArtifactToken
	var tokenHash string
	var jobID string
	var vmid sql.NullInt64
	var expiresAt string
	var createdAt string
	var lastUsed sql.NullString
	if err := scanner.Scan(&tokenHash, &jobID, &vmid, &expiresAt, &createdAt, &lastUsed); err != nil {
		return ArtifactToken{}, err
	}
	token.TokenHash = tokenHash
	token.JobID = jobID
	if vmid.Valid {
		value := int(vmid.Int64)
		token.VMID = &value
	}
	if expiresAt != "" {
		parsed, err := parseTime(expiresAt)
		if err != nil {
			return ArtifactToken{}, fmt.Errorf("parse expires_at: %w", err)
		}
		token.ExpiresAt = parsed
	}
	if createdAt != "" {
		parsed, err := parseTime(createdAt)
		if err != nil {
			return ArtifactToken{}, fmt.Errorf("parse created_at: %w", err)
		}
		token.CreatedAt = parsed
	}
	if lastUsed.Valid {
		parsed, err := parseTime(lastUsed.String)
		if err != nil {
			return ArtifactToken{}, fmt.Errorf("parse last_used_at: %w", err)
		}
		token.LastUsedAt = parsed
	}
	return token, nil
}
