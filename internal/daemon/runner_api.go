package daemon

import (
	"errors"
	"net/http"
	"strings"

	"github.com/agentlab/agentlab/internal/models"
)

// RunnerAPI handles guest runner status reports.
type RunnerAPI struct {
	orchestrator *JobOrchestrator
}

func NewRunnerAPI(orchestrator *JobOrchestrator) *RunnerAPI {
	return &RunnerAPI{orchestrator: orchestrator}
}

func (api *RunnerAPI) Register(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("/v1/runner/report", api.handleRunnerReport)
}

func (api *RunnerAPI) handleRunnerReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, []string{http.MethodPost})
		return
	}
	var req V1RunnerReportRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	status, err := parseJobStatus(req.Status)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if api.orchestrator == nil {
		writeError(w, http.StatusServiceUnavailable, "job orchestration unavailable")
		return
	}
	report := JobReport{
		JobID:     strings.TrimSpace(req.JobID),
		VMID:      req.VMID,
		Status:    status,
		Message:   strings.TrimSpace(req.Message),
		Artifacts: req.Artifacts,
		Result:    req.Result,
	}
	if report.JobID == "" {
		writeError(w, http.StatusBadRequest, "job_id is required")
		return
	}
	if report.VMID <= 0 {
		writeError(w, http.StatusBadRequest, "vmid must be positive")
		return
	}
	if err := api.orchestrator.HandleReport(r.Context(), report); err != nil {
		switch {
		case errors.Is(err, ErrJobNotFound):
			writeError(w, http.StatusNotFound, "job not found")
		case errors.Is(err, ErrJobSandboxMismatch):
			writeError(w, http.StatusConflict, "job sandbox mismatch")
		case errors.Is(err, ErrJobFinalized):
			writeError(w, http.StatusConflict, "job already finalized")
		default:
			writeError(w, http.StatusInternalServerError, "failed to record job report")
		}
		return
	}
	resp := V1RunnerReportResponse{
		JobStatus: string(status),
	}
	if status == models.JobRunning {
		resp.SandboxStatus = string(models.SandboxRunning)
	} else if target := sandboxStateForJobStatus(status); target != "" {
		resp.SandboxStatus = string(target)
	}
	writeJSON(w, http.StatusOK, resp)
}

func parseJobStatus(value string) (models.JobStatus, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	switch normalized {
	case string(models.JobRunning):
		return models.JobRunning, nil
	case string(models.JobCompleted):
		return models.JobCompleted, nil
	case string(models.JobFailed):
		return models.JobFailed, nil
	case string(models.JobTimeout):
		return models.JobTimeout, nil
	default:
		return "", errors.New("invalid job status")
	}
}
