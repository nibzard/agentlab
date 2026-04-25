// Package sandbox provides a unified backend abstraction for AgentLab sandboxes.
//
// ABOUTME: This package defines the SandboxBackend interface and shared types for
// managing sandbox lifecycles across different backend types (VM, LXC container).
//
// ABOUTME: The SandboxBackend interface abstracts the differences between Proxmox
// QEMU VMs and LXC containers, allowing the daemon to use either backend
// interchangeably. Each backend implementation handles the specific API calls
// needed for its sandbox type.
package sandbox

import (
	"context"
	"time"
)

// Type represents the type of sandbox backend.
type Type string

const (
	// TypeVM represents a full QEMU virtual machine sandbox.
	TypeVM Type = "vm"
	// TypeLXC represents an LXC container sandbox.
	TypeLXC Type = "lxc"
)

// ParseType converts a string to a Type, returning TypeVM as default.
func ParseType(s string) Type {
	switch s {
	case "lxc", "container", "lxd":
		return TypeLXC
	case "vm", "qemu", "":
		return TypeVM
	default:
		return TypeVM
	}
}

// Status represents the runtime state of a sandbox.
type Status string

const (
	StatusUnknown Status = "unknown"
	StatusRunning Status = "running"
	StatusStopped Status = "stopped"
)

// ContainerStats contains runtime statistics for a sandbox.
type ContainerStats struct {
	CPUUsage float64 // Fractional CPU usage (0.0-1.0+)
}

// ContainerSummary contains basic inventory metadata for a sandbox.
type ContainerSummary struct {
	ID     int
	Name   string
	Status Status
	Type   Type
}

// Snapshot represents a point-in-time snapshot of a sandbox.
type Snapshot struct {
	Name        string
	Description string
	CreatedAt   time.Time
}

// Capabilities describes which operations a backend supports.
type Capabilities struct {
	Snapshots      bool // Whether the backend supports snapshots
	Suspend        bool // Whether the backend supports suspend/resume
	WorkspaceMount bool // Whether the backend supports workspace volume mounting
	Firewall       bool // Whether the backend supports network firewall rules
}

// CreateConfig holds the parameters for creating a new sandbox.
type CreateConfig struct {
	// ID is the Proxmox VM/CT ID to assign.
	ID int
	// Name is the hostname for the sandbox.
	Name string
	// Image is the container image for LXC (e.g., "ubuntu:22.04").
	// Ignored for VM backends.
	Image string
	// TemplateID is the template VM ID to clone (VM backends only).
	// Ignored for LXC backends.
	TemplateID int
	// Cores is the number of CPU cores.
	Cores int
	// MemoryMB is the memory allocation in megabytes.
	MemoryMB int
	// Bridge is the network bridge to attach (e.g., "vmbr1").
	Bridge string
	// SSHPublicKey is the public key to inject for root access.
	SSHPublicKey string
	// CloudInitSnippet is the path to a cloud-init snippet (VM only).
	CloudInitSnippet string
	// RootDiskGB is the root disk size in GB (VM only).
	RootDiskGB int
	// RootDisk is the root disk identifier (VM only, e.g., "scsi0").
	RootDisk string
	// NetModel is the network device model (VM only).
	NetModel string
	// SCSIHW is the SCSI controller model (VM only).
	SCSIHW string
	// Firewall enables NIC-level firewall (VM only).
	Firewall *bool
	// FirewallGroup is the firewall group name (VM only).
	FirewallGroup string
	// CPUPinning is the CPU pinning config (VM only).
	CPUPinning string
	// Password is the root password for LXC containers.
	// If empty, SSH key auth is used exclusively.
	Password string
}

// Backend defines the unified interface for sandbox lifecycle operations.
//
// ABOUTME: This interface abstracts VM and LXC container operations behind a
// common API. Implementations include VMBackend (wrapping proxmox.Backend)
// and LXCBackend (using Proxmox LXC API).
//
// ABOUTME: The interface supports sandbox creation, lifecycle management,
// status queries, and optional snapshot operations.
type Backend interface {
	// SandboxType returns the type of sandbox this backend manages.
	SandboxType() Type

	// Capabilities returns the set of supported operations.
	Capabilities() Capabilities

	// Create creates a new sandbox with the given configuration.
	// Returns an error if the ID is already in use.
	Create(ctx context.Context, cfg CreateConfig) error

	// Start starts a stopped sandbox.
	Start(ctx context.Context, id int) error

	// Stop stops a running sandbox.
	Stop(ctx context.Context, id int) error

	// Suspend pauses a running sandbox.
	// Returns an error if the backend does not support suspend.
	Suspend(ctx context.Context, id int) error

	// Resume resumes a suspended sandbox.
	// Returns an error if the backend does not support suspend.
	Resume(ctx context.Context, id int) error

	// Destroy permanently deletes a sandbox and its resources.
	Destroy(ctx context.Context, id int) error

	// Status retrieves the current runtime status of a sandbox.
	Status(ctx context.Context, id int) (Status, error)

	// GuestIP retrieves the IP address of the sandbox.
	GuestIP(ctx context.Context, id int) (string, error)

	// List returns the node's current sandbox inventory.
	List(ctx context.Context) ([]ContainerSummary, error)

	// CurrentStats retrieves runtime stats for a sandbox.
	CurrentStats(ctx context.Context, id int) (ContainerStats, error)

	// ValidateTemplate checks if a template is suitable for provisioning.
	// For LXC backends, this validates the image is available.
	// For VM backends, this validates the template VM exists.
	ValidateTemplate(ctx context.Context, templateOrImage string) error

	// SnapshotCreate creates a snapshot of the sandbox.
	// Returns ErrNotSupported if the backend does not support snapshots.
	SnapshotCreate(ctx context.Context, id int, name string) error

	// SnapshotRollback reverts the sandbox to a named snapshot.
	SnapshotRollback(ctx context.Context, id int, name string) error

	// SnapshotDelete removes a named snapshot.
	SnapshotDelete(ctx context.Context, id int, name string) error

	// SnapshotList lists snapshots for a sandbox.
	SnapshotList(ctx context.Context, id int) ([]Snapshot, error)
}

// ErrNotSupported indicates the backend does not support the requested operation.
type ErrNotSupported struct {
	Op string
}

func (e ErrNotSupported) Error() string {
	return "operation not supported: " + e.Op
}
