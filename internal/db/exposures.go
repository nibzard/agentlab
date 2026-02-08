// ABOUTME: Exposure database operations for sandbox port exposure registry.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Exposure represents a host-owned exposure for a sandbox port.
type Exposure struct {
	Name      string
	VMID      int
	Port      int
	TargetIP  string
	URL       string
	State     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateExposure inserts a new exposure row.
func (s *Store) CreateExposure(ctx context.Context, exposure Exposure) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	exposure.Name = strings.TrimSpace(exposure.Name)
	if exposure.Name == "" {
		return errors.New("exposure name is required")
	}
	if exposure.VMID <= 0 {
		return errors.New("exposure vmid must be positive")
	}
	if exposure.Port <= 0 || exposure.Port > 65535 {
		return errors.New("exposure port must be between 1 and 65535")
	}
	exposure.TargetIP = strings.TrimSpace(exposure.TargetIP)
	if exposure.TargetIP == "" {
		return errors.New("exposure target_ip is required")
	}
	exposure.URL = strings.TrimSpace(exposure.URL)
	exposure.State = strings.TrimSpace(exposure.State)
	if exposure.State == "" {
		return errors.New("exposure state is required")
	}
	now := time.Now().UTC()
	createdAt := exposure.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := exposure.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	var url interface{}
	if exposure.URL != "" {
		url = exposure.URL
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO exposures (name, vmid, port, target_ip, url, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		exposure.Name,
		exposure.VMID,
		exposure.Port,
		exposure.TargetIP,
		url,
		exposure.State,
		formatTime(createdAt),
		formatTime(updatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert exposure %s: %w", exposure.Name, err)
	}
	return nil
}

// GetExposure loads an exposure by name.
func (s *Store) GetExposure(ctx context.Context, name string) (Exposure, error) {
	if s == nil || s.DB == nil {
		return Exposure{}, errors.New("db store is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Exposure{}, errors.New("exposure name is required")
	}
	row := s.DB.QueryRowContext(ctx, `SELECT name, vmid, port, target_ip, url, state, created_at, updated_at
		FROM exposures WHERE name = ?`, name)
	return scanExposureRow(row)
}

// ListExposures returns all exposures ordered by created_at descending.
func (s *Store) ListExposures(ctx context.Context) ([]Exposure, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT name, vmid, port, target_ip, url, state, created_at, updated_at
		FROM exposures ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list exposures: %w", err)
	}
	defer rows.Close()
	var out []Exposure
	for rows.Next() {
		exposure, err := scanExposureRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, exposure)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate exposures: %w", err)
	}
	return out, nil
}

// DeleteExposure removes an exposure by name.
func (s *Store) DeleteExposure(ctx context.Context, name string) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("exposure name is required")
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM exposures WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("delete exposure %s: %w", name, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected delete exposure %s: %w", name, err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func scanExposureRow(scanner interface{ Scan(dest ...any) error }) (Exposure, error) {
	var exposure Exposure
	var url sql.NullString
	var createdAt string
	var updatedAt string
	if err := scanner.Scan(
		&exposure.Name,
		&exposure.VMID,
		&exposure.Port,
		&exposure.TargetIP,
		&url,
		&exposure.State,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Exposure{}, err
	}
	if url.Valid {
		exposure.URL = url.String
	}
	if createdAt != "" {
		parsed, err := parseTime(createdAt)
		if err != nil {
			return Exposure{}, fmt.Errorf("parse exposure created_at: %w", err)
		}
		exposure.CreatedAt = parsed
	}
	if updatedAt != "" {
		parsed, err := parseTime(updatedAt)
		if err != nil {
			return Exposure{}, fmt.Errorf("parse exposure updated_at: %w", err)
		}
		exposure.UpdatedAt = parsed
	}
	return exposure, nil
}
