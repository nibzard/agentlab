package db

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// CreateBootstrapToken inserts a bootstrap token record.
func (s *Store) CreateBootstrapToken(ctx context.Context, token string, vmid int, expiresAt time.Time) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	if token == "" {
		return errors.New("token is required")
	}
	if vmid <= 0 {
		return errors.New("vmid must be positive")
	}
	if expiresAt.IsZero() {
		return errors.New("expires_at is required")
	}
	now := formatTime(time.Now().UTC())
	_, err := s.DB.ExecContext(ctx, `INSERT INTO bootstrap_tokens (token, vmid, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		token,
		vmid,
		formatTime(expiresAt),
		now,
	)
	if err != nil {
		return fmt.Errorf("insert bootstrap token for vmid %d: %w", vmid, err)
	}
	return nil
}
