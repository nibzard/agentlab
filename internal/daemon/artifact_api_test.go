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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
)

func TestArtifactUploadSuccess(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 1, 30, 2, 0, 0, 0, time.UTC)

	job := models.Job{
		ID:        "job_artifacts",
		RepoURL:   "https://example.com/repo.git",
		Ref:       "main",
		Profile:   "yolo",
		Status:    models.JobRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	token := "artifact-token"
	hash, err := db.HashArtifactToken(token)
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}
	if err := store.CreateArtifactToken(ctx, hash, job.ID, 2001, now.Add(time.Hour)); err != nil {
		t.Fatalf("create artifact token: %v", err)
	}

	root := t.TempDir()
	agentSubnet := mustParseCIDR(t, "10.77.0.0/16")
	api := NewArtifactAPI(store, root, 1024, agentSubnet, nil)
	api.now = func() time.Time { return now }

	body := []byte("hello world")
	req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "text/plain")
	req.RemoteAddr = "10.77.0.55:1234"
	resp := httptest.NewRecorder()

	api.handleUpload(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.Code)
	}

	var decoded V1ArtifactUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.JobID != job.ID {
		t.Fatalf("expected job_id %s, got %s", job.ID, decoded.JobID)
	}
	if decoded.Artifact.Path != defaultArtifactName {
		t.Fatalf("expected artifact path %s, got %s", defaultArtifactName, decoded.Artifact.Path)
	}
	expectedSha := sha256.Sum256(body)
	if decoded.Artifact.Sha256 != hex.EncodeToString(expectedSha[:]) {
		t.Fatalf("unexpected sha256")
	}

	artifactPath := filepath.Join(root, job.ID, defaultArtifactName)
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("artifact file missing: %v", err)
	}
	artifacts, err := store.ListArtifactsByJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(artifacts))
	}
	if artifacts[0].Path != defaultArtifactName {
		t.Fatalf("expected stored artifact path %s, got %s", defaultArtifactName, artifacts[0].Path)
	}
}

func TestArtifactUploadCleansFileOnInsertFailure(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 1, 30, 2, 15, 0, 0, time.UTC)

	job := models.Job{
		ID:        "job_artifact_insert_fail",
		RepoURL:   "https://example.com/repo.git",
		Ref:       "main",
		Profile:   "yolo",
		Status:    models.JobRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	token := "artifact-token-insert-fail"
	hash, err := db.HashArtifactToken(token)
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}
	if err := store.CreateArtifactToken(ctx, hash, job.ID, 2004, now.Add(time.Hour)); err != nil {
		t.Fatalf("create artifact token: %v", err)
	}
	if _, err := store.DB.Exec(`DROP TABLE artifacts`); err != nil {
		t.Fatalf("drop artifacts table: %v", err)
	}

	root := t.TempDir()
	agentSubnet := mustParseCIDR(t, "10.77.0.0/16")
	api := NewArtifactAPI(store, root, 1024, agentSubnet, nil)
	api.now = func() time.Time { return now }

	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("data"))
	req.Header.Set("Authorization", "Bearer "+token)
	req.RemoteAddr = "10.77.0.55:1234"
	resp := httptest.NewRecorder()

	api.handleUpload(resp, req)
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.Code)
	}

	artifactPath := filepath.Join(root, job.ID, defaultArtifactName)
	if _, err := os.Stat(artifactPath); err == nil || !os.IsNotExist(err) {
		t.Fatalf("expected artifact file removed, got err=%v", err)
	}
}

func TestArtifactUploadRateLimit(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 1, 30, 2, 5, 0, 0, time.UTC)

	job := models.Job{
		ID:        "job_artifacts_limit",
		RepoURL:   "https://example.com/repo.git",
		Ref:       "main",
		Profile:   "yolo",
		Status:    models.JobRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	token := "artifact-token-limit"
	hash, err := db.HashArtifactToken(token)
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}
	if err := store.CreateArtifactToken(ctx, hash, job.ID, 2005, now.Add(time.Hour)); err != nil {
		t.Fatalf("create artifact token: %v", err)
	}

	root := t.TempDir()
	agentSubnet := mustParseCIDR(t, "10.77.0.0/16")
	limiter := NewIPRateLimiter(1, 1)
	if limiter == nil {
		t.Fatal("expected limiter to be initialized")
	}
	limiter.now = func() time.Time { return now }
	if !limiter.Allow("10.77.0.55:1234") {
		t.Fatal("expected limiter to allow initial token")
	}

	api := NewArtifactAPI(store, root, 1024, agentSubnet, limiter)
	api.now = func() time.Time { return now }

	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("data"))
	req.Header.Set("Authorization", "Bearer "+token)
	req.RemoteAddr = "10.77.0.55:1234"
	resp := httptest.NewRecorder()

	api.handleUpload(resp, req)
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", resp.Code)
	}

	artifacts, err := store.ListArtifactsByJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("expected no artifacts recorded, got %d", len(artifacts))
	}

	if _, err := os.Stat(filepath.Join(root, job.ID)); err == nil || !os.IsNotExist(err) {
		t.Fatalf("expected no artifact directory created, got err=%v", err)
	}
}

func TestArtifactUploadRejectsTraversal(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 1, 30, 2, 30, 0, 0, time.UTC)

	job := models.Job{
		ID:        "job_traversal",
		RepoURL:   "https://example.com/repo.git",
		Ref:       "main",
		Profile:   "yolo",
		Status:    models.JobRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	token := "artifact-token-traversal"
	hash, err := db.HashArtifactToken(token)
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}
	if err := store.CreateArtifactToken(ctx, hash, job.ID, 2002, now.Add(time.Hour)); err != nil {
		t.Fatalf("create artifact token: %v", err)
	}

	agentSubnet := mustParseCIDR(t, "10.77.0.0/16")
	api := NewArtifactAPI(store, t.TempDir(), 1024, agentSubnet, nil)
	api.now = func() time.Time { return now }
	req := httptest.NewRequest(http.MethodPost, "/upload?path=../evil", strings.NewReader("data"))
	req.Header.Set("Authorization", "Bearer "+token)
	req.RemoteAddr = "10.77.0.55:1234"
	resp := httptest.NewRecorder()

	api.handleUpload(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.Code)
	}
}

func TestArtifactUploadEnforcesSizeLimit(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 1, 30, 3, 0, 0, 0, time.UTC)

	job := models.Job{
		ID:        "job_limits",
		RepoURL:   "https://example.com/repo.git",
		Ref:       "main",
		Profile:   "yolo",
		Status:    models.JobRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	token := "artifact-token-limit"
	hash, err := db.HashArtifactToken(token)
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}
	if err := store.CreateArtifactToken(ctx, hash, job.ID, 2003, now.Add(time.Hour)); err != nil {
		t.Fatalf("create artifact token: %v", err)
	}

	agentSubnet := mustParseCIDR(t, "10.77.0.0/16")
	api := NewArtifactAPI(store, t.TempDir(), 4, agentSubnet, nil)
	api.now = func() time.Time { return now }
	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("too-large"))
	req.Header.Set("Authorization", "Bearer "+token)
	req.RemoteAddr = "10.77.0.55:1234"
	resp := httptest.NewRecorder()

	api.handleUpload(resp, req)
	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", resp.Code)
	}
}
