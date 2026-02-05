// ABOUTME: Artifact database operations for job output storage and retention.
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

// Artifact stores artifact metadata for files uploaded from sandboxes.
// The actual file contents are stored on disk; this struct tracks
// metadata for retention and retrieval purposes.
type Artifact struct {
	ID        int64
	JobID     string
	VMID      *int
	Name      string
	Path      string
	SizeBytes int64
	Sha256    string
	MIME      string
	CreatedAt time.Time
}

// ArtifactRetentionRecord combines artifact metadata with job and sandbox state
// for garbage collection decisions. This is used by the artifact GC process
// to determine which artifacts can be deleted based on job completion,
// sandbox destruction, and profile-specific retention policies.
type ArtifactRetentionRecord struct {
	Artifact     Artifact
	JobProfile   string
	JobStatus    models.JobStatus
	JobUpdatedAt time.Time
	SandboxVMID  *int
	SandboxState models.SandboxState
}

// CreateArtifact inserts artifact metadata and returns the row id.
func (s *Store) CreateArtifact(ctx context.Context, artifact Artifact) (int64, error) {
	if s == nil || s.DB == nil {
		return 0, errors.New("db store is nil")
	}
	artifact.JobID = strings.TrimSpace(artifact.JobID)
	if artifact.JobID == "" {
		return 0, errors.New("job id is required")
	}
	artifact.Name = strings.TrimSpace(artifact.Name)
	if artifact.Name == "" {
		return 0, errors.New("artifact name is required")
	}
	artifact.Path = strings.TrimSpace(artifact.Path)
	if artifact.Path == "" {
		return 0, errors.New("artifact path is required")
	}
	if artifact.SizeBytes <= 0 {
		return 0, errors.New("artifact size must be positive")
	}
	artifact.Sha256 = strings.TrimSpace(artifact.Sha256)
	if artifact.Sha256 == "" {
		return 0, errors.New("artifact sha256 is required")
	}
	createdAt := artifact.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	var vmid sql.NullInt64
	if artifact.VMID != nil && *artifact.VMID > 0 {
		vmid = sql.NullInt64{Valid: true, Int64: int64(*artifact.VMID)}
	}
	var mime interface{}
	if strings.TrimSpace(artifact.MIME) != "" {
		mime = strings.TrimSpace(artifact.MIME)
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO artifacts (job_id, vmid, name, path, size_bytes, sha256, mime, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		artifact.JobID,
		vmid,
		artifact.Name,
		artifact.Path,
		artifact.SizeBytes,
		artifact.Sha256,
		mime,
		formatTime(createdAt),
	)
	if err != nil {
		return 0, fmt.Errorf("insert artifact for job %s: %w", artifact.JobID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("artifact id: %w", err)
	}
	return id, nil
}

// DeleteArtifact removes an artifact record by id.
func (s *Store) DeleteArtifact(ctx context.Context, id int64) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	if id <= 0 {
		return errors.New("artifact id is required")
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM artifacts WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete artifact %d: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected delete artifact %d: %w", id, err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ListArtifactRetentionCandidates returns artifacts with job metadata for GC.
func (s *Store) ListArtifactRetentionCandidates(ctx context.Context) ([]ArtifactRetentionRecord, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT a.id, a.job_id, a.vmid, a.name, a.path, a.size_bytes, a.sha256, a.mime, a.created_at,
		j.profile, j.status, j.updated_at, j.sandbox_vmid, s.state
		FROM artifacts a
		INNER JOIN jobs j ON a.job_id = j.id
		LEFT JOIN sandboxes s ON j.sandbox_vmid = s.vmid`)
	if err != nil {
		return nil, fmt.Errorf("list artifact retention candidates: %w", err)
	}
	defer rows.Close()

	var out []ArtifactRetentionRecord
	for rows.Next() {
		var artifact Artifact
		var vmid sql.NullInt64
		var mime sql.NullString
		var createdAt string
		var profile string
		var status string
		var jobUpdatedAt string
		var jobSandbox sql.NullInt64
		var sandboxState sql.NullString
		if err := rows.Scan(
			&artifact.ID,
			&artifact.JobID,
			&vmid,
			&artifact.Name,
			&artifact.Path,
			&artifact.SizeBytes,
			&artifact.Sha256,
			&mime,
			&createdAt,
			&profile,
			&status,
			&jobUpdatedAt,
			&jobSandbox,
			&sandboxState,
		); err != nil {
			return nil, err
		}
		if vmid.Valid {
			value := int(vmid.Int64)
			artifact.VMID = &value
		}
		if mime.Valid {
			artifact.MIME = mime.String
		}
		if createdAt != "" {
			parsed, err := parseTime(createdAt)
			if err != nil {
				return nil, fmt.Errorf("parse artifact created_at: %w", err)
			}
			artifact.CreatedAt = parsed
		}
		var updated time.Time
		if jobUpdatedAt != "" {
			parsed, err := parseTime(jobUpdatedAt)
			if err != nil {
				return nil, fmt.Errorf("parse job updated_at: %w", err)
			}
			updated = parsed
		}
		record := ArtifactRetentionRecord{
			Artifact:     artifact,
			JobProfile:   profile,
			JobStatus:    models.JobStatus(status),
			JobUpdatedAt: updated,
		}
		if jobSandbox.Valid {
			value := int(jobSandbox.Int64)
			record.SandboxVMID = &value
		} else if artifact.VMID != nil {
			record.SandboxVMID = artifact.VMID
		}
		if sandboxState.Valid {
			record.SandboxState = models.SandboxState(sandboxState.String)
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifact retention candidates: %w", err)
	}
	return out, nil
}

// ListArtifactsByJob returns artifacts for a job ordered by created_at.
func (s *Store) ListArtifactsByJob(ctx context.Context, jobID string) ([]Artifact, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, errors.New("job id is required")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, job_id, vmid, name, path, size_bytes, sha256, mime, created_at
		FROM artifacts WHERE job_id = ? ORDER BY created_at ASC`, jobID)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		artifact, err := scanArtifactRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifacts: %w", err)
	}
	return out, nil
}

func scanArtifactRow(scanner interface{ Scan(dest ...any) error }) (Artifact, error) {
	var artifact Artifact
	var vmid sql.NullInt64
	var mime sql.NullString
	var createdAt string
	if err := scanner.Scan(
		&artifact.ID,
		&artifact.JobID,
		&vmid,
		&artifact.Name,
		&artifact.Path,
		&artifact.SizeBytes,
		&artifact.Sha256,
		&mime,
		&createdAt,
	); err != nil {
		return Artifact{}, err
	}
	if vmid.Valid {
		value := int(vmid.Int64)
		artifact.VMID = &value
	}
	if mime.Valid {
		artifact.MIME = mime.String
	}
	if createdAt != "" {
		parsed, err := parseTime(createdAt)
		if err != nil {
			return Artifact{}, fmt.Errorf("parse created_at: %w", err)
		}
		artifact.CreatedAt = parsed
	}
	return artifact, nil
}
