package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

const (
	defaultLeaseGCInterval = 30 * time.Second // Interval between lease GC cycles
)

var (
	ErrInvalidTransition = errors.New("invalid sandbox state transition")
	ErrSandboxNotFound   = errors.New("sandbox not found")
	ErrLeaseNotRenewable = errors.New("sandbox lease is not renewable")
	ErrSandboxInUse      = errors.New("sandbox has a running job")
	ErrSnapshotMissing   = errors.New("snapshot not found")
)

// SandboxInUseError indicates a sandbox has a running job.
type SandboxInUseError struct {
	JobID string
}

func (e SandboxInUseError) Error() string {
	if e.JobID != "" {
		return fmt.Sprintf("sandbox has running job %s", e.JobID)
	}
	return "sandbox has running job"
}

func (e SandboxInUseError) Unwrap() error {
	return ErrSandboxInUse
}

// SnapshotMissingError indicates a missing snapshot during rollback.
type SnapshotMissingError struct {
	Name string
	Err  error
}

func (e SnapshotMissingError) Error() string {
	if e.Name != "" {
		return fmt.Sprintf("snapshot %q not found", e.Name)
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return ErrSnapshotMissing.Error()
}

func (e SnapshotMissingError) Unwrap() error {
	return ErrSnapshotMissing
}

// RevertOptions controls sandbox revert behavior.
type RevertOptions struct {
	Force   bool
	Restart *bool
}

// RevertResult captures the outcome of a sandbox revert.
type RevertResult struct {
	Snapshot   string
	WasRunning bool
	Restarted  bool
	Sandbox    models.Sandbox
}

type revertEventPayload struct {
	Snapshot   string `json:"snapshot"`
	Restart    bool   `json:"restart"`
	WasRunning bool   `json:"was_running"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

type lifecycleEventPayload struct {
	DurationMS int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

// SandboxManager enforces sandbox state transitions and lease garbage collection.
//
// It provides a state machine for sandbox lifecycle management, ensuring that
// state transitions follow the allowed transitions defined in models.SandboxState.
// The manager also handles lease expiration for keepalive sandboxes.
//
// Key responsibilities:
//   - Enforce valid state transitions
//   - Periodic garbage collection of expired leases
//   - Reconciliation of sandbox state with Proxmox VM state
//   - Workspace detachment on sandbox destroy
//   - Cloud-init snippet cleanup on destroy
//
// The manager can be configured with optional dependencies:
//   - WorkspaceManager: For workspace detachment on destroy
//   - Snippet cleaner: For cloud-init snippet cleanup
//   - Metrics: For Prometheus metrics collection
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
//
// The manager is created with default time source (time.Now) and GC interval.
// Use the With* methods to configure optional dependencies like WorkspaceManager,
// snippet cleaner, and metrics.
//
// Parameters:
//   - store: Database store for sandbox persistence
//   - backend: Proxmox backend for VM operations
//   - logger: Logger for operational output (uses log.Default if nil)
//
// Returns a configured SandboxManager ready for use.
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
//
// When a sandbox is destroyed, the workspace manager will be used to detach
// any associated workspace volume. This is typically called during service
// initialization.
//
// Returns the manager for method chaining.
func (m *SandboxManager) WithWorkspaceManager(manager *WorkspaceManager) *SandboxManager {
	if m == nil {
		return m
	}
	m.workspace = manager
	return m
}

// WithSnippetCleaner sets a callback to clean up cloud-init snippets on destroy.
//
// The callback is invoked after a sandbox is successfully destroyed, receiving
// the VMID of the destroyed sandbox. This allows cleanup of Proxmox cloud-init
// snippet files associated with the sandbox.
//
// Returns the manager for method chaining.
func (m *SandboxManager) WithSnippetCleaner(cleaner func(vmid int)) *SandboxManager {
	if m == nil {
		return m
	}
	m.snippetFn = cleaner
	return m
}

// WithMetrics wires optional Prometheus metrics.
//
// When metrics are configured, the manager will record:
//   - State transition counts (by from/to state)
//   - Sandbox provisioning duration (time from creation to running)
//
// Returns the manager for method chaining.
func (m *SandboxManager) WithMetrics(metrics *Metrics) *SandboxManager {
	if m == nil {
		return m
	}
	m.metrics = metrics
	return m
}

// StartLeaseGC runs lease GC immediately and then on an interval until ctx is done.
//
// Garbage collection finds all sandboxes with expired leases and transitions
// them to TIMEOUT state, then stops and destroys them. The GC runs on the
// configured interval (default 30 seconds) in a separate goroutine.
//
// The function returns immediately after starting the GC goroutine. Use context
// cancellation to stop the GC loop.
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

// StartReconciler runs state reconciliation immediately and then on an interval until ctx is done.
//
// Reconciliation syncs the database state with the actual Proxmox VM state,
// fixing inconsistencies such as:
//   - VMs destroyed outside of AgentLab (marked as DESTROYED)
//   - VMs stopped while in RUNNING state (marked as FAILED)
//   - VMs running while in REQUESTED state (marked as READY)
//
// The reconciler runs on the GC interval in a separate goroutine. The function
// returns immediately after starting the reconciler goroutine.
func (m *SandboxManager) StartReconciler(ctx context.Context) {
	if m == nil || m.store == nil || m.backend == nil {
		return
	}
	m.ReconcileState(ctx)
	ticker := time.NewTicker(m.gcInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.ReconcileState(ctx)
			}
		}
	}()
}

// Transition moves a sandbox to the requested state if allowed.
//
// This method enforces the state machine defined by allowedTransition().
// If the transition is not allowed, it returns ErrInvalidTransition.
// The transition is atomic using database optimistic locking.
//
// Parameters:
//   - ctx: Context for cancellation
//   - vmid: The VM ID of the sandbox to transition
//   - target: The desired target state
//
// Returns an error if:
//   - The sandbox doesn't exist
//   - The transition is not allowed
//   - The database update fails (concurrent modification)
//
// On successful transition, records an event and optionally updates metrics.
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
//
// Only sandboxes with Keepalive=true can have their leases renewed. The new
// expiration time is calculated as now + ttl.
//
// Parameters:
//   - ctx: Context for cancellation
//   - vmid: The VM ID of the sandbox
//   - ttl: The time-to-live extension duration
//
// Returns the new lease expiration time in UTC.
//
// Returns an error if:
//   - The sandbox doesn't exist or is destroyed
//   - The sandbox is not a keepalive sandbox
//   - The TTL is not positive
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

// Start boots a stopped sandbox and transitions it back to RUNNING.
//
// The sandbox must be in STOPPED state. The start process:
//  1. Starts the VM in Proxmox
//  2. Transitions STOPPED -> BOOTING -> READY -> RUNNING
//
// Returns ErrSandboxNotFound if the sandbox doesn't exist or has been destroyed.
// Returns ErrInvalidTransition if the sandbox is not STOPPED.
func (m *SandboxManager) Start(ctx context.Context, vmid int) (err error) {
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
		return ErrSandboxNotFound
	}
	if sandbox.State == models.SandboxRunning || sandbox.State == models.SandboxReady {
		return nil
	}
	if sandbox.State != models.SandboxStopped {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, sandbox.State, models.SandboxRunning)
	}
	if m.backend == nil {
		return errors.New("proxmox backend unavailable")
	}
	startedAt := m.now().UTC()
	defer func() {
		duration := m.now().UTC().Sub(startedAt)
		if err != nil {
			m.recordLifecycleEvent(ctx, vmid, "sandbox.start.failed", fmt.Sprintf("start failed: %s", err.Error()), lifecycleEventPayload{
				DurationMS: duration.Milliseconds(),
				Error:      err.Error(),
			})
			if m.metrics != nil {
				m.metrics.ObserveSandboxStart("failed", duration)
			}
			return
		}
		m.recordLifecycleEvent(ctx, vmid, "sandbox.start.completed", fmt.Sprintf("start completed in %s", duration), lifecycleEventPayload{
			DurationMS: duration.Milliseconds(),
		})
		if m.metrics != nil {
			m.metrics.ObserveSandboxStart("success", duration)
		}
	}()
	if err = m.backend.Start(ctx, proxmox.VMID(vmid)); err != nil {
		if errors.Is(err, proxmox.ErrVMNotFound) {
			return ErrSandboxNotFound
		}
		return fmt.Errorf("start vmid %d: %w", vmid, err)
	}
	if err = m.Transition(ctx, vmid, models.SandboxBooting); err != nil {
		return err
	}
	if err = m.Transition(ctx, vmid, models.SandboxReady); err != nil {
		return err
	}
	if err = m.Transition(ctx, vmid, models.SandboxRunning); err != nil {
		return err
	}
	return nil
}

// Stop halts a running sandbox and transitions it to STOPPED.
//
// The sandbox must be in READY or RUNNING state. The stop process:
//  1. Stops the VM in Proxmox
//  2. Transitions READY/RUNNING -> STOPPED
//
// Returns ErrSandboxNotFound if the sandbox doesn't exist or has been destroyed.
// Returns ErrInvalidTransition if the sandbox is not READY/RUNNING.
func (m *SandboxManager) Stop(ctx context.Context, vmid int) (err error) {
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
		return ErrSandboxNotFound
	}
	if sandbox.State == models.SandboxStopped {
		return nil
	}
	if sandbox.State != models.SandboxReady && sandbox.State != models.SandboxRunning {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, sandbox.State, models.SandboxStopped)
	}
	if m.backend == nil {
		return errors.New("proxmox backend unavailable")
	}
	startedAt := m.now().UTC()
	defer func() {
		duration := m.now().UTC().Sub(startedAt)
		if err != nil {
			m.recordLifecycleEvent(ctx, vmid, "sandbox.stop.failed", fmt.Sprintf("stop failed: %s", err.Error()), lifecycleEventPayload{
				DurationMS: duration.Milliseconds(),
				Error:      err.Error(),
			})
			if m.metrics != nil {
				m.metrics.ObserveSandboxStop("failed", duration)
			}
			return
		}
		m.recordLifecycleEvent(ctx, vmid, "sandbox.stop.completed", fmt.Sprintf("stop completed in %s", duration), lifecycleEventPayload{
			DurationMS: duration.Milliseconds(),
		})
		if m.metrics != nil {
			m.metrics.ObserveSandboxStop("success", duration)
		}
	}()
	if err = m.backend.Stop(ctx, proxmox.VMID(vmid)); err != nil {
		if errors.Is(err, proxmox.ErrVMNotFound) {
			return nil
		}
		return fmt.Errorf("stop vmid %d: %w", vmid, err)
	}
	if err = m.Transition(ctx, vmid, models.SandboxStopped); err != nil {
		return err
	}
	return nil
}

// Revert rolls a sandbox back to the canonical "clean" snapshot.
//
// The revert process:
//  1. Optionally stop the VM if it's running
//  2. Roll back the VM disk to the clean snapshot
//  3. Transition to STOPPED
//  4. Optionally restart the VM
//
// If Restart is nil, the sandbox is restarted only if it was running.
// If Force is false, the revert is blocked when a job is currently running.
func (m *SandboxManager) Revert(ctx context.Context, vmid int, opts RevertOptions) (result RevertResult, err error) {
	if m == nil || m.store == nil {
		return RevertResult{}, errors.New("sandbox manager not configured")
	}
	if vmid <= 0 {
		return RevertResult{}, errors.New("vmid must be positive")
	}
	sandbox, err := m.store.GetSandbox(ctx, vmid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RevertResult{}, ErrSandboxNotFound
		}
		return RevertResult{}, fmt.Errorf("load sandbox %d: %w", vmid, err)
	}
	if sandbox.State == models.SandboxDestroyed {
		return RevertResult{}, ErrSandboxNotFound
	}
	switch sandbox.State {
	case models.SandboxRequested, models.SandboxProvisioning, models.SandboxBooting:
		return RevertResult{}, fmt.Errorf("%w: %s -> revert", ErrInvalidTransition, sandbox.State)
	}
	if m.backend == nil {
		return RevertResult{}, errors.New("proxmox backend unavailable")
	}

	snapshotName := cleanSnapshotName
	status := proxmox.StatusUnknown
	if m.backend != nil {
		if current, statusErr := m.backend.Status(ctx, proxmox.VMID(vmid)); statusErr == nil {
			status = current
		}
	}

	wasRunning := sandbox.State == models.SandboxRunning || sandbox.State == models.SandboxReady
	if status == proxmox.StatusRunning {
		wasRunning = true
	}
	restart := wasRunning
	if opts.Restart != nil {
		restart = *opts.Restart
	}

	startedAt := m.now().UTC()
	m.recordRevertEvent(ctx, vmid, "sandbox.revert.started", "revert started", revertEventPayload{
		Snapshot:   snapshotName,
		Restart:    restart,
		WasRunning: wasRunning,
	})

	defer func() {
		if err == nil {
			return
		}
		duration := m.now().UTC().Sub(startedAt)
		m.recordRevertEvent(ctx, vmid, "sandbox.revert.failed", fmt.Sprintf("revert failed: %s", err.Error()), revertEventPayload{
			Snapshot:   snapshotName,
			Restart:    restart,
			WasRunning: wasRunning,
			DurationMS: duration.Milliseconds(),
			Error:      err.Error(),
		})
		if m.metrics != nil {
			m.metrics.IncSandboxRevert("failed")
			m.metrics.ObserveSandboxRevertDuration("failed", duration)
		}
	}()

	if !opts.Force {
		job, jobErr := m.store.GetJobBySandboxVMID(ctx, vmid)
		if jobErr == nil {
			if job.Status == models.JobRunning || job.Status == models.JobQueued {
				return RevertResult{}, SandboxInUseError{JobID: job.ID}
			}
		} else if !errors.Is(jobErr, sql.ErrNoRows) {
			return RevertResult{}, fmt.Errorf("load sandbox job: %w", jobErr)
		}
	}

	shouldStop := false
	if status == proxmox.StatusRunning {
		shouldStop = true
	} else if status == proxmox.StatusUnknown && wasRunning {
		shouldStop = true
	}

	if shouldStop {
		if sandbox.State == models.SandboxRunning || sandbox.State == models.SandboxReady {
			if err := m.Stop(ctx, vmid); err != nil {
				return RevertResult{}, err
			}
		} else {
			if err := m.stopSandbox(ctx, vmid); err != nil {
				return RevertResult{}, err
			}
			if err := m.Transition(ctx, vmid, models.SandboxStopped); err != nil {
				return RevertResult{}, err
			}
		}
	}

	if err := m.backend.SnapshotRollback(ctx, proxmox.VMID(vmid), snapshotName); err != nil {
		if isSnapshotMissing(err) {
			return RevertResult{}, SnapshotMissingError{Name: snapshotName, Err: err}
		}
		return RevertResult{}, fmt.Errorf("rollback snapshot %s: %w", snapshotName, err)
	}

	if err := m.Transition(ctx, vmid, models.SandboxStopped); err != nil {
		return RevertResult{}, err
	}

	restarted := false
	if restart {
		if err := m.Start(ctx, vmid); err != nil {
			return RevertResult{}, err
		}
		restarted = true
	}

	updated, err := m.store.GetSandbox(ctx, vmid)
	if err != nil {
		return RevertResult{}, fmt.Errorf("load sandbox %d: %w", vmid, err)
	}

	duration := m.now().UTC().Sub(startedAt)
	m.recordRevertEvent(ctx, vmid, "sandbox.revert.completed", "revert completed", revertEventPayload{
		Snapshot:   snapshotName,
		Restart:    restart,
		WasRunning: wasRunning,
		DurationMS: duration.Milliseconds(),
	})
	if m.metrics != nil {
		m.metrics.IncSandboxRevert("success")
		m.metrics.ObserveSandboxRevertDuration("success", duration)
	}

	result = RevertResult{
		Snapshot:   snapshotName,
		WasRunning: wasRunning,
		Restarted:  restarted,
		Sandbox:    updated,
	}
	return result, nil
}

// Destroy transitions a sandbox to destroyed after issuing a backend destroy.
//
// This is the normal destroy path that enforces state transitions. The sandbox
// must be in a state that allows transition to DESTROYED (typically STOPPED).
//
// The destroy process:
//  1. Detach any attached workspace (if configured)
//  2. Stop the VM in Proxmox
//  3. Destroy the VM in Proxmox
//  4. Transition state to DESTROYED
//  5. Clean up cloud-init snippets (if configured)
//
// Parameters:
//   - ctx: Context for cancellation
//   - vmid: The VM ID of the sandbox to destroy
//
// Returns an error if the sandbox state doesn't allow destruction or if any
// step of the destroy process fails.
func (m *SandboxManager) Destroy(ctx context.Context, vmid int) (err error) {
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
	startedAt := m.now().UTC()
	defer func() {
		duration := m.now().UTC().Sub(startedAt)
		if err != nil {
			m.recordLifecycleEvent(ctx, vmid, "sandbox.destroy.failed", fmt.Sprintf("destroy failed: %s", err.Error()), lifecycleEventPayload{
				DurationMS: duration.Milliseconds(),
				Error:      err.Error(),
			})
			if m.metrics != nil {
				m.metrics.ObserveSandboxDestroy("failed", duration)
			}
			return
		}
		m.recordLifecycleEvent(ctx, vmid, "sandbox.destroy.completed", fmt.Sprintf("destroy completed in %s", duration), lifecycleEventPayload{
			DurationMS: duration.Milliseconds(),
		})
		if m.metrics != nil {
			m.metrics.ObserveSandboxDestroy("success", duration)
		}
	}()
	if err := m.stopSandbox(ctx, vmid); err != nil {
		return err
	}
	if m.workspace != nil {
		if err = m.workspace.DetachFromVM(ctx, vmid); err != nil {
			return fmt.Errorf("detach workspace for vmid %d: %w", vmid, err)
		}
	}
	if err = m.destroySandbox(ctx, vmid); err != nil {
		return err
	}
	if err = m.Transition(ctx, vmid, models.SandboxDestroyed); err != nil {
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
	if m.workspace != nil {
		if err := m.workspace.DetachFromVM(ctx, sandbox.VMID); err != nil {
			return fmt.Errorf("detach workspace for vmid %d: %w", sandbox.VMID, err)
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
		if errors.Is(err, proxmox.ErrVMNotFound) {
			return nil
		}
		return fmt.Errorf("stop vmid %d: %w", vmid, err)
	}
	return nil
}

func (m *SandboxManager) destroySandbox(ctx context.Context, vmid int) error {
	if m.backend == nil {
		return nil
	}
	if err := m.backend.Destroy(ctx, proxmox.VMID(vmid)); err != nil {
		if errors.Is(err, proxmox.ErrVMNotFound) {
			return nil
		}
		return fmt.Errorf("destroy vmid %d: %w", vmid, err)
	}
	return nil
}

// ForceDestroy destroys a sandbox regardless of its current state.
//
// Unlike Destroy(), this method bypasses state transition validation and forces
// destruction of the VM. Use this for recovery operations when a sandbox is in
// an inconsistent state.
//
// The force destroy process:
//  1. Detach any attached workspace (if configured)
//  2. Destroy the VM in Proxmox (no stop first)
//  3. Transition state to DESTROYED
//  4. Clean up cloud-init snippets (if configured)
//
// Parameters:
//   - ctx: Context for cancellation
//   - vmid: The VM ID of the sandbox to destroy
//
// Returns an error if the sandbox doesn't exist or if backend operations fail.
func (m *SandboxManager) ForceDestroy(ctx context.Context, vmid int) (err error) {
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
	startedAt := m.now().UTC()
	defer func() {
		duration := m.now().UTC().Sub(startedAt)
		if err != nil {
			m.recordLifecycleEvent(ctx, vmid, "sandbox.destroy.failed", fmt.Sprintf("force destroy failed: %s", err.Error()), lifecycleEventPayload{
				DurationMS: duration.Milliseconds(),
				Error:      err.Error(),
			})
			if m.metrics != nil {
				m.metrics.ObserveSandboxDestroy("failed", duration)
			}
			return
		}
		m.recordLifecycleEvent(ctx, vmid, "sandbox.destroy.completed", fmt.Sprintf("force destroy completed in %s", duration), lifecycleEventPayload{
			DurationMS: duration.Milliseconds(),
		})
		if m.metrics != nil {
			m.metrics.ObserveSandboxDestroy("success", duration)
		}
	}()
	if err := m.stopSandbox(ctx, vmid); err != nil {
		return err
	}
	if m.workspace != nil {
		if err = m.workspace.DetachFromVM(ctx, vmid); err != nil {
			return fmt.Errorf("detach workspace for vmid %d: %w", vmid, err)
		}
	}
	if err = m.destroySandbox(ctx, vmid); err != nil {
		return err
	}
	if err = m.Transition(ctx, vmid, models.SandboxDestroyed); err != nil {
		return err
	}
	if m.snippetFn != nil {
		m.snippetFn(vmid)
	}
	return nil
}

// PruneOrphans destroys VMs for sandboxes stuck in TIMEOUT state.
//
// This is a recovery operation for sandboxes that timed out but their VMs
// were not destroyed (e.g., due to a crash). It destroys the Proxmox VMs
// and marks the sandboxes as DESTROYED.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns the count of sandboxes that were successfully pruned.
//
// This is typically called manually during maintenance operations.
func (m *SandboxManager) PruneOrphans(ctx context.Context) (int, error) {
	if m == nil || m.store == nil {
		return 0, errors.New("sandbox manager not configured")
	}
	sandboxes, err := m.store.ListSandboxes(ctx)
	if err != nil {
		return 0, fmt.Errorf("list sandboxes: %w", err)
	}
	count := 0
	for _, sb := range sandboxes {
		if sb.State == models.SandboxTimeout {
			if err := m.destroySandbox(ctx, sb.VMID); err != nil {
				if !errors.Is(err, proxmox.ErrVMNotFound) {
					m.logger.Printf("prune: failed to destroy sandbox %d: %v", sb.VMID, err)
					continue
				}
			}
			if err := m.Transition(ctx, sb.VMID, models.SandboxDestroyed); err != nil {
				m.logger.Printf("prune: failed to mark sandbox %d as destroyed: %v", sb.VMID, err)
				continue
			}
			count++
		}
	}
	return count, nil
}

func (m *SandboxManager) recordStateEvent(ctx context.Context, vmid int, from, to models.SandboxState) {
	msg := fmt.Sprintf("%s -> %s", from, to)
	_ = m.store.RecordEvent(ctx, "sandbox.state", &vmid, nil, msg, "")
}

func (m *SandboxManager) recordLeaseEvent(ctx context.Context, vmid int, expiresAt time.Time) {
	msg := fmt.Sprintf("renewed until %s", expiresAt.UTC().Format(time.RFC3339Nano))
	_ = m.store.RecordEvent(ctx, "sandbox.lease", &vmid, nil, msg, "")
}

func (m *SandboxManager) recordRevertEvent(ctx context.Context, vmid int, kind string, msg string, payload revertEventPayload) {
	if m == nil || m.store == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		if m.logger != nil {
			m.logger.Printf("sandbox %d: revert event json failed: %v", vmid, err)
		}
	}
	_ = m.store.RecordEvent(ctx, kind, &vmid, nil, msg, string(data))
}

func (m *SandboxManager) recordLifecycleEvent(ctx context.Context, vmid int, kind string, msg string, payload lifecycleEventPayload) {
	if m == nil || m.store == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		if m.logger != nil {
			m.logger.Printf("sandbox %d: lifecycle event json failed: %v", vmid, err)
		}
	}
	_ = m.store.RecordEvent(ctx, kind, &vmid, nil, msg, string(data))
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
		return to == models.SandboxDestroyed || to == models.SandboxBooting || to == models.SandboxReady || to == models.SandboxRunning
	case models.SandboxDestroyed:
		return false
	default:
		return false
	}
}

func isSnapshotMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "snapshot") {
		return false
	}
	phrases := []string{
		"not found",
		"does not exist",
		"no such snapshot",
		"cannot find snapshot",
		"can't find snapshot",
	}
	foundPhrase := false
	for _, phrase := range phrases {
		if strings.Contains(msg, phrase) {
			foundPhrase = true
			break
		}
	}
	if !foundPhrase {
		return false
	}
	return true
}

// ReconcileState syncs sandbox states with actual VM states from Proxmox.
//
// This fixes "zombie" sandboxes that are in an inconsistent state due to:
//   - VMs destroyed outside of AgentLab
//   - VMs stopped/started while AgentLab was down
//   - Previous crashes leaving inconsistent state
//
// Reconciliation rules:
//   - VM not found in Proxmox → mark as DESTROYED
//   - VM stopped but RUNNING → mark as FAILED
//   - VM running but REQUESTED → mark as READY
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns nil always; errors are logged rather than returned to allow
// partial reconciliation.
func (m *SandboxManager) ReconcileState(ctx context.Context) error {
	if m == nil || m.store == nil || m.backend == nil {
		return nil
	}

	sandboxes, err := m.store.ListSandboxes(ctx)
	if err != nil {
		return fmt.Errorf("list sandboxes: %w", err)
	}

	for _, sb := range sandboxes {
		if sb.State == models.SandboxDestroyed || sb.State == models.SandboxCompleted {
			continue
		}

		status, err := m.backend.Status(ctx, proxmox.VMID(sb.VMID))
		if err != nil {
			if errors.Is(err, proxmox.ErrVMNotFound) {
				if sb.State != models.SandboxDestroyed && sb.State != models.SandboxRequested {
					m.logger.Printf("reconcile: VM %d not found in Proxmox, marking as destroyed", sb.VMID)
					_ = m.Transition(ctx, sb.VMID, models.SandboxDestroyed)
				}
			}
			continue
		}

		if status == proxmox.StatusStopped && sb.State == models.SandboxRunning {
			m.logger.Printf("reconcile: VM %d stopped unexpectedly, marking as failed", sb.VMID)
			_ = m.Transition(ctx, sb.VMID, models.SandboxFailed)
		}

		if status == proxmox.StatusRunning {
			// If AgentLab crashed mid-provisioning, the VM can be running while the DB state
			// is stuck earlier in the state machine. Advance toward RUNNING step-by-step.
			switch sb.State {
			case models.SandboxRequested, models.SandboxProvisioning, models.SandboxBooting:
				// Stop at READY to avoid racing provisioning logic, which will advance READY -> RUNNING.
				for attempts := 0; attempts < 3 && sb.State != models.SandboxReady && sb.State != models.SandboxRunning; attempts++ {
					next, ok := nextSandboxStateTowardRunning(sb.State)
					if !ok || next == "" || next == sb.State || next == models.SandboxRunning {
						break
					}
					if err := m.Transition(ctx, sb.VMID, next); err != nil {
						if errors.Is(err, ErrInvalidTransition) {
							break
						}
						m.logger.Printf("reconcile: failed to advance sandbox %d (%s -> %s): %v", sb.VMID, sb.State, next, err)
						break
					}
					sb.State = next
				}
			}
		}

		// Opportunistically refresh missing IPs for running VMs. IP discovery can lag behind
		// the VM reaching RUNNING (e.g., guest agent install via cloud-init).
		if status == proxmox.StatusRunning && strings.TrimSpace(sb.IP) == "" {
			ipCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			ip, err := m.backend.GuestIP(ipCtx, proxmox.VMID(sb.VMID))
			cancel()
			if err == nil && strings.TrimSpace(ip) != "" {
				_ = m.store.UpdateSandboxIP(ctx, sb.VMID, strings.TrimSpace(ip))
			}
		}
	}

	return nil
}
