package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/agentlab/agentlab/internal/protocol"
)

func (s *Store) GetJobCancellation(ctx context.Context, id string) (protocol.JobCancellation, error) {
	var record protocol.JobCancellation
	var data string
	err := s.DB.QueryRowContext(ctx, `SELECT record_json FROM job_cancellations WHERE job_id=?`, id).Scan(&data)
	if err != nil {
		return record, err
	}
	err = json.Unmarshal([]byte(data), &record)
	return record, err
}

// RequestJobCancellation persists authority and the target before starting work.
// Completed requests remain immutable. Blocked requests can receive new authority.
func (s *Store) RequestJobCancellation(ctx context.Context, record protocol.JobCancellation) (protocol.JobCancellation, error) {
	target, err := s.GetJob(ctx, record.JobID)
	if err != nil {
		return record, err
	}
	if record.SandboxVMID == nil {
		record.SandboxVMID = target.SandboxVMID
	}
	if record.WorkspaceID == nil {
		record.WorkspaceID = target.WorkspaceID
	}
	previous, previousErr := s.GetJobCancellation(ctx, record.JobID)
	if previousErr != nil && !errors.Is(previousErr, sql.ErrNoRows) {
		return record, previousErr
	}
	if previousErr == nil && previous.State == "blocked" {
		changedVM := previous.SandboxVMID != nil && (record.SandboxVMID == nil || *previous.SandboxVMID != *record.SandboxVMID)
		changedWorkspace := previous.WorkspaceID != nil && (record.WorkspaceID == nil || *previous.WorkspaceID != *record.WorkspaceID)
		if changedVM || changedWorkspace {
			return previous, nil
		}
		record.Resources = previous.Resources
		record.RequestedAt = previous.RequestedAt
		record.Actors = append([]string{}, previous.Actors...)
	}
	if record.Actor != "" {
		known := false
		for _, actor := range record.Actors {
			if actor == record.Actor {
				known = true
			}
		}
		if !known {
			record.Actors = append(record.Actors, record.Actor)
		}
	}
	record.State = "requested"
	record.WorkStopped = false
	record.CleanupStatus = "pending"
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if record.RequestedAt == "" {
		record.RequestedAt = now
	}
	record.UpdatedAt = now
	if record.Resources == nil {
		record.Resources = []protocol.JobResource{}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return record, err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO job_cancellations(job_id,state,record_json) SELECT ?,?,? FROM jobs WHERE id=? AND (status IN ('QUEUED','RUNNING') OR EXISTS(SELECT 1 FROM job_cancellations WHERE job_id=jobs.id)) ON CONFLICT(job_id) DO UPDATE SET state=excluded.state,record_json=excluded.record_json WHERE job_cancellations.state='blocked'`, record.JobID, record.State, string(data), record.JobID)
	if err != nil {
		return record, err
	}
	saved, err := s.GetJobCancellation(ctx, record.JobID)
	if errors.Is(err, sql.ErrNoRows) {
		job, loadErr := s.GetJob(ctx, record.JobID)
		if loadErr != nil {
			return record, loadErr
		}
		if job.SandboxVMID != nil {
			record.Resources = append(record.Resources, protocol.JobResource{Kind: "sandbox", ID: strconv.Itoa(*job.SandboxVMID), State: "unchanged"})
		}
		if job.WorkspaceID != nil {
			record.Resources = append(record.Resources, protocol.JobResource{Kind: "workspace", ID: *job.WorkspaceID, State: "retained"})
		}
		if job.Status == "COMPLETED" {
			record.State = "not_needed"
			record.WorkStopped = true
			record.CleanupStatus = "not_required"
			return record, nil
		}
		record.State = "blocked"
		record.ErrorCode = "job_already_terminal"
		record.WorkStopped = false
		record.CleanupStatus = "inspection_required"
		record.Action = "the job is already terminal; inspect retained resources before issuing a resource-specific action"
		return record, nil
	}
	return saved, err
}

func (s *Store) SaveJobCancellation(ctx context.Context, record protocol.JobCancellation) error {
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE job_cancellations SET state=?,record_json=? WHERE job_id=?`, record.State, string(data), record.JobID)
	return err
}

func (s *Store) JobCancellationRequested(ctx context.Context, id string) (bool, error) {
	var state string
	err := s.DB.QueryRowContext(ctx, `SELECT state FROM job_cancellations WHERE job_id=?`, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return state != "not_needed", nil
}

// CompleteJobCancellation atomically protects the final result from late reports.
// Job execution state remains in jobs. Cancellation metadata describes cleanup.
func (s *Store) CompleteJobCancellation(ctx context.Context, record protocol.JobCancellation) error {
	record.State = "cancelled"
	record.WorkStopped = true
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var original sql.NullString
	var actual sql.NullInt64
	var actualWorkspace sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT result_json,sandbox_vmid,workspace_id FROM jobs WHERE id=?`, record.JobID).Scan(&original, &actual, &actualWorkspace); err != nil {
		return err
	}
	if (record.SandboxVMID == nil && actual.Valid) || (record.SandboxVMID != nil && (!actual.Valid || *record.SandboxVMID != int(actual.Int64))) {
		return fmt.Errorf("job sandbox binding changed before cancellation commit")
	}
	if record.WorkspaceID != nil && (!actualWorkspace.Valid || *record.WorkspaceID != actualWorkspace.String) {
		return fmt.Errorf("job workspace binding changed before cancellation commit")
	}
	result := map[string]any{}
	if original.Valid {
		_ = json.Unmarshal([]byte(original.String), &result)
	}
	if result == nil {
		result = map[string]any{}
	}
	result["status"] = "FAILED"
	result["cancelled"] = true
	result["message"] = "job cancelled"
	result["cancellation"] = record
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE job_cancellations SET state='finalizing' WHERE job_id=?`, record.JobID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET status='FAILED',result_json=?,updated_at=? WHERE id=?`, string(payload), formatTime(time.Now().UTC()), record.JobID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE job_cancellations SET state='cancelled',record_json=? WHERE job_id=?`, string(data), record.JobID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) PendingJobCancellations(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT job_id FROM job_cancellations WHERE state IN ('requested','running') ORDER BY job_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
