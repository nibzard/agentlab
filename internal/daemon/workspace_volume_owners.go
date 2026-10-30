package daemon

import (
	"context"
	"fmt"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/proxmox"
)

func (m *WorkspaceManager) workspaceVolumeOwner(ctx context.Context, workspaceID string) (db.WorkspaceVolumeOwner, error) {
	occupied := map[int]bool{}
	if backend, ok := m.backend.(proxmox.VolumeOwnerInventory); ok {
		ids, err := backend.VolumeOwnerVMIDs(ctx)
		if err != nil {
			return db.WorkspaceVolumeOwner{}, fmt.Errorf("inspect cluster storage owners: %w", err)
		}
		for _, id := range ids {
			occupied[int(id)] = true
		}
	} else {
		vms, err := m.backend.ListVMs(ctx)
		if err != nil {
			return db.WorkspaceVolumeOwner{}, err
		}
		for _, vm := range vms {
			occupied[int(vm.VMID)] = true
		}
	}
	owner, err := m.store.ReserveWorkspaceVolumeOwner(ctx, workspaceID, occupied)
	if err != nil {
		return owner, err
	}
	if occupied[owner.VMID] {
		return owner, fmt.Errorf("%w: VM ID %d now belongs to an external guest; preserve its data", db.ErrWorkspaceVolumeOwner, owner.VMID)
	}
	return owner, nil
}
