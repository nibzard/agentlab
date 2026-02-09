package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
)

func TestJobCreateWithWorkspaceSelection(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &orchestratorBackend{}
	workspaceMgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))
	workspace, err := workspaceMgr.Create(ctx, "dev", "local-zfs", 10)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	profiles := map[string]models.Profile{
		"default": {Name: "default", TemplateVM: 9000},
	}
	api := NewControlAPI(store, profiles, nil, workspaceMgr, &JobOrchestrator{}, "", nil)

	workspaceRef := workspace.Name
	req := V1JobCreateRequest{
		RepoURL:     "https://example.com/repo.git",
		Profile:     "default",
		Task:        "run tests",
		WorkspaceID: &workspaceRef,
	}
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(payload))
	api.handleJobCreate(rec, r)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp V1JobResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.WorkspaceID == nil || *resp.WorkspaceID != workspace.ID {
		t.Fatalf("expected workspace_id %s, got %#v", workspace.ID, resp.WorkspaceID)
	}
	job, err := store.GetJob(ctx, resp.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.WorkspaceID == nil || *job.WorkspaceID != workspace.ID {
		t.Fatalf("expected job workspace_id %s, got %#v", workspace.ID, job.WorkspaceID)
	}
	updatedWorkspace, err := store.GetWorkspace(ctx, workspace.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	expectedOwner := workspaceLeaseOwnerForJob(resp.ID)
	if updatedWorkspace.LeaseOwner != expectedOwner {
		t.Fatalf("expected lease owner %s, got %s", expectedOwner, updatedWorkspace.LeaseOwner)
	}
	if updatedWorkspace.LeaseExpires.IsZero() {
		t.Fatalf("expected lease expires set")
	}
}

func TestJobCreateWorkspaceConflict(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &orchestratorBackend{}
	workspaceMgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))
	workspace, err := workspaceMgr.Create(ctx, "dev", "local-zfs", 10)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	leaseOwner := workspaceLeaseOwnerForJob("job-existing")
	leaseNonce := "nonce-existing"
	if _, err := store.TryAcquireWorkspaceLease(ctx, workspace.ID, leaseOwner, leaseNonce, time.Now().UTC().Add(10*time.Minute)); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	profiles := map[string]models.Profile{
		"default": {Name: "default", TemplateVM: 9000},
	}
	api := NewControlAPI(store, profiles, nil, workspaceMgr, &JobOrchestrator{}, "", nil)

	workspaceRef := workspace.ID
	req := V1JobCreateRequest{
		RepoURL:     "https://example.com/repo.git",
		Profile:     "default",
		Task:        "run tests",
		WorkspaceID: &workspaceRef,
	}
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(payload))
	api.handleJobCreate(rec, r)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	details := resp["details"]
	if !strings.Contains(details, "lease_owner="+leaseOwner) {
		t.Fatalf("expected details to include lease_owner, got %q", details)
	}
}

func TestJobCreateWorkspaceWait(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &orchestratorBackend{}
	workspaceMgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))
	workspace, err := workspaceMgr.Create(ctx, "dev", "local-zfs", 10)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	leaseOwner := workspaceLeaseOwnerForJob("job-existing")
	leaseNonce := "nonce-wait"
	if _, err := store.TryAcquireWorkspaceLease(ctx, workspace.ID, leaseOwner, leaseNonce, time.Now().UTC().Add(10*time.Minute)); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		_, _ = store.ReleaseWorkspaceLease(ctx, workspace.ID, leaseOwner, leaseNonce)
	}()

	profiles := map[string]models.Profile{
		"default": {Name: "default", TemplateVM: 9000},
	}
	api := NewControlAPI(store, profiles, nil, workspaceMgr, &JobOrchestrator{}, "", nil)

	waitSeconds := 2
	workspaceRef := workspace.ID
	req := V1JobCreateRequest{
		RepoURL:              "https://example.com/repo.git",
		Profile:              "default",
		Task:                 "run tests",
		WorkspaceID:          &workspaceRef,
		WorkspaceWaitSeconds: &waitSeconds,
	}
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(payload))
	api.handleJobCreate(rec, r)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp V1JobResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.WorkspaceID == nil || *resp.WorkspaceID != workspace.ID {
		t.Fatalf("expected workspace_id %s, got %#v", workspace.ID, resp.WorkspaceID)
	}
}
