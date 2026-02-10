// Package proxmox provides a backend abstraction for interacting with Proxmox VE.
//
// ABOUTME: This package defines the Backend interface and common types for VM management,
// including two implementations: APIBackend (using Proxmox REST API) and ShellBackend
// (using qm/pvesh CLI commands).
//
// ABOUTME: The package supports VM lifecycle operations (clone, configure, start, stop,
// destroy), status queries, guest IP discovery, and volume management for workspace storage.
//
// ABOUTME: Backends are pluggable and can be selected at runtime based on configuration.
// The API backend is recommended for production due to better reliability and error handling.
package proxmox

import (
	"context"
	"time"
)

// VMID represents a Proxmox virtual machine identifier.
type VMID int

// Status represents the runtime state of a VM.
type Status string

const (
	// StatusUnknown indicates the VM state could not be determined.
	StatusUnknown Status = "unknown"
	// StatusRunning indicates the VM is currently running.
	StatusRunning Status = "running"
	// StatusStopped indicates the VM is stopped.
	StatusStopped Status = "stopped"
)

// VMStats contains runtime statistics for a VM.
type VMStats struct {
	CPUUsage float64 // Fractional CPU usage from status/current (0.0-1.0+).
}

// Snapshot represents a VM snapshot returned by Proxmox.
// ABOUTME: CreatedAt is zero when the backend cannot provide a timestamp.
type Snapshot struct {
	Name        string
	Description string
	CreatedAt   time.Time
}

// VolumeInfo contains metadata about a storage volume.
type VolumeInfo struct {
	VolumeID string // Proxmox volume identifier (e.g., "local-zfs:vm-0-disk-0")
	Storage  string // Storage name (e.g., "local-zfs")
	Path     string // Resolved filesystem path (if available)
}

// VMConfig contains configuration parameters for a VM.
type VMConfig struct {
	Name          string // VM name
	Cores         int    // Number of CPU cores
	MemoryMB      int    // Memory in megabytes
	Bridge        string // Network bridge (e.g., "vmbr1")
	NetModel      string // Network device model (e.g., "virtio")
	Firewall      *bool  // Whether to enable Proxmox firewall for the NIC (nil = leave unchanged)
	FirewallGroup string // Firewall group name to apply (empty = unset)
	CloudInit     string // Cloud-init snippet path
	CPUPinning    string // CPU pinning configuration
}

// Backend defines the interface for Proxmox operations.
// ABOUTME: Both APIBackend and ShellBackend implement this interface, allowing
// runtime backend selection and easy testing with mock implementations.
type Backend interface {
	// Clone creates a new VM by cloning a template.
	// ABOUTME: The target VMID must be unique and not already exist in Proxmox.
	Clone(ctx context.Context, template VMID, target VMID, name string) error

	// Configure updates VM configuration parameters.
	// ABOUTME: Only non-zero/non-empty fields are applied to the VM.
	Configure(ctx context.Context, vmid VMID, cfg VMConfig) error

	// Start starts a stopped VM.
	Start(ctx context.Context, vmid VMID) error

	// Stop stops a running VM gracefully.
	// ABOUTME: Returns ErrVMNotFound if the VM does not exist.
	Stop(ctx context.Context, vmid VMID) error

	// Suspend pauses a running VM.
	// ABOUTME: Uses Proxmox suspend to pause execution.
	Suspend(ctx context.Context, vmid VMID) error

	// Resume resumes a suspended VM.
	// ABOUTME: Uses Proxmox resume to continue execution.
	Resume(ctx context.Context, vmid VMID) error

	// Destroy permanently deletes a VM and its disks.
	// ABOUTME: This operation is irreversible. Returns ErrVMNotFound if the VM does not exist.
	Destroy(ctx context.Context, vmid VMID) error

	// SnapshotCreate creates a disk-only snapshot of the VM with the given name.
	// ABOUTME: Snapshots do not include VM memory state (no vmstate).
	SnapshotCreate(ctx context.Context, vmid VMID, name string) error

	// SnapshotRollback reverts the VM to the named snapshot.
	// ABOUTME: Callers should stop the VM before rollback; vmstate snapshots are not used.
	SnapshotRollback(ctx context.Context, vmid VMID, name string) error

	// SnapshotDelete removes the named snapshot from the VM.
	SnapshotDelete(ctx context.Context, vmid VMID, name string) error

	// SnapshotList lists snapshots for a VM.
	// ABOUTME: The snapshot list may include backend-specific metadata like timestamps.
	SnapshotList(ctx context.Context, vmid VMID) ([]Snapshot, error)

	// Status retrieves the current runtime status of a VM.
	// ABOUTME: Returns StatusRunning, StatusStopped, or StatusUnknown.
	Status(ctx context.Context, vmid VMID) (Status, error)

	// CurrentStats retrieves runtime stats for a VM.
	// ABOUTME: CPUUsage is a fractional value from Proxmox status/current.
	CurrentStats(ctx context.Context, vmid VMID) (VMStats, error)

	// GuestIP retrieves the IP address of the VM's guest agent.
	// ABOUTME: Falls back to DHCP lease lookup if guest agent is unavailable.
	// Returns ErrGuestIPNotFound if no IP can be determined.
	GuestIP(ctx context.Context, vmid VMID) (string, error)

	// VMConfig retrieves the raw configuration map for a VM.
	// ABOUTME: Returns ErrVMNotFound if the VM does not exist.
	VMConfig(ctx context.Context, vmid VMID) (map[string]string, error)

	// CreateVolume creates a new disk volume in the specified storage.
	// ABOUTME: Returns the volume ID (e.g., "local-zfs:vm-0-disk-0").
	CreateVolume(ctx context.Context, storage, name string, sizeGB int) (string, error)

	// AttachVolume attaches a volume to a VM at the specified slot.
	// ABOUTME: The slot is typically "scsi1", "virtio1", etc.
	AttachVolume(ctx context.Context, vmid VMID, volumeID, slot string) error

	// DetachVolume detaches a volume from a VM.
	// ABOUTME: The volume is not deleted, only detached from the VM.
	DetachVolume(ctx context.Context, vmid VMID, slot string) error

	// DeleteVolume permanently deletes a volume from storage.
	// ABOUTME: This operation is irreversible.
	DeleteVolume(ctx context.Context, volumeID string) error

	// VolumeInfo retrieves volume metadata and path.
	// ABOUTME: Returns ErrVolumeNotFound if the volume does not exist.
	VolumeInfo(ctx context.Context, volumeID string) (VolumeInfo, error)

	// VolumeSnapshotCreate creates a snapshot for a workspace volume.
	// ABOUTME: Callers should ensure the volume is detached before snapshotting for consistency.
	VolumeSnapshotCreate(ctx context.Context, volumeID, name string) error

	// VolumeSnapshotRestore restores a workspace volume to the named snapshot.
	// ABOUTME: This is destructive and should only be used when the volume is detached.
	VolumeSnapshotRestore(ctx context.Context, volumeID, name string) error

	// VolumeSnapshotDelete removes a snapshot from a workspace volume.
	VolumeSnapshotDelete(ctx context.Context, volumeID, name string) error

	// VolumeClone creates a new volume by cloning an existing workspace volume.
	// ABOUTME: Callers should ensure the source volume is detached before cloning for consistency.
	VolumeClone(ctx context.Context, sourceVolumeID, targetVolumeID string) error

	// VolumeCloneFromSnapshot creates a new volume by cloning a workspace volume snapshot.
	// ABOUTME: Callers should ensure the source volume is detached before cloning for consistency.
	VolumeCloneFromSnapshot(ctx context.Context, sourceVolumeID, snapshotName, targetVolumeID string) error

	// ValidateTemplate checks if a template VM is suitable for provisioning.
	// ABOUTME: Returns nil if the template exists and has qemu-guest-agent enabled.
	// Returns an error if the template is missing or misconfigured.
	ValidateTemplate(ctx context.Context, template VMID) error
}
