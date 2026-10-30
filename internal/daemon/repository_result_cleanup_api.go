package daemon

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/protocol"
)

func (api *ControlAPI) handleRepositoryResultCleanup(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, []string{http.MethodPost})
		return
	}
	if api.jobOrchestrator == nil {
		repositoryError(w, 503, "repository_cleanup_unavailable", "restore the job orchestrator before retrying", true)
		return
	}
	releaseReport, err := api.store.LockJobSubmission(r.Context(), "job-report", id)
	if err != nil {
		repositoryError(w, 409, "repository_cleanup_busy", "observe the job and retry cleanup", true)
		return
	}
	defer releaseReport()
	releaseExecution, err := api.store.LockJobSubmission(r.Context(), "job-execution", id)
	if err != nil {
		repositoryError(w, 409, "repository_cleanup_busy", "observe the job and retry cleanup", true)
		return
	}
	defer releaseExecution()
	job, err := api.store.GetJob(r.Context(), id)
	if err != nil {
		writeError(w, 404, "job not found")
		return
	}
	if !api.authorize(w, r, permJobCleanup, func() int {
		if job.SandboxVMID != nil {
			return *job.SandboxVMID
		}
		return 0
	}, false) {
		return
	}
	var saved struct {
		Message   string               `json:"message"`
		Artifacts []V1ArtifactMetadata `json:"artifacts"`
		Result    json.RawMessage      `json:"result"`
	}
	var body map[string]json.RawMessage
	var receipt protocol.RepositoryResult
	if !strings.HasPrefix(job.RepoURL, protocol.RepositorySnapshotPrefix) || json.Unmarshal([]byte(job.ResultJSON), &saved) != nil || json.Unmarshal(saved.Result, &body) != nil || json.Unmarshal(body["repository_result"], &receipt) != nil || receipt.State != "verified" || receipt.Target == nil || job.SandboxVMID == nil || receipt.Target.VMID != *job.SandboxVMID || (job.Status != models.JobCompleted && job.Status != models.JobFailed && job.Status != models.JobTimeout) || api.jobOrchestrator.jobCancellationExists(r.Context(), id) {
		repositoryError(w, 409, "repository_cleanup_unverified", "collect verified results and inspect the saved sandbox identity before cleanup", false)
		return
	}
	if receipt.CleanupStatus == "verified" || receipt.CleanupStatus == "retained" {
		writeJSON(w, 200, map[string]any{"job_id": id, "accepted": false, "repository_result": receipt, "resources": receipt.Resources})
		return
	}
	receipt.CleanupStatus = "pending"
	receipt.ErrorCode = ""
	receipt.Action = "observe the job until cleanup finishes"
	body["repository_result"], _ = json.Marshal(receipt)
	result, _ := json.Marshal(body)
	data, err := buildJobResult(job.Status, saved.Message, saved.Artifacts, result, api.now())
	if err != nil || api.store.UpdateJobResult(r.Context(), id, job.Status, data) != nil {
		writeError(w, 500, "failed to save result cleanup request")
		return
	}
	api.jobOrchestrator.startRepositoryResultCleanup(id)
	writeJSON(w, 202, map[string]any{"job_id": id, "accepted": true, "repository_result": receipt, "resources": receipt.Resources})
}
