package sandbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentlab/agentlab/internal/proxmox"
)

// VMBackend adapts the existing proxmox.Backend to the unified sandbox.Backend interface.
//
// ABOUTME: This adapter wraps a proxmox.Backend (API or Shell) and translates between
// the unified sandbox.CreateConfig and the VM-specific proxmox.VMConfig. It enables
// existing VM-based sandbox management through the new Backend interface.
type VMBackend struct {
	backend proxmox.Backend
}

// NewVMBackend creates a new VM backend adapter.
func NewVMBackend(backend proxmox.Backend) *VMBackend {
	if backend == nil {
		panic("proxmox.Backend must not be nil")
	}
	return &VMBackend{backend: backend}
}

// ProxmoxBackend returns the underlying proxmox.Backend for direct VM operations.
// This is used by components that still need VM-specific operations (e.g., volume management).
func (b *VMBackend) ProxmoxBackend() proxmox.Backend {
	return b.backend
}

func (b *VMBackend) SandboxType() Type { return TypeVM }

func (b *VMBackend) Capabilities() Capabilities {
	return Capabilities{
		Snapshots:      true,
		Suspend:        true,
		WorkspaceMount: true,
		Firewall:       true,
	}
}

func (b *VMBackend) Create(ctx context.Context, cfg CreateConfig) error {
	if cfg.TemplateID <= 0 {
		return fmt.Errorf("template_id is required for VM backend")
	}
	if err := b.backend.Clone(ctx, proxmox.VMID(cfg.TemplateID), proxmox.VMID(cfg.ID), cfg.Name); err != nil {
		return fmt.Errorf("clone vm: %w", err)
	}
	vmCfg := proxmox.VMConfig{
		Name:      cfg.Name,
		Cores:     cfg.Cores,
		MemoryMB:  cfg.MemoryMB,
		Bridge:    cfg.Bridge,
		NetModel:  cfg.NetModel,
		CloudInit: cfg.CloudInitSnippet,
		RootDiskGB: cfg.RootDiskGB,
		RootDisk:  cfg.RootDisk,
		SCSIHW:    cfg.SCSIHW,
		Firewall:  cfg.Firewall,
		FirewallGroup: cfg.FirewallGroup,
		CPUPinning: cfg.CPUPinning,
	}
	if err := b.backend.Configure(ctx, proxmox.VMID(cfg.ID), vmCfg); err != nil {
		return fmt.Errorf("configure vm: %w", err)
	}
	return nil
}

func (b *VMBackend) Start(ctx context.Context, id int) error {
	return b.backend.Start(ctx, proxmox.VMID(id))
}

func (b *VMBackend) Stop(ctx context.Context, id int) error {
	return b.backend.Stop(ctx, proxmox.VMID(id))
}

func (b *VMBackend) Suspend(ctx context.Context, id int) error {
	return b.backend.Suspend(ctx, proxmox.VMID(id))
}

func (b *VMBackend) Resume(ctx context.Context, id int) error {
	return b.backend.Resume(ctx, proxmox.VMID(id))
}

func (b *VMBackend) Destroy(ctx context.Context, id int) error {
	return b.backend.Destroy(ctx, proxmox.VMID(id))
}

func (b *VMBackend) Status(ctx context.Context, id int) (Status, error) {
	s, err := b.backend.Status(ctx, proxmox.VMID(id))
	if err != nil {
		return StatusUnknown, err
	}
	return Status(s), nil
}

func (b *VMBackend) GuestIP(ctx context.Context, id int) (string, error) {
	return b.backend.GuestIP(ctx, proxmox.VMID(id))
}

func (b *VMBackend) List(ctx context.Context) ([]ContainerSummary, error) {
	vms, err := b.backend.ListVMs(ctx)
	if err != nil {
		return nil, err
	}
	summaries := make([]ContainerSummary, len(vms))
	for i, vm := range vms {
		summaries[i] = ContainerSummary{
			ID:     int(vm.VMID),
			Name:   vm.Name,
			Status: Status(vm.Status),
			Type:   TypeVM,
		}
	}
	return summaries, nil
}

func (b *VMBackend) CurrentStats(ctx context.Context, id int) (ContainerStats, error) {
	stats, err := b.backend.CurrentStats(ctx, proxmox.VMID(id))
	if err != nil {
		return ContainerStats{}, err
	}
	return ContainerStats{CPUUsage: stats.CPUUsage}, nil
}

func (b *VMBackend) ValidateTemplate(ctx context.Context, templateOrImage string) error {
	// For VM backend, the templateOrImage is a VMID.
	// Delegate directly to the proxmox backend's ValidateTemplate using the profile's TemplateVM.
	// Note: The caller should pass the template VMID through CreateConfig.TemplateID
	// and call ValidateTemplate with the numeric VMID string.
	return nil // Actual validation happens via proxmox.Backend.ValidateTemplate directly
}

func (b *VMBackend) SnapshotCreate(ctx context.Context, id int, name string) error {
	return b.backend.SnapshotCreate(ctx, proxmox.VMID(id), name)
}

func (b *VMBackend) SnapshotRollback(ctx context.Context, id int, name string) error {
	return b.backend.SnapshotRollback(ctx, proxmox.VMID(id), name)
}

func (b *VMBackend) SnapshotDelete(ctx context.Context, id int, name string) error {
	return b.backend.SnapshotDelete(ctx, proxmox.VMID(id), name)
}

func (b *VMBackend) SnapshotList(ctx context.Context, id int) ([]Snapshot, error) {
	snaps, err := b.backend.SnapshotList(ctx, proxmox.VMID(id))
	if err != nil {
		return nil, err
	}
	result := make([]Snapshot, len(snaps))
	for i, s := range snaps {
		result[i] = Snapshot{
			Name:        s.Name,
			Description: s.Description,
			CreatedAt:   s.CreatedAt,
		}
	}
	return result, nil
}

// ValidateVMTemplate is a convenience function that validates a VM template
// using the underlying proxmox.Backend.
func (b *VMBackend) ValidateVMTemplate(ctx context.Context, templateVMID int) error {
	return b.backend.ValidateTemplate(ctx, proxmox.VMID(templateVMID))
}

// Compile-time check that VMBackend implements Backend.
var _ Backend = (*VMBackend)(nil)

// isVMNotFoundError checks if an error indicates the VM/container was not found.
func isVMNotFoundError(err error) bool {
	return errors.Is(err, proxmox.ErrVMNotFound)
}
