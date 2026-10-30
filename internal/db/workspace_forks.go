package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/agentlab/agentlab/internal/protocol"
)

func (s *Store) CreateWorkspaceFork(ctx context.Context, operation protocol.WorkspaceForkOperation) error {
	if operation.ServerIdentity == "" || operation.ID == "" || operation.Owner == "" || operation.SourceWorkspaceID == "" || operation.TargetWorkspaceID == "" || operation.TargetName == "" || operation.TargetVolumeID == "" || operation.CopySnapshot == "" {
		return errors.New("incomplete workspace fork operation")
	}
	operation.State = "planned"
	operation.CreatedAt = time.Now().UTC()
	operation.UpdatedAt = operation.CreatedAt
	data, err := json.Marshal(operation)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO workspace_fork_attempts(id,source_id,target_id,target_name,state,record_json) VALUES(?,?,?,?,?,?)`, operation.ID, operation.SourceWorkspaceID, operation.TargetWorkspaceID, operation.TargetName, operation.State, string(data))
	return err
}
func (s *Store) WorkspaceFork(ctx context.Context, sourceID, id string) (protocol.WorkspaceForkOperation, error) {
	var raw string
	var operation protocol.WorkspaceForkOperation
	err := s.DB.QueryRowContext(ctx, `SELECT record_json FROM workspace_fork_attempts WHERE id=? AND source_id=?`, id, sourceID).Scan(&raw)
	if err != nil {
		return operation, err
	}
	if err := json.Unmarshal([]byte(raw), &operation); err != nil {
		return operation, err
	}
	if operation.ID != id || operation.SourceWorkspaceID != sourceID {
		return operation, errors.New("workspace fork identity mismatch")
	}
	return operation, nil
}
func (s *Store) UpdateWorkspaceFork(ctx context.Context, operation protocol.WorkspaceForkOperation) error {
	var previous string
	if err := s.DB.QueryRowContext(ctx, `SELECT record_json FROM workspace_fork_attempts WHERE id=? AND source_id=?`, operation.ID, operation.SourceWorkspaceID).Scan(&previous); err != nil {
		return err
	}
	var original protocol.WorkspaceForkOperation
	if err := json.Unmarshal([]byte(previous), &original); err != nil {
		return err
	}
	if original.SizeGB != operation.SizeGB || original.ServerIdentity != operation.ServerIdentity || original.Owner != operation.Owner || original.SourceVolumeID != operation.SourceVolumeID || original.FromSnapshot != operation.FromSnapshot || original.CopySnapshot != operation.CopySnapshot || original.TargetWorkspaceID != operation.TargetWorkspaceID || original.TargetName != operation.TargetName || original.TargetVolumeID != operation.TargetVolumeID {
		return errors.New("workspace fork resource binding changed")
	}
	if previous := original.Receipt; previous != nil {
		if operation.Receipt == nil {
			return errors.New("workspace fork storage receipt was removed")
		}
		for _, pair := range [][2]string{{previous.SourceGUID, operation.Receipt.SourceGUID}, {previous.SnapshotGUID, operation.Receipt.SnapshotGUID}, {previous.TargetGUID, operation.Receipt.TargetGUID}, {previous.JournalID, operation.Receipt.JournalID}} {
			if pair[0] != "" && pair[0] != pair[1] {
				return errors.New("workspace fork storage identity changed")
			}
		}
	}
	operation.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(operation)
	if err != nil {
		return err
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE workspace_fork_attempts SET state=?,record_json=? WHERE id=? AND source_id=? AND target_id=? AND target_name=? AND record_json=?`, operation.State, string(data), operation.ID, operation.SourceWorkspaceID, operation.TargetWorkspaceID, operation.TargetName, previous)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("workspace fork binding changed")
	}
	return nil
}

func (s *Store) ListWorkspaceForks(ctx context.Context, sourceID string) ([]protocol.WorkspaceForkOperation, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT record_json FROM workspace_fork_attempts WHERE source_id=? ORDER BY id`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []protocol.WorkspaceForkOperation{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var operation protocol.WorkspaceForkOperation
		if err := json.Unmarshal([]byte(raw), &operation); err != nil {
			return nil, err
		}
		if operation.SourceWorkspaceID != sourceID {
			return nil, errors.New("workspace fork source changed")
		}
		result = append(result, operation)
	}
	return result, rows.Err()
}

func (s *Store) PendingWorkspaceForkByName(ctx context.Context, sourceID, name string) (protocol.WorkspaceForkOperation, error) {
	var id string
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM workspace_fork_attempts WHERE source_id=? AND target_name=? AND state!='succeeded'`, sourceID, name).Scan(&id); err != nil {
		return protocol.WorkspaceForkOperation{}, err
	}
	return s.WorkspaceFork(ctx, sourceID, id)
}

func (s *Store) WorkspaceForkByName(ctx context.Context, sourceID, name string) (protocol.WorkspaceForkOperation, error) {
	var id string
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM workspace_fork_attempts WHERE source_id=? AND target_name=? ORDER BY rowid DESC LIMIT 1`, sourceID, name).Scan(&id); err != nil {
		return protocol.WorkspaceForkOperation{}, err
	}
	return s.WorkspaceFork(ctx, sourceID, id)
}
