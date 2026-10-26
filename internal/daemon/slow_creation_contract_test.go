package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

type slowCloneBackend struct {
	*orchestratorBackend
	started chan struct{}
	release chan struct{}
	calls   atomic.Int64
}

func (b *slowCloneBackend) Clone(ctx context.Context, source, target proxmox.VMID, name string) error {
	b.calls.Add(1)
	close(b.started)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.release:
	}
	return b.orchestratorBackend.Clone(ctx, source, target, name)
}

// Deliberately exceeds the dashboard proxy's former 30s timeout; opt in so normal
// unit runs do not acquire a fixed 31s latency. No real backend is involved.
func TestCreateOperationSlowCloneBeyondProxyTimeout(t *testing.T) {
	if os.Getenv("AGENTLAB_SLOW_ACCEPTANCE") != "1" {
		t.Skip("set AGENTLAB_SLOW_ACCEPTANCE=1 for the timed 31s clone contract")
	}
	store := newTestStore(t)
	backend := &slowCloneBackend{orchestratorBackend: &orchestratorBackend{guestIP: "10.77.0.22"}, started: make(chan struct{}), release: make(chan struct{})}
	profiles := map[string]models.Profile{"default": {Name: "default", TemplateVM: 9000}}
	manager := NewSandboxManager(store, backend, nil)
	orchestrator := NewJobOrchestrator(store, profiles, backend, manager, nil, proxmox.SnippetStore{}, "ssh-ed25519 fixture", "", nil, nil, nil).WithNativeSSHKeys(true)
	api := NewControlAPI(store, profiles, manager, nil, orchestrator, "", nil)
	mux := http.NewServeMux()
	api.Register(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := server.Client()
	client.Timeout = 2 * time.Second
	post := func() V1OperationResponse {
		t.Helper()
		request, err := http.NewRequest("POST", server.URL+"/v1/sandboxes?async=true", strings.NewReader(`{"profile":"default"}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Idempotency-Key", "slow-clone")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != 202 && response.StatusCode != 200 {
			t.Fatalf("operation response: %d", response.StatusCode)
		}
		var op V1OperationResponse
		if err := json.NewDecoder(response.Body).Decode(&op); err != nil {
			t.Fatal(err)
		}
		return op
	}
	initial := post()
	select {
	case <-backend.started:
	case <-time.After(5 * time.Second):
		t.Fatal("clone did not start")
	}
	defer func() {
		select {
		case <-backend.release:
		default:
			close(backend.release)
		}
	}()
	started := time.Now()
	timer := time.NewTimer(31 * time.Second)
	defer timer.Stop()
	<-timer.C
	replay := post()
	if time.Since(started) < 30*time.Second || replay.ID != initial.ID || replay.State != "accepted" || replay.VMID == nil || backend.calls.Load() != 1 {
		t.Fatalf("slow replay lost identity/allocation: %+v clones=%d", replay, backend.calls.Load())
	}
	// A new controller has no live worker: it reports the allocated resource as
	// unknown without starting a second clone or concealing the allocation.
	restarted := NewControlAPI(store, profiles, manager, nil, orchestrator, "", nil)
	response := httptest.NewRecorder()
	restarted.handleOperation(response, httptest.NewRequest("GET", "/v1/operations/"+initial.ID, nil))
	var recovered V1OperationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.State != "unknown" || recovered.VMID == nil || *recovered.VMID != *replay.VMID {
		t.Fatalf("restart lost allocation: %s", response.Body.String())
	}
	close(backend.release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		op, err := store.GetOperation(context.Background(), initial.ID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("clone did not finish: %+v", op)
		}
		time.Sleep(10 * time.Millisecond)
	}
	final := post()
	sandboxes, err := store.ListSandboxes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if final.State != "succeeded" || len(sandboxes) != 1 || backend.calls.Load() != 1 {
		t.Fatalf("duplicate allocation: %+v sandboxes=%d clones=%d", final, len(sandboxes), backend.calls.Load())
	}
}
