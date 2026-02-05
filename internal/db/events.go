// ABOUTME: Event database operations for audit logging and debugging.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Event represents a single audit log event.
//
// Events are recorded for significant state changes and operations
// including sandbox lifecycle events, job status changes, and errors.
// They provide an audit trail for debugging and observability.
type Event struct {
	ID          int64
	Timestamp   time.Time
	Kind        string
	SandboxVMID *int
	JobID       *string
	Message     string
	JSON        string
}

// ListEventsBySandbox returns events for a sandbox after a given ID.
//
// Queries events associated with a specific sandbox VMID, starting from
// afterID (exclusive) and returning up to limit events in ascending
// order by event ID. Useful for streaming incremental event updates.
func (s *Store) ListEventsBySandbox(ctx context.Context, vmid int, afterID int64, limit int) ([]Event, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	if vmid <= 0 {
		return nil, errors.New("vmid must be positive")
	}
	if limit <= 0 {
		return nil, errors.New("limit must be positive")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, ts, kind, sandbox_vmid, job_id, msg, json
		FROM events WHERE sandbox_vmid = ? AND id > ? ORDER BY id ASC LIMIT ?`, vmid, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		ev, err := scanEventRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return out, nil
}

// ListEventsByJob returns events for a job after a given ID.
//
// Queries events associated with a specific job ID, starting from
// afterID (exclusive) and returning up to limit events in ascending
// order by event ID. Useful for streaming incremental event updates.
func (s *Store) ListEventsByJob(ctx context.Context, jobID string, afterID int64, limit int) ([]Event, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, errors.New("job id is required")
	}
	if limit <= 0 {
		return nil, errors.New("limit must be positive")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, ts, kind, sandbox_vmid, job_id, msg, json
		FROM events WHERE job_id = ? AND id > ? ORDER BY id ASC LIMIT ?`, jobID, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		ev, err := scanEventRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return out, nil
}

// ListEventsBySandboxTail returns the most recent events for a sandbox.
//
// Returns up to limit most recent events for a sandbox in descending order,
// then reverses the result to return events in chronological order.
// Useful for displaying recent activity when streaming from the beginning.
func (s *Store) ListEventsBySandboxTail(ctx context.Context, vmid int, limit int) ([]Event, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	if vmid <= 0 {
		return nil, errors.New("vmid must be positive")
	}
	if limit <= 0 {
		return nil, errors.New("limit must be positive")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, ts, kind, sandbox_vmid, job_id, msg, json
		FROM events WHERE sandbox_vmid = ? ORDER BY id DESC LIMIT ?`, vmid, limit)
	if err != nil {
		return nil, fmt.Errorf("list events tail: %w", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		ev, err := scanEventRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events tail: %w", err)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// ListEventsByJobTail returns the most recent events for a job.
//
// Returns up to limit most recent events for a job in descending order,
// then reverses the result to return events in chronological order.
// Useful for displaying recent activity when streaming from the beginning.
func (s *Store) ListEventsByJobTail(ctx context.Context, jobID string, limit int) ([]Event, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, errors.New("job id is required")
	}
	if limit <= 0 {
		return nil, errors.New("limit must be positive")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, ts, kind, sandbox_vmid, job_id, msg, json
		FROM events WHERE job_id = ? ORDER BY id DESC LIMIT ?`, jobID, limit)
	if err != nil {
		return nil, fmt.Errorf("list events tail: %w", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		ev, err := scanEventRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events tail: %w", err)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func scanEventRow(scanner interface{ Scan(dest ...any) error }) (Event, error) {
	var ev Event
	var ts string
	var kind string
	var sandboxVMID sql.NullInt64
	var jobID sql.NullString
	var msg sql.NullString
	var jsonPayload sql.NullString
	if err := scanner.Scan(&ev.ID, &ts, &kind, &sandboxVMID, &jobID, &msg, &jsonPayload); err != nil {
		return Event{}, err
	}
	if ts != "" {
		parsed, err := parseTime(ts)
		if err != nil {
			return Event{}, fmt.Errorf("parse event ts: %w", err)
		}
		ev.Timestamp = parsed
	}
	ev.Kind = kind
	if sandboxVMID.Valid {
		value := int(sandboxVMID.Int64)
		ev.SandboxVMID = &value
	}
	if jobID.Valid {
		value := jobID.String
		ev.JobID = &value
	}
	if msg.Valid {
		ev.Message = msg.String
	}
	if jsonPayload.Valid {
		ev.JSON = jsonPayload.String
	}
	return ev, nil
}
