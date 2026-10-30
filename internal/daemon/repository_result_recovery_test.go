package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
	"github.com/stretchr/testify/require"
)

func pendingRepositoryResult(t *testing.T, keepalive bool) (*JobOrchestrator, *proxmox.FakeBackend, models.Job) {
	t.Helper()
	o, backend, job, report := repositoryReportFixture(t, true, keepalive)
	receipt := o.prepareRepositoryResult(context.Background(), job, &report)
	require.Equal(t, "verified", receipt.State)
	require.NotNil(t, receipt.Target)
	require.Equal(t, "pending", receipt.CleanupStatus)
	require.NoError(t, o.sandboxManager.Transition(context.Background(), report.VMID, models.SandboxCompleted))
	saved, err := buildJobResult(report.Status, report.Message, report.Artifacts, report.Result, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, o.store.UpdateJobResult(context.Background(), job.ID, report.Status, saved))
	job, err = o.store.GetJob(context.Background(), job.ID)
	require.NoError(t, err)
	return o, backend, job
}

func restartedResultOrchestrator(o *JobOrchestrator) *JobOrchestrator {
	return NewJobOrchestrator(o.store, nil, o.backend, o.sandboxManager, nil, proxmox.SnippetStore{}, "", "", nil, nil, nil).WithArtifactRoot(o.artifactRoot)
}

func TestRepositoryResultRecoveryCompletesSavedCleanup(t *testing.T) {
	for _, keepalive := range []bool{false, true} {
		t.Run(map[bool]string{false: "remove", true: "keepalive"}[keepalive], func(t *testing.T) {
			o, backend, job := pendingRepositoryResult(t, keepalive)
			restarted := restartedResultOrchestrator(o)
			runner := &cancellationRecoveryRunner{ctx: context.Background(), done: make(chan struct{})}
			restarted.WithBackgroundRunner(runner)
			require.NoError(t, restarted.RecoverRepositoryResults(context.Background()))
			select {
			case <-runner.done:
			case <-time.After(5 * time.Second):
				t.Fatal("result recovery did not finish")
			}
			final, receipt := repositoryReceipt(t, restarted, job.ID)
			require.Equal(t, models.JobCompleted, final.Status)
			require.Equal(t, "verified", receipt.State)
			if keepalive {
				require.Equal(t, "retained", receipt.CleanupStatus)
				require.NotEmpty(t, receipt.Resources[0].ExpiresAt)
			} else {
				require.Equal(t, "verified", receipt.CleanupStatus)
				require.Equal(t, "removed", receipt.Resources[0].State)
			}
			before := final.ResultJSON
			require.NoError(t, restarted.reconcileRepositoryResult(context.Background(), job.ID))
			after, err := o.store.GetJob(context.Background(), job.ID)
			require.NoError(t, err)
			require.Equal(t, before, after.ResultJSON, "replay must not rewrite a completed receipt")
			status, err := backend.Status(context.Background(), proxmox.VMID(*job.SandboxVMID))
			if keepalive {
				require.NoError(t, err)
				require.Equal(t, proxmox.StatusRunning, status)
			} else {
				require.ErrorIs(t, err, proxmox.ErrVMNotFound)
			}
		})
	}
}

func TestRepositoryResultRecoveryPreservesChangedTargets(t *testing.T) {
	for _, kind := range []string{"generation", "server", "creation", "workspace", "legacy", "incarnation"} {
		t.Run(kind, func(t *testing.T) {
			o, backend, job := pendingRepositoryResult(t, false)
			_, receipt := repositoryReceipt(t, o, job.ID)
			switch kind {
			case "generation":
				require.NoError(t, backend.Destroy(context.Background(), 1100))
				require.NoError(t, backend.Clone(context.Background(), 9000, 1100, "result-owned"))
				require.NoError(t, backend.Start(context.Background(), 1100))
			case "server":
				receipt.Target.ServerIdentity = "other-server"
			case "creation":
				receipt.Target.CreatedAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
			case "workspace":
				workspace := "preserved-workspace"
				require.NoError(t, o.store.UpdateSandboxWorkspace(context.Background(), 1100, &workspace))
			case "legacy":
				receipt.Target = nil
			case "incarnation":
				_, err := o.store.DB.Exec(`UPDATE guest_identities SET incarnation='different-incarnation' WHERE vmid=1100`)
				require.NoError(t, err)
			}
			if kind == "server" || kind == "creation" || kind == "legacy" {
				var saved map[string]json.RawMessage
				require.NoError(t, json.Unmarshal([]byte(job.ResultJSON), &saved))
				var body map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(saved["result"], &body))
				body["repository_result"], _ = json.Marshal(receipt)
				saved["result"], _ = json.Marshal(body)
				data, _ := json.Marshal(saved)
				require.NoError(t, o.store.UpdateJobResult(context.Background(), job.ID, job.Status, string(data)))
			}
			require.NoError(t, restartedResultOrchestrator(o).reconcileRepositoryResult(context.Background(), job.ID))
			_, receipt = repositoryReceipt(t, o, job.ID)
			require.Equal(t, "blocked", receipt.CleanupStatus)
			require.Equal(t, "repository_cleanup_identity_unverified", receipt.ErrorCode)
			status, err := backend.Status(context.Background(), 1100)
			require.NoError(t, err)
			require.Equal(t, proxmox.StatusRunning, status)
		})
	}
}

func TestRepositoryResultRecoveryRechecksArtifactBytes(t *testing.T) {
	o, backend, job := pendingRepositoryResult(t, false)
	_, receipt := repositoryReceipt(t, o, job.ID)
	require.NoError(t, os.WriteFile(filepath.Join(o.artifactRoot, job.ID, receipt.PatchPath), []byte("changed after daemon shutdown"), 0600))
	require.NoError(t, restartedResultOrchestrator(o).reconcileRepositoryResult(context.Background(), job.ID))
	final, receipt := repositoryReceipt(t, o, job.ID)
	require.Equal(t, models.JobFailed, final.Status)
	require.Equal(t, "blocked", receipt.State)
	require.Equal(t, "repository_result_recovery_unverified", receipt.ErrorCode)
	require.Equal(t, "retained", receipt.Resources[0].State)
	status, err := backend.Status(context.Background(), 1100)
	require.NoError(t, err)
	require.Equal(t, proxmox.StatusRunning, status)
}

type interruptedResultBackend struct {
	proxmox.Backend
	cancel context.CancelFunc
	once   sync.Once
}

func (b *interruptedResultBackend) Destroy(ctx context.Context, id proxmox.VMID) error {
	if err := b.Backend.Destroy(ctx, id); err != nil {
		return err
	}
	b.once.Do(b.cancel)
	return nil
}

func TestRepositoryResultRecoveryAfterDestructionBeforeReceiptSave(t *testing.T) {
	o, backend, job, report := repositoryReportFixture(t, true, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	interrupt := &interruptedResultBackend{Backend: backend, cancel: cancel}
	o.backend = interrupt
	o.sandboxManager.backend = interrupt
	o.WithBackgroundRunner(&cancellationRecoveryRunner{ctx: ctx, done: make(chan struct{})})
	require.Error(t, o.HandleReport(context.Background(), report))
	_, receipt := repositoryReceipt(t, o, job.ID)
	require.Equal(t, "pending", receipt.CleanupStatus, "the first durable receipt must survive interrupted finalization")
	_, err := backend.Status(context.Background(), 1100)
	require.ErrorIs(t, err, proxmox.ErrVMNotFound)
	restarted := restartedResultOrchestrator(o)
	restarted.backend = backend
	restarted.sandboxManager.backend = backend
	require.NoError(t, restarted.reconcileRepositoryResult(context.Background(), job.ID))
	_, receipt = repositoryReceipt(t, o, job.ID)
	require.Equal(t, "verified", receipt.CleanupStatus)
	require.Equal(t, "removed", receipt.Resources[0].State)
	sandbox, err := o.store.GetSandbox(context.Background(), 1100)
	require.NoError(t, err)
	require.Equal(t, models.SandboxDestroyed, sandbox.State)
}

func TestPendingResultProtectsOriginalSource(t *testing.T) {
	o, _, job := pendingRepositoryResult(t, false)
	_, receipt := repositoryReceipt(t, o, job.ID)
	active, err := o.store.RepositorySnapshotActive(context.Background(), receipt.SourceID)
	require.NoError(t, err)
	require.True(t, active)
	require.NoError(t, o.reconcileRepositoryResult(context.Background(), job.ID))
	active, err = o.store.RepositorySnapshotActive(context.Background(), receipt.SourceID)
	require.NoError(t, err)
	require.False(t, active)
}

func TestRepositoryResultRecoveryHonorsNewKeepalive(t *testing.T) {
	o, backend, job := pendingRepositoryResult(t, false)
	_, err := o.store.DB.Exec(`UPDATE sandboxes SET keepalive=1 WHERE vmid=1100`)
	require.NoError(t, err)
	require.NoError(t, restartedResultOrchestrator(o).reconcileRepositoryResult(context.Background(), job.ID))
	_, receipt := repositoryReceipt(t, o, job.ID)
	require.Equal(t, "retained", receipt.CleanupStatus)
	status, err := backend.Status(context.Background(), 1100)
	require.NoError(t, err)
	require.Equal(t, proxmox.StatusRunning, status)
}

func TestConcurrentRepositoryResultRecoveryHasOneOutcome(t *testing.T) {
	o, backend, job := pendingRepositoryResult(t, false)
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() {
			results <- restartedResultOrchestrator(o).reconcileRepositoryResult(context.Background(), job.ID)
		}()
	}
	for i := 0; i < 4; i++ {
		require.NoError(t, <-results)
	}
	_, receipt := repositoryReceipt(t, o, job.ID)
	require.Equal(t, "verified", receipt.CleanupStatus)
	_, err := backend.Status(context.Background(), 1100)
	require.ErrorIs(t, err, proxmox.ErrVMNotFound)
}
