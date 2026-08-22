package daemon

import (
	"net/http"

	"github.com/agentlab/agentlab/internal/pool"
)

// PoolAPI exposes resource pool status over the control API.
//
// Status carries no secrets, so a token with the pool.status permission may
// read it. A sandbox-scoped token sees only the allocations inside its scope;
// the aggregates are recomputed from that subset (review F12).
type PoolAPI struct {
	pool *pool.Pool
}

// NewPoolAPI creates a new pool API handler.
func NewPoolAPI(p *pool.Pool) *PoolAPI {
	return &PoolAPI{pool: p}
}

// Register registers pool API routes on the given mux.
func (api *PoolAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/pool/status", api.handlePoolStatus)
}

func (api *PoolAPI) handlePoolStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !authorizeStandalone(w, r, permPoolStatus, false) {
		return
	}
	status := api.pool.Status()
	if filter := sandboxScopeFilter(r); filter != nil {
		status = filterPoolStatus(status, filter)
	}
	writeJSON(w, http.StatusOK, status)
}

// filterPoolStatus narrows a pool status to the allocations accepted by
// filter. Every aggregate is recomputed from the surviving allocations so the
// response stays self-consistent and leaks no cross-sandbox totals.
func filterPoolStatus(status pool.Status, filter func(int) bool) pool.Status {
	kept := make([]pool.Allocation, 0, len(status.Allocations))
	totalCores, totalMemory := 0, 0
	burstCount := 0
	for _, a := range status.Allocations {
		if !filter(a.SandboxID) {
			continue
		}
		kept = append(kept, a)
		totalCores += a.Cores
		totalMemory += a.MemoryMB
		if a.Burst {
			burstCount++
		}
	}

	cpuLimit := status.Config.CommitLimitCPU()
	memLimit := status.Config.CommitLimitMemoryMB()
	var availCores, availMem, utilCPU, utilMem float64
	if cpuLimit > 0 {
		availCores = cpuLimit - float64(totalCores)
		if availCores < 0 {
			availCores = 0
		}
		utilCPU = float64(totalCores) / cpuLimit
	}
	if memLimit > 0 {
		availMem = memLimit - float64(totalMemory)
		if availMem < 0 {
			availMem = 0
		}
		utilMem = float64(totalMemory) / memLimit
	}

	return pool.Status{
		Config:            status.Config,
		AllocatedCores:    totalCores,
		AllocatedMemoryMB: totalMemory,
		AvailableCores:    availCores,
		AvailableMemoryMB: availMem,
		UtilizationCPU:    utilCPU,
		UtilizationMemory: utilMem,
		ActiveCount:       len(kept),
		BurstCount:        burstCount,
		Allocations:       kept,
	}
}

// V1PoolStatusResponse is the JSON envelope returned by GET /v1/pool/status.
// It mirrors pool.Status directly for API consistency.
type V1PoolStatusResponse = pool.Status
