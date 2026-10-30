package daemon

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/agentlab/agentlab/internal/reposnapshot"
)

var repositoryIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (api *ControlAPI) repositoryRoot() string {
	if strings.TrimSpace(api.artifactRoot) == "" {
		return ""
	}
	return filepath.Join(api.artifactRoot, "repositories")
}

func repositoryError(w http.ResponseWriter, status int, code, action string, retryable bool) {
	submissionError(w, status, code, strings.ReplaceAll(code, "_", " "), action, retryable)
}

func (api *ControlAPI) handleRepositoryUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, []string{http.MethodPost})
		return
	}
	// Uploads are not bound to a sandbox. Scoped sandbox credentials cannot
	// allocate a new global source artifact.
	if !api.authorize(w, r, permRepositoryCreate, nil, true) {
		return
	}
	root := api.repositoryRoot()
	if root == "" || api.store == nil {
		repositoryError(w, 503, "repository_store_unavailable", "configure the artifact store before uploading a repository", false)
		return
	}
	if _, err := exec.LookPath("git"); err != nil {
		repositoryError(w, 503, "repository_git_unavailable", "install Git on the daemon host before uploading a repository", false)
		return
	}
	owner, err := requestOwner(r)
	if err != nil {
		repositoryError(w, 503, "server_identity_unavailable", "repair the server identity before retrying", false)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, (272<<20)+1)
	reader, err := r.MultipartReader()
	if err != nil {
		repositoryError(w, 400, "repository_upload_invalid", "send a manifest part followed by a bundle part", false)
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "manifest" {
		repositoryError(w, 400, "repository_upload_invalid", "send the snapshot manifest first", false)
		return
	}
	manifest, err := reposnapshot.ReadManifest(part)
	if err != nil {
		repositoryError(w, 400, "repository_manifest_invalid", "recreate the snapshot manifest before retrying", false)
		return
	}
	part, err = reader.NextPart()
	if err != nil || part.FormName() != "bundle" {
		repositoryError(w, 400, "repository_upload_invalid", "send the snapshot bundle after its manifest", false)
		return
	}
	metadata, _ := json.Marshal(manifest)
	sum := sha256.Sum256(append([]byte(owner+"\x00"), metadata...))
	id := hex.EncodeToString(sum[:])
	release, err := api.store.LockJobSubmission(r.Context(), "repository", id)
	if err != nil {
		repositoryError(w, 409, "repository_busy", "retry the same upload after the current request finishes", true)
		return
	}
	defer release()
	if err := os.MkdirAll(root, 0700); err != nil {
		repositoryError(w, 500, "repository_store_failed", "repair artifact storage permissions and retry the same upload", true)
		return
	}
	// The identity lock makes abandoned files from this upload safe to remove.
	entries, err := os.ReadDir(root)
	if err != nil {
		repositoryError(w, 500, "repository_store_failed", "repair artifact storage and retry", true)
		return
	}
	prefix := ".upload-" + id + "-"
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				repositoryError(w, 500, "repository_cleanup_failed", "repair artifact storage and retry the same upload", true)
				return
			}
		}
	}
	temp, err := os.MkdirTemp(root, prefix)
	if err != nil {
		repositoryError(w, 500, "repository_store_failed", "check artifact storage capacity and retry", true)
		return
	}
	defer os.RemoveAll(temp)
	bundle := filepath.Join(temp, "source.bundle")
	file, err := os.OpenFile(bundle, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		repositoryError(w, 500, "repository_store_failed", "check artifact storage capacity and retry", true)
		return
	}
	size, copyErr := io.Copy(file, io.LimitReader(part, (256<<20)+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil || size > 256<<20 {
		repositoryError(w, 413, "repository_upload_too_large", "send a complete bundle no larger than 256 MiB", false)
		return
	}
	if syncErr != nil || closeErr != nil {
		repositoryError(w, 500, "repository_store_failed", "check artifact storage capacity and retry", true)
		return
	}
	if _, err := reader.NextPart(); err != io.EOF {
		repositoryError(w, 400, "repository_upload_invalid", "send only the manifest and bundle parts", false)
		return
	}
	if err := reposnapshot.Validate(r.Context(), bundle, manifest); err != nil {
		repositoryError(w, 400, "repository_bundle_invalid", "recreate the repository snapshot and upload its matching bundle and manifest", false)
		return
	}
	now := api.now().UTC()
	record, existingOwner, err := api.store.GetRepositorySnapshot(r.Context(), id)
	exists := err == nil
	if err == nil {
		if existingOwner != owner || !bytes.Equal(mustRepositoryJSON(record.Manifest), metadata) {
			repositoryError(w, 409, "repository_identity_conflict", "inspect the original upload before retrying", false)
			return
		}
		if !now.Before(record.ExpiresAt) {
			repositoryError(w, 410, "repository_expired", "remove the expired upload before uploading this snapshot again", false)
			return
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		repositoryError(w, 500, "repository_store_failed", "retry the same upload", true)
		return
	}
	// Source bytes become durable before SQL publishes the reference. A retry
	// can complete publication if the daemon stops between these two writes.
	if err := syncRepositoryDirectory(temp); err != nil {
		repositoryError(w, 500, "repository_store_failed", "retry the same upload", true)
		return
	}
	destination := filepath.Join(root, id)
	if err := os.Rename(temp, destination); err != nil {
		if !sameRepositoryBundle(filepath.Join(destination, "source.bundle"), manifest) {
			repositoryError(w, 409, "repository_cache_changed", "remove the changed upload before retrying", false)
			return
		}
	}
	if err := syncRepositoryDirectory(root); err != nil {
		repositoryError(w, 500, "repository_store_failed", "retry the same upload", true)
		return
	}
	if !exists {
		record = protocol.RepositorySnapshot{ID: id, RepoURL: db.SnapshotRepositoryPrefix + id, Manifest: manifest, CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)}
	}
	record, err = api.store.SaveRepositorySnapshot(r.Context(), owner, record)
	if err != nil {
		repositoryError(w, 500, "repository_store_failed", "retry the same upload", true)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (api *ControlAPI) handleRepositoryByID(w http.ResponseWriter, r *http.Request) {
	permission := permRepositoryRead
	if r.Method == http.MethodDelete {
		permission = permRepositoryDelete
	} else if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, []string{http.MethodGet, http.MethodDelete})
		return
	}
	if !api.authorize(w, r, permission, nil, true) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/repositories/")
	if !repositoryIDPattern.MatchString(id) {
		writeError(w, 404, "repository not found")
		return
	}
	owner, err := requestOwner(r)
	if err != nil {
		repositoryError(w, 503, "server_identity_unavailable", "repair the server identity before retrying", false)
		return
	}
	release, err := api.store.LockJobSubmission(r.Context(), "repository", id)
	if err != nil {
		repositoryError(w, 409, "repository_busy", "retry after the current request finishes", true)
		return
	}
	defer release()
	record, actualOwner, err := api.store.GetRepositorySnapshot(r.Context(), id)
	if err != nil || actualOwner != owner {
		writeError(w, 404, "repository not found")
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, record)
		return
	}
	active, err := api.store.RepositorySnapshotActive(r.Context(), id)
	if err != nil {
		repositoryError(w, 500, "repository_store_failed", "retry after checking the job state", true)
		return
	}
	if active {
		repositoryError(w, 409, "repository_in_use", "wait for its queued and running jobs to finish before removing the upload", true)
		return
	}
	root := api.repositoryRoot()
	if root == "" {
		repositoryError(w, 503, "repository_store_unavailable", "restore the configured artifact store before cleanup", false)
		return
	}
	if err := os.RemoveAll(filepath.Join(root, id)); err != nil {
		repositoryError(w, 500, "repository_cleanup_failed", "repair artifact storage permissions and retry removal", true)
		return
	}
	if err := syncRepositoryDirectory(root); err != nil && !os.IsNotExist(err) {
		repositoryError(w, 500, "repository_cleanup_failed", "retry removal to confirm durable cleanup", true)
		return
	}
	if err := api.store.DeleteRepositorySnapshot(r.Context(), id, owner); err != nil {
		repositoryError(w, 500, "repository_cleanup_failed", "retry removal to finish metadata cleanup", true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "removed": true, "cleanup": "verified"})
}

func mustRepositoryJSON(value any) []byte { data, _ := json.Marshal(value); return data }
func syncRepositoryDirectory(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
func sameRepositoryBundle(path string, manifest reposnapshot.Manifest) bool {
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != manifest.BundleBytes {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(file, (256<<20)+1))
	return err == nil && size == manifest.BundleBytes && hex.EncodeToString(hash.Sum(nil)) == manifest.BundleSHA256
}

// prepareRepositoryJob pins admitted snapshot work before job creation. The same
// identity lock prevents removal until the job row protects the source.
func (api *ControlAPI) prepareRepositoryJob(w http.ResponseWriter, r *http.Request, request *V1JobCreateRequest) (func(), bool) {
	if !strings.HasPrefix(request.RepoURL, db.SnapshotRepositoryPrefix) {
		return func() {}, true
	}
	if !api.authorize(w, r, permRepositoryRead, nil, true) {
		return func() {}, false
	}
	if request.WorkspaceID != nil || request.WorkspaceCreate != nil || request.SessionID != nil {
		repositoryError(w, 400, "snapshot_workspace_unsupported", "use a stateless job for a local snapshot; existing workspaces remain unchanged", false)
		return func() {}, false
	}
	if profile, ok := api.profile(request.Profile); ok && profile.Type == models.SandboxTypeLXC {
		repositoryError(w, 400, "snapshot_backend_unsupported", "use a VM profile with an updated guest runner for snapshot jobs", false)
		return func() {}, false
	}
	id := strings.TrimPrefix(request.RepoURL, db.SnapshotRepositoryPrefix)
	if !repositoryIDPattern.MatchString(id) {
		repositoryError(w, 400, "repository_reference_invalid", "use the repo_url returned by repository upload", false)
		return func() {}, false
	}
	owner, err := requestOwner(r)
	if err != nil {
		repositoryError(w, 503, "server_identity_unavailable", "repair the server identity before retrying", false)
		return func() {}, false
	}
	release, err := api.store.LockJobSubmission(r.Context(), "repository", id)
	if err != nil {
		repositoryError(w, 409, "repository_busy", "retry the same submission after the current request finishes", true)
		return func() {}, false
	}
	record, actualOwner, err := api.store.GetRepositorySnapshot(r.Context(), id)
	if err != nil || owner != actualOwner {
		writeError(w, 404, "repository not found")
		return release, false
	}
	if !api.now().UTC().Before(record.ExpiresAt) {
		repositoryError(w, 410, "repository_expired", "inspect existing jobs, then remove and upload the expired snapshot again", false)
		return release, false
	}
	if request.Ref != record.Manifest.Revision {
		repositoryError(w, 400, "repository_revision_mismatch", "set ref to the revision returned by repository upload", false)
		return release, false
	}
	if api.repositoryRoot() == "" || !sameRepositoryBundle(filepath.Join(api.repositoryRoot(), id, "source.bundle"), record.Manifest) {
		repositoryError(w, 409, "repository_cache_changed", "repair the uploaded source before submitting new work", false)
		return release, false
	}
	return release, true
}
