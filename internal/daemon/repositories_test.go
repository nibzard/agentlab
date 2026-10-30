package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/agentlab/agentlab/internal/reposnapshot"
	"github.com/stretchr/testify/require"
)

func repositoryFixture(t *testing.T) reposnapshot.Result {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{{"init", "--initial-branch=main"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "Initial"}} {
		command := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=" + os.DevNull}, args...)...)
		output, err := command.CombinedOutput()
		require.NoError(t, err, "%s", output)
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "code.txt"), []byte("local changes\n"), 0600))
	result, err := reposnapshot.Create(context.Background(), reposnapshot.Options{Repository: root, OutputDir: t.TempDir()})
	require.NoError(t, err)
	return result
}

func uploadRepository(t *testing.T, api *ControlAPI, result reposnapshot.Result, actor *auth.RequestIdentity) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("manifest", "manifest.json")
	require.NoError(t, err)
	require.NoError(t, json.NewEncoder(part).Encode(result.Manifest))
	part, err = writer.CreateFormFile("bundle", "source.bundle")
	require.NoError(t, err)
	file, err := os.Open(result.BundlePath)
	require.NoError(t, err)
	_, err = io.Copy(part, file)
	file.Close()
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	request := httptest.NewRequest(http.MethodPost, "/v1/repositories", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if actor != nil {
		request = request.WithContext(auth.WithIdentity(request.Context(), actor))
	}
	response := httptest.NewRecorder()
	api.handleRepositoryUpload(response, request)
	return response
}

func repositoryRequest(api *ControlAPI, id, method string, actor *auth.RequestIdentity) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/v1/repositories/"+id, nil)
	if actor != nil {
		request = request.WithContext(auth.WithIdentity(request.Context(), actor))
	}
	response := httptest.NewRecorder()
	api.handleRepositoryByID(response, request)
	return response
}

func TestRepositoryUploadRetryRecoveryAndCleanup(t *testing.T) {
	api, store, _ := submissionAPI(t)
	api.artifactRoot = t.TempDir()
	fixture := repositoryFixture(t)
	first := uploadRepository(t, api, fixture, nil)
	require.Equal(t, 200, first.Code, first.Body.String())
	var original protocol.RepositorySnapshot
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &original))
	require.Equal(t, db.SnapshotRepositoryPrefix+original.ID, original.RepoURL)
	require.WithinDuration(t, original.CreatedAt.Add(30*24*time.Hour), original.ExpiresAt, time.Second)
	info, err := os.Stat(filepath.Join(api.repositoryRoot(), original.ID, "source.bundle"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	api = NewControlAPI(store, nil, nil, nil, nil, api.artifactRoot, nil)
	replay := uploadRepository(t, api, fixture, nil)
	require.Equal(t, 200, replay.Code, replay.Body.String())
	require.JSONEq(t, first.Body.String(), replay.Body.String())
	get := repositoryRequest(api, original.ID, http.MethodGet, nil)
	require.Equal(t, 200, get.Code)
	require.JSONEq(t, first.Body.String(), get.Body.String())
	remove := repositoryRequest(api, original.ID, http.MethodDelete, nil)
	require.Equal(t, 200, remove.Code, remove.Body.String())
	require.Contains(t, remove.Body.String(), `"cleanup":"verified"`)
	_, err = os.Stat(filepath.Join(api.repositoryRoot(), original.ID))
	require.True(t, os.IsNotExist(err))
	require.Equal(t, 404, repositoryRequest(api, original.ID, http.MethodGet, nil).Code)
}

func TestRepositoryUploadOwnershipScopeAndExpiry(t *testing.T) {
	api, _, _ := submissionAPI(t)
	api.artifactRoot = t.TempDir()
	fixture := repositoryFixture(t)
	alice := &auth.RequestIdentity{Method: "ssh-token", Fingerprint: "alice", Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"repository"}}}}
	bob := &auth.RequestIdentity{Method: "ssh-token", Fingerprint: "bob", Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"repository"}}}}
	first := uploadRepository(t, api, fixture, alice)
	require.Equal(t, 200, first.Code, first.Body.String())
	var original protocol.RepositorySnapshot
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &original))
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		require.Equal(t, 404, repositoryRequest(api, original.ID, method, bob).Code)
	}
	scoped := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"repository"}, Scope: []string{"sandbox:1001"}}}}
	require.Equal(t, 403, uploadRepository(t, api, fixture, scoped).Code)
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		require.Equal(t, 403, repositoryRequest(api, original.ID, method, scoped).Code)
	}
	second := uploadRepository(t, api, fixture, bob)
	require.Equal(t, 200, second.Code, second.Body.String())
	require.NotEqual(t, first.Body.String(), second.Body.String())
	api.now = func() time.Time { return original.ExpiresAt.Add(time.Second) }
	expired := uploadRepository(t, api, fixture, alice)
	require.Equal(t, 410, expired.Code, expired.Body.String())
	require.Contains(t, expired.Body.String(), "repository_expired")
}

func TestRepositoryUploadIntegrityAndConcurrency(t *testing.T) {
	api, _, _ := submissionAPI(t)
	api.artifactRoot = t.TempDir()
	fixture := repositoryFixture(t)
	var responses [4]*httptest.ResponseRecorder
	var group sync.WaitGroup
	for i := range responses {
		group.Add(1)
		go func(i int) { defer group.Done(); responses[i] = uploadRepository(t, api, fixture, nil) }(i)
	}
	group.Wait()
	for _, response := range responses {
		require.Equal(t, 200, response.Code, response.Body.String())
		require.JSONEq(t, responses[0].Body.String(), response.Body.String())
	}
	var original protocol.RepositorySnapshot
	require.NoError(t, json.Unmarshal(responses[0].Body.Bytes(), &original))
	require.NoError(t, os.WriteFile(filepath.Join(api.repositoryRoot(), original.ID, "source.bundle"), []byte("changed"), 0600))
	changed := uploadRepository(t, api, fixture, nil)
	require.Equal(t, 409, changed.Code, changed.Body.String())
	require.Contains(t, changed.Body.String(), "repository_cache_changed")
	fixture.Manifest.BundleSHA256 = strings.Repeat("0", 64)
	invalid := uploadRepository(t, api, fixture, nil)
	require.Equal(t, 400, invalid.Code, invalid.Body.String())
	entries, err := os.ReadDir(api.repositoryRoot())
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, strings.HasPrefix(entry.Name(), ".upload-"), "temporary upload leaked")
	}
}

func TestRepositoryRemovalProtectsActiveJobs(t *testing.T) {
	api, store, _ := submissionAPI(t)
	api.artifactRoot = t.TempDir()
	first := uploadRepository(t, api, repositoryFixture(t), nil)
	require.Equal(t, 200, first.Code, first.Body.String())
	var original protocol.RepositorySnapshot
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &original))
	now := time.Now().UTC()
	require.NoError(t, store.CreateJob(context.Background(), models.Job{ID: "uses-snapshot", RepoURL: original.RepoURL, Ref: original.Manifest.Revision, Profile: "default", Status: models.JobQueued, CreatedAt: now, UpdatedAt: now}))
	response := repositoryRequest(api, original.ID, http.MethodDelete, nil)
	require.Equal(t, 409, response.Code, response.Body.String())
	require.NoError(t, store.UpdateJobResult(context.Background(), "uses-snapshot", models.JobFailed, `{}`))
	require.Equal(t, 200, repositoryRequest(api, original.ID, http.MethodDelete, nil).Code)
}

func TestRepositoryJobAdmissionAndReplay(t *testing.T) {
	api, store, _ := submissionAPI(t)
	api.artifactRoot = t.TempDir()
	first := uploadRepository(t, api, repositoryFixture(t), nil)
	require.Equal(t, 200, first.Code, first.Body.String())
	var uploaded protocol.RepositorySnapshot
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &uploaded))
	create := func(key, ref string, workspace *string, actor *auth.RequestIdentity) *httptest.ResponseRecorder {
		data, _ := json.Marshal(V1JobCreateRequest{RepoURL: uploaded.RepoURL, Ref: ref, Task: "test local changes", Profile: "default", WorkspaceID: workspace})
		r := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(data))
		r.Header.Set("Idempotency-Key", key)
		if actor != nil {
			r = r.WithContext(auth.WithIdentity(r.Context(), actor))
		}
		w := httptest.NewRecorder()
		api.handleJobs(w, r)
		return w
	}
	bad := create("wrong-ref", "main", nil, nil)
	require.Equal(t, 400, bad.Code, bad.Body.String())
	workspace := "existing-workspace"
	bad = create("workspace", uploaded.Manifest.Revision, &workspace, nil)
	require.Equal(t, 400, bad.Code, bad.Body.String())
	other := &auth.RequestIdentity{Method: "ssh-token", Fingerprint: "other", Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"job.create", "repository.read"}}}}
	bad = create("other", uploaded.Manifest.Revision, nil, other)
	require.Equal(t, 404, bad.Code, bad.Body.String())
	created := create("local-job", uploaded.Manifest.Revision, nil, nil)
	require.Equal(t, 201, created.Code, created.Body.String())
	var job V1JobResponse
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &job))
	require.Equal(t, uploaded.Manifest.Revision, job.Ref)
	api.now = func() time.Time { return uploaded.ExpiresAt.Add(time.Second) }
	expired := create("expired-new-job", uploaded.Manifest.Revision, nil, nil)
	require.Equal(t, 410, expired.Code, expired.Body.String())
	api.now = time.Now
	require.NoError(t, store.UpdateJobResult(context.Background(), job.ID, models.JobFailed, `{}`))
	require.Equal(t, 200, repositoryRequest(api, uploaded.ID, http.MethodDelete, nil).Code)
	replay := create("local-job", uploaded.Manifest.Revision, nil, nil)
	require.Equal(t, 200, replay.Code, replay.Body.String())
	require.Contains(t, replay.Body.String(), job.ID)

}

func TestRepositoryRetryRemovesAbandonedUpload(t *testing.T) {
	api, _, _ := submissionAPI(t)
	api.artifactRoot = t.TempDir()
	fixture := repositoryFixture(t)
	first := uploadRepository(t, api, fixture, nil)
	require.Equal(t, 200, first.Code, first.Body.String())
	var original protocol.RepositorySnapshot
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &original))
	abandoned := filepath.Join(api.repositoryRoot(), ".upload-"+original.ID+"-abandoned")
	require.NoError(t, os.Mkdir(abandoned, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(abandoned, "partial.bundle"), []byte("partial"), 0600))
	replay := uploadRepository(t, api, fixture, nil)
	require.Equal(t, 200, replay.Code, replay.Body.String())
	_, err := os.Stat(abandoned)
	require.True(t, os.IsNotExist(err))
}
