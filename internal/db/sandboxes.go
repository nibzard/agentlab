package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/agentlab/agentlab/internal/models"
)

const timeLayout = time.RFC3339Nano

// CreateSandbox inserts a new sandbox row.
func (s *Store) CreateSandbox(ctx context.Context, sandbox models.Sandbox) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	if sandbox.VMID <= 0 {
		return errors.New("sandbox vmid is required")
	}
	if sandbox.Name == "" {
		return errors.New("sandbox name is required")
	}
	if sandbox.Profile == "" {
		return errors.New("sandbox profile is required")
	}
	if sandbox.State == "" {
		return errors.New("sandbox state is required")
	}
	now := time.Now().UTC()
	createdAt := sandbox.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := sandbox.LastUpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	var lease interface{}
	if !sandbox.LeaseExpires.IsZero() {
		lease = formatTime(sandbox.LeaseExpires)
	}
	var workspace interface{}
	if sandbox.WorkspaceID != nil && *sandbox.WorkspaceID != "" {
		workspace = *sandbox.WorkspaceID
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO sandboxes (
		vmid, name, profile, state, ip, workspace_id, keepalive, lease_expires_at, created_at, updated_at, meta_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sandbox.VMID,
		sandbox.Name,
		sandbox.Profile,
		sandbox.State,
		nullIfEmpty(sandbox.IP),
		workspace,
		sandbox.Keepalive,
		lease,
		formatTime(createdAt),
		formatTime(updatedAt),
		nil,
	)
	if err != nil {
		return fmt.Errorf("insert sandbox %d: %w", sandbox.VMID, err)
	}
	return nil
}

// GetSandbox loads a sandbox by vmid.
func (s *Store) GetSandbox(ctx context.Context, vmid int) (models.Sandbox, error) {
	if s == nil || s.DB == nil {
		return models.Sandbox{}, errors.New("db store is nil")
	}
	row := s.DB.QueryRowContext(ctx, `SELECT vmid, name, profile, state, ip, workspace_id, keepalive, lease_expires_at, created_at, updated_at
		FROM sandboxes WHERE vmid = ?`, vmid)
	return scanSandboxRow(row)
}

// ListSandboxes returns all sandboxes ordered by created_at descending.
func (s *Store) ListSandboxes(ctx context.Context) ([]models.Sandbox, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT vmid, name, profile, state, ip, workspace_id, keepalive, lease_expires_at, created_at, updated_at
		FROM sandboxes ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}
	defer rows.Close()
	var out []models.Sandbox
	for rows.Next() {
		sb, err := scanSandboxRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sb)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sandboxes: %w", err)
	}
	return out, nil
}

// MaxSandboxVMID returns the highest vmid stored, or 0 if none.
func (s *Store) MaxSandboxVMID(ctx context.Context) (int, error) {
	if s == nil || s.DB == nil {
		return 0, errors.New("db store is nil")
	}
	row := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(vmid), 0) FROM sandboxes`)
	var max int
	if err := row.Scan(&max); err != nil {
		return 0, fmt.Errorf("scan max vmid: %w", err)
	}
	return max, nil
}

// ListExpiredSandboxes returns sandboxes with leases expired at or before now.
func (s *Store) ListExpiredSandboxes(ctx context.Context, now time.Time) ([]models.Sandbox, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	cutoff := formatTime(now)
	rows, err := s.DB.QueryContext(ctx, `SELECT vmid, name, profile, state, ip, workspace_id, keepalive, lease_expires_at, created_at, updated_at
		FROM sandboxes
		WHERE lease_expires_at IS NOT NULL AND lease_expires_at <= ? AND state != ?`, cutoff, models.SandboxDestroyed)
	if err != nil {
		return nil, fmt.Errorf("list expired sandboxes: %w", err)
	}
	defer rows.Close()
	var out []models.Sandbox
	for rows.Next() {
		sb, err := scanSandboxRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sb)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired sandboxes: %w", err)
	}
	return out, nil
}

// UpdateSandboxState performs a compare-and-swap state transition.
func (s *Store) UpdateSandboxState(ctx context.Context, vmid int, from, to models.SandboxState) (bool, error) {
	if s == nil || s.DB == nil {
		return false, errors.New("db store is nil")
	}
	updatedAt := formatTime(time.Now().UTC())
	res, err := s.DB.ExecContext(ctx, `UPDATE sandboxes SET state = ?, updated_at = ? WHERE vmid = ? AND state = ?`,
		to, updatedAt, vmid, from)
	if err != nil {
		return false, fmt.Errorf("update sandbox %d state: %w", vmid, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected sandbox %d: %w", vmid, err)
	}
	return affected > 0, nil
}

// UpdateSandboxLease updates the lease expiration timestamp.
func (s *Store) UpdateSandboxLease(ctx context.Context, vmid int, leaseExpiresAt time.Time) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	var lease interface{}
	if !leaseExpiresAt.IsZero() {
		lease = formatTime(leaseExpiresAt)
	}
	updatedAt := formatTime(time.Now().UTC())
	_, err := s.DB.ExecContext(ctx, `UPDATE sandboxes SET lease_expires_at = ?, updated_at = ? WHERE vmid = ?`, lease, updatedAt, vmid)
	if err != nil {
		return fmt.Errorf("update sandbox %d lease: %w", vmid, err)
	}
	return nil
}

// UpdateSandboxIP updates the IP address for a sandbox.
func (s *Store) UpdateSandboxIP(ctx context.Context, vmid int, ip string) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	if vmid <= 0 {
		return errors.New("vmid must be positive")
	}
	updatedAt := formatTime(time.Now().UTC())
	_, err := s.DB.ExecContext(ctx, `UPDATE sandboxes SET ip = ?, updated_at = ? WHERE vmid = ?`,
		nullIfEmpty(ip),
		updatedAt,
		vmid,
	)
	if err != nil {
		return fmt.Errorf("update sandbox %d ip: %w", vmid, err)
	}
	return nil
}

// RecordEvent inserts an event row.
func (s *Store) RecordEvent(ctx context.Context, kind string, sandboxVMID *int, jobID *string, msg string, jsonPayload string) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	if kind == "" {
		return errors.New("event kind is required")
	}
	now := formatTime(time.Now().UTC())
	var vmid sql.NullInt64
	if sandboxVMID != nil {
		vmid = sql.NullInt64{Valid: true, Int64: int64(*sandboxVMID)}
	}
	var job sql.NullString
	if jobID != nil && *jobID != "" {
		job = sql.NullString{Valid: true, String: *jobID}
	}
	var msgVal interface{}
	if msg != "" {
		msgVal = msg
	}
	var jsonVal interface{}
	if jsonPayload != "" {
		jsonVal = jsonPayload
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO events (ts, kind, sandbox_vmid, job_id, msg, json) VALUES (?, ?, ?, ?, ?, ?)`,
		now, kind, vmid, job, msgVal, jsonVal)
	if err != nil {
		return fmt.Errorf("insert event %q: %w", kind, err)
	}
	return nil
}

func scanSandboxRow(scanner interface{ Scan(dest ...any) error }) (models.Sandbox, error) {
	var sb models.Sandbox
	var state string
	var ip sql.NullString
	var workspace sql.NullString
	var keepalive sql.NullBool
	var lease sql.NullString
	var createdAt string
	var updatedAt string
	if err := scanner.Scan(&sb.VMID, &sb.Name, &sb.Profile, &state, &ip, &workspace, &keepalive, &lease, &createdAt, &updatedAt); err != nil {
		return models.Sandbox{}, err
	}
	if state == "" {
		return models.Sandbox{}, errors.New("sandbox state missing")
	}
	sb.State = models.SandboxState(state)
	if ip.Valid {
		sb.IP = ip.String
	}
	if workspace.Valid {
		value := workspace.String
		sb.WorkspaceID = &value
	}
	if keepalive.Valid {
		sb.Keepalive = keepalive.Bool
	}
	if lease.Valid {
		parsed, err := parseTime(lease.String)
		if err != nil {
			return models.Sandbox{}, fmt.Errorf("parse lease_expires_at: %w", err)
		}
		sb.LeaseExpires = parsed
	}
	var err error
	if createdAt != "" {
		sb.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return models.Sandbox{}, fmt.Errorf("parse created_at: %w", err)
		}
	}
	if updatedAt != "" {
		sb.LastUpdatedAt, err = parseTime(updatedAt)
		if err != nil {
			return models.Sandbox{}, fmt.Errorf("parse updated_at: %w", err)
		}
	}
	return sb, nil
}

func parseTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(timeLayout, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed, nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(timeLayout)
}

func nullIfEmpty(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}
