package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/stretchr/testify/require"
)

func TestJobExecutionUsesVerifiedReceiptAndSandboxScope(t *testing.T) {
	o, _, job, report := repositoryReportFixture(t, true, true)
	require.NoError(t, o.HandleReport(context.Background(), report))
	api := NewControlAPI(o.store, nil, nil, nil, o, o.artifactRoot, nil)
	actor := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"job.artifacts"}, Scope: []string{"sandbox:1100"}}}}
	request := func(query string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+job.ID+"/execution"+query, nil)
		r = r.WithContext(auth.WithIdentity(r.Context(), actor))
		w := httptest.NewRecorder()
		api.handleJobByID(w, r)
		return w
	}
	index := request("")
	require.Equal(t, 200, index.Code, index.Body.String())
	require.Equal(t, "no-store", index.Header().Get("Cache-Control"))
	var result V1JobExecutionResponse
	require.NoError(t, json.Unmarshal(index.Body.Bytes(), &result))
	require.Equal(t, job.ID, result.JobID)
	require.Len(t, result.Entries, 2)
	page := request("?path=agent-runner.log&limit=4&archive_sha256=" + result.ArchiveSHA256)
	require.Equal(t, 200, page.Code, page.Body.String())
	require.NoError(t, json.Unmarshal(page.Body.Bytes(), &result))
	require.Equal(t, "agen", result.Chunk.Text)
	require.EqualValues(t, 4, result.Chunk.NextOffset)
	actor.Token.Claims.Scope = []string{"sandbox:1001"}
	require.Equal(t, 403, request("").Code)
	actor.Token.Claims.Scope = []string{"sandbox:1100"}
	actor.Token.Claims.Commands = []string{"job.read"}
	require.Equal(t, 403, request("").Code)
	actor.Token.Claims.Commands = []string{"job.artifacts"}
	require.Equal(t, 409, request("?archive_sha256="+strings.Repeat("f", 64)).Code)
	require.Equal(t, 400, request("?path=agent-runner.log&offset=-1").Code)
	require.Equal(t, 400, request("?path=agent-runner.log&limit=0").Code)
	require.Equal(t, 400, request("?path=agent-runner.log&path=report.json").Code)
	require.Equal(t, 404, request("?path=../secret").Code)
	artifacts, err := o.store.ListArtifactsByJob(context.Background(), job.ID)
	require.NoError(t, err)
	for _, artifact := range artifacts {
		if artifact.Name == "execution.tar.gz" {
			require.NoError(t, os.WriteFile(filepath.Join(o.artifactRoot, job.ID, filepath.FromSlash(artifact.Path)), []byte("must-not-leak"), 0600))
		}
	}
	changed := request("?path=agent-runner.log")
	require.Equal(t, 409, changed.Code)
	require.NotContains(t, changed.Body.String(), "must-not-leak")
}
func TestJobExecutionBlocksBeforeResultVerification(t *testing.T) {
	o, _, job, _ := repositoryReportFixture(t, true, true)
	api := NewControlAPI(o.store, nil, nil, nil, o, o.artifactRoot, nil)
	w := httptest.NewRecorder()
	api.handleJobByID(w, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+job.ID+"/execution", nil))
	require.Equal(t, 409, w.Code)
	require.Contains(t, w.Body.String(), "execution_evidence_unverified")
}

func TestJobExecutionConfinesArtifactReads(t *testing.T) {
	o, _, job, report := repositoryReportFixture(t, true, true)
	require.NoError(t, o.HandleReport(context.Background(), report))
	api := NewControlAPI(o.store, nil, nil, nil, o, o.artifactRoot, nil)
	artifacts, err := o.store.ListArtifactsByJob(context.Background(), job.ID)
	require.NoError(t, err)
	for _, artifact := range artifacts {
		if artifact.Name == "execution.tar.gz" {
			path := filepath.Join(o.artifactRoot, job.ID, filepath.FromSlash(artifact.Path))
			original, err := os.ReadFile(path)
			require.NoError(t, err)
			foreign := filepath.Join(t.TempDir(), "foreign-archive")
			require.NoError(t, os.WriteFile(foreign, original, 0600))
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Symlink(foreign, path))
			w := httptest.NewRecorder()
			api.handleJobByID(w, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+job.ID+"/execution?path=agent-runner.log", nil))
			require.Equal(t, 409, w.Code)
			require.NotContains(t, w.Body.String(), "agent finished")
			require.NoError(t, os.Remove(path))
			original[4] ^= 1
			require.NoError(t, os.WriteFile(path, original, 0600))
			w = httptest.NewRecorder()
			api.handleJobByID(w, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+job.ID+"/execution?path=agent-runner.log", nil))
			require.Equal(t, 409, w.Code)
			require.NotContains(t, w.Body.String(), "agent finished")
		}
	}
}

func TestJobExecutionRejectsSpecialArchiveWithoutWaiting(t *testing.T) {
	o, _, job, report := repositoryReportFixture(t, true, true)
	require.NoError(t, o.HandleReport(context.Background(), report))
	api := NewControlAPI(o.store, nil, nil, nil, o, o.artifactRoot, nil)
	artifacts, err := o.store.ListArtifactsByJob(context.Background(), job.ID)
	require.NoError(t, err)
	for _, artifact := range artifacts {
		if artifact.Name == "execution.tar.gz" {
			path := filepath.Join(o.artifactRoot, job.ID, filepath.FromSlash(artifact.Path))
			require.NoError(t, os.Remove(path))
			require.NoError(t, syscall.Mkfifo(path, 0600))
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				w := httptest.NewRecorder()
				api.handleJobByID(w, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+job.ID+"/execution", nil))
				done <- w
			}()
			select {
			case response := <-done:
				require.Equal(t, 409, response.Code)
			case <-time.After(time.Second):
				// Unblock a regressed blocking open before reporting failure.
				writer, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
				if err == nil {
					writer.Close()
				}
				t.Fatal("execution inspection waited on a special archive file")
			}
		}
	}
}
