package pool

import (
	"errors"
	"testing"
	"time"
)

func fixedNow(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestPool_AllocatePassthrough(t *testing.T) {
	// Zero config = pass-through mode, all allocations succeed.
	p := New(Config{})
	if p.IsEnabled() {
		t.Fatal("pool with zero config should not be enabled")
	}
	err := p.Allocate(1, "test", "default", 100, 65536, false)
	if err != nil {
		t.Fatalf("pass-through allocate should succeed: %v", err)
	}
}

func TestPool_AllocateWithinLimit(t *testing.T) {
	p := New(Config{TotalCores: 16, TotalMemoryMB: 32768})
	if !p.IsEnabled() {
		t.Fatal("pool should be enabled")
	}
	err := p.Allocate(1, "sb1", "default", 4, 8192, false)
	if err != nil {
		t.Fatalf("allocate within limit: %v", err)
	}
	status := p.Status()
	if status.AllocatedCores != 4 {
		t.Errorf("expected 4 allocated cores, got %d", status.AllocatedCores)
	}
	if status.AllocatedMemoryMB != 8192 {
		t.Errorf("expected 8192 allocated MB, got %d", status.AllocatedMemoryMB)
	}
	if status.ActiveCount != 1 {
		t.Errorf("expected 1 active, got %d", status.ActiveCount)
	}
}

func TestPool_AllocateOverCommit(t *testing.T) {
	p := New(Config{TotalCores: 8, TotalMemoryMB: 16384, CPUOverCommit: 2.0, MemoryOverCommit: 2.0})
	// Should be able to allocate 16 cores with 2x over-commit on 8 physical cores.
	err := p.Allocate(1, "sb1", "default", 8, 8192, false)
	if err != nil {
		t.Fatalf("first allocate: %v", err)
	}
	err = p.Allocate(2, "sb2", "default", 8, 8192, false)
	if err != nil {
		t.Fatalf("second allocate (within commit limit): %v", err)
	}
	// Third allocation at 8 cores would be 24 total, exceeding 16 commit limit.
	err = p.Allocate(3, "sb3", "default", 8, 8192, false)
	if err == nil {
		t.Fatal("expected error when exceeding commit limit")
	}
	if !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("expected ErrPoolExhausted, got: %v", err)
	}
}

func TestPool_AllocateBurst(t *testing.T) {
	p := New(Config{TotalCores: 8, TotalMemoryMB: 16384, CPUOverCommit: 1.0})
	// Fill up to commit limit.
	err := p.Allocate(1, "sb1", "default", 8, 8192, false)
	if err != nil {
		t.Fatalf("first allocate: %v", err)
	}
	// Non-burst should fail beyond commit limit.
	err = p.Allocate(2, "sb2", "default", 4, 4096, false)
	if err == nil {
		t.Fatal("expected error when exceeding commit limit without burst")
	}
	// Burst should succeed (up to 2x commit limit).
	err = p.Allocate(2, "sb2", "default", 4, 4096, true)
	if err != nil {
		t.Fatalf("burst allocate should succeed: %v", err)
	}
	status := p.Status()
	if status.BurstCount != 1 {
		t.Errorf("expected 1 burst allocation, got %d", status.BurstCount)
	}
}

func TestPool_Release(t *testing.T) {
	p := New(Config{TotalCores: 8, TotalMemoryMB: 16384})
	_ = p.Allocate(1, "sb1", "default", 4, 8192, false)
	_ = p.Allocate(2, "sb2", "default", 4, 8192, false)

	err := p.Release(1)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	status := p.Status()
	if status.ActiveCount != 1 {
		t.Errorf("expected 1 active after release, got %d", status.ActiveCount)
	}
	if status.AllocatedCores != 4 {
		t.Errorf("expected 4 cores after release, got %d", status.AllocatedCores)
	}

	err = p.Release(999)
	if !errors.Is(err, ErrAllocationNotFound) {
		t.Fatalf("expected ErrAllocationNotFound, got: %v", err)
	}
}

func TestPool_Update(t *testing.T) {
	p := New(Config{TotalCores: 16, TotalMemoryMB: 32768})
	_ = p.Allocate(1, "sb1", "default", 4, 8192, false)

	err := p.Update(1, 8, 16384)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	a, ok := p.Get(1)
	if !ok {
		t.Fatal("allocation not found")
	}
	if a.Cores != 8 {
		t.Errorf("expected 8 cores, got %d", a.Cores)
	}
	if a.MemoryMB != 16384 {
		t.Errorf("expected 16384 MB, got %d", a.MemoryMB)
	}

	err = p.Update(999, 4, 8192)
	if !errors.Is(err, ErrAllocationNotFound) {
		t.Fatalf("expected ErrAllocationNotFound for update, got: %v", err)
	}
}

func TestPool_StatusUtilization(t *testing.T) {
	p := New(Config{TotalCores: 16, TotalMemoryMB: 32768, CPUOverCommit: 1.0, MemoryOverCommit: 1.0})
	_ = p.Allocate(1, "sb1", "default", 8, 16384, false)

	status := p.Status()
	// 8/16 = 0.5
	if status.UtilizationCPU != 0.5 {
		t.Errorf("expected 0.5 CPU utilization, got %f", status.UtilizationCPU)
	}
	if status.UtilizationMemory != 0.5 {
		t.Errorf("expected 0.5 memory utilization, got %f", status.UtilizationMemory)
	}
	if status.AvailableCores != 8 {
		t.Errorf("expected 8 available cores, got %f", status.AvailableCores)
	}
	if status.AvailableMemoryMB != 16384 {
		t.Errorf("expected 16384 available MB, got %f", status.AvailableMemoryMB)
	}
}

func TestPool_CanAllocate(t *testing.T) {
	p := New(Config{TotalCores: 8, TotalMemoryMB: 16384})
	_ = p.Allocate(1, "sb1", "default", 8, 16384, false)

	err := p.CanAllocate(4, 4096, false)
	if err == nil {
		t.Fatal("expected CanAllocate to fail when pool is full")
	}
	if !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("expected ErrPoolExhausted, got: %v", err)
	}

	err = p.CanAllocate(4, 4096, true)
	if err != nil {
		t.Fatalf("expected CanAllocate burst to succeed: %v", err)
	}
}

func TestPool_ReclaimExpired(t *testing.T) {
	now := time.Now()
	p := New(Config{TotalCores: 8, TotalMemoryMB: 16384, BurstDuration: 5 * time.Minute})
	p.WithNow(fixedNow(now))

	// Normal allocation.
	_ = p.Allocate(1, "sb1", "default", 4, 8192, false)
	// Burst allocation.
	_ = p.Allocate(2, "sb2", "default", 8, 16384, true)

	status := p.Status()
	if status.ActiveCount != 2 {
		t.Fatalf("expected 2 active, got %d", status.ActiveCount)
	}

	// Advance time past burst duration.
	p.WithNow(fixedNow(now.Add(6 * time.Minute)))
	reclaimed := p.ReclaimExpired()
	if len(reclaimed) != 1 || reclaimed[0] != 2 {
		t.Fatalf("expected sandbox 2 reclaimed, got %v", reclaimed)
	}

	status = p.Status()
	if status.ActiveCount != 1 {
		t.Errorf("expected 1 active after reclaim, got %d", status.ActiveCount)
	}
}

func TestPool_ReallocateSameID(t *testing.T) {
	p := New(Config{TotalCores: 8, TotalMemoryMB: 16384})
	_ = p.Allocate(1, "sb1", "default", 4, 4096, false)

	// Re-allocate same sandbox with different resources.
	err := p.Allocate(1, "sb1-updated", "default", 6, 8192, false)
	if err != nil {
		t.Fatalf("re-allocate: %v", err)
	}
	status := p.Status()
	if status.ActiveCount != 1 {
		t.Errorf("expected 1 active after re-allocate, got %d", status.ActiveCount)
	}
	if status.AllocatedCores != 6 {
		t.Errorf("expected 6 cores, got %d", status.AllocatedCores)
	}
	a, _ := p.Get(1)
	if a.SandboxName != "sb1-updated" {
		t.Errorf("expected name sb1-updated, got %s", a.SandboxName)
	}
}

func TestPool_NilPool(t *testing.T) {
	var p *Pool
	if p.IsEnabled() {
		t.Fatal("nil pool should not be enabled")
	}
	if err := p.Allocate(1, "", "", 1, 1, false); err != nil {
		t.Fatalf("nil pool allocate should be no-op: %v", err)
	}
	if err := p.Release(1); err != nil {
		t.Fatalf("nil pool release should be no-op: %v", err)
	}
	if err := p.CanAllocate(1, 1, false); err != nil {
		t.Fatalf("nil pool CanAllocate should be no-op: %v", err)
	}
	s := p.Status()
	if s.ActiveCount != 0 {
		t.Errorf("nil pool status should have 0 active")
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := Config{TotalCores: 16, TotalMemoryMB: 32768}
	cfg.ApplyDefaults()
	if cfg.CPUOverCommit != DefaultCPUOverCommit {
		t.Errorf("expected default CPU over-commit %f, got %f", DefaultCPUOverCommit, cfg.CPUOverCommit)
	}
	if cfg.MemoryOverCommit != DefaultMemoryOverCommit {
		t.Errorf("expected default memory over-commit %f, got %f", DefaultMemoryOverCommit, cfg.MemoryOverCommit)
	}
}

func TestConfigCommitLimits(t *testing.T) {
	cfg := Config{TotalCores: 16, TotalMemoryMB: 32768, CPUOverCommit: 4.0, MemoryOverCommit: 2.0}
	if limit := cfg.CommitLimitCPU(); limit != 64 {
		t.Errorf("expected CPU limit 64, got %f", limit)
	}
	if limit := cfg.CommitLimitMemoryMB(); limit != 65536 {
		t.Errorf("expected memory limit 65536, got %f", limit)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{"valid", Config{TotalCores: 16, TotalMemoryMB: 32768}, false},
		{"negative cores", Config{TotalCores: -1}, true},
		{"negative memory", Config{TotalMemoryMB: -1}, true},
		{"negative cpu over-commit", Config{CPUOverCommit: -1}, true},
		{"negative mem over-commit", Config{MemoryOverCommit: -1}, true},
		{"zero values", Config{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
