package sandbox

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// MultiBackend routes sandbox operations to the correct backend based on sandbox type.
//
// ABOUTME: This backend maintains a registry of typed backends (VM, LXC, etc.) and
// dispatches each operation to the appropriate one. It resolves the backend for a
// sandbox ID by consulting a type lookup function.
//
// ABOUTME: The MultiBackend is the primary backend used by the daemon when both
// VM and LXC sandboxes are available. It allows mixing sandbox types on the same
// Proxmox node.
type MultiBackend struct {
	mu       sync.RWMutex
	backends map[Type]Backend
	typeFn   func(id int) Type // Resolves sandbox type from ID
}

// NewMultiBackend creates a new multi-backend router.
// The typeFn callback is used to determine which backend to use for a given sandbox ID.
func NewMultiBackend(typeFn func(id int) Type) *MultiBackend {
	if typeFn == nil {
		typeFn = func(_ int) Type { return TypeVM } // Default to VM
	}
	return &MultiBackend{
		backends: make(map[Type]Backend),
		typeFn:   typeFn,
	}
}

// Register adds a backend for a given sandbox type.
func (m *MultiBackend) Register(typ Type, backend Backend) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.backends[typ] = backend
}

// Backend returns the registered backend for the given type.
func (m *MultiBackend) Backend(typ Type) (Backend, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.backends[typ]
	return b, ok
}

func (m *MultiBackend) resolve(id int) (Backend, error) {
	typ := m.typeFn(id)
	m.mu.RLock()
	b, ok := m.backends[typ]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no backend registered for sandbox type %q", typ)
	}
	return b, nil
}

func (m *MultiBackend) SandboxType() Type {
	return TypeVM // MultiBackend doesn't have a single type
}

func (m *MultiBackend) Capabilities() Capabilities {
	return Capabilities{
		Snapshots:      true,
		Suspend:        true,
		WorkspaceMount: true,
		Firewall:       true,
	}
}

func (m *MultiBackend) Create(ctx context.Context, cfg CreateConfig) error {
	b, err := m.resolve(cfg.ID)
	if err != nil {
		return err
	}
	return b.Create(ctx, cfg)
}

func (m *MultiBackend) Start(ctx context.Context, id int) error {
	b, err := m.resolve(id)
	if err != nil {
		return err
	}
	return b.Start(ctx, id)
}

func (m *MultiBackend) Stop(ctx context.Context, id int) error {
	b, err := m.resolve(id)
	if err != nil {
		return err
	}
	return b.Stop(ctx, id)
}

func (m *MultiBackend) Suspend(ctx context.Context, id int) error {
	b, err := m.resolve(id)
	if err != nil {
		return err
	}
	return b.Suspend(ctx, id)
}

func (m *MultiBackend) Resume(ctx context.Context, id int) error {
	b, err := m.resolve(id)
	if err != nil {
		return err
	}
	return b.Resume(ctx, id)
}

func (m *MultiBackend) Destroy(ctx context.Context, id int) error {
	b, err := m.resolve(id)
	if err != nil {
		return err
	}
	return b.Destroy(ctx, id)
}

func (m *MultiBackend) Status(ctx context.Context, id int) (Status, error) {
	b, err := m.resolve(id)
	if err != nil {
		return StatusUnknown, err
	}
	return b.Status(ctx, id)
}

func (m *MultiBackend) GuestIP(ctx context.Context, id int) (string, error) {
	b, err := m.resolve(id)
	if err != nil {
		return "", err
	}
	return b.GuestIP(ctx, id)
}

func (m *MultiBackend) List(ctx context.Context) ([]ContainerSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var all []ContainerSummary
	for _, b := range m.backends {
		list, err := b.List(ctx)
		if err != nil {
			return nil, err
		}
		all = append(all, list...)
	}
	return all, nil
}

func (m *MultiBackend) CurrentStats(ctx context.Context, id int) (ContainerStats, error) {
	b, err := m.resolve(id)
	if err != nil {
		return ContainerStats{}, err
	}
	return b.CurrentStats(ctx, id)
}

func (m *MultiBackend) ValidateTemplate(ctx context.Context, templateOrImage string) error {
	return nil // Delegated to specific backends
}

func (m *MultiBackend) SnapshotCreate(ctx context.Context, id int, name string) error {
	b, err := m.resolve(id)
	if err != nil {
		return err
	}
	return b.SnapshotCreate(ctx, id, name)
}

func (m *MultiBackend) SnapshotRollback(ctx context.Context, id int, name string) error {
	b, err := m.resolve(id)
	if err != nil {
		return err
	}
	return b.SnapshotRollback(ctx, id, name)
}

func (m *MultiBackend) SnapshotDelete(ctx context.Context, id int, name string) error {
	b, err := m.resolve(id)
	if err != nil {
		return err
	}
	return b.SnapshotDelete(ctx, id, name)
}

func (m *MultiBackend) SnapshotList(ctx context.Context, id int) ([]Snapshot, error) {
	b, err := m.resolve(id)
	if err != nil {
		return nil, err
	}
	return b.SnapshotList(ctx, id)
}

// Compile-time check that MultiBackend implements Backend.
var _ Backend = (*MultiBackend)(nil)

// IsContainerNotFound checks if an error indicates the container was not found.
func IsContainerNotFound(err error) bool {
	return errors.Is(err, ErrContainerNotFound)
}
