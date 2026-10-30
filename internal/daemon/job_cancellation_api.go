package daemon

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/protocol"
)

func (api *ControlAPI) handleJobCancellation(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method == http.MethodPost {
		release, err := api.store.LockJobSubmission(r.Context(), "job-report", id)
		if err != nil {
			submissionError(w, 409, "cancellation_busy", "job result finalization is in progress", "observe the job and retry cancellation if work remains active", true)
			return
		}
		defer release()
	}
	job, err := api.store.GetJob(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "job not found")
		} else {
			writeError(w, 500, "failed to load job")
		}
		return
	}
	if actor := auth.FromContext(r.Context()); actor != nil && actor.Token != nil && len(actor.Token.Claims.Scope) > 0 {
		if job.SandboxVMID == nil || !actor.IsSandboxAllowed(*job.SandboxVMID) {
			writeAuthzDenied(w, permJobCancel)
			return
		}
	}
	switch r.Method {
	case http.MethodGet:
		record, err := api.store.GetJobCancellation(r.Context(), id)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, 200, protocol.JobCancellation{JobID: id, State: "not_requested", CleanupStatus: "not_requested", WorkStopped: job.Status == "COMPLETED", Resources: []protocol.JobResource{}})
			return
		}
		if err != nil {
			writeError(w, 500, "failed to load cancellation")
			return
		}
		writeJSON(w, 200, record)
	case http.MethodPost:
		if api.jobOrchestrator == nil {
			submissionError(w, 503, "cancellation_unavailable", "job cancellation is unavailable", "restore the job orchestrator before retrying", true)
			return
		}
		record := protocol.JobCancellation{JobID: id, SandboxVMID: job.SandboxVMID, WorkspaceID: job.WorkspaceID, Actor: "local_trusted"}
		if actor := auth.FromContext(r.Context()); actor != nil {
			record.Actor = actor.Method + ":" + actor.Fingerprint
			if actor.Token != nil {
				record.Scope = append([]string{}, actor.Token.Claims.Scope...)
			}
		}
		record, err = api.store.RequestJobCancellation(r.Context(), record)
		if err != nil {
			writeError(w, 500, "failed to save job cancellation")
			return
		}
		if record.State == "cancelled" || record.State == "not_needed" || record.ErrorCode == "job_already_terminal" {
			writeJSON(w, 200, record)
			return
		}
		api.jobOrchestrator.StartCancellation(id)
		writeJSON(w, http.StatusAccepted, record)
	default:
		writeMethodNotAllowed(w, []string{http.MethodGet, http.MethodPost})
	}
}
