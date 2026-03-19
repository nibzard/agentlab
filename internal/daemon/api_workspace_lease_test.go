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

	"github.com/agentlab/agentlab/internal/models"
)

func TestWorkspaceLeaseClearAPI(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	workspaceMgr := NewWorkspaceManager(store, nil, log.New(io.Discard, "", 0))
	api := NewControlAPI(store, nil, nil, workspaceMgr, nil, "", log.New(io.Discard, "", 0))
	mux := http.NewServeMux()
	api.Register(mux)

	workspace := models.Workspace{
		ID:           "workspace-lease-clear",
		Name:         "lease-clear",
		Storage:      "local-zfs",
		VolumeID:     "local-zfs:vm-500-disk-1",
		SizeGB:       20,
		LeaseOwner:   "session:branch-main",
		LeaseNonce:   "nonce-clear",
		LeaseExpires: time.Now().UTC().Add(30 * time.Minute),
		CreatedAt:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		LastUpdated:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/workspaces/"+workspace.ID+"/lease/clear", http.NoBody)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp V1WorkspaceLeaseClearResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Cleared {
		t.Fatalf("expected cleared=true")
	}
	if resp.PreviousOwner != workspace.LeaseOwner {
		t.Fatalf("previous_owner = %q, want %q", resp.PreviousOwner, workspace.LeaseOwner)
	}
	if resp.Workspace.ID != workspace.ID {
		t.Fatalf("workspace id = %q, want %q", resp.Workspace.ID, workspace.ID)
	}

	updated, err := store.GetWorkspace(ctx, workspace.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if updated.LeaseOwner != "" || updated.LeaseNonce != "" || !updated.LeaseExpires.IsZero() {
		t.Fatalf("expected lease metadata cleared")
	}
}

func TestWorkspaceLeaseClearAPIAlreadyClear(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	workspaceMgr := NewWorkspaceManager(store, nil, log.New(io.Discard, "", 0))
	api := NewControlAPI(store, nil, nil, workspaceMgr, nil, "", log.New(io.Discard, "", 0))
	mux := http.NewServeMux()
	api.Register(mux)

	workspace := models.Workspace{
		ID:          "workspace-no-lease",
		Name:        "no-lease",
		Storage:     "local-zfs",
		VolumeID:    "local-zfs:vm-501-disk-1",
		SizeGB:      20,
		CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/workspaces/"+workspace.ID+"/lease/clear", http.NoBody)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp V1WorkspaceLeaseClearResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Cleared {
		t.Fatalf("expected cleared=false")
	}
	if resp.PreviousOwner != "" {
		t.Fatalf("previous_owner = %q, want empty", resp.PreviousOwner)
	}
}
