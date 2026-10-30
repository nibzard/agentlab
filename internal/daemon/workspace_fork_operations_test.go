package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/agentlab/agentlab/internal/proxmox"
	"github.com/stretchr/testify/require"
)

type failingWorkspaceCopyBackend struct {
	*stubBackend
	copies  int
	creates int
	deletes int
}

func (b *failingWorkspaceCopyBackend) CreateVolume(context.Context, string, string, int) (string, error) {
	b.creates++
	return "", errors.New("copy must not allocate a placeholder")
}
func (b *failingWorkspaceCopyBackend) DeleteVolume(context.Context, string) error {
	b.deletes++
	return nil
}
func (b *failingWorkspaceCopyBackend) CopyWorkspaceVolume(_ context.Context, source, snapshot, target string) (proxmox.StorageOperation, error) {
	b.copies++
	if snapshot == "" {
		snapshot = proxmox.CopySnapshotName(target)
	}
	receipt := proxmox.StorageOperation{Version: 1, Source: source, Target: target, Snapshot: snapshot, State: "copying", Cleanup: "inspection_required", SourceGUID: "101"}
	return receipt, &proxmox.StorageOperationError{Receipt: receipt, Cause: errors.New("copy response lost")}
}

func TestWorkspaceForkFailurePersistsResourcesAndNeverDeletes(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	backend := &failingWorkspaceCopyBackend{stubBackend: &stubBackend{}}
	manager := NewWorkspaceManager(store, backend, nil)
	source := models.Workspace{ID: "source", Name: "source", Storage: "local-zfs", VolumeID: "local-zfs:vm-0-disk-1", SizeGB: 10, CreatedAt: time.Now(), LastUpdated: time.Now()}
	require.NoError(t, store.CreateWorkspace(ctx, source))
	_, err := manager.Fork(ctx, source.ID, "retained-target", "")
	var retained *workspaceForkError
	require.ErrorAs(t, err, &retained)
	require.Zero(t, backend.creates)
	require.Zero(t, backend.deletes)
	require.Equal(t, 1, backend.copies)
	saved, err := store.WorkspaceFork(ctx, source.ID, retained.Operation.ID)
	require.NoError(t, err)
	require.Equal(t, "blocked", saved.State)
	require.Equal(t, retained.Operation.TargetVolumeID, saved.TargetVolumeID)
	require.Equal(t, proxmox.CopySnapshotName(saved.TargetVolumeID), saved.CopySnapshot)
	require.Equal(t, saved.CopySnapshot, saved.Receipt.Snapshot)
	// Reconstruct a manager. Another create cannot reuse the pending name.
	restarted := NewWorkspaceManager(store, backend, nil)
	_, err = restarted.Fork(ctx, source.ID, "retained-target", "")
	require.ErrorIs(t, err, ErrWorkspaceExists)
	var replayed *workspaceForkError
	require.ErrorAs(t, err, &replayed)
	require.Equal(t, saved.ID, replayed.Operation.ID)
	require.Equal(t, 1, backend.copies)
	_, err = restarted.Create(ctx, "retained-target", "local-zfs", 10)
	require.ErrorIs(t, err, ErrWorkspaceExists)
	require.Zero(t, backend.creates)
}

func TestWorkspaceForkObservationHonorsSourceAuthorization(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	manager := NewWorkspaceManager(store, &stubBackend{}, nil)
	source := models.Workspace{ID: "source", Name: "named-source", Storage: "local-zfs", VolumeID: "local-zfs:vm-0-disk-1", SizeGB: 10, CreatedAt: time.Now(), LastUpdated: time.Now()}
	require.NoError(t, store.CreateWorkspace(ctx, source))
	require.NoError(t, store.CreateWorkspaceFork(ctx, protocol.WorkspaceForkOperation{ServerIdentity: "fixture-server", ID: "copy-one", Owner: "actor", SourceWorkspaceID: source.ID, SourceVolumeID: source.VolumeID, TargetWorkspaceID: "target", TargetName: "target", TargetVolumeID: "local-zfs:target", CopySnapshot: "copy-snapshot"}))
	api := NewControlAPI(store, nil, nil, manager, nil, "", nil)
	actor := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"workspace.read"}}}}
	for _, path := range []string{"/v1/workspaces/named-source/forks", "/v1/workspaces/named-source/forks/copy-one"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request = request.WithContext(auth.WithIdentity(request.Context(), actor))
		response := httptest.NewRecorder()
		api.handleWorkspaceByID(response, request)
		require.Equal(t, 200, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "copy-one")
		actor.Token.Claims.Commands = []string{"job.read"}
		denied := httptest.NewRecorder()
		api.handleWorkspaceByID(denied, request)
		require.Equal(t, 403, denied.Code, denied.Body.String())
		actor.Token.Claims.Commands = []string{"workspace.read"}
	}
	wrong := httptest.NewRecorder()
	api.handleWorkspaceByID(wrong, httptest.NewRequest(http.MethodGet, "/v1/workspaces/named-source/forks/other-operation", nil))
	require.Equal(t, 404, wrong.Code)
	var result map[string]any
	require.NoError(t, json.Unmarshal(wrong.Body.Bytes(), &result))
}

func TestWorkspaceForkObservationChecksHistoricalSandboxScope(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	previous := 1002
	source := models.Workspace{ID: "scoped-source", Name: "scoped-source", Storage: "local-zfs", VolumeID: "local-zfs:vm-0-disk-1", SizeGB: 10, LastAttachedVM: &previous, CreatedAt: time.Now(), LastUpdated: time.Now()}
	require.NoError(t, store.CreateWorkspace(ctx, source))
	require.NoError(t, store.CreateWorkspaceFork(ctx, protocol.WorkspaceForkOperation{ServerIdentity: "fixture-server", ID: "scoped-copy", Owner: "actor", SourceWorkspaceID: source.ID, SourceVolumeID: source.VolumeID, TargetWorkspaceID: "target", TargetName: "target", TargetVolumeID: "local-zfs:target", CopySnapshot: "copy-snapshot"}))
	api := NewControlAPI(store, nil, nil, NewWorkspaceManager(store, &stubBackend{}, nil), nil, "", nil)
	actor := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"workspace.read"}, Scope: []string{"sandbox:1001"}}}}
	for _, path := range []string{"/v1/workspaces/scoped-source/forks", "/v1/workspaces/scoped-source/forks/scoped-copy"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request = request.WithContext(auth.WithIdentity(request.Context(), actor))
		denied := httptest.NewRecorder()
		api.handleWorkspaceByID(denied, request)
		require.Equal(t, 403, denied.Code, denied.Body.String())
		actor.Token.Claims.Scope = []string{"sandbox:1002"}
		allowed := httptest.NewRecorder()
		api.handleWorkspaceByID(allowed, request)
		require.Equal(t, 200, allowed.Code, allowed.Body.String())
		actor.Token.Claims.Scope = []string{"sandbox:1001"}
	}
}

type verifiedWorkspaceCopyBackend struct {
	*stubBackend
	creates int
	deletes int
}

func (b *verifiedWorkspaceCopyBackend) CreateVolume(context.Context, string, string, int) (string, error) {
	b.creates++
	return "", errors.New("unexpected placeholder allocation")
}
func (b *verifiedWorkspaceCopyBackend) DeleteVolume(context.Context, string) error {
	b.deletes++
	return nil
}

func TestWorkspaceForkPublicationFailureRetainsVerifiedCopy(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	source := models.Workspace{ID: "source", Name: "source", Storage: "local-zfs", VolumeID: "local-zfs:vm-0-disk-1", SizeGB: 10, CreatedAt: time.Now(), LastUpdated: time.Now()}
	require.NoError(t, store.CreateWorkspace(ctx, source))
	_, err := store.DB.Exec(`CREATE TRIGGER fail_fork_publication BEFORE INSERT ON workspaces BEGIN SELECT RAISE(FAIL,'injected publication failure'); END`)
	require.NoError(t, err)
	backend := &verifiedWorkspaceCopyBackend{stubBackend: &stubBackend{}}
	manager := NewWorkspaceManager(store, backend, nil)
	_, err = manager.Fork(ctx, source.ID, "retain-copy", "")
	var retained *workspaceForkError
	require.ErrorAs(t, err, &retained)
	require.Zero(t, backend.creates)
	require.Zero(t, backend.deletes)
	saved, err := store.WorkspaceFork(ctx, source.ID, retained.Operation.ID)
	require.NoError(t, err)
	require.Equal(t, "blocked", saved.State)
	require.Equal(t, "retained", saved.TargetState)
	require.Equal(t, "inspection_required", saved.Cleanup)
	require.True(t, saved.Receipt.OK)
	require.Equal(t, "verified", saved.Receipt.Cleanup)
	// Recovery cannot replace the recorded volume with a caller's new reference.
	changed := saved
	changed.TargetVolumeID = "local-zfs:unrelated-user-volume"
	require.Error(t, store.UpdateWorkspaceFork(ctx, changed))
	actual, err := store.WorkspaceFork(ctx, source.ID, saved.ID)
	require.NoError(t, err)
	require.Equal(t, saved.TargetVolumeID, actual.TargetVolumeID)
}
