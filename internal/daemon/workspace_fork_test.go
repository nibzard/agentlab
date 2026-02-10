package daemon

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

func TestWorkspaceForkAttached(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))

	now := time.Now().UTC()
	vmid := 101
	source := models.Workspace{
		ID:          "ws-source",
		Name:        "source",
		Storage:     "local-zfs",
		VolumeID:    "local-zfs:vm-0-disk-1",
		SizeGB:      10,
		AttachedVM:  &vmid,
		CreatedAt:   now,
		LastUpdated: now,
	}
	if err := store.CreateWorkspace(ctx, source); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	_, err := mgr.Fork(ctx, source.ID, "forked", "")
	if !errors.Is(err, ErrWorkspaceForkAttached) {
		t.Fatalf("expected ErrWorkspaceForkAttached, got %v", err)
	}
}

func TestWorkspaceForkSnapshotMissing(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))

	now := time.Now().UTC()
	source := models.Workspace{
		ID:          "ws-source",
		Name:        "source",
		Storage:     "local-zfs",
		VolumeID:    "local-zfs:vm-0-disk-1",
		SizeGB:      10,
		CreatedAt:   now,
		LastUpdated: now,
	}
	if err := store.CreateWorkspace(ctx, source); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	_, err := mgr.Fork(ctx, source.ID, "forked", "missing")
	if !errors.Is(err, ErrWorkspaceSnapshotNotFound) {
		t.Fatalf("expected ErrWorkspaceSnapshotNotFound, got %v", err)
	}
}

func TestWorkspaceForkUnsupportedStorage(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{volumeCloneErr: proxmox.ErrStorageUnsupported}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))

	now := time.Now().UTC()
	source := models.Workspace{
		ID:          "ws-source",
		Name:        "source",
		Storage:     "local-zfs",
		VolumeID:    "local-zfs:vm-0-disk-1",
		SizeGB:      10,
		CreatedAt:   now,
		LastUpdated: now,
	}
	if err := store.CreateWorkspace(ctx, source); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	_, err := mgr.Fork(ctx, source.ID, "forked", "")
	if !errors.Is(err, proxmox.ErrStorageUnsupported) {
		t.Fatalf("expected ErrStorageUnsupported, got %v", err)
	}
}

func TestWorkspaceForkFromSnapshot(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))

	now := time.Now().UTC()
	source := models.Workspace{
		ID:          "ws-source",
		Name:        "source",
		Storage:     "local-zfs",
		VolumeID:    "local-zfs:vm-0-disk-1",
		SizeGB:      10,
		CreatedAt:   now,
		LastUpdated: now,
	}
	if err := store.CreateWorkspace(ctx, source); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	snapshot := models.WorkspaceSnapshot{
		WorkspaceID: source.ID,
		Name:        "snap1",
		BackendRef:  "snap1",
		CreatedAt:   now,
	}
	if err := store.CreateWorkspaceSnapshot(ctx, snapshot); err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	forked, err := mgr.Fork(ctx, source.ID, "forked", "snap1")
	if err != nil {
		t.Fatalf("fork workspace: %v", err)
	}
	if forked.Name != "forked" {
		t.Fatalf("expected forked workspace name, got %s", forked.Name)
	}
	if len(backend.volumeCloneSnapCalls) != 1 {
		t.Fatalf("expected snapshot clone call, got %d", len(backend.volumeCloneSnapCalls))
	}
	if len(backend.volumeCloneCalls) != 0 {
		t.Fatalf("expected no direct clone calls, got %d", len(backend.volumeCloneCalls))
	}
}
