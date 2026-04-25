package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/agentlab/agentlab/internal/db"
)

// timeLayout matches the db package format.
const timeLayout = time.RFC3339Nano

// Store provides database operations for users, teams, and audit logging.
type Store struct {
	db *db.Store
}

// NewStore creates a new user store backed by the given database store.
func NewStore(database *db.Store) *Store {
	return &Store{db: database}
}

// --- User operations ---

// CreateUser creates a new user with the given name, role, and primary SSH key.
func (s *Store) CreateUser(ctx context.Context, u User, publicKey string) (User, error) {
	if s.db == nil {
		return User{}, errors.New("db store is nil")
	}
	if u.Name == "" {
		return User{}, errors.New("user name is required")
	}
	if u.Fingerprint == "" {
		return User{}, errors.New("user fingerprint is required")
	}
	if !u.Role.IsValid() {
		return User{}, fmt.Errorf("invalid role: %s", u.Role)
	}

	now := time.Now().UTC()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	if u.UpdatedAt.IsZero() {
		u.UpdatedAt = now
	}
	if u.ID == "" {
		u.ID = u.Name
	}

	_, err := s.db.DB.ExecContext(ctx,
		`INSERT INTO users (id, name, role, primary_fingerprint, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		u.ID, u.Name, string(u.Role), u.Fingerprint, formatTime(u.CreatedAt), formatTime(u.UpdatedAt))
	if err != nil {
		return User{}, fmt.Errorf("insert user %s: %w", u.Name, err)
	}

	// Add the primary SSH key.
	if publicKey != "" {
		_, err = s.db.DB.ExecContext(ctx,
			`INSERT INTO user_ssh_keys (user_id, fingerprint, public_key, comment, added_at) VALUES (?, ?, ?, ?, ?)`,
			u.ID, u.Fingerprint, publicKey, "", formatTime(now))
		if err != nil {
			return User{}, fmt.Errorf("insert ssh key for user %s: %w", u.Name, err)
		}
	}

	return u, nil
}

// GetUser retrieves a user by ID.
func (s *Store) GetUser(ctx context.Context, id string) (User, error) {
	if s.db == nil {
		return User{}, errors.New("db store is nil")
	}
	var u User
	var role string
	var createdAt, updatedAt string
	err := s.db.DB.QueryRowContext(ctx,
		`SELECT id, name, role, primary_fingerprint, created_at, updated_at FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Name, &role, &u.Fingerprint, &createdAt, &updatedAt)
	if err != nil {
		return User{}, err
	}
	u.Role = Role(role)
	u.CreatedAt, _ = parseTime(createdAt)
	u.UpdatedAt, _ = parseTime(updatedAt)
	return u, nil
}

// GetUserByName retrieves a user by name.
func (s *Store) GetUserByName(ctx context.Context, name string) (User, error) {
	if s.db == nil {
		return User{}, errors.New("db store is nil")
	}
	var u User
	var role string
	var createdAt, updatedAt string
	err := s.db.DB.QueryRowContext(ctx,
		`SELECT id, name, role, primary_fingerprint, created_at, updated_at FROM users WHERE name = ?`, name).
		Scan(&u.ID, &u.Name, &role, &u.Fingerprint, &createdAt, &updatedAt)
	if err != nil {
		return User{}, err
	}
	u.Role = Role(role)
	u.CreatedAt, _ = parseTime(createdAt)
	u.UpdatedAt, _ = parseTime(updatedAt)
	return u, nil
}

// GetUserByFingerprint retrieves a user by any of their SSH key fingerprints.
func (s *Store) GetUserByFingerprint(ctx context.Context, fingerprint string) (User, error) {
	if s.db == nil {
		return User{}, errors.New("db store is nil")
	}
	// First check user_ssh_keys table for any key associated with the user.
	var userID string
	err := s.db.DB.QueryRowContext(ctx,
		`SELECT user_id FROM user_ssh_keys WHERE fingerprint = ?`, fingerprint).
		Scan(&userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Fall back to primary_fingerprint on users table.
			err = s.db.DB.QueryRowContext(ctx,
				`SELECT id FROM users WHERE primary_fingerprint = ?`, fingerprint).
				Scan(&userID)
			if err != nil {
				return User{}, err
			}
		} else {
			return User{}, err
		}
	}
	return s.GetUser(ctx, userID)
}

// ListUsers returns all registered users.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	if s.db == nil {
		return nil, errors.New("db store is nil")
	}
	rows, err := s.db.DB.QueryContext(ctx,
		`SELECT id, name, role, primary_fingerprint, created_at, updated_at FROM users ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var u User
		var role string
		var createdAt, updatedAt string
		if err := rows.Scan(&u.ID, &u.Name, &role, &u.Fingerprint, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		u.Role = Role(role)
		u.CreatedAt, _ = parseTime(createdAt)
		u.UpdatedAt, _ = parseTime(updatedAt)
		users = append(users, u)
	}
	return users, rows.Err()
}

// DeleteUser removes a user and all associated SSH keys.
func (s *Store) DeleteUser(ctx context.Context, id string) error {
	if s.db == nil {
		return errors.New("db store is nil")
	}
	res, err := s.db.DB.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete user %s: %w", id, err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UserCount returns the total number of registered users.
func (s *Store) UserCount(ctx context.Context) (int, error) {
	if s.db == nil {
		return 0, errors.New("db store is nil")
	}
	var count int
	err := s.db.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}

// --- SSH Key operations ---

// AddSSHKey associates an additional SSH public key with a user.
func (s *Store) AddSSHKey(ctx context.Context, userID, fingerprint, publicKey, comment string) error {
	if s.db == nil {
		return errors.New("db store is nil")
	}
	now := formatTime(time.Now().UTC())
	_, err := s.db.DB.ExecContext(ctx,
		`INSERT INTO user_ssh_keys (user_id, fingerprint, public_key, comment, added_at) VALUES (?, ?, ?, ?, ?)`,
		userID, fingerprint, publicKey, comment, now)
	if err != nil {
		return fmt.Errorf("add ssh key for user %s: %w", userID, err)
	}
	return nil
}

// RemoveSSHKey removes an SSH key from a user by fingerprint.
func (s *Store) RemoveSSHKey(ctx context.Context, userID, fingerprint string) error {
	if s.db == nil {
		return errors.New("db store is nil")
	}
	res, err := s.db.DB.ExecContext(ctx,
		`DELETE FROM user_ssh_keys WHERE user_id = ? AND fingerprint = ?`, userID, fingerprint)
	if err != nil {
		return fmt.Errorf("remove ssh key for user %s: %w", userID, err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ListSSHKeys returns all SSH keys for a user.
func (s *Store) ListSSHKeys(ctx context.Context, userID string) ([]SSHKey, error) {
	if s.db == nil {
		return nil, errors.New("db store is nil")
	}
	rows, err := s.db.DB.QueryContext(ctx,
		`SELECT id, user_id, fingerprint, public_key, comment, added_at FROM user_ssh_keys WHERE user_id = ? ORDER BY added_at ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list ssh keys for user %s: %w", userID, err)
	}
	defer rows.Close()
	var keys []SSHKey
	for rows.Next() {
		var k SSHKey
		var addedAt string
		if err := rows.Scan(&k.ID, &k.UserID, &k.Fingerprint, &k.PublicKey, &k.Comment, &addedAt); err != nil {
			return nil, err
		}
		k.AddedAt, _ = parseTime(addedAt)
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// --- Team operations ---

// CreateTeam creates a new team.
func (s *Store) CreateTeam(ctx context.Context, t Team) (Team, error) {
	if s.db == nil {
		return Team{}, errors.New("db store is nil")
	}
	if t.Name == "" {
		return Team{}, errors.New("team name is required")
	}
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = now
	}
	if t.ID == "" {
		t.ID = t.Name
	}
	_, err := s.db.DB.ExecContext(ctx,
		`INSERT INTO teams (id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Description, formatTime(t.CreatedAt), formatTime(t.UpdatedAt))
	if err != nil {
		return Team{}, fmt.Errorf("insert team %s: %w", t.Name, err)
	}
	return t, nil
}

// GetTeam retrieves a team by ID.
func (s *Store) GetTeam(ctx context.Context, id string) (Team, error) {
	if s.db == nil {
		return Team{}, errors.New("db store is nil")
	}
	var t Team
	var createdAt, updatedAt string
	err := s.db.DB.QueryRowContext(ctx,
		`SELECT id, name, description, created_at, updated_at FROM teams WHERE id = ?`, id).
		Scan(&t.ID, &t.Name, &t.Description, &createdAt, &updatedAt)
	if err != nil {
		return Team{}, err
	}
	t.CreatedAt, _ = parseTime(createdAt)
	t.UpdatedAt, _ = parseTime(updatedAt)
	return t, nil
}

// DeleteTeam removes a team.
func (s *Store) DeleteTeam(ctx context.Context, id string) error {
	if s.db == nil {
		return errors.New("db store is nil")
	}
	res, err := s.db.DB.ExecContext(ctx, `DELETE FROM teams WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete team %s: %w", id, err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ListTeams returns all teams.
func (s *Store) ListTeams(ctx context.Context) ([]Team, error) {
	if s.db == nil {
		return nil, errors.New("db store is nil")
	}
	rows, err := s.db.DB.QueryContext(ctx,
		`SELECT id, name, description, created_at, updated_at FROM teams ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	defer rows.Close()
	var teams []Team
	for rows.Next() {
		var t Team
		var createdAt, updatedAt string
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = parseTime(createdAt)
		t.UpdatedAt, _ = parseTime(updatedAt)
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

// --- Team Member operations ---

// AddTeamMember adds a user to a team with the given role.
func (s *Store) AddTeamMember(ctx context.Context, teamID, userID string, role Role) error {
	if s.db == nil {
		return errors.New("db store is nil")
	}
	if !role.IsValid() {
		return fmt.Errorf("invalid role: %s", role)
	}
	now := formatTime(time.Now().UTC())
	_, err := s.db.DB.ExecContext(ctx,
		`INSERT INTO team_members (team_id, user_id, role, joined_at) VALUES (?, ?, ?, ?)`,
		teamID, userID, string(role), now)
	if err != nil {
		return fmt.Errorf("add team member %s to team %s: %w", userID, teamID, err)
	}
	return nil
}

// RemoveTeamMember removes a user from a team.
func (s *Store) RemoveTeamMember(ctx context.Context, teamID, userID string) error {
	if s.db == nil {
		return errors.New("db store is nil")
	}
	_, err := s.db.DB.ExecContext(ctx,
		`DELETE FROM team_members WHERE team_id = ? AND user_id = ?`, teamID, userID)
	if err != nil {
		return fmt.Errorf("remove team member %s from team %s: %w", userID, teamID, err)
	}
	return nil
}

// ListTeamMembers returns all members of a team.
func (s *Store) ListTeamMembers(ctx context.Context, teamID string) ([]TeamMember, error) {
	if s.db == nil {
		return nil, errors.New("db store is nil")
	}
	rows, err := s.db.DB.QueryContext(ctx,
		`SELECT team_id, user_id, role, joined_at FROM team_members WHERE team_id = ? ORDER BY joined_at ASC`, teamID)
	if err != nil {
		return nil, fmt.Errorf("list team members for team %s: %w", teamID, err)
	}
	defer rows.Close()
	var members []TeamMember
	for rows.Next() {
		var m TeamMember
		var role string
		var joinedAt string
		if err := rows.Scan(&m.TeamID, &m.UserID, &role, &joinedAt); err != nil {
			return nil, err
		}
		m.Role = Role(role)
		m.JoinedAt, _ = parseTime(joinedAt)
		members = append(members, m)
	}
	return members, rows.Err()
}

// --- Audit Log operations ---

// Audit records an action in the audit log.
func (s *Store) Audit(ctx context.Context, userID, action, resource, detail string) error {
	if s.db == nil {
		return errors.New("db store is nil")
	}
	if action == "" {
		return errors.New("audit action is required")
	}
	now := formatTime(time.Now().UTC())
	_, err := s.db.DB.ExecContext(ctx,
		`INSERT INTO audit_log (user_id, action, resource, detail, timestamp) VALUES (?, ?, ?, ?, ?)`,
		userID, action, resource, detail, now)
	if err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}
	return nil
}

// ListAuditLog returns audit entries ordered by timestamp descending.
// If userID is non-empty, filters to that user. If limit > 0, limits results.
func (s *Store) ListAuditLog(ctx context.Context, userID string, limit int) ([]AuditEntry, error) {
	if s.db == nil {
		return nil, errors.New("db store is nil")
	}
	query := `SELECT id, user_id, action, resource, detail, timestamp FROM audit_log`
	var args []interface{}
	if userID != "" {
		query += ` WHERE user_id = ?`
		args = append(args, userID)
	}
	query += ` ORDER BY timestamp DESC`
	if limit > 0 {
		query += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := s.db.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit log: %w", err)
	}
	defer rows.Close()
	var entries []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var ts string
		if err := rows.Scan(&e.ID, &e.UserID, &e.Action, &e.Resource, &e.Detail, &ts); err != nil {
			return nil, err
		}
		e.Timestamp, _ = parseTime(ts)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// --- Resource Quota operations ---

// SetQuota sets resource quotas for a user or team.
func (s *Store) SetQuota(ctx context.Context, scopeType, scopeID string, maxSandboxes, maxCPU, maxRAMMB int) error {
	if s.db == nil {
		return errors.New("db store is nil")
	}
	_, err := s.db.DB.ExecContext(ctx,
		`INSERT INTO resource_quotas (scope_type, scope_id, max_sandboxes, max_cpu, max_ram_mb)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(scope_type, scope_id) DO UPDATE SET max_sandboxes=?, max_cpu=?, max_ram_mb=?`,
		scopeType, scopeID, maxSandboxes, maxCPU, maxRAMMB,
		maxSandboxes, maxCPU, maxRAMMB)
	if err != nil {
		return fmt.Errorf("set quota for %s:%s: %w", scopeType, scopeID, err)
	}
	return nil
}

// GetQuota returns the resource quota for a user or team. Returns nil if no quota is set.
func (s *Store) GetQuota(ctx context.Context, scopeType, scopeID string) (*ResourceQuota, error) {
	if s.db == nil {
		return nil, errors.New("db store is nil")
	}
	var q ResourceQuota
	err := s.db.DB.QueryRowContext(ctx,
		`SELECT id, scope_type, scope_id, max_sandboxes, max_cpu, max_ram_mb FROM resource_quotas WHERE scope_type = ? AND scope_id = ?`,
		scopeType, scopeID).
		Scan(&q.ID, &q.ScopeType, &q.ScopeID, &q.MaxSandboxes, &q.MaxCPU, &q.MaxRAMMB)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &q, nil
}

// --- helpers ---

func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

func parseTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(timeLayout, value)
}
