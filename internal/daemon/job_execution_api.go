package daemon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/executionarchive"
	"github.com/agentlab/agentlab/internal/hostidentity"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/protocol"
)

type V1JobExecutionResponse struct {
	JobID          string                    `json:"job_id"`
	ServerIdentity string                    `json:"server_identity"`
	ManifestSHA256 string                    `json:"manifest_sha256"`
	State          string                    `json:"state"`
	ArchiveSHA256  string                    `json:"archive_sha256"`
	ArchiveBytes   int64                     `json:"archive_bytes"`
	Evidence       executionarchive.Evidence `json:"evidence"`
	Entries        []executionarchive.Entry  `json:"entries"`
	Chunk          *executionarchive.Chunk   `json:"chunk,omitempty"`
}

func (api *ControlAPI) handleJobExecution(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, []string{http.MethodGet})
		return
	}
	if api.store == nil || api.artifactRoot == "" {
		repositoryError(w, 503, "execution_inspection_unavailable", "restore the job and artifact stores before inspecting execution logs", true)
		return
	}
	if id == "" || filepath.Base(id) != id {
		writeError(w, 404, "job not found")
		return
	}
	job, err := api.store.GetJob(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "job not found")
		return
	}
	if err != nil {
		writeError(w, 503, "job storage unavailable")
		return
	}
	var saved struct {
		Result struct {
			Receipt protocol.RepositoryResult `json:"repository_result"`
		} `json:"result"`
	}
	receipt := &saved.Result.Receipt
	sourceID := strings.TrimPrefix(job.RepoURL, protocol.RepositorySnapshotPrefix)
	if !strings.HasPrefix(job.RepoURL, protocol.RepositorySnapshotPrefix) || !repositoryIDPattern.MatchString(sourceID) || json.Unmarshal([]byte(job.ResultJSON), &saved) != nil || receipt.SourceID != sourceID || receipt.SourceRevision != job.Ref || receipt.State != "verified" || !repositoryIDPattern.MatchString(receipt.ManifestSHA256) || receipt.Execution == nil || job.SandboxVMID == nil || (job.Status != models.JobCompleted && job.Status != models.JobFailed && job.Status != models.JobTimeout) {
		repositoryError(w, 409, "execution_evidence_unverified", "execution logs are not verified yet; inspect job progress or download its available artifacts", false)
		return
	}
	options := executionarchive.InspectOptions{Path: r.URL.Query().Get("path")}
	for _, name := range []string{"path", "offset", "limit", "archive_sha256"} {
		if len(r.URL.Query()[name]) > 1 {
			repositoryError(w, 400, "invalid_execution_query", "provide one path, offset, limit, and archive digest", false)
			return
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		options.Offset, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			repositoryError(w, 400, "invalid_execution_query", "use a nonnegative byte offset returned by the server", false)
			return
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		options.Limit, err = strconv.Atoi(raw)
		if err != nil || options.Limit < 4 || options.Limit > executionarchive.MaxChunkBytes {
			repositoryError(w, 400, "invalid_execution_query", "use a text limit from 4 through 65536 bytes", false)
			return
		}
	}
	path := "repository-results/" + receipt.ManifestSHA256 + "/execution.tar.gz"
	artifacts, err := api.store.ListArtifactsByJob(r.Context(), id)
	if err != nil {
		writeError(w, 503, "artifact storage unavailable")
		return
	}
	var selected *db.Artifact
	for i := range artifacts {
		if artifacts[i].Path == path {
			if selected != nil {
				repositoryError(w, 409, "execution_archive_unverified", "preserve duplicate artifact records and inspect this job", false)
				return
			}
			selected = &artifacts[i]
		}
	}
	if selected == nil || selected.VMID == nil || *selected.VMID != *job.SandboxVMID {
		repositoryError(w, 409, "execution_archive_unverified", "restore the execution archive bound to this job's sandbox", false)
		return
	}
	if expected := r.URL.Query().Get("archive_sha256"); expected != "" && expected != selected.Sha256 {
		repositoryError(w, 409, "execution_archive_changed", "reload the execution index before reading another log chunk", false)
		return
	}
	root, err := os.OpenRoot(api.artifactRoot)
	if err != nil {
		writeError(w, 503, "artifact storage unavailable")
		return
	}
	defer root.Close()
	file, err := root.OpenFile(filepath.Join(id, filepath.FromSlash(path)), os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		repositoryError(w, 409, "execution_archive_unavailable", "restore the recorded execution archive or collect it again from the retained sandbox", false)
		return
	}
	defer file.Close()
	inspection, err := executionarchive.Inspect(r.Context(), file, executionarchive.Expected{JobID: job.ID, SourceID: receipt.SourceID, SourceRevision: job.Ref, ManifestSHA256: receipt.ManifestSHA256, Status: string(job.Status), TestCommands: job.TestCommands}, selected.Sha256, selected.SizeBytes, options)
	if err != nil {
		switch {
		case errors.Is(err, executionarchive.ErrEntryNotFound):
			repositoryError(w, 404, "execution_entry_not_found", "select a log path from this job's execution index", false)
		case errors.Is(err, executionarchive.ErrEntryNotText):
			repositoryError(w, 415, "execution_entry_not_text", "download the archive to inspect this non-UTF-8 entry", false)
		case errors.Is(err, executionarchive.ErrInvalidOffset):
			repositoryError(w, 400, "invalid_execution_query", "use the next byte offset returned by the server and a limit from 4 through 65536 bytes", false)
		default:
			repositoryError(w, 409, "execution_archive_unverified", "preserve this job's artifacts; the execution archive failed verification", false)
		}
		return
	}
	if !executionarchive.Equal(inspection.Evidence, *receipt.Execution) {
		repositoryError(w, 409, "execution_evidence_changed", "preserve the archive and inspect the saved execution receipt", false)
		return
	}
	identity, err := hostidentity.Local()
	if err != nil {
		writeError(w, 503, "server identity unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, V1JobExecutionResponse{JobID: id, ServerIdentity: identity, ManifestSHA256: receipt.ManifestSHA256, State: "verified", ArchiveSHA256: inspection.ArchiveSHA256, ArchiveBytes: inspection.ArchiveBytes, Evidence: inspection.Evidence, Entries: inspection.Entries, Chunk: inspection.Chunk})
}
