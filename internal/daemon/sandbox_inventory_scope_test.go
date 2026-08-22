package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

// TestSandboxInventoryScopedTokenSeesOnlyOwnSandbox covers review F10, task
// T26: the inventory route filters records with sandboxScopeFilter, and scoped
// callers never see unmanaged Proxmox VM records.
func TestSandboxInventoryScopedTokenSeesOnlyOwnSandbox(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	for _, vmid := range []int{1001, 1002} {
		if err := store.CreateSandbox(context.Background(), models.Sandbox{
			VMID:          vmid,
			Name:          "sb",
			Profile:       "default",
			State:         models.SandboxRunning,
			CreatedAt:     now,
			LastUpdatedAt: now,
		}); err != nil {
			t.Fatalf("create sandbox %d: %v", vmid, err)
		}
	}

	backend := &stubBackend{listVMs: []proxmox.VMSummary{
		{VMID: 1001, Name: "sb-1001", Status: proxmox.StatusRunning},
		{VMID: 1002, Name: "sb-1002", Status: proxmox.StatusRunning},
		{VMID: 2001, Name: "untracked", Status: proxmox.StatusStopped},
	}}
	api := NewControlAPI(store, map[string]models.Profile{}, nil, nil, nil, "", log.New(io.Discard, "", 0)).
		WithBackend(backend)

	mux := http.NewServeMux()
	api.Register(mux)

	getInventory := func(t *testing.T, id *auth.RequestIdentity) V1SandboxInventoryResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/sandboxes/inventory", nil)
		if id != nil {
			req = req.WithContext(auth.WithIdentity(req.Context(), id))
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /v1/sandboxes/inventory: status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
		}
		var resp V1SandboxInventoryResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return resp
	}

	scoped := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{
		Commands: []string{"sandbox.list"},
		Scope:    []string{"sandbox:1001"},
	}}}

	resp := getInventory(t, scoped)
	if len(resp.Sandboxes) != 1 {
		t.Fatalf("scoped inventory returned %d records, want 1: %+v", len(resp.Sandboxes), resp.Sandboxes)
	}
	entry := resp.Sandboxes[0]
	if entry.VMID != 1001 || !entry.Managed {
		t.Fatalf("scoped record = {vmid %d, managed %v}, want {1001, true}", entry.VMID, entry.Managed)
	}

	// Unscoped callers keep the full inventory, unmanaged VM included.
	full := getInventory(t, nil)
	if len(full.Sandboxes) != 3 {
		t.Fatalf("unscoped inventory returned %d records, want 3: %+v", len(full.Sandboxes), full.Sandboxes)
	}
	managed := 0
	for _, e := range full.Sandboxes {
		if e.Managed {
			managed++
		}
	}
	if managed != 2 {
		t.Fatalf("unscoped managed records = %d, want 2", managed)
	}
}
