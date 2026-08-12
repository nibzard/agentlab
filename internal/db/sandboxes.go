// ABOUTME: Sandbox database operations for managing VM sandbox state and metadata.
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

// timeLayout is the format used for storing timestamps in SQLite.
// timeLayout accepts BOTH the legacy RFC3339Nano form (fractional trailing
// zeros trimmed) and the fixed-width form below, so reads remain compatible
// during and after the M1 migration. It is used for parsing only.
const timeLayout = time.RFC3339Nano

// timeLayoutFixed emits a fixed-width 9-digit fractional component. Because the
// width never varies, SQLite's lexicographic TEXT comparison orders these
// values identically to their chronological order — including within the same
// second, where RFC3339Nano's trimmed form can mis-order (review M1). Used for
// formatting only.
const timeLayoutFixed = "2006-01-02T15:04:05.000000000Z07:00"

// CreateSandbox inserts a new sandbox row.
//
// The sandbox must have valid VMID, name, profile, and state fields.
// Timestamps are set to current time if zero.
//
// Parameters:
//   - ctx: Context for cancellation
//   - sandbox: The sandbox to create
//
// Returns an error if validation fails or the insert fails.
func (s *Store) CreateSandbox(ctx context.Context, sandbox models.Sandbox) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	if sandbox.VMID <= 0 {
		return errors.New("sandbox vmid is required")
	}
	if sandbox.Name == "" {
		return errors.New("sandbox name is required")
	}
	if sandbox.Profile == "" {
		return errors.New("sandbox profile is required")
	}
	if sandbox.State == "" {
		return errors.New("sandbox state is required")
	}
	now := time.Now().UTC()
	createdAt := sandbox.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := sandbox.LastUpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	var lease interface{}
	if !sandbox.LeaseExpires.IsZero() {
		lease = formatTime(sandbox.LeaseExpires)
	}
	var lastUsed interface{}
	if !sandbox.LastUsedAt.IsZero() {
		lastUsed = formatTime(sandbox.LastUsedAt)
	}
	var workspace interface{}
	if sandbox.WorkspaceID != nil && *sandbox.WorkspaceID != "" {
		workspace = *sandbox.WorkspaceID
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO sandboxes (
		vmid, name, profile, state, ip, workspace_id, keepalive, lease_expires_at, last_used_at, created_at, updated_at, meta_json, type, image, tags, prompt, owner
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sandbox.VMID,
		sandbox.Name,
		sandbox.Profile,
		sandbox.State,
		nullIfEmpty(sandbox.IP),
		workspace,
		sandbox.Keepalive,
		lease,
		lastUsed,
		formatTime(createdAt),
		formatTime(updatedAt),
		nil,
		sandboxTypeOrDefault(sandbox.Type),
		nullIfEmpty(sandbox.Image),
		sandbox.Tags,
		sandbox.Prompt,
		sandbox.Owner,
	)
	if err != nil {
		return fmt.Errorf("insert sandbox %d: %w", sandbox.VMID, err)
	}
	return nil
}

// GetSandbox loads a sandbox by vmid.
//
// Parameters:
//   - ctx: Context for cancellation
//   - vmid: The VM ID of the sandbox to load
//
// Returns the sandbox and nil on success, or an error if not found
// (sql.ErrNoRows) or on database error.
func (s *Store) GetSandbox(ctx context.Context, vmid int) (models.Sandbox, error) {
	if s == nil || s.DB == nil {
		return models.Sandbox{}, errors.New("db store is nil")
	}
	row := s.DB.QueryRowContext(ctx, `SELECT vmid, name, profile, state, ip, workspace_id, keepalive, lease_expires_at, last_used_at, created_at, updated_at, type, image, tags, prompt, owner
		FROM sandboxes WHERE vmid = ?`, vmid)
	return scanSandboxRow(row)
}

// GetSandboxByIP loads a sandbox by its IP address.
//
// Parameters:
//   - ctx: Context for cancellation
//   - ip: The IP address of the sandbox to look up
//
// Returns the sandbox and nil on success, or an error if not found
// (sql.ErrNoRows) or on database error.
func (s *Store) GetSandboxByIP(ctx context.Context, ip string) (models.Sandbox, error) {
	if s == nil || s.DB == nil {
		return models.Sandbox{}, errors.New("db store is nil")
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return models.Sandbox{}, errors.New("ip is required")
	}
	row := s.DB.QueryRowContext(ctx, `SELECT vmid, name, profile, state, ip, workspace_id, keepalive, lease_expires_at, last_used_at, created_at, updated_at, type, image, tags, prompt, owner
		FROM sandboxes WHERE ip = ?`, ip)
	return scanSandboxRow(row)
}

// liveStatesForCredentialProxy are the sandbox states considered live enough to
// originate credential-proxy traffic. A stale, stopped, suspended, failed, or
// destroyed row that retains an address must NOT supply identity for named or
// tagged attachment (review H4).
var liveStatesForCredentialProxy = []models.SandboxState{
	models.SandboxProvisioning,
	models.SandboxBooting,
	models.SandboxReady,
	models.SandboxRunning,
}

// ErrAmbiguousSandbox indicates that more than one live sandbox matched a
// lookup (e.g. a duplicated address). Credential resolution must treat this as
// "unidentified" rather than guessing (review H4).
var ErrAmbiguousSandbox = errors.New("ambiguous sandbox identity for address")

// eligibleStateList renders the live-state set as a SQL IN-list. The values are
// package constants, never user input, so interpolation is safe.
func eligibleStateList() string {
	parts := make([]string, len(liveStatesForCredentialProxy))
	for i, st := range liveStatesForCredentialProxy {
		parts[i] = "'" + string(st) + "'"
	}
	return strings.Join(parts, ",")
}

// GetLiveSandboxByIP loads the unique live sandbox currently bound to ip. Only
// sandboxes in an eligible live state match; stale, stopped, suspended, failed,
// or destroyed rows are ignored so a reused address cannot inherit the prior
// sandbox's name or tags. It returns sql.ErrNoRows when no live sandbox matches
// and ErrAmbiguousSandbox when more than one does (review H4).
func (s *Store) GetLiveSandboxByIP(ctx context.Context, ip string) (models.Sandbox, error) {
	if s == nil || s.DB == nil {
		return models.Sandbox{}, errors.New("db store is nil")
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return models.Sandbox{}, errors.New("ip is required")
	}
	query := `SELECT vmid, name, profile, state, ip, workspace_id, keepalive, lease_expires_at, last_used_at, created_at, updated_at, type, image, tags, prompt, owner
		FROM sandboxes WHERE ip = ? AND state IN (` + eligibleStateList() + `)`
	rows, err := s.DB.QueryContext(ctx, query, ip)
	if err != nil {
		return models.Sandbox{}, fmt.Errorf("live sandbox by ip %s: %w", ip, err)
	}
	defer rows.Close()
	var found []models.Sandbox
	for rows.Next() {
		sb, err := scanSandboxRow(rows)
		if err != nil {
			return models.Sandbox{}, err
		}
		found = append(found, sb)
	}
	if err := rows.Err(); err != nil {
		return models.Sandbox{}, err
	}
	switch len(found) {
	case 0:
		return models.Sandbox{}, sql.ErrNoRows
	case 1:
		return found[0], nil
	default:
		return models.Sandbox{}, ErrAmbiguousSandbox
	}
}

// ClearSandboxIP retires the address association for a sandbox, so a later
// reuse of that address cannot resolve to this (now destroyed) row. Called on
// transition to DESTROYED (review H4).
func (s *Store) ClearSandboxIP(ctx context.Context, vmid int) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE sandboxes SET ip = '', updated_at = ? WHERE vmid = ?`,
		formatTime(time.Now().UTC()), vmid)
	if err != nil {
		return fmt.Errorf("clear sandbox %d ip: %w", vmid, err)
	}
	return nil
}

// ListSandboxes returns all sandboxes ordered by created_at descending.
func (s *Store) ListSandboxes(ctx context.Context) ([]models.Sandbox, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT vmid, name, profile, state, ip, workspace_id, keepalive, lease_expires_at, last_used_at, created_at, updated_at, type, image, tags, prompt, owner
		FROM sandboxes ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}
	defer rows.Close()
	var out []models.Sandbox
	for rows.Next() {
		sb, err := scanSandboxRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sb)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sandboxes: %w", err)
	}
	return out, nil
}

// CountSandboxesByState returns a count of sandboxes grouped by state.
func (s *Store) CountSandboxesByState(ctx context.Context) (map[models.SandboxState]int, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT state, COUNT(*) FROM sandboxes GROUP BY state`)
	if err != nil {
		return nil, fmt.Errorf("count sandboxes: %w", err)
	}
	defer rows.Close()
	out := make(map[models.SandboxState]int)
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, fmt.Errorf("scan sandbox count: %w", err)
		}
		if state == "" {
			continue
		}
		out[models.SandboxState(state)] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sandbox counts: %w", err)
	}
	return out, nil
}

// MaxSandboxVMID returns the highest vmid stored, or 0 if none.
func (s *Store) MaxSandboxVMID(ctx context.Context) (int, error) {
	if s == nil || s.DB == nil {
		return 0, errors.New("db store is nil")
	}
	row := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(vmid), 0) FROM sandboxes`)
	var max int
	if err := row.Scan(&max); err != nil {
		return 0, fmt.Errorf("scan max vmid: %w", err)
	}
	return max, nil
}

// ListExpiredSandboxes returns sandboxes with leases expired at or before now.
func (s *Store) ListExpiredSandboxes(ctx context.Context, now time.Time) ([]models.Sandbox, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	cutoff := formatTime(now)
	rows, err := s.DB.QueryContext(ctx, `SELECT vmid, name, profile, state, ip, workspace_id, keepalive, lease_expires_at, last_used_at, created_at, updated_at, type, image, tags, prompt, owner
		FROM sandboxes
		WHERE lease_expires_at IS NOT NULL AND lease_expires_at <= ? AND state != ?`, cutoff, models.SandboxDestroyed)
	if err != nil {
		return nil, fmt.Errorf("list expired sandboxes: %w", err)
	}
	defer rows.Close()
	var out []models.Sandbox
	for rows.Next() {
		sb, err := scanSandboxRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sb)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired sandboxes: %w", err)
	}
	return out, nil
}

// UpdateSandboxState performs a compare-and-swap state transition.
func (s *Store) UpdateSandboxState(ctx context.Context, vmid int, from, to models.SandboxState) (bool, error) {
	if s == nil || s.DB == nil {
		return false, errors.New("db store is nil")
	}
	updatedAt := formatTime(time.Now().UTC())
	res, err := s.DB.ExecContext(ctx, `UPDATE sandboxes SET state = ?, updated_at = ? WHERE vmid = ? AND state = ?`,
		to, updatedAt, vmid, from)
	if err != nil {
		return false, fmt.Errorf("update sandbox %d state: %w", vmid, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected sandbox %d: %w", vmid, err)
	}
	return affected > 0, nil
}

// ForceSetSandboxState overwrites the stored sandbox state without a compare-and-swap check.
func (s *Store) ForceSetSandboxState(ctx context.Context, vmid int, state models.SandboxState) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	if vmid <= 0 {
		return errors.New("vmid must be positive")
	}
	if state == "" {
		return errors.New("sandbox state is required")
	}
	updatedAt := formatTime(time.Now().UTC())
	res, err := s.DB.ExecContext(ctx, `UPDATE sandboxes SET state = ?, updated_at = ? WHERE vmid = ?`,
		state,
		updatedAt,
		vmid,
	)
	if err != nil {
		return fmt.Errorf("force update sandbox %d state: %w", vmid, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected sandbox %d force state: %w", vmid, err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpdateSandboxLease updates the lease expiration timestamp.
func (s *Store) UpdateSandboxLease(ctx context.Context, vmid int, leaseExpiresAt time.Time) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	var lease interface{}
	if !leaseExpiresAt.IsZero() {
		lease = formatTime(leaseExpiresAt)
	}
	updatedAt := formatTime(time.Now().UTC())
	_, err := s.DB.ExecContext(ctx, `UPDATE sandboxes SET lease_expires_at = ?, updated_at = ? WHERE vmid = ?`, lease, updatedAt, vmid)
	if err != nil {
		return fmt.Errorf("update sandbox %d lease: %w", vmid, err)
	}
	return nil
}

// UpdateSandboxLastUsed updates the last_used_at timestamp.
func (s *Store) UpdateSandboxLastUsed(ctx context.Context, vmid int, lastUsedAt time.Time) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	if vmid <= 0 {
		return errors.New("vmid must be positive")
	}
	var lastUsed interface{}
	if !lastUsedAt.IsZero() {
		lastUsed = formatTime(lastUsedAt)
	}
	updatedAt := formatTime(time.Now().UTC())
	res, err := s.DB.ExecContext(ctx, `UPDATE sandboxes SET last_used_at = ?, updated_at = ? WHERE vmid = ?`,
		lastUsed,
		updatedAt,
		vmid,
	)
	if err != nil {
		return fmt.Errorf("update sandbox %d last_used_at: %w", vmid, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected sandbox %d last_used_at: %w", vmid, err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpdateSandboxIP updates the IP address for a sandbox.
func (s *Store) UpdateSandboxIP(ctx context.Context, vmid int, ip string) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	if vmid <= 0 {
		return errors.New("vmid must be positive")
	}
	updatedAt := formatTime(time.Now().UTC())
	_, err := s.DB.ExecContext(ctx, `UPDATE sandboxes SET ip = ?, updated_at = ? WHERE vmid = ?`,
		nullIfEmpty(ip),
		updatedAt,
		vmid,
	)
	if err != nil {
		return fmt.Errorf("update sandbox %d ip: %w", vmid, err)
	}
	return nil
}

// UpdateSandboxWorkspace updates the workspace id for a sandbox.
func (s *Store) UpdateSandboxWorkspace(ctx context.Context, vmid int, workspaceID *string) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	if vmid <= 0 {
		return errors.New("vmid must be positive")
	}
	var workspace interface{}
	if workspaceID != nil && strings.TrimSpace(*workspaceID) != "" {
		workspace = strings.TrimSpace(*workspaceID)
	}
	updatedAt := formatTime(time.Now().UTC())
	res, err := s.DB.ExecContext(ctx, `UPDATE sandboxes SET workspace_id = ?, updated_at = ? WHERE vmid = ?`,
		workspace,
		updatedAt,
		vmid,
	)
	if err != nil {
		return fmt.Errorf("update sandbox %d workspace: %w", vmid, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected sandbox %d workspace: %w", vmid, err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpdateSandboxTags replaces the comma-joined tag string for a sandbox. An empty
// tags value clears the set (the column is NOT NULL, so clearing stores the
// empty string rather than NULL). Tags are expected to already be normalized by
// the caller (review M6).
func (s *Store) UpdateSandboxTags(ctx context.Context, vmid int, tags string) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	if vmid <= 0 {
		return errors.New("vmid must be positive")
	}
	updatedAt := formatTime(time.Now().UTC())
	res, err := s.DB.ExecContext(ctx, `UPDATE sandboxes SET tags = ?, updated_at = ? WHERE vmid = ?`,
		tags,
		updatedAt,
		vmid,
	)
	if err != nil {
		return fmt.Errorf("update sandbox %d tags: %w", vmid, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected sandbox %d tags: %w", vmid, err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// TouchSandbox updates only the updated_at timestamp for a sandbox.
func (s *Store) TouchSandbox(ctx context.Context, vmid int) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	if vmid <= 0 {
		return errors.New("vmid must be positive")
	}
	updatedAt := formatTime(time.Now().UTC())
	res, err := s.DB.ExecContext(ctx, `UPDATE sandboxes SET updated_at = ? WHERE vmid = ?`, updatedAt, vmid)
	if err != nil {
		return fmt.Errorf("touch sandbox %d: %w", vmid, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected sandbox %d touch: %w", vmid, err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// RecordEvent inserts an event row.
func (s *Store) RecordEvent(ctx context.Context, kind string, sandboxVMID *int, jobID *string, msg string, jsonPayload string) error {
	if s == nil || s.DB == nil {
		return errors.New("db store is nil")
	}
	if kind == "" {
		return errors.New("event kind is required")
	}
	now := formatTime(time.Now().UTC())
	var vmid sql.NullInt64
	if sandboxVMID != nil {
		vmid = sql.NullInt64{Valid: true, Int64: int64(*sandboxVMID)}
	}
	var job sql.NullString
	if jobID != nil && *jobID != "" {
		job = sql.NullString{Valid: true, String: *jobID}
	}
	var msgVal interface{}
	if msg != "" {
		msgVal = msg
	}
	var jsonVal interface{}
	if jsonPayload != "" {
		jsonVal = jsonPayload
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO events (ts, kind, sandbox_vmid, job_id, msg, json) VALUES (?, ?, ?, ?, ?, ?)`,
		now, kind, vmid, job, msgVal, jsonVal)
	if err != nil {
		return fmt.Errorf("insert event %q: %w", kind, err)
	}
	return nil
}

func scanSandboxRow(scanner interface{ Scan(dest ...any) error }) (models.Sandbox, error) {
	var sb models.Sandbox
	var state string
	var ip sql.NullString
	var workspace sql.NullString
	var keepalive sql.NullBool
	var lease sql.NullString
	var lastUsed sql.NullString
	var createdAt string
	var updatedAt string
	var sbType sql.NullString
	var sbImage sql.NullString
	var sbTags sql.NullString
	var sbPrompt sql.NullString
	var sbOwner sql.NullString
	if err := scanner.Scan(&sb.VMID, &sb.Name, &sb.Profile, &state, &ip, &workspace, &keepalive, &lease, &lastUsed, &createdAt, &updatedAt, &sbType, &sbImage, &sbTags, &sbPrompt, &sbOwner); err != nil {
		return models.Sandbox{}, err
	}
	if state == "" {
		return models.Sandbox{}, errors.New("sandbox state missing")
	}
	sb.State = models.SandboxState(state)
	if sbType.Valid && sbType.String != "" {
		sb.Type = models.SandboxType(sbType.String)
	} else {
		sb.Type = models.SandboxTypeVM
	}
	if sbImage.Valid {
		sb.Image = sbImage.String
	}
	if sbTags.Valid {
		sb.Tags = sbTags.String
	}
	if sbPrompt.Valid {
		sb.Prompt = sbPrompt.String
	}
	if sbOwner.Valid {
		sb.Owner = sbOwner.String
	}
	if ip.Valid {
		sb.IP = ip.String
	}
	if workspace.Valid {
		value := workspace.String
		sb.WorkspaceID = &value
	}
	if keepalive.Valid {
		sb.Keepalive = keepalive.Bool
	}
	if lease.Valid {
		parsed, err := parseTime(lease.String)
		if err != nil {
			return models.Sandbox{}, fmt.Errorf("parse lease_expires_at: %w", err)
		}
		sb.LeaseExpires = parsed
	}
	if lastUsed.Valid {
		parsed, err := parseTime(lastUsed.String)
		if err != nil {
			return models.Sandbox{}, fmt.Errorf("parse last_used_at: %w", err)
		}
		sb.LastUsedAt = parsed
	}
	var err error
	if createdAt != "" {
		sb.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return models.Sandbox{}, fmt.Errorf("parse created_at: %w", err)
		}
	}
	if updatedAt != "" {
		sb.LastUpdatedAt, err = parseTime(updatedAt)
		if err != nil {
			return models.Sandbox{}, fmt.Errorf("parse updated_at: %w", err)
		}
	}
	return sb, nil
}

// sandboxTypeOrDefault returns the sandbox type or "vm" if empty.
func sandboxTypeOrDefault(t models.SandboxType) string {
	if t == "" {
		return string(models.SandboxTypeVM)
	}
	return string(t)
}

func parseTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(timeLayout, value)
	if err != nil {
		// Fall back to the fixed-width layout in case a future variant slips in.
		if p2, err2 := time.Parse(timeLayoutFixed, value); err2 == nil {
			return p2, nil
		}
		return time.Time{}, err
	}
	return parsed, nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(timeLayoutFixed)
}

func nullIfEmpty(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}
