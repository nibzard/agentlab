package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/protocol"
)

var ErrWorkspaceAllocationConflict = errors.New("workspace allocation belongs to another request")
var ErrWorkspaceAllocationUncertain = errors.New("workspace allocation requires inspection")

type JobWorkspaceAllocation = protocol.WorkspaceAllocation

func (s *Store) JobWorkspaceAllocation(ctx context.Context, jobID string) (JobWorkspaceAllocation, error) {
	var a JobWorkspaceAllocation
	var created string
	err := s.DB.QueryRowContext(ctx, `SELECT a.job_id,a.workspace_id,a.name,a.storage,a.size_gb,a.state,a.volume_id,a.created_at,COALESCE(o.vmid,0),COALESCE(o.allocation_name,'') FROM job_workspace_allocations a LEFT JOIN workspace_volume_owners o ON o.workspace_id=a.workspace_id WHERE a.job_id=?`, jobID).Scan(&a.JobID, &a.WorkspaceID, &a.Name, &a.Storage, &a.SizeGB, &a.State, &a.VolumeID, &created, &a.OwnerVMID, &a.AllocationName)
	if err != nil {
		return a, err
	}
	a.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	return a, err
}

// ReserveJobWorkspaceAllocation stores the resource identity before any backend
// call. Callers serialize the request and workspace name across processes.
func (s *Store) ReserveJobWorkspaceAllocation(ctx context.Context, a JobWorkspaceAllocation) (JobWorkspaceAllocation, error) {
	if a.JobID == "" || a.WorkspaceID == "" || a.Name == "" || a.Storage == "" || a.SizeGB <= 0 {
		return a, errors.New("incomplete workspace allocation")
	}
	if existing, err := s.JobWorkspaceAllocation(ctx, a.JobID); err == nil {
		if existing.Name != a.Name || existing.Storage != a.Storage || existing.SizeGB != a.SizeGB {
			return existing, ErrWorkspaceAllocationConflict
		}
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return a, err
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces WHERE name=? OR id=?`, a.Name, a.WorkspaceID).Scan(&count); err != nil {
		return a, err
	}
	if count != 0 {
		return a, ErrWorkspaceAllocationConflict
	}
	a.CreatedAt = time.Now().UTC()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO job_workspace_allocations(job_id,workspace_id,name,storage,size_gb,state,created_at) VALUES(?,?,?,?,?,'planned',?)`, a.JobID, a.WorkspaceID, a.Name, a.Storage, a.SizeGB, formatTime(a.CreatedAt))
	if err != nil {
		return a, err
	}
	a.State = "planned"
	return a, nil
}

func (s *Store) WorkspaceAllocationPending(ctx context.Context, name string) (bool, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM job_workspace_allocations WHERE name=? AND state!='committed')+(SELECT COUNT(*) FROM workspace_fork_attempts WHERE target_name=? AND state!='succeeded')`, name, name).Scan(&count)
	return count != 0, err
}

// BeginJobWorkspaceAllocation is a one-way boundary. An interrupted call must
// never allocate again until external resource ownership has been reconciled.
func (s *Store) BeginJobWorkspaceAllocation(ctx context.Context, jobID string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE job_workspace_allocations SET state='allocating' WHERE job_id=? AND state='planned'`, jobID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrWorkspaceAllocationUncertain
	}
	return nil
}

func (s *Store) RecordJobWorkspaceVolume(ctx context.Context, jobID, volumeID string, complete bool) error {
	if volumeID == "" {
		return errors.New("workspace volume is required")
	}
	state := "allocating"
	if complete {
		state = "allocated"
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE job_workspace_allocations SET state=?,volume_id=? WHERE job_id=? AND state='allocating' AND volume_id=''`, state, volumeID, jobID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrWorkspaceAllocationUncertain
	}
	return nil
}

// CommitJobWorkspaceAllocation publishes the workspace and submission binding
// in one transaction. A failure preserves the known volume for a later retry.
func (s *Store) CommitJobWorkspaceAllocation(ctx context.Context, jobID string) (models.Workspace, error) {
	a, err := s.JobWorkspaceAllocation(ctx, jobID)
	if err != nil {
		return models.Workspace{}, err
	}
	if a.State == "committed" {
		return s.GetWorkspace(ctx, a.WorkspaceID)
	}
	if a.State != "allocated" || a.VolumeID == "" {
		return models.Workspace{}, ErrWorkspaceAllocationUncertain
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return models.Workspace{}, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces WHERE volid=?`, a.VolumeID).Scan(&count); err != nil {
		return models.Workspace{}, err
	}
	if count != 0 {
		return models.Workspace{}, ErrWorkspaceAllocationConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workspaces(id,name,storage,volid,size_gb,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, a.WorkspaceID, a.Name, a.Storage, a.VolumeID, a.SizeGB, formatTime(a.CreatedAt), formatTime(a.CreatedAt))
	if err != nil {
		return models.Workspace{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO job_submission_workspaces(job_id,workspace_id) VALUES(?,?)`, jobID, a.WorkspaceID)
	if err != nil {
		return models.Workspace{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE job_workspace_allocations SET state='committed' WHERE job_id=? AND state='allocated' AND volume_id=?`, jobID, a.VolumeID)
	if err != nil {
		return models.Workspace{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return models.Workspace{}, err
	}
	if n != 1 {
		return models.Workspace{}, ErrWorkspaceAllocationUncertain
	}
	if err := tx.Commit(); err != nil {
		return models.Workspace{}, err
	}
	return s.GetWorkspace(ctx, a.WorkspaceID)
}

// ResetJobWorkspacePreflight is permitted only when the backend proves it did
// not issue an allocation call. It never clears a partial or complete receipt.
func (s *Store) ResetJobWorkspacePreflight(ctx context.Context, jobID string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE job_workspace_allocations SET state='planned' WHERE job_id=? AND state='allocating' AND volume_id=''`, jobID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrWorkspaceAllocationUncertain
	}
	return nil
}
