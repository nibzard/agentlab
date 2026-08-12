// Package pool provides a resource pooling model for sandbox over-commit.
//
// ABOUTME: This package tracks CPU and RAM allocations across sandboxes,
// allowing over-commit ratios for density on self-hosted hardware.
// It enforces soft and hard limits, supports burst allocations with
// auto-reclaim, and provides a status endpoint for observability.
package pool

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrPoolExhausted indicates the pool cannot satisfy the requested allocation.
var ErrPoolExhausted = errors.New("resource pool exhausted")

// ErrAllocationNotFound indicates no active allocation matches the given sandbox ID.
var ErrAllocationNotFound = errors.New("allocation not found")

// Config defines the resource pool configuration.
//
// Total resources represent the physical capacity of the host node.
// Over-commit ratios allow allocating more than physical capacity
// (e.g., CPU over-commit of 4.0 means 4x the physical cores).
type Config struct {
	// TotalCores is the total number of physical CPU cores available.
	TotalCores int `yaml:"total_cores" json:"total_cores"`
	// TotalMemoryMB is the total physical RAM in megabytes.
	TotalMemoryMB int `yaml:"total_memory_mb" json:"total_memory_mb"`
	// CPUOverCommit is the ratio by which CPU can be over-committed (default 1.0).
	// A value of 4.0 means 4x the physical cores can be allocated.
	CPUOverCommit float64 `yaml:"cpu_over_commit" json:"cpu_over_commit"`
	// MemoryOverCommit is the ratio by which RAM can be over-committed (default 1.0).
	// A value of 2.0 means 2x the physical RAM can be allocated.
	MemoryOverCommit float64 `yaml:"memory_over_commit" json:"memory_over_commit"`
	// BurstDuration is how long a burst allocation is allowed to exceed
	// the hard limit before auto-reclaim triggers (default 0 = disabled).
	BurstDuration time.Duration `yaml:"burst_duration" json:"burst_duration,omitempty"`
}

// Defaults.
const (
	DefaultCPUOverCommit    = 1.0
	DefaultMemoryOverCommit = 1.0
)

// ApplyDefaults fills zero-valued fields with sensible defaults.
func (c *Config) ApplyDefaults() {
	if c.CPUOverCommit <= 0 {
		c.CPUOverCommit = DefaultCPUOverCommit
	}
	if c.MemoryOverCommit <= 0 {
		c.MemoryOverCommit = DefaultMemoryOverCommit
	}
}

// CommitLimitCPU returns the maximum allocatable CPU cores (total * over-commit).
func (c Config) CommitLimitCPU() float64 {
	return float64(c.TotalCores) * c.CPUOverCommit
}

// CommitLimitMemoryMB returns the maximum allocatable RAM in MB (total * over-commit).
func (c Config) CommitLimitMemoryMB() float64 {
	return float64(c.TotalMemoryMB) * c.MemoryOverCommit
}

// Validate checks the configuration for errors.
func (c Config) Validate() error {
	if c.TotalCores < 0 {
		return fmt.Errorf("total_cores must be non-negative")
	}
	if c.TotalMemoryMB < 0 {
		return fmt.Errorf("total_memory_mb must be non-negative")
	}
	if c.CPUOverCommit < 0 {
		return fmt.Errorf("cpu_over_commit must be non-negative")
	}
	if c.MemoryOverCommit < 0 {
		return fmt.Errorf("memory_over_commit must be non-negative")
	}
	return nil
}

// Allocation represents a resource allocation for a single sandbox.
type Allocation struct {
	// SandboxID is the unique identifier (VMID) of the sandbox.
	SandboxID int `json:"sandbox_id"`
	// SandboxName is the human-readable name of the sandbox.
	SandboxName string `json:"sandbox_name"`
	// Cores is the number of CPU cores allocated.
	Cores int `json:"cores"`
	// MemoryMB is the RAM allocated in megabytes.
	MemoryMB int `json:"memory_mb"`
	// Profile is the profile used to create the sandbox.
	Profile string `json:"profile"`
	// Burst indicates this allocation exceeds the soft limit.
	Burst bool `json:"burst"`
	// AllocatedAt is when the allocation was created.
	AllocatedAt time.Time `json:"allocated_at"`
	// ExpiresAt is when a burst allocation will be auto-reclaimed (zero if not burst).
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// Status represents the current state of the resource pool.
type Status struct {
	// Config is the active pool configuration.
	Config Config `json:"config"`
	// AllocatedCores is the sum of CPU cores across all active sandboxes.
	AllocatedCores int `json:"allocated_cores"`
	// AllocatedMemoryMB is the sum of RAM across all active sandboxes.
	AllocatedMemoryMB int `json:"allocated_memory_mb"`
	// AvailableCores is cores remaining within the commit limit.
	AvailableCores float64 `json:"available_cores"`
	// AvailableMemoryMB is RAM remaining within the commit limit.
	AvailableMemoryMB float64 `json:"available_memory_mb"`
	// UtilizationCPU is the fraction of the commit limit used (0.0-1.0+).
	UtilizationCPU float64 `json:"utilization_cpu"`
	// UtilizationMemory is the fraction of the commit limit used (0.0-1.0+).
	UtilizationMemory float64 `json:"utilization_memory"`
	// ActiveCount is the number of sandboxes with allocations.
	ActiveCount int `json:"active_count"`
	// BurstCount is the number of sandboxes in burst mode.
	BurstCount int `json:"burst_count"`
	// Allocations is the list of all active allocations.
	Allocations []Allocation `json:"allocations"`
}

// Pool tracks resource allocations across sandboxes with over-commit support.
//
// It is safe for concurrent use. All public methods are goroutine-safe.
type Pool struct {
	mu          sync.RWMutex
	config      Config
	allocations map[int]*Allocation // keyed by sandbox ID
	now         func() time.Time
}

// New creates a new resource pool with the given configuration.
// If cfg is zero-valued, the pool operates in pass-through mode
// (all allocations succeed, no limits enforced).
func New(cfg Config) *Pool {
	cfg.ApplyDefaults()
	return &Pool{
		config:      cfg,
		allocations: make(map[int]*Allocation),
		now:         time.Now,
	}
}

// WithNow sets the clock function for testing.
func (p *Pool) WithNow(fn func() time.Time) *Pool {
	if p == nil {
		return p
	}
	p.mu.Lock()
	p.now = fn
	p.mu.Unlock()
	return p
}

// IsEnabled returns true if the pool has actual capacity configured
// (non-zero total resources). A pool with zero total resources operates
// in pass-through mode: all allocations succeed without limit checks.
func (p *Pool) IsEnabled() bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.config.TotalCores > 0 || p.config.TotalMemoryMB > 0
}

// Allocate reserves resources for a sandbox.
//
// It checks whether the requested resources fit within the commit limits.
// If the pool has no capacity configured (pass-through mode), the allocation
// always succeeds.
//
// Returns ErrPoolExhausted if the allocation would exceed hard limits.
// The burst parameter allows the allocation to temporarily exceed the
// commit limit up to a hard ceiling (2x the commit limit).
func (p *Pool) Allocate(sandboxID int, sandboxName, profile string, cores, memoryMB int, burst bool) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	// Pass-through mode: no limits configured.
	if p.config.TotalCores == 0 && p.config.TotalMemoryMB == 0 {
		p.allocations[sandboxID] = &Allocation{
			SandboxID:   sandboxID,
			SandboxName: sandboxName,
			Cores:       cores,
			MemoryMB:    memoryMB,
			Profile:     profile,
			AllocatedAt: p.now(),
		}
		return nil
	}

	// Check for existing allocation (update case).
	existing := p.allocations[sandboxID]
	if existing != nil {
		// Re-allocation: release old first, then allocate new.
		delete(p.allocations, sandboxID)
	}

	// Calculate current usage.
	totalCores, totalMemory := p.totalsLocked()

	// Add the new allocation.
	newCores := totalCores + cores
	newMemory := totalMemory + memoryMB

	cpuLimit := p.config.CommitLimitCPU()
	memLimit := p.config.CommitLimitMemoryMB()

	// Hard limit is 2x commit limit for burst; commit limit for normal.
	cpuHardLimit := cpuLimit * 2
	memHardLimit := memLimit * 2
	if !burst {
		cpuHardLimit = cpuLimit
		memHardLimit = memLimit
	}

	if cpuLimit > 0 && float64(newCores) > cpuHardLimit {
		// Restore existing if there was one.
		if existing != nil {
			p.allocations[sandboxID] = existing
		}
		return fmt.Errorf("%w: cpu allocation %d cores exceeds %s limit of %.0f cores (used: %d, requested: %d)",
			ErrPoolExhausted, newCores, limitLabel(burst), cpuHardLimit, totalCores, cores)
	}
	if memLimit > 0 && float64(newMemory) > memHardLimit {
		if existing != nil {
			p.allocations[sandboxID] = existing
		}
		return fmt.Errorf("%w: memory allocation %d MB exceeds %s limit of %.0f MB (used: %d, requested: %d)",
			ErrPoolExhausted, newMemory, limitLabel(burst), memHardLimit, totalMemory, memoryMB)
	}

	now := p.now()
	alloc := &Allocation{
		SandboxID:   sandboxID,
		SandboxName: sandboxName,
		Cores:       cores,
		MemoryMB:    memoryMB,
		Profile:     profile,
		Burst:       burst && (float64(newCores) > cpuLimit || float64(newMemory) > memLimit),
		AllocatedAt: now,
	}
	if alloc.Burst && p.config.BurstDuration > 0 {
		alloc.ExpiresAt = now.Add(p.config.BurstDuration)
	}
	p.allocations[sandboxID] = alloc
	return nil
}

// Release frees resources previously allocated for a sandbox.
// Returns ErrAllocationNotFound if no allocation exists for sandboxID.
func (p *Pool) Release(sandboxID int) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.allocations[sandboxID]; !ok {
		return ErrAllocationNotFound
	}
	delete(p.allocations, sandboxID)
	return nil
}

// Update modifies an existing allocation's resource claim.
func (p *Pool) Update(sandboxID int, cores, memoryMB int) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	alloc, ok := p.allocations[sandboxID]
	if !ok {
		return ErrAllocationNotFound
	}
	alloc.Cores = cores
	alloc.MemoryMB = memoryMB
	return nil
}

// Get retrieves the allocation for a sandbox, if any.
func (p *Pool) Get(sandboxID int) (Allocation, bool) {
	if p == nil {
		return Allocation{}, false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	a, ok := p.allocations[sandboxID]
	if !ok {
		return Allocation{}, false
	}
	return *a, true
}

// Status returns the current pool utilization and all allocations.
func (p *Pool) Status() Status {
	if p == nil {
		return Status{}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	totalCores, totalMemory := p.totalsLocked()

	cpuLimit := p.config.CommitLimitCPU()
	memLimit := p.config.CommitLimitMemoryMB()

	var availCores, availMem float64
	if cpuLimit > 0 {
		availCores = cpuLimit - float64(totalCores)
		if availCores < 0 {
			availCores = 0
		}
	}
	if memLimit > 0 {
		availMem = memLimit - float64(totalMemory)
		if availMem < 0 {
			availMem = 0
		}
	}

	var utilCPU, utilMem float64
	if cpuLimit > 0 {
		utilCPU = float64(totalCores) / cpuLimit
	}
	if memLimit > 0 {
		utilMem = float64(totalMemory) / memLimit
	}

	burstCount := 0
	allocs := make([]Allocation, 0, len(p.allocations))
	for _, a := range p.allocations {
		allocs = append(allocs, *a)
		if a.Burst {
			burstCount++
		}
	}

	return Status{
		Config:            p.config,
		AllocatedCores:    totalCores,
		AllocatedMemoryMB: totalMemory,
		AvailableCores:    availCores,
		AvailableMemoryMB: availMem,
		UtilizationCPU:    utilCPU,
		UtilizationMemory: utilMem,
		ActiveCount:       len(p.allocations),
		BurstCount:        burstCount,
		Allocations:       allocs,
	}
}

// ReclaimExpired removes burst allocations that have exceeded their duration.
// Returns the sandbox IDs of reclaimed allocations.
func (p *Pool) ReclaimExpired() []int {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()
	var reclaimed []int
	for id, a := range p.allocations {
		if a.Burst && !a.ExpiresAt.IsZero() && now.After(a.ExpiresAt) {
			reclaimed = append(reclaimed, id)
			delete(p.allocations, id)
		}
	}
	return reclaimed
}

// PeekExpiredBurst returns the VMIDs of burst allocations whose expiry has
// passed, WITHOUT removing them. Callers that destroy the backing VM should
// Release(id) only after destruction succeeds, so accounting is not dropped
// before the VM is gone (review H3).
func (p *Pool) PeekExpiredBurst() []int {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	var expired []int
	for id, a := range p.allocations {
		if a.Burst && !a.ExpiresAt.IsZero() && now.After(a.ExpiresAt) {
			expired = append(expired, id)
		}
	}
	return expired
}

// CanAllocate checks whether an allocation would fit without actually reserving.
// Returns nil if the allocation can proceed, or an error describing why not.
func (p *Pool) CanAllocate(cores, memoryMB int, burst bool) error {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.config.TotalCores == 0 && p.config.TotalMemoryMB == 0 {
		return nil
	}

	totalCores, totalMemory := p.totalsLocked()
	newCores := totalCores + cores
	newMemory := totalMemory + memoryMB

	cpuLimit := p.config.CommitLimitCPU()
	memLimit := p.config.CommitLimitMemoryMB()

	cpuHardLimit := cpuLimit * 2
	memHardLimit := memLimit * 2
	if !burst {
		cpuHardLimit = cpuLimit
		memHardLimit = memLimit
	}

	if cpuLimit > 0 && float64(newCores) > cpuHardLimit {
		return fmt.Errorf("%w: cpu allocation %d cores would exceed %s limit of %.0f",
			ErrPoolExhausted, newCores, limitLabel(burst), cpuHardLimit)
	}
	if memLimit > 0 && float64(newMemory) > memHardLimit {
		return fmt.Errorf("%w: memory allocation %d MB would exceed %s limit of %.0f",
			ErrPoolExhausted, newMemory, limitLabel(burst), memHardLimit)
	}
	return nil
}

func (p *Pool) totalsLocked() (totalCores, totalMemory int) {
	for _, a := range p.allocations {
		totalCores += a.Cores
		totalMemory += a.MemoryMB
	}
	return
}

func limitLabel(burst bool) string {
	if burst {
		return "burst"
	}
	return "commit"
}
