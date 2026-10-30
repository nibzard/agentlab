package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestedTestsPersistAndBindKeyedSubmission(t *testing.T) {
	api, store, runner := submissionAPI(t)
	send := func(commands []string) *httptest.ResponseRecorder {
		data, err := json.Marshal(V1JobCreateRequest{RepoURL: "https://example.com/repo.git", Profile: "default", Task: "change", TestCommands: commands})
		require.NoError(t, err)
		r := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(data))
		r.Header.Set("Idempotency-Key", "explicit-tests")
		w := httptest.NewRecorder()
		api.handleJobs(w, r)
		return w
	}
	commands := []string{"go test ./...", "printf 'two\\n'\n"}
	first := send(commands)
	require.Equal(t, 201, first.Code, first.Body.String())
	var view V1JobResponse
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &view))
	require.Equal(t, commands, view.TestCommands)
	stored, err := store.GetJob(context.Background(), view.ID)
	require.NoError(t, err)
	require.Equal(t, commands, stored.TestCommands)
	require.Equal(t, commands, bootstrapJobFromModel(stored).TestCommands)
	require.Equal(t, 200, send(commands).Code)
	changed := send([]string{"true"})
	require.Equal(t, 409, changed.Code, changed.Body.String())
	require.Contains(t, changed.Body.String(), "idempotency_conflict")
	require.Equal(t, int32(1), runner.starts.Load())
}
func TestInvalidRequestedTestsCreateNoResources(t *testing.T) {
	api, store, runner := submissionAPI(t)
	for _, commands := range [][]string{{""}, {strings.Repeat("a", 4097)}, make([]string, 17), {"true\x00false"}} {
		data, _ := json.Marshal(V1JobCreateRequest{RepoURL: "https://example.com/repo.git", Profile: "default", Task: "change", TestCommands: commands})
		r := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(data))
		r.Header.Set("Idempotency-Key", "invalid-tests")
		w := httptest.NewRecorder()
		api.handleJobs(w, r)
		require.Equal(t, 400, w.Code, w.Body.String())
	}
	var count int
	require.NoError(t, store.DB.QueryRow("SELECT COUNT(*) FROM jobs").Scan(&count))
	require.Zero(t, count)
	require.Zero(t, runner.starts.Load())
}

func TestURLJobMissingTestEvidencePreservesSandbox(t *testing.T) {
	o, backend, job, report := repositoryReportFixture(t, true, false)
	_, err := o.store.DB.Exec(`UPDATE jobs SET repo_url=?,test_commands=? WHERE id=?`, "https://example.com/repo.git", `["go test ./..."]`, job.ID)
	require.NoError(t, err)
	require.ErrorContains(t, o.HandleReport(context.Background(), report), "requested test evidence rejected")
	saved, err := o.store.GetJob(context.Background(), job.ID)
	require.NoError(t, err)
	require.Equal(t, job.Status, saved.Status)
	status, err := backend.Status(context.Background(), 1100)
	require.NoError(t, err)
	require.Equal(t, "running", string(status))
}
