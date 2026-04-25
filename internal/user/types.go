// Package user provides multi-user support via SSH keys for AgentLab.
//
// Design (ref: IMPROVEMENT_PLAN.md §9):
//   - Users are identified by SSH key fingerprints (no passwords).
//   - A user can have multiple SSH keys associated with their account.
//   - Roles: admin (all sandboxes, user management) and user (own sandboxes only).
//   - Teams group users for resource sharing and quota management.
//   - All user actions are recorded in an audit log.
package user

import (
	"time"
)

// Role represents a user's permission level.
type Role string

const (
	// RoleAdmin grants full access: all sandboxes, user management, team management.
	RoleAdmin Role = "admin"
	// RoleUser grants access to own sandboxes only.
	RoleUser Role = "user"
)

// ValidRoles contains all valid role values.
var ValidRoles = []Role{RoleAdmin, RoleUser}

// IsValid checks whether a role value is recognized.
func (r Role) IsValid() bool {
	for _, v := range ValidRoles {
		if r == v {
			return true
		}
	}
	return false
}

// User represents a registered user identified by SSH keys.
type User struct {
	ID          string    // Unique identifier (auto-generated)
	Name        string    // Human-readable name (e.g., "alice")
	Role        Role      // "admin" or "user"
	Fingerprint string    // Primary SSH key fingerprint
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// SSHKey represents an SSH public key associated with a user.
type SSHKey struct {
	ID          int       // Auto-increment ID
	UserID      string    // Owner user ID
	Fingerprint string    // SHA-256 fingerprint of the key
	PublicKey   string    // Full SSH public key string
	Comment     string    // Comment from the key (optional)
	AddedAt     time.Time // When the key was added
}

// Team represents a group of users sharing resources.
type Team struct {
	ID          string    // Unique identifier
	Name        string    // Team name
	Description string    // Optional description
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// TeamMember represents a user's membership in a team.
type TeamMember struct {
	TeamID    string    // Team ID
	UserID    string    // User ID
	Role      Role      // Role within the team (admin or user)
	JoinedAt  time.Time // When the user joined
}

// AuditEntry represents a recorded user action for compliance and debugging.
type AuditEntry struct {
	ID          int64     // Auto-increment ID
	UserID      string    // Who performed the action (empty for system)
	Action      string    // What was done (e.g., "sandbox.create", "user.add")
	Resource    string    // Target resource (e.g., "sandbox:1001", "user:alice")
	Detail      string    // Additional detail (JSON or free text)
	Timestamp   time.Time // When the action occurred
}

// ResourceQuota represents resource limits for a user or team.
type ResourceQuota struct {
	ID            int64  // Auto-increment ID
	ScopeType     string // "user" or "team"
	ScopeID       string // User ID or Team ID
	MaxSandboxes  int    // Maximum concurrent sandboxes (0 = unlimited)
	MaxCPU        int    // Maximum CPU cores (0 = unlimited)
	MaxRAMMB      int    // Maximum RAM in MB (0 = unlimited)
}
