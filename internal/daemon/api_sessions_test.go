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

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

func TestSessionResumeStopLifecycle(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &orchestratorBackend{guestIP: "10.77.0.99"}
	workspaceMgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))
	sandboxMgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0)).WithWorkspaceManager(workspaceMgr)
	snippetStore := proxmox.SnippetStore{Dir: t.TempDir()}
	profiles := map[string]models.Profile{
		"default": {Name: "default", TemplateVM: 9000},
	}
	orchestrator := NewJobOrchestrator(
		store,
		profiles,
		backend,
		sandboxMgr,
		workspaceMgr,
		snippetStore,
		"ssh-rsa AAAA",
		"http://10.77.0.1:8844",
		log.New(io.Discard, "", 0),
		nil,
		nil,
	)
	api := NewControlAPI(store, profiles, sandboxMgr, workspaceMgr, orchestrator, "", log.New(io.Discard, "", 0))

	workspace, err := workspaceMgr.Create(ctx, "dev-session", "local-zfs", 10)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workspaceRef := workspace.ID
	req := V1SessionCreateRequest{
		Name:        "dev-session",
		Profile:     "default",
		WorkspaceID: &workspaceRef,
	}
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(payload))
	api.handleSessionCreate(rec, r)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created V1SessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("expected session id")
	}

	leaseOwner := workspaceLeaseOwnerForSession(created.ID)
	updatedWorkspace, err := store.GetWorkspace(ctx, workspace.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if updatedWorkspace.LeaseOwner != leaseOwner {
		t.Fatalf("expected lease owner %s, got %s", leaseOwner, updatedWorkspace.LeaseOwner)
	}

	rec = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/resume", http.NoBody)
	api.handleSessionResume(rec, r, created.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resumed V1SessionResumeResponse
	if err := json.NewDecoder(rec.Body).Decode(&resumed); err != nil {
		t.Fatalf("decode resume response: %v", err)
	}
	if resumed.Session.CurrentVMID == nil || *resumed.Session.CurrentVMID <= 0 {
		t.Fatalf("expected current_vmid set after resume")
	}
	if resumed.Sandbox.VMID != *resumed.Session.CurrentVMID {
		t.Fatalf("expected sandbox vmid %d, got %d", *resumed.Session.CurrentVMID, resumed.Sandbox.VMID)
	}

	workspaceAfterResume, err := store.GetWorkspace(ctx, workspace.ID)
	if err != nil {
		t.Fatalf("get workspace after resume: %v", err)
	}
	if workspaceAfterResume.AttachedVM == nil || *workspaceAfterResume.AttachedVM != resumed.Sandbox.VMID {
		t.Fatalf("expected workspace attached to vmid %d", resumed.Sandbox.VMID)
	}

	rec = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/stop", http.NoBody)
	api.handleSessionStop(rec, r, created.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var stopped V1SessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&stopped); err != nil {
		t.Fatalf("decode stop response: %v", err)
	}
	if stopped.CurrentVMID != nil {
		t.Fatalf("expected current_vmid cleared after stop")
	}

	workspaceAfterStop, err := store.GetWorkspace(ctx, workspace.ID)
	if err != nil {
		t.Fatalf("get workspace after stop: %v", err)
	}
	if workspaceAfterStop.AttachedVM != nil {
		t.Fatalf("expected workspace detached after stop")
	}
	if workspaceAfterStop.LeaseOwner != leaseOwner {
		t.Fatalf("expected lease owner %s after stop, got %s", leaseOwner, workspaceAfterStop.LeaseOwner)
	}
}
