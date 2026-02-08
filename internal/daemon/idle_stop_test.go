package daemon

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
)

type fakeSSHDetector struct {
	active bool
	err    error
}

func (f fakeSSHDetector) HasActiveSSH(context.Context, string) (bool, error) {
	return f.active, f.err
}

func TestIdleStopperStopsIdleSandbox(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{cpuUsage: 0.01}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	profiles := map[string]models.Profile{
		"default": {
			Name:    "default",
			RawYAML: "behavior:\n  idle_stop_minutes_default: 1\n",
		},
	}
	fixed := time.Date(2026, 2, 8, 18, 0, 0, 0, time.UTC)
	sandbox := models.Sandbox{
		VMID:          101,
		Name:          "idle-sb",
		Profile:       "default",
		State:         models.SandboxRunning,
		IP:            "10.77.0.10",
		Keepalive:     true,
		CreatedAt:     fixed.Add(-2 * time.Minute),
		LastUpdatedAt: fixed.Add(-2 * time.Minute),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	stopper := NewIdleStopper(store, backend, profiles, manager, fakeSSHDetector{active: false}, log.New(io.Discard, "", 0), nil, IdleStopConfig{
		Enabled:        true,
		Interval:       time.Minute,
		DefaultMinutes: 0,
		CPUThreshold:   0.05,
	})
	stopper.now = func() time.Time { return fixed }
	stopper.Evaluate(ctx)

	updated, err := store.GetSandbox(ctx, sandbox.VMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if updated.State != models.SandboxStopped {
		t.Fatalf("expected sandbox stopped, got %s", updated.State)
	}
	if backend.stopCalls == 0 {
		t.Fatalf("expected stop to be called")
	}
}

func TestIdleStopperSkipsWhenSSHActive(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{cpuUsage: 0.0}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	profiles := map[string]models.Profile{
		"default": {
			Name:    "default",
			RawYAML: "behavior:\n  idle_stop_minutes_default: 1\n",
		},
	}
	fixed := time.Date(2026, 2, 8, 19, 0, 0, 0, time.UTC)
	sandbox := models.Sandbox{
		VMID:          202,
		Name:          "active-ssh",
		Profile:       "default",
		State:         models.SandboxRunning,
		IP:            "10.77.0.20",
		CreatedAt:     fixed.Add(-10 * time.Minute),
		LastUpdatedAt: fixed.Add(-10 * time.Minute),
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	stopper := NewIdleStopper(store, backend, profiles, manager, fakeSSHDetector{active: true}, log.New(io.Discard, "", 0), nil, IdleStopConfig{
		Enabled:        true,
		Interval:       time.Minute,
		DefaultMinutes: 0,
		CPUThreshold:   0.05,
	})
	stopper.now = func() time.Time { return fixed }
	stopper.Evaluate(ctx)

	updated, err := store.GetSandbox(ctx, sandbox.VMID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if updated.State != models.SandboxRunning {
		t.Fatalf("expected sandbox running, got %s", updated.State)
	}
	if backend.stopCalls != 0 {
		t.Fatalf("expected stop not called")
	}
}
