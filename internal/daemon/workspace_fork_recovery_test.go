package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
	"github.com/stretchr/testify/require"
)

type recoveringWorkspaceCopyBackend struct {
	*failingWorkspaceCopyBackend
	recoveries    int
	expected      proxmox.StorageOperation
	recoveryError bool
}

func (b *recoveringWorkspaceCopyBackend) RecoverWorkspaceVolume(_ context.Context, expected proxmox.StorageOperation) (proxmox.StorageOperation, error) {
	b.recoveries++
	b.expected = expected
	if b.recoveryError {
		return proxmox.StorageOperation{}, errors.New("reply lost again")
	}
	expected.OK = true
	expected.State = "verified"
	expected.Cleanup = "verified"
	expected.SourceGUID = "101"
	expected.SnapshotGUID = "301"
	expected.TargetGUID = "201"
	expected.Recovered = true
	expected.JournalID = "native-journal"
	return expected, nil
}

func TestWorkspaceForkRecoveryPublishesOriginalTargetAfterRestart(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	source := models.Workspace{ID: "source", Name: "source", Storage: "local-zfs", VolumeID: "local-zfs:vm-0-disk-1", SizeGB: 10, CreatedAt: time.Now(), LastUpdated: time.Now()}
	require.NoError(t, store.CreateWorkspace(ctx, source))
	backend := &recoveringWorkspaceCopyBackend{failingWorkspaceCopyBackend: &failingWorkspaceCopyBackend{stubBackend: &stubBackend{}}}
	manager := NewWorkspaceManager(store, backend, nil)
	_, err := manager.Fork(ctx, source.ID, "recovered-target", "")
	var retained *workspaceForkError
	require.ErrorAs(t, err, &retained)
	original := retained.Operation
	manager = NewWorkspaceManager(store, backend, nil)
	actor := &auth.RequestIdentity{Method: "ssh-token", Subject: "other-actor", Fingerprint: "other-issuer"}
	_, err = manager.ResumeFork(auth.WithIdentity(ctx, actor), source.ID, original.ID)
	require.Error(t, err)
	require.Zero(t, backend.recoveries)
	// Unknown replies retain all recorded native identities.
	backend.recoveryError = true
	_, err = manager.ResumeFork(ctx, source.ID, original.ID)
	require.Error(t, err)
	saved, err := store.WorkspaceFork(ctx, source.ID, original.ID)
	require.NoError(t, err)
	require.Equal(t, "101", saved.Receipt.SourceGUID)
	backend.recoveryError = false
	workspace, err := manager.ResumeFork(ctx, source.ID, original.ID)
	require.NoError(t, err)
	require.Equal(t, original.TargetWorkspaceID, workspace.ID)
	require.Equal(t, original.TargetVolumeID, workspace.VolumeID)
	require.Equal(t, "101", backend.expected.SourceGUID)
	require.Equal(t, 1, backend.copies)
	require.Zero(t, backend.creates)
	require.Zero(t, backend.deletes)
	replay, err := manager.Fork(ctx, source.ID, "recovered-target", "")
	require.NoError(t, err)
	require.Equal(t, workspace.ID, replay.ID)
	require.Equal(t, 2, backend.recoveries)
	_, err = manager.ResumeFork(ctx, source.ID, original.ID)
	require.NoError(t, err)
	require.Equal(t, 2, backend.recoveries)
}

func TestWorkspaceForkRecoveryRejectsChangedSourceBeforeNativeCall(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	source := models.Workspace{ID: "source", Name: "source", Storage: "local-zfs", VolumeID: "local-zfs:vm-0-disk-1", SizeGB: 10, CreatedAt: time.Now(), LastUpdated: time.Now()}
	require.NoError(t, store.CreateWorkspace(ctx, source))
	backend := &recoveringWorkspaceCopyBackend{failingWorkspaceCopyBackend: &failingWorkspaceCopyBackend{stubBackend: &stubBackend{}}}
	manager := NewWorkspaceManager(store, backend, nil)
	_, err := manager.Fork(ctx, source.ID, "target", "")
	var retained *workspaceForkError
	require.ErrorAs(t, err, &retained)
	_, err = store.DB.Exec("UPDATE workspaces SET size_gb=11 WHERE id=?", source.ID)
	require.NoError(t, err)
	_, err = manager.ResumeFork(ctx, source.ID, retained.Operation.ID)
	require.Error(t, err)
	require.Zero(t, backend.recoveries)
	require.Equal(t, 1, backend.copies)
}

type recoveringVerifiedWorkspaceCopyBackend struct {
	*verifiedWorkspaceCopyBackend
	expected   proxmox.StorageOperation
	recoveries int
}

func (b *recoveringVerifiedWorkspaceCopyBackend) RecoverWorkspaceVolume(_ context.Context, expected proxmox.StorageOperation) (proxmox.StorageOperation, error) {
	b.expected = expected
	b.recoveries++
	expected.OK = true
	expected.State = "verified"
	expected.Cleanup = "verified"
	return expected, nil
}
func TestWorkspaceForkRecoveryFinishesFailedPublication(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	source := models.Workspace{ID: "source", Name: "source", Storage: "local-zfs", VolumeID: "local-zfs:vm-0-disk-1", SizeGB: 10, CreatedAt: time.Now(), LastUpdated: time.Now()}
	require.NoError(t, store.CreateWorkspace(ctx, source))
	_, err := store.DB.Exec(`CREATE TRIGGER fail_recovery_publication BEFORE INSERT ON workspaces BEGIN SELECT RAISE(FAIL,'injected failure'); END`)
	require.NoError(t, err)
	backend := &recoveringVerifiedWorkspaceCopyBackend{verifiedWorkspaceCopyBackend: &verifiedWorkspaceCopyBackend{stubBackend: &stubBackend{}}}
	manager := NewWorkspaceManager(store, backend, nil)
	_, err = manager.Fork(ctx, source.ID, "target", "")
	var retained *workspaceForkError
	require.ErrorAs(t, err, &retained)
	saved, err := store.WorkspaceFork(ctx, source.ID, retained.Operation.ID)
	require.NoError(t, err)
	require.True(t, saved.Receipt.OK)
	_, err = store.DB.Exec(`DROP TRIGGER fail_recovery_publication`)
	require.NoError(t, err)
	manager = NewWorkspaceManager(store, backend, nil)
	target, err := manager.ResumeFork(ctx, source.ID, saved.ID)
	require.NoError(t, err)
	require.Equal(t, saved.TargetWorkspaceID, target.ID)
	require.Equal(t, saved.TargetVolumeID, target.VolumeID)
	require.Equal(t, saved.Receipt.SourceGUID, backend.expected.SourceGUID)
	require.Equal(t, saved.Receipt.SnapshotGUID, backend.expected.SnapshotGUID)
	require.Equal(t, saved.Receipt.TargetGUID, backend.expected.TargetGUID)
	require.Equal(t, 1, backend.recoveries)
	require.Zero(t, backend.creates)
	require.Zero(t, backend.deletes)
}
