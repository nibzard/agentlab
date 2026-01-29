package daemon

import (
	"context"
	"errors"
	"io"
	"log"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

type stubBackend struct {
	stopErr    error
	destroyErr error
}

func (s *stubBackend) Clone(context.Context, proxmox.VMID, proxmox.VMID, string) error {
	return nil
}

func (s *stubBackend) Configure(context.Context, proxmox.VMID, proxmox.VMConfig) error {
	return nil
}

func (s *stubBackend) Start(context.Context, proxmox.VMID) error {
	return nil
}

func (s *stubBackend) Stop(context.Context, proxmox.VMID) error {
	return s.stopErr
}

func (s *stubBackend) Destroy(context.Context, proxmox.VMID) error {
	return s.destroyErr
}

func (s *stubBackend) Status(context.Context, proxmox.VMID) (proxmox.Status, error) {
	return proxmox.StatusUnknown, nil
}

func (s *stubBackend) GuestIP(context.Context, proxmox.VMID) (string, error) {
	return "", nil
}

func TestSandboxTransitions(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	mgr := NewSandboxManager(store, nil, log.New(io.Discard, "", 0))

	sandbox := models.Sandbox{
		VMID:      100,
		Name:      "test-sb",
		Profile:   "default",
		State:     models.SandboxRequested,
		Keepalive: false,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if err := mgr.Transition(ctx, sandbox.VMID, models.SandboxProvisioning); err != nil {
		t.Fatalf("transition to provisioning: %v", err)
	}
	if err := mgr.Transition(ctx, sandbox.VMID, models.SandboxRunning); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected invalid transition error, got %v", err)
	}
}

func TestSandboxLeaseRenewal(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	mgr := NewSandboxManager(store, nil, log.New(io.Discard, "", 0))
	base := time.Date(2026, 1, 29, 10, 0, 0, 0, time.UTC)
	mgr.now = func() time.Time { return base }

	sandbox := models.Sandbox{
		VMID:         101,
		Name:         "keepalive",
		Profile:      "default",
		State:        models.SandboxReady,
		Keepalive:    true,
		LeaseExpires: base.Add(30 * time.Minute),
		CreatedAt:    base,
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	expiresAt, err := mgr.RenewLease(ctx, sandbox.VMID, 2*time.Hour)
	if err != nil {
		t.Fatalf("renew lease: %v", err)
	}
	expected := base.Add(2 * time.Hour)
	if !expiresAt.Equal(expected) {
		t.Fatalf("expected expiry %s, got %s", expected, expiresAt)
	}
}

func TestSandboxLeaseGC(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	base := time.Date(2026, 1, 29, 11, 0, 0, 0, time.UTC)
	mgr.now = func() time.Time { return base }

	sandbox := models.Sandbox{
		VMID:         102,
		Name:         "expired",
		Profile:      "default",
		State:        models.SandboxRunning,
		Keepalive:    false,
		LeaseExpires: base.Add(-1 * time.Minute),
		CreatedAt:    base.Add(-2 * time.Hour),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	mgr.runLeaseGC(ctx)

	updated, err := store.GetSandbox(ctx, sandbox.VMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if updated.State != models.SandboxDestroyed {
		t.Fatalf("expected destroyed, got %s", updated.State)
	}
}

func newTestStore(t *testing.T) *db.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agentlab.db")
	store, err := db.Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}
