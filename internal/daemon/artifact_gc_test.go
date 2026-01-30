package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
)

func TestParseArtifactRetention(t *testing.T) {
	raw := "name: retention\ntemplate_vmid: 9000\nartifacts:\n  ttl_minutes: 120\n"
	dur, configured, err := parseArtifactRetention(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !configured {
		t.Fatal("expected retention to be configured")
	}
	if dur != 2*time.Hour {
		t.Fatalf("expected 2h retention, got %s", dur)
	}

	raw = "name: retention\nartifacts:\n  retention_days: 2\n"
	dur, configured, err = parseArtifactRetention(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !configured {
		t.Fatal("expected retention to be configured")
	}
	if dur != 48*time.Hour {
		t.Fatalf("expected 48h retention, got %s", dur)
	}

	dur, configured, err = parseArtifactRetention(" ")
	if err != nil {
		t.Fatalf("unexpected error for empty: %v", err)
	}
	if configured {
		t.Fatal("expected retention to be unconfigured")
	}
	if dur != 0 {
		t.Fatalf("expected zero duration, got %s", dur)
	}
}

func TestArtifactGCDeletesExpired(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 1, 30, 4, 0, 0, 0, time.UTC)

	sandbox := models.Sandbox{
		VMID:          2201,
		Name:          "sandbox-retention",
		Profile:       "retention-profile",
		State:         models.SandboxDestroyed,
		CreatedAt:     now.Add(-3 * time.Hour),
		LastUpdatedAt: now.Add(-2 * time.Hour),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	job := models.Job{
		ID:          "job-retention",
		RepoURL:     "https://example.com/repo.git",
		Ref:         "main",
		Profile:     "retention-profile",
		Status:      models.JobCompleted,
		SandboxVMID: &sandbox.VMID,
		CreatedAt:   now.Add(-3 * time.Hour),
		UpdatedAt:   now.Add(-90 * time.Minute),
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	root := t.TempDir()
	jobDir := filepath.Join(root, job.ID)
	if err := os.MkdirAll(jobDir, 0o750); err != nil {
		t.Fatalf("create job dir: %v", err)
	}
	content := []byte("artifact data")
	sum := sha256.Sum256(content)
	artifactPath := filepath.Join(jobDir, "bundle.tar.gz")
	if err := os.WriteFile(artifactPath, content, 0o640); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	vmid := sandbox.VMID
	artifact := db.Artifact{
		JobID:     job.ID,
		VMID:      &vmid,
		Name:      "bundle.tar.gz",
		Path:      "bundle.tar.gz",
		SizeBytes: int64(len(content)),
		Sha256:    hex.EncodeToString(sum[:]),
		MIME:      "application/gzip",
		CreatedAt: now.Add(-2 * time.Hour),
	}
	if _, err := store.CreateArtifact(ctx, artifact); err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	profiles := map[string]models.Profile{
		"retention-profile": {
			Name:       "retention-profile",
			TemplateVM: 9000,
			RawYAML:    "name: retention-profile\ntemplate_vmid: 9000\nartifacts:\n  ttl_minutes: 60\n",
		},
	}
	gc := NewArtifactGC(store, profiles, root, log.New(io.Discard, "", 0), NewRedactor(nil))
	gc.now = func() time.Time { return now }

	gc.run(ctx)

	if _, err := os.Stat(artifactPath); !os.IsNotExist(err) {
		t.Fatalf("expected artifact file deleted, got err=%v", err)
	}
	artifacts, err := store.ListArtifactsByJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("expected artifacts deleted, got %d", len(artifacts))
	}
	events, err := store.ListEventsBySandbox(ctx, sandbox.VMID, 0, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected deletion event")
	}
	if events[0].Kind != "artifact.gc" {
		t.Fatalf("expected artifact.gc event, got %s", events[0].Kind)
	}
}
