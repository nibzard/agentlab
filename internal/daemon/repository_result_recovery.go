package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/executionarchive"
	"github.com/agentlab/agentlab/internal/hostidentity"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/agentlab/agentlab/internal/proxmox"
)

var vmGenerationPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$`)

func (o *JobOrchestrator) repositoryResultTarget(ctx context.Context, job models.Job) *protocol.RepositoryResultTarget {
	if job.SandboxVMID == nil || job.WorkspaceID != nil || job.SessionID != nil || o.backend == nil {
		return nil
	}
	identity, err := hostidentity.Local()
	if err != nil {
		return nil
	}
	sandbox, err := o.store.GetSandbox(ctx, *job.SandboxVMID)
	if err != nil || sandbox.CreatedAt.IsZero() || sandbox.Name == "" || sandbox.WorkspaceID != nil || (sandbox.Type != "" && sandbox.Type != models.SandboxTypeVM) {
		return nil
	}
	config, err := o.backend.VMConfig(ctx, proxmox.VMID(sandbox.VMID))
	if err != nil || config["name"] != sandbox.Name || !vmGenerationPattern.MatchString(config["vmgenid"]) {
		return nil
	}
	incarnation, err := o.store.GetSandboxIncarnation(ctx, sandbox.VMID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return &protocol.RepositoryResultTarget{ServerIdentity: identity, VMID: sandbox.VMID, Name: sandbox.Name, CreatedAt: sandbox.CreatedAt.UTC().Format(time.RFC3339Nano), GenerationID: strings.ToLower(config["vmgenid"]), Incarnation: incarnation}
}

func (o *JobOrchestrator) repositoryTargetMatches(ctx context.Context, job models.Job, sandbox models.Sandbox, target *protocol.RepositoryResultTarget, backendPresent bool) bool {
	if target == nil || job.SandboxVMID == nil || *job.SandboxVMID != target.VMID || target.VMID != sandbox.VMID || sandbox.Name != target.Name || sandbox.CreatedAt.UTC().Format(time.RFC3339Nano) != target.CreatedAt || job.WorkspaceID != nil || job.SessionID != nil || sandbox.WorkspaceID != nil || (sandbox.Type != "" && sandbox.Type != models.SandboxTypeVM) {
		return false
	}
	identity, err := hostidentity.Local()
	if err != nil || identity != target.ServerIdentity || !vmGenerationPattern.MatchString(target.GenerationID) {
		return false
	}
	if !backendPresent {
		return true
	}
	if o.backend == nil || sandbox.State == models.SandboxDestroyed {
		return false
	}
	config, err := o.backend.VMConfig(ctx, proxmox.VMID(target.VMID))
	if err != nil || config["name"] != target.Name || strings.ToLower(config["vmgenid"]) != target.GenerationID {
		return false
	}
	if target.Incarnation != "" {
		incarnation, err := o.store.GetSandboxIncarnation(ctx, target.VMID)
		if err != nil || incarnation != target.Incarnation {
			return false
		}
	}
	return true
}

// RecoverRepositoryResults resumes durable pending cleanup in tracked work.
// The saved job result is the journal; no second job status is introduced.
func (o *JobOrchestrator) RecoverRepositoryResults(ctx context.Context) error {
	rows, err := o.store.DB.QueryContext(ctx, `SELECT id FROM jobs WHERE repo_url LIKE ? AND status IN ('COMPLETED','FAILED','TIMEOUT') AND CASE WHEN json_valid(result_json) THEN json_extract(result_json,'$.result.repository_result.cleanup_status')='pending' ELSE 0 END ORDER BY created_at,id`, db.SnapshotRepositoryPrefix+"%")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(ids) == 0 {
		return err
	}
	runner := o.runner
	if runner == nil {
		runner = DetachedRunner()
	}
	if !runner.Go("recover-repository-results", func(ctx context.Context) {
		for _, id := range ids {
			if ctx.Err() != nil {
				return
			}
			duration := o.failureTimeout
			if duration <= 0 {
				duration = defaultFailureTimeout
			}
			bounded, cancel := context.WithTimeout(ctx, duration)
			err := o.reconcileRepositoryResult(bounded, id)
			cancel()
			if err != nil && o.logger != nil {
				o.logger.Printf("job %s result recovery needs inspection", id)
			}
		}
	}) {
		return fmt.Errorf("daemon is stopping; result recovery remains pending")
	}
	return nil
}

func (o *JobOrchestrator) reconcileRepositoryResult(ctx context.Context, id string) error {
	releaseReport, err := o.store.LockJobSubmission(ctx, "job-report", id)
	if err != nil {
		return err
	}
	defer releaseReport()
	releaseExecution, err := o.store.LockJobSubmission(ctx, "job-execution", id)
	if err != nil {
		return err
	}
	defer releaseExecution()
	job, err := o.store.GetJob(ctx, id)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(job.RepoURL, db.SnapshotRepositoryPrefix) || (job.Status != models.JobCompleted && job.Status != models.JobFailed && job.Status != models.JobTimeout) {
		return nil
	}
	if o.jobCancellationExists(ctx, id) {
		return ErrJobFinalized
	}
	var saved struct {
		Status    models.JobStatus     `json:"status"`
		Message   string               `json:"message"`
		Artifacts []V1ArtifactMetadata `json:"artifacts"`
		Result    json.RawMessage      `json:"result"`
	}
	if err := json.Unmarshal([]byte(job.ResultJSON), &saved); err != nil {
		return err
	}
	var body struct {
		Receipt protocol.RepositoryResult `json:"repository_result"`
	}
	if err := json.Unmarshal(saved.Result, &body); err != nil {
		return err
	}
	receipt := &body.Receipt
	if receipt.CleanupStatus != "pending" {
		return nil
	}
	if job.SandboxVMID == nil {
		return fmt.Errorf("pending result has no sandbox binding")
	}
	report := JobReport{JobID: id, VMID: *job.SandboxVMID, Status: job.Status, Message: saved.Message, Artifacts: saved.Artifacts, Result: saved.Result}
	verified, artifacts, verifyErr := o.verifyRepositoryResult(ctx, job, receipt.ManifestSHA256, string(job.Status))
	if verifyErr == nil {
		evidence, err := executionarchive.ValidateReport(saved.Result, executionarchive.Expected{TestCommands: job.TestCommands, JobID: job.ID, SourceID: verified.SourceID, SourceRevision: job.Ref, ManifestSHA256: receipt.ManifestSHA256, Status: string(job.Status)})
		if err != nil || verified.Execution == nil || !executionarchive.Equal(evidence, *verified.Execution) {
			verifyErr = fmt.Errorf("saved execution report conflicts with its archive")
		}
	}
	if verifyErr != nil || receipt.State != "verified" || saved.Status != job.Status || !sameRepositoryEvidence(receipt, verified) {
		receipt.State = "blocked"
		receipt.CleanupStatus = "blocked"
		receipt.ErrorCode = "repository_result_recovery_unverified"
		receipt.Action = "inspect the retained sandbox and result artifacts before cleanup"
		report.Artifacts = nil
		report.Status = models.JobFailed
	} else {
		report.Artifacts = artifacts
	}
	return o.finalizeRepositoryResources(ctx, job, &report, receipt)
}

func (o *JobOrchestrator) startRepositoryResultCleanup(id string) {
	runner := o.runner
	if runner == nil {
		runner = DetachedRunner()
	}
	runner.Go("repository-result-cleanup:"+id, func(ctx context.Context) {
		duration := o.failureTimeout
		if duration <= 0 {
			duration = defaultFailureTimeout
		}
		bounded, cancel := context.WithTimeout(ctx, duration)
		defer cancel()
		if err := o.reconcileRepositoryResult(bounded, id); err != nil && o.logger != nil {
			o.logger.Printf("job %s result cleanup needs inspection", id)
		}
	})
}

func sameRepositoryEvidence(a, b *protocol.RepositoryResult) bool {
	if a == nil || b == nil {
		return false
	}
	left, right := *a, *b
	left.Target = nil
	right.Target = nil
	left.Resources = nil
	right.Resources = nil
	left.VerifiedAt = ""
	right.VerifiedAt = ""
	left.CleanupStatus = ""
	right.CleanupStatus = ""
	left.Action = ""
	right.Action = ""
	left.ErrorCode = ""
	right.ErrorCode = ""
	x, _ := json.Marshal(left)
	y, _ := json.Marshal(right)
	return string(x) == string(y)
}
