package daemon

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"log"
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
	ErrWorkspaceNotFound    = errors.New("workspace not found")
	ErrWorkspaceExists      = errors.New("workspace already exists")
	ErrWorkspaceAttached    = errors.New("workspace already attached")
	ErrWorkspaceNotAttached = errors.New("workspace not attached")
	ErrWorkspaceVMInUse     = errors.New("vmid already has workspace attached")
	ErrWorkspaceLeaseHeld   = errors.New("workspace lease already held")
)

// WorkspaceManager handles persistent workspace volumes.
type WorkspaceManager struct {
	store   *db.Store
	backend proxmox.Backend
	logger  *log.Logger
	now     func() time.Time
	rand    io.Reader
}

func NewWorkspaceManager(store *db.Store, backend proxmox.Backend, logger *log.Logger) *WorkspaceManager {
	if logger == nil {
		logger = log.Default()
	}
	return &WorkspaceManager{
		store:   store,
		backend: backend,
		logger:  logger,
		now:     time.Now,
		rand:    rand.Reader,
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
