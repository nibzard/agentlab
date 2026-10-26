package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

type leaseChangingBackend struct {
	stubBackend
	onDestroy func()
}

func (b *leaseChangingBackend) Destroy(ctx context.Context, id proxmox.VMID) error {
	if b.onDestroy != nil {
		b.onDestroy()
	}
	return b.stubBackend.Destroy(ctx, id)
}

func TestDestroySessionLeaseAndConcurrentReacquisition(t *testing.T) {
	for _, mode := range []string{"owned", "rebound", "new-nonce", "rebound-during-destroy", "backend-failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			store := newTestStore(t)
			vmid := 1500
			backend := &leaseChangingBackend{}
			manager := NewSandboxManager(store, backend, nil)
			if err := store.CreateSandbox(ctx, models.Sandbox{VMID: vmid, Name: "fixture", Profile: "default", State: models.SandboxRunning}); err != nil {
				t.Fatal(err)
			}
			workspace := models.Workspace{ID: "ws", Name: "ws", Storage: "local", VolumeID: "vol", SizeGB: 10}
			if err := store.CreateWorkspace(ctx, workspace); err != nil {
				t.Fatal(err)
			}
			session := models.Session{ID: "session", Name: "session", Profile: "default", WorkspaceID: workspace.ID, CurrentVMID: &vmid}
			if err := store.CreateSession(ctx, session); err != nil {
				t.Fatal(err)
			}
			owner := workspaceLeaseOwnerForSession(session.ID)
			if ok, err := store.TryAcquireWorkspaceLease(ctx, workspace.ID, owner, "original", time.Now().Add(time.Hour)); err != nil || !ok {
				t.Fatalf("lease: %t %v", ok, err)
			}
			if mode == "rebound" {
				newVMID := 1501
				if err := store.UpdateSessionCurrentVMID(ctx, session.ID, &newVMID); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "new-nonce" {
				backend.onDestroy = func() {
					if ok, err := store.ReleaseWorkspaceLease(ctx, workspace.ID, owner, "original"); err != nil || !ok {
						t.Fatalf("release for replacement: %t %v", ok, err)
					}
					if ok, err := store.TryAcquireWorkspaceLease(ctx, workspace.ID, owner, "replacement", time.Now().Add(time.Hour)); err != nil || !ok {
						t.Fatalf("replacement: %t %v", ok, err)
					}
				}
			}
			if mode == "rebound-during-destroy" {
				backend.onDestroy = func() {
					newVMID := 1501
					if err := store.UpdateSessionCurrentVMID(ctx, session.ID, &newVMID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "backend-failure" {
				backend.destroyErr = errors.New("fixture destroy failure")
			}
			err := manager.ForceDestroy(ctx, vmid)
			if (err != nil) != (mode == "backend-failure") {
				t.Fatalf("unexpected destroy result: %v", err)
			}
			updated, err := store.GetWorkspace(ctx, workspace.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "owned":
				if updated.LeaseOwner != "" || updated.LeaseNonce != "" || !updated.LeaseExpires.IsZero() {
					t.Fatalf("owned lease retained: %+v", updated)
				}
			case "new-nonce":
				if updated.LeaseNonce != "replacement" {
					t.Fatal("stale cleanup released newly acquired nonce")
				}
			default:
				if updated.LeaseNonce != "original" {
					t.Fatal("released unrelated or failed-operation lease")
				}
			}
			sandbox, err := store.GetSandbox(ctx, vmid)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "backend-failure" && sandbox.State == models.SandboxDestroyed {
				t.Fatal("failed backend destruction marked successful")
			}
		})
	}
}
