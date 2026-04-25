// Package backend provides a pluggable backend registry for AgentLab sandboxes.
//
// ABOUTME: This package manages the registration and creation of sandbox backends
// (Proxmox VM, LXC, Docker, libvirt). Each backend type is registered with a factory
// function that creates the appropriate sandbox.Backend implementation from config.
package backend

import (
	"fmt"
	"sort"
	"sync"

	"github.com/agentlab/agentlab/internal/config"
	"github.com/agentlab/agentlab/internal/sandbox"
)

// Factory creates a sandbox.Backend from the given configuration.
type Factory func(cfg config.Config) (sandbox.Backend, error)

// Registry manages backend factories by sandbox type.
type Registry struct {
	mu        sync.RWMutex
	factories map[sandbox.Type]Factory
}

// NewRegistry creates a new backend registry.
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[sandbox.Type]Factory),
	}
}

// Register adds a factory for a backend type.
// It panics if a factory is already registered for the given type.
func (r *Registry) Register(typ sandbox.Type, factory Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[typ]; exists {
		panic(fmt.Sprintf("backend factory already registered for type %q", typ))
	}
	r.factories[typ] = factory
}

// Create instantiates a backend for the given type using the registered factory.
func (r *Registry) Create(typ sandbox.Type, cfg config.Config) (sandbox.Backend, error) {
	r.mu.RLock()
	factory, ok := r.factories[typ]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no backend factory registered for type %q", typ)
	}
	return factory(cfg)
}

// Has reports whether a factory is registered for the given type.
func (r *Registry) Has(typ sandbox.Type) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.factories[typ]
	return ok
}

// AvailableTypes returns all registered backend types in sorted order.
func (r *Registry) AvailableTypes() []sandbox.Type {
	r.mu.RLock()
	defer r.mu.RUnlock()
	types := make([]sandbox.Type, 0, len(r.factories))
	for typ := range r.factories {
		types = append(types, typ)
	}
	sort.Slice(types, func(i, j int) bool {
		return string(types[i]) < string(types[j])
	})
	return types
}

// TypeNames returns the string names of all available backend types.
func (r *Registry) TypeNames() []string {
	types := r.AvailableTypes()
	names := make([]string, len(types))
	for i, t := range types {
		names[i] = string(t)
	}
	return names
}
