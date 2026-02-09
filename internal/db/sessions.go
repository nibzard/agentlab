// ABOUTME: Session database operations for persisted workspace sessions.
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

// CreateSession inserts a new session row into the database.
func (s *Store) CreateSession(ctx context.Context, session models.Session) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	session.ID = strings.TrimSpace(session.ID)
	if session.ID == "" {
		return errors.New("session id is required")
	}
	session.Name = strings.TrimSpace(session.Name)
	if session.Name == "" {
		return errors.New("session name is required")
	}
	session.WorkspaceID = strings.TrimSpace(session.WorkspaceID)
	if session.WorkspaceID == "" {
		return errors.New("session workspace_id is required")
	}
	session.Profile = strings.TrimSpace(session.Profile)
	if session.Profile == "" {
		return errors.New("session profile is required")
	}
	session.Branch = strings.TrimSpace(session.Branch)
	session.MetaJSON = strings.TrimSpace(session.MetaJSON)

	now := time.Now().UTC()
	createdAt := session.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := session.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	var current interface{}
	if session.CurrentVMID != nil && *session.CurrentVMID > 0 {
		current = *session.CurrentVMID
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO sessions (
		id, name, workspace_id, current_vmid, profile, branch, created_at, updated_at, meta_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID,
		session.Name,
		session.WorkspaceID,
		current,
		session.Profile,
		nullIfEmpty(session.Branch),
		formatTime(createdAt),
		formatTime(updatedAt),
		nullIfEmpty(session.MetaJSON),
	)
	if err != nil {
		return fmt.Errorf("insert session %s: %w", session.ID, err)
	}
	return nil
}

// GetSession loads a session by ID.
func (s *Store) GetSession(ctx context.Context, id string) (models.Session, error) {
	if s == nil || s.DB == nil {
		return models.Session{}, errors.New("db store is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return models.Session{}, errors.New("session id is required")
	}
	row := s.DB.QueryRowContext(ctx, `SELECT id, name, workspace_id, current_vmid, profile, branch, created_at, updated_at, meta_json
		FROM sessions WHERE id = ?`, id)
	return scanSessionRow(row)
}

// GetSessionByName loads a session by name.
func (s *Store) GetSessionByName(ctx context.Context, name string) (models.Session, error) {
	if s == nil || s.DB == nil {
		return models.Session{}, errors.New("db store is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return models.Session{}, errors.New("session name is required")
	}
	row := s.DB.QueryRowContext(ctx, `SELECT id, name, workspace_id, current_vmid, profile, branch, created_at, updated_at, meta_json
		FROM sessions WHERE name = ?`, name)
	return scanSessionRow(row)
}

// ListSessions returns all sessions ordered by created_at descending.
func (s *Store) ListSessions(ctx context.Context) ([]models.Session, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, workspace_id, current_vmid, profile, branch, created_at, updated_at, meta_json
		FROM sessions ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	var out []models.Session
	for rows.Next() {
		session, err := scanSessionRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	return out, nil
}

// UpdateSessionCurrentVMID updates the current VMID for a session.
func (s *Store) UpdateSessionCurrentVMID(ctx context.Context, id string, vmid *int) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("session id is required")
	}
	var current interface{}
	if vmid != nil && *vmid > 0 {
		current = *vmid
	}
	updatedAt := formatTime(time.Now().UTC())
	_, err := s.DB.ExecContext(ctx, `UPDATE sessions SET current_vmid = ?, updated_at = ? WHERE id = ?`, current, updatedAt, id)
	if err != nil {
		return fmt.Errorf("update session %s current_vmid: %w", id, err)
	}
	return nil
}

// UpdateSessionBranch updates the branch label for a session.
func (s *Store) UpdateSessionBranch(ctx context.Context, id, branch string) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("session id is required")
	}
	branch = strings.TrimSpace(branch)
	updatedAt := formatTime(time.Now().UTC())
	_, err := s.DB.ExecContext(ctx, `UPDATE sessions SET branch = ?, updated_at = ? WHERE id = ?`, nullIfEmpty(branch), updatedAt, id)
	if err != nil {
		return fmt.Errorf("update session %s branch: %w", id, err)
	}
	return nil
}

func scanSessionRow(scanner interface{ Scan(dest ...any) error }) (models.Session, error) {
	var session models.Session
	var current sql.NullInt64
	var branch sql.NullString
	var createdAt string
	var updatedAt string
	var meta sql.NullString
	if err := scanner.Scan(&session.ID, &session.Name, &session.WorkspaceID, &current, &session.Profile, &branch, &createdAt, &updatedAt, &meta); err != nil {
		return models.Session{}, err
	}
	if current.Valid {
		value := int(current.Int64)
		session.CurrentVMID = &value
	}
	if branch.Valid {
		session.Branch = branch.String
	}
	if meta.Valid {
		session.MetaJSON = meta.String
	}
	var err error
	if createdAt != "" {
		session.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return models.Session{}, fmt.Errorf("parse created_at: %w", err)
		}
	}
	if updatedAt != "" {
		session.UpdatedAt, err = parseTime(updatedAt)
		if err != nil {
			return models.Session{}, fmt.Errorf("parse updated_at: %w", err)
		}
	}
	return session, nil
}
