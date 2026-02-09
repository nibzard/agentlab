package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestJobRunWorkspaceExisting(t *testing.T) {
	var gotReq jobCreateRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode job request: %v", err)
		}
		resp := jobResponse{
			ID:        "job-1",
			RepoURL:   gotReq.RepoURL,
			Ref:       "main",
			Profile:   gotReq.Profile,
			Task:      gotReq.Task,
			Mode:      "dangerous",
			Status:    "QUEUED",
			Keepalive: false,
			CreatedAt: "2026-02-09T12:00:00Z",
			UpdatedAt: "2026-02-09T12:00:00Z",
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	err := runJobRun(context.Background(), []string{
		"--repo", "https://github.com/org/repo",
		"--profile", "yolo",
		"--task", "run",
		"--workspace", "my-workspace",
		"--workspace-wait", "45s",
	}, base)
	if err != nil {
		t.Fatalf("runJobRun() error = %v", err)
	}
	if gotReq.WorkspaceID == nil || *gotReq.WorkspaceID != "my-workspace" {
		t.Fatalf("workspace_id = %v, want my-workspace", gotReq.WorkspaceID)
	}
	if gotReq.WorkspaceCreate != nil {
		t.Fatalf("workspace_create should be nil")
	}
	if gotReq.WorkspaceWaitSeconds == nil || *gotReq.WorkspaceWaitSeconds != 45 {
		t.Fatalf("workspace_wait_seconds = %v, want 45", gotReq.WorkspaceWaitSeconds)
	}
}

func TestJobRunWorkspaceCreate(t *testing.T) {
	var gotReq jobCreateRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode job request: %v", err)
		}
		resp := jobResponse{
			ID:        "job-2",
			RepoURL:   gotReq.RepoURL,
			Ref:       "main",
			Profile:   gotReq.Profile,
			Task:      gotReq.Task,
			Mode:      "dangerous",
			Status:    "QUEUED",
			Keepalive: false,
			CreatedAt: "2026-02-09T12:00:00Z",
			UpdatedAt: "2026-02-09T12:00:00Z",
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	err := runJobRun(context.Background(), []string{
		"--repo", "https://github.com/org/repo",
		"--profile", "yolo",
		"--task", "run",
		"--workspace", "new:ws-data",
		"--workspace-size", "80G",
		"--workspace-storage", "local-zfs",
	}, base)
	if err != nil {
		t.Fatalf("runJobRun() error = %v", err)
	}
	if gotReq.WorkspaceID != nil {
		t.Fatalf("workspace_id should be nil")
	}
	if gotReq.WorkspaceCreate == nil {
		t.Fatalf("workspace_create should be set")
	}
	if gotReq.WorkspaceCreate.Name != "ws-data" {
		t.Fatalf("workspace_create.name = %q", gotReq.WorkspaceCreate.Name)
	}
	if gotReq.WorkspaceCreate.SizeGB != 80 {
		t.Fatalf("workspace_create.size_gb = %d", gotReq.WorkspaceCreate.SizeGB)
	}
	if gotReq.WorkspaceCreate.Storage != "local-zfs" {
		t.Fatalf("workspace_create.storage = %q", gotReq.WorkspaceCreate.Storage)
	}
}

func TestJobRunStatefulDefaults(t *testing.T) {
	var gotReq jobCreateRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode job request: %v", err)
		}
		resp := jobResponse{
			ID:        "job-3",
			RepoURL:   gotReq.RepoURL,
			Ref:       "main",
			Profile:   gotReq.Profile,
			Task:      gotReq.Task,
			Mode:      "dangerous",
			Status:    "QUEUED",
			Keepalive: false,
			CreatedAt: "2026-02-09T12:00:00Z",
			UpdatedAt: "2026-02-09T12:00:00Z",
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	err := runJobRun(context.Background(), []string{
		"--repo", "https://github.com/org/mega-repo.git",
		"--profile", "yolo",
		"--task", "run",
		"--stateful",
	}, base)
	if err != nil {
		t.Fatalf("runJobRun() error = %v", err)
	}
	if gotReq.WorkspaceCreate == nil {
		t.Fatalf("workspace_create should be set")
	}
	if gotReq.WorkspaceCreate.Name != "stateful-mega-repo" {
		t.Fatalf("workspace_create.name = %q", gotReq.WorkspaceCreate.Name)
	}
	if gotReq.WorkspaceCreate.SizeGB != defaultStatefulWorkspaceSizeGB {
		t.Fatalf("workspace_create.size_gb = %d", gotReq.WorkspaceCreate.SizeGB)
	}
	if gotReq.WorkspaceCreate.Storage != defaultStatefulWorkspaceStorage {
		t.Fatalf("workspace_create.storage = %q", gotReq.WorkspaceCreate.Storage)
	}
}

func TestJobRunWorkspaceValidation(t *testing.T) {
	base := commonFlags{socketPath: "/tmp/agentlab.sock", jsonOutput: false, timeout: time.Second}
	err := runJobRun(context.Background(), []string{
		"--repo", "https://github.com/org/repo",
		"--profile", "yolo",
		"--task", "run",
		"--workspace-size", "80G",
	}, base)
	if err == nil {
		t.Fatalf("expected error for workspace-size without create")
	}
	err = runJobRun(context.Background(), []string{
		"--repo", "https://github.com/org/repo",
		"--profile", "yolo",
		"--task", "run",
		"--workspace-wait", "2m",
	}, base)
	if err == nil {
		t.Fatalf("expected error for workspace-wait without workspace")
	}
}
