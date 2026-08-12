package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/pool"
)

func allocProfiles() map[string]models.Profile {
	return map[string]models.Profile{
		"big": {Name: "big", RawYAML: "resources:\n  cores: 4\n  memory_mb: 4096\n"},
		"small": {
			Name:    "small",
			RawYAML: "resources:\n  cores: 2\n  memory_mb: 2048\n  allow_burst: true\n",
		},
		"none": {Name: "none", RawYAML: ""}, // no resource footprint
	}
}

func enabledPool() *pool.Pool {
	return pool.New(pool.Config{TotalCores: 10, TotalMemoryMB: 10240, CPUOverCommit: 1, MemoryOverCommit: 1})
}

// TestCreateSandboxWithRetry_CommitsFinalVMID verifies that on a VMID
// collision the pool allocation is committed for the FINAL (successfully
// created) VMID, not the collided one (review H3).
func TestCreateSandboxWithRetry_CommitsFinalVMID(t *testing.T) {
	store := newTestStore(t)
	// Seed a sandbox occupying VMID 1001 to force a collision.
	if err := store.CreateSandbox(context.Background(), models.Sandbox{
		VMID: 1001, Name: "occupied", Profile: "small",
		State: models.SandboxReady, CreatedAt: time.Now(), LastUpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	p := enabledPool()
	profiles := allocProfiles()

	sb := models.Sandbox{
		VMID: 1001, Name: "sandbox-1001", Profile: "small",
		State: models.SandboxRequested, CreatedAt: time.Now(), LastUpdatedAt: time.Now(),
	}
	created, err := createSandboxWithRetry(context.Background(), store, sb, p, profiles)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.VMID != 1002 {
		t.Fatalf("created VMID=%d want 1002 (collision retry)", created.VMID)
	}
	// Allocation must be keyed to the final VMID.
	if _, ok := p.Get(1001); ok {
		t.Fatal("pool leaked allocation for collided VMID 1001")
	}
	a, ok := p.Get(1002)
	if !ok {
		t.Fatal("pool has no allocation for final VMID 1002")
	}
	if a.Cores != 2 || a.MemoryMB != 2048 {
		t.Fatalf("allocation cores=%d mem=%d want 2/2048", a.Cores, a.MemoryMB)
	}
}

// TestCreateSandboxWithRetry_RollbackOnReserveFailure verifies that when the
// pool refuses the allocation (capacity exhausted), no sandbox row is created
// and no allocation is leaked (review H3).
func TestCreateSandboxWithRetry_RollbackOnReserveFailure(t *testing.T) {
	store := newTestStore(t)
	// Pool with capacity smaller than the profile's footprint.
	p := pool.New(pool.Config{TotalCores: 2, TotalMemoryMB: 1024, CPUOverCommit: 1, MemoryOverCommit: 1})
	profiles := allocProfiles()

	sb := models.Sandbox{
		VMID: 2001, Name: "sandbox-2001", Profile: "big", // needs 4 cores > 2 limit
		State: models.SandboxRequested, CreatedAt: time.Now(), LastUpdatedAt: time.Now(),
	}
	_, err := createSandboxWithRetry(context.Background(), store, sb, p, profiles)
	if err == nil {
		t.Fatal("expected allocation failure, got nil")
	}
	if _, err := store.GetSandbox(context.Background(), 2001); err == nil {
		t.Fatal("sandbox row should not exist after failed allocation")
	}
	for _, id := range []int{2001, 2002, 2003} {
		if _, ok := p.Get(id); ok {
			t.Fatalf("pool leaked allocation for VMID %d", id)
		}
	}
}

// TestReservePoolForSandbox_NoOpWhenDisabled verifies the pool is bypassed when
// nil or in pass-through mode, so disabling the pool keeps current behavior.
func TestReservePoolForSandbox_NoOpWhenDisabled(t *testing.T) {
	profiles := allocProfiles()
	// nil pool.
	if err := reservePoolForSandbox(nil, 1, "n", "big", profiles); err != nil {
		t.Fatalf("nil pool: %v", err)
	}
	// Pass-through (zero total resources) pool.
	pt := pool.New(pool.Config{})
	if err := reservePoolForSandbox(pt, 1, "n", "big", profiles); err != nil {
		t.Fatalf("pass-through pool: %v", err)
	}
	if _, ok := pt.Get(1); ok {
		t.Fatal("pass-through pool recorded an allocation")
	}
	// Profile with no resource footprint is a no-op even on an enabled pool.
	en := enabledPool()
	if err := reservePoolForSandbox(en, 1, "n", "none", profiles); err != nil {
		t.Fatalf("footprintless profile: %v", err)
	}
	if _, ok := en.Get(1); ok {
		t.Fatal("enabled pool recorded allocation for footprintless profile")
	}
}

// TestReconstructPool verifies the pool is rebuilt from live sandbox rows after
// a restart, skipping destroyed rows (review H3).
func TestReconstructPool(t *testing.T) {
	store := newTestStore(t)
	profiles := allocProfiles()
	now := time.Now()
	for _, c := range []struct {
		vmid  int
		state models.SandboxState
	}{
		{3001, models.SandboxRunning},
		{3002, models.SandboxReady},
		{3003, models.SandboxDestroyed}, // must be skipped
		{3004, models.SandboxRequested},
	} {
		if err := store.CreateSandbox(context.Background(), models.Sandbox{
			VMID: c.vmid, Name: "sb", Profile: "small", State: c.state,
			CreatedAt: now, LastUpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed %d: %v", c.vmid, err)
		}
	}
	p := enabledPool()
	n, err := ReconstructPool(context.Background(), p, store, profiles)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if n != 3 {
		t.Fatalf("reconstructed %d, want 3 (Destroyed skipped)", n)
	}
	for _, id := range []int{3001, 3002, 3004} {
		if _, ok := p.Get(id); !ok {
			t.Errorf("expected allocation for live sandbox %d", id)
		}
	}
	if _, ok := p.Get(3003); ok {
		t.Error("destroyed sandbox 3003 should not be reconstructed")
	}
}

// TestPool_PeekExpiredBurstDoesNotDelete verifies the burst reclaimer primitive
// returns expired IDs without dropping accounting, so the caller can destroy the
// VM first and Release only on success (review H3).
func TestPool_PeekExpiredBurstDoesNotDelete(t *testing.T) {
	// TotalCores=10, over-commit 1x → commit limit 10 cores, burst hard limit 20.
	// Allocating 12 cores with burst=true exceeds the commit limit, so the
	// allocation is marked Burst and given an expiry.
	p := pool.New(pool.Config{TotalCores: 10, TotalMemoryMB: 10240, CPUOverCommit: 1, MemoryOverCommit: 1, BurstDuration: time.Minute})
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p = p.WithNow(func() time.Time { return base })
	if err := p.Allocate(4001, "burst", "small", 12, 2048, true); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	// Advance the clock past the burst expiry.
	p = p.WithNow(func() time.Time { return base.Add(2 * time.Minute) })

	expired := p.PeekExpiredBurst()
	if len(expired) != 1 || expired[0] != 4001 {
		t.Fatalf("expired=%v, want [4001]", expired)
	}
	// Peek must not have removed anything.
	if _, ok := p.Get(4001); !ok {
		t.Fatal("PeekExpiredBurst dropped accounting before destruction")
	}
	// After Release, it is gone.
	_ = p.Release(4001)
	if _, ok := p.Get(4001); ok {
		t.Fatal("Release did not drop allocation")
	}
}
