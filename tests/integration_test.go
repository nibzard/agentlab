//go:build integration
// +build integration

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/config"
	"github.com/agentlab/agentlab/internal/daemon"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type integrationHarness struct {
	cfg          config.Config
	backend      *proxmox.FakeBackend
	store        *db.Store
	service      *daemon.Service
	controlURL   string
	token        string
	unixClient   *http.Client
	remoteClient *http.Client
	cancel       context.CancelFunc
	errCh        chan error
}

func newIntegrationHarness(t *testing.T) *integrationHarness {
	t.Helper()
	temp := t.TempDir()
	token := "test-token"
	cfg := config.Config{
		ConfigPath:              filepath.Join(temp, "config.yaml"),
		ProfilesDir:             filepath.Join(temp, "profiles"),
		DataDir:                 temp,
		LogDir:                  filepath.Join(temp, "log"),
		RunDir:                  filepath.Join(temp, "run"),
		SocketPath:              filepath.Join(temp, "run", "agentlabd.sock"),
		ControlListen:           "127.0.0.1:0",
		ControlAuthToken:        token,
		DBPath:                  filepath.Join(temp, "agentlab.db"),
		BootstrapListen:         "127.0.0.1:0",
		ArtifactListen:          "127.0.0.1:0",
		ArtifactDir:             filepath.Join(temp, "artifacts"),
		ArtifactMaxBytes:        10 * 1024 * 1024,
		ArtifactTokenTTLMinutes: 60,
		SecretsDir:              filepath.Join(temp, "secrets"),
		SecretsBundle:           "default",
		SecretsAgeKeyPath:       filepath.Join(temp, "age.key"),
		SecretsSopsPath:         "sops",
		SnippetsDir:             filepath.Join(temp, "snippets"),
		SnippetStorage:          "local",
		SSHPublicKey:            "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBtestkey agent@test",
		ProxmoxCommandTimeout:   time.Second,
		ProvisioningTimeout:     time.Second,
		IdleStopEnabled:         false,
		IdleStopInterval:        time.Second,
	}

	store, err := db.Open(cfg.DBPath)
	require.NoError(t, err)

	backend := proxmox.NewFakeBackend()
	backend.AddTemplate(9000)

	profiles := map[string]models.Profile{
		"default": {
			Name:       "default",
			TemplateVM: 9000,
			RawYAML:    "",
		},
	}

	service, err := daemon.NewServiceWithBackend(cfg, profiles, store, backend)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Serve(ctx)
	}()

	controlAddr := service.ControlAddr()
	require.NotEmpty(t, controlAddr)
	controlURL := "http://" + controlAddr

	unixClient := newUnixClient(cfg.SocketPath)
	remoteClient := &http.Client{}

	waitForHealth(t, remoteClient, controlURL+"/healthz")
	waitForHealth(t, unixClient, "http://unix/healthz")

	h := &integrationHarness{
		cfg:          cfg,
		backend:      backend,
		store:        store,
		service:      service,
		controlURL:   controlURL,
		token:        token,
		unixClient:   unixClient,
		remoteClient: remoteClient,
		cancel:       cancel,
		errCh:        errCh,
	}

	t.Cleanup(func() {
		cancel()
		err := <-errCh
		require.NoError(t, err)
		_ = os.Remove(cfg.SocketPath)
	})

	return h
}

func newUnixClient(socketPath string) *http.Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	return &http.Client{Transport: transport}
}

func waitForHealth(t *testing.T, client *http.Client, url string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for healthz on %s", url)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func apiRequest(t *testing.T, client *http.Client, baseURL, method, path, token string, payload any) (int, http.Header, []byte) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		buf := &bytes.Buffer{}
		enc := json.NewEncoder(buf)
		enc.SetEscapeHTML(false)
		err := enc.Encode(payload)
		require.NoError(t, err)
		body = buf
	}
	req, err := http.NewRequest(method, baseURL+path, body)
	require.NoError(t, err)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, resp.Header, data
}

func waitForJobStatus(t *testing.T, h *integrationHarness, jobID, status string) daemon.V1JobResponse {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		code, _, body := apiRequest(t, h.remoteClient, h.controlURL, http.MethodGet, "/v1/jobs/"+jobID, h.token, nil)
		if code == http.StatusOK {
			var resp daemon.V1JobResponse
			if err := json.Unmarshal(body, &resp); err == nil {
				if resp.Status == status && resp.SandboxVMID != nil {
					return resp
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for job %s to reach status %s", jobID, status)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitForWorkspaceAttached(t *testing.T, h *integrationHarness, workspaceID string, vmid int) daemon.V1WorkspaceResponse {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		code, _, body := apiRequest(t, h.remoteClient, h.controlURL, http.MethodGet, "/v1/workspaces/"+workspaceID, h.token, nil)
		if code == http.StatusOK {
			var resp daemon.V1WorkspaceResponse
			if err := json.Unmarshal(body, &resp); err == nil {
				if resp.AttachedVMID != nil && *resp.AttachedVMID == vmid {
					return resp
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for workspace %s to attach to vmid %d", workspaceID, vmid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestControlAuthRemoteAndUnix(t *testing.T) {
	h := newIntegrationHarness(t)

	code, headers, _ := apiRequest(t, h.remoteClient, h.controlURL, http.MethodGet, "/v1/status", "", nil)
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.NotEmpty(t, headers.Get("WWW-Authenticate"))

	code, _, body := apiRequest(t, h.remoteClient, h.controlURL, http.MethodGet, "/v1/status", h.token, nil)
	assert.Equal(t, http.StatusOK, code)
	var statusResp daemon.V1StatusResponse
	require.NoError(t, json.Unmarshal(body, &statusResp))

	code, _, body = apiRequest(t, h.unixClient, "http://unix", http.MethodGet, "/v1/status", "", nil)
	assert.Equal(t, http.StatusOK, code)
	require.NoError(t, json.Unmarshal(body, &statusResp))
}

func TestJobCreateWithWorkspaceAttach(t *testing.T) {
	h := newIntegrationHarness(t)

	jobReq := daemon.V1JobCreateRequest{
		RepoURL: "https://example.com/repo.git",
		Profile: "default",
		Task:    "echo hi",
		WorkspaceCreate: &daemon.V1WorkspaceCreateRequest{
			Name:   "ws-stateful",
			SizeGB: 10,
		},
	}
	code, _, body := apiRequest(t, h.remoteClient, h.controlURL, http.MethodPost, "/v1/jobs", h.token, jobReq)
	require.Equal(t, http.StatusCreated, code)

	var jobResp daemon.V1JobResponse
	require.NoError(t, json.Unmarshal(body, &jobResp))
	require.NotEmpty(t, jobResp.ID)
	require.NotNil(t, jobResp.WorkspaceID)

	jobResp = waitForJobStatus(t, h, jobResp.ID, string(models.JobRunning))
	require.NotNil(t, jobResp.SandboxVMID)
	workspace := waitForWorkspaceAttached(t, h, *jobResp.WorkspaceID, *jobResp.SandboxVMID)
	require.NotNil(t, workspace.AttachedVMID)
	assert.Equal(t, *jobResp.SandboxVMID, *workspace.AttachedVMID)
}

func TestWorkspaceLeaseConflictAndWait(t *testing.T) {
	h := newIntegrationHarness(t)

	createReq := daemon.V1WorkspaceCreateRequest{
		Name:   "shared-ws",
		SizeGB: 5,
	}
	code, _, body := apiRequest(t, h.remoteClient, h.controlURL, http.MethodPost, "/v1/workspaces", h.token, createReq)
	require.Equal(t, http.StatusCreated, code)
	var wsResp daemon.V1WorkspaceResponse
	require.NoError(t, json.Unmarshal(body, &wsResp))
	require.NotEmpty(t, wsResp.ID)

	leaseOwner := "job:held"
	leaseNonce := "nonce-held"
	acquired, err := h.store.TryAcquireWorkspaceLease(context.Background(), wsResp.ID, leaseOwner, leaseNonce, time.Now().UTC().Add(5*time.Minute))
	require.NoError(t, err)
	require.True(t, acquired)

	jobReq := daemon.V1JobCreateRequest{
		RepoURL:     "https://example.com/repo.git",
		Profile:     "default",
		Task:        "echo waiting",
		WorkspaceID: &wsResp.ID,
	}
	code, _, body = apiRequest(t, h.remoteClient, h.controlURL, http.MethodPost, "/v1/jobs", h.token, jobReq)
	assert.Equal(t, http.StatusConflict, code)
	var errResp map[string]string
	require.NoError(t, json.Unmarshal(body, &errResp))
	assert.Equal(t, "workspace lease held", errResp["error"])

	waitSeconds := 2
	jobReq.WorkspaceWaitSeconds = &waitSeconds

	releaseDone := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond)
		_, _ = h.store.ReleaseWorkspaceLease(context.Background(), wsResp.ID, leaseOwner, leaseNonce)
		close(releaseDone)
	}()

	start := time.Now()
	code, _, body = apiRequest(t, h.remoteClient, h.controlURL, http.MethodPost, "/v1/jobs", h.token, jobReq)
	elapsed := time.Since(start)
	assert.Equal(t, http.StatusCreated, code)
	assert.GreaterOrEqual(t, elapsed, 200*time.Millisecond)

	var jobResp daemon.V1JobResponse
	require.NoError(t, json.Unmarshal(body, &jobResp))
	require.NotEmpty(t, jobResp.ID)
	<-releaseDone
}

func TestArtifactListAndDownloadRemote(t *testing.T) {
	h := newIntegrationHarness(t)

	jobID := "job-artifacts"
	now := time.Now().UTC()
	job := models.Job{
		ID:        jobID,
		RepoURL:   "https://example.com/repo.git",
		Ref:       "main",
		Profile:   "default",
		Status:    models.JobCompleted,
		CreatedAt: now,
		UpdatedAt: now,
	}
	require.NoError(t, h.store.CreateJob(context.Background(), job))

	jobDir := filepath.Join(h.cfg.ArtifactDir, jobID)
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	content := []byte("artifact payload")
	require.NoError(t, os.WriteFile(filepath.Join(jobDir, "output.txt"), content, 0o600))

	artifact := db.Artifact{
		JobID:     jobID,
		Name:      "output.txt",
		Path:      "output.txt",
		SizeBytes: int64(len(content)),
		Sha256:    "deadbeef",
		CreatedAt: now,
	}
	_, err := h.store.CreateArtifact(context.Background(), artifact)
	require.NoError(t, err)

	code, _, body := apiRequest(t, h.remoteClient, h.controlURL, http.MethodGet, "/v1/jobs/"+jobID+"/artifacts", h.token, nil)
	require.Equal(t, http.StatusOK, code)
	var listResp daemon.V1ArtifactsResponse
	require.NoError(t, json.Unmarshal(body, &listResp))
	require.Len(t, listResp.Artifacts, 1)
	assert.Equal(t, "output.txt", listResp.Artifacts[0].Name)

	code, headers, body := apiRequest(t, h.remoteClient, h.controlURL, http.MethodGet, "/v1/jobs/"+jobID+"/artifacts/download?name=output.txt", h.token, nil)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, headers.Get("Content-Disposition"), "output.txt")
	assert.Equal(t, content, body)
}
