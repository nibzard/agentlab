package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

var (
	ErrSandboxSnapshotActive            = errors.New("sandbox must be stopped for snapshots")
	ErrSandboxSnapshotWorkspaceAttached = errors.New("sandbox workspace attached")
)

// SandboxSnapshotOptions controls snapshot safety gates.
type SandboxSnapshotOptions struct {
	Force bool
}

// SnapshotCreate saves a named snapshot for the sandbox VM.
func (m *SandboxManager) SnapshotCreate(ctx context.Context, vmid int, name string, opts SandboxSnapshotOptions) (proxmox.Snapshot, error) {
	if m == nil || m.store == nil {
		return proxmox.Snapshot{}, errors.New("sandbox manager not configured")
	}
	if vmid <= 0 {
		return proxmox.Snapshot{}, errors.New("vmid must be positive")
	}
	if m.backend == nil {
		return proxmox.Snapshot{}, errors.New("proxmox backend unavailable")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return proxmox.Snapshot{}, errors.New("snapshot name is required")
	}

	sandbox, err := m.store.GetSandbox(ctx, vmid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return proxmox.Snapshot{}, ErrSandboxNotFound
		}
		return proxmox.Snapshot{}, fmt.Errorf("load sandbox %d: %w", vmid, err)
	}
	if sandbox.State == models.SandboxDestroyed {
		return proxmox.Snapshot{}, ErrSandboxNotFound
	}
	switch sandbox.State {
	case models.SandboxRequested, models.SandboxProvisioning, models.SandboxBooting:
		return proxmox.Snapshot{}, fmt.Errorf("%w: %s -> snapshot", ErrInvalidTransition, sandbox.State)
	}

	status := proxmox.StatusUnknown
	if current, statusErr := m.backend.Status(ctx, proxmox.VMID(vmid)); statusErr == nil {
		status = current
	}
	if !opts.Force {
		if sandbox.State != models.SandboxStopped || status == proxmox.StatusRunning {
			return proxmox.Snapshot{}, ErrSandboxSnapshotActive
		}
		if sandbox.WorkspaceID != nil && strings.TrimSpace(*sandbox.WorkspaceID) != "" {
			return proxmox.Snapshot{}, ErrSandboxSnapshotWorkspaceAttached
		}
	}

	if err := m.backend.SnapshotCreate(ctx, proxmox.VMID(vmid), name); err != nil {
		if errors.Is(err, proxmox.ErrVMNotFound) {
			return proxmox.Snapshot{}, ErrSandboxNotFound
		}
		return proxmox.Snapshot{}, fmt.Errorf("create snapshot %s: %w", name, err)
	}
	return proxmox.Snapshot{
		Name:      name,
		CreatedAt: m.now().UTC(),
	}, nil
}

// SnapshotRestore rolls the sandbox VM back to a named snapshot.
func (m *SandboxManager) SnapshotRestore(ctx context.Context, vmid int, name string, opts SandboxSnapshotOptions) (proxmox.Snapshot, error) {
	if m == nil || m.store == nil {
		return proxmox.Snapshot{}, errors.New("sandbox manager not configured")
	}
	if vmid <= 0 {
		return proxmox.Snapshot{}, errors.New("vmid must be positive")
	}
	if m.backend == nil {
		return proxmox.Snapshot{}, errors.New("proxmox backend unavailable")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return proxmox.Snapshot{}, errors.New("snapshot name is required")
	}

	sandbox, err := m.store.GetSandbox(ctx, vmid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return proxmox.Snapshot{}, ErrSandboxNotFound
		}
		return proxmox.Snapshot{}, fmt.Errorf("load sandbox %d: %w", vmid, err)
	}
	if sandbox.State == models.SandboxDestroyed {
		return proxmox.Snapshot{}, ErrSandboxNotFound
	}
	switch sandbox.State {
	case models.SandboxRequested, models.SandboxProvisioning, models.SandboxBooting:
		return proxmox.Snapshot{}, fmt.Errorf("%w: %s -> snapshot", ErrInvalidTransition, sandbox.State)
	}

	status := proxmox.StatusUnknown
	if current, statusErr := m.backend.Status(ctx, proxmox.VMID(vmid)); statusErr == nil {
		status = current
	}
	if !opts.Force {
		if sandbox.State != models.SandboxStopped || status == proxmox.StatusRunning {
			return proxmox.Snapshot{}, ErrSandboxSnapshotActive
		}
		if sandbox.WorkspaceID != nil && strings.TrimSpace(*sandbox.WorkspaceID) != "" {
			return proxmox.Snapshot{}, ErrSandboxSnapshotWorkspaceAttached
		}
	}

	if err := m.backend.SnapshotRollback(ctx, proxmox.VMID(vmid), name); err != nil {
		if isSnapshotMissing(err) {
			return proxmox.Snapshot{}, SnapshotMissingError{Name: name, Err: err}
		}
		if errors.Is(err, proxmox.ErrVMNotFound) {
			return proxmox.Snapshot{}, ErrSandboxNotFound
		}
		return proxmox.Snapshot{}, fmt.Errorf("rollback snapshot %s: %w", name, err)
	}

	// Proxmox rollback may stop the VM even when the DB state still says RUNNING.
	// Reconcile state and clear stale IP data to avoid surfacing a non-reachable target.
	status = proxmox.StatusUnknown
	if current, statusErr := m.backend.Status(ctx, proxmox.VMID(vmid)); statusErr == nil {
		status = current
	}
	if status != proxmox.StatusRunning {
		if sandbox.State != models.SandboxStopped {
			if err := m.Transition(ctx, vmid, models.SandboxStopped); err != nil {
				return proxmox.Snapshot{}, err
			}
		}
		if err := m.store.UpdateSandboxIP(ctx, vmid, ""); err != nil {
			return proxmox.Snapshot{}, err
		}
	} else {
		ipCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ip, ipErr := m.backend.GuestIP(ipCtx, proxmox.VMID(vmid))
		cancel()
		if ipErr == nil && strings.TrimSpace(ip) != "" {
			if err := m.store.UpdateSandboxIP(ctx, vmid, strings.TrimSpace(ip)); err != nil {
				return proxmox.Snapshot{}, err
			}
		}
	}

	return proxmox.Snapshot{Name: name}, nil
}

// SnapshotList returns named snapshots for a sandbox VM.
func (m *SandboxManager) SnapshotList(ctx context.Context, vmid int) ([]proxmox.Snapshot, error) {
	if m == nil || m.store == nil {
		return nil, errors.New("sandbox manager not configured")
	}
	if vmid <= 0 {
		return nil, errors.New("vmid must be positive")
	}
	if m.backend == nil {
		return nil, errors.New("proxmox backend unavailable")
	}
	sandbox, err := m.store.GetSandbox(ctx, vmid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSandboxNotFound
		}
		return nil, fmt.Errorf("load sandbox %d: %w", vmid, err)
	}
	if sandbox.State == models.SandboxDestroyed {
		return nil, ErrSandboxNotFound
	}

	snapshots, err := m.backend.SnapshotList(ctx, proxmox.VMID(vmid))
	if err != nil {
		if errors.Is(err, proxmox.ErrVMNotFound) {
			return nil, ErrSandboxNotFound
		}
		return nil, fmt.Errorf("list snapshots: %w", err)
	}

	filtered := make([]proxmox.Snapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		name := strings.TrimSpace(snapshot.Name)
		if name == "" || strings.EqualFold(name, "current") {
			continue
		}
		snapshot.Name = name
		filtered = append(filtered, snapshot)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return strings.ToLower(filtered[i].Name) < strings.ToLower(filtered[j].Name)
	})
	return filtered, nil
}
