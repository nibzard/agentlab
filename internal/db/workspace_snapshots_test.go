package db

import (
	"context"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceSnapshots(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 2, 9, 10, 0, 0, 0, time.UTC)

	workspace := models.Workspace{
		ID:          "ws-1",
		Name:        "workspace-alpha",
		Storage:     "local-zfs",
		VolumeID:    "local-zfs:vm-100-disk-1",
		SizeGB:      20,
		CreatedAt:   now,
		LastUpdated: now,
	}
	err := store.CreateWorkspace(ctx, workspace)
	require.NoError(t, err)

	t.Run("create and get snapshot", func(t *testing.T) {
		snapshot := models.WorkspaceSnapshot{
			WorkspaceID: workspace.ID,
			Name:        "baseline",
			BackendRef:  "baseline",
			CreatedAt:   now,
		}
		err := store.CreateWorkspaceSnapshot(ctx, snapshot)
		require.NoError(t, err)

		got, err := store.GetWorkspaceSnapshot(ctx, workspace.ID, "baseline")
		require.NoError(t, err)
		assert.Equal(t, snapshot.WorkspaceID, got.WorkspaceID)
		assert.Equal(t, snapshot.Name, got.Name)
		assert.Equal(t, snapshot.BackendRef, got.BackendRef)
	})

	t.Run("list snapshots ordered by created_at desc", func(t *testing.T) {
		older := models.WorkspaceSnapshot{
			WorkspaceID: workspace.ID,
			Name:        "older",
			BackendRef:  "older",
			CreatedAt:   now.Add(-2 * time.Hour),
		}
		newer := models.WorkspaceSnapshot{
			WorkspaceID: workspace.ID,
			Name:        "newer",
			BackendRef:  "newer",
			CreatedAt:   now.Add(2 * time.Hour),
		}
		require.NoError(t, store.CreateWorkspaceSnapshot(ctx, older))
		require.NoError(t, store.CreateWorkspaceSnapshot(ctx, newer))

		list, err := store.ListWorkspaceSnapshots(ctx, workspace.ID)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(list), 2)
		assert.Equal(t, "newer", list[0].Name)
	})
}

func TestCreateWorkspaceSnapshotValidation(t *testing.T) {
	ctx := context.Background()

	t.Run("nil store", func(t *testing.T) {
		err := (*Store)(nil).CreateWorkspaceSnapshot(ctx, models.WorkspaceSnapshot{
			WorkspaceID: "ws-1",
			Name:        "snap",
			BackendRef:  "snap",
		})
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("missing workspace id", func(t *testing.T) {
		store := openTestStore(t)
		err := store.CreateWorkspaceSnapshot(ctx, models.WorkspaceSnapshot{
			Name:       "snap",
			BackendRef: "snap",
		})
		assert.EqualError(t, err, "workspace id is required")
	})

	t.Run("missing name", func(t *testing.T) {
		store := openTestStore(t)
		err := store.CreateWorkspaceSnapshot(ctx, models.WorkspaceSnapshot{
			WorkspaceID: "ws-1",
			BackendRef:  "snap",
		})
		assert.EqualError(t, err, "snapshot name is required")
	})
}
