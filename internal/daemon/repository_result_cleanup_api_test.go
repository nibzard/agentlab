package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/agentlab/agentlab/internal/proxmox"
	"github.com/stretchr/testify/require"
)

func resultCleanupRequest(t *testing.T, o *JobOrchestrator, id string, commands, scope []string) *httptest.ResponseRecorder {
	t.Helper()
	api := NewControlAPI(o.store, nil, o.sandboxManager, nil, o, o.artifactRoot, nil)
	r := httptest.NewRequest(http.MethodPost, "/v1/jobs/"+id+"/result/cleanup", nil)
	if commands != nil {
		actor := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{Commands: commands, Scope: scope}}}
		r = r.WithContext(auth.WithIdentity(r.Context(), actor))
	}
	w := httptest.NewRecorder()
	api.handleJobByID(w, r)
	return w
}

func TestRepositoryCleanupRequestPreservesTargetAndResumes(t *testing.T) {
	o, _, job := pendingRepositoryResult(t, false)
	_, original := repositoryReceipt(t, o, job.ID)
	runner := &submissionRunner{}
	o.WithBackgroundRunner(runner)
	response := resultCleanupRequest(t, o, job.ID, []string{"job.cleanup"}, []string{"sandbox:1100"})
	require.Equal(t, 202, response.Code, response.Body.String())
	_, receipt := repositoryReceipt(t, o, job.ID)
	require.Equal(t, original.Target, receipt.Target)
	require.Equal(t, "pending", receipt.CleanupStatus)
	require.EqualValues(t, 1, runner.starts.Load())
	require.NoError(t, o.reconcileRepositoryResult(context.Background(), job.ID))
	_, receipt = repositoryReceipt(t, o, job.ID)
	require.Equal(t, "verified", receipt.CleanupStatus)
	require.Empty(t, receipt.Action)
	response = resultCleanupRequest(t, o, job.ID, []string{"job.cleanup"}, []string{"sandbox:1100"})
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"accepted":false`)
	require.EqualValues(t, 1, runner.starts.Load())
}

func TestRepositoryCleanupRequestDenialsPreserveWork(t *testing.T) {
	for _, test := range []struct{ commands, scope []string }{
		{[]string{"job.read"}, []string{"sandbox:1100"}},
		{[]string{"job.cleanup"}, []string{"sandbox:9999"}},
	} {
		o, backend, job := pendingRepositoryResult(t, false)
		runner := &submissionRunner{}
		o.WithBackgroundRunner(runner)
		before := job.ResultJSON
		response := resultCleanupRequest(t, o, job.ID, test.commands, test.scope)
		require.Equal(t, 403, response.Code, response.Body.String())
		after, err := o.store.GetJob(context.Background(), job.ID)
		require.NoError(t, err)
		require.Equal(t, before, after.ResultJSON)
		require.Zero(t, runner.starts.Load())
		status, err := backend.Status(context.Background(), 1100)
		require.NoError(t, err)
		require.Equal(t, proxmox.StatusRunning, status)
	}
}

func TestRepositoryCleanupRequestCannotReplaceSavedIdentity(t *testing.T) {
	o, backend, job := pendingRepositoryResult(t, false)
	_, original := repositoryReceipt(t, o, job.ID)
	require.NoError(t, backend.Destroy(context.Background(), 1100))
	require.NoError(t, backend.Clone(context.Background(), 9000, 1100, "result-owned"))
	require.NoError(t, backend.Start(context.Background(), 1100))
	require.NoError(t, o.reconcileRepositoryResult(context.Background(), job.ID))
	_, blocked := repositoryReceipt(t, o, job.ID)
	require.Equal(t, "blocked", blocked.CleanupStatus)
	active, err := o.store.RepositorySnapshotActive(context.Background(), original.SourceID)
	require.NoError(t, err)
	require.True(t, active, "blocked cleanup must keep the evidence needed for a retry")
	o.WithBackgroundRunner(&submissionRunner{})
	response := resultCleanupRequest(t, o, job.ID, nil, nil)
	require.Equal(t, 202, response.Code, response.Body.String())
	require.NoError(t, o.reconcileRepositoryResult(context.Background(), job.ID))
	_, after := repositoryReceipt(t, o, job.ID)
	require.Equal(t, original.Target, after.Target)
	require.Equal(t, "blocked", after.CleanupStatus)
	status, err := backend.Status(context.Background(), 1100)
	require.NoError(t, err)
	require.Equal(t, proxmox.StatusRunning, status)
}

func TestRepositoryCleanupRequestRequiresVerifiedEvidence(t *testing.T) {
	o, backend, job := pendingRepositoryResult(t, false)
	var saved map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(job.ResultJSON), &saved))
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(saved["result"], &body))
	body["repository_result"], _ = json.Marshal(protocol.RepositoryResult{State: "blocked", CleanupStatus: "blocked"})
	saved["result"], _ = json.Marshal(body)
	data, _ := json.Marshal(saved)
	require.NoError(t, o.store.UpdateJobResult(context.Background(), job.ID, models.JobFailed, string(data)))
	runner := &submissionRunner{}
	o.WithBackgroundRunner(runner)
	response := resultCleanupRequest(t, o, job.ID, nil, nil)
	require.Equal(t, 409, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "repository_cleanup_unverified")
	require.Zero(t, runner.starts.Load())
	status, err := backend.Status(context.Background(), 1100)
	require.NoError(t, err)
	require.Equal(t, proxmox.StatusRunning, status)
}
