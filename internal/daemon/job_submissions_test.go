package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/stretchr/testify/require"
)

type submissionRunner struct{ starts atomic.Int32 }

func (r *submissionRunner) Go(string, func(context.Context)) bool { r.starts.Add(1); return true }
func (r *submissionRunner) LifecycleContext() context.Context     { return context.Background() }

func submissionAPI(t *testing.T) (*ControlAPI, *db.Store, *submissionRunner) {
	t.Helper()
	store := newTestStore(t)
	runner := &submissionRunner{}
	api := NewControlAPI(store, map[string]models.Profile{"default": {Name: "default", TemplateVM: 9000}}, nil, nil, (&JobOrchestrator{}).WithBackgroundRunner(runner), "", nil)
	return api, store, runner
}

func submitJob(api *ControlAPI, key, task string, actor *auth.RequestIdentity) *httptest.ResponseRecorder {
	payload, _ := json.Marshal(V1JobCreateRequest{RepoURL: "https://example.com/repo.git", Profile: "default", Task: task})
	request := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(payload))
	request.Header.Set("Idempotency-Key", key)
	if actor != nil {
		request = request.WithContext(auth.WithIdentity(request.Context(), actor))
	}
	response := httptest.NewRecorder()
	api.handleJobs(response, request)
	return response
}

func TestSubmissionRetryAndDaemonReconstruction(t *testing.T) {
	api, store, runner := submissionAPI(t)
	first := submitJob(api, "retry-key", "test", nil)
	require.Equal(t, 201, first.Code, first.Body.String())
	var original V1JobResponse
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &original))
	// Reconstruct the handler, as after a daemon restart. The job table remains
	// the only source of execution state, even if its profile has since changed.
	restarted := NewControlAPI(store, map[string]models.Profile{}, nil, nil, (&JobOrchestrator{}).WithBackgroundRunner(runner), "", nil)
	replay := submitJob(restarted, "retry-key", "test", nil)
	require.Equal(t, 200, replay.Code, replay.Body.String())
	var result V1JobResponse
	require.NoError(t, json.Unmarshal(replay.Body.Bytes(), &result))
	require.Equal(t, original.ID, result.ID)
	require.Equal(t, "true", replay.Header().Get("Idempotency-Replayed"))
	require.Equal(t, int32(1), runner.starts.Load())
	conflict := submitJob(api, "retry-key", "different", nil)
	require.Equal(t, 409, conflict.Code)
	var errorResponse V1ErrorResponse
	require.NoError(t, json.Unmarshal(conflict.Body.Bytes(), &errorResponse))
	require.Equal(t, "idempotency_conflict", errorResponse.Code)
	require.NotNil(t, errorResponse.Retryable)
	require.False(t, *errorResponse.Retryable)
	api.now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	expired := submitJob(api, "retry-key", "test", nil)
	require.Equal(t, 410, expired.Code)
	require.Equal(t, int32(1), runner.starts.Load())
}

func TestSubmissionConcurrentRequestsCreateOnce(t *testing.T) {
	api, store, runner := submissionAPI(t)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := submitJob(api, "concurrent", "test", nil)
			if response.Code != 200 && response.Code != 201 {
				t.Errorf("submission: %d %s", response.Code, response.Body.String())
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
		if first != id {
			t.Error("request created multiple jobs")
		}
	}
	var count int
	require.NoError(t, store.DB.QueryRow("SELECT COUNT(*) FROM jobs").Scan(&count))
	require.Equal(t, 1, count)
	require.Equal(t, int32(1), runner.starts.Load())
}

func TestSubmissionOwnerAndScopeRemainEffective(t *testing.T) {
	api, store, _ := submissionAPI(t)
	actor := func(fingerprint string) *auth.RequestIdentity {
		return &auth.RequestIdentity{Method: "ssh-token", Fingerprint: fingerprint, Subject: "task", Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"job.create"}}}}
	}
	alice := actor("alice")
	bob := actor("bob")
	first := submitJob(api, "shared-key", "test", alice)
	require.Equal(t, 201, first.Code)
	other := submitJob(api, "shared-key", "test", bob)
	require.Equal(t, 201, other.Code)
	var original V1JobResponse
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &original))
	_, err := store.UpdateJobSandbox(context.Background(), original.ID, 1002)
	require.NoError(t, err)
	alice.Token.Claims.Scope = []string{"sandbox:1001"}
	denied := submitJob(api, "shared-key", "test", alice)
	require.Equal(t, 403, denied.Code)
	alice.Token.Claims.Commands = []string{"job.read"}
	denied = submitJob(api, "new-key", "test", alice)
	require.Equal(t, 403, denied.Code)
	var count int
	require.NoError(t, store.DB.QueryRow("SELECT COUNT(*) FROM jobs").Scan(&count))
	require.Equal(t, 2, count)
}

func TestSubmissionRecoversLeaseBeforeJobInsert(t *testing.T) {
	api, store, runner := submissionAPI(t)
	backend := &orchestratorBackend{}
	api.workspaceMgr = NewWorkspaceManager(store, backend, nil)
	workspace, err := api.workspaceMgr.Create(context.Background(), "existing", "local-zfs", 10)
	require.NoError(t, err)
	request := V1JobCreateRequest{RepoURL: "https://example.com/repo.git", Ref: defaultJobRef, Profile: "default", Task: "test", Mode: defaultJobMode, WorkspaceID: &workspace.ID}
	payload, err := json.Marshal(request)
	require.NoError(t, err)
	before := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(payload))
	before.Header.Set("Idempotency-Key", "lease-retry")
	submission, release, replayed := api.prepareJobSubmission(httptest.NewRecorder(), before, request)
	require.False(t, replayed)
	require.NotNil(t, submission)
	require.NoError(t, store.BindSubmissionWorkspace(context.Background(), *submission, workspace.ID))
	acquired, err := store.TryAcquireWorkspaceLease(context.Background(), workspace.ID, workspaceLeaseOwnerForJob(submission.JobID), "nonce-before-disconnect", time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.True(t, acquired)
	release()
	retry := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(payload))
	retry.Header.Set("Idempotency-Key", "lease-retry")
	response := httptest.NewRecorder()
	api.handleJobs(response, retry)
	require.Equal(t, 201, response.Code, response.Body.String())
	var job V1JobResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &job))
	require.Equal(t, submission.JobID, job.ID)
	current, err := store.GetWorkspace(context.Background(), workspace.ID)
	require.NoError(t, err)
	require.Equal(t, "nonce-before-disconnect", current.LeaseNonce)
	require.Equal(t, int32(1), runner.starts.Load())
}

func TestSubmissionWorkspaceCreateRequiresManager(t *testing.T) {
	api, store, runner := submissionAPI(t)
	payload := []byte(`{"repo_url":"https://example.com/repo.git","profile":"default","task":"test","workspace_create":{"name":"new","size_gb":10}}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(payload))
	request.Header.Set("Idempotency-Key", "unsupported")
	response := httptest.NewRecorder()
	api.handleJobs(response, request)
	require.Equal(t, 500, response.Code)
	var result V1ErrorResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Contains(t, result.Message, "workspace manager unavailable")
	var count int
	require.NoError(t, store.DB.QueryRow("SELECT COUNT(*) FROM job_submissions").Scan(&count))
	require.Equal(t, 1, count)
	require.Zero(t, runner.starts.Load())
}

func TestSubmissionDoesNotRecreateRemovedJob(t *testing.T) {
	api, store, runner := submissionAPI(t)
	first := submitJob(api, "removed", "test", nil)
	require.Equal(t, 201, first.Code)
	var original V1JobResponse
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &original))
	_, err := store.DB.Exec("DELETE FROM jobs WHERE id=?", original.ID)
	require.NoError(t, err)
	retry := submitJob(api, "removed", "test", nil)
	require.Equal(t, 410, retry.Code, retry.Body.String())
	require.Contains(t, retry.Body.String(), "submission_job_removed")
	require.Equal(t, int32(1), runner.starts.Load())
}
