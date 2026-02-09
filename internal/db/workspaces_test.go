package db

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateWorkspace(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := openTestStore(t)
		ws := models.Workspace{
			ID:          "ws-1",
			Name:        "test-workspace",
			Storage:     "local-zfs",
			VolumeID:    "local-zfs:vm-100-disk-1",
			SizeGB:      50,
			CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateWorkspace(ctx, ws)
		require.NoError(t, err)

		got, err := store.GetWorkspace(ctx, "ws-1")
		require.NoError(t, err)
		assert.Equal(t, "ws-1", got.ID)
		assert.Equal(t, "test-workspace", got.Name)
		assert.Equal(t, "local-zfs", got.Storage)
		assert.Equal(t, "local-zfs:vm-100-disk-1", got.VolumeID)
		assert.Equal(t, 50, got.SizeGB)
	})

	t.Run("nil store", func(t *testing.T) {
		err := (*Store)(nil).CreateWorkspace(ctx, models.Workspace{
			ID:       "x",
			Name:     "y",
			Storage:  "z",
			VolumeID: "v",
			SizeGB:   10,
		})
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("nil db", func(t *testing.T) {
		err := (&Store{}).CreateWorkspace(ctx, models.Workspace{
			ID:       "x",
			Name:     "y",
			Storage:  "z",
			VolumeID: "v",
			SizeGB:   10,
		})
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("missing id", func(t *testing.T) {
		store := openTestStore(t)
		ws := models.Workspace{
			Name:     "test-workspace",
			Storage:  "local-zfs",
			VolumeID: "local-zfs:vm-100-disk-1",
			SizeGB:   50,
		}
		err := store.CreateWorkspace(ctx, ws)
		assert.EqualError(t, err, "workspace id is required")
	})

	t.Run("empty id after trim", func(t *testing.T) {
		store := openTestStore(t)
		ws := models.Workspace{
			ID:       "   ",
			Name:     "test-workspace",
			Storage:  "local-zfs",
			VolumeID: "local-zfs:vm-100-disk-1",
			SizeGB:   50,
		}
		err := store.CreateWorkspace(ctx, ws)
		assert.EqualError(t, err, "workspace id is required")
	})

	t.Run("missing name", func(t *testing.T) {
		store := openTestStore(t)
		ws := models.Workspace{
			ID:       "ws-1",
			Storage:  "local-zfs",
			VolumeID: "local-zfs:vm-100-disk-1",
			SizeGB:   50,
		}
		err := store.CreateWorkspace(ctx, ws)
		assert.EqualError(t, err, "workspace name is required")
	})

	t.Run("missing storage", func(t *testing.T) {
		store := openTestStore(t)
		ws := models.Workspace{
			ID:       "ws-1",
			Name:     "test-workspace",
			VolumeID: "local-zfs:vm-100-disk-1",
			SizeGB:   50,
		}
		err := store.CreateWorkspace(ctx, ws)
		assert.EqualError(t, err, "workspace storage is required")
	})

	t.Run("missing volume id", func(t *testing.T) {
		store := openTestStore(t)
		ws := models.Workspace{
			ID:      "ws-1",
			Name:    "test-workspace",
			Storage: "local-zfs",
			SizeGB:  50,
		}
		err := store.CreateWorkspace(ctx, ws)
		assert.EqualError(t, err, "workspace volume id is required")
	})

	t.Run("invalid size_gb - zero", func(t *testing.T) {
		store := openTestStore(t)
		ws := models.Workspace{
			ID:       "ws-1",
			Name:     "test-workspace",
			Storage:  "local-zfs",
			VolumeID: "local-zfs:vm-100-disk-1",
			SizeGB:   0,
		}
		err := store.CreateWorkspace(ctx, ws)
		assert.EqualError(t, err, "workspace size_gb must be positive")
	})

	t.Run("invalid size_gb - negative", func(t *testing.T) {
		store := openTestStore(t)
		ws := models.Workspace{
			ID:       "ws-1",
			Name:     "test-workspace",
			Storage:  "local-zfs",
			VolumeID: "local-zfs:vm-100-disk-1",
			SizeGB:   -10,
		}
		err := store.CreateWorkspace(ctx, ws)
		assert.EqualError(t, err, "workspace size_gb must be positive")
	})

	t.Run("duplicate id", func(t *testing.T) {
		store := openTestStore(t)
		ws := models.Workspace{
			ID:          "ws-1",
			Name:        "test-workspace",
			Storage:     "local-zfs",
			VolumeID:    "local-zfs:vm-100-disk-1",
			SizeGB:      50,
			CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateWorkspace(ctx, ws)
		require.NoError(t, err)

		err = store.CreateWorkspace(ctx, ws)
		assert.Error(t, err)
	})

	t.Run("duplicate name", func(t *testing.T) {
		store := openTestStore(t)
		ws1 := models.Workspace{
			ID:          "ws-1",
			Name:        "same-name",
			Storage:     "local-zfs",
			VolumeID:    "local-zfs:vm-100-disk-1",
			SizeGB:      50,
			CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateWorkspace(ctx, ws1)
		require.NoError(t, err)

		ws2 := models.Workspace{
			ID:          "ws-2",
			Name:        "same-name",
			Storage:     "local-zfs",
			VolumeID:    "local-zfs:vm-101-disk-1",
			SizeGB:      50,
			CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err = store.CreateWorkspace(ctx, ws2)
		assert.Error(t, err)
	})

	t.Run("with attached vm", func(t *testing.T) {
		store := openTestStore(t)
		vmid := 100
		ws := models.Workspace{
			ID:          "ws-1",
			Name:        "test-workspace",
			Storage:     "local-zfs",
			VolumeID:    "local-zfs:vm-100-disk-1",
			SizeGB:      50,
			AttachedVM:  &vmid,
			CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateWorkspace(ctx, ws)
		require.NoError(t, err)

		got, err := store.GetWorkspace(ctx, "ws-1")
		require.NoError(t, err)
		assert.Equal(t, 100, *got.AttachedVM)
	})

	t.Run("auto timestamps", func(t *testing.T) {
		store := openTestStore(t)
		before := time.Now().UTC()
		ws := models.Workspace{
			ID:          "ws-1",
			Name:        "test-workspace",
			Storage:     "local-zfs",
			VolumeID:    "local-zfs:vm-100-disk-1",
			SizeGB:      50,
			LastUpdated: before,
		}
		err := store.CreateWorkspace(ctx, ws)
		require.NoError(t, err)

		got, err := store.GetWorkspace(ctx, "ws-1")
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now().UTC(), got.CreatedAt, time.Second)
		assert.Equal(t, before, got.LastUpdated)
	})
}

func TestGetWorkspace(t *testing.T) {
	ctx := context.Background()

	t.Run("exists", func(t *testing.T) {
		store := openTestStore(t)
		ws := models.Workspace{
			ID:          "ws-1",
			Name:        "test-workspace",
			Storage:     "local-zfs",
			VolumeID:    "local-zfs:vm-100-disk-1",
			SizeGB:      50,
			CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateWorkspace(ctx, ws)
		require.NoError(t, err)

		got, err := store.GetWorkspace(ctx, "ws-1")
		require.NoError(t, err)
		assert.Equal(t, "ws-1", got.ID)
	})

	t.Run("not found", func(t *testing.T) {
		store := openTestStore(t)
		_, err := store.GetWorkspace(ctx, "nonexistent")
		assert.Equal(t, sql.ErrNoRows, err)
	})

	t.Run("nil store", func(t *testing.T) {
		_, err := (*Store)(nil).GetWorkspace(ctx, "x")
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("nil db", func(t *testing.T) {
		_, err := (&Store{}).GetWorkspace(ctx, "x")
		assert.EqualError(t, err, "db store is nil")
	})
}

func TestGetWorkspaceByName(t *testing.T) {
	ctx := context.Background()

	t.Run("exists", func(t *testing.T) {
		store := openTestStore(t)
		ws := models.Workspace{
			ID:          "ws-1",
			Name:        "test-workspace",
			Storage:     "local-zfs",
			VolumeID:    "local-zfs:vm-100-disk-1",
			SizeGB:      50,
			CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateWorkspace(ctx, ws)
		require.NoError(t, err)

		got, err := store.GetWorkspaceByName(ctx, "test-workspace")
		require.NoError(t, err)
		assert.Equal(t, "ws-1", got.ID)
		assert.Equal(t, "test-workspace", got.Name)
	})

	t.Run("not found", func(t *testing.T) {
		store := openTestStore(t)
		_, err := store.GetWorkspaceByName(ctx, "nonexistent")
		assert.Equal(t, sql.ErrNoRows, err)
	})

	t.Run("nil store", func(t *testing.T) {
		_, err := (*Store)(nil).GetWorkspaceByName(ctx, "x")
		assert.EqualError(t, err, "db store is nil")
	})
}

func TestGetWorkspaceByAttachedVMID(t *testing.T) {
	ctx := context.Background()

	t.Run("exists", func(t *testing.T) {
		store := openTestStore(t)
		vmid := 100
		ws := models.Workspace{
			ID:          "ws-1",
			Name:        "test-workspace",
			Storage:     "local-zfs",
			VolumeID:    "local-zfs:vm-100-disk-1",
			SizeGB:      50,
			AttachedVM:  &vmid,
			CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateWorkspace(ctx, ws)
		require.NoError(t, err)

		got, err := store.GetWorkspaceByAttachedVMID(ctx, 100)
		require.NoError(t, err)
		assert.Equal(t, "ws-1", got.ID)
		assert.Equal(t, 100, *got.AttachedVM)
	})

	t.Run("not found", func(t *testing.T) {
		store := openTestStore(t)
		_, err := store.GetWorkspaceByAttachedVMID(ctx, 999)
		assert.Equal(t, sql.ErrNoRows, err)
	})

	t.Run("invalid vmid - zero", func(t *testing.T) {
		store := openTestStore(t)
		_, err := store.GetWorkspaceByAttachedVMID(ctx, 0)
		assert.EqualError(t, err, "vmid must be positive")
	})

	t.Run("invalid vmid - negative", func(t *testing.T) {
		store := openTestStore(t)
		_, err := store.GetWorkspaceByAttachedVMID(ctx, -1)
		assert.EqualError(t, err, "vmid must be positive")
	})

	t.Run("nil store", func(t *testing.T) {
		_, err := (*Store)(nil).GetWorkspaceByAttachedVMID(ctx, 1)
		assert.EqualError(t, err, "db store is nil")
	})
}

func TestListWorkspaces(t *testing.T) {
	ctx := context.Background()

	t.Run("empty list", func(t *testing.T) {
		store := openTestStore(t)
		list, err := store.ListWorkspaces(ctx)
		require.NoError(t, err)
		assert.Empty(t, list)
	})

	t.Run("multiple workspaces ordered by created_at desc", func(t *testing.T) {
		store := openTestStore(t)
		ws1 := models.Workspace{
			ID:          "ws-1",
			Name:        "workspace-1",
			Storage:     "local-zfs",
			VolumeID:    "local-zfs:vm-100-disk-1",
			SizeGB:      50,
			CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		ws2 := models.Workspace{
			ID:          "ws-2",
			Name:        "workspace-2",
			Storage:     "local-zfs",
			VolumeID:    "local-zfs:vm-101-disk-1",
			SizeGB:      50,
			CreatedAt:   time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
			LastUpdated: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateWorkspace(ctx, ws1)
		require.NoError(t, err)
		err = store.CreateWorkspace(ctx, ws2)
		require.NoError(t, err)

		list, err := store.ListWorkspaces(ctx)
		require.NoError(t, err)
		assert.Len(t, list, 2)
		assert.Equal(t, "ws-2", list[0].ID)
		assert.Equal(t, "ws-1", list[1].ID)
	})

	t.Run("nil store", func(t *testing.T) {
		_, err := (*Store)(nil).ListWorkspaces(ctx)
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("nil db", func(t *testing.T) {
		_, err := (&Store{}).ListWorkspaces(ctx)
		assert.EqualError(t, err, "db store is nil")
	})
}

func TestAttachWorkspace(t *testing.T) {
	ctx := context.Background()

	t.Run("success - attach to unattached workspace", func(t *testing.T) {
		store := openTestStore(t)
		ws := models.Workspace{
			ID:          "ws-1",
			Name:        "test-workspace",
			Storage:     "local-zfs",
			VolumeID:    "local-zfs:vm-100-disk-1",
			SizeGB:      50,
			CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateWorkspace(ctx, ws)
		require.NoError(t, err)

		attached, err := store.AttachWorkspace(ctx, "ws-1", 123)
		require.NoError(t, err)
		assert.True(t, attached)

		got, err := store.GetWorkspace(ctx, "ws-1")
		require.NoError(t, err)
		assert.NotNil(t, got.AttachedVM)
		assert.Equal(t, 123, *got.AttachedVM)
	})

	t.Run("failed - already attached", func(t *testing.T) {
		store := openTestStore(t)
		vmid := 100
		ws := models.Workspace{
			ID:          "ws-1",
			Name:        "test-workspace",
			Storage:     "local-zfs",
			VolumeID:    "local-zfs:vm-100-disk-1",
			SizeGB:      50,
			AttachedVM:  &vmid,
			CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateWorkspace(ctx, ws)
		require.NoError(t, err)

		// Try to attach to a different VM
		attached, err := store.AttachWorkspace(ctx, "ws-1", 123)
		require.NoError(t, err)
		assert.False(t, attached)

		// Should still be attached to original VM
		got, err := store.GetWorkspace(ctx, "ws-1")
		require.NoError(t, err)
		assert.Equal(t, 100, *got.AttachedVM)
	})

	t.Run("workspace not found", func(t *testing.T) {
		store := openTestStore(t)
		attached, err := store.AttachWorkspace(ctx, "nonexistent", 123)
		require.NoError(t, err)
		assert.False(t, attached)
	})

	t.Run("nil store", func(t *testing.T) {
		attached, err := (*Store)(nil).AttachWorkspace(ctx, "x", 1)
		assert.EqualError(t, err, "db store is nil")
		assert.False(t, attached)
	})

	t.Run("missing workspace id", func(t *testing.T) {
		store := openTestStore(t)
		attached, err := store.AttachWorkspace(ctx, "", 123)
		assert.EqualError(t, err, "workspace id is required")
		assert.False(t, attached)
	})

	t.Run("empty workspace id after trim", func(t *testing.T) {
		store := openTestStore(t)
		attached, err := store.AttachWorkspace(ctx, "   ", 123)
		assert.EqualError(t, err, "workspace id is required")
		assert.False(t, attached)
	})

	t.Run("invalid vmid - zero", func(t *testing.T) {
		store := openTestStore(t)
		attached, err := store.AttachWorkspace(ctx, "ws-1", 0)
		assert.EqualError(t, err, "vmid must be positive")
		assert.False(t, attached)
	})

	t.Run("invalid vmid - negative", func(t *testing.T) {
		store := openTestStore(t)
		attached, err := store.AttachWorkspace(ctx, "ws-1", -1)
		assert.EqualError(t, err, "vmid must be positive")
		assert.False(t, attached)
	})
}

func TestWorkspaceLeaseLifecycle(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	ws := models.Workspace{
		ID:          "ws-lease",
		Name:        "lease-workspace",
		Storage:     "local-zfs",
		VolumeID:    "local-zfs:vm-200-disk-1",
		SizeGB:      20,
		CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	require.NoError(t, store.CreateWorkspace(ctx, ws))

	now := time.Now().UTC()
	expires := now.Add(30 * time.Minute)

	acquired, err := store.TryAcquireWorkspaceLease(ctx, ws.ID, "job:one", "nonce-1", expires)
	require.NoError(t, err)
	assert.True(t, acquired)

	updated, err := store.GetWorkspace(ctx, ws.ID)
	require.NoError(t, err)
	assert.Equal(t, "job:one", updated.LeaseOwner)
	assert.Equal(t, "nonce-1", updated.LeaseNonce)
	assert.WithinDuration(t, expires, updated.LeaseExpires, time.Second)

	acquired, err = store.TryAcquireWorkspaceLease(ctx, ws.ID, "job:two", "nonce-2", now.Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, acquired)

	renewed, err := store.RenewWorkspaceLease(ctx, ws.ID, "job:one", "bad-nonce", now.Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, renewed)

	renewed, err = store.RenewWorkspaceLease(ctx, ws.ID, "job:one", "nonce-1", now.Add(time.Hour))
	require.NoError(t, err)
	assert.True(t, renewed)

	released, err := store.ReleaseWorkspaceLease(ctx, ws.ID, "job:one", "bad-nonce")
	require.NoError(t, err)
	assert.False(t, released)

	released, err = store.ReleaseWorkspaceLease(ctx, ws.ID, "job:one", "nonce-1")
	require.NoError(t, err)
	assert.True(t, released)

	cleared, err := store.GetWorkspace(ctx, ws.ID)
	require.NoError(t, err)
	assert.Empty(t, cleared.LeaseOwner)
	assert.Empty(t, cleared.LeaseNonce)
	assert.True(t, cleared.LeaseExpires.IsZero())
}

func TestWorkspaceLeaseExpiryAllowsAcquire(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	ws := models.Workspace{
		ID:          "ws-expired",
		Name:        "expired-workspace",
		Storage:     "local-zfs",
		VolumeID:    "local-zfs:vm-300-disk-1",
		SizeGB:      20,
		CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	require.NoError(t, store.CreateWorkspace(ctx, ws))

	now := time.Now().UTC()
	expired := now.Add(-1 * time.Minute)
	acquired, err := store.TryAcquireWorkspaceLease(ctx, ws.ID, "job:old", "nonce-old", expired)
	require.NoError(t, err)
	assert.True(t, acquired)

	newExpires := now.Add(15 * time.Minute)
	acquired, err = store.TryAcquireWorkspaceLease(ctx, ws.ID, "job:new", "nonce-new", newExpires)
	require.NoError(t, err)
	assert.True(t, acquired)

	updated, err := store.GetWorkspace(ctx, ws.ID)
	require.NoError(t, err)
	assert.Equal(t, "job:new", updated.LeaseOwner)
	assert.Equal(t, "nonce-new", updated.LeaseNonce)
	assert.WithinDuration(t, newExpires, updated.LeaseExpires, time.Second)
}

func TestDetachWorkspace(t *testing.T) {
	ctx := context.Background()

	t.Run("success - detach from correct vm", func(t *testing.T) {
		store := openTestStore(t)
		vmid := 100
		ws := models.Workspace{
			ID:          "ws-1",
			Name:        "test-workspace",
			Storage:     "local-zfs",
			VolumeID:    "local-zfs:vm-100-disk-1",
			SizeGB:      50,
			AttachedVM:  &vmid,
			CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateWorkspace(ctx, ws)
		require.NoError(t, err)

		detached, err := store.DetachWorkspace(ctx, "ws-1", 100)
		require.NoError(t, err)
		assert.True(t, detached)

		got, err := store.GetWorkspace(ctx, "ws-1")
		require.NoError(t, err)
		assert.Nil(t, got.AttachedVM)
	})

	t.Run("failed - wrong vm", func(t *testing.T) {
		store := openTestStore(t)
		vmid := 100
		ws := models.Workspace{
			ID:          "ws-1",
			Name:        "test-workspace",
			Storage:     "local-zfs",
			VolumeID:    "local-zfs:vm-100-disk-1",
			SizeGB:      50,
			AttachedVM:  &vmid,
			CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateWorkspace(ctx, ws)
		require.NoError(t, err)

		// Try to detach from different VM
		detached, err := store.DetachWorkspace(ctx, "ws-1", 999)
		require.NoError(t, err)
		assert.False(t, detached)

		// Should still be attached
		got, err := store.GetWorkspace(ctx, "ws-1")
		require.NoError(t, err)
		assert.Equal(t, 100, *got.AttachedVM)
	})

	t.Run("failed - workspace not attached", func(t *testing.T) {
		store := openTestStore(t)
		ws := models.Workspace{
			ID:          "ws-1",
			Name:        "test-workspace",
			Storage:     "local-zfs",
			VolumeID:    "local-zfs:vm-100-disk-1",
			SizeGB:      50,
			CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateWorkspace(ctx, ws)
		require.NoError(t, err)

		detached, err := store.DetachWorkspace(ctx, "ws-1", 100)
		require.NoError(t, err)
		assert.False(t, detached)
	})

	t.Run("workspace not found", func(t *testing.T) {
		store := openTestStore(t)
		detached, err := store.DetachWorkspace(ctx, "nonexistent", 100)
		require.NoError(t, err)
		assert.False(t, detached)
	})

	t.Run("nil store", func(t *testing.T) {
		detached, err := (*Store)(nil).DetachWorkspace(ctx, "x", 1)
		assert.EqualError(t, err, "db store is nil")
		assert.False(t, detached)
	})

	t.Run("missing workspace id", func(t *testing.T) {
		store := openTestStore(t)
		detached, err := store.DetachWorkspace(ctx, "", 100)
		assert.EqualError(t, err, "workspace id is required")
		assert.False(t, detached)
	})

	t.Run("empty workspace id after trim", func(t *testing.T) {
		store := openTestStore(t)
		detached, err := store.DetachWorkspace(ctx, "   ", 100)
		assert.EqualError(t, err, "workspace id is required")
		assert.False(t, detached)
	})

	t.Run("invalid vmid - zero", func(t *testing.T) {
		store := openTestStore(t)
		detached, err := store.DetachWorkspace(ctx, "ws-1", 0)
		assert.EqualError(t, err, "vmid must be positive")
		assert.False(t, detached)
	})

	t.Run("invalid vmid - negative", func(t *testing.T) {
		store := openTestStore(t)
		detached, err := store.DetachWorkspace(ctx, "ws-1", -1)
		assert.EqualError(t, err, "vmid must be positive")
		assert.False(t, detached)
	})
}

func TestWorkspaceAttachDetachRoundtrip(t *testing.T) {
	ctx := context.Background()

	t.Run("attach then detach", func(t *testing.T) {
		store := openTestStore(t)
		ws := models.Workspace{
			ID:          "ws-1",
			Name:        "test-workspace",
			Storage:     "local-zfs",
			VolumeID:    "local-zfs:vm-100-disk-1",
			SizeGB:      50,
			CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateWorkspace(ctx, ws)
		require.NoError(t, err)

		// Attach to VM A
		attached, err := store.AttachWorkspace(ctx, "ws-1", 100)
		require.NoError(t, err)
		assert.True(t, attached)

		got, err := store.GetWorkspace(ctx, "ws-1")
		require.NoError(t, err)
		assert.Equal(t, 100, *got.AttachedVM)

		// Detach from VM A
		detached, err := store.DetachWorkspace(ctx, "ws-1", 100)
		require.NoError(t, err)
		assert.True(t, detached)

		got, err = store.GetWorkspace(ctx, "ws-1")
		require.NoError(t, err)
		assert.Nil(t, got.AttachedVM)

		// Attach to VM B
		attached, err = store.AttachWorkspace(ctx, "ws-1", 200)
		require.NoError(t, err)
		assert.True(t, attached)

		got, err = store.GetWorkspace(ctx, "ws-1")
		require.NoError(t, err)
		assert.Equal(t, 200, *got.AttachedVM)
	})

	t.Run("trim whitespace in id", func(t *testing.T) {
		store := openTestStore(t)
		ws := models.Workspace{
			ID:          "ws-1",
			Name:        "test-workspace",
			Storage:     "local-zfs",
			VolumeID:    "local-zfs:vm-100-disk-1",
			SizeGB:      50,
			CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateWorkspace(ctx, ws)
		require.NoError(t, err)

		// Use id with whitespace
		attached, err := store.AttachWorkspace(ctx, "  ws-1  ", 100)
		require.NoError(t, err)
		assert.True(t, attached)

		got, err := store.GetWorkspace(ctx, "ws-1")
		require.NoError(t, err)
		assert.Equal(t, 100, *got.AttachedVM)
	})
}
