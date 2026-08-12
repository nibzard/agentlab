package daemon

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

// TestReconcileState_VMNotFoundRunning_MarksDestroyed: when Proxmox no longer
// knows about a VM that the DB still records as RUNNING, reconciliation must
// advance it to DESTROYED (review test-debt).
func TestReconcileState_VMNotFoundRunning_MarksDestroyed(t *testing.T) {
	store := newTestStore(t)
	backend := &stubBackend{statusErr: proxmox.ErrVMNotFound}
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))

	requireSandbox(t, store, 7101, models.SandboxRunning)

	if err := mgr.ReconcileState(context.Background()); err != nil {
		t.Fatalf("ReconcileState: %v", err)
	}
	assertState(t, store, 7101, models.SandboxDestroyed)
}

// TestReconcileState_VMNotFoundRequested_LeavesUnchanged: a REQUESTED sandbox
// whose VM is not found is left alone — provisioning may not have cloned yet, so
// "missing" is expected, not a reason to mark destroyed.
func TestReconcileState_VMNotFoundRequested_LeavesUnchanged(t *testing.T) {
	store := newTestStore(t)
	backend := &stubBackend{statusErr: proxmox.ErrVMNotFound}
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))

	requireSandbox(t, store, 7102, models.SandboxRequested)

	if err := mgr.ReconcileState(context.Background()); err != nil {
		t.Fatalf("ReconcileState: %v", err)
	}
	assertState(t, store, 7102, models.SandboxRequested)
}

// TestReconcileState_StoppedRunning_MarksFailed: a VM the DB thinks is RUNNING
// but Proxmox reports STOPPED must reconcile to FAILED.
func TestReconcileState_StoppedRunning_MarksFailed(t *testing.T) {
	store := newTestStore(t)
	backend := &stubBackend{status: proxmox.StatusStopped}
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))

	requireSandbox(t, store, 7103, models.SandboxRunning)

	if err := mgr.ReconcileState(context.Background()); err != nil {
		t.Fatalf("ReconcileState: %v", err)
	}
	assertState(t, store, 7103, models.SandboxFailed)
}

// TestReconcileState_RunningRequested_AdvancesToReady: if AgentLab crashed mid-
// provisioning while the VM actually reached RUNNING, reconciliation advances the
// DB state machine step-by-step toward READY (stopping before RUNNING so the
// provisioning path can finish the last hop).
func TestReconcileState_RunningRequested_AdvancesToReady(t *testing.T) {
	store := newTestStore(t)
	backend := &stubBackend{status: proxmox.StatusRunning}
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))

	requireSandbox(t, store, 7104, models.SandboxRequested)

	if err := mgr.ReconcileState(context.Background()); err != nil {
		t.Fatalf("ReconcileState: %v", err)
	}
	assertState(t, store, 7104, models.SandboxReady)
}

// TestReconcileState_RunningProvisioning_AdvancesToReady: the same recovery also
// works from a PROVISIONING midpoint.
func TestReconcileState_RunningProvisioning_AdvancesToReady(t *testing.T) {
	store := newTestStore(t)
	backend := &stubBackend{status: proxmox.StatusRunning}
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))

	requireSandbox(t, store, 7105, models.SandboxProvisioning)

	if err := mgr.ReconcileState(context.Background()); err != nil {
		t.Fatalf("ReconcileState: %v", err)
	}
	assertState(t, store, 7105, models.SandboxReady)
}

// TestReconcileState_SkipsTerminalStates: DESTROYED and COMPLETED sandboxes are
// not reconciled even if the backend reports an unexpected status.
func TestReconcileState_SkipsTerminalStates(t *testing.T) {
	store := newTestStore(t)
	// Status that would otherwise flip a RUNNING box to FAILED; terminal states
	// must be skipped before that logic runs.
	backend := &stubBackend{status: proxmox.StatusStopped}
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))

	requireSandbox(t, store, 7106, models.SandboxDestroyed)
	requireSandbox(t, store, 7107, models.SandboxCompleted)

	if err := mgr.ReconcileState(context.Background()); err != nil {
		t.Fatalf("ReconcileState: %v", err)
	}
	assertState(t, store, 7106, models.SandboxDestroyed)
	assertState(t, store, 7107, models.SandboxCompleted)
}

// TestReconcileState_NilBackendIsNoop: reconciliation tolerates a missing
// backend (returns nil without touching state).
func TestReconcileState_NilBackendIsNoop(t *testing.T) {
	store := newTestStore(t)
	mgr := NewSandboxManager(store, nil, log.New(io.Discard, "", 0))
	requireSandbox(t, store, 7108, models.SandboxRunning)

	if err := mgr.ReconcileState(context.Background()); err != nil {
		t.Fatalf("ReconcileState: %v", err)
	}
	assertState(t, store, 7108, models.SandboxRunning)
}

// --- helpers ---

func requireSandbox(t *testing.T, store *db.Store, vmid int, state models.SandboxState) {
	t.Helper()
	if err := store.CreateSandbox(context.Background(), models.Sandbox{
		VMID:      vmid,
		Name:      "reconcile-sb",
		Profile:   "default",
		State:     state,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create sandbox %d: %v", vmid, err)
	}
}

func assertState(t *testing.T, store *db.Store, vmid int, want models.SandboxState) {
	t.Helper()
	sb, err := store.GetSandbox(context.Background(), vmid)
	if err != nil {
		t.Fatalf("GetSandbox %d: %v", vmid, err)
	}
	if sb.State != want {
		t.Fatalf("sandbox %d state = %s, want %s", vmid, sb.State, want)
	}
}
