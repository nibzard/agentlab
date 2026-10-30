package daemon

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/agentlab/agentlab/internal/proxmox"
)

type workspaceForkError struct {
	Operation protocol.WorkspaceForkOperation
	Cause     error
}

func (e *workspaceForkError) Error() string {
	return "workspace fork stopped; inspect the saved operation and retained resources"
}
func (e *workspaceForkError) Unwrap() error { return e.Cause }

func writeWorkspaceForkFailure(w http.ResponseWriter, err error) bool {
	var requirement *proxmox.WorkspaceStorageRequirement
	if errors.As(err, &requirement) {
		response := daemonErrorResponse(http.StatusConflict, requirement.Error())
		response.Code = "workspace_storage_helper_required"
		response.State = "blocked"
		response.Action = "use a local Proxmox shell backend, or enable the explicit local helper for a loopback API backend"
		retryable := false
		response.Retryable = &retryable
		writeJSON(w, http.StatusConflict, response)
		return true
	}
	var retained *workspaceForkError
	if !errors.As(err, &retained) {
		return false
	}
	response := daemonErrorResponse(http.StatusConflict, retained.Error())
	response.Code = "workspace_fork_inspection_required"
	response.State = "blocked"
	response.WorkspaceFork = &retained.Operation
	retryable := false
	response.Retryable = &retryable
	response.Action = "read workspace fork-status for the source and operation ID; use fork-resume when the daemon supports native recovery; preserve resources if recovery remains blocked"
	writeJSON(w, http.StatusConflict, response)
	return true
}

func (api *ControlAPI) handleWorkspaceForkStatus(w http.ResponseWriter, r *http.Request, source, id string) {
	if api.workspaceMgr == nil {
		writeError(w, 503, "workspace manager unavailable")
		return
	}
	workspace, err := api.workspaceMgr.Resolve(r.Context(), source)
	if err != nil {
		writeError(w, 404, "workspace not found")
		return
	}
	if id == "" {
		operations, err := api.store.ListWorkspaceForks(r.Context(), workspace.ID)
		if err != nil {
			writeError(w, 500, "failed to list workspace fork operations")
			return
		}
		writeJSON(w, http.StatusOK, V1WorkspaceForksResponse{Operations: operations})
		return
	}
	operation, err := api.store.WorkspaceFork(r.Context(), workspace.ID, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "workspace fork operation not found")
		return
	}
	if err != nil {
		writeError(w, 500, "failed to load workspace fork operation")
		return
	}
	writeJSON(w, http.StatusOK, operation)
}

func (api *ControlAPI) handleWorkspaceForkResume(w http.ResponseWriter, r *http.Request, source, id string) {
	if api.workspaceMgr == nil {
		writeError(w, 503, "workspace manager unavailable")
		return
	}
	workspace, err := api.workspaceMgr.ResumeFork(r.Context(), source, id)
	if err != nil {
		if writeWorkspaceStorageFailure(w, err) {
			return
		}
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrWorkspaceNotFound) {
			writeError(w, 404, "workspace fork operation not found")
			return
		}
		writeError(w, 409, "workspace fork recovery is blocked", err)
		return
	}
	writeJSON(w, 200, workspaceToV1(workspace))
}

func writeWorkspaceStorageFailure(w http.ResponseWriter, err error) bool {
	if writeWorkspaceForkFailure(w, err) {
		return true
	}
	var storage *proxmox.StorageOperationError
	if !errors.As(err, &storage) {
		return false
	}
	response := daemonErrorResponse(http.StatusConflict, storage.Error())
	response.Code = "workspace_storage_inspection_required"
	response.State = "blocked"
	response.StorageOperation = &storage.Receipt
	response.Action = "inspect the recorded source volume and snapshot; preserve existing data before retrying"
	retryable := false
	response.Retryable = &retryable
	writeJSON(w, http.StatusConflict, response)
	return true
}
