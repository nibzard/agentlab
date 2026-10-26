package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sort"
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
	Force      bool   `json:"force,omitempty"`
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
	store           *db.Store
	backend         proxmox.Backend
	logger          *log.Logger
	workspace       *WorkspaceManager
	snippetFn       func(vmid int)
	exposures       *ExposureCleaner
	metrics         *Metrics
	notifier        *Notifier
	now             func() time.Time
	gcInterval      time.Duration
	leaseWarnWindow time.Duration
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

// WithLeaseWarnWindow configures how long before lease expiration a sandbox
// receives a pre-expiry warning. Zero (the default) disables the warning pass.
//
// Returns the manager for method chaining.
func (m *SandboxManager) WithLeaseWarnWindow(window time.Duration) *SandboxManager {
	if m == nil {
		return m
	}
	m.leaseWarnWindow = window
	return m
}

// WithNotifier sets the webhook notifier used for operational notifications
// such as lease pre-expiry warnings. A nil or disabled notifier means
// warnings are recorded as events only.
//
// Returns the manager for method chaining.
func (m *SandboxManager) WithNotifier(notifier *Notifier) *SandboxManager {
	if m == nil {
		return m
	}
	m.notifier = notifier
	return m
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

// WithExposureCleaner sets the exposure cleanup helper for destroy operations.
//
// When a sandbox is destroyed, the exposure cleaner will remove any related
// host-level serve rules and delete exposure records (best-effort).
func (m *SandboxManager) WithExposureCleaner(cleaner *ExposureCleaner) *SandboxManager {
	if m == nil {
		return m
	}
	m.exposures = cleaner
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
// them to TIMEOUT state, then stops and destroys them. When a lease warn
// window is configured, each cycle first emits pre-expiry warnings (once per
// lease term) for sandboxes approaching expiration. The GC runs on the
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
	// Retire the address association on destruction so a later reuse of the
	// address cannot resolve to this row (review H4). Best-effort: a failure
	// here does not undo the state transition.
	if target == models.SandboxDestroyed {
		if err := m.store.ClearSandboxIP(ctx, vmid); err != nil {
			m.logger.Printf("clear sandbox %d ip on destroy: %v", vmid, err)
		}
	}
	if m.metrics != nil {
		m.metrics.IncSandboxTransition(current, target)
		if target == models.SandboxRunning && !sandbox.CreatedAt.IsZero() {
			m.metrics.ObserveSandboxProvision(m.now().UTC().Sub(sandbox.CreatedAt))
		}
	}
	return nil
}

// RenewLease extends a sandbox lease.
//
// Lease renewal is only allowed while the sandbox is in RUNNING state. The new
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
//   - The sandbox is not in RUNNING state
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
	if sandbox.State != models.SandboxRunning {
		return time.Time{}, fmt.Errorf("%w: %s -> renew_lease", ErrInvalidTransition, sandbox.State)
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
			m.recordLifecycleEvent(ctx, vmid, EventKindSandboxStartFailed, fmt.Sprintf("start failed: %s", err.Error()), lifecycleEventPayload{
				DurationMS: duration.Milliseconds(),
				Error:      err.Error(),
			})
			if m.metrics != nil {
				m.metrics.ObserveSandboxStart("failed", duration)
			}
			return
		}
		m.recordLifecycleEvent(ctx, vmid, EventKindSandboxStartCompleted, fmt.Sprintf("start completed in %s", duration), lifecycleEventPayload{
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
	if sandbox.State != models.SandboxReady && sandbox.State != models.SandboxRunning && sandbox.State != models.SandboxSuspended {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, sandbox.State, models.SandboxStopped)
	}
	if m.backend == nil {
		return errors.New("proxmox backend unavailable")
	}
	startedAt := m.now().UTC()
	defer func() {
		duration := m.now().UTC().Sub(startedAt)
		if err != nil {
			m.recordLifecycleEvent(ctx, vmid, EventKindSandboxStopFailed, fmt.Sprintf("stop failed: %s", err.Error()), lifecycleEventPayload{
				DurationMS: duration.Milliseconds(),
				Error:      err.Error(),
			})
			if m.metrics != nil {
				m.metrics.ObserveSandboxStop("failed", duration)
			}
			return
		}
		m.recordLifecycleEvent(ctx, vmid, EventKindSandboxStopCompleted, fmt.Sprintf("stop completed in %s", duration), lifecycleEventPayload{
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

// Pause suspends a sandbox and transitions it to SUSPENDED.
//
// The sandbox must be in READY or RUNNING state. When a workspace is attached,
// pausing is blocked if a job is actively running.
func (m *SandboxManager) Pause(ctx context.Context, vmid int) (err error) {
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
	if sandbox.State == models.SandboxSuspended {
		return nil
	}
	if sandbox.State != models.SandboxReady && sandbox.State != models.SandboxRunning {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, sandbox.State, models.SandboxSuspended)
	}
	if sandbox.WorkspaceID != nil {
		job, jobErr := m.store.GetJobBySandboxVMID(ctx, vmid)
		if jobErr == nil {
			if job.Status == models.JobRunning || job.Status == models.JobQueued {
				return SandboxInUseError{JobID: job.ID}
			}
		} else if !errors.Is(jobErr, sql.ErrNoRows) {
			return fmt.Errorf("load sandbox job: %w", jobErr)
		}
	}
	if m.backend == nil {
		return errors.New("proxmox backend unavailable")
	}
	startedAt := m.now().UTC()
	defer func() {
		duration := m.now().UTC().Sub(startedAt)
		if err != nil {
			m.recordLifecycleEvent(ctx, vmid, EventKindSandboxPauseFailed, fmt.Sprintf("pause failed: %s", err.Error()), lifecycleEventPayload{
				DurationMS: duration.Milliseconds(),
				Error:      err.Error(),
			})
			return
		}
		m.recordLifecycleEvent(ctx, vmid, EventKindSandboxPauseCompleted, fmt.Sprintf("pause completed in %s", duration), lifecycleEventPayload{
			DurationMS: duration.Milliseconds(),
		})
	}()
	if err = m.backend.Suspend(ctx, proxmox.VMID(vmid)); err != nil {
		if errors.Is(err, proxmox.ErrVMNotFound) {
			return ErrSandboxNotFound
		}
		return fmt.Errorf("suspend vmid %d: %w", vmid, err)
	}
	if err = m.Transition(ctx, vmid, models.SandboxSuspended); err != nil {
		return err
	}
	return nil
}

// Resume resumes a suspended sandbox and transitions it back to READY or RUNNING.
//
// The sandbox must be in SUSPENDED state. If a job is active, the sandbox
// returns to RUNNING; otherwise it returns to READY.
func (m *SandboxManager) Resume(ctx context.Context, vmid int) (err error) {
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
	if sandbox.State != models.SandboxSuspended {
		return fmt.Errorf("%w: %s -> resume", ErrInvalidTransition, sandbox.State)
	}
	if m.backend == nil {
		return errors.New("proxmox backend unavailable")
	}
	startedAt := m.now().UTC()
	defer func() {
		duration := m.now().UTC().Sub(startedAt)
		if err != nil {
			m.recordLifecycleEvent(ctx, vmid, EventKindSandboxResumeFailed, fmt.Sprintf("resume failed: %s", err.Error()), lifecycleEventPayload{
				DurationMS: duration.Milliseconds(),
				Error:      err.Error(),
			})
			return
		}
		m.recordLifecycleEvent(ctx, vmid, EventKindSandboxResumeCompleted, fmt.Sprintf("resume completed in %s", duration), lifecycleEventPayload{
			DurationMS: duration.Milliseconds(),
		})
	}()
	if err = m.backend.Resume(ctx, proxmox.VMID(vmid)); err != nil {
		if errors.Is(err, proxmox.ErrVMNotFound) {
			return ErrSandboxNotFound
		}
		return fmt.Errorf("resume vmid %d: %w", vmid, err)
	}
	target := models.SandboxReady
	job, jobErr := m.store.GetJobBySandboxVMID(ctx, vmid)
	if jobErr == nil {
		if job.Status == models.JobRunning || job.Status == models.JobQueued {
			target = models.SandboxRunning
		}
	} else if !errors.Is(jobErr, sql.ErrNoRows) {
		return fmt.Errorf("load sandbox job: %w", jobErr)
	}
	if err = m.Transition(ctx, vmid, target); err != nil {
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

	wasRunning := sandbox.State == models.SandboxRunning || sandbox.State == models.SandboxReady || sandbox.State == models.SandboxSuspended
	if status == proxmox.StatusRunning {
		wasRunning = true
	}
	restart := wasRunning
	if opts.Restart != nil {
		restart = *opts.Restart
	}

	startedAt := m.now().UTC()
	m.recordRevertEvent(ctx, vmid, EventKindSandboxRevertStarted, "revert started", revertEventPayload{
		Snapshot:   snapshotName,
		Restart:    restart,
		WasRunning: wasRunning,
		Force:      opts.Force,
	})

	defer func() {
		if err == nil {
			return
		}
		duration := m.now().UTC().Sub(startedAt)
		m.recordRevertEvent(ctx, vmid, EventKindSandboxRevertFailed, fmt.Sprintf("revert failed: %s", err.Error()), revertEventPayload{
			Snapshot:   snapshotName,
			Restart:    restart,
			WasRunning: wasRunning,
			Force:      opts.Force,
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
	} else if sandbox.State == models.SandboxSuspended {
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
	m.recordRevertEvent(ctx, vmid, EventKindSandboxRevertFinished, "revert completed", revertEventPayload{
		Snapshot:   snapshotName,
		Restart:    restart,
		WasRunning: wasRunning,
		Force:      opts.Force,
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
// may be running; it is stopped before its persistent workspace is detached.
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
	leases := m.workspaceLeasesForSandbox(ctx, vmid)
	startedAt := m.now().UTC()
	defer func() {
		duration := m.now().UTC().Sub(startedAt)
		if err != nil {
			m.recordLifecycleEvent(ctx, vmid, EventKindSandboxDestroyFailed, fmt.Sprintf("destroy failed: %s", err.Error()), lifecycleEventPayload{
				DurationMS: duration.Milliseconds(),
				Error:      err.Error(),
			})
			if m.metrics != nil {
				m.metrics.ObserveSandboxDestroy("failed", duration)
			}
			return
		}
		m.recordLifecycleEvent(ctx, vmid, EventKindSandboxDestroyCompleted, fmt.Sprintf("destroy completed in %s", duration), lifecycleEventPayload{
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
	m.releaseWorkspaceLeaseSnapshots(ctx, vmid, leases)
	if m.snippetFn != nil {
		m.snippetFn(vmid)
	}
	m.cleanupExposures(ctx, vmid)
	// Delete the sandbox secret. Best-effort: a failure here should not
	// undo the destroy operation.
	if secretErr := m.store.DeleteSandboxSecret(ctx, vmid); secretErr != nil && m.logger != nil {
		m.logger.Printf("delete sandbox %d secret: %v", vmid, secretErr)
	}
	return nil
}

func (m *SandboxManager) runLeaseGC(ctx context.Context) {
	m.warnExpiringLeases(ctx)
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

// warnExpiringLeases emits a one-time pre-expiry warning for every sandbox
// whose lease expires within the configured warn window and has not been
// warned yet. A sandbox is marked as warned only after the warning has been
// recorded; when a webhook notifier is configured, delivery must succeed
// first, so a temporarily unreachable webhook retries on the next GC cycle
// instead of silently dropping the alert. Lease renewal clears the warned
// marker, so a renewed lease warns again near its new expiration.
func (m *SandboxManager) warnExpiringLeases(ctx context.Context) {
	if m == nil || m.store == nil || m.leaseWarnWindow <= 0 {
		return
	}
	now := m.now().UTC()
	sandboxes, err := m.store.ListSandboxesWithLeaseExpiringBefore(ctx, now, now.Add(m.leaseWarnWindow))
	if err != nil {
		m.logger.Printf("sandbox lease warn error: %v", err)
		return
	}
	for _, sandbox := range sandboxes {
		remaining := sandbox.LeaseExpires.Sub(now).Round(time.Minute)
		if remaining <= 0 {
			continue
		}
		if m.notifier != nil && m.notifier.Enabled() {
			if err := m.notifier.NotifyLeaseExpiring(ctx, sandbox, remaining); err != nil {
				m.logger.Printf("sandbox lease warn notify vmid=%d: %v", sandbox.VMID, err)
				continue
			}
		}
		vmid := sandbox.VMID
		msg := fmt.Sprintf("lease expires in %s (at %s)", remaining, sandbox.LeaseExpires.UTC().Format(time.RFC3339))
		payload := struct {
			ExpiresAt string `json:"expires_at"`
			Remaining string `json:"remaining"`
		}{
			ExpiresAt: sandbox.LeaseExpires.UTC().Format(time.RFC3339),
			Remaining: remaining.String(),
		}
		if err := emitEvent(ctx, NewStoreEventRecorder(m.store), EventKindSandboxLeaseExpiring, &vmid, nil, msg, payload); err != nil {
			m.logger.Printf("record lease warn event vmid=%d: %v", sandbox.VMID, err)
			continue
		}
		if err := m.store.MarkSandboxLeaseWarned(ctx, sandbox.VMID, now); err != nil {
			m.logger.Printf("mark lease warned vmid=%d: %v", sandbox.VMID, err)
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
	m.releaseWorkspaceLeases(ctx, sandbox.VMID)
	if err := m.destroySandbox(ctx, sandbox.VMID); err != nil {
		return err
	}
	if err := m.Transition(ctx, sandbox.VMID, models.SandboxDestroyed); err != nil {
		return err
	}
	if m.snippetFn != nil {
		m.snippetFn(sandbox.VMID)
	}
	m.cleanupExposures(ctx, sandbox.VMID)
	// Delete the sandbox secret. Best-effort: a failure here should not
	// undo the destroy operation.
	if secretErr := m.store.DeleteSandboxSecret(ctx, sandbox.VMID); secretErr != nil && m.logger != nil {
		m.logger.Printf("delete sandbox %d secret: %v", sandbox.VMID, secretErr)
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

// ForceDestroy is the recovery alias for Destroy. Both paths stop the VM,
// detach persistent storage, validate the transition, and release owned leases.
// All supported sandbox states permit destruction.
func (m *SandboxManager) ForceDestroy(ctx context.Context, vmid int) error {
	return m.Destroy(ctx, vmid)
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
type PruneOutcome struct {
	VMID      int    `json:"vmid"`
	Name      string `json:"name"`
	Outcome   string `json:"outcome"`
	Error     string `json:"error,omitempty"`
	Retryable bool   `json:"retryable"`
}
type PruneResult struct {
	Count    int            `json:"count"`
	DryRun   bool           `json:"dry_run"`
	Outcomes []PruneOutcome `json:"outcomes"`
}

func (m *SandboxManager) PruneOrphans(ctx context.Context) (int, error) {
	result, err := m.PruneTimedOut(ctx, false, nil)
	return result.Count, err
}

// PruneTimedOut never broadens its target set to stopped sandboxes. An optional
// target list lets callers execute exactly the resources shown in a preview.
func (m *SandboxManager) PruneTimedOut(ctx context.Context, dryRun bool, targets map[int]bool) (PruneResult, error) {
	result := PruneResult{DryRun: dryRun, Outcomes: make([]PruneOutcome, 0)}
	if m == nil || m.store == nil {
		return result, errors.New("sandbox manager not configured")
	}
	sandboxes, err := m.store.ListSandboxes(ctx)
	if err != nil {
		return result, err
	}
	seen := map[int]bool{}
	for _, sb := range sandboxes {
		if targets != nil && !targets[sb.VMID] {
			continue
		}
		seen[sb.VMID] = true
		if sb.State != models.SandboxTimeout {
			if targets != nil {
				result.Outcomes = append(result.Outcomes, PruneOutcome{VMID: sb.VMID, Name: sb.Name, Outcome: "skipped", Error: "state changed to " + string(sb.State)})
			}
			continue
		}
		outcome := PruneOutcome{VMID: sb.VMID, Name: sb.Name, Outcome: "preview"}
		if !dryRun {
			if err := m.Destroy(ctx, sb.VMID); err != nil {
				outcome.Outcome = "failed"
				outcome.Retryable = true
				outcome.Error = err.Error()
			} else {
				outcome.Outcome = "destroyed"
				result.Count++
			}
		}
		result.Outcomes = append(result.Outcomes, outcome)
	}
	missing := make([]int, 0)
	for vmid, selected := range targets {
		if selected && !seen[vmid] {
			missing = append(missing, vmid)
		}
	}
	sort.Ints(missing)
	for _, vmid := range missing {
		result.Outcomes = append(result.Outcomes, PruneOutcome{VMID: vmid, Outcome: "not_found", Error: "sandbox no longer exists"})
	}
	return result, nil
}

// startupOrphanStates are the transient provisioning states a sandbox can be
// stranded in if the daemon is killed (SIGKILL) mid-provisioning. These are the
// only states the startup orphan sweep considers reclaimable (review H2).
var startupOrphanStates = map[models.SandboxState]bool{
	models.SandboxRequested:    true,
	models.SandboxProvisioning: true,
	models.SandboxBooting:      true,
}

// SweepStartupOrphans reclaims sandboxes stranded in a transient provisioning
// state by a prior crash. A sandbox is reclaimed only when ALL of:
//
//   - its state is a transient provisioning state (REQUESTED/PROVISIONING/BOOTING)
//   - it is older than the grace period (so legitimate in-flight work is not
//     disturbed)
//   - ownership proof: no VM exists for its VMID in the backend (the clone
//     never completed, or was destroyed out-of-band). A VM that still exists,
//     running or stopped, may belong to a later allocation or an untracked
//     workload, so the sweep leaves it for the reconciler.
//
// Reclaimed sandboxes are destroyed (best effort) and marked DESTROYED.
//
// This closes the SIGKILL-recovery gap where a sandbox with no lease expiry and
// no backend VM could otherwise remain stranded forever (review H2).
func (m *SandboxManager) SweepStartupOrphans(ctx context.Context, grace time.Duration) (int, error) {
	if m == nil || m.store == nil {
		return 0, errors.New("sandbox manager not configured")
	}
	sandboxes, err := m.store.ListSandboxes(ctx)
	if err != nil {
		return 0, fmt.Errorf("list sandboxes: %w", err)
	}
	now := m.now().UTC()
	count := 0
	for _, sb := range sandboxes {
		if !startupOrphanStates[sb.State] {
			continue
		}
		// Grace: only reclaim sandboxes older than the grace window.
		age := now.Sub(sb.CreatedAt)
		if age < grace {
			continue
		}
		// Ownership proof: only a VMID with no backend VM is reclaimed.
		// Destroying by VMID alone could hit an unrelated VM that took
		// the ID after a failed clone.
		if m.backend != nil {
			status, err := m.backend.Status(ctx, proxmox.VMID(sb.VMID))
			if err == nil && status != proxmox.StatusUnknown {
				continue
			}
		}
		m.logger.Printf("orphan sweep: reclaiming stranded sandbox %d (state=%s, age=%s)", sb.VMID, sb.State, age.Round(time.Second))
		if err := m.destroySandbox(ctx, sb.VMID); err != nil {
			if !errors.Is(err, proxmox.ErrVMNotFound) {
				m.logger.Printf("orphan sweep: failed to destroy sandbox %d: %v", sb.VMID, err)
				continue
			}
		}
		if err := m.Transition(ctx, sb.VMID, models.SandboxDestroyed); err != nil {
			m.logger.Printf("orphan sweep: failed to mark sandbox %d destroyed: %v", sb.VMID, err)
			continue
		}
		m.cleanupExposures(ctx, sb.VMID)
		// Delete the sandbox secret. Best-effort: a failure here should not
		// undo the destroy operation.
		if secretErr := m.store.DeleteSandboxSecret(ctx, sb.VMID); secretErr != nil && m.logger != nil {
			m.logger.Printf("delete sandbox %d secret: %v", sb.VMID, secretErr)
		}
		count++
	}
	return count, nil
}

func (m *SandboxManager) recordStateEvent(ctx context.Context, vmid int, from, to models.SandboxState) {
	msg := fmt.Sprintf("%s -> %s", from, to)
	payload := struct {
		FromState string `json:"from_state"`
		ToState   string `json:"to_state"`
	}{
		FromState: string(from),
		ToState:   string(to),
	}
	_ = emitEvent(ctx, NewStoreEventRecorder(m.store), EventKindSandboxState, &vmid, nil, msg, payload)
}

func (m *SandboxManager) recordLeaseEvent(ctx context.Context, vmid int, expiresAt time.Time) {
	msg := fmt.Sprintf("renewed until %s", expiresAt.UTC().Format(time.RFC3339Nano))
	payload := map[string]any{
		"expires_at": expiresAt.UTC().Format(time.RFC3339Nano),
	}
	_ = emitEvent(ctx, NewStoreEventRecorder(m.store), EventKindSandboxLease, &vmid, nil, msg, payload)
}

type workspaceLeaseSnapshot struct {
	ID, Owner, Nonce string
	JobID            *string
}

// Capture the lease before backend work. Cleanup must compare that exact nonce,
// including when another task reacquires a lease under the same owner string.
func (m *SandboxManager) workspaceLeasesForSandbox(ctx context.Context, vmid int) []workspaceLeaseSnapshot {
	if m == nil || m.store == nil || vmid <= 0 {
		return nil
	}
	leases := make([]workspaceLeaseSnapshot, 0)
	seen := map[string]bool{}
	add := func(workspace models.Workspace, owner string, jobID *string) {
		if !seen[workspace.ID] && workspace.LeaseOwner == owner && strings.TrimSpace(workspace.LeaseNonce) != "" {
			leases = append(leases, workspaceLeaseSnapshot{ID: workspace.ID, Owner: owner, Nonce: workspace.LeaseNonce, JobID: jobID})
			seen[workspace.ID] = true
		}
	}
	if job, err := m.store.GetJobBySandboxVMID(ctx, vmid); err == nil && job.WorkspaceID != nil {
		if workspace, err := m.store.GetWorkspace(ctx, *job.WorkspaceID); err == nil {
			id := job.ID
			add(workspace, workspaceLeaseOwnerForJob(id), &id)
		}
	}
	if owner := workspaceLeaseOwnerForSandbox(vmid); owner != "" {
		if workspace, err := m.store.GetWorkspaceByLeaseOwner(ctx, owner); err == nil {
			add(workspace, owner, nil)
		}
	}
	// Sessions can own a workspace independently of a job. A re-bound session is
	// excluded so destruction of its former sandbox cannot release its new lease.
	if sessions, err := m.store.ListSessions(ctx); err == nil {
		for _, session := range sessions {
			if session.CurrentVMID == nil || *session.CurrentVMID != vmid {
				continue
			}
			if workspace, err := m.store.GetWorkspace(ctx, session.WorkspaceID); err == nil {
				add(workspace, workspaceLeaseOwnerForSession(session.ID), nil)
			}
		}
	}
	return leases
}

func (m *SandboxManager) releaseWorkspaceLeaseSnapshots(ctx context.Context, vmid int, leases []workspaceLeaseSnapshot) {
	for _, lease := range leases {
		released, err := m.store.ReleaseWorkspaceLeaseForSandbox(ctx, lease.ID, lease.Owner, lease.Nonce, vmid)
		if err != nil {
			if m.logger != nil {
				m.logger.Printf("release workspace lease vmid=%d workspace=%s: %v", vmid, lease.ID, err)
			}
			continue
		}
		if released {
			recordWorkspaceLeaseEvent(ctx, m.store, "workspace.lease.released", &vmid, lease.JobID, lease.ID, lease.Owner, time.Time{})
		}
	}
}

func (m *SandboxManager) releaseWorkspaceLeases(ctx context.Context, vmid int) {
	m.releaseWorkspaceLeaseSnapshots(ctx, vmid, m.workspaceLeasesForSandbox(ctx, vmid))
}

func (m *SandboxManager) recordRevertEvent(ctx context.Context, vmid int, kind EventKind, msg string, payload revertEventPayload) {
	if m == nil || m.store == nil || kind == "" {
		return
	}
	_ = emitEvent(ctx, NewStoreEventRecorder(m.store), kind, &vmid, nil, msg, payload)
}

func (m *SandboxManager) recordLifecycleEvent(ctx context.Context, vmid int, kind EventKind, msg string, payload lifecycleEventPayload) {
	if m == nil || m.store == nil || kind == "" {
		return
	}
	_ = emitEvent(ctx, NewStoreEventRecorder(m.store), kind, &vmid, nil, msg, payload)
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
		return to == models.SandboxRunning || to == models.SandboxSuspended || to == models.SandboxStopped || to == models.SandboxTimeout || to == models.SandboxDestroyed
	case models.SandboxRunning:
		return to == models.SandboxSuspended || to == models.SandboxCompleted || to == models.SandboxFailed || to == models.SandboxTimeout || to == models.SandboxStopped || to == models.SandboxDestroyed
	case models.SandboxSuspended:
		return to == models.SandboxReady || to == models.SandboxRunning || to == models.SandboxStopped || to == models.SandboxTimeout || to == models.SandboxDestroyed
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

// ghostSandboxTTL is how long a REQUESTED sandbox whose VM does not exist in
// Proxmox is left alone before reconciliation reaps it as DESTROYED. Cloning
// plus first boot can take several minutes, so this must comfortably exceed
// the longest normal provisioning run.
const ghostSandboxTTL = 30 * time.Minute

// sandboxStaleFor reports how long the sandbox record has gone without a
// state update, falling back to CreatedAt when LastUpdatedAt is unset. A
// zero-time record reports zero so unknown-age rows are never reaped.
func sandboxStaleFor(sb models.Sandbox) time.Duration {
	ref := sb.LastUpdatedAt
	if ref.IsZero() || sb.CreatedAt.After(ref) {
		ref = sb.CreatedAt
	}
	if ref.IsZero() {
		return 0
	}
	return time.Since(ref)
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
	return foundPhrase
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
//   - VM not found and REQUESTED → mark as DESTROYED only after ghostSandboxTTL
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
				switch {
				case sb.State == models.SandboxDestroyed:
					// Already terminal; nothing to do.
				case sb.State == models.SandboxRequested && sandboxStaleFor(sb) <= ghostSandboxTTL:
					// Provisioning may not have cloned the VM yet; "missing"
					// is expected for a fresh REQUESTED row, so leave it alone
					// until it has been stale past the TTL.
				case sb.State == models.SandboxRequested:
					m.logger.Printf("reconcile: VM %d absent for %s in %s (no clone in progress), marking as destroyed", sb.VMID, sandboxStaleFor(sb).Round(time.Minute), sb.State)
					if err := m.Transition(ctx, sb.VMID, models.SandboxDestroyed); err == nil {
						m.cleanupExposures(ctx, sb.VMID)
					}
				default:
					m.logger.Printf("reconcile: VM %d not found in Proxmox, marking as destroyed", sb.VMID)
					if err := m.Transition(ctx, sb.VMID, models.SandboxDestroyed); err == nil {
						m.cleanupExposures(ctx, sb.VMID)
					}
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

func (m *SandboxManager) cleanupExposures(ctx context.Context, vmid int) {
	if m == nil || m.exposures == nil {
		return
	}
	if err := m.exposures.CleanupByVMID(ctx, vmid); err != nil {
		if m.logger != nil {
			m.logger.Printf("sandbox %d: exposure cleanup failed: %v", vmid, err)
		}
	}
}
