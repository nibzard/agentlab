package db

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	testutil "github.com/agentlab/agentlab/internal/testing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateSandbox(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := openTestStore(t)
		sb := testutil.NewTestSandbox(testutil.SandboxOpts{
			VMID:  testutil.TestVMID,
			Name:  "test-sandbox",
			State: models.SandboxProvisioning,
		})
		err := store.CreateSandbox(ctx, sb)
		require.NoError(t, err)

		got, err := store.GetSandbox(ctx, testutil.TestVMID)
		require.NoError(t, err)
		assert.Equal(t, testutil.TestVMID, got.VMID)
		assert.Equal(t, "test-sandbox", got.Name)
		assert.Equal(t, testutil.TestProfile, got.Profile)
		assert.Equal(t, models.SandboxProvisioning, got.State)
	})

	t.Run("nil store", func(t *testing.T) {
		err := (*Store)(nil).CreateSandbox(ctx, testutil.NewTestSandbox(testutil.SandboxOpts{VMID: 1}))
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("nil db", func(t *testing.T) {
		err := (&Store{}).CreateSandbox(ctx, testutil.NewTestSandbox(testutil.SandboxOpts{VMID: 1}))
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("missing vmid", func(t *testing.T) {
		store := openTestStore(t)
		sb := models.Sandbox{
			Name:    "test-sandbox",
			Profile: testutil.TestProfile,
			State:   models.SandboxProvisioning,
		}
		err := store.CreateSandbox(ctx, sb)
		assert.EqualError(t, err, "sandbox vmid is required")
	})

	t.Run("invalid vmid - zero", func(t *testing.T) {
		store := openTestStore(t)
		sb := models.Sandbox{
			VMID:    0,
			Name:    "test-sandbox",
			Profile: testutil.TestProfile,
			State:   models.SandboxProvisioning,
		}
		err := store.CreateSandbox(ctx, sb)
		assert.EqualError(t, err, "sandbox vmid is required")
	})

	t.Run("invalid vmid - negative", func(t *testing.T) {
		store := openTestStore(t)
		sb := models.Sandbox{
			VMID:    -1,
			Name:    "test-sandbox",
			Profile: testutil.TestProfile,
			State:   models.SandboxProvisioning,
		}
		err := store.CreateSandbox(ctx, sb)
		assert.EqualError(t, err, "sandbox vmid is required")
	})

	t.Run("missing name", func(t *testing.T) {
		store := openTestStore(t)
		sb := models.Sandbox{
			VMID:    testutil.TestVMID,
			Profile: testutil.TestProfile,
			State:   models.SandboxProvisioning,
		}
		err := store.CreateSandbox(ctx, sb)
		assert.EqualError(t, err, "sandbox name is required")
	})

	t.Run("missing profile", func(t *testing.T) {
		store := openTestStore(t)
		sb := models.Sandbox{
			VMID:  testutil.TestVMID,
			Name:  "test-sandbox",
			State: models.SandboxProvisioning,
		}
		err := store.CreateSandbox(ctx, sb)
		assert.EqualError(t, err, "sandbox profile is required")
	})

	t.Run("missing state", func(t *testing.T) {
		store := openTestStore(t)
		sb := models.Sandbox{
			VMID:    testutil.TestVMID,
			Name:    "test-sandbox",
			Profile: testutil.TestProfile,
		}
		err := store.CreateSandbox(ctx, sb)
		assert.EqualError(t, err, "sandbox state is required")
	})

	t.Run("duplicate vmid", func(t *testing.T) {
		store := openTestStore(t)
		sb := testutil.NewTestSandbox(testutil.SandboxOpts{VMID: testutil.TestVMID})
		err := store.CreateSandbox(ctx, sb)
		require.NoError(t, err)

		err = store.CreateSandbox(ctx, sb)
		assert.Error(t, err)
	})

	t.Run("with optional fields", func(t *testing.T) {
		store := openTestStore(t)
		wsID := "ws-1"
		lease := testutil.FixedTime.Add(time.Hour * 24)
		sb := testutil.NewTestSandbox(testutil.SandboxOpts{
			VMID:        testutil.TestVMID,
			Name:        "test-sandbox",
			State:       models.SandboxRunning,
			WorkspaceID: &wsID,
		})
		sb.IP = "10.77.0.10"
		sb.Keepalive = true
		sb.LeaseExpires = lease
		err := store.CreateSandbox(ctx, sb)
		require.NoError(t, err)

		got, err := store.GetSandbox(ctx, testutil.TestVMID)
		require.NoError(t, err)
		assert.Equal(t, "10.77.0.10", got.IP)
		assert.Equal(t, "ws-1", *got.WorkspaceID)
		assert.True(t, got.Keepalive)
		assert.Equal(t, lease, got.LeaseExpires)
	})

	t.Run("auto timestamps", func(t *testing.T) {
		store := openTestStore(t)
		before := time.Now().UTC()
		sb := models.Sandbox{
			VMID:          testutil.TestVMID,
			Name:          "test-sandbox",
			Profile:       testutil.TestProfile,
			State:         models.SandboxProvisioning,
			LastUpdatedAt: before,
		}
		err := store.CreateSandbox(ctx, sb)
		require.NoError(t, err)

		got, err := store.GetSandbox(ctx, testutil.TestVMID)
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now().UTC(), got.CreatedAt, time.Second)
		assert.Equal(t, before, got.LastUpdatedAt)
	})
}

func TestGetSandbox(t *testing.T) {
	ctx := context.Background()

	t.Run("exists", func(t *testing.T) {
		store := openTestStore(t)
		sb := testutil.NewTestSandbox(testutil.SandboxOpts{VMID: testutil.TestVMID})
		err := store.CreateSandbox(ctx, sb)
		require.NoError(t, err)

		got, err := store.GetSandbox(ctx, testutil.TestVMID)
		require.NoError(t, err)
		assert.Equal(t, testutil.TestVMID, got.VMID)
	})

	t.Run("not found", func(t *testing.T) {
		store := openTestStore(t)
		_, err := store.GetSandbox(ctx, 999)
		assert.Equal(t, sql.ErrNoRows, err)
	})

	t.Run("nil store", func(t *testing.T) {
		_, err := (*Store)(nil).GetSandbox(ctx, 1)
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("nil db", func(t *testing.T) {
		_, err := (&Store{}).GetSandbox(ctx, 1)
		assert.EqualError(t, err, "db store is nil")
	})
}

func TestListSandboxes(t *testing.T) {
	ctx := context.Background()

	t.Run("empty list", func(t *testing.T) {
		store := openTestStore(t)
		list, err := store.ListSandboxes(ctx)
		require.NoError(t, err)
		assert.Empty(t, list)
	})

	t.Run("multiple sandboxes ordered by created_at desc", func(t *testing.T) {
		store := openTestStore(t)
		sb1 := testutil.NewTestSandbox(testutil.SandboxOpts{
			VMID:  testutil.TestVMID,
			Name:  "sandbox-1",
			State: models.SandboxRunning,
		})
		sb2 := testutil.NewTestSandbox(testutil.SandboxOpts{
			VMID:  testutil.TestVMID + 1,
			Name:  "sandbox-2",
			State: models.SandboxProvisioning,
			CreatedAt: testutil.FixedTime.Add(time.Hour * 24),
			LastUpdatedAt: testutil.FixedTime.Add(time.Hour * 24),
		})
		err := store.CreateSandbox(ctx, sb1)
		require.NoError(t, err)
		err = store.CreateSandbox(ctx, sb2)
		require.NoError(t, err)

		list, err := store.ListSandboxes(ctx)
		require.NoError(t, err)
		assert.Len(t, list, 2)
		assert.Equal(t, testutil.TestVMID+1, list[0].VMID)
		assert.Equal(t, testutil.TestVMID, list[1].VMID)
	})

	t.Run("nil store", func(t *testing.T) {
		_, err := (*Store)(nil).ListSandboxes(ctx)
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("nil db", func(t *testing.T) {
		_, err := (&Store{}).ListSandboxes(ctx)
		assert.EqualError(t, err, "db store is nil")
	})
}

func TestMaxSandboxVMID(t *testing.T) {
	ctx := context.Background()

	t.Run("empty database", func(t *testing.T) {
		store := openTestStore(t)
		max, err := store.MaxSandboxVMID(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0, max)
	})

	t.Run("single sandbox", func(t *testing.T) {
		store := openTestStore(t)
		sb := testutil.NewTestSandbox(testutil.SandboxOpts{
			VMID:  testutil.TestVMID,
			Name:  "sandbox-1",
			State: models.SandboxRunning,
		})
		err := store.CreateSandbox(ctx, sb)
		require.NoError(t, err)

		max, err := store.MaxSandboxVMID(ctx)
		require.NoError(t, err)
		assert.Equal(t, testutil.TestVMID, max)
	})

	t.Run("multiple sandboxes", func(t *testing.T) {
		store := openTestStore(t)
		for _, vmid := range []int{testutil.TestVMID, 250, 150} {
			sb := testutil.NewTestSandbox(testutil.SandboxOpts{
				VMID:  vmid,
				Name:  "sandbox",
				State: models.SandboxRunning,
			})
			err := store.CreateSandbox(ctx, sb)
			require.NoError(t, err)
		}

		max, err := store.MaxSandboxVMID(ctx)
		require.NoError(t, err)
		assert.Equal(t, 250, max)
	})

	t.Run("nil store", func(t *testing.T) {
		max, err := (*Store)(nil).MaxSandboxVMID(ctx)
		assert.EqualError(t, err, "db store is nil")
		assert.Equal(t, 0, max)
	})

	t.Run("nil db", func(t *testing.T) {
		max, err := (&Store{}).MaxSandboxVMID(ctx)
		assert.EqualError(t, err, "db store is nil")
		assert.Equal(t, 0, max)
	})
}

func TestListExpiredSandboxes(t *testing.T) {
	ctx := context.Background()
	now := testutil.FixedTime.Add(time.Hour * 24 * 9)

	t.Run("empty list", func(t *testing.T) {
		store := openTestStore(t)
		list, err := store.ListExpiredSandboxes(ctx, now)
		require.NoError(t, err)
		assert.Empty(t, list)
	})

	t.Run("expired sandboxes returned", func(t *testing.T) {
		store := openTestStore(t)
		sbExpired := testutil.NewTestSandbox(testutil.SandboxOpts{
			VMID:         testutil.TestVMID,
			Name:         "expired-sandbox",
			State:        models.SandboxRunning,
			LeaseExpires: now.Add(-time.Hour),
		})
		err := store.CreateSandbox(ctx, sbExpired)
		require.NoError(t, err)

		sbActive := testutil.NewTestSandbox(testutil.SandboxOpts{
			VMID:         testutil.TestVMID + 1,
			Name:         "active-sandbox",
			State:        models.SandboxRunning,
			LeaseExpires: now.Add(time.Hour),
		})
		err = store.CreateSandbox(ctx, sbActive)
		require.NoError(t, err)

		sbNoLease := testutil.NewTestSandbox(testutil.SandboxOpts{
			VMID:  testutil.TestVMID + 2,
			Name:  "no-lease-sandbox",
			State: models.SandboxRunning,
		})
		err = store.CreateSandbox(ctx, sbNoLease)
		require.NoError(t, err)

		sbDestroyed := testutil.NewTestSandbox(testutil.SandboxOpts{
			VMID:         testutil.TestVMID + 3,
			Name:         "destroyed-sandbox",
			State:        models.SandboxDestroyed,
			LeaseExpires: now.Add(-time.Hour),
		})
		err = store.CreateSandbox(ctx, sbDestroyed)
		require.NoError(t, err)

		list, err := store.ListExpiredSandboxes(ctx, now)
		require.NoError(t, err)
		assert.Len(t, list, 1)
		assert.Equal(t, testutil.TestVMID, list[0].VMID)
	})

	t.Run("nil store", func(t *testing.T) {
		_, err := (*Store)(nil).ListExpiredSandboxes(ctx, now)
		assert.EqualError(t, err, "db store is nil")
	})
}

func TestUpdateSandboxState(t *testing.T) {
	ctx := context.Background()

	t.Run("success - valid transition", func(t *testing.T) {
		store := openTestStore(t)
		sb := testutil.NewTestSandbox(testutil.SandboxOpts{
			VMID:  testutil.TestVMID,
			State: models.SandboxProvisioning,
		})
		err := store.CreateSandbox(ctx, sb)
		require.NoError(t, err)

		updated, err := store.UpdateSandboxState(ctx, testutil.TestVMID, models.SandboxProvisioning, models.SandboxBooting)
		require.NoError(t, err)
		assert.True(t, updated)

		got, err := store.GetSandbox(ctx, testutil.TestVMID)
		require.NoError(t, err)
		assert.Equal(t, models.SandboxBooting, got.State)
	})

	t.Run("failed - wrong current state", func(t *testing.T) {
		store := openTestStore(t)
		sb := testutil.NewTestSandbox(testutil.SandboxOpts{
			VMID:  testutil.TestVMID,
			State: models.SandboxProvisioning,
		})
		err := store.CreateSandbox(ctx, sb)
		require.NoError(t, err)

		// Try to transition from wrong state
		updated, err := store.UpdateSandboxState(ctx, testutil.TestVMID, models.SandboxRunning, models.SandboxCompleted)
		require.NoError(t, err)
		assert.False(t, updated)

		// State should not have changed
		got, err := store.GetSandbox(ctx, testutil.TestVMID)
		require.NoError(t, err)
		assert.Equal(t, models.SandboxProvisioning, got.State)
	})

	t.Run("sandbox not found", func(t *testing.T) {
		store := openTestStore(t)
		updated, err := store.UpdateSandboxState(ctx, 999, models.SandboxProvisioning, models.SandboxBooting)
		require.NoError(t, err)
		assert.False(t, updated)
	})

	t.Run("nil store", func(t *testing.T) {
		updated, err := (*Store)(nil).UpdateSandboxState(ctx, 1, models.SandboxProvisioning, models.SandboxBooting)
		assert.EqualError(t, err, "db store is nil")
		assert.False(t, updated)
	})
}

func TestUpdateSandboxLease(t *testing.T) {
	ctx := context.Background()

	t.Run("success - set lease", func(t *testing.T) {
		store := openTestStore(t)
		sb := testutil.NewTestSandbox(testutil.SandboxOpts{
			VMID:  testutil.TestVMID,
			State: models.SandboxRunning,
		})
		err := store.CreateSandbox(ctx, sb)
		require.NoError(t, err)

		newLease := testutil.FixedTime.Add(time.Hour * 24)
		err = store.UpdateSandboxLease(ctx, testutil.TestVMID, newLease)
		require.NoError(t, err)

		got, err := store.GetSandbox(ctx, testutil.TestVMID)
		require.NoError(t, err)
		assert.Equal(t, newLease, got.LeaseExpires)
	})

	t.Run("success - clear lease", func(t *testing.T) {
		store := openTestStore(t)
		lease := testutil.FixedTime.Add(time.Hour * 24)
		sb := testutil.NewTestSandbox(testutil.SandboxOpts{
			VMID:         testutil.TestVMID,
			State:        models.SandboxRunning,
			LeaseExpires: lease,
		})
		err := store.CreateSandbox(ctx, sb)
		require.NoError(t, err)

		err = store.UpdateSandboxLease(ctx, testutil.TestVMID, time.Time{})
		require.NoError(t, err)

		got, err := store.GetSandbox(ctx, testutil.TestVMID)
		require.NoError(t, err)
		assert.True(t, got.LeaseExpires.IsZero())
	})

	t.Run("nil store", func(t *testing.T) {
		err := (*Store)(nil).UpdateSandboxLease(ctx, 1, time.Now())
		assert.EqualError(t, err, "db store is nil")
	})
}

func TestUpdateSandboxIP(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := openTestStore(t)
		sb := testutil.NewTestSandbox(testutil.SandboxOpts{
			VMID:  testutil.TestVMID,
			State: models.SandboxRunning,
		})
		err := store.CreateSandbox(ctx, sb)
		require.NoError(t, err)

		err = store.UpdateSandboxIP(ctx, testutil.TestVMID, "10.77.0.50")
		require.NoError(t, err)

		got, err := store.GetSandbox(ctx, testutil.TestVMID)
		require.NoError(t, err)
		assert.Equal(t, "10.77.0.50", got.IP)
	})

	t.Run("clear IP", func(t *testing.T) {
		store := openTestStore(t)
		sb := testutil.NewTestSandbox(testutil.SandboxOpts{
			VMID:  testutil.TestVMID,
			State: models.SandboxRunning,
		})
		sb.IP = "10.77.0.50"
		err := store.CreateSandbox(ctx, sb)
		require.NoError(t, err)

		err = store.UpdateSandboxIP(ctx, testutil.TestVMID, "")
		require.NoError(t, err)

		got, err := store.GetSandbox(ctx, testutil.TestVMID)
		require.NoError(t, err)
		assert.Equal(t, "", got.IP)
	})

	t.Run("invalid vmid", func(t *testing.T) {
		store := openTestStore(t)
		err := store.UpdateSandboxIP(ctx, 0, "10.77.0.50")
		assert.EqualError(t, err, "vmid must be positive")
	})

	t.Run("negative vmid", func(t *testing.T) {
		store := openTestStore(t)
		err := store.UpdateSandboxIP(ctx, -1, "10.77.0.50")
		assert.EqualError(t, err, "vmid must be positive")
	})

	t.Run("nil store", func(t *testing.T) {
		err := (*Store)(nil).UpdateSandboxIP(ctx, 1, "10.77.0.50")
		assert.EqualError(t, err, "db store is nil")
	})
}

func TestUpdateSandboxWorkspace(t *testing.T) {
	ctx := context.Background()

	t.Run("success - set workspace", func(t *testing.T) {
		store := openTestStore(t)
		sb := testutil.NewTestSandbox(testutil.SandboxOpts{
			VMID:  testutil.TestVMID,
			State: models.SandboxRunning,
		})
		err := store.CreateSandbox(ctx, sb)
		require.NoError(t, err)

		wsID := "ws-123"
		err = store.UpdateSandboxWorkspace(ctx, testutil.TestVMID, &wsID)
		require.NoError(t, err)

		got, err := store.GetSandbox(ctx, testutil.TestVMID)
		require.NoError(t, err)
		assert.Equal(t, "ws-123", *got.WorkspaceID)
	})

	t.Run("success - clear workspace", func(t *testing.T) {
		store := openTestStore(t)
		wsID := "ws-123"
		sb := testutil.NewTestSandbox(testutil.SandboxOpts{
			VMID:        testutil.TestVMID,
			State:       models.SandboxRunning,
			WorkspaceID: &wsID,
		})
		err := store.CreateSandbox(ctx, sb)
		require.NoError(t, err)

		err = store.UpdateSandboxWorkspace(ctx, testutil.TestVMID, nil)
		require.NoError(t, err)

		got, err := store.GetSandbox(ctx, testutil.TestVMID)
		require.NoError(t, err)
		assert.Nil(t, got.WorkspaceID)
	})

	t.Run("sandbox not found", func(t *testing.T) {
		store := openTestStore(t)
		wsID := "ws-123"
		err := store.UpdateSandboxWorkspace(ctx, 999, &wsID)
		assert.Equal(t, sql.ErrNoRows, err)
	})

	t.Run("invalid vmid", func(t *testing.T) {
		store := openTestStore(t)
		wsID := "ws-123"
		err := store.UpdateSandboxWorkspace(ctx, 0, &wsID)
		assert.EqualError(t, err, "vmid must be positive")
	})

	t.Run("nil store", func(t *testing.T) {
		wsID := "ws-123"
		err := (*Store)(nil).UpdateSandboxWorkspace(ctx, 1, &wsID)
		assert.EqualError(t, err, "db store is nil")
	})
}

func TestRecordEvent(t *testing.T) {
	ctx := context.Background()

	t.Run("success with all fields", func(t *testing.T) {
		store := openTestStore(t)
		vmid := testutil.TestVMID
		jobID := "job-1"

		err := store.RecordEvent(ctx, "sandbox_created", &vmid, &jobID, "sandbox created", `{"foo": "bar"}`)
		require.NoError(t, err)
	})

	t.Run("success with minimal fields", func(t *testing.T) {
		store := openTestStore(t)
		err := store.RecordEvent(ctx, "test_event", nil, nil, "", "")
		require.NoError(t, err)
	})

	t.Run("success with only vmid", func(t *testing.T) {
		store := openTestStore(t)
		vmid := testutil.TestVMID
		err := store.RecordEvent(ctx, "sandbox_event", &vmid, nil, "test", "")
		require.NoError(t, err)
	})

	t.Run("success with only job id", func(t *testing.T) {
		store := openTestStore(t)
		jobID := "job-1"
		err := store.RecordEvent(ctx, "job_event", nil, &jobID, "test", "")
		require.NoError(t, err)
	})

	t.Run("missing kind", func(t *testing.T) {
		store := openTestStore(t)
		err := store.RecordEvent(ctx, "", nil, nil, "", "")
		assert.EqualError(t, err, "event kind is required")
	})

	t.Run("nil store", func(t *testing.T) {
		err := (*Store)(nil).RecordEvent(ctx, "test", nil, nil, "", "")
		assert.EqualError(t, err, "db store is nil")
	})
}
