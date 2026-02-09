package daemon

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

func TestWorkspaceCheckVolumeMissing(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{volumeErr: proxmox.ErrVolumeNotFound}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))

	now := time.Now().UTC()
	workspace := models.Workspace{
		ID:          "ws-missing",
		Name:        "missing",
		Storage:     "local-zfs",
		VolumeID:    "local-zfs:vm-0-disk-0",
		SizeGB:      10,
		CreatedAt:   now,
		LastUpdated: now,
	}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	result, err := mgr.Check(ctx, workspace.ID)
	if err != nil {
		t.Fatalf("check workspace: %v", err)
	}
	if result.Volume.Exists {
		t.Fatalf("expected volume missing")
	}
	if !hasWorkspaceFinding(result.Findings, workspaceCheckCodeVolumeMissing) {
		t.Fatalf("expected volume_missing finding")
	}
}

func TestWorkspaceCheckStaleAttachedVM(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{
		vmConfigErr: proxmox.ErrVMNotFound,
		volumeInfo:  proxmox.VolumeInfo{VolumeID: "local-zfs:vm-0-disk-1"},
	}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))

	now := time.Now().UTC()
	vmid := 120
	workspace := models.Workspace{
		ID:          "ws-stale",
		Name:        "stale",
		Storage:     "local-zfs",
		VolumeID:    "local-zfs:vm-0-disk-1",
		SizeGB:      20,
		AttachedVM:  &vmid,
		CreatedAt:   now,
		LastUpdated: now,
	}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	result, err := mgr.Check(ctx, workspace.ID)
	if err != nil {
		t.Fatalf("check workspace: %v", err)
	}
	if !hasWorkspaceFinding(result.Findings, workspaceCheckCodeAttachedVMStale) {
		t.Fatalf("expected attached_vmid_stale finding")
	}
	if !hasWorkspaceFinding(result.Findings, workspaceCheckCodeVMissing) {
		t.Fatalf("expected vm_missing finding")
	}
}

func TestWorkspaceCheckWrongSlotAndDrift(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{
		vmConfig: map[string]string{
			"scsi2": "local-zfs:vm-0-disk-2",
		},
		volumeInfo: proxmox.VolumeInfo{VolumeID: "local-zfs:vm-0-disk-2", Storage: "local-zfs"},
	}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))

	now := time.Now().UTC()
	vmid := 101
	sandbox := models.Sandbox{
		VMID:          vmid,
		Name:          "sb-101",
		Profile:       "default",
		State:         models.SandboxRunning,
		Keepalive:     false,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	workspace := models.Workspace{
		ID:          "ws-drift",
		Name:        "drift",
		Storage:     "local-zfs",
		VolumeID:    "local-zfs:vm-0-disk-2",
		SizeGB:      30,
		AttachedVM:  &vmid,
		CreatedAt:   now,
		LastUpdated: now,
	}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	result, err := mgr.Check(ctx, workspace.ID)
	if err != nil {
		t.Fatalf("check workspace: %v", err)
	}
	if !hasWorkspaceFinding(result.Findings, workspaceCheckCodeSandboxDrift) {
		t.Fatalf("expected sandbox_record_drift finding")
	}
	if !hasWorkspaceFinding(result.Findings, workspaceCheckCodeVolumeWrongSlot) {
		t.Fatalf("expected volume_wrong_slot finding")
	}
}

func hasWorkspaceFinding(findings []WorkspaceCheckFinding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
