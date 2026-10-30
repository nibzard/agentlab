package daemon

import (
	"bytes"
	"context"
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
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/agentlab/agentlab/internal/proxmox"
	"github.com/agentlab/agentlab/internal/secrets"
	"github.com/stretchr/testify/require"
)

func TestRunnerRepositoryRequiresBoundActiveGuest(t *testing.T) {
	control, store, _ := submissionAPI(t)
	control.artifactRoot = t.TempDir()
	fixture := repositoryFixture(t)
	response := uploadRepository(t, control, fixture, nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	var source protocol.RepositorySnapshot
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &source))
	seedSecretTestSandbox(t, store, 7001, "source-guest", "10.77.9.10")
	secret := seedSandboxSecret(t, store, 7001)
	now := time.Now().UTC()
	vmid := 7001
	require.NoError(t, store.CreateJob(context.Background(), models.Job{ID: "local-source", RepoURL: source.RepoURL, Ref: source.Manifest.Revision, Profile: "default", Status: models.JobRunning, SandboxVMID: &vmid, CreatedAt: now, UpdatedAt: now}))
	runner := NewRunnerAPI(nil, store, mustParseCIDR(t, "10.77.0.0/16")).WithRepositoryRoot(control.artifactRoot)
	for _, item := range []struct {
		name, addr, secret, job, vmid string
		want                          int
	}{
		{"bound", "10.77.9.10:123", secret, "local-source", "7001", 200},
		{"outside subnet", "192.168.1.2:123", secret, "local-source", "7001", 403},
		{"missing secret", "10.77.9.10:123", "", "local-source", "7001", 403},
		{"wrong secret", "10.77.9.10:123", "invalid", "local-source", "7001", 403},
		{"different job", "10.77.9.10:123", secret, "other-job", "7001", 403},
		{"different sandbox", "10.77.9.10:123", secret, "local-source", "7002", 403},
	} {
		t.Run(item.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/v1/runner/repository?vmid="+item.vmid+"&job_id="+item.job, nil)
			r.RemoteAddr = item.addr
			r.Header.Set(sandboxSecretHeader, item.secret)
			w := httptest.NewRecorder()
			runner.handleRunnerRepository(w, r)
			require.Equal(t, item.want, w.Code, w.Body.String())
			if item.want == 200 {
				data, err := os.ReadFile(fixture.BundlePath)
				require.NoError(t, err)
				require.True(t, bytes.Equal(data, w.Body.Bytes()))
			}
		})
	}
	require.NoError(t, store.UpdateJobResult(context.Background(), "local-source", models.JobCompleted, `{}`))
	r := httptest.NewRequest(http.MethodGet, "/v1/runner/repository?vmid=7001&job_id=local-source", nil)
	r.RemoteAddr = "10.77.9.10:123"
	r.Header.Set(sandboxSecretHeader, secret)
	w := httptest.NewRecorder()
	runner.handleRunnerRepository(w, r)
	require.Equal(t, 403, w.Code)
}

func TestGuestRunnerUsesSnapshotWithoutChangingExistingRepository(t *testing.T) {
	runGuestSnapshot(t, false)
}

func TestGuestRunnerRejectsCredentialArtifactsBeforeUpload(t *testing.T) {
	runGuestSnapshot(t, true)
}

func runGuestSnapshot(t *testing.T, unsafeArtifact bool) {
	runGuestSnapshotWithTestCommands(t, unsafeArtifact, nil, models.JobCompleted)
}

func TestGuestSnapshotCollectsExplicitTestEvidence(t *testing.T) {
	t.Run("passed", func(t *testing.T) {
		runGuestSnapshotWithTestCommands(t, false, []string{"grep -q 'agent change' code.txt"}, models.JobCompleted)
	})
	t.Run("failed", func(t *testing.T) {
		runGuestSnapshotWithTestCommands(t, false, []string{"grep -q 'agent change' code.txt", "exit 7"}, models.JobFailed)
	})
}

func runGuestSnapshotWithTestCommands(t *testing.T, unsafeArtifact bool, testCommands []string, wantStatus models.JobStatus) {
	t.Helper()
	for _, command := range []string{"bash", "git", "jq", "curl", "sha256sum"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skip(command + " is required")
		}
	}
	control, store, _ := submissionAPI(t)
	control.artifactRoot = t.TempDir()
	response := uploadRepository(t, control, repositoryFixture(t), nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	var source protocol.RepositorySnapshot
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &source))
	seedSecretTestSandbox(t, store, 7001, "snapshot-runner", "10.77.9.10")
	secret := seedSandboxSecret(t, store, 7001)
	vmid := 7001
	now := time.Now().UTC()
	require.NoError(t, store.CreateJob(context.Background(), models.Job{ID: "snapshot-task", TestCommands: testCommands, RepoURL: source.RepoURL, Ref: source.Manifest.Revision, Profile: "default", Status: models.JobRunning, SandboxVMID: &vmid, CreatedAt: now, UpdatedAt: now}))
	backend := proxmox.NewFakeBackend()
	backend.AddTemplate(9000)
	require.NoError(t, backend.Clone(context.Background(), 9000, 7001, "snapshot-runner"))
	require.NoError(t, backend.Start(context.Background(), 7001))
	manager := NewSandboxManager(store, backend, nil)
	orchestrator := NewJobOrchestrator(store, nil, backend, manager, nil, proxmox.SnippetStore{}, "", "", nil, nil, nil).WithArtifactRoot(control.artifactRoot)
	runner := NewRunnerAPI(orchestrator, store, nil).WithRepositoryRoot(control.artifactRoot)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/runner/repository", runner.handleRunnerRepository)
	mux.HandleFunc("/v1/runner/report", runner.handleRunnerReport)
	NewArtifactAPI(store, control.artifactRoot, 256<<20, nil, nil).Register(mux)
	mux.HandleFunc("/v1/sandbox/bootstrap-status", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	server := httptest.NewServer(mux)
	defer server.Close()
	root := t.TempDir()
	secrets := filepath.Join(root, "secrets")
	run := filepath.Join(root, "run")
	work := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(secrets, 0700))
	require.NoError(t, os.MkdirAll(work, 0700))
	bootstrapPath := filepath.Join(root, "bootstrap.json")
	data, _ := json.Marshal(map[string]any{"token": "fixture-bootstrap", "controller": server.URL, "vmid": 7001})
	require.NoError(t, os.WriteFile(bootstrapPath, data, 0600))
	job := bootstrapJobFromModel(models.Job{ID: "snapshot-task", TestCommands: testCommands, RepoURL: source.RepoURL, Ref: source.Manifest.Revision, Profile: "default", Task: "modify local content"})
	job.RepositorySnapshot = &protocol.RepositorySource{ID: source.ID, SourceRevision: source.Manifest.SourceRevision, Revision: source.Manifest.Revision, BundleSHA256: source.Manifest.BundleSHA256, BundleBytes: source.Manifest.BundleBytes}
	artifactToken := "fixture-artifact-credential"
	hash, err := db.HashArtifactToken(artifactToken)
	require.NoError(t, err)
	require.NoError(t, store.CreateArtifactToken(context.Background(), hash, "snapshot-task", 7001, now.Add(time.Hour)))
	data, _ = json.Marshal(map[string]any{"job": job, "sandbox_secret": secret, "artifact": map[string]string{"endpoint": server.URL + "/upload", "token": artifactToken}})
	require.NoError(t, os.WriteFile(filepath.Join(secrets, "bootstrap.json"), data, 0600))
	existing := filepath.Join(work, "repo")
	require.NoError(t, os.Mkdir(existing, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(existing, "private.txt"), []byte("existing user data"), 0600))
	agent := filepath.Join(root, "agent.sh")
	agentScript := "#!/bin/sh\nprintf 'agent change\\n' >> code.txt\n"
	if unsafeArtifact {
		agentScript += "printf '%s' \"$UNSAFE_FIXTURE_CREDENTIAL\" >\"$AGENTLAB_ARTIFACTS_DIR/custom.bin\"\n"
	}
	require.NoError(t, os.WriteFile(agent, []byte(agentScript), 0700))
	script, err := filepath.Abs("../../scripts/guest/agent-runner")
	require.NoError(t, err)
	helper := filepath.Join(root, "agentlab-repository")
	build := exec.Command("go", "build", "-o", helper, "./cmd/agentlabd")
	build.Dir = "../.."
	buildOutput, err := build.CombinedOutput()
	require.NoError(t, err, "%s", buildOutput)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", script)
	artifactsDir := filepath.Join(root, "artifacts")
	require.NoError(t, os.MkdirAll(artifactsDir, 0700))
	command.Env = append(os.Environ(), "AGENTLAB_REPOSITORY_TOOL="+helper, "AGENTLAB_RUNNER_ENV="+filepath.Join(root, "missing.env"), "AGENTLAB_BOOTSTRAP="+bootstrapPath, "AGENTLAB_SECRETS_DIR="+secrets, "AGENTLAB_RUN_DIR="+run, "AGENTLAB_WORK_DIR_BASE="+work, "AGENTLAB_REPO_DIR="+existing, "AGENTLAB_AGENT_COMMAND="+agent, "AGENTLAB_RETRY_MAX=1", "AGENTLAB_BOOTSTRAP_CACHE=1")
	command.Env = append(command.Env, "AGENTLAB_ARTIFACTS_DIR="+artifactsDir, "UNSAFE_FIXTURE_CREDENTIAL="+artifactToken)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	report, err := os.ReadFile(filepath.Join(run, "report.json"))
	require.NoError(t, err)
	var result struct {
		Status             string                    `json:"status"`
		RepositoryPath     string                    `json:"repository_path"`
		RepositorySnapshot protocol.RepositorySource `json:"repository_snapshot"`
	}
	require.NoError(t, json.Unmarshal(report, &result))
	require.Equal(t, string(wantStatus), result.Status)
	require.NotEqual(t, existing, result.RepositoryPath)
	require.Equal(t, source.Manifest.Revision, result.RepositorySnapshot.Revision)
	contents, err := os.ReadFile(filepath.Join(result.RepositoryPath, "code.txt"))
	require.NoError(t, err)
	require.Equal(t, "local changes\nagent change\n", string(contents))
	contents, err = os.ReadFile(filepath.Join(existing, "private.txt"))
	require.NoError(t, err)
	require.Equal(t, "existing user data", string(contents))
	require.False(t, strings.Contains(string(output), secret), "guest output exposed its endpoint secret")
	final, err := store.GetJob(context.Background(), "snapshot-task")
	require.NoError(t, err)
	if unsafeArtifact {
		require.Equal(t, models.JobFailed, final.Status, final.ResultJSON)
		artifacts, err := store.ListArtifactsByJob(context.Background(), "snapshot-task")
		require.NoError(t, err)
		require.Empty(t, artifacts, "credential rejection must precede every result upload")
		require.Contains(t, string(output), "execution evidence rejected")
		require.NotContains(t, string(output), artifactToken)
		status, err := backend.Status(context.Background(), 7001)
		require.NoError(t, err)
		require.Equal(t, proxmox.StatusRunning, status)
		return
	}
	require.Equal(t, wantStatus, final.Status, final.ResultJSON)
	var durable struct {
		Result struct {
			RepositoryResult protocol.RepositoryResult `json:"repository_result"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(final.ResultJSON), &durable))
	receipt := durable.Result.RepositoryResult
	require.Equal(t, "verified", receipt.State)
	if len(testCommands) > 0 {
		require.NotNil(t, receipt.Execution)
		require.Equal(t, 0, receipt.Execution.AgentExitCode)
		require.Len(t, receipt.Execution.TestRuns, len(testCommands))
		want := "passed"
		if wantStatus == models.JobFailed {
			want = "failed"
		}
		require.Equal(t, want, receipt.Execution.Tests)
	}
	require.Equal(t, "verified", receipt.CleanupStatus)
	require.Equal(t, source.Manifest.Revision, receipt.SourceRevision)
	patch, err := os.ReadFile(filepath.Join(control.artifactRoot, "snapshot-task", receipt.PatchPath))
	require.NoError(t, err)
	require.Contains(t, string(patch), "+agent change")
	require.NotContains(t, string(patch), "-local changes")
	inventory, err := backend.ListVMs(context.Background())
	require.NoError(t, err)
	for _, vm := range inventory {
		require.NotEqual(t, proxmox.VMID(7001), vm.VMID)
	}
	require.NotContains(t, string(output), artifactToken)

}

func TestBootstrapIncludesPinnedRepositorySource(t *testing.T) {
	control, store, _ := submissionAPI(t)
	control.artifactRoot = t.TempDir()
	response := uploadRepository(t, control, repositoryFixture(t), nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	var source protocol.RepositorySnapshot
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &source))
	seedSecretTestSandbox(t, store, 7001, "snapshot-bootstrap", "10.77.9.10")
	vmid := 7001
	now := time.Now().UTC()
	require.NoError(t, store.CreateJob(context.Background(), models.Job{ID: "snapshot-bootstrap", RepoURL: source.RepoURL, Ref: source.Manifest.Revision, Profile: "default", Status: models.JobRunning, SandboxVMID: &vmid, CreatedAt: now, UpdatedAt: now}))
	hash, err := db.HashBootstrapToken("snapshot-bootstrap-token")
	require.NoError(t, err)
	require.NoError(t, store.CreateBootstrapToken(context.Background(), hash, vmid, now.Add(time.Hour)))
	secretsDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(secretsDir, "default.yaml"), []byte("version: 1\n"), 0600))
	api := NewBootstrapAPI(store, map[string]models.Profile{"default": {Name: "default"}}, secrets.Store{Dir: secretsDir, AllowPlaintext: true}, "default", nil, "", time.Hour, nil, nil)
	r := httptest.NewRequest(http.MethodPost, "/v1/bootstrap/fetch", strings.NewReader(`{"token":"snapshot-bootstrap-token","vmid":7001}`))
	w := httptest.NewRecorder()
	api.handleBootstrapFetch(w, r)
	require.Equal(t, 200, w.Code, w.Body.String())
	var payload V1BootstrapFetchResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.NotNil(t, payload.Job.RepositorySnapshot)
	require.Equal(t, source.ID, payload.Job.RepositorySnapshot.ID)
	require.Equal(t, source.Manifest.Revision, payload.Job.RepositorySnapshot.Revision)
	require.Equal(t, source.Manifest.BundleSHA256, payload.Job.RepositorySnapshot.BundleSHA256)
}
