package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentlab/agentlab/internal/proxmox"
	"github.com/agentlab/agentlab/internal/sandbox"
)

// sandboxBackendAdapter wraps a sandbox.Backend to implement proxmox.Backend.
//
// This allows Docker and Libvirt backends (which implement sandbox.Backend) to be
// used anywhere the daemon expects a proxmox.Backend — in SandboxManager,
// WorkspaceManager, JobOrchestrator, and ControlAPI.
//
// Operations that are specific to Proxmox (volume management, VM config queries)
// return a descriptive error so callers can handle the unsupported case gracefully.
type sandboxBackendAdapter struct {
	inner sandbox.Backend
}

var _ proxmox.Backend = (*sandboxBackendAdapter)(nil)

// newSandboxBackendAdapter creates a proxmox.Backend adapter around a sandbox.Backend.
func newSandboxBackendAdapter(b sandbox.Backend) proxmox.Backend {
	return &sandboxBackendAdapter{inner: b}
}

// errUnsupportedBackend is returned for Proxmox-only operations on non-Proxmox backends.
type errUnsupportedBackend struct {
	op string
}

func (e errUnsupportedBackend) Error() string {
	return fmt.Sprintf("operation %q not supported by %s backend", e.op, "non-proxmox")
}

func (b *sandboxBackendAdapter) Clone(ctx context.Context, _ proxmox.VMID, target proxmox.VMID, name string) error {
	cfg := sandbox.CreateConfig{
		ID:   int(target),
		Name: name,
	}
	return b.inner.Create(ctx, cfg)
}

func (b *sandboxBackendAdapter) Configure(_ context.Context, _ proxmox.VMID, _ proxmox.VMConfig) error {
	// Docker/Libvirt handle configuration at create time.
	return nil
}

func (b *sandboxBackendAdapter) Start(ctx context.Context, vmid proxmox.VMID) error {
	err := b.inner.Start(ctx, int(vmid))
	return b.mapNotFound(err)
}

func (b *sandboxBackendAdapter) Stop(ctx context.Context, vmid proxmox.VMID) error {
	err := b.inner.Stop(ctx, int(vmid))
	return b.mapNotFound(err)
}

func (b *sandboxBackendAdapter) Suspend(ctx context.Context, vmid proxmox.VMID) error {
	err := b.inner.Suspend(ctx, int(vmid))
	return b.mapNotFound(err)
}

func (b *sandboxBackendAdapter) Resume(ctx context.Context, vmid proxmox.VMID) error {
	err := b.inner.Resume(ctx, int(vmid))
	return b.mapNotFound(err)
}

func (b *sandboxBackendAdapter) Destroy(ctx context.Context, vmid proxmox.VMID) error {
	err := b.inner.Destroy(ctx, int(vmid))
	return b.mapNotFound(err)
}

func (b *sandboxBackendAdapter) SnapshotCreate(ctx context.Context, vmid proxmox.VMID, name string) error {
	err := b.inner.SnapshotCreate(ctx, int(vmid), name)
	return b.mapNotFound(err)
}

func (b *sandboxBackendAdapter) SnapshotRollback(ctx context.Context, vmid proxmox.VMID, name string) error {
	err := b.inner.SnapshotRollback(ctx, int(vmid), name)
	return b.mapNotFound(err)
}

func (b *sandboxBackendAdapter) SnapshotDelete(ctx context.Context, vmid proxmox.VMID, name string) error {
	err := b.inner.SnapshotDelete(ctx, int(vmid), name)
	return b.mapNotFound(err)
}

func (b *sandboxBackendAdapter) SnapshotList(ctx context.Context, vmid proxmox.VMID) ([]proxmox.Snapshot, error) {
	snaps, err := b.inner.SnapshotList(ctx, int(vmid))
	if err != nil {
		return nil, b.mapNotFound(err)
	}
	result := make([]proxmox.Snapshot, len(snaps))
	for i, s := range snaps {
		result[i] = proxmox.Snapshot{
			Name:        s.Name,
			Description: s.Description,
			CreatedAt:   s.CreatedAt,
		}
	}
	return result, nil
}

func (b *sandboxBackendAdapter) Status(ctx context.Context, vmid proxmox.VMID) (proxmox.Status, error) {
	s, err := b.inner.Status(ctx, int(vmid))
	if err != nil {
		return proxmox.StatusUnknown, b.mapNotFound(err)
	}
	switch s {
	case sandbox.StatusRunning:
		return proxmox.StatusRunning, nil
	case sandbox.StatusStopped:
		return proxmox.StatusStopped, nil
	default:
		return proxmox.StatusUnknown, nil
	}
}

func (b *sandboxBackendAdapter) ListVMs(ctx context.Context) ([]proxmox.VMSummary, error) {
	containers, err := b.inner.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]proxmox.VMSummary, len(containers))
	for i, c := range containers {
		var status proxmox.Status
		switch c.Status {
		case sandbox.StatusRunning:
			status = proxmox.StatusRunning
		case sandbox.StatusStopped:
			status = proxmox.StatusStopped
		default:
			status = proxmox.StatusUnknown
		}
		result[i] = proxmox.VMSummary{
			VMID:   proxmox.VMID(c.ID),
			Name:   c.Name,
			Status: status,
		}
	}
	return result, nil
}

func (b *sandboxBackendAdapter) CurrentStats(ctx context.Context, vmid proxmox.VMID) (proxmox.VMStats, error) {
	stats, err := b.inner.CurrentStats(ctx, int(vmid))
	if err != nil {
		return proxmox.VMStats{}, err
	}
	return proxmox.VMStats{CPUUsage: stats.CPUUsage}, nil
}

func (b *sandboxBackendAdapter) GuestIP(ctx context.Context, vmid proxmox.VMID) (string, error) {
	ip, err := b.inner.GuestIP(ctx, int(vmid))
	if err != nil {
		return "", b.mapNotFound(err)
	}
	return ip, nil
}

func (b *sandboxBackendAdapter) VMConfig(_ context.Context, _ proxmox.VMID) (map[string]string, error) {
	return nil, errUnsupportedBackend{op: "vm_config"}
}

func (b *sandboxBackendAdapter) CreateVolume(_ context.Context, _, _ string, _ int) (string, error) {
	return "", errUnsupportedBackend{op: "create_volume"}
}

func (b *sandboxBackendAdapter) AttachVolume(_ context.Context, _ proxmox.VMID, _, _ string) error {
	return errUnsupportedBackend{op: "attach_volume"}
}

func (b *sandboxBackendAdapter) DetachVolume(_ context.Context, _ proxmox.VMID, _ string) error {
	return errUnsupportedBackend{op: "detach_volume"}
}

func (b *sandboxBackendAdapter) DeleteVolume(_ context.Context, _ string) error {
	return errUnsupportedBackend{op: "delete_volume"}
}

func (b *sandboxBackendAdapter) VolumeInfo(_ context.Context, _ string) (proxmox.VolumeInfo, error) {
	return proxmox.VolumeInfo{}, errUnsupportedBackend{op: "volume_info"}
}

func (b *sandboxBackendAdapter) VolumeSnapshotCreate(_ context.Context, _, _ string) error {
	return errUnsupportedBackend{op: "volume_snapshot_create"}
}

func (b *sandboxBackendAdapter) VolumeSnapshotRestore(_ context.Context, _, _ string) error {
	return errUnsupportedBackend{op: "volume_snapshot_restore"}
}

func (b *sandboxBackendAdapter) VolumeSnapshotDelete(_ context.Context, _, _ string) error {
	return errUnsupportedBackend{op: "volume_snapshot_delete"}
}

func (b *sandboxBackendAdapter) VolumeClone(_ context.Context, _, _ string) error {
	return errUnsupportedBackend{op: "volume_clone"}
}

func (b *sandboxBackendAdapter) VolumeCloneFromSnapshot(_ context.Context, _, _, _ string) error {
	return errUnsupportedBackend{op: "volume_clone_from_snapshot"}
}

func (b *sandboxBackendAdapter) ValidateTemplate(ctx context.Context, template proxmox.VMID) error {
	// For Docker/Libvirt backends, ValidateTemplate on sandbox.Backend
	// accepts a string (image name or template name). Convert VMID to
	// string for the delegate call.
	return b.inner.ValidateTemplate(ctx, fmt.Sprintf("%d", int(template)))
}

// mapNotFound translates sandbox.ErrContainerNotFound to proxmox.ErrVMNotFound.
func (b *sandboxBackendAdapter) mapNotFound(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sandbox.ErrContainerNotFound) {
		return fmt.Errorf("%w: %v", proxmox.ErrVMNotFound, err)
	}
	return err
}
