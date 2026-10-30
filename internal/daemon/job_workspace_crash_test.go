package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
	"github.com/stretchr/testify/require"
)

type exitingWorkspaceBackend struct {
	proxmox.Backend
	marker string
}

func (b exitingWorkspaceBackend) CreateVolume(_ context.Context, storage, name string, _ int) (string, error) {
	file, err := os.OpenFile(b.marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	_, err = file.WriteString(storage + ":" + name)
	if err != nil {
		file.Close()
		return "", err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return "", err
	}
	file.Close()
	// The external allocation persists, but the backend response is lost.
	os.Exit(42)
	return "", nil
}

func TestWorkspaceAllocationProcessTermination(t *testing.T) {
	if os.Getenv("AGENTLAB_TEST_WORKSPACE_ALLOCATION_CHILD") == "1" {
		store, err := db.Open(os.Getenv("AGENTLAB_TEST_WORKSPACE_ALLOCATION_DB"))
		require.NoError(t, err)
		defer store.Close()
		manager := NewWorkspaceManager(store, exitingWorkspaceBackend{Backend: &orchestratorBackend{}, marker: os.Getenv("AGENTLAB_TEST_WORKSPACE_ALLOCATION_MARKER")}, nil)
		_, err = manager.CreateForSubmission(context.Background(), db.JobSubmission{JobID: os.Getenv("AGENTLAB_TEST_WORKSPACE_ALLOCATION_JOB")}, "crash-workspace", "local-zfs", 10)
		t.Fatalf("worker did not reach the termination boundary: %v", err)
	}
	root := t.TempDir()
	path, marker := filepath.Join(root, "state.db"), filepath.Join(root, "external-volume")
	store, err := db.Open(path)
	require.NoError(t, err)
	request := V1JobCreateRequest{RepoURL: "https://example.com/repo.git", Ref: defaultJobRef, Profile: "default", Task: "test", Mode: defaultJobMode, WorkspaceCreate: &V1WorkspaceCreateRequest{Name: "crash-workspace", Storage: "local-zfs", SizeGB: 10}}
	runner := &submissionRunner{}
	api := NewControlAPI(store, map[string]models.Profile{"default": {Name: "default", TemplateVM: 9000}}, nil, nil, (&JobOrchestrator{}).WithBackgroundRunner(runner), "", nil)
	before := httptest.NewRequest(http.MethodPost, "/v1/jobs", nil)
	before.Header.Set("Idempotency-Key", "process-crash")
	submission, release, replayed := api.prepareJobSubmission(httptest.NewRecorder(), before, request)
	require.False(t, replayed)
	require.NotNil(t, submission)
	release()
	require.NoError(t, store.Close())
	child := exec.Command(os.Args[0], "-test.run=^TestWorkspaceAllocationProcessTermination$")
	child.Env = append(os.Environ(), "AGENTLAB_TEST_WORKSPACE_ALLOCATION_CHILD=1", "AGENTLAB_TEST_WORKSPACE_ALLOCATION_DB="+path, "AGENTLAB_TEST_WORKSPACE_ALLOCATION_MARKER="+marker, "AGENTLAB_TEST_WORKSPACE_ALLOCATION_JOB="+submission.JobID)
	output, err := child.CombinedOutput()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, string(output))
	require.Equal(t, 42, exit.ExitCode(), string(output))
	// Reopen SQLite after actual process death. The released kernel lock permits
	// observation, but does not authorize another backend allocation.
	store, err = db.Open(path)
	require.NoError(t, err)
	defer store.Close()
	backend := &allocationBackend{Backend: &orchestratorBackend{}}
	api = NewControlAPI(store, api.profiles, nil, NewWorkspaceManager(store, backend, nil), (&JobOrchestrator{}).WithBackgroundRunner(runner), "", nil)
	retry := submitWorkspaceJob(api, "process-crash", "crash-workspace")
	require.Equal(t, 409, retry.Code, retry.Body.String())
	var failure V1ErrorResponse
	require.NoError(t, json.Unmarshal(retry.Body.Bytes(), &failure))
	require.Equal(t, "blocked", failure.State)
	require.Equal(t, submission.JobID, failure.WorkspaceAllocation.JobID)
	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "local-zfs:"+failure.WorkspaceAllocation.AllocationName, string(data))
	require.Zero(t, backend.creates.Load())
	require.Zero(t, backend.deletes.Load())
	require.Zero(t, runner.starts.Load())
}
