package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/proxmox"
	"github.com/agentlab/agentlab/internal/sandbox"
)

// fakeSandboxBackend is a mock sandbox.Backend for testing the adapter.
type fakeSandboxBackend struct {
	started    []int
	stopped    []int
	destroyed  []int
	suspended  []int
	resumed    []int
	statusMap  map[int]sandbox.Status // id -> status
	ipMap      map[int]string         // id -> ip
	snapshots  map[int][]sandbox.Snapshot
	statsMap   map[int]sandbox.ContainerStats
	listResult []sandbox.ContainerSummary
	validateOK bool
	validateFn func(string) error
	createErr  error
}

func (f *fakeSandboxBackend) SandboxType() sandbox.Type { return sandbox.TypeDocker }
func (f *fakeSandboxBackend) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{Snapshots: true, Suspend: true}
}
func (f *fakeSandboxBackend) Create(_ context.Context, cfg sandbox.CreateConfig) error {
	return f.createErr
}
func (f *fakeSandboxBackend) Start(_ context.Context, id int) error {
	f.started = append(f.started, id)
	return nil
}
func (f *fakeSandboxBackend) Stop(_ context.Context, id int) error {
	f.stopped = append(f.stopped, id)
	return nil
}
func (f *fakeSandboxBackend) Suspend(_ context.Context, id int) error {
	f.suspended = append(f.suspended, id)
	return nil
}
func (f *fakeSandboxBackend) Resume(_ context.Context, id int) error {
	f.resumed = append(f.resumed, id)
	return nil
}
func (f *fakeSandboxBackend) Destroy(_ context.Context, id int) error {
	f.destroyed = append(f.destroyed, id)
	return nil
}
func (f *fakeSandboxBackend) Status(_ context.Context, id int) (sandbox.Status, error) {
	if s, ok := f.statusMap[id]; ok {
		return s, nil
	}
	return sandbox.StatusUnknown, sandbox.ErrContainerNotFound
}
func (f *fakeSandboxBackend) GuestIP(_ context.Context, id int) (string, error) {
	if ip, ok := f.ipMap[id]; ok {
		return ip, nil
	}
	return "", sandbox.ErrGuestIPNotFound
}
func (f *fakeSandboxBackend) List(_ context.Context) ([]sandbox.ContainerSummary, error) {
	return f.listResult, nil
}
func (f *fakeSandboxBackend) CurrentStats(_ context.Context, id int) (sandbox.ContainerStats, error) {
	if s, ok := f.statsMap[id]; ok {
		return s, nil
	}
	return sandbox.ContainerStats{}, nil
}
func (f *fakeSandboxBackend) ValidateTemplate(_ context.Context, t string) error {
	if f.validateFn != nil {
		return f.validateFn(t)
	}
	if f.validateOK {
		return nil
	}
	return errors.New("not found")
}
func (f *fakeSandboxBackend) SnapshotCreate(_ context.Context, id int, name string) error {
	return nil
}
func (f *fakeSandboxBackend) SnapshotRollback(_ context.Context, id int, name string) error {
	return nil
}
func (f *fakeSandboxBackend) SnapshotDelete(_ context.Context, id int, name string) error {
	return nil
}
func (f *fakeSandboxBackend) SnapshotList(_ context.Context, id int) ([]sandbox.Snapshot, error) {
	if snaps, ok := f.snapshots[id]; ok {
		return snaps, nil
	}
	return nil, nil
}

func TestSandboxBackendAdapter_LifecycleOps(t *testing.T) {
	fake := &fakeSandboxBackend{}
	adapter := newSandboxBackendAdapter(fake)

	ctx := context.Background()

	// Test Start
	if err := adapter.Start(ctx, 42); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(fake.started) != 1 || fake.started[0] != 42 {
		t.Fatalf("Start() delegated id=%v, want [42]", fake.started)
	}

	// Test Stop
	if err := adapter.Stop(ctx, 42); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if len(fake.stopped) != 1 || fake.stopped[0] != 42 {
		t.Fatalf("Stop() delegated id=%v, want [42]", fake.stopped)
	}

	// Test Suspend
	if err := adapter.Suspend(ctx, 42); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}
	if len(fake.suspended) != 1 || fake.suspended[0] != 42 {
		t.Fatalf("Suspend() delegated id=%v, want [42]", fake.suspended)
	}

	// Test Resume
	if err := adapter.Resume(ctx, 42); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if len(fake.resumed) != 1 || fake.resumed[0] != 42 {
		t.Fatalf("Resume() delegated id=%v, want [42]", fake.resumed)
	}

	// Test Destroy
	if err := adapter.Destroy(ctx, 42); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if len(fake.destroyed) != 1 || fake.destroyed[0] != 42 {
		t.Fatalf("Destroy() delegated id=%v, want [42]", fake.destroyed)
	}
}

func TestSandboxBackendAdapter_StatusTranslation(t *testing.T) {
	fake := &fakeSandboxBackend{
		statusMap: map[int]sandbox.Status{
			1: sandbox.StatusRunning,
			2: sandbox.StatusStopped,
			3: sandbox.StatusUnknown,
		},
	}
	adapter := newSandboxBackendAdapter(fake)
	ctx := context.Background()

	tests := []struct {
		id   int
		want proxmox.Status
	}{
		{1, proxmox.StatusRunning},
		{2, proxmox.StatusStopped},
		{3, proxmox.StatusUnknown},
	}
	for _, tt := range tests {
		got, err := adapter.Status(ctx, proxmox.VMID(tt.id))
		if err != nil {
			t.Errorf("Status(%d) error = %v", tt.id, err)
		}
		if got != tt.want {
			t.Errorf("Status(%d) = %q, want %q", tt.id, got, tt.want)
		}
	}

	// Unknown VM should map ErrContainerNotFound -> ErrVMNotFound
	_, err := adapter.Status(ctx, 99)
	if !errors.Is(err, proxmox.ErrVMNotFound) {
		t.Errorf("Status(99) error = %v, want ErrVMNotFound", err)
	}
}

func TestSandboxBackendAdapter_GuestIP(t *testing.T) {
	fake := &fakeSandboxBackend{
		ipMap: map[int]string{1: "10.0.0.5"},
	}
	adapter := newSandboxBackendAdapter(fake)
	ctx := context.Background()

	ip, err := adapter.GuestIP(ctx, 1)
	if err != nil {
		t.Fatalf("GuestIP(1) error = %v", err)
	}
	if ip != "10.0.0.5" {
		t.Fatalf("GuestIP(1) = %q, want %q", ip, "10.0.0.5")
	}

	// Unknown VM returns ErrGuestIPNotFound (same as proxmox.Backend behavior)
	_, err = adapter.GuestIP(ctx, 99)
	if !errors.Is(err, sandbox.ErrGuestIPNotFound) {
		t.Errorf("GuestIP(99) error = %v, want ErrGuestIPNotFound", err)
	}
}

func TestSandboxBackendAdapter_ListVMs(t *testing.T) {
	fake := &fakeSandboxBackend{
		listResult: []sandbox.ContainerSummary{
			{ID: 1, Name: "agentlab-1", Status: sandbox.StatusRunning, Type: sandbox.TypeDocker},
			{ID: 2, Name: "agentlab-2", Status: sandbox.StatusStopped, Type: sandbox.TypeDocker},
		},
	}
	adapter := newSandboxBackendAdapter(fake)
	ctx := context.Background()

	vms, err := adapter.ListVMs(ctx)
	if err != nil {
		t.Fatalf("ListVMs() error = %v", err)
	}
	if len(vms) != 2 {
		t.Fatalf("ListVMs() returned %d items, want 2", len(vms))
	}
	if vms[0].VMID != 1 || vms[0].Status != proxmox.StatusRunning {
		t.Errorf("ListVMs()[0] = %+v, want VMID=1 Status=running", vms[0])
	}
	if vms[1].VMID != 2 || vms[1].Status != proxmox.StatusStopped {
		t.Errorf("ListVMs()[1] = %+v, want VMID=2 Status=stopped", vms[1])
	}
}

func TestSandboxBackendAdapter_SnapshotOps(t *testing.T) {
	fake := &fakeSandboxBackend{
		snapshots: map[int][]sandbox.Snapshot{
			1: {{Name: "snap1", Description: "test", CreatedAt: time.Now()}},
		},
	}
	adapter := newSandboxBackendAdapter(fake)
	ctx := context.Background()

	if err := adapter.SnapshotCreate(ctx, 1, "snap1"); err != nil {
		t.Fatalf("SnapshotCreate() error = %v", err)
	}
	if err := adapter.SnapshotRollback(ctx, 1, "snap1"); err != nil {
		t.Fatalf("SnapshotRollback() error = %v", err)
	}
	if err := adapter.SnapshotDelete(ctx, 1, "snap1"); err != nil {
		t.Fatalf("SnapshotDelete() error = %v", err)
	}

	snaps, err := adapter.SnapshotList(ctx, 1)
	if err != nil {
		t.Fatalf("SnapshotList() error = %v", err)
	}
	if len(snaps) != 1 || snaps[0].Name != "snap1" {
		t.Fatalf("SnapshotList() = %+v, want snap1", snaps)
	}
}

func TestSandboxBackendAdapter_UnsupportedOps(t *testing.T) {
	fake := &fakeSandboxBackend{}
	adapter := newSandboxBackendAdapter(fake)
	ctx := context.Background()

	unsupportedOps := []struct {
		name string
		fn   func() error
	}{
		{"create_volume", func() error {
			_, err := adapter.CreateVolume(ctx, "store", "vol", 10)
			return err
		}},
		{"attach_volume", func() error {
			return adapter.AttachVolume(ctx, 1, "vol", "scsi1")
		}},
		{"detach_volume", func() error {
			return adapter.DetachVolume(ctx, 1, "scsi1")
		}},
		{"delete_volume", func() error {
			return adapter.DeleteVolume(ctx, "vol")
		}},
		{"volume_info", func() error {
			_, err := adapter.VolumeInfo(ctx, "vol")
			return err
		}},
		{"volume_snapshot_create", func() error {
			return adapter.VolumeSnapshotCreate(ctx, "vol", "snap")
		}},
		{"volume_snapshot_restore", func() error {
			return adapter.VolumeSnapshotRestore(ctx, "vol", "snap")
		}},
		{"volume_snapshot_delete", func() error {
			return adapter.VolumeSnapshotDelete(ctx, "vol", "snap")
		}},
		{"volume_clone", func() error {
			return adapter.VolumeClone(ctx, "src", "dst")
		}},
		{"volume_clone_from_snapshot", func() error {
			return adapter.VolumeCloneFromSnapshot(ctx, "src", "snap", "dst")
		}},
		{"vm_config", func() error {
			_, err := adapter.VMConfig(ctx, 1)
			return err
		}},
	}

	for _, op := range unsupportedOps {
		err := op.fn()
		if err == nil {
			t.Errorf("%s: expected error for unsupported operation, got nil", op.name)
		}
		var unsupported errUnsupportedBackend
		if !errors.As(err, &unsupported) {
			t.Errorf("%s: error = %v (%T), want errUnsupportedBackend", op.name, err, err)
		}
	}
}

func TestSandboxBackendAdapter_CloneAndConfigure(t *testing.T) {
	fake := &fakeSandboxBackend{}
	adapter := newSandboxBackendAdapter(fake)
	ctx := context.Background()

	// Clone delegates to Create
	if err := adapter.Clone(ctx, 100, 200, "test-sandbox"); err != nil {
		t.Fatalf("Clone() error = %v", err)
	}

	// Configure is a no-op
	if err := adapter.Configure(ctx, 200, proxmox.VMConfig{}); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
}

func TestSandboxBackendAdapter_CurrentStats(t *testing.T) {
	fake := &fakeSandboxBackend{
		statsMap: map[int]sandbox.ContainerStats{1: {CPUUsage: 0.5}},
	}
	adapter := newSandboxBackendAdapter(fake)
	ctx := context.Background()

	stats, err := adapter.CurrentStats(ctx, 1)
	if err != nil {
		t.Fatalf("CurrentStats() error = %v", err)
	}
	if stats.CPUUsage != 0.5 {
		t.Fatalf("CurrentStats().CPUUsage = %f, want 0.5", stats.CPUUsage)
	}
}

func TestSandboxBackendAdapter_ImplementsInterface(t *testing.T) {
	// Compile-time check that the adapter satisfies proxmox.Backend.
	var _ proxmox.Backend = newSandboxBackendAdapter(&fakeSandboxBackend{})
}
