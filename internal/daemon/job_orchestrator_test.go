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
	snapshotCalls  []snapshotCall
	guestIP        string
	blockClone     bool
	blockConfigure bool
	blockStart     bool
}

type snapshotCall struct {
	vmid proxmox.VMID
	name string
}

func (b *orchestratorBackend) Clone(ctx context.Context, _ proxmox.VMID, target proxmox.VMID, _ string) error {
	b.cloneCalls = append(b.cloneCalls, target)
	if b.blockClone {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (b *orchestratorBackend) Configure(ctx context.Context, _ proxmox.VMID, cfg proxmox.VMConfig) error {
	b.configureCalls = append(b.configureCalls, cfg)
	if b.blockConfigure {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (b *orchestratorBackend) Start(ctx context.Context, vmid proxmox.VMID) error {
	b.startCalls = append(b.startCalls, vmid)
	if b.blockStart {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (b *orchestratorBackend) Stop(context.Context, proxmox.VMID) error {
	return nil
}

func (b *orchestratorBackend) Destroy(_ context.Context, vmid proxmox.VMID) error {
	b.destroyCalls = append(b.destroyCalls, vmid)
	return nil
}

func (b *orchestratorBackend) SnapshotCreate(_ context.Context, vmid proxmox.VMID, name string) error {
	b.snapshotCalls = append(b.snapshotCalls, snapshotCall{vmid: vmid, name: name})
	return nil
}

func (b *orchestratorBackend) SnapshotRollback(context.Context, proxmox.VMID, string) error {
	return nil
}

func (b *orchestratorBackend) SnapshotDelete(context.Context, proxmox.VMID, string) error {
	return nil
}

func (b *orchestratorBackend) Status(context.Context, proxmox.VMID) (proxmox.Status, error) {
	return proxmox.StatusRunning, nil
}

func (b *orchestratorBackend) CurrentStats(context.Context, proxmox.VMID) (proxmox.VMStats, error) {
	return proxmox.VMStats{CPUUsage: 0.0}, nil
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

func (b *orchestratorBackend) ValidateTemplate(context.Context, proxmox.VMID) error {
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
	orchestrator := NewJobOrchestrator(store, profiles, backend, manager, nil, snippetStore, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtestkey agent@test", "http://10.77.0.1:8844", log.New(io.Discard, "", 0), nil, nil)
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
	if len(backend.snapshotCalls) != 1 {
		t.Fatalf("expected snapshot called once, got %d", len(backend.snapshotCalls))
	}
	if backend.snapshotCalls[0].vmid != proxmox.VMID(sandbox.VMID) || backend.snapshotCalls[0].name != cleanSnapshotName {
		t.Fatalf("expected snapshot call for vmid %d name %q, got %+v", sandbox.VMID, cleanSnapshotName, backend.snapshotCalls[0])
	}
}

func TestJobOrchestratorProvisionSandbox(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &orchestratorBackend{guestIP: "10.77.0.42"}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	profiles := map[string]models.Profile{
		"yolo": {
			Name:       "yolo",
			TemplateVM: 9000,
			RawYAML: `
name: yolo
template_vmid: 9000
network:
  bridge: vmbr1
  model: virtio
resources:
  cores: 4
  memory_mb: 4096
  cpulist: "0-3"
`,
		},
	}
	snippetDir := t.TempDir()
	snippetStore := proxmox.SnippetStore{Storage: "local", Dir: snippetDir}
	orchestrator := NewJobOrchestrator(store, profiles, backend, manager, nil, snippetStore, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtestkey agent@test", "http://10.77.0.1:8844", log.New(io.Discard, "", 0), nil, nil)

	now := time.Date(2026, 1, 29, 15, 0, 0, 0, time.UTC)
	sandbox := models.Sandbox{
		VMID:          1200,
		Name:          "sandbox-1200",
		Profile:       "yolo",
		State:         models.SandboxRequested,
		Keepalive:     true,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	updated, err := orchestrator.ProvisionSandbox(ctx, sandbox.VMID)
	if err != nil {
		t.Fatalf("provision sandbox: %v", err)
	}
	if updated.State != models.SandboxRunning {
		t.Fatalf("expected sandbox RUNNING, got %s", updated.State)
	}
	if updated.IP != backend.guestIP {
		t.Fatalf("expected sandbox IP %s, got %s", backend.guestIP, updated.IP)
	}
	if len(backend.cloneCalls) != 1 || backend.cloneCalls[0] != proxmox.VMID(sandbox.VMID) {
		t.Fatalf("expected clone called for vmid %d", sandbox.VMID)
	}
	if len(backend.configureCalls) != 1 {
		t.Fatalf("expected configure called once")
	}
	cfg := backend.configureCalls[0]
	if cfg.Cores != 4 || cfg.MemoryMB != 4096 || cfg.CPUPinning != "0-3" {
		t.Fatalf("unexpected resource config: %+v", cfg)
	}
	if cfg.Bridge != "vmbr1" || cfg.NetModel != "virtio" {
		t.Fatalf("unexpected network config: %+v", cfg)
	}
	if cfg.CloudInit == "" || !strings.Contains(cfg.CloudInit, "snippets") {
		t.Fatalf("expected cloud-init snippet path, got %q", cfg.CloudInit)
	}
	matches, err := filepath.Glob(filepath.Join(snippetDir, "agentlab-*.yaml"))
	if err != nil {
		t.Fatalf("snippet glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected snippet file in %s", snippetDir)
	}
	if len(backend.snapshotCalls) != 1 {
		t.Fatalf("expected snapshot called once, got %d", len(backend.snapshotCalls))
	}
	if backend.snapshotCalls[0].vmid != proxmox.VMID(sandbox.VMID) || backend.snapshotCalls[0].name != cleanSnapshotName {
		t.Fatalf("expected snapshot call for vmid %d name %q, got %+v", sandbox.VMID, cleanSnapshotName, backend.snapshotCalls[0])
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
	orchestrator := NewJobOrchestrator(store, profiles, backend, manager, nil, proxmox.SnippetStore{}, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtestkey agent@test", "http://10.77.0.1:8844", log.New(io.Discard, "", 0), nil, nil)

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
	orchestrator := NewJobOrchestrator(store, profiles, backend, manager, nil, proxmox.SnippetStore{}, "", "http://10.77.0.1:8844", log.New(io.Discard, "", 0), nil, nil)

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

func TestJobOrchestratorRunTimeoutCleanup(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &orchestratorBackend{blockConfigure: true}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	profiles := map[string]models.Profile{
		"yolo": {Name: "yolo", TemplateVM: 9000},
	}
	snippetDir := t.TempDir()
	snippetStore := proxmox.SnippetStore{Storage: "local", Dir: snippetDir}
	orchestrator := NewJobOrchestrator(store, profiles, backend, manager, nil, snippetStore, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtestkey agent@test", "http://10.77.0.1:8844", log.New(io.Discard, "", 0), nil, nil)
	orchestrator.WithProvisionTimeout(25 * time.Millisecond)

	now := time.Date(2026, 1, 29, 16, 0, 0, 0, time.UTC)
	orchestrator.now = func() time.Time { return now }
	job := models.Job{
		ID:        "job_timeout",
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
		t.Fatalf("expected timeout error")
	}

	updatedJob, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if updatedJob.Status != models.JobFailed {
		t.Fatalf("expected job FAILED, got %s", updatedJob.Status)
	}
	if updatedJob.SandboxVMID == nil || *updatedJob.SandboxVMID == 0 {
		t.Fatalf("expected sandbox vmid set")
	}
	sandbox, err := store.GetSandbox(ctx, *updatedJob.SandboxVMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sandbox.State != models.SandboxDestroyed {
		t.Fatalf("expected sandbox DESTROYED, got %s", sandbox.State)
	}
	if len(backend.destroyCalls) != 1 {
		t.Fatalf("expected destroy call, got %d", len(backend.destroyCalls))
	}
}

func TestJobOrchestratorProvisionSandboxTimeoutCleanup(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &orchestratorBackend{blockConfigure: true}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	profiles := map[string]models.Profile{
		"yolo": {Name: "yolo", TemplateVM: 9000},
	}
	snippetDir := t.TempDir()
	snippetStore := proxmox.SnippetStore{Storage: "local", Dir: snippetDir}
	orchestrator := NewJobOrchestrator(store, profiles, backend, manager, nil, snippetStore, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtestkey agent@test", "http://10.77.0.1:8844", log.New(io.Discard, "", 0), nil, nil)
	orchestrator.WithProvisionTimeout(25 * time.Millisecond)

	now := time.Date(2026, 1, 29, 16, 30, 0, 0, time.UTC)
	sandbox := models.Sandbox{
		VMID:          1300,
		Name:          "sandbox-1300",
		Profile:       "yolo",
		State:         models.SandboxRequested,
		Keepalive:     true,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	if _, err := orchestrator.ProvisionSandbox(ctx, sandbox.VMID); err == nil {
		t.Fatalf("expected timeout error")
	}

	updatedSandbox, err := store.GetSandbox(ctx, sandbox.VMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if updatedSandbox.State != models.SandboxDestroyed {
		t.Fatalf("expected sandbox DESTROYED, got %s", updatedSandbox.State)
	}
	if len(backend.destroyCalls) != 1 {
		t.Fatalf("expected destroy call, got %d", len(backend.destroyCalls))
	}
}

func TestWorkspaceRebindTimeoutCleanup(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &orchestratorBackend{blockStart: true}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	workspaceMgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))
	profiles := map[string]models.Profile{
		"default": {Name: "default", TemplateVM: 9000},
	}
	snippetStore := proxmox.SnippetStore{Storage: "local", Dir: t.TempDir()}
	orchestrator := NewJobOrchestrator(store, profiles, backend, manager, workspaceMgr, snippetStore, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtestkey agent@test", "http://10.77.0.1:8844", log.New(io.Discard, "", 0), nil, nil)
	orchestrator.WithProvisionTimeout(200 * time.Millisecond)

	now := time.Date(2026, 1, 29, 17, 0, 0, 0, time.UTC)
	orchestrator.now = func() time.Time { return now }
	oldSandbox := models.Sandbox{
		VMID:          1400,
		Name:          "sandbox-1400",
		Profile:       "default",
		State:         models.SandboxRunning,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	if err := store.CreateSandbox(ctx, oldSandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	workspace, err := workspaceMgr.Create(ctx, "dev", "local-zfs", 10)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workspace, err = workspaceMgr.Attach(ctx, workspace.ID, oldSandbox.VMID)
	if err != nil {
		t.Fatalf("attach workspace: %v", err)
	}
	if workspace.AttachedVM == nil || *workspace.AttachedVM != oldSandbox.VMID {
		t.Fatalf("expected workspace attached to vmid %d", oldSandbox.VMID)
	}

	if _, err := orchestrator.RebindWorkspace(ctx, workspace.ID, "default", nil, false); err == nil {
		t.Fatalf("expected timeout error")
	}

	updatedWorkspace, err := store.GetWorkspace(ctx, workspace.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if updatedWorkspace.AttachedVM == nil || *updatedWorkspace.AttachedVM != oldSandbox.VMID {
		t.Fatalf("expected workspace reattached to vmid %d, got %v", oldSandbox.VMID, updatedWorkspace.AttachedVM)
	}
	updatedOld, err := store.GetSandbox(ctx, oldSandbox.VMID)
	if err != nil {
		t.Fatalf("get old sandbox: %v", err)
	}
	if updatedOld.WorkspaceID == nil || *updatedOld.WorkspaceID != workspace.ID {
		t.Fatalf("expected old sandbox workspace restored")
	}

	newVMID := oldSandbox.VMID + 1
	updatedNew, err := store.GetSandbox(ctx, newVMID)
	if err != nil {
		t.Fatalf("get new sandbox: %v", err)
	}
	if updatedNew.State != models.SandboxDestroyed {
		t.Fatalf("expected sandbox DESTROYED, got %s", updatedNew.State)
	}
	if len(backend.destroyCalls) != 1 {
		t.Fatalf("expected destroy call, got %d", len(backend.destroyCalls))
	}
}
