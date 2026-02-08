package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentlab/agentlab/internal/models"
	testutil "github.com/agentlab/agentlab/internal/testing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusHandler(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	sbRunning := testutil.NewTestSandbox(testutil.SandboxOpts{
		VMID:  201,
		Name:  "sandbox-running",
		State: models.SandboxRunning,
	})
	sbFailed := testutil.NewTestSandbox(testutil.SandboxOpts{
		VMID:  202,
		Name:  "sandbox-failed",
		State: models.SandboxFailed,
	})
	require.NoError(t, store.CreateSandbox(ctx, sbRunning))
	require.NoError(t, store.CreateSandbox(ctx, sbFailed))

	jobRunning := testutil.NewTestJob(testutil.JobOpts{ID: "job-running", Status: models.JobRunning})
	jobFailed := testutil.NewTestJob(testutil.JobOpts{ID: "job-failed", Status: models.JobFailed})
	require.NoError(t, store.CreateJob(ctx, jobRunning))
	require.NoError(t, store.CreateJob(ctx, jobFailed))

	jobID := jobFailed.ID
	vmid := sbFailed.VMID
	require.NoError(t, store.RecordEvent(ctx, "job.failed", &vmid, &jobID, "runner crash", ""))

	artifactRoot := t.TempDir()
	api := NewControlAPI(store, map[string]models.Profile{}, nil, nil, nil, artifactRoot, log.New(io.Discard, "", 0)).
		WithMetricsEnabled(true)

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	rec := httptest.NewRecorder()
	api.handleStatus(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp V1StatusResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))

	assert.Equal(t, 1, resp.Sandboxes[string(models.SandboxRunning)])
	assert.Equal(t, 1, resp.Sandboxes[string(models.SandboxFailed)])
	assert.Equal(t, 1, resp.Jobs[string(models.JobRunning)])
	assert.Equal(t, 1, resp.Jobs[string(models.JobFailed)])
	assert.True(t, resp.Metrics.Enabled)
	assert.Equal(t, artifactRoot, resp.Artifacts.Root)
	assert.Empty(t, resp.Artifacts.Error)
	assert.NotZero(t, resp.Artifacts.TotalBytes)
	assert.NotZero(t, resp.Artifacts.FreeBytes)
	require.Len(t, resp.RecentFailures, 1)
	assert.Equal(t, "job.failed", resp.RecentFailures[0].Kind)
}
