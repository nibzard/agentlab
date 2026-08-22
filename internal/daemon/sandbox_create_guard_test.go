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

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

// occupiedVMBackend wraps the orchestrator fake with a fixed Proxmox VM list,
// so create-guard tests can report an occupied VMID while still recording
// clone and destroy calls.
type occupiedVMBackend struct {
	*orchestratorBackend
	vms []proxmox.VMSummary
}

func (b *occupiedVMBackend) ListVMs(context.Context) ([]proxmox.VMSummary, error) {
	out := make([]proxmox.VMSummary, len(b.vms))
	copy(out, b.vms)
	return out, nil
}

func newSandboxCreateGuardAPI(t *testing.T, vms []proxmox.VMSummary) (*ControlAPI, *occupiedVMBackend, *db.Store) {
	t.Helper()
	store := newTestStore(t)
	backend := &occupiedVMBackend{
		orchestratorBackend: &orchestratorBackend{guestIP: "10.77.0.50"},
		vms:                 vms,
	}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	profiles := map[string]models.Profile{
		"default": {
			Name:       "default",
			TemplateVM: 9000,
			RawYAML:    "name: default\ntemplate_vmid: 9000\n",
		},
	}
	snippetStore := proxmox.SnippetStore{Storage: "local", Dir: t.TempDir()}
	orchestrator := NewJobOrchestrator(store, profiles, backend, manager, nil, snippetStore,
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtestkey agent@test", "http://10.77.0.1:8844",
		log.New(io.Discard, "", 0), nil, nil)
	api := NewControlAPI(store, profiles, manager, nil, orchestrator, "", log.New(io.Discard, "", 0)).
		WithBackend(backend)
	return api, backend, store
}

func postSandboxes(t *testing.T, api *ControlAPI, id *auth.RequestIdentity, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	api.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", bytes.NewBufferString(body))
	if id != nil {
		req = req.WithContext(auth.WithIdentity(req.Context(), id))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestSandboxCreateOccupiedProxmoxVMIDRejected covers review F1, task T01: a
// caller-supplied VMID that is occupied in the Proxmox inventory must be
// refused with 409 before any row is inserted and before provisioning runs.
func TestSandboxCreateOccupiedProxmoxVMIDRejected(t *testing.T) {
	api, backend, store := newSandboxCreateGuardAPI(t, []proxmox.VMSummary{
		{VMID: 100, Name: "victim", Status: proxmox.StatusStopped},
	})

	rec := postSandboxes(t, api, nil, `{"profile":"default","vmid":100}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
	var resp V1ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if !bytes.Contains([]byte(resp.Error), []byte("vmid already exists")) {
		t.Fatalf("error = %q, want vmid already exists", resp.Error)
	}

	sandboxes, err := store.ListSandboxes(context.Background())
	if err != nil {
		t.Fatalf("list sandboxes: %v", err)
	}
	if len(sandboxes) != 0 {
		t.Fatalf("expected no sandbox rows, got %d", len(sandboxes))
	}
	if len(backend.cloneCalls) != 0 {
		t.Fatalf("expected no clone calls, got %d", len(backend.cloneCalls))
	}
	if len(backend.destroyCalls) != 0 {
		t.Fatalf("expected no destroy calls, got %d", len(backend.destroyCalls))
	}
}

// TestSandboxCreateVMIDScopeEnforced covers review F1, task T03: the create
// route resolves the requested VMID for the sandbox scope check. A token
// scoped to another sandbox cannot name an out-of-scope VMID, and an in-scope
// request still decodes its body and provisions normally.
func TestSandboxCreateVMIDScopeEnforced(t *testing.T) {
	api, backend, store := newSandboxCreateGuardAPI(t, nil)

	scoped := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{
		Commands: []string{"sandbox.create"},
		Scope:    []string{"sandbox:2000"},
	}}}

	// Out-of-scope VMID: denied before any state change.
	rec := postSandboxes(t, api, scoped, `{"profile":"default","vmid":100}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("out-of-scope create: status = %d, want %d (body %s)", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	sandboxes, err := store.ListSandboxes(context.Background())
	if err != nil {
		t.Fatalf("list sandboxes: %v", err)
	}
	if len(sandboxes) != 0 {
		t.Fatalf("expected no sandbox rows after denial, got %d", len(sandboxes))
	}
	if len(backend.cloneCalls) != 0 {
		t.Fatalf("expected no clone calls after denial, got %d", len(backend.cloneCalls))
	}

	// In-scope VMID: the scope check passes, the buffered body is restored for
	// the handler, and provisioning completes.
	rec = postSandboxes(t, api, scoped, `{"profile":"default","vmid":2000}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("in-scope create: status = %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if len(backend.cloneCalls) != 1 || backend.cloneCalls[0] != proxmox.VMID(2000) {
		t.Fatalf("clone calls = %v, want [2000]", backend.cloneCalls)
	}
	created, err := store.GetSandbox(context.Background(), 2000)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if created.State != models.SandboxRunning {
		t.Fatalf("state = %s, want %s", created.State, models.SandboxRunning)
	}
}

// TestSandboxCreateAutoVMIDScopedTokenAllowed verifies that a scoped token can
// still create a sandbox without a caller-supplied VMID: the resolver reports
// no concrete target, so only the command check applies.
func TestSandboxCreateAutoVMIDScopedTokenAllowed(t *testing.T) {
	api, _, store := newSandboxCreateGuardAPI(t, nil)

	scoped := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{
		Commands: []string{"sandbox.create"},
		Scope:    []string{"sandbox:2000"},
	}}}

	rec := postSandboxes(t, api, scoped, `{"profile":"default"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("auto-vmid create: status = %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var resp V1SandboxResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, err := store.GetSandbox(context.Background(), resp.VMID); err != nil {
		t.Fatalf("get sandbox %d: %v", resp.VMID, err)
	}
}

// TestSandboxCreateVMIDResolver is a unit test for the body-peek resolver: it
// returns the caller-supplied vmid, 0 when absent, and 0 for a body the strict
// decoder would reject, all while leaving the body readable for the handler.
func TestSandboxCreateVMIDResolver(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", bytes.NewBufferString(`{"profile":"default","vmid":142}`))
	if got := sandboxCreateVMID(req); got != 142 {
		t.Fatalf("resolver = %d, want 142", got)
	}
	var after V1SandboxCreateRequest
	if err := decodeJSONPayload(mustReadBody(t, req), &after); err != nil {
		t.Fatalf("handler decode after resolver: %v", err)
	}
	if after.VMID == nil || *after.VMID != 142 {
		t.Fatalf("restored body vmid = %v, want 142", after.VMID)
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/sandboxes", bytes.NewBufferString(`{"profile":"default"}`))
	if got := sandboxCreateVMID(req); got != 0 {
		t.Fatalf("resolver without vmid = %d, want 0", got)
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/sandboxes", bytes.NewBufferString(`not json`))
	if got := sandboxCreateVMID(req); got != 0 {
		t.Fatalf("resolver on malformed body = %d, want 0", got)
	}

	if got := sandboxCreateVMID(httptest.NewRequest(http.MethodPost, "/v1/sandboxes", nil)); got != 0 {
		t.Fatalf("resolver on nil body = %d, want 0", got)
	}
}

func mustReadBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return data
}

// TestSandboxCreateOccupiedCheckSkippedWithoutBackend pins the nil-backend
// behavior: the Proxmox occupancy check cannot run, and nothing else in the
// create path changes for callers that supply a free VMID.
func TestSandboxCreateOccupiedCheckSkippedWithoutBackend(t *testing.T) {
	store := newTestStore(t)
	api := NewControlAPI(store, map[string]models.Profile{}, nil, nil, nil, "", log.New(io.Discard, "", 0))
	if occupied, err := api.proxmoxVMIDOccupied(context.Background(), 100); err != nil || occupied {
		t.Fatalf("nil backend: occupied = %v, err = %v, want false, nil", occupied, err)
	}
}
