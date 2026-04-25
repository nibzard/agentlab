package daemon

import (
	"net/http"

	"github.com/agentlab/agentlab/internal/pool"
)

// PoolAPI exposes resource pool status over the control API.
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
	status := api.pool.Status()
	writeJSON(w, http.StatusOK, status)
}

// V1PoolStatusResponse is the JSON envelope returned by GET /v1/pool/status.
// It mirrors pool.Status directly for API consistency.
type V1PoolStatusResponse = pool.Status
