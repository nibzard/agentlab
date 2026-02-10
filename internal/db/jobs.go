// ABOUTME: Job database operations for creating, retrieving, and updating jobs.
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

// CreateJob inserts a new job row into the database.
func (s *Store) CreateJob(ctx context.Context, job models.Job) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	if job.ID == "" {
		return errors.New("job id is required")
	}
	if job.RepoURL == "" {
		return errors.New("job repo_url is required")
	}
	if job.Ref == "" {
		return errors.New("job ref is required")
	}
	if job.Profile == "" {
		return errors.New("job profile is required")
	}
	if job.Status == "" {
		return errors.New("job status is required")
	}
	now := time.Now().UTC()
	createdAt := job.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := job.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	var sandbox interface{}
	if job.SandboxVMID != nil && *job.SandboxVMID > 0 {
		sandbox = *job.SandboxVMID
	}
	var ttl interface{}
	if job.TTLMinutes > 0 {
		ttl = job.TTLMinutes
	}
	var workspace interface{}
	if job.WorkspaceID != nil && strings.TrimSpace(*job.WorkspaceID) != "" {
		workspace = strings.TrimSpace(*job.WorkspaceID)
	}
	var session interface{}
	if job.SessionID != nil && strings.TrimSpace(*job.SessionID) != "" {
		session = strings.TrimSpace(*job.SessionID)
	}
	var result interface{}
	if job.ResultJSON != "" {
		result = job.ResultJSON
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO jobs (
		id, repo_url, ref, profile, status, sandbox_vmid, task, mode, ttl_minutes, keepalive, workspace_id, session_id, created_at, updated_at, result_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID,
		job.RepoURL,
		job.Ref,
		job.Profile,
		job.Status,
		sandbox,
		nullIfEmpty(job.Task),
		nullIfEmpty(job.Mode),
		ttl,
		job.Keepalive,
		workspace,
		session,
		formatTime(createdAt),
		formatTime(updatedAt),
		result,
	)
	if err != nil {
		return fmt.Errorf("insert job %s: %w", job.ID, err)
	}
	return nil
}

// GetJob loads a job by id.
func (s *Store) GetJob(ctx context.Context, id string) (models.Job, error) {
	if s == nil || s.DB == nil {
		return models.Job{}, errors.New("db store is nil")
	}
	row := s.DB.QueryRowContext(ctx, `SELECT id, repo_url, ref, profile, task, mode, ttl_minutes, keepalive, status, sandbox_vmid, workspace_id, session_id, created_at, updated_at, result_json
		FROM jobs WHERE id = ?`, id)
	return scanJobRow(row)
}

// GetJobBySandboxVMID loads the most recent job attached to a sandbox VMID.
func (s *Store) GetJobBySandboxVMID(ctx context.Context, vmid int) (models.Job, error) {
	if s == nil || s.DB == nil {
		return models.Job{}, errors.New("db store is nil")
	}
	if vmid <= 0 {
		return models.Job{}, errors.New("vmid must be positive")
	}
	row := s.DB.QueryRowContext(ctx, `SELECT id, repo_url, ref, profile, task, mode, ttl_minutes, keepalive, status, sandbox_vmid, workspace_id, session_id, created_at, updated_at, result_json
		FROM jobs WHERE sandbox_vmid = ?
		ORDER BY created_at DESC LIMIT 1`, vmid)
	return scanJobRow(row)
}

// CountJobsByStatus returns a count of jobs grouped by status.
func (s *Store) CountJobsByStatus(ctx context.Context) (map[models.JobStatus]int, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT status, COUNT(*) FROM jobs GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("count jobs: %w", err)
	}
	defer rows.Close()
	out := make(map[models.JobStatus]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan job count: %w", err)
		}
		if status == "" {
			continue
		}
		out[models.JobStatus(status)] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate job counts: %w", err)
	}
	return out, nil
}

// UpdateJobSandbox updates a job with the attached sandbox vmid.
func (s *Store) UpdateJobSandbox(ctx context.Context, id string, vmid int) (bool, error) {
	if s == nil || s.DB == nil {
		return false, errors.New("db store is nil")
	}
	if id == "" {
		return false, errors.New("job id is required")
	}
	if vmid <= 0 {
		return false, errors.New("vmid must be positive")
	}
	updatedAt := formatTime(time.Now().UTC())
	res, err := s.DB.ExecContext(ctx, `UPDATE jobs SET sandbox_vmid = ?, updated_at = ? WHERE id = ?`, vmid, updatedAt, id)
	if err != nil {
		return false, fmt.Errorf("update job %s sandbox: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected job %s: %w", id, err)
	}
	return affected > 0, nil
}

// UpdateJobStatus updates the status of a job.
func (s *Store) UpdateJobStatus(ctx context.Context, id string, status models.JobStatus) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	if id == "" {
		return errors.New("job id is required")
	}
	if status == "" {
		return errors.New("job status is required")
	}
	updatedAt := formatTime(time.Now().UTC())
	res, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status = ?, updated_at = ? WHERE id = ?`, status, updatedAt, id)
	if err != nil {
		return fmt.Errorf("update job %s status: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected job %s: %w", id, err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpdateJobResult updates status and result_json for a job.
func (s *Store) UpdateJobResult(ctx context.Context, id string, status models.JobStatus, resultJSON string) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	if id == "" {
		return errors.New("job id is required")
	}
	if status == "" {
		return errors.New("job status is required")
	}
	var result interface{}
	if resultJSON != "" {
		result = resultJSON
	}
	updatedAt := formatTime(time.Now().UTC())
	res, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status = ?, result_json = ?, updated_at = ? WHERE id = ?`, status, result, updatedAt, id)
	if err != nil {
		return fmt.Errorf("update job %s result: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected job %s: %w", id, err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func scanJobRow(scanner interface{ Scan(dest ...any) error }) (models.Job, error) {
	var job models.Job
	var task sql.NullString
	var mode sql.NullString
	var ttl sql.NullInt64
	var keepalive sql.NullBool
	var status string
	var sandbox sql.NullInt64
	var workspace sql.NullString
	var session sql.NullString
	var createdAt string
	var updatedAt string
	var result sql.NullString
	if err := scanner.Scan(
		&job.ID,
		&job.RepoURL,
		&job.Ref,
		&job.Profile,
		&task,
		&mode,
		&ttl,
		&keepalive,
		&status,
		&sandbox,
		&workspace,
		&session,
		&createdAt,
		&updatedAt,
		&result,
	); err != nil {
		return models.Job{}, err
	}
	if task.Valid {
		job.Task = task.String
	}
	if mode.Valid {
		job.Mode = mode.String
	}
	if ttl.Valid {
		job.TTLMinutes = int(ttl.Int64)
	}
	if keepalive.Valid {
		job.Keepalive = keepalive.Bool
	}
	if status == "" {
		return models.Job{}, errors.New("job status missing")
	}
	job.Status = models.JobStatus(status)
	if sandbox.Valid {
		value := int(sandbox.Int64)
		job.SandboxVMID = &value
	}
	if workspace.Valid {
		value := workspace.String
		job.WorkspaceID = &value
	}
	if session.Valid {
		value := session.String
		job.SessionID = &value
	}
	var err error
	if createdAt != "" {
		job.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return models.Job{}, fmt.Errorf("parse created_at: %w", err)
		}
	}
	if updatedAt != "" {
		job.UpdatedAt, err = parseTime(updatedAt)
		if err != nil {
			return models.Job{}, fmt.Errorf("parse updated_at: %w", err)
		}
	}
	if result.Valid {
		job.ResultJSON = result.String
	}
	return job, nil
}
