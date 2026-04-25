package user

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// Registry manages users, teams, and access control.
//
// It provides high-level operations for user management and role-based
// access control, building on top of the Store's database operations.
type Registry struct {
	store *Store
}

// NewRegistry creates a new user registry backed by the given store.
func NewRegistry(store *Store) *Registry {
	return &Registry{store: store}
}

// Store returns the underlying user store for direct database access.
func (r *Registry) Store() *Store {
	return r.store
}

// AddUser creates a new user. If this is the first user, they are automatically
// assigned the admin role regardless of the requested role.
func (r *Registry) AddUser(ctx context.Context, name string, sshPublicKey string, role Role) (User, error) {
	if r.store == nil {
		return User{}, errors.New("registry store is nil")
	}
	if name == "" {
		return User{}, errors.New("user name is required")
	}
	if sshPublicKey == "" {
		return User{}, errors.New("ssh public key is required")
	}

	// Parse the SSH public key to get the fingerprint.
	pubKey, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(sshPublicKey))
	if err != nil {
		return User{}, fmt.Errorf("parse ssh public key: %w", err)
	}
	fingerprint := ssh.FingerprintSHA256(pubKey)

	// Check if this key is already registered to another user.
	existing, err := r.store.GetUserByFingerprint(ctx, fingerprint)
	if err == nil && existing.ID != "" {
		return User{}, fmt.Errorf("SSH key already registered to user %q", existing.Name)
	}

	// If this is the first user, they become admin automatically.
	count, err := r.store.UserCount(ctx)
	if err != nil {
		return User{}, fmt.Errorf("check user count: %w", err)
	}
	if count == 0 {
		role = RoleAdmin
	}

	if !role.IsValid() {
		return User{}, fmt.Errorf("invalid role: %s (must be admin or user)", role)
	}

	u := User{
		ID:          name,
		Name:        name,
		Role:        role,
		Fingerprint: fingerprint,
	}

	created, err := r.store.CreateUser(ctx, u, sshPublicKey)
	if err != nil {
		return User{}, err
	}

	// Audit the user creation.
	_ = r.store.Audit(ctx, created.ID, "user.add", "user:"+name, "role="+string(role)+" fingerprint="+fingerprint+" comment="+comment)

	return created, nil
}

// RemoveUser deletes a user. Admins cannot remove themselves if they are the last admin.
func (r *Registry) RemoveUser(ctx context.Context, requesterID, targetUserID string) error {
	if r.store == nil {
		return errors.New("registry store is nil")
	}
	target, err := r.store.GetUser(ctx, targetUserID)
	if err != nil {
		return fmt.Errorf("user not found: %w", err)
	}

	// Prevent removing the last admin.
	if target.Role == RoleAdmin {
		users, err := r.store.ListUsers(ctx)
		if err != nil {
			return err
		}
		adminCount := 0
		for _, u := range users {
			if u.Role == RoleAdmin {
				adminCount++
			}
		}
		if adminCount <= 1 {
			return errors.New("cannot remove the last admin user")
		}
	}

	err = r.store.DeleteUser(ctx, targetUserID)
	if err != nil {
		return err
	}

	_ = r.store.Audit(ctx, requesterID, "user.remove", "user:"+target.Name, "")
	return nil
}

// LookupByFingerprint finds a user by SSH key fingerprint.
func (r *Registry) LookupByFingerprint(ctx context.Context, fingerprint string) (*User, error) {
	if r.store == nil {
		return nil, errors.New("registry store is nil")
	}
	u, err := r.store.GetUserByFingerprint(ctx, fingerprint)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// IsAdmin checks whether a user has admin privileges.
func (r *Registry) IsAdmin(ctx context.Context, userID string) bool {
	if r.store == nil {
		return false
	}
	u, err := r.store.GetUser(ctx, userID)
	if err != nil {
		return false
	}
	return u.Role == RoleAdmin
}

// CanAccessSandbox checks whether a user can access a specific sandbox.
// Admins can access all sandboxes. Regular users can only access their own.
func (r *Registry) CanAccessSandbox(ctx context.Context, userID string, sandboxOwner string) bool {
	if r.store == nil {
		// If no user system, allow all access (single-user mode).
		return true
	}
	// If no users are registered, allow all access (single-user mode).
	count, err := r.store.UserCount(ctx)
	if err != nil || count == 0 {
		return true
	}
	if r.IsAdmin(ctx, userID) {
		return true
	}
	// Users can access their own sandboxes and unowned sandboxes.
	return sandboxOwner == "" || sandboxOwner == userID
}

// ListUsers returns all registered users with their sandbox counts.
func (r *Registry) ListUsers(ctx context.Context) ([]User, error) {
	if r.store == nil {
		return nil, errors.New("registry store is nil")
	}
	return r.store.ListUsers(ctx)
}

// --- Team management ---

// CreateTeam creates a new team.
func (r *Registry) CreateTeam(ctx context.Context, name, description string, creatorID string) (Team, error) {
	if r.store == nil {
		return Team{}, errors.New("registry store is nil")
	}
	if name == "" {
		return Team{}, errors.New("team name is required")
	}
	t := Team{
		ID:          name,
		Name:        name,
		Description: description,
	}
	created, err := r.store.CreateTeam(ctx, t)
	if err != nil {
		return Team{}, err
	}

	// Add the creator as team admin.
	if creatorID != "" {
		if err := r.store.AddTeamMember(ctx, created.ID, creatorID, RoleAdmin); err != nil {
			return Team{}, fmt.Errorf("add creator to team: %w", err)
		}
	}

	_ = r.store.Audit(ctx, creatorID, "team.add", "team:"+name, "description="+description)
	return created, nil
}

// RemoveTeam deletes a team.
func (r *Registry) RemoveTeam(ctx context.Context, teamID, requesterID string) error {
	if r.store == nil {
		return errors.New("registry store is nil")
	}
	err := r.store.DeleteTeam(ctx, teamID)
	if err != nil {
		return err
	}
	_ = r.store.Audit(ctx, requesterID, "team.remove", "team:"+teamID, "")
	return nil
}

// AddTeamMember adds a user to a team.
func (r *Registry) AddTeamMember(ctx context.Context, teamID, userID string, role Role, requesterID string) error {
	if r.store == nil {
		return errors.New("registry store is nil")
	}
	err := r.store.AddTeamMember(ctx, teamID, userID, role)
	if err != nil {
		return err
	}
	_ = r.store.Audit(ctx, requesterID, "team.member.add", "team:"+teamID, "user="+userID+" role="+string(role))
	return nil
}

// RemoveTeamMember removes a user from a team.
func (r *Registry) RemoveTeamMember(ctx context.Context, teamID, userID string, requesterID string) error {
	if r.store == nil {
		return errors.New("registry store is nil")
	}
	err := r.store.RemoveTeamMember(ctx, teamID, userID)
	if err != nil {
		return err
	}
	_ = r.store.Audit(ctx, requesterID, "team.member.remove", "team:"+teamID, "user="+userID)
	return nil
}

// ListTeamMembers returns all members of a team with their user details.
func (r *Registry) ListTeamMembers(ctx context.Context, teamID string) ([]TeamMember, error) {
	if r.store == nil {
		return nil, errors.New("registry store is nil")
	}
	return r.store.ListTeamMembers(ctx, teamID)
}

// ListTeams returns all teams.
func (r *Registry) ListTeams(ctx context.Context) ([]Team, error) {
	if r.store == nil {
		return nil, errors.New("registry store is nil")
	}
	return r.store.ListTeams(ctx)
}

// --- Audit ---

// RecordAction records an action in the audit log.
func (r *Registry) RecordAction(ctx context.Context, userID, action, resource, detail string) error {
	if r.store == nil {
		return nil
	}
	return r.store.Audit(ctx, userID, action, resource, detail)
}

// ListAuditLog returns recent audit log entries.
func (r *Registry) ListAuditLog(ctx context.Context, userID string, limit int) ([]AuditEntry, error) {
	if r.store == nil {
		return nil, errors.New("registry store is nil")
	}
	return r.store.ListAuditLog(ctx, userID, limit)
}
