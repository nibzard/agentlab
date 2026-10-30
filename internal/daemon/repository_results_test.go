package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/executionarchive"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/agentlab/agentlab/internal/proxmox"
	"github.com/agentlab/agentlab/internal/reposnapshot"
	testutil "github.com/agentlab/agentlab/internal/testing"
	"github.com/stretchr/testify/require"
)

func repositoryReportFixture(t *testing.T, changed, keepalive bool) (*JobOrchestrator, *proxmox.FakeBackend, models.Job, JobReport) {
	t.Helper()
	ctx := context.Background()
	store := newTestStore(t)
	initial := repositoryFixture(t)
	clone := filepath.Join(t.TempDir(), "task")
	command := exec.Command("git", "clone", "--quiet", "-b", "agentlab-snapshot", initial.BundlePath, clone)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	if changed {
		require.NoError(t, os.WriteFile(filepath.Join(clone, "code.txt"), []byte("task result\n"), 0600))
	}
	exported, err := reposnapshot.Create(ctx, reposnapshot.Options{Repository: clone, OutputDir: t.TempDir(), BaseRevision: initial.Manifest.Revision})
	require.NoError(t, err)
	backend := proxmox.NewFakeBackend()
	backend.AddTemplate(9000)
	require.NoError(t, backend.Clone(ctx, 9000, 1100, "result-owned"))
	require.NoError(t, backend.Start(ctx, 1100))
	now := time.Now().UTC()
	vmid := 1100
	require.NoError(t, store.CreateSandbox(ctx, models.Sandbox{VMID: vmid, Name: "result-owned", Profile: "default", State: models.SandboxRunning, Keepalive: keepalive, LeaseExpires: now.Add(time.Hour), CreatedAt: now, LastUpdatedAt: now}))
	job := models.Job{ID: "result-job", RepoURL: protocol.RepositorySnapshotPrefix + strings.Repeat("a", 64), Ref: initial.Manifest.Revision, Profile: "default", Status: models.JobRunning, SandboxVMID: &vmid, Keepalive: keepalive, CreatedAt: now, UpdatedAt: now}
	require.NoError(t, store.CreateJob(ctx, job))
	_, err = store.SaveRepositorySnapshot(ctx, "fixture", protocol.RepositorySnapshot{ID: strings.Repeat("a", 64), Manifest: initial.Manifest, CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)})
	require.NoError(t, err)
	root := t.TempDir()
	manifest, err := os.ReadFile(exported.ManifestPath)
	require.NoError(t, err)
	manifestHash := sha256.Sum256(manifest)
	prefix := "repository-results/" + hex.EncodeToString(manifestHash[:]) + "/"
	for name, source := range map[string]string{"manifest.json": exported.ManifestPath, "source.bundle": exported.BundlePath, "changes.patch": exported.PatchPath, "execution.tar.gz": ""} {
		data := testutil.ExecutionArchive(t, executionarchive.Expected{JobID: job.ID, SourceID: strings.Repeat("a", 64), SourceRevision: job.Ref, ManifestSHA256: hex.EncodeToString(manifestHash[:]), Status: "COMPLETED"})
		if source != "" {
			data, err = os.ReadFile(source)
			require.NoError(t, err)
		}
		if len(data) == 0 {
			continue
		}
		path := filepath.Join(root, job.ID, filepath.FromSlash(prefix), name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, data, 0600))
		sum := sha256.Sum256(data)
		_, err := store.CreateArtifact(ctx, db.Artifact{JobID: job.ID, VMID: &vmid, Name: name, Path: prefix + name, SizeBytes: int64(len(data)), Sha256: hex.EncodeToString(sum[:]), CreatedAt: now})
		require.NoError(t, err)
	}
	manager := NewSandboxManager(store, backend, nil)
	o := NewJobOrchestrator(store, nil, backend, manager, nil, proxmox.SnippetStore{}, "", "", nil, nil, nil).WithArtifactRoot(root)
	result, _ := json.Marshal(map[string]any{"schema_version": 1, "job_id": job.ID, "source_id": strings.Repeat("a", 64), "source_revision": job.Ref, "status": "COMPLETED", "duration_seconds": 1, "exit_code": 0, "repository_result": map[string]string{"state": "verified", "manifest_sha256": hex.EncodeToString(manifestHash[:])}})
	return o, backend, job, JobReport{JobID: job.ID, VMID: vmid, Status: models.JobCompleted, Result: result}
}

func repositoryReceipt(t *testing.T, o *JobOrchestrator, id string) (models.Job, protocol.RepositoryResult) {
	t.Helper()
	job, err := o.store.GetJob(context.Background(), id)
	require.NoError(t, err)
	var body struct {
		Result struct {
			RepositoryResult protocol.RepositoryResult `json:"repository_result"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(job.ResultJSON), &body))
	return job, body.Result.RepositoryResult
}

func TestRepositoryResultVerificationAndResourceOutcomes(t *testing.T) {
	for _, keepalive := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleanup", true: "retained"}[keepalive], func(t *testing.T) {
			o, backend, job, report := repositoryReportFixture(t, true, keepalive)
			require.NoError(t, o.HandleReport(context.Background(), report))
			final, receipt := repositoryReceipt(t, o, job.ID)
			require.Equal(t, models.JobCompleted, final.Status)
			require.Equal(t, "verified", receipt.State)
			require.NotNil(t, receipt.Execution)
			require.Equal(t, "guest_reported", receipt.Execution.State)
			require.Equal(t, "not_reported", receipt.Execution.Tests)
			if keepalive {
				require.Equal(t, "retained", receipt.CleanupStatus)
				require.NotEmpty(t, receipt.Resources[0].ExpiresAt)
			} else {
				require.Equal(t, "verified", receipt.CleanupStatus)
				require.Equal(t, "removed", receipt.Resources[0].State)
			}
			require.ErrorIs(t, o.HandleReport(context.Background(), report), ErrJobFinalized)
			inventory, err := backend.ListVMs(context.Background())
			require.NoError(t, err)
			present := false
			for _, vm := range inventory {
				if int(vm.VMID) == report.VMID {
					present = true
				}
			}
			require.Equal(t, keepalive, present)
		})
	}
}

func TestUnverifiedRepositoryResultPreservesSandbox(t *testing.T) {
	o, backend, job, report := repositoryReportFixture(t, true, false)
	artifacts, err := o.store.ListArtifactsByJob(context.Background(), job.ID)
	require.NoError(t, err)
	for _, artifact := range artifacts {
		if artifact.Name == "changes.patch" {
			require.NoError(t, os.WriteFile(filepath.Join(o.artifactRoot, job.ID, artifact.Path), []byte("changed"), 0600))
		}
	}
	require.NoError(t, o.HandleReport(context.Background(), report))
	final, receipt := repositoryReceipt(t, o, job.ID)
	require.Equal(t, models.JobFailed, final.Status)
	require.Equal(t, "blocked", receipt.State)
	require.Equal(t, "blocked", receipt.CleanupStatus)
	require.Equal(t, "retained", receipt.Resources[0].State)
	status, err := backend.Status(context.Background(), proxmox.VMID(report.VMID))
	require.NoError(t, err)
	require.Equal(t, proxmox.StatusRunning, status)
}

func TestRepositoryResultPreservesChangedBackendIdentity(t *testing.T) {
	o, backend, job, report := repositoryReportFixture(t, true, false)
	require.NoError(t, backend.Configure(context.Background(), proxmox.VMID(report.VMID), proxmox.VMConfig{Name: "different-work"}))
	require.NoError(t, o.HandleReport(context.Background(), report))
	_, receipt := repositoryReceipt(t, o, job.ID)
	require.Equal(t, "verified", receipt.State)
	require.Equal(t, "blocked", receipt.CleanupStatus)
	require.Equal(t, "identity_changed", receipt.Resources[0].State)
}

func TestRepositoryResultWithNoChanges(t *testing.T) {
	o, _, job, report := repositoryReportFixture(t, false, false)
	require.NoError(t, o.HandleReport(context.Background(), report))
	_, receipt := repositoryReceipt(t, o, job.ID)
	require.Equal(t, "verified", receipt.State)
	require.EqualValues(t, 0, receipt.PatchBytes)
	require.Empty(t, receipt.PatchPath)
}

func TestRepositoryResultArtifactUploadIsImmutable(t *testing.T) {
	o, _, job, _ := repositoryReportFixture(t, true, false)
	hash, err := db.HashArtifactToken("result-upload-token")
	require.NoError(t, err)
	require.NoError(t, o.store.CreateArtifactToken(context.Background(), hash, job.ID, *job.SandboxVMID, time.Now().Add(time.Hour)))
	api := NewArtifactAPI(o.store, o.artifactRoot, 256<<20, nil, nil)
	path := "repository-results/" + strings.Repeat("b", 64) + "/changes.patch"
	upload := func(data string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/upload?path="+path, bytes.NewBufferString(data))
		r.Header.Set("Authorization", "Bearer result-upload-token")
		w := httptest.NewRecorder()
		api.handleUpload(w, r)
		return w
	}
	first := upload("original result")
	require.Equal(t, 201, first.Code, first.Body.String())
	replay := upload("original result")
	require.Equal(t, 200, replay.Code, replay.Body.String())
	require.JSONEq(t, first.Body.String(), replay.Body.String())
	require.Equal(t, 409, upload("changed result").Code)
	content, err := os.ReadFile(filepath.Join(o.artifactRoot, job.ID, path))
	require.NoError(t, err)
	require.Equal(t, "original result", string(content))
}

type resultBlockingBackend struct {
	proxmox.Backend
	started chan struct{}
	release chan struct{}
}

func (b *resultBlockingBackend) Destroy(ctx context.Context, id proxmox.VMID) error {
	close(b.started)
	select {
	case <-b.release:
		return b.Backend.Destroy(ctx, id)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestCancellationDoesNotInterruptSavedResultFinalization(t *testing.T) {
	o, backend, job, report := repositoryReportFixture(t, true, false)
	blocking := &resultBlockingBackend{Backend: backend, started: make(chan struct{}), release: make(chan struct{})}
	o.backend = blocking
	o.sandboxManager.backend = blocking
	done := make(chan error, 1)
	go func() { done <- o.HandleReport(context.Background(), report) }()
	select {
	case <-blocking.started:
	case err := <-done:
		t.Fatalf("report ended before cleanup: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("report did not reach cleanup")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	api := NewControlAPI(o.store, nil, o.sandboxManager, nil, o, o.artifactRoot, nil)
	r := httptest.NewRequest(http.MethodPost, "/v1/jobs/"+job.ID+"/cancel", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	api.handleJobCancellation(w, r, job.ID)
	close(blocking.release)
	require.Equal(t, 409, w.Code, w.Body.String())
	require.NoError(t, <-done)
	_, receipt := repositoryReceipt(t, o, job.ID)
	require.Equal(t, "verified", receipt.State)
	require.Equal(t, "verified", receipt.CleanupStatus)
}

func TestRepositoryResultRequiresOriginalSourceEvidence(t *testing.T) {
	o, backend, job, report := repositoryReportFixture(t, true, false)
	source, _, err := o.store.GetRepositorySnapshot(context.Background(), strings.TrimPrefix(job.RepoURL, protocol.RepositorySnapshotPrefix))
	require.NoError(t, err)
	source.Manifest.Files[0].SHA256 = strings.Repeat("0", 64)
	data, err := json.Marshal(source.Manifest)
	require.NoError(t, err)
	_, err = o.store.DB.Exec(`UPDATE repository_snapshots SET manifest_json=? WHERE id=?`, string(data), source.ID)
	require.NoError(t, err)
	require.NoError(t, o.HandleReport(context.Background(), report))
	final, receipt := repositoryReceipt(t, o, job.ID)
	require.Equal(t, models.JobFailed, final.Status)
	require.Equal(t, "repository_result_base_mismatch", receipt.ErrorCode)
	require.Equal(t, "retained", receipt.Resources[0].State)
	status, err := backend.Status(context.Background(), proxmox.VMID(report.VMID))
	require.NoError(t, err)
	require.Equal(t, proxmox.StatusRunning, status)
}

func TestResultCleanupSurvivesReportingGuestDisconnect(t *testing.T) {
	o, backend, job, report := repositoryReportFixture(t, true, false)
	blocking := &resultBlockingBackend{Backend: backend, started: make(chan struct{}), release: make(chan struct{})}
	o.backend = blocking
	o.sandboxManager.backend = blocking
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- o.HandleReport(ctx, report) }()
	select {
	case <-blocking.started:
	case err := <-done:
		t.Fatalf("report ended before cleanup: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("report did not reach cleanup")
	}
	cancel()
	close(blocking.release)
	require.NoError(t, <-done)
	_, receipt := repositoryReceipt(t, o, job.ID)
	require.Equal(t, "verified", receipt.State)
	require.Equal(t, "verified", receipt.CleanupStatus)
	require.Equal(t, "removed", receipt.Resources[0].State)
}

func TestInvalidExecutionEvidenceRetainsSandbox(t *testing.T) {
	for _, kind := range []string{"not_an_archive", "another_job", "terminal_report_mismatch"} {
		t.Run(kind, func(t *testing.T) {
			o, backend, job, report := repositoryReportFixture(t, true, false)
			if kind == "terminal_report_mismatch" {
				var body map[string]any
				require.NoError(t, json.Unmarshal(report.Result, &body))
				body["duration_seconds"] = 2
				report.Result, _ = json.Marshal(body)
			} else {
				artifacts, err := o.store.ListArtifactsByJob(context.Background(), job.ID)
				require.NoError(t, err)
				for _, artifact := range artifacts {
					if artifact.Name != "execution.tar.gz" {
						continue
					}
					data := []byte("not an archive, with a valid receipt hash")
					if kind == "another_job" {
						manifestHash := strings.Split(artifact.Path, "/")[1]
						data = testutil.ExecutionArchive(t, executionarchive.Expected{JobID: "different-job", SourceID: strings.Repeat("a", 64), SourceRevision: job.Ref, ManifestSHA256: manifestHash, Status: "COMPLETED"})
					}
					require.NoError(t, os.WriteFile(filepath.Join(o.artifactRoot, job.ID, artifact.Path), data, 0600))
					digest := sha256.Sum256(data)
					_, err := o.store.DB.Exec(`UPDATE artifacts SET sha256=?, size_bytes=? WHERE id=?`, hex.EncodeToString(digest[:]), len(data), artifact.ID)
					require.NoError(t, err)
				}
			}
			require.NoError(t, o.HandleReport(context.Background(), report))
			final, receipt := repositoryReceipt(t, o, job.ID)
			require.Equal(t, models.JobFailed, final.Status)
			require.Equal(t, "blocked", receipt.State)
			require.Nil(t, receipt.Execution)
			if kind == "terminal_report_mismatch" {
				require.Equal(t, "execution_report_mismatch", receipt.ErrorCode)
			} else {
				require.Equal(t, "execution_archive_invalid", receipt.ErrorCode)
			}
			require.Equal(t, "retained", receipt.Resources[0].State)
			status, err := backend.Status(context.Background(), proxmox.VMID(report.VMID))
			require.NoError(t, err)
			require.Equal(t, proxmox.StatusRunning, status)
		})
	}
}
