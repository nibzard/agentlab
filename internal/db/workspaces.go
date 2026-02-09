// ABOUTME: Workspace database operations for persistent storage volumes.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/models"
)

// CreateWorkspace inserts a new workspace row into the database.
func (s *Store) CreateWorkspace(ctx context.Context, workspace models.Workspace) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	if strings.TrimSpace(workspace.ID) == "" {
		return errors.New("workspace id is required")
	}
	if strings.TrimSpace(workspace.Name) == "" {
		return errors.New("workspace name is required")
	}
	if strings.TrimSpace(workspace.Storage) == "" {
		return errors.New("workspace storage is required")
	}
	if strings.TrimSpace(workspace.VolumeID) == "" {
		return errors.New("workspace volume id is required")
	}
	if workspace.SizeGB <= 0 {
		return errors.New("workspace size_gb must be positive")
	}
	now := time.Now().UTC()
	createdAt := workspace.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := workspace.LastUpdated
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	var leaseOwner interface{}
	if strings.TrimSpace(workspace.LeaseOwner) != "" {
		leaseOwner = strings.TrimSpace(workspace.LeaseOwner)
	}
	var leaseNonce interface{}
	if strings.TrimSpace(workspace.LeaseNonce) != "" {
		leaseNonce = strings.TrimSpace(workspace.LeaseNonce)
	}
	var leaseExpires interface{}
	if !workspace.LeaseExpires.IsZero() {
		leaseExpires = formatTime(workspace.LeaseExpires)
	}
	var attached interface{}
	if workspace.AttachedVM != nil && *workspace.AttachedVM > 0 {
		attached = *workspace.AttachedVM
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO workspaces (
		id, name, storage, volid, size_gb, attached_vmid, lease_owner, lease_nonce, lease_expires_at, created_at, updated_at, meta_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		workspace.ID,
		workspace.Name,
		workspace.Storage,
		workspace.VolumeID,
		workspace.SizeGB,
		attached,
		leaseOwner,
		leaseNonce,
		leaseExpires,
		formatTime(createdAt),
		formatTime(updatedAt),
		nil,
	)
	if err != nil {
		return fmt.Errorf("insert workspace %s: %w", workspace.ID, err)
	}
	return nil
}

// GetWorkspace loads a workspace by id.
func (s *Store) GetWorkspace(ctx context.Context, id string) (models.Workspace, error) {
	if s == nil || s.DB == nil {
		return models.Workspace{}, errors.New("db store is nil")
	}
	row := s.DB.QueryRowContext(ctx, `SELECT id, name, storage, volid, size_gb, attached_vmid, lease_owner, lease_nonce, lease_expires_at, created_at, updated_at
		FROM workspaces WHERE id = ?`, id)
	return scanWorkspaceRow(row)
}

// GetWorkspaceByName loads a workspace by name.
func (s *Store) GetWorkspaceByName(ctx context.Context, name string) (models.Workspace, error) {
	if s == nil || s.DB == nil {
		return models.Workspace{}, errors.New("db store is nil")
	}
	row := s.DB.QueryRowContext(ctx, `SELECT id, name, storage, volid, size_gb, attached_vmid, lease_owner, lease_nonce, lease_expires_at, created_at, updated_at
		FROM workspaces WHERE name = ?`, name)
	return scanWorkspaceRow(row)
}

// GetWorkspaceByAttachedVMID loads a workspace attached to a vmid.
func (s *Store) GetWorkspaceByAttachedVMID(ctx context.Context, vmid int) (models.Workspace, error) {
	if s == nil || s.DB == nil {
		return models.Workspace{}, errors.New("db store is nil")
	}
	if vmid <= 0 {
		return models.Workspace{}, errors.New("vmid must be positive")
	}
	row := s.DB.QueryRowContext(ctx, `SELECT id, name, storage, volid, size_gb, attached_vmid, lease_owner, lease_nonce, lease_expires_at, created_at, updated_at
		FROM workspaces WHERE attached_vmid = ?`, vmid)
	return scanWorkspaceRow(row)
}

// ListWorkspaces returns all workspaces ordered by created_at descending.
func (s *Store) ListWorkspaces(ctx context.Context) ([]models.Workspace, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, storage, volid, size_gb, attached_vmid, lease_owner, lease_nonce, lease_expires_at, created_at, updated_at
		FROM workspaces ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	defer rows.Close()
	var out []models.Workspace
	for rows.Next() {
		ws, err := scanWorkspaceRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ws)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workspaces: %w", err)
	}
	return out, nil
}

// AttachWorkspace sets attached_vmid if currently unattached.
func (s *Store) AttachWorkspace(ctx context.Context, id string, vmid int) (bool, error) {
	if s == nil || s.DB == nil {
		return false, errors.New("db store is nil")
	}
	if strings.TrimSpace(id) == "" {
		return false, errors.New("workspace id is required")
	}
	if vmid <= 0 {
		return false, errors.New("vmid must be positive")
	}
	updatedAt := formatTime(time.Now().UTC())
	id = strings.TrimSpace(id)
	res, err := s.DB.ExecContext(ctx, `UPDATE workspaces SET attached_vmid = ?, updated_at = ?
		WHERE id = ? AND attached_vmid IS NULL`, vmid, updatedAt, id)
	if err != nil {
		return false, fmt.Errorf("attach workspace %s: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected attach workspace %s: %w", id, err)
	}
	return affected > 0, nil
}

// DetachWorkspace clears attached_vmid if it matches the provided vmid.
func (s *Store) DetachWorkspace(ctx context.Context, id string, vmid int) (bool, error) {
	if s == nil || s.DB == nil {
		return false, errors.New("db store is nil")
	}
	if strings.TrimSpace(id) == "" {
		return false, errors.New("workspace id is required")
	}
	if vmid <= 0 {
		return false, errors.New("vmid must be positive")
	}
	updatedAt := formatTime(time.Now().UTC())
	id = strings.TrimSpace(id)
	res, err := s.DB.ExecContext(ctx, `UPDATE workspaces SET attached_vmid = NULL, updated_at = ?
		WHERE id = ? AND attached_vmid = ?`, updatedAt, id, vmid)
	if err != nil {
		return false, fmt.Errorf("detach workspace %s: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected detach workspace %s: %w", id, err)
	}
	return affected > 0, nil
}

func scanWorkspaceRow(scanner interface{ Scan(dest ...any) error }) (models.Workspace, error) {
	var ws models.Workspace
	var attached sql.NullInt64
	var leaseOwner sql.NullString
	var leaseNonce sql.NullString
	var leaseExpires sql.NullString
	var createdAt string
	var updatedAt string
	if err := scanner.Scan(&ws.ID, &ws.Name, &ws.Storage, &ws.VolumeID, &ws.SizeGB, &attached, &leaseOwner, &leaseNonce, &leaseExpires, &createdAt, &updatedAt); err != nil {
		return models.Workspace{}, err
	}
	if attached.Valid {
		value := int(attached.Int64)
		ws.AttachedVM = &value
	}
	if leaseOwner.Valid {
		ws.LeaseOwner = leaseOwner.String
	}
	if leaseNonce.Valid {
		ws.LeaseNonce = leaseNonce.String
	}
	if leaseExpires.Valid && leaseExpires.String != "" {
		parsed, err := parseTime(leaseExpires.String)
		if err != nil {
			return models.Workspace{}, fmt.Errorf("parse lease_expires_at: %w", err)
		}
		ws.LeaseExpires = parsed
	}
	var err error
	if createdAt != "" {
		ws.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return models.Workspace{}, fmt.Errorf("parse created_at: %w", err)
		}
	}
	if updatedAt != "" {
		ws.LastUpdated, err = parseTime(updatedAt)
		if err != nil {
			return models.Workspace{}, fmt.Errorf("parse updated_at: %w", err)
		}
	}
	return ws, nil
}

// GetWorkspaceByLeaseOwner loads a workspace by its lease owner.
func (s *Store) GetWorkspaceByLeaseOwner(ctx context.Context, owner string) (models.Workspace, error) {
	if s == nil || s.DB == nil {
		return models.Workspace{}, errors.New("db store is nil")
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return models.Workspace{}, errors.New("lease owner is required")
	}
	row := s.DB.QueryRowContext(ctx, `SELECT id, name, storage, volid, size_gb, attached_vmid, lease_owner, lease_nonce, lease_expires_at, created_at, updated_at
		FROM workspaces WHERE lease_owner = ?`, owner)
	return scanWorkspaceRow(row)
}

// TryAcquireWorkspaceLease attempts to acquire the workspace lease if unheld or expired.
func (s *Store) TryAcquireWorkspaceLease(ctx context.Context, id, owner, nonce string, expiresAt time.Time) (bool, error) {
	if s == nil || s.DB == nil {
		return false, errors.New("db store is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false, errors.New("workspace id is required")
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return false, errors.New("lease owner is required")
	}
	nonce = strings.TrimSpace(nonce)
	if nonce == "" {
		return false, errors.New("lease nonce is required")
	}
	if expiresAt.IsZero() {
		return false, errors.New("lease expires_at is required")
	}
	now := time.Now().UTC()
	cutoff := formatTime(now)
	updatedAt := formatTime(now)
	res, err := s.DB.ExecContext(ctx, `UPDATE workspaces
		SET lease_owner = ?, lease_nonce = ?, lease_expires_at = ?, updated_at = ?
		WHERE id = ? AND (lease_owner IS NULL OR lease_expires_at IS NULL OR lease_expires_at <= ?)`,
		owner, nonce, formatTime(expiresAt), updatedAt, id, cutoff)
	if err != nil {
		return false, fmt.Errorf("acquire workspace lease %s: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected acquire workspace lease %s: %w", id, err)
	}
	return affected > 0, nil
}

// RenewWorkspaceLease extends an existing lease if the owner and nonce match.
func (s *Store) RenewWorkspaceLease(ctx context.Context, id, owner, nonce string, expiresAt time.Time) (bool, error) {
	if s == nil || s.DB == nil {
		return false, errors.New("db store is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false, errors.New("workspace id is required")
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return false, errors.New("lease owner is required")
	}
	nonce = strings.TrimSpace(nonce)
	if nonce == "" {
		return false, errors.New("lease nonce is required")
	}
	if expiresAt.IsZero() {
		return false, errors.New("lease expires_at is required")
	}
	updatedAt := formatTime(time.Now().UTC())
	res, err := s.DB.ExecContext(ctx, `UPDATE workspaces
		SET lease_expires_at = ?, updated_at = ?
		WHERE id = ? AND lease_owner = ? AND lease_nonce = ?`,
		formatTime(expiresAt), updatedAt, id, owner, nonce)
	if err != nil {
		return false, fmt.Errorf("renew workspace lease %s: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected renew workspace lease %s: %w", id, err)
	}
	return affected > 0, nil
}

// ReleaseWorkspaceLease clears a lease if the owner and nonce match.
func (s *Store) ReleaseWorkspaceLease(ctx context.Context, id, owner, nonce string) (bool, error) {
	if s == nil || s.DB == nil {
		return false, errors.New("db store is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false, errors.New("workspace id is required")
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return false, errors.New("lease owner is required")
	}
	nonce = strings.TrimSpace(nonce)
	if nonce == "" {
		return false, errors.New("lease nonce is required")
	}
	updatedAt := formatTime(time.Now().UTC())
	res, err := s.DB.ExecContext(ctx, `UPDATE workspaces
		SET lease_owner = NULL, lease_nonce = NULL, lease_expires_at = NULL, updated_at = ?
		WHERE id = ? AND lease_owner = ? AND lease_nonce = ?`, updatedAt, id, owner, nonce)
	if err != nil {
		return false, fmt.Errorf("release workspace lease %s: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected release workspace lease %s: %w", id, err)
	}
	return affected > 0, nil
}
