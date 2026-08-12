package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/pool"
)

func nextSandboxVMID(ctx context.Context, store *db.Store) (int, error) {
	if store == nil {
		return 0, errors.New("store is nil")
	}
	maxVMID, err := store.MaxSandboxVMID(ctx)
	if err != nil {
		return 0, err
	}
	if maxVMID < defaultSandboxVMIDStart {
		return defaultSandboxVMIDStart, nil
	}
	return maxVMID + 1, nil
}

// reservePoolForSandbox reserves pool resources for a sandbox under vmid. It is
// a no-op when the pool is nil/disabled or the profile specifies no resource
// footprint. Centralizing reservation here means every creation path (manual
// and job-created) routes through one owner (review H3).
func reservePoolForSandbox(p *pool.Pool, vmid int, name, profileName string, profiles map[string]models.Profile) error {
	if p == nil || !p.IsEnabled() {
		return nil
	}
	cores, memoryMB, allowBurst := 0, 0, false
	if prof, ok := profiles[profileName]; ok {
		cores, memoryMB, allowBurst = profileResourceAlloc(prof)
	}
	if cores == 0 && memoryMB == 0 {
		return nil
	}
	return p.Allocate(vmid, name, profileName, cores, memoryMB, allowBurst)
}

// releasePoolForSandbox releases any pool allocation for vmid. No-op when the
// pool is nil. Errors are intentionally swallowed: Release reports
// ErrAllocationNotFound when there was nothing to release, which is expected on
// bypass/destroy paths.
func releasePoolForSandbox(p *pool.Pool, vmid int) {
	if p == nil {
		return
	}
	_ = p.Release(vmid)
}

// createSandboxWithRetry allocates a unique VMID and creates the sandbox row.
// When a resource pool is configured, the pool allocation is committed for the
// FINAL (successfully created) VMID: on a VMID collision the prior attempt's
// reservation is released and the next VMID is reserved before retrying. Every
// failure path rolls the in-flight reservation back, so a failed create cannot
// leak a phantom allocation (review H3).
func createSandboxWithRetry(ctx context.Context, store *db.Store, sandbox models.Sandbox, p *pool.Pool, profiles map[string]models.Profile) (models.Sandbox, error) {
	if store == nil {
		return models.Sandbox{}, errors.New("store is nil")
	}
	attempt := sandbox
	reserved := false
	reservedVMID := 0
	for i := 0; i < 5; i++ {
		// (Re)reserve for the current attempt's VMID, releasing any reservation
		// keyed to a collided VMID first.
		if !reserved || reservedVMID != attempt.VMID {
			if reserved {
				releasePoolForSandbox(p, reservedVMID)
			}
			if err := reservePoolForSandbox(p, attempt.VMID, attempt.Name, attempt.Profile, profiles); err != nil {
				reserved = false
				return models.Sandbox{}, err
			}
			reserved = true
			reservedVMID = attempt.VMID
		}
		err := store.CreateSandbox(ctx, attempt)
		if err == nil {
			// Allocation is committed for attempt.VMID, which matches the row.
			return attempt, nil
		}
		if !isUniqueConstraint(err) {
			releasePoolForSandbox(p, reservedVMID)
			reserved = false
			return models.Sandbox{}, err
		}
		attempt.VMID++
		if attempt.Name == sandbox.Name {
			attempt.Name = fmt.Sprintf("sandbox-%d", attempt.VMID)
		}
	}
	// Exhausted retries: release the last reservation.
	releasePoolForSandbox(p, reservedVMID)
	return models.Sandbox{}, errors.New("vmid allocation failed")
}

// ReconstructPool rebuilds in-memory pool allocations from live sandbox rows so
// a daemon restart does not lose capacity accounting. Only non-destroyed
// sandboxes whose profile carries a resource footprint are re-counted.
// Reconstruction is best-effort: a single over-commit (e.g. from a config
// change) skips that row rather than aborting the sweep (review H3).
func ReconstructPool(ctx context.Context, p *pool.Pool, store *db.Store, profiles map[string]models.Profile) (int, error) {
	if p == nil || !p.IsEnabled() || store == nil {
		return 0, nil
	}
	sandboxes, err := store.ListSandboxes(ctx)
	if err != nil {
		return 0, fmt.Errorf("list sandboxes for pool reconstruction: %w", err)
	}
	count := 0
	for _, sb := range sandboxes {
		if sb.State == models.SandboxDestroyed {
			continue
		}
		cores, memoryMB, allowBurst := 0, 0, false
		if prof, ok := profiles[sb.Profile]; ok {
			cores, memoryMB, allowBurst = profileResourceAlloc(prof)
		}
		if cores == 0 && memoryMB == 0 {
			continue
		}
		if err := p.Allocate(sb.VMID, sb.Name, sb.Profile, cores, memoryMB, allowBurst); err != nil {
			continue
		}
		count++
	}
	return count, nil
}
