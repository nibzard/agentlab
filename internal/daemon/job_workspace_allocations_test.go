package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
	"github.com/stretchr/testify/require"
)

type allocationBackend struct {
	proxmox.Backend
	creates         atomic.Int32
	deletes         atomic.Int32
	allocationError error
	returnReceipt   bool
	cancel          func()
}

func (b *allocationBackend) CreateVolume(ctx context.Context, storage, name string, size int) (string, error) {
	b.creates.Add(1)
	if b.cancel != nil {
		b.cancel()
	}
	if b.allocationError != nil && !b.returnReceipt {
		return "", b.allocationError
	}
	return storage + ":" + name, b.allocationError
}
func (b *allocationBackend) DeleteVolume(context.Context, string) error { b.deletes.Add(1); return nil }

func workspaceSubmissionAPI(t *testing.T) (*ControlAPI, *db.Store, *allocationBackend, *submissionRunner) {
	t.Helper()
	api, store, runner := submissionAPI(t)
	backend := &allocationBackend{Backend: &orchestratorBackend{}}
	api.workspaceMgr = NewWorkspaceManager(store, backend, nil)
	return api, store, backend, runner
}
func submitWorkspaceJob(api *ControlAPI, key, name string) *httptest.ResponseRecorder {
	payload, _ := json.Marshal(V1JobCreateRequest{RepoURL: "https://example.com/repo.git", Profile: "default", Task: "test", WorkspaceCreate: &V1WorkspaceCreateRequest{Name: name, Storage: "local-zfs", SizeGB: 10}})
	request := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(payload))
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	api.handleJobs(response, request)
	return response
}

func TestWorkspaceSubmissionReplayAndConcurrency(t *testing.T) {
	api, store, backend, runner := workspaceSubmissionAPI(t)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := submitWorkspaceJob(api, "same-key", "created-once")
			if response.Code != 200 && response.Code != 201 {
				t.Errorf("submit: %d %s", response.Code, response.Body.String())
				return
			}
			var result V1JobResponse
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Error(err)
				return
			}
			ids <- result.ID
		}()
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		require.Equal(t, first, id)
	}
	require.Equal(t, int32(1), backend.creates.Load())
	require.Zero(t, backend.deletes.Load())
	require.Equal(t, int32(1), runner.starts.Load())
	allocation, err := store.JobWorkspaceAllocation(context.Background(), first)
	require.NoError(t, err)
	require.Equal(t, "committed", allocation.State)
	// An unchanged request replays before mutable profile/default validation.
	restarted := NewControlAPI(store, map[string]models.Profile{}, nil, NewWorkspaceManager(store, backend, nil), (&JobOrchestrator{}).WithBackgroundRunner(runner), "", nil)
	replay := submitWorkspaceJob(restarted, "same-key", "created-once")
	require.Equal(t, 200, replay.Code, replay.Body.String())
	require.Equal(t, int32(1), backend.creates.Load())
}

func TestWorkspaceSubmissionUnknownOutcomeNeverAllocatesAgain(t *testing.T) {
	for _, receipt := range []bool{false, true} {
		t.Run(map[bool]string{false: "no receipt", true: "partial receipt"}[receipt], func(t *testing.T) {
			api, store, backend, runner := workspaceSubmissionAPI(t)
			backend.allocationError = errors.New("lost backend response")
			backend.returnReceipt = receipt
			first := submitWorkspaceJob(api, "lost-response", "uncertain")
			require.Equal(t, 409, first.Code, first.Body.String())
			var failure V1ErrorResponse
			require.NoError(t, json.Unmarshal(first.Body.Bytes(), &failure))
			require.Equal(t, "workspace_allocation_inspection_required", failure.Code)
			require.NotNil(t, failure.WorkspaceAllocation)
			require.Equal(t, "allocating", failure.WorkspaceAllocation.State)
			require.False(t, *failure.Retryable)
			require.Equal(t, receipt, failure.WorkspaceAllocation.VolumeID != "")
			restarted := NewControlAPI(store, api.profiles, nil, NewWorkspaceManager(store, backend, nil), (&JobOrchestrator{}).WithBackgroundRunner(runner), "", nil)
			retry := submitWorkspaceJob(restarted, "lost-response", "uncertain")
			require.Equal(t, 409, retry.Code, retry.Body.String())
			require.Equal(t, int32(1), backend.creates.Load())
			require.Zero(t, backend.deletes.Load())
			require.Zero(t, runner.starts.Load())
			_, err := api.workspaceMgr.Create(context.Background(), "uncertain", "local-zfs", 10)
			require.ErrorIs(t, err, ErrWorkspaceExists)
			require.Equal(t, int32(1), backend.creates.Load())
		})
	}
}

func TestWorkspaceSubmissionKnownReceiptRecoversWithoutAllocation(t *testing.T) {
	api, store, backend, runner := workspaceSubmissionAPI(t)
	ctx := context.Background()
	request := V1JobCreateRequest{RepoURL: "https://example.com/repo.git", Ref: defaultJobRef, Profile: "default", Task: "test", Mode: defaultJobMode, WorkspaceCreate: &V1WorkspaceCreateRequest{Name: "recover", Storage: "local-zfs", SizeGB: 10}}
	before := httptest.NewRequest(http.MethodPost, "/v1/jobs", nil)
	before.Header.Set("Idempotency-Key", "known-receipt")
	submission, release, replayed := api.prepareJobSubmission(httptest.NewRecorder(), before, request)
	require.False(t, replayed)
	require.NotNil(t, submission)
	allocation, err := store.ReserveJobWorkspaceAllocation(ctx, db.JobWorkspaceAllocation{JobID: submission.JobID, WorkspaceID: "ws-recovered", Name: "recover", Storage: "local-zfs", SizeGB: 10})
	require.NoError(t, err)
	require.NoError(t, store.BeginJobWorkspaceAllocation(ctx, allocation.JobID))
	require.NoError(t, store.RecordJobWorkspaceVolume(ctx, allocation.JobID, "local-zfs:known-volume", true))
	release()
	// A failed transaction must preserve the volume receipt for the next retry.
	_, err = store.DB.Exec(`CREATE TRIGGER fail_workspace_insert BEFORE INSERT ON workspaces BEGIN SELECT RAISE(FAIL,'injected publication failure'); END`)
	require.NoError(t, err)
	first := submitWorkspaceJob(api, "known-receipt", "recover")
	require.Equal(t, 409, first.Code, first.Body.String())
	var failure V1ErrorResponse
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &failure))
	require.True(t, *failure.Retryable)
	require.Equal(t, "allocated", failure.WorkspaceAllocation.State)
	_, err = store.DB.Exec(`DROP TRIGGER fail_workspace_insert`)
	require.NoError(t, err)
	retry := submitWorkspaceJob(api, "known-receipt", "recover")
	require.Equal(t, 201, retry.Code, retry.Body.String())
	workspace, err := store.GetWorkspace(ctx, "ws-recovered")
	require.NoError(t, err)
	require.Equal(t, "local-zfs:known-volume", workspace.VolumeID)
	require.Equal(t, int32(1), runner.starts.Load())
	require.Zero(t, backend.creates.Load())
	require.Zero(t, backend.deletes.Load())
	// Expiry still prevents any side effect or replay under a fresh request.
	api.now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	expired := submitWorkspaceJob(api, "known-receipt", "recover")
	require.Equal(t, 410, expired.Code)
}

func TestWorkspaceSubmissionConflictAndRemovedWorkspace(t *testing.T) {
	api, store, backend, _ := workspaceSubmissionAPI(t)
	first := submitWorkspaceJob(api, "first", "one-workspace")
	require.Equal(t, 201, first.Code, first.Body.String())
	other := submitWorkspaceJob(api, "second", "one-workspace")
	require.Equal(t, 409, other.Code, other.Body.String())
	require.Equal(t, int32(1), backend.creates.Load())
	var result V1JobResponse
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &result))
	allocation, err := store.JobWorkspaceAllocation(context.Background(), result.ID)
	require.NoError(t, err)
	// Preserve the reservation when publication later fails or resources vanish.
	// A removed job is already protected by the submission tombstone.
	_, err = store.DB.Exec(`DELETE FROM jobs WHERE id=?`, result.ID)
	require.NoError(t, err)
	_, err = store.DB.Exec(`DELETE FROM workspaces WHERE id=?`, allocation.WorkspaceID)
	require.NoError(t, err)
	_, err = api.workspaceMgr.CreateForSubmission(context.Background(), db.JobSubmission{JobID: result.ID}, "one-workspace", "local-zfs", 10)
	var retained *workspaceAllocationError
	require.ErrorAs(t, err, &retained)
	require.Equal(t, "committed", retained.allocation.State)
	require.Equal(t, int32(1), backend.creates.Load())
	require.Zero(t, backend.deletes.Load())
}

func TestWorkspaceSubmissionCheckpointSurvivesClientCancellation(t *testing.T) {
	api, store, backend, _ := workspaceSubmissionAPI(t)
	ctx, cancel := context.WithCancel(context.Background())
	backend.cancel = cancel
	submission, err := store.ReserveJobSubmission(context.Background(), db.JobSubmission{Owner: "owner", KeyHash: "key", InputHash: "input", JobID: "job-cancelled-request", ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	require.NoError(t, err)
	_, err = api.workspaceMgr.CreateForSubmission(ctx, submission, "cancelled-request", "local-zfs", 10)
	var retained *workspaceAllocationError
	require.ErrorAs(t, err, &retained)
	allocation, err := store.JobWorkspaceAllocation(context.Background(), submission.JobID)
	require.NoError(t, err)
	require.Equal(t, "allocated", allocation.State)
	require.NotEmpty(t, allocation.VolumeID)
	backend.cancel = nil
	workspace, err := api.workspaceMgr.CreateForSubmission(context.Background(), submission, "cancelled-request", "local-zfs", 10)
	require.NoError(t, err)
	require.Equal(t, allocation.WorkspaceID, workspace.ID)
	require.Equal(t, int32(1), backend.creates.Load())
	require.Zero(t, backend.deletes.Load())
}

func TestWorkspaceSubmissionsCompeteForOneName(t *testing.T) {
	api, _, backend, _ := workspaceSubmissionAPI(t)
	var wg sync.WaitGroup
	var successes atomic.Int32
	for _, key := range []string{"one", "two", "three", "four"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			response := submitWorkspaceJob(api, key, "shared-name")
			if response.Code == 201 {
				successes.Add(1)
			} else if response.Code != 409 {
				t.Errorf("unexpected response: %d %s", response.Code, response.Body.String())
			}
		}(key)
	}
	wg.Wait()
	require.Equal(t, int32(1), successes.Load())
	require.Equal(t, int32(1), backend.creates.Load())
	require.Zero(t, backend.deletes.Load())
}

type mismatchedReceiptBackend struct{ *allocationBackend }

func (b mismatchedReceiptBackend) CreateVolume(context.Context, string, string, int) (string, error) {
	b.creates.Add(1)
	return "other-storage:unexpected-volume", nil
}

func TestWorkspaceAllocationRejectsMismatchedStorageReceipt(t *testing.T) {
	api, store, backend, _ := workspaceSubmissionAPI(t)
	api.workspaceMgr = NewWorkspaceManager(store, mismatchedReceiptBackend{backend}, nil)
	first := submitWorkspaceJob(api, "wrong-storage", "receipt-mismatch")
	require.Equal(t, 409, first.Code, first.Body.String())
	var failure V1ErrorResponse
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &failure))
	require.Equal(t, "allocating", failure.WorkspaceAllocation.State)
	require.Equal(t, "other-storage:unexpected-volume", failure.WorkspaceAllocation.VolumeID)
	require.False(t, *failure.Retryable)
	again := submitWorkspaceJob(api, "wrong-storage", "receipt-mismatch")
	require.Equal(t, 409, again.Code, again.Body.String())
	require.Equal(t, int32(1), backend.creates.Load())
	require.Zero(t, backend.deletes.Load())
	workspaces, err := store.ListWorkspaces(context.Background())
	require.NoError(t, err)
	require.Empty(t, workspaces)
}
