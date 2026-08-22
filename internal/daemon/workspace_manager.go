package daemon

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

const (
	defaultWorkspaceStorage = "local-zfs"
	workspaceDiskSlot       = "scsi1"
	workspaceIDBytes        = 8
)

var (
	ErrWorkspaceNotFound         = errors.New("workspace not found")
	ErrWorkspaceExists           = errors.New("workspace already exists")
	ErrWorkspaceAttached         = errors.New("workspace already attached")
	ErrWorkspaceNotAttached      = errors.New("workspace not attached")
	ErrWorkspaceVMInUse          = errors.New("vmid already has workspace attached")
	ErrWorkspaceLeaseHeld        = errors.New("workspace lease already held")
	ErrWorkspaceSnapshotExists   = errors.New("workspace snapshot already exists")
	ErrWorkspaceSnapshotNotFound = errors.New("workspace snapshot not found")
	ErrWorkspaceSnapshotAttached = errors.New("workspace must be detached for snapshot operations")
	ErrWorkspaceForkAttached     = errors.New("workspace must be detached for fork operations")
	ErrWorkspaceFSCKAttached     = errors.New("workspace must be detached for fsck")
	ErrWorkspaceFSCKUnsupported  = errors.New("workspace fsck unsupported for volume path")
)

// WorkspaceManager handles persistent workspace volumes.
type WorkspaceManager struct {
	store               *db.Store
	backend             proxmox.Backend
	logger              *log.Logger
	now                 func() time.Time
	rand                io.Reader
	fsckRunner          workspaceFSCKRunner
	fsckTargetValidator workspaceFSCKTargetValidator
}

func NewWorkspaceManager(store *db.Store, backend proxmox.Backend, logger *log.Logger) *WorkspaceManager {
	if logger == nil {
		logger = log.Default()
	}
	return &WorkspaceManager{
		store:               store,
		backend:             backend,
		logger:              logger,
		now:                 time.Now,
		rand:                rand.Reader,
		fsckRunner:          defaultWorkspaceFSCKRunner,
		fsckTargetValidator: defaultWorkspaceFSCKTargetValidator,
	}
}

func (m *WorkspaceManager) Create(ctx context.Context, name, storage string, sizeGB int) (models.Workspace, error) {
	if m == nil || m.store == nil {
		return models.Workspace{}, errors.New("workspace manager unavailable")
	}
	if m.backend == nil {
		return models.Workspace{}, errors.New("workspace backend unavailable")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return models.Workspace{}, errors.New("workspace name is required")
	}
	if sizeGB <= 0 {
		return models.Workspace{}, errors.New("size_gb must be positive")
	}
	storage = strings.TrimSpace(storage)
	if storage == "" {
		storage = defaultWorkspaceStorage
	}

	if _, err := m.store.GetWorkspaceByName(ctx, name); err == nil {
		return models.Workspace{}, ErrWorkspaceExists
	} else if !errors.Is(err, sql.ErrNoRows) {
		return models.Workspace{}, err
	}

	id, err := newWorkspaceID(m.rand)
	if err != nil {
		return models.Workspace{}, err
	}

	volid, err := m.backend.CreateVolume(ctx, storage, id, sizeGB)
	if err != nil {
		return models.Workspace{}, err
	}

	now := m.now().UTC()
	workspace := models.Workspace{
		ID:          id,
		Name:        name,
		Storage:     storage,
		VolumeID:    volid,
		SizeGB:      sizeGB,
		CreatedAt:   now,
		LastUpdated: now,
	}

	if err := m.store.CreateWorkspace(ctx, workspace); err != nil {
		if isUniqueConstraint(err) {
			_ = m.backend.DeleteVolume(ctx, volid)
			return models.Workspace{}, ErrWorkspaceExists
		}
		_ = m.backend.DeleteVolume(ctx, volid)
		return models.Workspace{}, err
	}
	return workspace, nil
}

func (m *WorkspaceManager) Resolve(ctx context.Context, idOrName string) (models.Workspace, error) {
	if m == nil || m.store == nil {
		return models.Workspace{}, errors.New("workspace manager unavailable")
	}
	idOrName = strings.TrimSpace(idOrName)
	if idOrName == "" {
		return models.Workspace{}, errors.New("workspace id is required")
	}
	workspace, err := m.store.GetWorkspace(ctx, idOrName)
	if err == nil {
		return workspace, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return models.Workspace{}, err
	}
	workspace, err = m.store.GetWorkspaceByName(ctx, idOrName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Workspace{}, ErrWorkspaceNotFound
		}
		return models.Workspace{}, err
	}
	return workspace, nil
}

func (m *WorkspaceManager) List(ctx context.Context) ([]models.Workspace, error) {
	if m == nil || m.store == nil {
		return nil, errors.New("workspace manager unavailable")
	}
	return m.store.ListWorkspaces(ctx)
}

func (m *WorkspaceManager) Attach(ctx context.Context, idOrName string, vmid int) (models.Workspace, error) {
	if m == nil || m.store == nil {
		return models.Workspace{}, errors.New("workspace manager unavailable")
	}
	if m.backend == nil {
		return models.Workspace{}, errors.New("workspace backend unavailable")
	}
	if vmid <= 0 {
		return models.Workspace{}, errors.New("vmid must be positive")
	}
	workspace, err := m.Resolve(ctx, idOrName)
	if err != nil {
		return models.Workspace{}, err
	}
	if workspace.AttachedVM != nil {
		if *workspace.AttachedVM == vmid {
			return workspace, nil
		}
		return models.Workspace{}, ErrWorkspaceAttached
	}
	if err := m.checkAttachLease(ctx, workspace, vmid); err != nil {
		return models.Workspace{}, err
	}
	if _, err := m.store.GetSandbox(ctx, vmid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Workspace{}, ErrSandboxNotFound
		}
		return models.Workspace{}, err
	}
	if existing, err := m.store.GetWorkspaceByAttachedVMID(ctx, vmid); err == nil && existing.ID != workspace.ID {
		return models.Workspace{}, ErrWorkspaceVMInUse
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return models.Workspace{}, err
	}

	if err := m.backend.AttachVolume(ctx, proxmox.VMID(vmid), workspace.VolumeID, workspaceDiskSlot); err != nil {
		return models.Workspace{}, err
	}
	attached, err := m.store.AttachWorkspace(ctx, workspace.ID, vmid)
	if err != nil {
		_ = m.backend.DetachVolume(ctx, proxmox.VMID(vmid), workspaceDiskSlot)
		return models.Workspace{}, err
	}
	if !attached {
		_ = m.backend.DetachVolume(ctx, proxmox.VMID(vmid), workspaceDiskSlot)
		return models.Workspace{}, ErrWorkspaceAttached
	}
	if err := m.store.UpdateSandboxWorkspace(ctx, vmid, &workspace.ID); err != nil {
		_ = m.backend.DetachVolume(ctx, proxmox.VMID(vmid), workspaceDiskSlot)
		_, _ = m.store.DetachWorkspace(ctx, workspace.ID, vmid)
		if errors.Is(err, sql.ErrNoRows) {
			return models.Workspace{}, ErrSandboxNotFound
		}
		return models.Workspace{}, err
	}
	return m.store.GetWorkspace(ctx, workspace.ID)
}

func (m *WorkspaceManager) Detach(ctx context.Context, idOrName string) (models.Workspace, error) {
	if m == nil || m.store == nil {
		return models.Workspace{}, errors.New("workspace manager unavailable")
	}
	if m.backend == nil {
		return models.Workspace{}, errors.New("workspace backend unavailable")
	}
	workspace, err := m.Resolve(ctx, idOrName)
	if err != nil {
		return models.Workspace{}, err
	}
	if workspace.AttachedVM == nil || *workspace.AttachedVM == 0 {
		return workspace, nil
	}
	vmid := *workspace.AttachedVM
	if err := m.backend.DetachVolume(ctx, proxmox.VMID(vmid), workspaceDiskSlot); err != nil {
		if !errors.Is(err, proxmox.ErrVMNotFound) {
			return models.Workspace{}, err
		}
	}
	detached, err := m.store.DetachWorkspace(ctx, workspace.ID, vmid)
	if err != nil {
		return models.Workspace{}, err
	}
	if !detached {
		return models.Workspace{}, ErrWorkspaceNotAttached
	}
	if err := m.store.UpdateSandboxWorkspace(ctx, vmid, nil); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return models.Workspace{}, err
	}
	return m.store.GetWorkspace(ctx, workspace.ID)
}

func (m *WorkspaceManager) DetachFromVM(ctx context.Context, vmid int) error {
	if m == nil || m.store == nil {
		return errors.New("workspace manager unavailable")
	}
	if vmid <= 0 {
		return errors.New("vmid must be positive")
	}
	workspace, err := m.store.GetWorkspaceByAttachedVMID(ctx, vmid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	_, err = m.Detach(ctx, workspace.ID)
	return err
}

// checkAttachLease mirrors the lease check RebindWorkspace performs: a
// workspace whose lease is live cannot be attached anywhere except to the
// sandbox the lease belongs to. A lease held by a sandbox covers only that
// sandbox. A lease held by a job or a session covers the sandbox that job
// or session is bound to. Rebind acquires the lease for the target sandbox
// before it attaches, and the orchestrator binds the job (and, for
// session-backed jobs, the session) to the target sandbox before it
// attaches, so those internal flows still pass.
func (m *WorkspaceManager) checkAttachLease(ctx context.Context, workspace models.Workspace, vmid int) error {
	owner := strings.TrimSpace(workspace.LeaseOwner)
	if owner == "" {
		return nil
	}
	if workspace.LeaseExpires.IsZero() || !workspace.LeaseExpires.After(m.now().UTC()) {
		return nil
	}
	allowed, err := m.leaseOwnerAllowsAttach(ctx, owner, vmid)
	if err != nil {
		return err
	}
	if allowed {
		return nil
	}
	return fmt.Errorf("%w by %s", ErrWorkspaceLeaseHeld, owner)
}

// leaseOwnerAllowsAttach reports whether a live lease held by owner still
// permits attaching the workspace to vmid. Unknown owners and owners that
// cannot be resolved fail closed, like the lease acquisition in rebind.
func (m *WorkspaceManager) leaseOwnerAllowsAttach(ctx context.Context, owner string, vmid int) (bool, error) {
	if leasedVMID, ok := workspaceLeaseSandboxOwner(owner); ok {
		return leasedVMID == vmid, nil
	}
	jobID, isJob := strings.CutPrefix(owner, "job:")
	if isJob {
		job, err := m.jobBoundToSandbox(ctx, vmid)
		if err != nil {
			return false, err
		}
		return job.ID == jobID, nil
	}
	sessionID, isSession := strings.CutPrefix(owner, "session:")
	if isSession {
		session, err := m.store.GetSession(ctx, sessionID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, nil
			}
			return false, err
		}
		if session.CurrentVMID != nil && *session.CurrentVMID == vmid {
			return true, nil
		}
		// A session-backed job binds the session's next sandbox before it
		// attaches the workspace, before the session row is updated.
		job, err := m.jobBoundToSandbox(ctx, vmid)
		if err != nil {
			return false, err
		}
		return job.SessionID != nil && *job.SessionID == sessionID, nil
	}
	return false, nil
}

// jobBoundToSandbox loads the job that a sandbox is provisioned for. An
// unbound sandbox reports an empty job.
func (m *WorkspaceManager) jobBoundToSandbox(ctx context.Context, vmid int) (models.Job, error) {
	job, err := m.store.GetJobBySandboxVMID(ctx, vmid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Job{}, nil
		}
		return models.Job{}, err
	}
	return job, nil
}

// workspaceLeaseSandboxOwner reports the VMID encoded in a sandbox lease
// owner and whether the owner identifies a sandbox at all.
func workspaceLeaseSandboxOwner(owner string) (int, bool) {
	value, found := strings.CutPrefix(strings.TrimSpace(owner), "sandbox:")
	if !found {
		return 0, false
	}
	vmid, err := strconv.Atoi(value)
	if err != nil || vmid <= 0 {
		return 0, false
	}
	return vmid, true
}

func newWorkspaceID(r io.Reader) (string, error) {
	if r == nil {
		r = rand.Reader
	}
	buf := make([]byte, workspaceIDBytes)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return "workspace-" + hex.EncodeToString(buf), nil
}
