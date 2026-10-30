package daemon

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
)

func (api *RunnerAPI) WithRepositoryRoot(artifactRoot string) *RunnerAPI {
	if strings.TrimSpace(artifactRoot) != "" {
		api.repositoryRoot = filepath.Join(artifactRoot, "repositories")
	}
	return api
}

func (api *RunnerAPI) handleRunnerRepository(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, []string{http.MethodGet})
		return
	}
	if !api.remoteAllowed(r.RemoteAddr) {
		writeError(w, 403, "repository access restricted to agent subnet")
		return
	}
	vmid, err := strconv.Atoi(r.URL.Query().Get("vmid"))
	if err != nil || vmid <= 0 || !verifySandboxSecret(r.Context(), api.store, r, vmid) {
		writeError(w, 403, "invalid or missing sandbox secret")
		return
	}
	jobID := r.URL.Query().Get("job_id")
	job, err := api.store.GetJob(r.Context(), jobID)
	if err != nil || job.SandboxVMID == nil || *job.SandboxVMID != vmid || (job.Status != models.JobQueued && job.Status != models.JobRunning) {
		writeError(w, 403, "repository access requires the current active job")
		return
	}
	if _, err := api.store.GetJobCancellation(r.Context(), jobID); err == nil || !errors.Is(err, sql.ErrNoRows) {
		writeError(w, 409, "job cancellation prevents repository delivery")
		return
	}
	sandbox, err := api.store.GetSandbox(r.Context(), vmid)
	if err != nil || sandbox.State == models.SandboxDestroyed {
		writeError(w, 403, "sandbox identity revoked")
		return
	}
	if api.repositoryRoot == "" {
		writeError(w, 503, "repository delivery unavailable")
		return
	}
	id := strings.TrimPrefix(job.RepoURL, db.SnapshotRepositoryPrefix)
	if !strings.HasPrefix(job.RepoURL, db.SnapshotRepositoryPrefix) || !repositoryIDPattern.MatchString(id) {
		writeError(w, 404, "job has no repository snapshot")
		return
	}
	record, _, err := api.store.GetRepositorySnapshot(r.Context(), id)
	if err != nil || record.Manifest.Revision != job.Ref {
		writeError(w, 503, "job repository snapshot unavailable")
		return
	}
	path := filepath.Join(api.repositoryRoot, id, "source.bundle")
	if !sameRepositoryBundle(path, record.Manifest) {
		writeError(w, 409, "repository bundle changed")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		writeError(w, 503, "repository bundle unavailable")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/x-git-bundle")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", `"`+record.Manifest.BundleSHA256+`"`)
	http.ServeContent(w, r, "source.bundle", record.CreatedAt, file)
}
