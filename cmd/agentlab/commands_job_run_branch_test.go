package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestJobRunBranchCreatesSession(t *testing.T) {
	var gotSessionReq sessionCreateRequest
	var gotJobReq jobCreateRequest
	var createdSession sessionResponse

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
		if createdSession.Name != "" && name == createdSession.Name {
			writeJSON(t, w, http.StatusOK, createdSession)
			return
		}
		writeJSON(t, w, http.StatusNotFound, map[string]string{"error": "session not found"})
	})
	mux.HandleFunc("/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotSessionReq); err != nil {
			t.Fatalf("decode session request: %v", err)
		}
		workspaceID := ""
		if gotSessionReq.WorkspaceCreate != nil {
			workspaceID = gotSessionReq.WorkspaceCreate.Name
		}
		createdSession = sessionResponse{
			ID:          "session-123",
			Name:        gotSessionReq.Name,
			WorkspaceID: workspaceID,
			Profile:     gotSessionReq.Profile,
			Branch:      gotSessionReq.Branch,
			CreatedAt:   "2026-02-10T12:00:00Z",
			UpdatedAt:   "2026-02-10T12:00:00Z",
		}
		writeJSON(t, w, http.StatusCreated, createdSession)
	})
	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotJobReq); err != nil {
			t.Fatalf("decode job request: %v", err)
		}
		resp := jobResponse{
			ID:          "job-branch-1",
			RepoURL:     gotJobReq.RepoURL,
			Ref:         "main",
			Profile:     gotJobReq.Profile,
			Task:        gotJobReq.Task,
			Mode:        "dangerous",
			Status:      "QUEUED",
			Keepalive:   false,
			CreatedAt:   "2026-02-10T12:00:00Z",
			UpdatedAt:   "2026-02-10T12:00:00Z",
			WorkspaceID: gotJobReq.WorkspaceID,
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	err := runJobRun(context.Background(), []string{
		"--repo", "https://github.com/org/repo",
		"--profile", "yolo",
		"--task", "run",
		"--branch", "feature/login",
	}, base)
	if err != nil {
		t.Fatalf("runJobRun() error = %v", err)
	}

	if gotSessionReq.Name != "branch-feature-login" {
		t.Fatalf("session.name = %q, want branch-feature-login", gotSessionReq.Name)
	}
	if gotSessionReq.Profile != "yolo" {
		t.Fatalf("session.profile = %q, want yolo", gotSessionReq.Profile)
	}
	if gotSessionReq.Branch != "feature/login" {
		t.Fatalf("session.branch = %q, want feature/login", gotSessionReq.Branch)
	}
	if gotSessionReq.WorkspaceCreate == nil {
		t.Fatalf("workspace_create should be set for branch session")
	}
	if gotSessionReq.WorkspaceCreate.Name != "branch-feature-login" {
		t.Fatalf("workspace_create.name = %q, want branch-feature-login", gotSessionReq.WorkspaceCreate.Name)
	}
	if gotSessionReq.WorkspaceCreate.SizeGB != defaultStatefulWorkspaceSizeGB {
		t.Fatalf("workspace_create.size_gb = %d, want %d", gotSessionReq.WorkspaceCreate.SizeGB, defaultStatefulWorkspaceSizeGB)
	}
	if gotSessionReq.WorkspaceCreate.Storage != defaultStatefulWorkspaceStorage {
		t.Fatalf("workspace_create.storage = %q, want %q", gotSessionReq.WorkspaceCreate.Storage, defaultStatefulWorkspaceStorage)
	}

	if gotJobReq.WorkspaceID == nil || *gotJobReq.WorkspaceID != createdSession.WorkspaceID {
		t.Fatalf("job.workspace_id = %v, want %s", gotJobReq.WorkspaceID, createdSession.WorkspaceID)
	}
	if gotJobReq.WorkspaceCreate != nil {
		t.Fatalf("job.workspace_create should be nil")
	}
	if gotJobReq.SessionID == nil || *gotJobReq.SessionID != createdSession.ID {
		t.Fatalf("job.session_id = %v, want %s", gotJobReq.SessionID, createdSession.ID)
	}
}
