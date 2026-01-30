package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/secrets"
)

func TestBootstrapFetchSuccess(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 1, 30, 1, 0, 0, 0, time.UTC)

	sandbox := models.Sandbox{
		VMID:          2001,
		Name:          "sandbox-2001",
		Profile:       "yolo",
		State:         models.SandboxRunning,
		Keepalive:     false,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	job := models.Job{
		ID:          "job_bootstrap",
		RepoURL:     "https://example.com/repo.git",
		Ref:         "main",
		Profile:     "yolo",
		Task:        "run tests",
		Mode:        "dangerous",
		Status:      models.JobRunning,
		SandboxVMID: &sandbox.VMID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	token := "token-xyz"
	hash, err := db.HashBootstrapToken(token)
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}
	if err := store.CreateBootstrapToken(ctx, hash, sandbox.VMID, now.Add(5*time.Minute)); err != nil {
		t.Fatalf("create bootstrap token: %v", err)
	}

	secretsDir := t.TempDir()
	bundlePath := filepath.Join(secretsDir, "default.yaml")
	bundle := []byte(`version: 1

git:
  username: "x-access-token"
  token: "ghp_test"

env:
  OPENAI_API_KEY: "sk-test"

claude:
  settings:
    model: "claude-test"
`)
	if err := os.WriteFile(bundlePath, bundle, 0o600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	api := NewBootstrapAPI(store, nil, secrets.Store{Dir: secretsDir, AllowPlaintext: true}, "default", "10.77.0.1:8844", "http://10.77.0.1:8846/upload", time.Hour, nil)
	api.now = func() time.Time { return now }

	payload := `{"token":"` + token + `","vmid":2001}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bootstrap/fetch", strings.NewReader(payload))
	req.RemoteAddr = "10.77.0.55:1234"
	resp := httptest.NewRecorder()
	api.handleBootstrapFetch(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	var decoded V1BootstrapFetchResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Job.ID != job.ID {
		t.Fatalf("expected job id %s, got %s", job.ID, decoded.Job.ID)
	}
	if decoded.Job.RepoURL != job.RepoURL {
		t.Fatalf("expected repo_url %s, got %s", job.RepoURL, decoded.Job.RepoURL)
	}
	if decoded.Env["OPENAI_API_KEY"] != "sk-test" {
		t.Fatalf("expected env OPENAI_API_KEY")
	}
	if decoded.Git == nil || decoded.Git.Token != "ghp_test" {
		t.Fatalf("expected git token")
	}
	if !strings.Contains(decoded.ClaudeSettingsJSON, "claude-test") {
		t.Fatalf("expected claude settings in response")
	}
	if decoded.Artifact == nil || decoded.Artifact.Endpoint == "" {
		t.Fatalf("expected artifact endpoint")
	}
	if decoded.Artifact.Endpoint != "http://10.77.0.1:8846/upload" {
		t.Fatalf("unexpected artifact endpoint %s", decoded.Artifact.Endpoint)
	}
	if decoded.Artifact.Token == "" {
		t.Fatalf("expected artifact token")
	}
	tokenHash, err := db.HashArtifactToken(decoded.Artifact.Token)
	if err != nil {
		t.Fatalf("hash artifact token: %v", err)
	}
	tokenRecord, err := store.GetArtifactToken(ctx, tokenHash)
	if err != nil {
		t.Fatalf("load artifact token: %v", err)
	}
	if tokenRecord.JobID != job.ID {
		t.Fatalf("expected artifact token job %s, got %s", job.ID, tokenRecord.JobID)
	}
	if tokenRecord.VMID == nil || *tokenRecord.VMID != sandbox.VMID {
		t.Fatalf("expected artifact token vmid %d", sandbox.VMID)
	}
	if decoded.Policy == nil || decoded.Policy.Mode != "dangerous" {
		t.Fatalf("expected policy mode dangerous")
	}

	reqReplay := httptest.NewRequest(http.MethodPost, "/v1/bootstrap/fetch", strings.NewReader(payload))
	reqReplay.RemoteAddr = "10.77.0.55:1234"
	replay := httptest.NewRecorder()
	api.handleBootstrapFetch(replay, reqReplay)
	if replay.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on reused token, got %d", replay.Code)
	}
}

func TestBootstrapFetchRejectsNonAgentSubnet(t *testing.T) {
	api := NewBootstrapAPI(newTestStore(t), nil, secrets.Store{}, "default", "10.77.0.1:8844", "http://10.77.0.1:8846/upload", time.Hour, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/bootstrap/fetch", strings.NewReader(`{"token":"t","vmid":1}`))
	req.RemoteAddr = "192.168.1.5:2222"
	resp := httptest.NewRecorder()
	api.handleBootstrapFetch(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.Code)
	}
}
