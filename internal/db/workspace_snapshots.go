// ABOUTME: Workspace snapshot database operations for auditability.
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

// CreateWorkspaceSnapshot inserts a new workspace snapshot row.
func (s *Store) CreateWorkspaceSnapshot(ctx context.Context, snapshot models.WorkspaceSnapshot) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	snapshot.WorkspaceID = strings.TrimSpace(snapshot.WorkspaceID)
	if snapshot.WorkspaceID == "" {
		return errors.New("workspace id is required")
	}
	snapshot.Name = strings.TrimSpace(snapshot.Name)
	if snapshot.Name == "" {
		return errors.New("snapshot name is required")
	}
	snapshot.BackendRef = strings.TrimSpace(snapshot.BackendRef)
	if snapshot.BackendRef == "" {
		return errors.New("snapshot backend_ref is required")
	}
	now := time.Now().UTC()
	createdAt := snapshot.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO workspace_snapshots (
		workspace_id, name, backend_ref, created_at, meta_json
	) VALUES (?, ?, ?, ?, ?)`,
		snapshot.WorkspaceID,
		snapshot.Name,
		snapshot.BackendRef,
		formatTime(createdAt),
		nullIfEmpty(strings.TrimSpace(snapshot.MetaJSON)),
	)
	if err != nil {
		return fmt.Errorf("insert workspace snapshot %s/%s: %w", snapshot.WorkspaceID, snapshot.Name, err)
	}
	return nil
}

// GetWorkspaceSnapshot loads a workspace snapshot by workspace_id and name.
func (s *Store) GetWorkspaceSnapshot(ctx context.Context, workspaceID, name string) (models.WorkspaceSnapshot, error) {
	if s == nil || s.DB == nil {
		return models.WorkspaceSnapshot{}, errors.New("db store is nil")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return models.WorkspaceSnapshot{}, errors.New("workspace id is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return models.WorkspaceSnapshot{}, errors.New("snapshot name is required")
	}
	row := s.DB.QueryRowContext(ctx, `SELECT workspace_id, name, backend_ref, created_at, meta_json
		FROM workspace_snapshots WHERE workspace_id = ? AND name = ?`, workspaceID, name)
	return scanWorkspaceSnapshotRow(row)
}

// ListWorkspaceSnapshots returns all snapshots for a workspace ordered by created_at descending.
func (s *Store) ListWorkspaceSnapshots(ctx context.Context, workspaceID string) ([]models.WorkspaceSnapshot, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, errors.New("workspace id is required")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT workspace_id, name, backend_ref, created_at, meta_json
		FROM workspace_snapshots WHERE workspace_id = ? ORDER BY created_at DESC`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list workspace snapshots %s: %w", workspaceID, err)
	}
	defer rows.Close()
	var out []models.WorkspaceSnapshot
	for rows.Next() {
		snapshot, err := scanWorkspaceSnapshotRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workspace snapshots %s: %w", workspaceID, err)
	}
	return out, nil
}

func scanWorkspaceSnapshotRow(scanner interface{ Scan(dest ...any) error }) (models.WorkspaceSnapshot, error) {
	var snapshot models.WorkspaceSnapshot
	var createdAt string
	var meta sql.NullString
	if err := scanner.Scan(&snapshot.WorkspaceID, &snapshot.Name, &snapshot.BackendRef, &createdAt, &meta); err != nil {
		return models.WorkspaceSnapshot{}, err
	}
	if createdAt != "" {
		parsed, err := parseTime(createdAt)
		if err != nil {
			return models.WorkspaceSnapshot{}, fmt.Errorf("parse workspace_snapshot created_at: %w", err)
		}
		snapshot.CreatedAt = parsed
	}
	if meta.Valid {
		snapshot.MetaJSON = meta.String
	}
	return snapshot, nil
}
