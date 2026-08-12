package daemon

import (
	"context"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

// --- taskTracker ---

func TestTaskTracker_RegisterAndDrain(t *testing.T) {
	tr := &taskTracker{}
	done, ok := tr.register("a")
	if !ok {
		t.Fatal("expected register to succeed")
	}
	if c := tr.count(); c != 1 {
		t.Fatalf("count=%d want 1", c)
	}
	tr.closeRegistration()
	// After closeRegistration, new registration is refused so Wait cannot race
	// a later Add (review H2).
	if _, ok := tr.register("b"); ok {
		t.Fatal("expected register to be refused while draining")
	}
	done()
	if c := tr.count(); c != 0 {
		t.Fatalf("count=%d want 0", c)
	}
	if !tr.wait(time.Second) {
		t.Fatal("wait should drain immediately once work is done")
	}
	if !tr.isDraining() {
		t.Fatal("expected draining flag set")
	}
}

func TestTaskTracker_WaitTimeout(t *testing.T) {
	tr := &taskTracker{}
	done, ok := tr.register("long")
	if !ok {
		t.Fatal("expected register to succeed")
	}
	tr.closeRegistration()
	if tr.wait(20 * time.Millisecond) {
		t.Fatal("wait should time out while work is in flight")
	}
	done()
	// After the work finishes, a subsequent wait succeeds.
	if !tr.wait(time.Second) {
		t.Fatal("wait should drain after work completes")
	}
}

func TestTaskTracker_ConcurrentRegisterDone(t *testing.T) {
	// Run with -race: verifies no Add-versus-Wait race (review H2).
	tr := &taskTracker{}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if d, ok := tr.register("x"); ok {
					time.Sleep(time.Microsecond)
					d()
				}
			}
		}()
	}
	wg.Wait()
	tr.closeRegistration()
	if !tr.wait(2 * time.Second) {
		t.Fatal("wait should drain all concurrent work")
	}
	if c := tr.count(); c != 0 {
		t.Fatalf("count=%d want 0 after drain", c)
	}
}

// --- Service lifecycle ---

func TestService_GoTracksAndCancelsWork(t *testing.T) {
	s := &Service{}
	parent, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()
	s.lifecycleCtx, s.lifecycleCancel = context.WithCancel(parent)
	s.tasks = &taskTracker{}

	var finished int32
	started := make(chan struct{})
	if !s.Go("work", func(lc context.Context) {
		close(started)
		<-lc.Done() // block until shutdown cancels the lifecycle context
		atomic.StoreInt32(&finished, 1)
	}) {
		t.Fatal("Go returned false before shutdown")
	}
	<-started

	// Ordered shutdown: close registration, cancel lifecycle, wait for workers.
	s.tasks.closeRegistration()
	s.lifecycleCancel()
	if !s.tasks.wait(2 * time.Second) {
		t.Fatal("shutdown did not drain tracked work in time")
	}
	if atomic.LoadInt32(&finished) != 1 {
		t.Fatal("tracked work did not observe lifecycle cancellation")
	}
	// New work is refused once registration is closed.
	if s.Go("late", func(context.Context) {}) {
		t.Fatal("Go must refuse work after closeRegistration")
	}
}

func TestService_LifecycleContextCancelledAtShutdown(t *testing.T) {
	s := &Service{}
	parent, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()
	s.lifecycleCtx, s.lifecycleCancel = context.WithCancel(parent)
	s.tasks = &taskTracker{}
	if err := s.LifecycleContext().Err(); err != nil {
		t.Fatalf("lifecycle ctx unexpectedly done: %v", err)
	}
	s.lifecycleCancel()
	if err := s.LifecycleContext().Err(); err == nil {
		t.Fatal("lifecycle ctx should be cancelled after shutdown")
	}
}

func TestService_LifecycleContextFallback(t *testing.T) {
	s := &Service{} // no lifecycle established
	// With no lifecycle context set, the fallback is context.Background(), which
	// is never cancelled.
	if err := s.LifecycleContext().Err(); err != nil {
		t.Fatalf("fallback lifecycle ctx unexpectedly done: %v", err)
	}
	// Go with no tracker runs detached and returns true.
	ok := s.Go("detached", func(ctx context.Context) {
		if ctx == nil {
			t.Error("ctx is nil")
		}
	})
	if !ok {
		t.Fatal("Go should run detached when no tracker is configured")
	}
}

func TestDetachedRunner(t *testing.T) {
	r := DetachedRunner()
	if r.LifecycleContext() == nil {
		t.Fatal("LifecycleContext should not be nil")
	}
	done := make(chan struct{})
	if !r.Go("x", func(ctx context.Context) { close(done) }) {
		t.Fatal("DetachedRunner.Go should return true")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("DetachedRunner.Go did not run fn")
	}
}

// --- SweepStartupOrphans (each branch, review H2) ---

func seedOrphanSandbox(t *testing.T, store interface {
	CreateSandbox(context.Context, models.Sandbox) error
}, vmid int, state models.SandboxState, age time.Duration) {
	t.Helper()
	now := time.Now().UTC()
	created := now.Add(-age)
	if err := store.CreateSandbox(context.Background(), models.Sandbox{
		VMID:          vmid,
		Name:          "sb",
		Profile:       "default",
		State:         state,
		CreatedAt:     created,
		LastUpdatedAt: created,
	}); err != nil {
		t.Fatalf("seed sandbox %d: %v", vmid, err)
	}
}

func TestSweepStartupOrphans_Branches(t *testing.T) {
	grace := time.Hour

	t.Run("transient past grace with no VM is destroyed", func(t *testing.T) {
		store := newTestStore(t)
		seedOrphanSandbox(t, store, 3001, models.SandboxRequested, 2*grace)
		backend := &stubBackend{status: proxmox.StatusUnknown} // no live VM
		mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
		n, err := mgr.SweepStartupOrphans(context.Background(), grace)
		if err != nil {
			t.Fatalf("sweep: %v", err)
		}
		if n != 1 {
			t.Fatalf("destroyed %d, want 1", n)
		}
		sb, _ := store.GetSandbox(context.Background(), 3001)
		if sb.State != models.SandboxDestroyed {
			t.Fatalf("state=%s want DESTROYED", sb.State)
		}
	})

	t.Run("transient within grace is left alone", func(t *testing.T) {
		store := newTestStore(t)
		seedOrphanSandbox(t, store, 3002, models.SandboxProvisioning, time.Second)
		backend := &stubBackend{status: proxmox.StatusUnknown}
		mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
		n, err := mgr.SweepStartupOrphans(context.Background(), grace)
		if err != nil {
			t.Fatalf("sweep: %v", err)
		}
		if n != 0 {
			t.Fatalf("destroyed %d, want 0 (within grace)", n)
		}
		sb, _ := store.GetSandbox(context.Background(), 3002)
		if sb.State != models.SandboxProvisioning {
			t.Fatalf("state changed to %s, want PROVISIONING", sb.State)
		}
	})

	t.Run("transient past grace with running VM is adopted (not destroyed)", func(t *testing.T) {
		store := newTestStore(t)
		seedOrphanSandbox(t, store, 3003, models.SandboxBooting, 2*grace)
		backend := &stubBackend{status: proxmox.StatusRunning} // ownership proof: VM is alive
		mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
		n, err := mgr.SweepStartupOrphans(context.Background(), grace)
		if err != nil {
			t.Fatalf("sweep: %v", err)
		}
		if n != 0 {
			t.Fatalf("destroyed %d, want 0 (VM running)", n)
		}
	})

	t.Run("non-transient states are skipped", func(t *testing.T) {
		store := newTestStore(t)
		seedOrphanSandbox(t, store, 3004, models.SandboxRunning, 2*grace)
		seedOrphanSandbox(t, store, 3005, models.SandboxDestroyed, 2*grace)
		backend := &stubBackend{status: proxmox.StatusUnknown}
		mgr := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
		n, err := mgr.SweepStartupOrphans(context.Background(), grace)
		if err != nil {
			t.Fatalf("sweep: %v", err)
		}
		if n != 0 {
			t.Fatalf("destroyed %d, want 0 (no transient states)", n)
		}
	})

	t.Run("nil backend still destroys transient past-grace orphans", func(t *testing.T) {
		store := newTestStore(t)
		seedOrphanSandbox(t, store, 3006, models.SandboxRequested, 2*grace)
		mgr := NewSandboxManager(store, nil, log.New(io.Discard, "", 0))
		n, err := mgr.SweepStartupOrphans(context.Background(), grace)
		if err != nil {
			t.Fatalf("sweep: %v", err)
		}
		if n != 1 {
			t.Fatalf("destroyed %d, want 1", n)
		}
		sb, _ := store.GetSandbox(context.Background(), 3006)
		if sb.State != models.SandboxDestroyed {
			t.Fatalf("state=%s want DESTROYED", sb.State)
		}
	})
}
