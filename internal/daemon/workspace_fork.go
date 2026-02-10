package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/agentlab/agentlab/internal/models"
)

// Fork creates a new workspace volume and record cloned from a source workspace.
func (m *WorkspaceManager) Fork(ctx context.Context, idOrName, newName, snapshotName string) (models.Workspace, error) {
	if m == nil || m.store == nil {
		return models.Workspace{}, errors.New("workspace manager unavailable")
	}
	if m.backend == nil {
		return models.Workspace{}, errors.New("workspace backend unavailable")
	}
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return models.Workspace{}, errors.New("workspace name is required")
	}
	snapshotName = strings.TrimSpace(snapshotName)

	source, err := m.Resolve(ctx, idOrName)
	if err != nil {
		return models.Workspace{}, err
	}
	if source.AttachedVM != nil && *source.AttachedVM > 0 {
		return models.Workspace{}, ErrWorkspaceForkAttached
	}
	if _, err := m.store.GetWorkspaceByName(ctx, newName); err == nil {
		return models.Workspace{}, ErrWorkspaceExists
	} else if !errors.Is(err, sql.ErrNoRows) {
		return models.Workspace{}, err
	}

	var snapshot models.WorkspaceSnapshot
	if snapshotName != "" {
		snapshot, err = m.store.GetWorkspaceSnapshot(ctx, source.ID, snapshotName)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return models.Workspace{}, ErrWorkspaceSnapshotNotFound
			}
			return models.Workspace{}, err
		}
	}

	owner, nonce, err := m.acquireForkLease(ctx, source)
	if err != nil {
		return models.Workspace{}, err
	}
	defer m.releaseForkLease(ctx, source.ID, owner, nonce)

	latest, err := m.store.GetWorkspace(ctx, source.ID)
	if err == nil && latest.AttachedVM != nil && *latest.AttachedVM > 0 {
		return models.Workspace{}, ErrWorkspaceForkAttached
	}

	newID, err := newWorkspaceID(m.rand)
	if err != nil {
		return models.Workspace{}, err
	}

	volid, err := m.reserveCloneVolumeID(ctx, source.Storage, newID, source.SizeGB)
	if err != nil {
		return models.Workspace{}, err
	}

	if snapshotName == "" {
		err = m.backend.VolumeClone(ctx, source.VolumeID, volid)
	} else {
		snapshotRef := strings.TrimSpace(snapshot.BackendRef)
		if snapshotRef == "" {
			snapshotRef = snapshot.Name
		}
		err = m.backend.VolumeCloneFromSnapshot(ctx, source.VolumeID, snapshotRef, volid)
	}
	if err != nil {
		_ = m.backend.DeleteVolume(ctx, volid)
		return models.Workspace{}, err
	}

	now := m.now().UTC()
	workspace := models.Workspace{
		ID:          newID,
		Name:        newName,
		Storage:     source.Storage,
		VolumeID:    volid,
		SizeGB:      source.SizeGB,
		CreatedAt:   now,
		LastUpdated: now,
	}
	if err := m.store.CreateWorkspace(ctx, workspace); err != nil {
		if isUniqueConstraint(err) {
			_ = m.backend.DeleteVolume(ctx, volid)
			return models.Workspace{}, ErrWorkspaceExists
		}
		_ = m.backend.DeleteVolume(ctx, volid)
		return models.Workspace{}, err
	}
	return workspace, nil
}

func (m *WorkspaceManager) reserveCloneVolumeID(ctx context.Context, storage, name string, sizeGB int) (string, error) {
	volid, err := m.backend.CreateVolume(ctx, storage, name, sizeGB)
	if err != nil {
		return "", err
	}
	if err := m.backend.DeleteVolume(ctx, volid); err != nil {
		if m.logger != nil {
			m.logger.Printf("workspace fork: failed to release placeholder volume %s: %v", volid, err)
		}
		return "", err
	}
	return volid, nil
}

func (m *WorkspaceManager) acquireForkLease(ctx context.Context, workspace models.Workspace) (string, string, error) {
	if m == nil || m.store == nil {
		return "", "", errors.New("workspace lease store unavailable")
	}
	if strings.TrimSpace(workspace.ID) == "" {
		return "", "", errors.New("workspace id is required")
	}
	owner := fmt.Sprintf("fork:%s:%d", workspace.ID, m.now().UTC().UnixNano())
	nonce, err := newWorkspaceLeaseNonce(m.rand)
	if err != nil {
		return "", "", err
	}
	expiresAt := m.now().UTC().Add(workspaceLeaseDefaultTTL)
	acquired, err := m.store.TryAcquireWorkspaceLease(ctx, workspace.ID, owner, nonce, expiresAt)
	if err != nil {
		return "", "", err
	}
	if !acquired {
		return "", "", ErrWorkspaceLeaseHeld
	}
	return owner, nonce, nil
}

func (m *WorkspaceManager) releaseForkLease(ctx context.Context, workspaceID, owner, nonce string) {
	if m == nil || m.store == nil {
		return
	}
	workspaceID = strings.TrimSpace(workspaceID)
	owner = strings.TrimSpace(owner)
	nonce = strings.TrimSpace(nonce)
	if workspaceID == "" || owner == "" || nonce == "" {
		return
	}
	_, _ = m.store.ReleaseWorkspaceLease(ctx, workspaceID, owner, nonce)
}
