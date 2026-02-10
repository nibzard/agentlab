package daemon

import (
	"context"
	"errors"
	"io"
	"log"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

type stubBackend struct {
	startErr              error
	stopErr               error
	suspendErr            error
	resumeErr             error
	destroyErr            error
	detachErr             error
	snapshotRollbackErr   error
	statusErr             error
	status                proxmox.Status
	statsErr              error
	cpuUsage              float64
	vmConfig              map[string]string
	vmConfigErr           error
	volumeInfo            proxmox.VolumeInfo
	volumeErr             error
	volumeCloneErr        error
	volumeCloneSnapErr    error
	volumeCloneCalls      []volumeCloneCall
	volumeCloneSnapCalls  []volumeCloneSnapshotCall
	startCalls            int
	stopCalls             int
	suspendCalls          int
	resumeCalls           int
	detachCalls           int
	snapshotRollbackCalls int
}

type volumeCloneCall struct {
	source string
	target string
}

type volumeCloneSnapshotCall struct {
	source   string
	snapshot string
	target   string
}

func (s *stubBackend) Clone(context.Context, proxmox.VMID, proxmox.VMID, string) error {
	return nil
}

func (s *stubBackend) Configure(context.Context, proxmox.VMID, proxmox.VMConfig) error {
	return nil
}

func (s *stubBackend) Start(context.Context, proxmox.VMID) error {
	s.startCalls++
	return s.startErr
}

func (s *stubBackend) Stop(context.Context, proxmox.VMID) error {
	s.stopCalls++
	return s.stopErr
}

func (s *stubBackend) Suspend(context.Context, proxmox.VMID) error {
	s.suspendCalls++
	return s.suspendErr
}

func (s *stubBackend) Resume(context.Context, proxmox.VMID) error {
	s.resumeCalls++
	return s.resumeErr
}

func (s *stubBackend) Destroy(context.Context, proxmox.VMID) error {
	return s.destroyErr
}

func (s *stubBackend) SnapshotCreate(context.Context, proxmox.VMID, string) error {
	return nil
}

func (s *stubBackend) SnapshotRollback(context.Context, proxmox.VMID, string) error {
	s.snapshotRollbackCalls++
	return s.snapshotRollbackErr
}

func (s *stubBackend) SnapshotDelete(context.Context, proxmox.VMID, string) error {
	return nil
}

func (s *stubBackend) Status(context.Context, proxmox.VMID) (proxmox.Status, error) {
	if s.statusErr != nil {
		return proxmox.StatusUnknown, s.statusErr
	}
	if s.status != "" {
		return s.status, nil
	}
	return proxmox.StatusUnknown, nil
}

func (s *stubBackend) CurrentStats(context.Context, proxmox.VMID) (proxmox.VMStats, error) {
	if s.statsErr != nil {
		return proxmox.VMStats{}, s.statsErr
	}
	return proxmox.VMStats{CPUUsage: s.cpuUsage}, nil
}

func (s *stubBackend) GuestIP(context.Context, proxmox.VMID) (string, error) {
	return "", nil
}

func (s *stubBackend) VMConfig(context.Context, proxmox.VMID) (map[string]string, error) {
	if s.vmConfigErr != nil {
		return nil, s.vmConfigErr
	}
	if s.vmConfig == nil {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(s.vmConfig))
	for key, value := range s.vmConfig {
		out[key] = value
	}
	return out, nil
}

func (s *stubBackend) CreateVolume(context.Context, string, string, int) (string, error) {
	return "local-zfs:workspace", nil
}

func (s *stubBackend) AttachVolume(context.Context, proxmox.VMID, string, string) error {
	return nil
}

func (s *stubBackend) DetachVolume(context.Context, proxmox.VMID, string) error {
	s.detachCalls++
	return s.detachErr
}

func (s *stubBackend) DeleteVolume(context.Context, string) error {
	return nil
}

func (s *stubBackend) VolumeInfo(_ context.Context, volumeID string) (proxmox.VolumeInfo, error) {
	if s.volumeErr != nil {
		return proxmox.VolumeInfo{}, s.volumeErr
	}
	if s.volumeInfo.VolumeID != "" || s.volumeInfo.Path != "" || s.volumeInfo.Storage != "" {
		return s.volumeInfo, nil
	}
	return proxmox.VolumeInfo{VolumeID: volumeID}, nil
}

func (s *stubBackend) VolumeSnapshotCreate(context.Context, string, string) error {
	return nil
}

func (s *stubBackend) VolumeSnapshotRestore(context.Context, string, string) error {
	return nil
}

func (s *stubBackend) VolumeSnapshotDelete(context.Context, string, string) error {
	return nil
}

func (s *stubBackend) VolumeClone(_ context.Context, sourceVolumeID, targetVolumeID string) error {
	s.volumeCloneCalls = append(s.volumeCloneCalls, volumeCloneCall{source: sourceVolumeID, target: targetVolumeID})
	if s.volumeCloneErr != nil {
		return s.volumeCloneErr
	}
	return nil
}

func (s *stubBackend) VolumeCloneFromSnapshot(_ context.Context, sourceVolumeID, snapshotName, targetVolumeID string) error {
	s.volumeCloneSnapCalls = append(s.volumeCloneSnapCalls, volumeCloneSnapshotCall{
		source:   sourceVolumeID,
		snapshot: snapshotName,
		target:   targetVolumeID,
	})
	if s.volumeCloneSnapErr != nil {
		return s.volumeCloneSnapErr
	}
	return nil
}

func (s *stubBackend) ValidateTemplate(context.Context, proxmox.VMID) error {
	return nil
}

func TestSandboxTransitions(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	mgr := NewSandboxManager(store, nil, log.New(io.Discard, "", 0))

	sandbox := models.Sandbox{
		VMID:      100,
		Name:      "test-sb",
		Profile:   "default",
		State:     models.SandboxRequested,
		Keepalive: false,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if err := mgr.Transition(ctx, sandbox.VMID, models.SandboxProvisioning); err != nil {
		t.Fatalf("transition to provisioning: %v", err)
	}
	if err := mgr.Transition(ctx, sandbox.VMID, models.SandboxRunning); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected invalid transition error, got %v", err)
	}
}

func TestSandboxLeaseRenewal(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	mgr := NewSandboxManager(store, nil, log.New(io.Discard, "", 0))
	base := time.Date(2026, 1, 29, 10, 0, 0, 0, time.UTC)
	mgr.now = func() time.Time { return base }

	sandbox := models.Sandbox{
		VMID:         101,
		Name:         "keepalive",
		Profile:      "default",
		State:        models.SandboxReady,
		Keepalive:    true,
		LeaseExpires: base.Add(30 * time.Minute),
		CreatedAt:    base,
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	expiresAt, err := mgr.RenewLease(ctx, sandbox.VMID, 2*time.Hour)
	if err != nil {
		t.Fatalf("renew lease: %v", err)
	}
	expected := base.Add(2 * time.Hour)
	if !expiresAt.Equal(expected) {
		t.Fatalf("expected expiry %s, got %s", expected, expiresAt)
	}
}

func TestSandboxLeaseGC(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	base := time.Date(2026, 1, 29, 11, 0, 0, 0, time.UTC)
	mgr.now = func() time.Time { return base }

	sandbox := models.Sandbox{
		VMID:         102,
		Name:         "expired",
		Profile:      "default",
		State:        models.SandboxRunning,
		Keepalive:    false,
		LeaseExpires: base.Add(-1 * time.Minute),
		CreatedAt:    base.Add(-2 * time.Hour),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	mgr.runLeaseGC(ctx)

	updated, err := store.GetSandbox(ctx, sandbox.VMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if updated.State != models.SandboxDestroyed {
		t.Fatalf("expected destroyed, got %s", updated.State)
	}
}

func TestSandboxStartStopLifecycle(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))

	sandbox := models.Sandbox{
		VMID:      108,
		Name:      "stoppable",
		Profile:   "default",
		State:     models.SandboxStopped,
		Keepalive: false,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	if err := mgr.Start(ctx, sandbox.VMID); err != nil {
		t.Fatalf("start sandbox: %v", err)
	}
	if backend.startCalls != 1 {
		t.Fatalf("expected start called once, got %d", backend.startCalls)
	}
	updated, err := store.GetSandbox(ctx, sandbox.VMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if updated.State != models.SandboxRunning {
		t.Fatalf("expected running, got %s", updated.State)
	}

	if err := mgr.Stop(ctx, sandbox.VMID); err != nil {
		t.Fatalf("stop sandbox: %v", err)
	}
	if backend.stopCalls != 1 {
		t.Fatalf("expected stop called once, got %d", backend.stopCalls)
	}
	updated, err = store.GetSandbox(ctx, sandbox.VMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if updated.State != models.SandboxStopped {
		t.Fatalf("expected stopped, got %s", updated.State)
	}
}

func TestSandboxStartStopInvalidState(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))

	sandbox := models.Sandbox{
		VMID:      109,
		Name:      "invalid-transition",
		Profile:   "default",
		State:     models.SandboxProvisioning,
		Keepalive: false,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	if err := mgr.Start(ctx, sandbox.VMID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected invalid transition for start, got %v", err)
	}
	if err := mgr.Stop(ctx, sandbox.VMID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected invalid transition for stop, got %v", err)
	}
}

func TestSandboxPauseResumeReady(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))

	sandbox := models.Sandbox{
		VMID:      120,
		Name:      "pause-ready",
		Profile:   "default",
		State:     models.SandboxReady,
		Keepalive: false,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	if err := mgr.Pause(ctx, sandbox.VMID); err != nil {
		t.Fatalf("pause sandbox: %v", err)
	}
	if backend.suspendCalls != 1 {
		t.Fatalf("expected suspend called once, got %d", backend.suspendCalls)
	}
	updated, err := store.GetSandbox(ctx, sandbox.VMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if updated.State != models.SandboxSuspended {
		t.Fatalf("expected suspended, got %s", updated.State)
	}

	if err := mgr.Resume(ctx, sandbox.VMID); err != nil {
		t.Fatalf("resume sandbox: %v", err)
	}
	if backend.resumeCalls != 1 {
		t.Fatalf("expected resume called once, got %d", backend.resumeCalls)
	}
	updated, err = store.GetSandbox(ctx, sandbox.VMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if updated.State != models.SandboxReady {
		t.Fatalf("expected ready, got %s", updated.State)
	}
}

func TestSandboxPauseResumeRunningJob(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))

	sandbox := models.Sandbox{
		VMID:      121,
		Name:      "pause-running",
		Profile:   "default",
		State:     models.SandboxRunning,
		Keepalive: false,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	vmid := sandbox.VMID
	job := models.Job{
		ID:          "job-running",
		RepoURL:     "https://example.com/repo.git",
		Ref:         "main",
		Profile:     "default",
		Status:      models.JobRunning,
		SandboxVMID: &vmid,
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	if err := mgr.Pause(ctx, sandbox.VMID); err != nil {
		t.Fatalf("pause sandbox: %v", err)
	}
	if backend.suspendCalls != 1 {
		t.Fatalf("expected suspend called once, got %d", backend.suspendCalls)
	}
	updated, err := store.GetSandbox(ctx, sandbox.VMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if updated.State != models.SandboxSuspended {
		t.Fatalf("expected suspended, got %s", updated.State)
	}

	if err := mgr.Resume(ctx, sandbox.VMID); err != nil {
		t.Fatalf("resume sandbox: %v", err)
	}
	if backend.resumeCalls != 1 {
		t.Fatalf("expected resume called once, got %d", backend.resumeCalls)
	}
	updated, err = store.GetSandbox(ctx, sandbox.VMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if updated.State != models.SandboxRunning {
		t.Fatalf("expected running, got %s", updated.State)
	}
}

func TestSandboxPauseWorkspaceRunningJobBlocked(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))

	workspaceID := "workspace-1"
	sandbox := models.Sandbox{
		VMID:        122,
		Name:        "pause-workspace",
		Profile:     "default",
		State:       models.SandboxRunning,
		Keepalive:   false,
		WorkspaceID: &workspaceID,
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	vmid := sandbox.VMID
	job := models.Job{
		ID:          "job-workspace",
		RepoURL:     "https://example.com/repo.git",
		Ref:         "main",
		Profile:     "default",
		Status:      models.JobRunning,
		SandboxVMID: &vmid,
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	if err := mgr.Pause(ctx, sandbox.VMID); !errors.Is(err, ErrSandboxInUse) {
		t.Fatalf("expected sandbox in use error, got %v", err)
	}
	if backend.suspendCalls != 0 {
		t.Fatalf("expected suspend not called, got %d", backend.suspendCalls)
	}
}

func TestSandboxPauseResumeInvalidState(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))

	sandbox := models.Sandbox{
		VMID:      123,
		Name:      "pause-invalid",
		Profile:   "default",
		State:     models.SandboxProvisioning,
		Keepalive: false,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	if err := mgr.Pause(ctx, sandbox.VMID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected invalid transition for pause, got %v", err)
	}
	if err := mgr.Resume(ctx, sandbox.VMID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected invalid transition for resume, got %v", err)
	}
}

func TestSandboxRevertStopped(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))

	sandbox := models.Sandbox{
		VMID:      112,
		Name:      "revert-stopped",
		Profile:   "default",
		State:     models.SandboxStopped,
		Keepalive: false,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	result, err := mgr.Revert(ctx, sandbox.VMID, RevertOptions{})
	if err != nil {
		t.Fatalf("revert sandbox: %v", err)
	}
	if backend.snapshotRollbackCalls != 1 {
		t.Fatalf("expected snapshot rollback once, got %d", backend.snapshotRollbackCalls)
	}
	if backend.stopCalls != 0 {
		t.Fatalf("expected stop not called, got %d", backend.stopCalls)
	}
	if backend.startCalls != 0 {
		t.Fatalf("expected start not called, got %d", backend.startCalls)
	}
	if result.Restarted {
		t.Fatalf("expected no restart, got restarted=true")
	}
	if result.Snapshot != cleanSnapshotName {
		t.Fatalf("expected snapshot %s, got %s", cleanSnapshotName, result.Snapshot)
	}
	if result.Sandbox.State != models.SandboxStopped {
		t.Fatalf("expected sandbox STOPPED, got %s", result.Sandbox.State)
	}
}

func TestSandboxRevertRunning(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{status: proxmox.StatusRunning}
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))

	sandbox := models.Sandbox{
		VMID:      113,
		Name:      "revert-running",
		Profile:   "default",
		State:     models.SandboxRunning,
		Keepalive: false,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	result, err := mgr.Revert(ctx, sandbox.VMID, RevertOptions{})
	if err != nil {
		t.Fatalf("revert sandbox: %v", err)
	}
	if backend.snapshotRollbackCalls != 1 {
		t.Fatalf("expected snapshot rollback once, got %d", backend.snapshotRollbackCalls)
	}
	if backend.stopCalls != 1 {
		t.Fatalf("expected stop called once, got %d", backend.stopCalls)
	}
	if backend.startCalls != 1 {
		t.Fatalf("expected start called once, got %d", backend.startCalls)
	}
	if !result.Restarted {
		t.Fatalf("expected restart, got restarted=false")
	}
	if result.Sandbox.State != models.SandboxRunning {
		t.Fatalf("expected sandbox RUNNING, got %s", result.Sandbox.State)
	}
}

func TestSandboxRevertMissingSnapshot(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{snapshotRollbackErr: errors.New("snapshot 'clean' does not exist")}
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))

	sandbox := models.Sandbox{
		VMID:      114,
		Name:      "revert-missing",
		Profile:   "default",
		State:     models.SandboxStopped,
		Keepalive: false,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	_, err := mgr.Revert(ctx, sandbox.VMID, RevertOptions{})
	if err == nil {
		t.Fatalf("expected snapshot missing error")
	}
	if !errors.Is(err, ErrSnapshotMissing) {
		t.Fatalf("expected ErrSnapshotMissing, got %v", err)
	}
	if backend.snapshotRollbackCalls != 1 {
		t.Fatalf("expected snapshot rollback once, got %d", backend.snapshotRollbackCalls)
	}
}

func TestSandboxDestroyMissingVM(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{destroyErr: proxmox.ErrVMNotFound}
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))

	sandbox := models.Sandbox{
		VMID:      103,
		Name:      "missing-vm",
		Profile:   "default",
		State:     models.SandboxProvisioning,
		Keepalive: false,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	if err := mgr.Destroy(ctx, sandbox.VMID); err != nil {
		t.Fatalf("destroy sandbox: %v", err)
	}
	updated, err := store.GetSandbox(ctx, sandbox.VMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if updated.State != models.SandboxDestroyed {
		t.Fatalf("expected destroyed, got %s", updated.State)
	}
}

func TestSandboxLeaseGCDetachesWorkspace(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{detachErr: proxmox.ErrVMNotFound}
	workspaceMgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0)).WithWorkspaceManager(workspaceMgr)
	base := time.Date(2026, 1, 29, 12, 0, 0, 0, time.UTC)
	mgr.now = func() time.Time { return base }

	sandbox := models.Sandbox{
		VMID:         104,
		Name:         "expired-workspace",
		Profile:      "default",
		State:        models.SandboxRunning,
		Keepalive:    false,
		LeaseExpires: base.Add(-1 * time.Minute),
		CreatedAt:    base.Add(-1 * time.Hour),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	workspace, err := workspaceMgr.Create(ctx, "gc-workspace", "", 10)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := workspaceMgr.Attach(ctx, workspace.ID, sandbox.VMID); err != nil {
		t.Fatalf("attach workspace: %v", err)
	}

	mgr.runLeaseGC(ctx)

	updatedSandbox, err := store.GetSandbox(ctx, sandbox.VMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if updatedSandbox.WorkspaceID != nil {
		t.Fatalf("expected sandbox workspace cleared, got %v", *updatedSandbox.WorkspaceID)
	}
	updatedWorkspace, err := store.GetWorkspace(ctx, workspace.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if updatedWorkspace.AttachedVM != nil {
		t.Fatalf("expected workspace detached, got %d", *updatedWorkspace.AttachedVM)
	}
	if backend.detachCalls == 0 {
		t.Fatalf("expected workspace detach to be invoked")
	}
}

func TestSandboxDestroyReleasesWorkspaceLease(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	workspaceMgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0)).WithWorkspaceManager(workspaceMgr)

	now := time.Date(2026, 1, 29, 12, 15, 0, 0, time.UTC)
	sandbox := models.Sandbox{
		VMID:          106,
		Name:          "lease-sandbox",
		Profile:       "default",
		State:         models.SandboxRunning,
		Keepalive:     false,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	workspace, err := workspaceMgr.Create(ctx, "lease-ws", "", 10)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	job := models.Job{
		ID:          "job-lease",
		RepoURL:     "https://example.com/repo.git",
		Ref:         "main",
		Profile:     "default",
		Task:        "run tests",
		Status:      models.JobRunning,
		SandboxVMID: &sandbox.VMID,
		WorkspaceID: &workspace.ID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	leaseOwner := workspaceLeaseOwnerForJob(job.ID)
	if _, err := store.TryAcquireWorkspaceLease(ctx, workspace.ID, leaseOwner, "nonce-lease", now.Add(10*time.Minute)); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}

	if err := mgr.Destroy(ctx, sandbox.VMID); err != nil {
		t.Fatalf("destroy sandbox: %v", err)
	}
	updatedWorkspace, err := store.GetWorkspace(ctx, workspace.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if updatedWorkspace.LeaseOwner != "" || updatedWorkspace.LeaseNonce != "" || !updatedWorkspace.LeaseExpires.IsZero() {
		t.Fatalf("expected workspace lease released")
	}
}

func newTestStore(t *testing.T) *db.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agentlab.db")
	store, err := db.Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestSandboxManagerConcurrentRenewLease(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	mgr := NewSandboxManager(store, nil, log.New(io.Discard, "", 0))
	base := time.Date(2026, 1, 29, 13, 0, 0, 0, time.UTC)
	mgr.now = func() time.Time { return base }

	sandbox := models.Sandbox{
		VMID:          105,
		Name:          "concurrent-lease",
		Profile:       "default",
		State:         models.SandboxReady,
		Keepalive:     true,
		LeaseExpires:  base.Add(5 * time.Minute),
		CreatedAt:     base,
		LastUpdatedAt: base,
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	ttl := 2 * time.Hour
	workers := 8
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := mgr.RenewLease(ctx, sandbox.VMID, ttl)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("renew lease error: %v", err)
		}
	}

	updated, err := store.GetSandbox(ctx, sandbox.VMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	expected := base.Add(ttl)
	if !updated.LeaseExpires.Equal(expected) {
		t.Fatalf("expected lease %s, got %s", expected, updated.LeaseExpires)
	}
}

func TestSandboxManagerConcurrentTransition(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	mgr := NewSandboxManager(store, nil, log.New(io.Discard, "", 0))

	sandbox := models.Sandbox{
		VMID:          106,
		Name:          "concurrent-transition",
		Profile:       "default",
		State:         models.SandboxRunning,
		Keepalive:     false,
		CreatedAt:     time.Now().UTC(),
		LastUpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	workers := 8
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- mgr.Transition(ctx, sandbox.VMID, models.SandboxCompleted)
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil && !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("unexpected transition error: %v", err)
		}
	}

	updated, err := store.GetSandbox(ctx, sandbox.VMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if updated.State != models.SandboxCompleted {
		t.Fatalf("expected completed, got %s", updated.State)
	}
}

func TestSandboxManagerConcurrentLeaseGC(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	mgr := NewSandboxManager(store, nil, log.New(io.Discard, "", 0))
	base := time.Date(2026, 1, 29, 14, 0, 0, 0, time.UTC)
	mgr.now = func() time.Time { return base }

	sandbox := models.Sandbox{
		VMID:          107,
		Name:          "concurrent-gc",
		Profile:       "default",
		State:         models.SandboxRunning,
		Keepalive:     false,
		LeaseExpires:  base.Add(-1 * time.Minute),
		CreatedAt:     base.Add(-2 * time.Hour),
		LastUpdatedAt: base.Add(-90 * time.Minute),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	workers := 3
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mgr.runLeaseGC(ctx)
		}()
	}
	wg.Wait()

	updated, err := store.GetSandbox(ctx, sandbox.VMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if updated.State != models.SandboxDestroyed {
		t.Fatalf("expected destroyed, got %s", updated.State)
	}
}
