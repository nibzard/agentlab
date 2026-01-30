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

func TestJobCreateAppliesProfileDefaults(t *testing.T) {
	store := newTestStore(t)
	profiles := map[string]models.Profile{
		"default": {
			Name:       "default",
			TemplateVM: 9000,
			RawYAML: "name: default\n" +
				"template_vmid: 9000\n" +
				"behavior:\n" +
				"  keepalive_default: true\n" +
				"  ttl_minutes_default: 45\n",
		},
	}

	api := NewControlAPI(store, profiles, nil, nil, &JobOrchestrator{}, "")
	req := V1JobCreateRequest{
		RepoURL: "https://example.com/repo.git",
		Profile: "default",
		Task:    "run tests",
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
	if resp.TTLMinutes == nil || *resp.TTLMinutes != 45 {
		t.Fatalf("expected ttl_minutes 45, got %#v", resp.TTLMinutes)
	}
	if !resp.Keepalive {
		t.Fatalf("expected keepalive true")
	}

	job, err := store.GetJob(context.Background(), resp.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.TTLMinutes != 45 {
		t.Fatalf("expected ttl_minutes 45, got %d", job.TTLMinutes)
	}
	if !job.Keepalive {
		t.Fatalf("expected keepalive true")
	}
}

func TestSandboxCreateAppliesProfileDefaults(t *testing.T) {
	store := newTestStore(t)
	backend := &orchestratorBackend{guestIP: "10.77.0.99"}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	profiles := map[string]models.Profile{
		"default": {
			Name:       "default",
			TemplateVM: 9000,
			RawYAML: "name: default\n" +
				"template_vmid: 9000\n" +
				"behavior:\n" +
				"  keepalive_default: true\n" +
				"  ttl_minutes_default: 90\n",
		},
	}
	snippetStore := proxmox.SnippetStore{Storage: "local", Dir: t.TempDir()}
	orchestrator := NewJobOrchestrator(store, profiles, backend, manager, nil, snippetStore, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtestkey agent@test", "http://10.77.0.1:8844", log.New(io.Discard, "", 0), nil, nil)
	api := NewControlAPI(store, profiles, manager, nil, orchestrator, "")
	fixed := time.Date(2026, 1, 30, 12, 0, 0, 0, time.UTC)
	api.now = func() time.Time { return fixed }

	req := V1SandboxCreateRequest{Profile: "default"}
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", bytes.NewReader(payload))
	api.handleSandboxCreate(rec, r)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp V1SandboxResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Keepalive {
		t.Fatalf("expected keepalive true")
	}
	expectedLease := fixed.Add(90 * time.Minute).Format(time.RFC3339Nano)
	if resp.LeaseExpires == nil || *resp.LeaseExpires != expectedLease {
		t.Fatalf("expected lease_expires_at %s, got %#v", expectedLease, resp.LeaseExpires)
	}

	sandbox, err := store.GetSandbox(context.Background(), resp.VMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if !sandbox.Keepalive {
		t.Fatalf("expected keepalive true")
	}
	if !sandbox.LeaseExpires.Equal(fixed.Add(90 * time.Minute)) {
		t.Fatalf("expected lease_expires %s, got %s", expectedLease, sandbox.LeaseExpires.UTC().Format(time.RFC3339Nano))
	}
}

func TestWorkspaceRebindAppliesProfileDefaults(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &orchestratorBackend{guestIP: "10.77.0.50"}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	workspaceMgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))
	profiles := map[string]models.Profile{
		"default": {
			Name:       "default",
			TemplateVM: 9000,
			RawYAML: "name: default\n" +
				"template_vmid: 9000\n" +
				"behavior:\n" +
				"  keepalive_default: false\n" +
				"  ttl_minutes_default: 30\n",
		},
	}
	snippetStore := proxmox.SnippetStore{Storage: "local", Dir: t.TempDir()}
	orchestrator := NewJobOrchestrator(store, profiles, backend, manager, workspaceMgr, snippetStore, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtestkey agent@test", "http://10.77.0.1:8844", log.New(io.Discard, "", 0), nil, nil)
	fixed := time.Date(2026, 1, 30, 13, 0, 0, 0, time.UTC)
	orchestrator.now = func() time.Time { return fixed }

	workspace, err := workspaceMgr.Create(ctx, "dev", "local-zfs", 10)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	result, err := orchestrator.RebindWorkspace(ctx, workspace.ID, "default", nil, false)
	if err != nil {
		t.Fatalf("rebind workspace: %v", err)
	}
	if result.Sandbox.Keepalive {
		t.Fatalf("expected keepalive false")
	}
	expectedLease := fixed.Add(30 * time.Minute)
	if !result.Sandbox.LeaseExpires.Equal(expectedLease) {
		t.Fatalf("expected lease_expires %s, got %s", expectedLease.UTC().Format(time.RFC3339Nano), result.Sandbox.LeaseExpires.UTC().Format(time.RFC3339Nano))
	}
}
