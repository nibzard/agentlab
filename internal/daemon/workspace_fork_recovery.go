package daemon

import (
	"context"
	"errors"

	"github.com/agentlab/agentlab/internal/hostidentity"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/agentlab/agentlab/internal/proxmox"
)

func (m *WorkspaceManager) ResumeFork(ctx context.Context, sourceName, id string) (models.Workspace, error) {
	if m == nil || m.store == nil || m.backend == nil {
		return models.Workspace{}, errors.New("workspace manager unavailable")
	}
	source, err := m.Resolve(ctx, sourceName)
	if err != nil {
		return models.Workspace{}, err
	}
	operation, err := m.store.WorkspaceFork(ctx, source.ID, id)
	if err != nil {
		return models.Workspace{}, err
	}
	release, err := m.store.LockJobSubmission(ctx, "workspace-name", operation.TargetName)
	if err != nil {
		return models.Workspace{}, err
	}
	defer release()
	operation, err = m.store.WorkspaceFork(ctx, source.ID, id)
	if err != nil {
		return models.Workspace{}, err
	}
	return m.resumeWorkspaceForkLocked(ctx, source, operation)
}

func (m *WorkspaceManager) resumeWorkspaceForkLocked(ctx context.Context, source models.Workspace, operation protocol.WorkspaceForkOperation) (models.Workspace, error) {
	blocked := func(err error) (models.Workspace, error) {
		return models.Workspace{}, &workspaceForkError{Operation: operation, Cause: err}
	}
	owner, err := requestOwnerContext(ctx)
	if err != nil {
		return blocked(err)
	}
	identity, err := hostidentity.Local()
	if err != nil {
		return blocked(err)
	}
	if owner != operation.Owner || identity != operation.ServerIdentity {
		return blocked(errors.New("fork actor or server changed"))
	}
	if operation.State == "succeeded" {
		existing, err := m.store.GetWorkspace(ctx, operation.TargetWorkspaceID)
		if err != nil {
			return blocked(err)
		}
		if existing.Name != operation.TargetName || existing.VolumeID != operation.TargetVolumeID || existing.Storage != source.Storage || (operation.SizeGB > 0 && existing.SizeGB != operation.SizeGB) {
			return blocked(errors.New("published fork binding changed"))
		}
		return existing, nil
	}
	if source.ID != operation.SourceWorkspaceID || source.VolumeID != operation.SourceVolumeID || operation.SizeGB <= 0 || source.SizeGB != operation.SizeGB {
		return blocked(errors.New("fork source binding changed or lacks a pinned size"))
	}
	backend, ok := m.backend.(proxmox.WorkspaceCopyRecoveryBackend)
	if !ok {
		return blocked(proxmox.ErrStorageUnsupported)
	}
	if source.AttachedVM != nil {
		return blocked(ErrWorkspaceForkAttached)
	}
	// Recovery cannot create a replacement owner reservation for an existing intent.
	if _, err := m.store.WorkspaceVolumeOwner(ctx, operation.TargetWorkspaceID); err != nil {
		return blocked(err)
	}
	planned, err := m.reserveCloneVolumeID(ctx, source.Storage, operation.TargetWorkspaceID, operation.SizeGB)
	if err != nil {
		return blocked(err)
	}
	if planned != operation.TargetVolumeID {
		return blocked(errors.New("native fork target binding changed"))
	}
	lease, nonce, err := m.acquireForkLease(ctx, source)
	if err != nil {
		return blocked(err)
	}
	defer m.releaseForkLease(ctx, source.ID, lease, nonce)
	latest, err := m.store.GetWorkspace(ctx, source.ID)
	if err != nil {
		return blocked(err)
	}
	if latest.AttachedVM != nil || latest.VolumeID != operation.SourceVolumeID || latest.Storage != source.Storage || latest.SizeGB != operation.SizeGB {
		return blocked(errors.New("fork source changed during admission"))
	}
	expected := protocol.StorageOperation{Version: 1, Source: operation.SourceVolumeID, Snapshot: operation.CopySnapshot, Target: operation.TargetVolumeID, Operation: "copy-snapshot"}
	if operation.FromSnapshot == "" {
		expected.Operation = "copy"
	}
	if previous := operation.Receipt; previous != nil {
		expected.SourceGUID, expected.SnapshotGUID, expected.TargetGUID = previous.SourceGUID, previous.SnapshotGUID, previous.TargetGUID
	}
	return m.completeWorkspaceFork(ctx, source, operation, func(ctx context.Context) (proxmox.StorageOperation, error) {
		return backend.RecoverWorkspaceVolume(ctx, expected)
	})
}
