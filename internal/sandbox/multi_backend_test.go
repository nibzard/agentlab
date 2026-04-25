package sandbox

import (
	"context"
	"testing"
)

// mockBackend is a simple mock for testing MultiBackend routing.
type mockBackend struct {
	typ     Type
	created bool
	started bool
	stopped bool
}

func (m *mockBackend) SandboxType() Type                                    { return m.typ }
func (m *mockBackend) Capabilities() Capabilities                          { return Capabilities{} }
func (m *mockBackend) Create(_ context.Context, cfg CreateConfig) error    { m.created = true; return nil }
func (m *mockBackend) Start(_ context.Context, id int) error              { m.started = true; return nil }
func (m *mockBackend) Stop(_ context.Context, id int) error               { m.stopped = true; return nil }
func (m *mockBackend) Suspend(_ context.Context, id int) error             { return nil }
func (m *mockBackend) Resume(_ context.Context, id int) error              { return nil }
func (m *mockBackend) Destroy(_ context.Context, id int) error             { return nil }
func (m *mockBackend) Status(_ context.Context, id int) (Status, error)   { return StatusRunning, nil }
func (m *mockBackend) GuestIP(_ context.Context, id int) (string, error)   { return "10.77.0.5", nil }
func (m *mockBackend) List(_ context.Context) ([]ContainerSummary, error)  { return nil, nil }
func (m *mockBackend) CurrentStats(_ context.Context, id int) (ContainerStats, error) { return ContainerStats{}, nil }
func (m *mockBackend) ValidateTemplate(_ context.Context, s string) error  { return nil }
func (m *mockBackend) SnapshotCreate(_ context.Context, id int, n string) error { return nil }
func (m *mockBackend) SnapshotRollback(_ context.Context, id int, n string) error { return nil }
func (m *mockBackend) SnapshotDelete(_ context.Context, id int, n string) error { return nil }
func (m *mockBackend) SnapshotList(_ context.Context, id int) ([]Snapshot, error) { return nil, nil }

func TestMultiBackendRouting(t *testing.T) {
	vm := &mockBackend{typ: TypeVM}
	lxc := &mockBackend{typ: TypeLXC}

	// Type function: VMIDs < 2000 are VMs, >= 2000 are LXC
	mb := NewMultiBackend(func(id int) Type {
		if id >= 2000 {
			return TypeLXC
		}
		return TypeVM
	})
	mb.Register(TypeVM, vm)
	mb.Register(TypeLXC, lxc)

	// Create with VM ID
	if err := mb.Create(nil, CreateConfig{ID: 1000, Name: "test-vm"}); err != nil {
		t.Fatalf("Create VM: %v", err)
	}
	if !vm.created {
		t.Error("VM backend should have been called for ID 1000")
	}
	if lxc.created {
		t.Error("LXC backend should NOT have been called for ID 1000")
	}

	// Start with LXC ID
	if err := mb.Start(nil, 2001); err != nil {
		t.Fatalf("Start LXC: %v", err)
	}
	if !lxc.started {
		t.Error("LXC backend should have been called for ID 2001")
	}
	if vm.started {
		t.Error("VM backend should NOT have been called for ID 2001")
	}

	// Stop with VM ID
	if err := mb.Stop(nil, 1000); err != nil {
		t.Fatalf("Stop VM: %v", err)
	}
	if !vm.stopped {
		t.Error("VM backend should have been called for Stop")
	}
}

func TestMultiBackendBackend(t *testing.T) {
	mb := NewMultiBackend(nil)
	vm := &mockBackend{typ: TypeVM}
	mb.Register(TypeVM, vm)

	b, ok := mb.Backend(TypeVM)
	if !ok {
		t.Fatal("expected VM backend to be found")
	}
	if b.SandboxType() != TypeVM {
		t.Errorf("wrong backend type: %q", b.SandboxType())
	}

	_, ok = mb.Backend(TypeLXC)
	if ok {
		t.Error("LXC backend should not be registered")
	}
}
