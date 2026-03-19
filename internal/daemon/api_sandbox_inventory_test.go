package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

func TestSandboxInventoryHandler(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	err := store.CreateSandbox(context.Background(), models.Sandbox{
		VMID:          1053,
		Name:          "openclaw",
		Profile:       "yolo",
		State:         models.SandboxDestroyed,
		CreatedAt:     now,
		LastUpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	backend := &stubBackend{
		listVMs: []proxmox.VMSummary{
			{VMID: 1053, Name: "openclaw", Status: proxmox.StatusRunning},
			{VMID: 2001, Name: "scratch-vm", Status: proxmox.StatusStopped},
		},
	}
	api := NewControlAPI(store, map[string]models.Profile{}, nil, nil, nil, "", log.New(io.Discard, "", 0)).
		WithBackend(backend).
		WithTailscalePeerInventory(func(context.Context) (map[string]tailnetPeer, error) {
			return map[string]tailnetPeer{
				"openclaw": {
					HostName:     "openclaw",
					DNSName:      "openclaw.tailnet.ts.net",
					TailscaleIPs: []string{"100.64.0.10"},
				},
			}, nil
		})

	req := httptest.NewRequest(http.MethodGet, "/v1/sandboxes/inventory", nil)
	rec := httptest.NewRecorder()
	api.handleSandboxInventory(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp V1SandboxInventoryResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Sandboxes) != 2 {
		t.Fatalf("expected 2 sandboxes, got %d", len(resp.Sandboxes))
	}

	first := resp.Sandboxes[0]
	if first.VMID != 1053 {
		t.Fatalf("first vmid = %d, want 1053", first.VMID)
	}
	if first.AgentlabState != string(models.SandboxDestroyed) {
		t.Fatalf("agentlab state = %s", first.AgentlabState)
	}
	if first.ProxmoxStatus != string(proxmox.StatusRunning) {
		t.Fatalf("proxmox status = %s", first.ProxmoxStatus)
	}
	if len(first.Drift) == 0 || first.Drift[0] != sandboxDriftRestoredAfterDestroy {
		t.Fatalf("drift = %#v", first.Drift)
	}
	if first.TailscaleDNS != "openclaw.tailnet.ts.net" {
		t.Fatalf("tailscale dns = %q", first.TailscaleDNS)
	}
	if len(first.TailscaleIPs) != 1 || first.TailscaleIPs[0] != "100.64.0.10" {
		t.Fatalf("tailscale ips = %#v", first.TailscaleIPs)
	}

	second := resp.Sandboxes[1]
	if second.VMID != 2001 {
		t.Fatalf("second vmid = %d, want 2001", second.VMID)
	}
	if second.Managed {
		t.Fatalf("expected unmanaged proxmox vm")
	}
	if len(second.Drift) == 0 || second.Drift[0] != sandboxDriftUnmanagedProxmoxVM {
		t.Fatalf("second drift = %#v", second.Drift)
	}
}

func TestSandboxReconcileApplyAdoptsRestoredSandbox(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	err := store.CreateSandbox(context.Background(), models.Sandbox{
		VMID:          1053,
		Name:          "openclaw",
		Profile:       "yolo",
		State:         models.SandboxDestroyed,
		LeaseExpires:  now.Add(-time.Hour),
		CreatedAt:     now.Add(-2 * time.Hour),
		LastUpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	backend := &stubBackend{
		listVMs: []proxmox.VMSummary{
			{VMID: 1053, Name: "openclaw", Status: proxmox.StatusRunning},
		},
		guestIP: "10.77.0.195",
	}
	api := NewControlAPI(store, map[string]models.Profile{}, nil, nil, nil, "", log.New(io.Discard, "", 0)).WithBackend(backend)

	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/reconcile", bytes.NewBufferString(`{"apply":true}`))
	rec := httptest.NewRecorder()
	api.handleSandboxReconcile(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp V1SandboxReconcileResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.DryRun {
		t.Fatalf("expected apply response")
	}
	if resp.Reconciled != 1 {
		t.Fatalf("reconciled = %d, want 1", resp.Reconciled)
	}

	sb, err := store.GetSandbox(context.Background(), 1053)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.State != models.SandboxRunning {
		t.Fatalf("state = %s, want %s", sb.State, models.SandboxRunning)
	}
	if !sb.LeaseExpires.IsZero() {
		t.Fatalf("expected cleared lease, got %s", sb.LeaseExpires)
	}
	if sb.IP != "10.77.0.195" {
		t.Fatalf("ip = %q, want 10.77.0.195", sb.IP)
	}
}
