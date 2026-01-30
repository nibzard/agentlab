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

// CreateWorkspace inserts a new workspace row.
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
	var attached interface{}
	if workspace.AttachedVM != nil && *workspace.AttachedVM > 0 {
		attached = *workspace.AttachedVM
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO workspaces (
		id, name, storage, volid, size_gb, attached_vmid, created_at, updated_at, meta_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		workspace.ID,
		workspace.Name,
		workspace.Storage,
		workspace.VolumeID,
		workspace.SizeGB,
		attached,
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
	row := s.DB.QueryRowContext(ctx, `SELECT id, name, storage, volid, size_gb, attached_vmid, created_at, updated_at
		FROM workspaces WHERE id = ?`, id)
	return scanWorkspaceRow(row)
}

// GetWorkspaceByName loads a workspace by name.
func (s *Store) GetWorkspaceByName(ctx context.Context, name string) (models.Workspace, error) {
	if s == nil || s.DB == nil {
		return models.Workspace{}, errors.New("db store is nil")
	}
	row := s.DB.QueryRowContext(ctx, `SELECT id, name, storage, volid, size_gb, attached_vmid, created_at, updated_at
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
	row := s.DB.QueryRowContext(ctx, `SELECT id, name, storage, volid, size_gb, attached_vmid, created_at, updated_at
		FROM workspaces WHERE attached_vmid = ?`, vmid)
	return scanWorkspaceRow(row)
}

// ListWorkspaces returns all workspaces ordered by created_at descending.
func (s *Store) ListWorkspaces(ctx context.Context) ([]models.Workspace, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, storage, volid, size_gb, attached_vmid, created_at, updated_at
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
	var createdAt string
	var updatedAt string
	if err := scanner.Scan(&ws.ID, &ws.Name, &ws.Storage, &ws.VolumeID, &ws.SizeGB, &attached, &createdAt, &updatedAt); err != nil {
		return models.Workspace{}, err
	}
	if attached.Valid {
		value := int(attached.Int64)
		ws.AttachedVM = &value
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
