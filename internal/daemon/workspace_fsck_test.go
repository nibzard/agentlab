package daemon

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

func TestWorkspaceFSCKReadOnlyDefault(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{
		volumeInfo: proxmox.VolumeInfo{
			VolumeID: "local-zfs:vm-0-disk-9",
			Storage:  "local-zfs",
			Path:     "/dev/fake",
		},
	}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))
	mgr.fsckTargetValidator = func(string) error { return nil }

	var gotArgs []string
	mgr.fsckRunner = func(_ context.Context, args []string) (string, int, error) {
		gotArgs = append([]string{}, args...)
		return "clean", 0, nil
	}

	now := time.Now().UTC()
	workspace := models.Workspace{
		ID:          "ws-fsck-ro",
		Name:        "fsck-ro",
		Storage:     "local-zfs",
		VolumeID:    "local-zfs:vm-0-disk-9",
		SizeGB:      10,
		CreatedAt:   now,
		LastUpdated: now,
	}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	result, err := mgr.FSCK(ctx, workspace.ID, false)
	if err != nil {
		t.Fatalf("fsck: %v", err)
	}
	if result.Mode != workspaceFSCKModeReadOnly {
		t.Fatalf("expected mode %q, got %q", workspaceFSCKModeReadOnly, result.Mode)
	}
	if !containsArg(gotArgs, "-n") {
		t.Fatalf("expected -n for read-only fsck, args=%v", gotArgs)
	}
	if containsArg(gotArgs, "-y") {
		t.Fatalf("did not expect -y for read-only fsck, args=%v", gotArgs)
	}
}

func TestWorkspaceFSCKRepairFlag(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{
		volumeInfo: proxmox.VolumeInfo{
			VolumeID: "local-zfs:vm-0-disk-10",
			Storage:  "local-zfs",
			Path:     "/dev/fake",
		},
	}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))
	mgr.fsckTargetValidator = func(string) error { return nil }

	var gotArgs []string
	mgr.fsckRunner = func(_ context.Context, args []string) (string, int, error) {
		gotArgs = append([]string{}, args...)
		return "repaired", 1, nil
	}

	now := time.Now().UTC()
	workspace := models.Workspace{
		ID:          "ws-fsck-repair",
		Name:        "fsck-repair",
		Storage:     "local-zfs",
		VolumeID:    "local-zfs:vm-0-disk-10",
		SizeGB:      10,
		CreatedAt:   now,
		LastUpdated: now,
	}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	result, err := mgr.FSCK(ctx, workspace.ID, true)
	if err != nil {
		t.Fatalf("fsck: %v", err)
	}
	if result.Mode != workspaceFSCKModeRepair {
		t.Fatalf("expected mode %q, got %q", workspaceFSCKModeRepair, result.Mode)
	}
	if !containsArg(gotArgs, "-y") {
		t.Fatalf("expected -y for repair fsck, args=%v", gotArgs)
	}
	if containsArg(gotArgs, "-n") {
		t.Fatalf("did not expect -n for repair fsck, args=%v", gotArgs)
	}
}

func TestWorkspaceFSCKAttached(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{
		volumeInfo: proxmox.VolumeInfo{VolumeID: "local-zfs:vm-0-disk-11"},
	}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))
	mgr.fsckTargetValidator = func(string) error { return nil }
	mgr.fsckRunner = func(_ context.Context, _ []string) (string, int, error) {
		return "", 0, nil
	}

	now := time.Now().UTC()
	vmid := 101
	workspace := models.Workspace{
		ID:          "ws-fsck-attached",
		Name:        "fsck-attached",
		Storage:     "local-zfs",
		VolumeID:    "local-zfs:vm-0-disk-11",
		SizeGB:      10,
		AttachedVM:  &vmid,
		CreatedAt:   now,
		LastUpdated: now,
	}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	_, err := mgr.FSCK(ctx, workspace.ID, false)
	if !errorsIs(err, ErrWorkspaceFSCKAttached) {
		t.Fatalf("expected ErrWorkspaceFSCKAttached, got %v", err)
	}
}

func TestWorkspaceFSCKNeedsRepairStatus(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{
		volumeInfo: proxmox.VolumeInfo{
			VolumeID: "local-zfs:vm-0-disk-12",
			Storage:  "local-zfs",
			Path:     "/dev/fake",
		},
	}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))
	mgr.fsckTargetValidator = func(string) error { return nil }
	mgr.fsckRunner = func(_ context.Context, _ []string) (string, int, error) {
		return "errors", 4, nil
	}

	now := time.Now().UTC()
	workspace := models.Workspace{
		ID:          "ws-fsck-needs",
		Name:        "fsck-needs",
		Storage:     "local-zfs",
		VolumeID:    "local-zfs:vm-0-disk-12",
		SizeGB:      10,
		CreatedAt:   now,
		LastUpdated: now,
	}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	result, err := mgr.FSCK(ctx, workspace.ID, false)
	if err != nil {
		t.Fatalf("fsck: %v", err)
	}
	if result.Status != workspaceFSCKStatusNeedsRepair {
		t.Fatalf("expected status %q, got %q", workspaceFSCKStatusNeedsRepair, result.Status)
	}
	if !result.NeedsRepair {
		t.Fatalf("expected needs_repair true")
	}
}

func containsArg(args []string, target string) bool {
	for _, arg := range args {
		if strings.TrimSpace(arg) == target {
			return true
		}
	}
	return false
}

func errorsIs(err, target error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, target)
}
