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

func TestExposureCRUD(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, time.February, 8, 16, 0, 0, 0, time.UTC)

	sandbox := models.Sandbox{
		VMID:          900,
		Name:          "exposure-sb",
		Profile:       "default",
		State:         models.SandboxRunning,
		IP:            "10.77.0.50",
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	require.NoError(t, store.CreateSandbox(ctx, sandbox))

	exposure := Exposure{
		Name:      "web-900",
		VMID:      900,
		Port:      8080,
		TargetIP:  "10.77.0.50",
		State:     "requested",
		CreatedAt: now,
		UpdatedAt: now,
	}
	require.NoError(t, store.CreateExposure(ctx, exposure))

	got, err := store.GetExposure(ctx, "web-900")
	require.NoError(t, err)
	assert.Equal(t, exposure.Name, got.Name)
	assert.Equal(t, exposure.VMID, got.VMID)
	assert.Equal(t, exposure.Port, got.Port)
	assert.Equal(t, exposure.TargetIP, got.TargetIP)
	assert.Equal(t, exposure.State, got.State)
	assert.True(t, got.CreatedAt.Equal(now))
	assert.True(t, got.UpdatedAt.Equal(now))

	list, err := store.ListExposures(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "web-900", list[0].Name)

	require.NoError(t, store.DeleteExposure(ctx, "web-900"))

	_, err = store.GetExposure(ctx, "web-900")
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

func TestListExposuresByVMID(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, time.February, 8, 17, 0, 0, 0, time.UTC)

	sandbox := models.Sandbox{
		VMID:          901,
		Name:          "exposure-sb-901",
		Profile:       "default",
		State:         models.SandboxRunning,
		IP:            "10.77.0.51",
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	require.NoError(t, store.CreateSandbox(ctx, sandbox))
	other := models.Sandbox{
		VMID:          902,
		Name:          "exposure-sb-902",
		Profile:       "default",
		State:         models.SandboxRunning,
		IP:            "10.77.0.52",
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	require.NoError(t, store.CreateSandbox(ctx, other))

	require.NoError(t, store.CreateExposure(ctx, Exposure{
		Name:      "web-901",
		VMID:      901,
		Port:      8080,
		TargetIP:  "10.77.0.51",
		State:     "requested",
		CreatedAt: now,
		UpdatedAt: now,
	}))
	require.NoError(t, store.CreateExposure(ctx, Exposure{
		Name:      "ssh-901",
		VMID:      901,
		Port:      22,
		TargetIP:  "10.77.0.51",
		State:     "requested",
		CreatedAt: now.Add(1 * time.Minute),
		UpdatedAt: now.Add(1 * time.Minute),
	}))
	require.NoError(t, store.CreateExposure(ctx, Exposure{
		Name:      "web-902",
		VMID:      902,
		Port:      8080,
		TargetIP:  "10.77.0.52",
		State:     "requested",
		CreatedAt: now,
		UpdatedAt: now,
	}))

	list, err := store.ListExposuresByVMID(ctx, 901)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "ssh-901", list[0].Name)
	assert.Equal(t, "web-901", list[1].Name)
}

func TestCreateExposureValidation(t *testing.T) {
	ctx := context.Background()

	t.Run("nil store", func(t *testing.T) {
		err := (*Store)(nil).CreateExposure(ctx, Exposure{Name: "x"})
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("missing name", func(t *testing.T) {
		store := openTestStore(t)
		err := store.CreateExposure(ctx, Exposure{VMID: 1, Port: 80, TargetIP: "10.77.0.1", State: "requested"})
		assert.EqualError(t, err, "exposure name is required")
	})

	t.Run("invalid vmid", func(t *testing.T) {
		store := openTestStore(t)
		err := store.CreateExposure(ctx, Exposure{Name: "x", VMID: 0, Port: 80, TargetIP: "10.77.0.1", State: "requested"})
		assert.EqualError(t, err, "exposure vmid must be positive")
	})

	t.Run("invalid port", func(t *testing.T) {
		store := openTestStore(t)
		err := store.CreateExposure(ctx, Exposure{Name: "x", VMID: 1, Port: 70000, TargetIP: "10.77.0.1", State: "requested"})
		assert.EqualError(t, err, "exposure port must be between 1 and 65535")
	})

	t.Run("missing target ip", func(t *testing.T) {
		store := openTestStore(t)
		err := store.CreateExposure(ctx, Exposure{Name: "x", VMID: 1, Port: 80, TargetIP: "", State: "requested"})
		assert.EqualError(t, err, "exposure target_ip is required")
	})

	t.Run("missing state", func(t *testing.T) {
		store := openTestStore(t)
		err := store.CreateExposure(ctx, Exposure{Name: "x", VMID: 1, Port: 80, TargetIP: "10.77.0.1", State: ""})
		assert.EqualError(t, err, "exposure state is required")
	})
}

func TestDeleteExposureMissing(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	err := store.DeleteExposure(ctx, "missing")
	assert.ErrorIs(t, err, sql.ErrNoRows)
}
