package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/hostidentity"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/agentlab/agentlab/internal/proxmox"
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
	releaseName, err := m.store.LockJobSubmission(ctx, "workspace-name", newName)
	if err != nil {
		return models.Workspace{}, err
	}
	defer releaseName()
	previous, previousErr := m.store.WorkspaceForkByName(ctx, source.ID, newName)
	if previousErr == nil {
		actor, err := requestOwnerContext(ctx)
		if err != nil {
			return models.Workspace{}, err
		}
		if actor != previous.Owner || previous.FromSnapshot != snapshotName {
			return models.Workspace{}, ErrWorkspaceExists
		}
		if _, supported := m.backend.(proxmox.WorkspaceCopyRecoveryBackend); supported || previous.State == "succeeded" {
			return m.resumeWorkspaceForkLocked(ctx, source, previous)
		}
		return models.Workspace{}, &workspaceForkError{Operation: previous, Cause: ErrWorkspaceExists}
	} else if !errors.Is(previousErr, sql.ErrNoRows) {
		return models.Workspace{}, previousErr
	}
	if source.AttachedVM != nil && *source.AttachedVM > 0 {
		return models.Workspace{}, ErrWorkspaceForkAttached
	}
	pending, err := m.store.WorkspaceAllocationPending(ctx, newName)
	if err != nil {
		return models.Workspace{}, err
	}
	if pending {
		previous, loadErr := m.store.PendingWorkspaceForkByName(ctx, source.ID, newName)
		if loadErr == nil {
			actor, ownerErr := requestOwnerContext(ctx)
			if ownerErr != nil {
				return models.Workspace{}, ownerErr
			}
			if actor == previous.Owner {
				return models.Workspace{}, &workspaceForkError{Operation: previous, Cause: ErrWorkspaceExists}
			}
		} else if !errors.Is(loadErr, sql.ErrNoRows) {
			return models.Workspace{}, loadErr
		}
		return models.Workspace{}, ErrWorkspaceExists
	}
	if capability, ok := m.backend.(interface{ WorkspaceStorageSupported() bool }); ok && !capability.WorkspaceStorageSupported() {
		return models.Workspace{}, &proxmox.WorkspaceStorageRequirement{}
	}
	copyBackend, ok := m.backend.(proxmox.WorkspaceCopyBackend)
	if !ok {
		return models.Workspace{}, proxmox.ErrStorageUnsupported
	}
	if _, err := m.store.GetWorkspaceByName(ctx, newName); err == nil {
		return models.Workspace{}, ErrWorkspaceExists
	} else if !errors.Is(err, sql.ErrNoRows) {
		return models.Workspace{}, err
	}

	requestedSnapshot := snapshotName
	var snapshot models.WorkspaceSnapshot
	if snapshotName != "" {
		snapshot, err = m.store.GetWorkspaceSnapshot(ctx, source.ID, snapshotName)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return models.Workspace{}, ErrWorkspaceSnapshotNotFound
			}
			return models.Workspace{}, err
		}
		if value := strings.TrimSpace(snapshot.BackendRef); value != "" {
			snapshotName = value
		}
	}

	owner, nonce, err := m.acquireForkLease(ctx, source)
	if err != nil {
		return models.Workspace{}, err
	}
	defer m.releaseForkLease(ctx, source.ID, owner, nonce)

	latest, err := m.store.GetWorkspace(ctx, source.ID)
	if err != nil {
		return models.Workspace{}, err
	}
	if err == nil && latest.AttachedVM != nil && *latest.AttachedVM > 0 {
		return models.Workspace{}, ErrWorkspaceForkAttached
	}
	if latest.VolumeID != source.VolumeID || latest.Storage != source.Storage || latest.SizeGB != source.SizeGB {
		return models.Workspace{}, errors.New("fork source changed during admission")
	}

	newID, err := newWorkspaceID(m.rand)
	if err != nil {
		return models.Workspace{}, err
	}

	volid, err := m.reserveCloneVolumeID(ctx, source.Storage, newID, source.SizeGB)
	if err != nil {
		return models.Workspace{}, err
	}

	actor, err := requestOwnerContext(ctx)
	if err != nil {
		return models.Workspace{}, err
	}
	serverIdentity, err := hostidentity.Local()
	if err != nil {
		return models.Workspace{}, err
	}
	operation := protocol.WorkspaceForkOperation{ServerIdentity: serverIdentity, ID: newID, Owner: actor, SourceWorkspaceID: source.ID, SourceVolumeID: source.VolumeID, FromSnapshot: requestedSnapshot, CopySnapshot: snapshotName, TargetName: newName, TargetWorkspaceID: newID, TargetVolumeID: volid, SizeGB: source.SizeGB, State: "planned", Cleanup: "inspection_required"}
	if operation.CopySnapshot == "" {
		operation.CopySnapshot = proxmox.CopySnapshotName(volid)
	}
	if err := m.store.CreateWorkspaceFork(ctx, operation); err != nil {
		return models.Workspace{}, err
	}
	operation, err = m.store.WorkspaceFork(ctx, source.ID, newID)
	if err != nil {
		return models.Workspace{}, err
	}
	return m.completeWorkspaceFork(ctx, source, operation, func(ctx context.Context) (proxmox.StorageOperation, error) {
		return copyBackend.CopyWorkspaceVolume(ctx, source.VolumeID, snapshotName, volid)
	})
}

func (m *WorkspaceManager) completeWorkspaceFork(ctx context.Context, source models.Workspace, operation protocol.WorkspaceForkOperation, copyVolume func(context.Context) (proxmox.StorageOperation, error)) (models.Workspace, error) {
	checkpoint := func() error {
		saveContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return m.store.UpdateWorkspaceFork(saveContext, operation)
	}
	retained := func(cause error) (models.Workspace, error) {
		operation.State = "blocked"
		if operation.TargetState != "published" {
			operation.Cleanup = "inspection_required"
			operation.TargetState = "unverified"
			if operation.Receipt != nil && operation.Receipt.OK {
				operation.TargetState = "retained"
			}
		}
		saveErr := checkpoint()
		return models.Workspace{}, &workspaceForkError{Operation: operation, Cause: errors.Join(cause, saveErr)}
	}
	operation.State = "running"
	if err := checkpoint(); err != nil {
		return models.Workspace{}, err
	}
	receipt, err := copyVolume(ctx)
	if previous := operation.Receipt; previous != nil {
		for _, pair := range []struct {
			old  string
			next *string
		}{{previous.SourceGUID, &receipt.SourceGUID}, {previous.SnapshotGUID, &receipt.SnapshotGUID}, {previous.TargetGUID, &receipt.TargetGUID}, {previous.JournalID, &receipt.JournalID}} {
			if pair.old == "" {
				continue
			}
			if *pair.next != "" && *pair.next != pair.old {
				return retained(errors.New("native fork identity changed"))
			}
			*pair.next = pair.old
		}
	}
	operation.Receipt = &receipt
	if err != nil {
		return retained(err)
	}
	if receipt.Version != 1 || !receipt.OK || receipt.State != "verified" || receipt.Cleanup != "verified" || receipt.Source != source.VolumeID || receipt.Target != operation.TargetVolumeID || receipt.Snapshot != operation.CopySnapshot || receipt.SourceGUID == "" || receipt.SnapshotGUID == "" || receipt.TargetGUID == "" {
		return retained(errors.New("workspace copy receipt is incomplete or mismatched"))
	}
	operation.TargetState = "verified"
	operation.Cleanup = receipt.Cleanup
	if err := checkpoint(); err != nil {
		return retained(err)
	}

	now := m.now().UTC()
	workspace := models.Workspace{
		ID:          operation.TargetWorkspaceID,
		Name:        operation.TargetName,
		Storage:     source.Storage,
		VolumeID:    operation.TargetVolumeID,
		SizeGB:      operation.SizeGB,
		CreatedAt:   now,
		LastUpdated: now,
	}
	if existing, err := m.store.GetWorkspace(ctx, workspace.ID); err == nil {
		if existing.Name != workspace.Name || existing.Storage != workspace.Storage || existing.VolumeID != workspace.VolumeID || existing.SizeGB != workspace.SizeGB {
			return retained(errors.New("fork workspace binding changed"))
		}
		workspace = existing
	} else if !errors.Is(err, sql.ErrNoRows) {
		return retained(err)
	} else if err := m.store.CreateWorkspace(ctx, workspace); err != nil {
		return retained(err)
	}
	operation.TargetState = "published"
	operation.State = "succeeded"
	if err := checkpoint(); err != nil {
		return retained(err)
	}

	return workspace, nil
}

func (m *WorkspaceManager) reserveCloneVolumeID(ctx context.Context, storage, name string, sizeGB int) (string, error) {
	planner, ok := m.backend.(proxmox.WorkspaceClonePlanner)
	if !ok {
		return "", proxmox.ErrStorageUnsupported
	}
	owner, err := m.workspaceVolumeOwner(ctx, name)
	if err != nil {
		return "", err
	}
	return planner.PlanWorkspaceClone(ctx, storage, owner.AllocationName, sizeGB)
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
	cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = m.store.ReleaseWorkspaceLease(cleanup, workspaceID, owner, nonce)
}
