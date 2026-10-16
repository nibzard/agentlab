package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	tb "github.com/agentlab/agentlab/internal/templatebuild"
)

var ErrTemplateConflict = errors.New("template_vmid_conflict")
var ErrTemplateIdempotency = errors.New("idempotency key reused with different input")
var ErrTemplateQueueFull = errors.New("template build queue is full")

// SubmitTemplateBuild atomically commits the idempotency key, VMID reservation,
// durable request and audit event. Sandbox inserts consult the same reservations.
func (s *Store) SubmitTemplateBuild(ctx context.Context, b tb.Build, request string, occupied map[int]bool, limits tb.Limits) (tb.Build, bool, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return b, false, e
	}
	defer tx.Rollback()
	var raw, previous string
	e = tx.QueryRowContext(ctx, `SELECT record_json,request_json FROM template_builds WHERE request_id=?`, b.Plan.RequestID).Scan(&raw, &previous)
	if e == nil {
		if previous != request {
			return b, false, ErrTemplateIdempotency
		}
		e = json.Unmarshal([]byte(raw), &b)
		return b, true, e
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return b, false, e
	}
	var pending int
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM template_builds WHERE state='QUEUED'`).Scan(&pending); e != nil {
		return b, false, e
	}
	if pending >= limits.Pending {
		return b, false, ErrTemplateQueueFull
	}
	start, end := b.Plan.VMID, b.Plan.VMID
	if start == 0 {
		start, end = limits.VMIDMin, limits.VMIDMax
	}
	found := false
	for id := start; id <= end; id++ {
		if occupied[id] {
			continue
		}
		var n int
		if e = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM sandboxes WHERE vmid=?) + (SELECT COUNT(*) FROM vmid_reservations WHERE vmid=?)`, id, id).Scan(&n); e != nil {
			return b, false, e
		}
		if n > 0 {
			continue
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO vmid_reservations(vmid,owner) VALUES(?,?)`, id, b.ID); e != nil {
			return b, false, e
		}
		b.VMID = id
		b.Plan.VMID = id
		found = true
		break
	}
	if !found {
		return b, false, ErrTemplateConflict
	}
	// Reserve the smoke clone through the same transaction and sandbox guard.
	for id := limits.VMIDMin; id <= limits.VMIDMax; id++ {
		if occupied[id] || id == b.VMID {
			continue
		}
		var n int
		if e = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM sandboxes WHERE vmid=?) + (SELECT COUNT(*) FROM vmid_reservations WHERE vmid=?)`, id, id).Scan(&n); e != nil {
			return b, false, e
		}
		if n != 0 {
			continue
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO vmid_reservations(vmid,owner) VALUES(?,?)`, id, b.ID); e != nil {
			return b, false, e
		}
		b.SmokeVMID = id
		break
	}
	if b.SmokeVMID == 0 {
		return b, false, fmt.Errorf("%w: two unused VMIDs are required for template and boot verification", ErrTemplateConflict)
	}
	encoded, e := json.Marshal(b)
	if e != nil {
		return b, false, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO template_builds(id,request_id,request_json,vmid,state,record_json) VALUES(?,?,?,?,?,?)`, b.ID, b.Plan.RequestID, request, b.VMID, b.State, string(encoded))
	if e != nil {
		return b, false, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO events(ts,kind,msg,json) VALUES(?,?,?,?)`, formatTime(time.Now().UTC()), "template.build.submitted", b.ID, templateEventJSON("template.build.submitted", map[string]any{"build_id": b.ID, "vmid": b.VMID, "smoke_vmid": b.SmokeVMID}))
	if e != nil {
		return b, false, e
	}
	return b, false, tx.Commit()
}
func (s *Store) TemplateBuildByRequest(ctx context.Context, id, request string) (tb.Build, error) {
	var b tb.Build
	var raw, old string
	e := s.DB.QueryRowContext(ctx, `SELECT record_json,request_json FROM template_builds WHERE request_id=?`, id).Scan(&raw, &old)
	if e != nil {
		return b, e
	}
	if old != request {
		return b, ErrTemplateIdempotency
	}
	e = json.Unmarshal([]byte(raw), &b)
	return b, e
}
func (s *Store) TemplateBuild(ctx context.Context, id string) (tb.Build, error) {
	var b tb.Build
	var raw string
	e := s.DB.QueryRowContext(ctx, `SELECT record_json FROM template_builds WHERE id=?`, id).Scan(&raw)
	if e != nil {
		return b, e
	}
	e = json.Unmarshal([]byte(raw), &b)
	return b, e
}
func (s *Store) TemplateBuilds(ctx context.Context, state string, offset, limit int) ([]tb.Build, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT record_json FROM template_builds WHERE (?='' OR state=?) ORDER BY rowid LIMIT ? OFFSET ?`, state, state, limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []tb.Build{}
	for rows.Next() {
		var raw string
		var b tb.Build
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &b); e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// SaveTemplateBuild compares the previous state; only legal transitions are accepted.
// Callers serialize updates to cancel metadata with stage/result updates.
func (s *Store) SaveTemplateBuild(ctx context.Context, b tb.Build) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var old, previous string
	if e = tx.QueryRowContext(ctx, `SELECT state,record_json FROM template_builds WHERE id=?`, b.ID).Scan(&old, &previous); e != nil {
		return e
	}
	var prior tb.Build
	if e = json.Unmarshal([]byte(previous), &prior); e != nil {
		return e
	}
	// Cancellation and positive launch evidence are monotonic, even if another
	// daemon observed progress from an older snapshot of the same running build.
	b.CancelRequested = b.CancelRequested || prior.CancelRequested
	b.WorkerLaunched = b.WorkerLaunched || prior.WorkerLaunched
	valid := old == b.State && (old == "QUEUED" || old == "RUNNING") || old == "QUEUED" && (b.State == "RUNNING" || b.State == "CANCELLED" || b.State == "FAILED") || old == "RUNNING" && b.Terminal()
	if !valid {
		return fmt.Errorf("invalid template build transition %s -> %s", old, b.State)
	}
	b.UpdatedAt = time.Now().UTC()
	raw, e := json.Marshal(b)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE template_builds SET state=?,record_json=? WHERE id=?`, b.State, string(raw), b.ID); e != nil {
		return e
	}
	if b.State == "SUCCEEDED" {
		if b.Result == nil {
			return errors.New("successful build requires a verified manifest")
		}
		m, e := json.Marshal(b.Result)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO template_manifests(vmid,build_id,record_json) VALUES(?,?,?)`, b.VMID, b.ID, string(m)); e != nil {
			return e
		}
	}
	if b.Terminal() && b.SmokeVMID > 0 && !b.CleanupRequired && len(b.SmokeResources) == 0 {
		if _, e = tx.ExecContext(ctx, `DELETE FROM vmid_reservations WHERE vmid=? AND owner=?`, b.SmokeVMID, b.ID); e != nil {
			return e
		}
	}
	if b.Terminal() {
		payload := map[string]any{"build_id": b.ID, "state": b.State, "stage": b.Stage, "cleanup_required": b.CleanupRequired}
		kind := "template.build.completed"
		if _, e = tx.ExecContext(ctx, `INSERT INTO events(ts,kind,msg,json) VALUES(?,?,?,?)`, formatTime(b.UpdatedAt), kind, b.ID, templateEventJSON(kind, payload)); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) AppendTemplateLog(ctx context.Context, id, stage, message string) error {
	// The final record explicitly marks truncation; subsequent output is dropped.
	_, e := s.DB.ExecContext(ctx, `INSERT INTO template_build_logs(build_id,sequence,timestamp,stage,message)
 SELECT ?,COALESCE(MAX(sequence),0)+1,?,?,CASE WHEN COALESCE(SUM(length(message)),0)+length(?)>52424704 THEN '[log truncated at 50 MiB]' ELSE ? END FROM template_build_logs WHERE build_id=? HAVING COALESCE(SUM(length(message)),0)<52424704`, id, formatTime(time.Now().UTC()), stage, message, message, id)
	return e
}
func (s *Store) TemplateLogs(ctx context.Context, id string, after int64, limit int) (tb.Logs, error) {
	out := tb.Logs{Records: []tb.Log{}, Next: after}
	rows, e := s.DB.QueryContext(ctx, `SELECT sequence,timestamp,stage,message FROM template_build_logs WHERE build_id=? AND sequence>? ORDER BY sequence LIMIT ?`, id, after, limit)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var l tb.Log
		var ts string
		if e = rows.Scan(&l.Sequence, &ts, &l.Stage, &l.Message); e != nil {
			return out, e
		}
		l.Timestamp, e = time.Parse(timeLayout, ts)
		if e != nil {
			return out, e
		}
		out.Records = append(out.Records, l)
		out.Next = l.Sequence
	}
	return out, rows.Err()
}
func (s *Store) TemplateManifest(ctx context.Context, vmid int) (tb.Manifest, error) {
	var m tb.Manifest
	var raw string
	e := s.DB.QueryRowContext(ctx, `SELECT record_json FROM template_manifests WHERE vmid=?`, vmid).Scan(&raw)
	if e != nil {
		return m, e
	}
	e = json.Unmarshal([]byte(raw), &m)
	return m, e
}
func (s *Store) TemplateManifests(ctx context.Context, offset, limit int) ([]tb.Manifest, error) {
	out := []tb.Manifest{}
	rows, e := s.DB.QueryContext(ctx, `SELECT record_json FROM template_manifests ORDER BY vmid LIMIT ? OFFSET ?`, limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var m tb.Manifest
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &m); e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func templateEventJSON(kind string, payload any) string {
	raw, _ := json.Marshal(map[string]any{"kind": kind, "schema_version": 1, "stage": "lifecycle", "payload": payload})
	return string(raw)
}

// ImportTemplateLogs retains the worker's UTC time and sequence across daemon
// downtime. A batch and its cursor commit atomically, so retries cannot duplicate
// records or move the cursor past a missing source record.
func (s *Store) ImportTemplateLogs(ctx context.Context, id string, records []tb.Log) error {
	if len(records) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var last int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM template_build_logs WHERE build_id=?`, id).Scan(&last); err != nil {
		return err
	}
	lastStage := ""
	if last > 0 {
		if err = tx.QueryRowContext(ctx, `SELECT stage FROM template_build_logs WHERE build_id=? AND sequence=?`, id, last).Scan(&lastStage); err != nil {
			return err
		}
	}
	for _, record := range records {
		if record.Sequence <= last {
			continue
		}
		if record.Sequence != last+1 {
			return fmt.Errorf("template log sequence gap: expected %d, received %d", last+1, record.Sequence)
		}
		if record.Timestamp.IsZero() {
			return fmt.Errorf("template log timestamp is missing")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO template_build_logs(build_id,sequence,timestamp,stage,message) VALUES(?,?,?,?,?)`, id, record.Sequence, formatTime(record.Timestamp.UTC()), record.Stage, record.Message); err != nil {
			return err
		}
		if record.Stage != lastStage {
			kind := "template.build.stage_changed"
			payload := map[string]any{"build_id": id, "stage": record.Stage}
			if _, err = tx.ExecContext(ctx, `INSERT INTO events(ts,kind,msg,json) VALUES(?,?,?,?)`, formatTime(record.Timestamp.UTC()), kind, id, templateEventJSON(kind, payload)); err != nil {
				return err
			}
			lastStage = record.Stage
		}
		last = record.Sequence
	}
	return tx.Commit()
}

// ClaimTemplateBuild atomically acquires the host's single worker slot. The
// record comparison also prevents a stale claimant from undoing cancellation.
// The RUNNING row is durable before any worker configuration or unit is created.
func (s *Store) ClaimTemplateBuild(ctx context.Context, id string) (tb.Build, bool, error) {
	var b tb.Build
	var previous string
	if err := s.DB.QueryRowContext(ctx, `SELECT record_json FROM template_builds WHERE id=?`, id).Scan(&previous); err != nil {
		return b, false, err
	}
	if err := json.Unmarshal([]byte(previous), &b); err != nil {
		return b, false, err
	}
	if b.State != "QUEUED" || b.CancelRequested {
		return b, false, nil
	}
	now := time.Now().UTC()
	b.State = "RUNNING"
	b.StartedAt = &now
	b.UpdatedAt = now
	encoded, err := json.Marshal(b)
	if err != nil {
		return b, false, err
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE template_builds SET state='RUNNING',record_json=? WHERE id=? AND state='QUEUED' AND record_json=? AND NOT EXISTS(SELECT 1 FROM template_builds WHERE state='RUNNING')`, string(encoded), id, previous)
	if err != nil {
		return b, false, err
	}
	count, err := result.RowsAffected()
	return b, count == 1, err
}

// MarkTemplateWorkerLaunched records a positive systemd acknowledgement without
// overwriting cancellation or stage metadata changed by a concurrent observer.
func (s *Store) MarkTemplateWorkerLaunched(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE template_builds SET record_json=json_set(record_json,'$.worker_launched',json('true')) WHERE id=? AND state='RUNNING'`, id)
	return err
}
