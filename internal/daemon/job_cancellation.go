package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/agentlab/agentlab/internal/proxmox"
)

var ErrJobCancellationRequested = errors.New("job cancellation requested")

func (o *JobOrchestrator) watchJobCancellation(ctx context.Context, cancel context.CancelFunc, id string) {
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				requested, err := o.store.JobCancellationRequested(ctx, id)
				if err == nil && requested {
					cancel()
					return
				}
			}
		}
	}()
}

func (o *JobOrchestrator) StartCancellation(id string) {
	if o == nil {
		return
	}
	runner := o.runner
	if runner == nil {
		runner = DetachedRunner()
	}
	runner.Go("cancel-job:"+id, func(ctx context.Context) {
		if err := o.CancelJob(ctx, id); err != nil && o.logger != nil {
			o.logger.Printf("job %s cancellation needs inspection", id)
		}
	})
}

func (o *JobOrchestrator) RecoverCancellations(ctx context.Context) error {
	ids, err := o.store.PendingJobCancellations(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		o.StartCancellation(id)
	}
	return nil
}

// CancelJob shares the execution lock with provisioning. It cannot report that
// work stopped until provisioning has exited and backend state is verified.
func (o *JobOrchestrator) CancelJob(ctx context.Context, id string) error {
	if o == nil || o.store == nil {
		return errors.New("job cancellation unavailable")
	}
	release, err := o.store.LockJobSubmission(ctx, "job-execution", id)
	if err != nil {
		return err
	}
	defer release()
	record, err := o.store.GetJobCancellation(ctx, id)
	if err != nil {
		return err
	}
	if record.State == "cancelled" || record.State == "not_needed" {
		return nil
	}
	blocked := func(code, action string, cause error) error {
		if record.SandboxVMID != nil {
			present := false
			for _, resource := range record.Resources {
				if resource.Kind == "sandbox" && resource.ID == strconv.Itoa(*record.SandboxVMID) {
					present = true
				}
			}
			if !present {
				record.Resources = append(record.Resources, protocol.JobResource{Kind: "sandbox", ID: strconv.Itoa(*record.SandboxVMID), State: "inspection_required"})
			}
		}
		record.State = "blocked"
		record.ErrorCode = code
		record.Action = action
		record.WorkStopped = false
		record.CleanupStatus = "inspection_required"
		if saveErr := o.store.SaveJobCancellation(ctx, record); saveErr != nil {
			return errors.Join(cause, saveErr)
		}
		return cause
	}
	record.State = "running"
	record.ErrorCode = ""
	record.Action = ""
	if err := o.store.SaveJobCancellation(ctx, record); err != nil {
		return err
	}
	job, err := o.store.GetJob(ctx, id)
	if err != nil {
		return blocked("job_unavailable", "inspect the job record before retrying cancellation", err)
	}
	if record.WorkspaceID != nil && (job.WorkspaceID == nil || *record.WorkspaceID != *job.WorkspaceID) {
		return blocked("cancellation_target_changed", "inspect the original and current workspace bindings", errors.New("job workspace binding changed"))
	}
	if job.SessionID != nil {
		return blocked("session_cancellation_unsupported", "inspect the session; cancellation must preserve shared interactive work", errors.New("shared session job cancellation is unavailable"))
	}
	record.Resources = []protocol.JobResource{}
	if job.WorkspaceID != nil {
		record.Resources = append(record.Resources, protocol.JobResource{Kind: "workspace", ID: *job.WorkspaceID, State: "retained"})
	}
	record.CleanupStatus = "verified"
	if job.SandboxVMID != nil {
		vmid := *job.SandboxVMID
		if record.SandboxVMID != nil && *record.SandboxVMID != vmid {
			return blocked("cancellation_target_changed", "inspect the original and current sandbox bindings", errors.New("job sandbox binding changed"))
		}
		record.SandboxVMID = &vmid
		authority := auth.Token{Claims: auth.TokenClaims{Scope: record.Scope}}
		if !authority.IsSandboxAllowed(vmid) {
			return blocked("authorization_required", "request cancellation with authority for the job's current sandbox", errors.New("sandbox is outside the saved cancellation scope"))
		}
		if o.backend == nil || o.sandboxManager == nil {
			return blocked("backend_unavailable", "restore the sandbox backend and retry cancellation", errors.New("sandbox backend unavailable"))
		}
		var others int
		if err := o.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE sandbox_vmid=? AND id!=? AND status IN ('QUEUED','RUNNING')`, vmid, id).Scan(&others); err != nil {
			return blocked("job_store_unavailable", "restore access to job state before retrying", err)
		}
		if others > 0 {
			return blocked("shared_sandbox_active", "inspect other active jobs before stopping this sandbox", errors.New("another active job uses the sandbox"))
		}
		sandbox, err := o.store.GetSandbox(ctx, vmid)
		if err != nil {
			return blocked("sandbox_ownership_unverified", "inspect the recorded sandbox before retrying cancellation", err)
		}
		if sandbox.Type != "" && sandbox.Type != models.SandboxTypeVM {
			return blocked("unsupported_sandbox_type", "use the supported container controls and inspect this job before cancellation", errors.New("job cancellation requires a VM sandbox"))
		}
		inventory, err := o.backend.ListVMs(ctx)
		if err != nil {
			return blocked("backend_unavailable", "restore backend access and retry cancellation", err)
		}
		present := false
		for _, vm := range inventory {
			if int(vm.VMID) == vmid {
				present = true
				if vm.Name != sandbox.Name {
					return blocked("sandbox_identity_changed", "preserve the current VM and inspect the job's original sandbox", errors.New("sandbox name no longer matches backend identity"))
				}
			}
		}
		resource := protocol.JobResource{Kind: "sandbox", ID: strconv.Itoa(vmid)}
		if !sandbox.LeaseExpires.IsZero() {
			resource.ExpiresAt = sandbox.LeaseExpires.UTC().Format(time.RFC3339Nano)
		}
		if !present {
			// A missing backend VM is already stopped. Reconcile the managed row only.
			if sandbox.State != models.SandboxDestroyed {
				if err := o.sandboxManager.Destroy(ctx, vmid); err != nil {
					return blocked("cleanup_failed", "inspect the missing VM's managed resources and retry cleanup", err)
				}
			}
			resource.State = "destroyed"
		} else if job.Keepalive {
			if err := o.backend.Stop(ctx, proxmox.VMID(vmid)); err != nil {
				return blocked("stop_failed", "inspect the retained sandbox and retry cancellation", err)
			}
			status, err := o.backend.Status(ctx, proxmox.VMID(vmid))
			if err != nil || status != proxmox.StatusStopped {
				return blocked("stop_unverified", "verify the sandbox stopped before reporting cancellation", fmt.Errorf("backend stop is not verified: %v", err))
			}
			if sandbox.State != models.SandboxStopped {
				changed, err := o.store.UpdateSandboxState(ctx, vmid, sandbox.State, models.SandboxStopped)
				if err != nil || !changed {
					return blocked("sandbox_state_changed", "inspect the retained sandbox state before retrying", errors.New("sandbox state changed during cancellation"))
				}
			}
			resource.State = "retained_stopped"
			record.CleanupStatus = "retained"
		} else {
			if err := o.sandboxManager.Destroy(ctx, vmid); err != nil {
				return blocked("cleanup_failed", "inspect the temporary sandbox and retry cancellation", err)
			}
			after, err := o.backend.ListVMs(ctx)
			if err != nil {
				return blocked("cleanup_unverified", "verify temporary sandbox removal before reporting cancellation", err)
			}
			for _, vm := range after {
				if int(vm.VMID) == vmid {
					return blocked("cleanup_unverified", "the backend still reports the temporary sandbox; inspect and retry", errors.New("temporary sandbox remains"))
				}
			}
			resource.State = "destroyed"
		}
		if resource.State == "destroyed" && o.workspaceMgr != nil {
			if err := o.workspaceMgr.DetachFromVM(ctx, vmid); err != nil {
				return blocked("workspace_detach_failed", "inspect the retained workspace attachment and retry cancellation", err)
			}
		}
		record.Resources = append(record.Resources, resource)
	}
	if job.WorkspaceID != nil {
		o.releaseWorkspaceLease(ctx, *job.WorkspaceID, workspaceLeaseOwnerForJob(job.ID), job.ID, 0)
	}
	if job.WorkspaceID != nil {
		current, err := o.store.GetWorkspace(ctx, *job.WorkspaceID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return blocked("workspace_cleanup_unverified", "inspect the retained workspace lease before retrying", err)
		}
		if err == nil && current.LeaseOwner == workspaceLeaseOwnerForJob(job.ID) && current.LeaseExpires.After(time.Now()) {
			record.CleanupStatus = "retained"
			for i := range record.Resources {
				if record.Resources[i].Kind == "workspace" {
					record.Resources[i].State = "retained_lease_active"
					record.Resources[i].ExpiresAt = current.LeaseExpires.UTC().Format(time.RFC3339Nano)
				}
			}
		}
	}

	record.State = "cancelled"
	record.WorkStopped = true
	if err := o.store.CompleteJobCancellation(ctx, record); err != nil {
		return blocked("cancellation_commit_failed", "inspect resource outcomes and retry cancellation", err)
	}
	_ = emitEvent(ctx, NewStoreEventRecorder(o.store), EventKindJobFailed, record.SandboxVMID, &job.ID, "job cancelled", map[string]any{"status": "FAILED", "cancelled": true, "actor": record.Actor})
	return nil
}

func (o *JobOrchestrator) jobCancellationExists(ctx context.Context, id string) bool {
	requested, err := o.store.JobCancellationRequested(ctx, id)
	return err == nil && requested
}
