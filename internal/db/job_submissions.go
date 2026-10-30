package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

var ErrSubmissionIDCollision = errors.New("job ID already exists")
var ErrSubmissionConflict = errors.New("request key already belongs to different inputs")
var ErrSubmissionExpired = errors.New("request key expired; inspect the original job before submitting new work")

type JobSubmission struct {
	Committed bool
	Owner     string
	KeyHash   string
	InputHash string
	JobID     string
	ExpiresAt time.Time
}

// ReserveJobSubmission binds a key to one job ID before any lease mutation.
// Records remain after expiry. A key never silently creates another job.
func (s *Store) ReserveJobSubmission(ctx context.Context, request JobSubmission, now time.Time) (JobSubmission, error) {
	if s == nil || s.DB == nil {
		return JobSubmission{}, errors.New("db store is nil")
	}
	if request.Owner == "" || request.KeyHash == "" || request.InputHash == "" || request.JobID == "" || request.ExpiresAt.IsZero() {
		return JobSubmission{}, errors.New("incomplete job submission identity")
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO job_submissions(owner,key_hash,input_hash,job_id,expires_at) SELECT ?,?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM jobs WHERE id=?) AND NOT EXISTS(SELECT 1 FROM job_submissions WHERE job_id=?) ON CONFLICT(owner,key_hash) DO NOTHING`, request.Owner, request.KeyHash, request.InputHash, request.JobID, formatTime(request.ExpiresAt), request.JobID, request.JobID)
	if err != nil {
		return JobSubmission{}, err
	}
	var existing JobSubmission
	var expiry string
	err = s.DB.QueryRowContext(ctx, `SELECT owner,key_hash,input_hash,job_id,expires_at,committed FROM job_submissions WHERE owner=? AND key_hash=?`, request.Owner, request.KeyHash).Scan(&existing.Owner, &existing.KeyHash, &existing.InputHash, &existing.JobID, &expiry, &existing.Committed)
	if errors.Is(err, sql.ErrNoRows) {
		return existing, ErrSubmissionIDCollision
	}
	if err != nil {
		return existing, err
	}
	existing.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiry)
	if err != nil {
		return existing, err
	}
	if existing.InputHash != request.InputHash {
		return existing, ErrSubmissionConflict
	}
	if !now.Before(existing.ExpiresAt) {
		return existing, ErrSubmissionExpired
	}
	return existing, nil
}

// BindSubmissionWorkspace keeps a resolved workspace stable across retries.
// It records the binding before a lease is acquired.
func (s *Store) BindSubmissionWorkspace(ctx context.Context, request JobSubmission, workspaceID string) error {
	// A separate binding avoids changing the input hash when an alias resolves.
	_, err := s.DB.ExecContext(ctx, `INSERT INTO job_submission_workspaces(job_id,workspace_id) VALUES(?,?) ON CONFLICT(job_id) DO NOTHING`, request.JobID, workspaceID)
	if err != nil {
		return err
	}
	var bound string
	if err := s.DB.QueryRowContext(ctx, `SELECT workspace_id FROM job_submission_workspaces WHERE job_id=?`, request.JobID).Scan(&bound); err != nil {
		return err
	}
	if bound != workspaceID {
		return ErrSubmissionConflict
	}
	return nil
}

var submissionMemoryLocks sync.Map

// LockJobSubmission serializes mutation for a request across daemon processes.
// File locks release on process termination. SQL records remain authoritative.
func (s *Store) LockJobSubmission(ctx context.Context, owner, key string) (func(), error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	digest := sha256.Sum256([]byte(owner + "\x00" + key))
	name := hex.EncodeToString(digest[:])
	if s.Path == "" || s.Path == ":memory:" {
		value, _ := submissionMemoryLocks.LoadOrStore(fmt.Sprintf("%p:%s", s, name), make(chan struct{}, 1))
		gate := value.(chan struct{})
		select {
		case gate <- struct{}{}:
			return func() { <-gate }, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	canonical, err := filepath.Abs(s.Path)
	if err != nil {
		return nil, err
	}
	canonical, err = filepath.EvalSymlinks(canonical)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(filepath.Dir(canonical), ".job-request-locks")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(root, name), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { syscall.Flock(int(file.Fd()), syscall.LOCK_UN); file.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			file.Close()
			return nil, err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
