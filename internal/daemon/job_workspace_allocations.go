package daemon

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

type workspaceAllocationError struct {
	allocation db.JobWorkspaceAllocation
	cause      error
}

func (e *workspaceAllocationError) Error() string {
	return "workspace creation has a retained allocation; inspect it before retrying"
}
func (e *workspaceAllocationError) Unwrap() error { return e.cause }

// CreateForSubmission reserves the workspace identity before allocating storage.
// It never repeats a backend allocation with an unknown outcome or deletes an
// allocated volume merely because publication failed.
func (m *WorkspaceManager) CreateForSubmission(ctx context.Context, submission db.JobSubmission, name, storage string, sizeGB int) (models.Workspace, error) {
	if m == nil || m.store == nil || m.backend == nil {
		return models.Workspace{}, errors.New("workspace manager unavailable")
	}
	name, storage = strings.TrimSpace(name), strings.TrimSpace(storage)
	if name == "" || sizeGB <= 0 {
		return models.Workspace{}, errors.New("valid workspace name and size are required")
	}
	if storage == "" {
		storage = defaultWorkspaceStorage
	}
	release, err := m.store.LockJobSubmission(ctx, "workspace-name", name)
	if err != nil {
		return models.Workspace{}, err
	}
	defer release()
	a, err := m.store.JobWorkspaceAllocation(ctx, submission.JobID)
	if errors.Is(err, sql.ErrNoRows) {
		id, idErr := newWorkspaceID(m.rand)
		if idErr != nil {
			return models.Workspace{}, idErr
		}
		a, err = m.store.ReserveJobWorkspaceAllocation(ctx, db.JobWorkspaceAllocation{JobID: submission.JobID, WorkspaceID: id, Name: name, Storage: storage, SizeGB: sizeGB})
	}
	if err != nil {
		if errors.Is(err, db.ErrWorkspaceAllocationConflict) || isUniqueConstraint(err) {
			return models.Workspace{}, ErrWorkspaceExists
		}
		return models.Workspace{}, err
	}
	if a.Name != name || a.Storage != storage || a.SizeGB != sizeGB {
		return models.Workspace{}, db.ErrSubmissionConflict
	}
	uncertain := func(cause error) (models.Workspace, error) {
		return models.Workspace{}, &workspaceAllocationError{allocation: a, cause: cause}
	}
	if a.State == "committed" && a.OwnerVMID != 0 {
		owner, err := m.workspaceVolumeOwner(ctx, a.WorkspaceID)
		if err != nil {
			return uncertain(err)
		}
		if owner.VMID != a.OwnerVMID || owner.AllocationName != a.AllocationName {
			return uncertain(db.ErrWorkspaceVolumeOwner)
		}
	}
	if a.State == "committed" {
		workspace, err := m.store.GetWorkspace(ctx, a.WorkspaceID)
		if err != nil {
			return uncertain(err)
		}
		if workspace.Name != a.Name || workspace.Storage != a.Storage || workspace.VolumeID != a.VolumeID || workspace.SizeGB != a.SizeGB {
			return uncertain(db.ErrWorkspaceAllocationConflict)
		}
		return workspace, nil
	}
	if a.State == "allocating" {
		return uncertain(db.ErrWorkspaceAllocationUncertain)
	}
	if a.State == "planned" {
		owner, err := m.workspaceVolumeOwner(ctx, a.WorkspaceID)
		if err != nil {
			return uncertain(err)
		}
		a.OwnerVMID, a.AllocationName = owner.VMID, owner.AllocationName
		if err := m.store.BeginJobWorkspaceAllocation(ctx, a.JobID); err != nil {
			return uncertain(err)
		}
		a.State = "allocating"
		volume, allocationErr := m.backend.CreateVolume(ctx, a.Storage, a.AllocationName, a.SizeGB)
		var preflight *proxmox.VolumePreflightError
		if volume == "" && errors.As(allocationErr, &preflight) {
			checkpoint, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			resetErr := m.store.ResetJobWorkspacePreflight(checkpoint, a.JobID)
			cancel()
			if resetErr == nil {
				a.State = "planned"
			}
			return uncertain(errors.Join(allocationErr, resetErr))
		}

		if volume != "" && (!strings.HasPrefix(volume, a.Storage+":") || len(volume) == len(a.Storage)+1) {
			allocationErr = errors.Join(allocationErr, errors.New("backend returned a mismatched volume receipt"))
		}

		// Persist a known receipt even if the requesting client disconnected. A
		// bounded independent checkpoint does not continue provisioning the job.
		if volume != "" {
			a.VolumeID = volume
			checkpoint, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			receiptErr := m.store.RecordJobWorkspaceVolume(checkpoint, a.JobID, volume, allocationErr == nil)
			cancel()
			if receiptErr != nil {
				return uncertain(errors.Join(allocationErr, receiptErr))
			}
			a.VolumeID = volume
			if allocationErr == nil {
				a.State = "allocated"
			}
		}
		if allocationErr != nil || volume == "" {
			return uncertain(errors.Join(allocationErr, db.ErrWorkspaceAllocationUncertain))
		}
	}
	if a.OwnerVMID != 0 {
		owner, err := m.workspaceVolumeOwner(ctx, a.WorkspaceID)
		if err != nil {
			return uncertain(err)
		}
		if owner.VMID != a.OwnerVMID || owner.AllocationName != a.AllocationName {
			return uncertain(db.ErrWorkspaceVolumeOwner)
		}
	}
	workspace, err := m.store.CommitJobWorkspaceAllocation(ctx, a.JobID)
	if err != nil {
		return uncertain(err)
	}
	return workspace, nil
}

func workspaceAllocationFailure(w http.ResponseWriter, err error) bool {
	var allocation *workspaceAllocationError
	if !errors.As(err, &allocation) {
		return false
	}
	response := daemonErrorResponse(http.StatusConflict, allocation.Error())
	response.State = "blocked"
	response.Code = "workspace_allocation_inspection_required"
	response.Action = "inspect the recorded workspace and backend allocation; preserve existing data and reuse the original request key after reconciliation"
	retryable := allocation.allocation.State == "allocated" && !errors.Is(allocation.cause, db.ErrWorkspaceAllocationConflict) && !errors.Is(allocation.cause, db.ErrWorkspaceVolumeOwner)
	response.Retryable = &retryable
	if allocation.allocation.State == "planned" {
		response.Code = "workspace_allocation_preflight_failed"
		response.Action = "repair storage configuration or access, then retry the original request key"
	}
	response.WorkspaceAllocation = &allocation.allocation
	writeJSON(w, http.StatusConflict, response)
	return true
}
