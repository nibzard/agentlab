// Package models provides data structures and constants for AgentLab.
//
// This package contains the core domain models used throughout AgentLab:
//   - Sandbox: Represents a Proxmox VM and its lifecycle state
//   - Job: Represents a job to be executed in a sandbox
//   - Profile: Configuration template for sandbox provisioning
//   - Workspace: Persistent storage volume that can be attached to sandboxes
//
// All models are designed for database persistence and JSON serialization.
package models

import "time"

// SandboxState represents the current state of a sandbox in its lifecycle.
//
// The state machine enforces valid transitions:
//
//	REQUESTED → PROVISIONING → BOOTING → READY → RUNNING → (COMPLETED|FAILED|TIMEOUT) → STOPPED → DESTROYED
//
// STOPPED sandboxes can be restarted, transitioning back through BOOTING/READY/RUNNING.
//
// States can also transition to TIMEOUT at any point before COMPLETED/FAILED/STOPPED,
// and to DESTROYED from most states (via force destroy).
type SandboxState string

const (
	// SandboxRequested is the initial state when a sandbox is requested but not yet created.
	SandboxRequested SandboxState = "REQUESTED"
	// SandboxProvisioning indicates the VM is being created in Proxmox.
	SandboxProvisioning SandboxState = "PROVISIONING"
	// SandboxBooting indicates the VM has been created and is booting.
	SandboxBooting SandboxState = "BOOTING"
	// SandboxReady indicates the VM is running and ready to accept jobs.
	SandboxReady SandboxState = "READY"
	// SandboxRunning indicates a job is actively executing in the sandbox.
	SandboxRunning SandboxState = "RUNNING"
	// SandboxCompleted indicates the job finished successfully.
	SandboxCompleted SandboxState = "COMPLETED"
	// SandboxFailed indicates the job failed.
	SandboxFailed SandboxState = "FAILED"
	// SandboxTimeout indicates the sandbox lease expired.
	SandboxTimeout SandboxState = "TIMEOUT"
	// SandboxStopped indicates the VM has been stopped but not destroyed.
	SandboxStopped SandboxState = "STOPPED"
	// SandboxDestroyed indicates the VM has been destroyed and resources released.
	SandboxDestroyed SandboxState = "DESTROYED"
)

// Sandbox represents a Proxmox VM managed by AgentLab.
//
// Fields:
//   - VMID: The Proxmox VM ID (unique identifier)
//   - Name: Human-readable name for the sandbox
//   - Profile: Name of the profile used to create the sandbox
//   - State: Current state in the sandbox lifecycle
//   - IP: IP address of the VM in the agent subnet
//   - WorkspaceID: ID of attached workspace volume (optional)
//   - Keepalive: Whether the sandbox lease auto-renews
//   - LeaseExpires: When the sandbox lease expires (zero if no TTL)
//   - LastUsedAt: When the sandbox was last touched by a user interaction
//   - CreatedAt: When the sandbox was first created
//   - LastUpdatedAt: When the sandbox state was last updated
type Sandbox struct {
	VMID          int
	Name          string
	Profile       string
	State         SandboxState
	IP            string
	WorkspaceID   *string
	Keepalive     bool
	LeaseExpires  time.Time
	LastUsedAt    time.Time
	CreatedAt     time.Time
	LastUpdatedAt time.Time
}

// JobStatus represents the current status of a job in its lifecycle.
//
// Job state transitions:
//
//	QUEUED → RUNNING → (COMPLETED|FAILED|TIMEOUT)
type JobStatus string

const (
	// JobQueued is the initial state when a job is created and waiting to run.
	JobQueued JobStatus = "QUEUED"
	// JobRunning indicates the job is actively executing in a sandbox.
	JobRunning JobStatus = "RUNNING"
	// JobCompleted indicates the job finished successfully.
	JobCompleted JobStatus = "COMPLETED"
	// JobFailed indicates the job execution failed.
	JobFailed JobStatus = "FAILED"
	// JobTimeout indicates the job or sandbox lease expired.
	JobTimeout JobStatus = "TIMEOUT"
)

// Job represents a unit of work to be executed in a sandbox.
//
// A job specifies:
//   - A git repository to clone
//   - A git ref (branch, tag, or commit) to checkout
//   - A profile for sandbox provisioning
//   - A task/command to execute
//   - Optional TTL and keepalive settings
//   - Optional workspace attachment
//
// Fields:
//   - ID: Unique job identifier (job_<hex>)
//   - RepoURL: Git repository URL
//   - Ref: Git reference (branch, tag, commit)
//   - Profile: Profile name for sandbox provisioning
//   - Task: Task/command to execute
//   - Mode: Execution mode (e.g., "dangerous")
//   - TTLMinutes: Time-to-live in minutes (0 for no expiry)
//   - Keepalive: Whether to auto-renew the lease
//   - WorkspaceID: ID of attached workspace volume (optional)
//   - Status: Current job status
//   - SandboxVMID: VM ID of the assigned sandbox (set when RUNNING)
//   - CreatedAt: When the job was created
//   - UpdatedAt: When the job was last updated
//   - ResultJSON: JSON-encoded result (set when COMPLETED)
type Job struct {
	ID          string
	RepoURL     string
	Ref         string
	Profile     string
	Task        string
	Mode        string
	TTLMinutes  int
	Keepalive   bool
	WorkspaceID *string
	Status      JobStatus
	SandboxVMID *int
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ResultJSON  string
}

// Profile defines the configuration template for sandbox provisioning.
//
// Profiles are loaded from YAML files in /etc/agentlab/profiles/ and define:
//   - Which VM template to use
//   - Resource allocation (CPU, memory, disk)
//   - Behavior defaults (TTL, keepalive)
//   - Inner sandbox configuration
//
// Fields:
//   - Name: Profile identifier (matches filename)
//   - TemplateVM: Proxmox VM ID of the template to clone
//   - UpdatedAt: When the profile was last loaded from disk
//   - RawYAML: The raw YAML configuration for debugging
type Profile struct {
	Name       string
	TemplateVM int
	UpdatedAt  time.Time
	RawYAML    string
}

// Workspace represents a persistent storage volume that can be attached to sandboxes.
//
// Workspaces provide durable storage that survives sandbox destruction. A workspace
// can be attached to one sandbox at a time, and can be reattached to different
// sandboxes over time.
//
// Fields:
//   - ID: Unique workspace identifier (matches name)
//   - Name: Human-readable name (also the ID)
//   - Storage: Proxmox storage backend name
//   - VolumeID: Proxmox volume identifier
//   - SizeGB: Size in gigabytes
//   - AttachedVM: VM ID of currently attached sandbox (nil if detached)
//   - LeaseOwner: Current lease owner identifier (empty if unleased)
//   - LeaseNonce: Lease CAS nonce for renew/release operations
//   - LeaseExpires: Lease expiration timestamp (zero if no lease)
//   - CreatedAt: When the workspace was created
//   - LastUpdated: When the workspace was last attached/detached
type Workspace struct {
	ID           string
	Name         string
	Storage      string
	VolumeID     string
	SizeGB       int
	AttachedVM   *int
	LeaseOwner   string
	LeaseNonce   string
	LeaseExpires time.Time
	CreatedAt    time.Time
	LastUpdated  time.Time
}

// Session represents a persisted workspace-backed session.
//
// Sessions bind a workspace to a current sandbox and profile defaults, allowing
// users to resume work by reprovisioning a fresh sandbox against the same
// workspace.
//
// Fields:
//   - ID: Unique session identifier
//   - Name: Human-readable session name
//   - WorkspaceID: Workspace attached to the session
//   - CurrentVMID: Active sandbox VM ID (nil if stopped)
//   - Profile: Profile name for resume defaults
//   - Branch: Optional branch label for session tracking
//   - CreatedAt: When the session was created
//   - UpdatedAt: When the session was last updated
//   - MetaJSON: Optional metadata (JSON-encoded)
type Session struct {
	ID          string
	Name        string
	WorkspaceID string
	CurrentVMID *int
	Profile     string
	Branch      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	MetaJSON    string
}
