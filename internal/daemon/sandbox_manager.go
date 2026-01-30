package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

const (
	defaultLeaseGCInterval = 30 * time.Second
)

var (
	ErrInvalidTransition = errors.New("invalid sandbox state transition")
	ErrSandboxNotFound   = errors.New("sandbox not found")
	ErrLeaseNotRenewable = errors.New("sandbox lease is not renewable")
)

// SandboxManager enforces sandbox state transitions and lease GC.
type SandboxManager struct {
	store      *db.Store
	backend    proxmox.Backend
	logger     *log.Logger
	workspace  *WorkspaceManager
	snippetFn  func(vmid int)
	metrics    *Metrics
	now        func() time.Time
	gcInterval time.Duration
}

// NewSandboxManager builds a sandbox manager with defaults.
func NewSandboxManager(store *db.Store, backend proxmox.Backend, logger *log.Logger) *SandboxManager {
	if logger == nil {
		logger = log.Default()
	}
	return &SandboxManager{
		store:      store,
		backend:    backend,
		logger:     logger,
		now:        time.Now,
		gcInterval: defaultLeaseGCInterval,
	}
}

// WithWorkspaceManager sets the workspace manager for detach-on-destroy.
func (m *SandboxManager) WithWorkspaceManager(manager *WorkspaceManager) *SandboxManager {
	if m == nil {
		return m
	}
	m.workspace = manager
	return m
}

// WithSnippetCleaner sets a callback to clean up cloud-init snippets on destroy.
func (m *SandboxManager) WithSnippetCleaner(cleaner func(vmid int)) *SandboxManager {
	if m == nil {
		return m
	}
	m.snippetFn = cleaner
	return m
}

// WithMetrics wires optional Prometheus metrics.
func (m *SandboxManager) WithMetrics(metrics *Metrics) *SandboxManager {
	if m == nil {
		return m
	}
	m.metrics = metrics
	return m
}

// StartLeaseGC runs lease GC immediately and then on an interval until ctx is done.
func (m *SandboxManager) StartLeaseGC(ctx context.Context) {
	if m == nil || m.store == nil || m.gcInterval <= 0 {
		return
	}
	m.runLeaseGC(ctx)
	ticker := time.NewTicker(m.gcInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.runLeaseGC(ctx)
			}
		}
	}()
}

// Transition moves a sandbox to the requested state if allowed.
func (m *SandboxManager) Transition(ctx context.Context, vmid int, target models.SandboxState) error {
	if m == nil || m.store == nil {
		return errors.New("sandbox manager not configured")
	}
	if target == "" {
		return errors.New("target state is required")
	}
	sandbox, err := m.store.GetSandbox(ctx, vmid)
	if err != nil {
		return fmt.Errorf("load sandbox %d: %w", vmid, err)
	}
	current := sandbox.State
	if current == target {
		return nil
	}
	if !allowedTransition(current, target) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current, target)
	}
	updated, err := m.store.UpdateSandboxState(ctx, vmid, current, target)
	if err != nil {
		return err
	}
	if !updated {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current, target)
	}
	m.recordStateEvent(ctx, vmid, current, target)
	if m.metrics != nil {
		m.metrics.IncSandboxTransition(current, target)
		if target == models.SandboxRunning && !sandbox.CreatedAt.IsZero() {
			m.metrics.ObserveSandboxProvision(m.now().UTC().Sub(sandbox.CreatedAt))
		}
	}
	return nil
}

// RenewLease extends a keepalive sandbox lease.
func (m *SandboxManager) RenewLease(ctx context.Context, vmid int, ttl time.Duration) (time.Time, error) {
	if m == nil || m.store == nil {
		return time.Time{}, errors.New("sandbox manager not configured")
	}
	if ttl <= 0 {
		return time.Time{}, errors.New("ttl must be positive")
	}
	sandbox, err := m.store.GetSandbox(ctx, vmid)
	if err != nil {
		return time.Time{}, fmt.Errorf("load sandbox %d: %w", vmid, err)
	}
	if sandbox.State == models.SandboxDestroyed {
		return time.Time{}, ErrSandboxNotFound
	}
	if !sandbox.Keepalive {
		return time.Time{}, ErrLeaseNotRenewable
	}
	expiresAt := m.now().UTC().Add(ttl)
	if err := m.store.UpdateSandboxLease(ctx, vmid, expiresAt); err != nil {
		return time.Time{}, err
	}
	m.recordLeaseEvent(ctx, vmid, expiresAt)
	return expiresAt, nil
}

// Destroy transitions a sandbox to destroyed after issuing a backend destroy.
func (m *SandboxManager) Destroy(ctx context.Context, vmid int) error {
	if m == nil || m.store == nil {
		return errors.New("sandbox manager not configured")
	}
	sandbox, err := m.store.GetSandbox(ctx, vmid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSandboxNotFound
		}
		return fmt.Errorf("load sandbox %d: %w", vmid, err)
	}
	if sandbox.State == models.SandboxDestroyed {
		return nil
	}
	if m.workspace != nil {
		if err := m.workspace.DetachFromVM(ctx, vmid); err != nil {
			return fmt.Errorf("detach workspace for vmid %d: %w", vmid, err)
		}
	}
	if err := m.destroySandbox(ctx, vmid); err != nil {
		return err
	}
	if err := m.Transition(ctx, vmid, models.SandboxDestroyed); err != nil {
		return err
	}
	if m.snippetFn != nil {
		m.snippetFn(vmid)
	}
	return nil
}

func (m *SandboxManager) runLeaseGC(ctx context.Context) {
	sandboxes, err := m.store.ListExpiredSandboxes(ctx, m.now().UTC())
	if err != nil {
		m.logger.Printf("sandbox lease GC error: %v", err)
		return
	}
	for _, sandbox := range sandboxes {
		if err := m.expireSandbox(ctx, sandbox); err != nil {
			m.logger.Printf("sandbox lease GC vmid=%d: %v", sandbox.VMID, err)
		}
	}
}

func (m *SandboxManager) expireSandbox(ctx context.Context, sandbox models.Sandbox) error {
	switch sandbox.State {
	case models.SandboxDestroyed:
		return nil
	case models.SandboxTimeout, models.SandboxCompleted, models.SandboxFailed, models.SandboxStopped:
		// continue
	default:
		if err := m.Transition(ctx, sandbox.VMID, models.SandboxTimeout); err != nil {
			return err
		}
	}
	if sandbox.State != models.SandboxStopped && sandbox.State != models.SandboxDestroyed {
		if err := m.stopSandbox(ctx, sandbox.VMID); err != nil {
			return err
		}
		if err := m.Transition(ctx, sandbox.VMID, models.SandboxStopped); err != nil {
			return err
		}
	}
	if err := m.destroySandbox(ctx, sandbox.VMID); err != nil {
		return err
	}
	if err := m.Transition(ctx, sandbox.VMID, models.SandboxDestroyed); err != nil {
		return err
	}
	if m.snippetFn != nil {
		m.snippetFn(sandbox.VMID)
	}
	return nil
}

func (m *SandboxManager) stopSandbox(ctx context.Context, vmid int) error {
	if m.backend == nil {
		return nil
	}
	if err := m.backend.Stop(ctx, proxmox.VMID(vmid)); err != nil {
		return fmt.Errorf("stop vmid %d: %w", vmid, err)
	}
	return nil
}

func (m *SandboxManager) destroySandbox(ctx context.Context, vmid int) error {
	if m.backend == nil {
		return nil
	}
	if err := m.backend.Destroy(ctx, proxmox.VMID(vmid)); err != nil {
		return fmt.Errorf("destroy vmid %d: %w", vmid, err)
	}
	return nil
}

func (m *SandboxManager) recordStateEvent(ctx context.Context, vmid int, from, to models.SandboxState) {
	msg := fmt.Sprintf("%s -> %s", from, to)
	_ = m.store.RecordEvent(ctx, "sandbox.state", &vmid, nil, msg, "")
}

func (m *SandboxManager) recordLeaseEvent(ctx context.Context, vmid int, expiresAt time.Time) {
	msg := fmt.Sprintf("renewed until %s", expiresAt.UTC().Format(time.RFC3339Nano))
	_ = m.store.RecordEvent(ctx, "sandbox.lease", &vmid, nil, msg, "")
}

func allowedTransition(from, to models.SandboxState) bool {
	switch from {
	case models.SandboxRequested:
		return to == models.SandboxProvisioning || to == models.SandboxTimeout || to == models.SandboxDestroyed
	case models.SandboxProvisioning:
		return to == models.SandboxBooting || to == models.SandboxTimeout || to == models.SandboxDestroyed
	case models.SandboxBooting:
		return to == models.SandboxReady || to == models.SandboxTimeout || to == models.SandboxDestroyed
	case models.SandboxReady:
		return to == models.SandboxRunning || to == models.SandboxStopped || to == models.SandboxTimeout || to == models.SandboxDestroyed
	case models.SandboxRunning:
		return to == models.SandboxCompleted || to == models.SandboxFailed || to == models.SandboxTimeout || to == models.SandboxStopped || to == models.SandboxDestroyed
	case models.SandboxCompleted:
		return to == models.SandboxStopped || to == models.SandboxDestroyed
	case models.SandboxFailed:
		return to == models.SandboxStopped || to == models.SandboxDestroyed
	case models.SandboxTimeout:
		return to == models.SandboxStopped || to == models.SandboxDestroyed
	case models.SandboxStopped:
		return to == models.SandboxDestroyed
	case models.SandboxDestroyed:
		return false
	default:
		return false
	}
}
