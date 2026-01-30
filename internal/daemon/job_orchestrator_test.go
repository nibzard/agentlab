package daemon

import (
	"bytes"
	"context"
	"io"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

type orchestratorBackend struct {
	cloneCalls     []proxmox.VMID
	configureCalls []proxmox.VMConfig
	startCalls     []proxmox.VMID
	destroyCalls   []proxmox.VMID
	guestIP        string
}

func (b *orchestratorBackend) Clone(_ context.Context, _ proxmox.VMID, target proxmox.VMID, _ string) error {
	b.cloneCalls = append(b.cloneCalls, target)
	return nil
}

func (b *orchestratorBackend) Configure(_ context.Context, _ proxmox.VMID, cfg proxmox.VMConfig) error {
	b.configureCalls = append(b.configureCalls, cfg)
	return nil
}

func (b *orchestratorBackend) Start(_ context.Context, vmid proxmox.VMID) error {
	b.startCalls = append(b.startCalls, vmid)
	return nil
}

func (b *orchestratorBackend) Stop(context.Context, proxmox.VMID) error {
	return nil
}

func (b *orchestratorBackend) Destroy(_ context.Context, vmid proxmox.VMID) error {
	b.destroyCalls = append(b.destroyCalls, vmid)
	return nil
}

func (b *orchestratorBackend) Status(context.Context, proxmox.VMID) (proxmox.Status, error) {
	return proxmox.StatusRunning, nil
}

func (b *orchestratorBackend) GuestIP(context.Context, proxmox.VMID) (string, error) {
	return b.guestIP, nil
}

func (b *orchestratorBackend) CreateVolume(context.Context, string, string, int) (string, error) {
	return "local-zfs:workspace", nil
}

func (b *orchestratorBackend) AttachVolume(context.Context, proxmox.VMID, string, string) error {
	return nil
}

func (b *orchestratorBackend) DetachVolume(context.Context, proxmox.VMID, string) error {
	return nil
}

func (b *orchestratorBackend) DeleteVolume(context.Context, string) error {
	return nil
}

func TestJobOrchestratorRun(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &orchestratorBackend{guestIP: "10.77.0.99"}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	profiles := map[string]models.Profile{
		"yolo": {Name: "yolo", TemplateVM: 9000},
	}
	snippetDir := t.TempDir()
	snippetStore := proxmox.SnippetStore{Storage: "local", Dir: snippetDir}
	orchestrator := NewJobOrchestrator(store, profiles, backend, manager, nil, snippetStore, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtestkey agent@test", "http://10.77.0.1:8844", log.New(io.Discard, "", 0), nil)
	orchestrator.rand = bytes.NewReader(bytes.Repeat([]byte{0x01}, 64))
	now := time.Date(2026, 1, 29, 12, 0, 0, 0, time.UTC)
	orchestrator.now = func() time.Time { return now }

	job := models.Job{
		ID:        "job_test",
		RepoURL:   "https://example.com/repo.git",
		Ref:       "main",
		Profile:   "yolo",
		Task:      "run tests",
		Status:    models.JobQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := orchestrator.Run(ctx, job.ID); err != nil {
		t.Fatalf("run job: %v", err)
	}

	updatedJob, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if updatedJob.Status != models.JobRunning {
		t.Fatalf("expected job RUNNING, got %s", updatedJob.Status)
	}
	if updatedJob.SandboxVMID == nil || *updatedJob.SandboxVMID == 0 {
		t.Fatalf("expected sandbox vmid set")
	}
	sandbox, err := store.GetSandbox(ctx, *updatedJob.SandboxVMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sandbox.State != models.SandboxRunning {
		t.Fatalf("expected sandbox RUNNING, got %s", sandbox.State)
	}
	if sandbox.IP != backend.guestIP {
		t.Fatalf("expected sandbox IP %s, got %s", backend.guestIP, sandbox.IP)
	}
	if len(backend.cloneCalls) != 1 || backend.cloneCalls[0] != proxmox.VMID(sandbox.VMID) {
		t.Fatalf("expected clone called for vmid %d", sandbox.VMID)
	}
	if len(backend.configureCalls) != 1 {
		t.Fatalf("expected configure called once")
	}
	if backend.configureCalls[0].CloudInit == "" || !strings.Contains(backend.configureCalls[0].CloudInit, "snippets") {
		t.Fatalf("expected cloud-init snippet path, got %q", backend.configureCalls[0].CloudInit)
	}
	matches, err := filepath.Glob(filepath.Join(snippetDir, "agentlab-*.yaml"))
	if err != nil {
		t.Fatalf("snippet glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected snippet file in %s", snippetDir)
	}
}

func TestJobOrchestratorRejectsHostMountProfile(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &orchestratorBackend{}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	profiles := map[string]models.Profile{
		"yolo": {
			Name:       "yolo",
			TemplateVM: 9000,
			RawYAML: `
name: yolo
template_vmid: 9000
host_mounts:
  - /etc
`,
		},
	}
	orchestrator := NewJobOrchestrator(store, profiles, backend, manager, nil, proxmox.SnippetStore{}, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtestkey agent@test", "http://10.77.0.1:8844", log.New(io.Discard, "", 0), nil)

	now := time.Date(2026, 1, 29, 13, 0, 0, 0, time.UTC)
	job := models.Job{
		ID:        "job_host_mount",
		RepoURL:   "https://example.com/repo.git",
		Ref:       "main",
		Profile:   "yolo",
		Task:      "run tests",
		Status:    models.JobQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	if err := orchestrator.Run(ctx, job.ID); err == nil {
		t.Fatalf("expected error")
	}

	updatedJob, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if updatedJob.Status != models.JobFailed {
		t.Fatalf("expected job FAILED, got %s", updatedJob.Status)
	}
	sandboxes, err := store.ListSandboxes(ctx)
	if err != nil {
		t.Fatalf("list sandboxes: %v", err)
	}
	if len(sandboxes) != 0 {
		t.Fatalf("expected no sandboxes, got %d", len(sandboxes))
	}
	if len(backend.cloneCalls) != 0 {
		t.Fatalf("expected no clone calls, got %d", len(backend.cloneCalls))
	}
}

func TestJobOrchestratorHandleReportComplete(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &orchestratorBackend{}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	profiles := map[string]models.Profile{
		"yolo": {Name: "yolo", TemplateVM: 9000},
	}
	orchestrator := NewJobOrchestrator(store, profiles, backend, manager, nil, proxmox.SnippetStore{}, "", "http://10.77.0.1:8844", log.New(io.Discard, "", 0), nil)

	now := time.Date(2026, 1, 29, 12, 30, 0, 0, time.UTC)
	sandbox := models.Sandbox{
		VMID:          1100,
		Name:          "sandbox-1100",
		Profile:       "yolo",
		State:         models.SandboxRunning,
		Keepalive:     false,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	job := models.Job{
		ID:          "job_complete",
		RepoURL:     "https://example.com/repo.git",
		Ref:         "main",
		Profile:     "yolo",
		Task:        "ship it",
		Status:      models.JobRunning,
		SandboxVMID: &sandbox.VMID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	report := JobReport{
		JobID:   job.ID,
		VMID:    sandbox.VMID,
		Status:  models.JobCompleted,
		Message: "done",
		Artifacts: []V1ArtifactMetadata{{
			Name: "bundle",
			Path: "/var/lib/agentlab/artifacts/job_complete.tar.gz",
		}},
	}
	if err := orchestrator.HandleReport(ctx, report); err != nil {
		t.Fatalf("handle report: %v", err)
	}

	updatedJob, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if updatedJob.Status != models.JobCompleted {
		t.Fatalf("expected job COMPLETED, got %s", updatedJob.Status)
	}
	if updatedJob.ResultJSON == "" || !strings.Contains(updatedJob.ResultJSON, "bundle") {
		t.Fatalf("expected result_json to include artifacts")
	}
	updatedSandbox, err := store.GetSandbox(ctx, sandbox.VMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if updatedSandbox.State != models.SandboxDestroyed {
		t.Fatalf("expected sandbox DESTROYED, got %s", updatedSandbox.State)
	}
}
