package db

import (
	"context"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceVolumeOwnerReservationProtectsSharedIDs(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	first, err := store.ReserveWorkspaceVolumeOwner(ctx, "workspace-one", nil)
	require.NoError(t, err)
	require.GreaterOrEqual(t, first.VMID, 900000000)
	require.LessOrEqual(t, first.VMID, 999999999)
	again, err := store.ReserveWorkspaceVolumeOwner(ctx, "workspace-one", nil)
	require.NoError(t, err)
	require.Equal(t, first, again)
	second, err := store.ReserveWorkspaceVolumeOwner(ctx, "workspace-two", map[int]bool{first.VMID: true})
	require.NoError(t, err)
	require.NotEqual(t, first.VMID, second.VMID)
	// Sandbox inserts use the same database guard as template reservations.
	now := time.Now()
	err = store.CreateSandbox(ctx, models.Sandbox{VMID: first.VMID, Name: "must-not-reuse", Profile: "default", State: models.SandboxRequested, CreatedAt: now})
	require.Error(t, err)
	// A changed reservation is a block, never a request for another ID.
	_, err = store.DB.Exec(`UPDATE vmid_reservations SET owner='other-operation' WHERE vmid=?`, first.VMID)
	require.NoError(t, err)
	_, err = store.ReserveWorkspaceVolumeOwner(ctx, "workspace-one", nil)
	require.ErrorIs(t, err, ErrWorkspaceVolumeOwner)
	var count int
	require.NoError(t, store.DB.QueryRow(`SELECT COUNT(*) FROM workspace_volume_owners WHERE workspace_id='workspace-one'`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestWorkspaceVolumeOwnerSkipsExternalCollision(t *testing.T) {
	firstStore := openTestStore(t)
	candidate, err := firstStore.ReserveWorkspaceVolumeOwner(context.Background(), "workspace-same", nil)
	require.NoError(t, err)
	otherStore := openTestStore(t)
	chosen, err := otherStore.ReserveWorkspaceVolumeOwner(context.Background(), "workspace-same", map[int]bool{candidate.VMID: true})
	require.NoError(t, err)
	require.NotEqual(t, candidate.VMID, chosen.VMID)
}
