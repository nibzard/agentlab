package daemon

import (
	"context"
	"errors"
	"io"
	"log"
	"strconv"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
)

func TestWorkspaceAttachLeaseHeldByOtherSandbox(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))

	now := time.Now().UTC()
	if err := store.CreateSandbox(ctx, models.Sandbox{
		VMID:          1002,
		Name:          "sandbox-1002",
		Profile:       "default",
		State:         models.SandboxRequested,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	workspace := models.Workspace{
		ID:           "ws-leased",
		Name:         "leased",
		Storage:      "local-zfs",
		VolumeID:     "local-zfs:vm-0-disk-1",
		SizeGB:       10,
		LeaseOwner:   workspaceLeaseOwnerForSandbox(1001),
		LeaseNonce:   "nonce",
		LeaseExpires: now.Add(10 * time.Minute),
		CreatedAt:    now,
		LastUpdated:  now,
	}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	_, err := mgr.Attach(ctx, workspace.ID, 1002)
	if !errors.Is(err, ErrWorkspaceLeaseHeld) {
		t.Fatalf("expected ErrWorkspaceLeaseHeld, got %v", err)
	}
	updated, err := store.GetWorkspace(ctx, workspace.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if updated.AttachedVM != nil {
		t.Fatalf("expected workspace to stay detached, got attached_vmid=%d", *updated.AttachedVM)
	}
	sandbox, err := store.GetSandbox(ctx, 1002)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sandbox.WorkspaceID != nil {
		t.Fatalf("expected sandbox workspace to stay unset, got %s", *sandbox.WorkspaceID)
	}
}

func TestWorkspaceAttachLeaseHeldBySameSandbox(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))

	now := time.Now().UTC()
	if err := store.CreateSandbox(ctx, models.Sandbox{
		VMID:          1002,
		Name:          "sandbox-1002",
		Profile:       "default",
		State:         models.SandboxRequested,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	workspace := models.Workspace{
		ID:           "ws-leased",
		Name:         "leased",
		Storage:      "local-zfs",
		VolumeID:     "local-zfs:vm-0-disk-1",
		SizeGB:       10,
		LeaseOwner:   workspaceLeaseOwnerForSandbox(1002),
		LeaseNonce:   "nonce",
		LeaseExpires: now.Add(10 * time.Minute),
		CreatedAt:    now,
		LastUpdated:  now,
	}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	attached, err := mgr.Attach(ctx, workspace.ID, 1002)
	if err != nil {
		t.Fatalf("attach workspace: %v", err)
	}
	if attached.AttachedVM == nil || *attached.AttachedVM != 1002 {
		t.Fatalf("expected workspace attached to 1002, got %+v", attached.AttachedVM)
	}
}

func TestWorkspaceAttachExpiredLeaseAllowed(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))

	now := time.Now().UTC()
	if err := store.CreateSandbox(ctx, models.Sandbox{
		VMID:          1002,
		Name:          "sandbox-1002",
		Profile:       "default",
		State:         models.SandboxRequested,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	workspace := models.Workspace{
		ID:           "ws-leased",
		Name:         "leased",
		Storage:      "local-zfs",
		VolumeID:     "local-zfs:vm-0-disk-1",
		SizeGB:       10,
		LeaseOwner:   workspaceLeaseOwnerForSandbox(1001),
		LeaseNonce:   "nonce",
		LeaseExpires: now.Add(-1 * time.Minute),
		CreatedAt:    now,
		LastUpdated:  now,
	}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	attached, err := mgr.Attach(ctx, workspace.ID, 1002)
	if err != nil {
		t.Fatalf("attach workspace: %v", err)
	}
	if attached.AttachedVM == nil || *attached.AttachedVM != 1002 {
		t.Fatalf("expected workspace attached to 1002, got %+v", attached.AttachedVM)
	}
}

func TestWorkspaceAttachJobLeaseAllowedForBoundSandbox(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))

	now := time.Now().UTC()
	vmid := 1002
	if err := store.CreateSandbox(ctx, models.Sandbox{
		VMID:          vmid,
		Name:          "sandbox-1002",
		Profile:       "default",
		State:         models.SandboxRequested,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	// The orchestrator binds the job to its sandbox before it attaches the
	// workspace, so a job lease covers exactly that sandbox.
	if err := store.CreateJob(ctx, models.Job{
		ID:          "job-1",
		RepoURL:     "https://example.com/r.git",
		Ref:         "main",
		Profile:     "default",
		Status:      models.JobRunning,
		SandboxVMID: &vmid,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("create job: %v", err)
	}
	workspace := models.Workspace{
		ID:           "ws-leased",
		Name:         "leased",
		Storage:      "local-zfs",
		VolumeID:     "local-zfs:vm-0-disk-1",
		SizeGB:       10,
		LeaseOwner:   workspaceLeaseOwnerForJob("job-1"),
		LeaseNonce:   "nonce",
		LeaseExpires: now.Add(10 * time.Minute),
		CreatedAt:    now,
		LastUpdated:  now,
	}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	attached, err := mgr.Attach(ctx, workspace.ID, vmid)
	if err != nil {
		t.Fatalf("attach workspace: %v", err)
	}
	if attached.AttachedVM == nil || *attached.AttachedVM != vmid {
		t.Fatalf("expected workspace attached to %d, got %+v", vmid, attached.AttachedVM)
	}
}

func TestWorkspaceAttachJobLeaseHeldByOtherJob(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))

	now := time.Now().UTC()
	if err := store.CreateSandbox(ctx, models.Sandbox{
		VMID:          1002,
		Name:          "sandbox-1002",
		Profile:       "default",
		State:         models.SandboxRequested,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	// job-1 still holds the lease and is bound to another sandbox.
	other := 1001
	if err := store.CreateJob(ctx, models.Job{
		ID:          "job-1",
		RepoURL:     "https://example.com/r.git",
		Ref:         "main",
		Profile:     "default",
		Status:      models.JobRunning,
		SandboxVMID: &other,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("create job: %v", err)
	}
	workspace := models.Workspace{
		ID:           "ws-leased",
		Name:         "leased",
		Storage:      "local-zfs",
		VolumeID:     "local-zfs:vm-0-disk-1",
		SizeGB:       10,
		LeaseOwner:   workspaceLeaseOwnerForJob("job-1"),
		LeaseNonce:   "nonce",
		LeaseExpires: now.Add(10 * time.Minute),
		CreatedAt:    now,
		LastUpdated:  now,
	}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	_, err := mgr.Attach(ctx, workspace.ID, 1002)
	if !errors.Is(err, ErrWorkspaceLeaseHeld) {
		t.Fatalf("expected ErrWorkspaceLeaseHeld, got %v", err)
	}
}

func TestWorkspaceAttachSessionLeaseAllowedForBoundSandbox(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))

	now := time.Now().UTC()
	vmid := 1002
	if err := store.CreateSandbox(ctx, models.Sandbox{
		VMID:          vmid,
		Name:          "sandbox-1002",
		Profile:       "default",
		State:         models.SandboxRequested,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if err := store.CreateSession(ctx, models.Session{
		ID:          "sess-1",
		Name:        "sess",
		WorkspaceID: "ws-leased",
		Profile:     "default",
		CurrentVMID: &vmid,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	workspace := models.Workspace{
		ID:           "ws-leased",
		Name:         "leased",
		Storage:      "local-zfs",
		VolumeID:     "local-zfs:vm-0-disk-1",
		SizeGB:       10,
		LeaseOwner:   workspaceLeaseOwnerForSession("sess-1"),
		LeaseNonce:   "nonce",
		LeaseExpires: now.Add(10 * time.Minute),
		CreatedAt:    now,
		LastUpdated:  now,
	}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	attached, err := mgr.Attach(ctx, workspace.ID, vmid)
	if err != nil {
		t.Fatalf("attach workspace: %v", err)
	}
	if attached.AttachedVM == nil || *attached.AttachedVM != vmid {
		t.Fatalf("expected workspace attached to %d, got %+v", vmid, attached.AttachedVM)
	}
}

func TestWorkspaceAttachSessionLeaseAllowedForSessionJob(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))

	now := time.Now().UTC()
	vmid := 1002
	if err := store.CreateSandbox(ctx, models.Sandbox{
		VMID:          vmid,
		Name:          "sandbox-1002",
		Profile:       "default",
		State:         models.SandboxRequested,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	// The session has no current sandbox yet. Its job already binds the
	// target sandbox, as in the provision-with-job flow.
	session := "sess-1"
	if err := store.CreateSession(ctx, models.Session{
		ID:          session,
		Name:        "sess",
		WorkspaceID: "ws-leased",
		Profile:     "default",
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.CreateJob(ctx, models.Job{
		ID:          "job-1",
		RepoURL:     "https://example.com/r.git",
		Ref:         "main",
		Profile:     "default",
		Status:      models.JobRunning,
		SandboxVMID: &vmid,
		SessionID:   &session,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("create job: %v", err)
	}
	workspace := models.Workspace{
		ID:           "ws-leased",
		Name:         "leased",
		Storage:      "local-zfs",
		VolumeID:     "local-zfs:vm-0-disk-1",
		SizeGB:       10,
		LeaseOwner:   workspaceLeaseOwnerForSession(session),
		LeaseNonce:   "nonce",
		LeaseExpires: now.Add(10 * time.Minute),
		CreatedAt:    now,
		LastUpdated:  now,
	}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	attached, err := mgr.Attach(ctx, workspace.ID, vmid)
	if err != nil {
		t.Fatalf("attach workspace: %v", err)
	}
	if attached.AttachedVM == nil || *attached.AttachedVM != vmid {
		t.Fatalf("expected workspace attached to %d, got %+v", vmid, attached.AttachedVM)
	}
}

func TestWorkspaceAttachSessionLeaseHeldByOtherSession(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))

	now := time.Now().UTC()
	if err := store.CreateSandbox(ctx, models.Sandbox{
		VMID:          1002,
		Name:          "sandbox-1002",
		Profile:       "default",
		State:         models.SandboxRequested,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	// The leasing session points at another sandbox: a stopped session keeps
	// its lease so no other sandbox can take the workspace meanwhile.
	other := 1001
	if err := store.CreateSession(ctx, models.Session{
		ID:          "sess-1",
		Name:        "sess",
		WorkspaceID: "ws-leased",
		Profile:     "default",
		CurrentVMID: &other,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	workspace := models.Workspace{
		ID:           "ws-leased",
		Name:         "leased",
		Storage:      "local-zfs",
		VolumeID:     "local-zfs:vm-0-disk-1",
		SizeGB:       10,
		LeaseOwner:   workspaceLeaseOwnerForSession("sess-1"),
		LeaseNonce:   "nonce",
		LeaseExpires: now.Add(10 * time.Minute),
		CreatedAt:    now,
		LastUpdated:  now,
	}
	if err := store.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	_, err := mgr.Attach(ctx, workspace.ID, 1002)
	if !errors.Is(err, ErrWorkspaceLeaseHeld) {
		t.Fatalf("expected ErrWorkspaceLeaseHeld, got %v", err)
	}
	updated, err := store.GetWorkspace(ctx, workspace.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if updated.AttachedVM != nil {
		t.Fatalf("expected workspace to stay detached, got attached_vmid=%d", *updated.AttachedVM)
	}
}

func TestWorkspaceAttachMaintenanceLeaseHeld(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))

	now := time.Now().UTC()
	if err := store.CreateSandbox(ctx, models.Sandbox{
		VMID:          1002,
		Name:          "sandbox-1002",
		Profile:       "default",
		State:         models.SandboxRequested,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	// Fork, fsck, and snapshot holds lease the workspace precisely to keep
	// attachers out; an unrecognized owner fails closed. A session lease
	// whose session row is gone fails closed the same way.
	for i, owner := range []string{"fork:ws-leased:1", "session:gone"} {
		workspace := models.Workspace{
			ID:           "ws-held-" + strconv.Itoa(i),
			Name:         "held-" + strconv.Itoa(i),
			Storage:      "local-zfs",
			VolumeID:     "local-zfs:vm-0-disk-1",
			SizeGB:       10,
			LeaseOwner:   owner,
			LeaseNonce:   "nonce",
			LeaseExpires: now.Add(10 * time.Minute),
			CreatedAt:    now,
			LastUpdated:  now,
		}
		if err := store.CreateWorkspace(ctx, workspace); err != nil {
			t.Fatalf("create workspace: %v", err)
		}

		_, err := mgr.Attach(ctx, workspace.ID, 1002)
		if !errors.Is(err, ErrWorkspaceLeaseHeld) {
			t.Fatalf("owner %q: expected ErrWorkspaceLeaseHeld, got %v", owner, err)
		}
	}
}

func TestWorkspaceLeaseSandboxOwner(t *testing.T) {
	for _, tc := range []struct {
		owner string
		vmid  int
		ok    bool
	}{
		{owner: "sandbox:1001", vmid: 1001, ok: true},
		{owner: "sandbox:0", ok: false},
		{owner: "sandbox:-1", ok: false},
		{owner: "sandbox:abc", ok: false},
		{owner: "sandbox:1001:extra", ok: false},
		{owner: "job:job-1", ok: false},
		{owner: "session:s-1", ok: false},
		{owner: "", ok: false},
	} {
		vmid, ok := workspaceLeaseSandboxOwner(tc.owner)
		if ok != tc.ok || (ok && vmid != tc.vmid) {
			t.Fatalf("owner %q: expected (%d, %v), got (%d, %v)", tc.owner, tc.vmid, tc.ok, vmid, ok)
		}
	}
}
