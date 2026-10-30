package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/agentlab/agentlab/internal/proxmox"
	"github.com/stretchr/testify/require"
)

func cancellationFixture(t *testing.T, keepalive bool) (*JobOrchestrator, *proxmox.FakeBackend, *db.Store, models.Job, models.Workspace) {
	t.Helper()
	ctx := context.Background()
	store := newTestStore(t)
	backend := proxmox.NewFakeBackend()
	backend.AddTemplate(9000)
	require.NoError(t, backend.Clone(ctx, 9000, 1100, "cancel-owned"))
	require.NoError(t, backend.Start(ctx, 1100))
	now := time.Now().UTC()
	require.NoError(t, store.CreateSandbox(ctx, models.Sandbox{VMID: 1100, Name: "cancel-owned", Profile: "default", State: models.SandboxRunning, Keepalive: keepalive, CreatedAt: now, LastUpdatedAt: now}))
	workspaces := NewWorkspaceManager(store, backend, nil)
	workspace, err := workspaces.Create(ctx, "preserve-user-data", "local-zfs", 10)
	require.NoError(t, err)
	_, err = workspaces.Attach(ctx, workspace.ID, 1100)
	require.NoError(t, err)
	vmid := 1100
	job := models.Job{ID: "cancel-job", RepoURL: "https://example.com/repo.git", Ref: "main", Profile: "default", Status: models.JobRunning, SandboxVMID: &vmid, WorkspaceID: &workspace.ID, Keepalive: keepalive, CreatedAt: now, UpdatedAt: now}
	require.NoError(t, store.CreateJob(ctx, job))
	manager := NewSandboxManager(store, backend, nil).WithWorkspaceManager(workspaces)
	orchestrator := NewJobOrchestrator(store, map[string]models.Profile{"default": {Name: "default", TemplateVM: 9000}}, backend, manager, workspaces, proxmox.SnippetStore{}, "key", "http://controller", nil, nil, nil)
	return orchestrator, backend, store, job, workspace
}

func TestCancellationVerifiesCleanupAndPreservesWorkspace(t *testing.T) {
	for _, keepalive := range []bool{false, true} {
		t.Run(strconv.FormatBool(keepalive), func(t *testing.T) {
			orchestrator, backend, store, job, workspace := cancellationFixture(t, keepalive)
			request, err := store.RequestJobCancellation(context.Background(), protocol.JobCancellation{JobID: job.ID, SandboxVMID: job.SandboxVMID, Actor: "test"})
			require.NoError(t, err)
			require.False(t, request.WorkStopped)
			require.Error(t, store.UpdateJobResult(context.Background(), job.ID, models.JobCompleted, `{"late":true}`))
			require.NoError(t, orchestrator.CancelJob(context.Background(), job.ID))
			record, err := store.GetJobCancellation(context.Background(), job.ID)
			require.NoError(t, err)
			require.Equal(t, "cancelled", record.State)
			require.True(t, record.WorkStopped)
			require.NoError(t, orchestrator.CancelJob(context.Background(), job.ID))
			_, err = backend.VolumeInfo(context.Background(), workspace.VolumeID)
			require.NoError(t, err, "workspace volume was deleted")
			if keepalive {
				status, err := backend.Status(context.Background(), 1100)
				require.NoError(t, err)
				require.Equal(t, proxmox.StatusStopped, status)
				require.Equal(t, "retained", record.CleanupStatus)
			} else {
				_, err := backend.Status(context.Background(), 1100)
				require.ErrorIs(t, err, proxmox.ErrVMNotFound)
			}
			updated, err := store.GetJob(context.Background(), job.ID)
			require.NoError(t, err)
			require.Equal(t, models.JobFailed, updated.Status)
			require.Contains(t, updated.ResultJSON, `"cancelled":true`)
			require.ErrorIs(t, orchestrator.HandleReport(context.Background(), JobReport{JobID: job.ID, VMID: 1100, Status: models.JobCompleted}), ErrJobFinalized)
			require.Error(t, store.UpdateJobResult(context.Background(), job.ID, models.JobCompleted, `{"late":true}`))
			again, err := store.GetJob(context.Background(), job.ID)
			require.NoError(t, err)
			require.Equal(t, models.JobFailed, again.Status)
		})
	}
}

func TestCancellationChecksSavedScope(t *testing.T) {
	orchestrator, backend, store, job, _ := cancellationFixture(t, false)
	_, err := store.RequestJobCancellation(context.Background(), protocol.JobCancellation{JobID: job.ID, SandboxVMID: job.SandboxVMID, Scope: []string{"sandbox:9999"}})
	require.NoError(t, err)
	require.Error(t, orchestrator.CancelJob(context.Background(), job.ID))
	record, err := store.GetJobCancellation(context.Background(), job.ID)
	require.NoError(t, err)
	require.Equal(t, "blocked", record.State)
	require.False(t, record.WorkStopped)
	status, err := backend.Status(context.Background(), 1100)
	require.NoError(t, err)
	require.Equal(t, proxmox.StatusRunning, status)
}

func TestCancellationEndpointIsScopedAndDurable(t *testing.T) {
	orchestrator, _, store, job, _ := cancellationFixture(t, false)
	runner := &submissionRunner{}
	orchestrator.WithBackgroundRunner(runner)
	api := NewControlAPI(store, nil, orchestrator.sandboxManager, orchestrator.workspaceMgr, orchestrator, "", nil)
	actor := &auth.RequestIdentity{Method: "ssh-token", Fingerprint: "actor", Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"job.cancel", "job.read"}, Scope: []string{"sandbox:9999"}}}}
	call := func(method string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "/v1/jobs/"+job.ID+"/cancel", nil)
		request = request.WithContext(auth.WithIdentity(request.Context(), actor))
		response := httptest.NewRecorder()
		api.handleJobByID(response, request)
		return response
	}
	require.Equal(t, 403, call(http.MethodPost).Code)
	_, err := store.GetJobCancellation(context.Background(), job.ID)
	require.True(t, errors.Is(err, sql.ErrNoRows))
	actor.Token.Claims.Scope = []string{"sandbox:1100"}
	response := call(http.MethodPost)
	require.Equal(t, 202, response.Code, response.Body.String())
	var record protocol.JobCancellation
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &record))
	require.False(t, record.WorkStopped)
	require.Equal(t, int32(1), runner.starts.Load())
	// A new orchestrator can resume the saved request without the HTTP caller.
	require.NoError(t, orchestrator.RecoverCancellations(context.Background()))
	require.Equal(t, int32(2), runner.starts.Load())
	require.NoError(t, orchestrator.CancelJob(context.Background(), job.ID))
	require.Equal(t, 200, call(http.MethodGet).Code)
	response = call(http.MethodPost)
	require.Equal(t, 200, response.Code)
	require.Equal(t, int32(2), runner.starts.Load())
}

type cancelDuringProvision struct {
	*proxmox.FakeBackend
	entered chan struct{}
	clones  atomic.Int32
}

func (b *cancelDuringProvision) Clone(ctx context.Context, template, target proxmox.VMID, name string) error {
	b.clones.Add(1)
	return b.FakeBackend.Clone(ctx, template, target, name)
}
func (b *cancelDuringProvision) Configure(ctx context.Context, _ proxmox.VMID, _ proxmox.VMConfig) error {
	close(b.entered)
	<-ctx.Done()
	return ctx.Err()
}

func TestCancellationDuringProvisioningDoesNotCreateAnotherSandbox(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := newTestStore(t)
	backend := &cancelDuringProvision{FakeBackend: proxmox.NewFakeBackend(), entered: make(chan struct{})}
	backend.AddTemplate(9000)
	manager := NewSandboxManager(store, backend, nil)
	orchestrator := NewJobOrchestrator(store, map[string]models.Profile{"default": {Name: "default", TemplateVM: 9000}}, backend, manager, nil, proxmox.SnippetStore{Storage: "local", Dir: t.TempDir()}, "key", "http://controller", nil, nil, nil)
	now := time.Now().UTC()
	job := models.Job{ID: "provision-cancel", RepoURL: "https://example.com/repo.git", Ref: "main", Profile: "default", Status: models.JobQueued, CreatedAt: now, UpdatedAt: now}
	require.NoError(t, store.CreateJob(ctx, job))
	runDone := make(chan error, 1)
	go func() { runDone <- orchestrator.Run(ctx, job.ID) }()
	select {
	case <-backend.entered:
	case <-ctx.Done():
		t.Fatal("provisioning did not reach configuration")
	}
	current, err := store.GetJob(ctx, job.ID)
	require.NoError(t, err)
	_, err = store.RequestJobCancellation(ctx, protocol.JobCancellation{JobID: job.ID, SandboxVMID: current.SandboxVMID})
	require.NoError(t, err)
	require.NoError(t, orchestrator.CancelJob(ctx, job.ID))
	require.Error(t, <-runDone)
	record, err := store.GetJobCancellation(ctx, job.ID)
	require.NoError(t, err)
	require.True(t, record.WorkStopped)
	require.Equal(t, int32(1), backend.clones.Load())
	_, err = backend.Status(ctx, proxmox.VMID(*current.SandboxVMID))
	require.ErrorIs(t, err, proxmox.ErrVMNotFound)
	require.ErrorIs(t, orchestrator.Run(ctx, job.ID), ErrJobCancellationRequested)
	require.Equal(t, int32(1), backend.clones.Load())
}

func TestCancellationDoesNotActOnAnOldFailedJob(t *testing.T) {
	orchestrator, backend, store, job, _ := cancellationFixture(t, false)
	runner := &submissionRunner{}
	orchestrator.WithBackgroundRunner(runner)
	require.NoError(t, store.UpdateJobStatus(context.Background(), job.ID, models.JobFailed))
	api := NewControlAPI(store, nil, orchestrator.sandboxManager, orchestrator.workspaceMgr, orchestrator, "", nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/jobs/"+job.ID+"/cancel", nil)
	response := httptest.NewRecorder()
	api.handleJobByID(response, request)
	require.Equal(t, 200, response.Code)
	var result protocol.JobCancellation
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Equal(t, "blocked", result.State)
	require.False(t, result.WorkStopped)
	require.Equal(t, "job_already_terminal", result.ErrorCode)
	require.Zero(t, runner.starts.Load())
	status, err := backend.Status(context.Background(), 1100)
	require.NoError(t, err)
	require.Equal(t, proxmox.StatusRunning, status)
}

type cancellationRecoveryRunner struct {
	ctx  context.Context
	done chan struct{}
}

func (r *cancellationRecoveryRunner) Go(_ string, fn func(context.Context)) bool {
	go func() { defer close(r.done); fn(r.ctx) }()
	return true
}
func (r *cancellationRecoveryRunner) LifecycleContext() context.Context { return r.ctx }

func TestCancellationRecoveryCompletesSavedWork(t *testing.T) {
	original, backend, store, job, workspace := cancellationFixture(t, false)
	record, err := store.RequestJobCancellation(context.Background(), protocol.JobCancellation{JobID: job.ID, Actor: "restart"})
	require.NoError(t, err)
	record.State = "running"
	require.NoError(t, store.SaveJobCancellation(context.Background(), record))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runner := &cancellationRecoveryRunner{ctx: ctx, done: make(chan struct{})}
	restarted := NewJobOrchestrator(store, nil, backend, original.sandboxManager, original.workspaceMgr, proxmox.SnippetStore{}, "", "", nil, nil, nil).WithBackgroundRunner(runner)
	require.NoError(t, restarted.RecoverCancellations(ctx))
	select {
	case <-runner.done:
	case <-ctx.Done():
		t.Fatal("saved cancellation did not finish")
	}
	final, err := store.GetJobCancellation(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, "cancelled", final.State)
	require.True(t, final.WorkStopped)
	_, err = backend.Status(ctx, 1100)
	require.ErrorIs(t, err, proxmox.ErrVMNotFound)
	_, err = backend.VolumeInfo(ctx, workspace.VolumeID)
	require.NoError(t, err)
}
