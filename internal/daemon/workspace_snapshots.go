package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
)

// SnapshotCreate creates a snapshot for the workspace volume.
func (m *WorkspaceManager) SnapshotCreate(ctx context.Context, idOrName, snapshotName string) (models.WorkspaceSnapshot, error) {
	if m == nil || m.store == nil {
		return models.WorkspaceSnapshot{}, errors.New("workspace manager unavailable")
	}
	if m.backend == nil {
		return models.WorkspaceSnapshot{}, errors.New("workspace backend unavailable")
	}
	snapshotName = strings.TrimSpace(snapshotName)
	if snapshotName == "" {
		return models.WorkspaceSnapshot{}, errors.New("snapshot name is required")
	}
	workspace, err := m.Resolve(ctx, idOrName)
	if err != nil {
		return models.WorkspaceSnapshot{}, err
	}
	if workspace.AttachedVM != nil && *workspace.AttachedVM > 0 {
		return models.WorkspaceSnapshot{}, ErrWorkspaceSnapshotAttached
	}

	owner, nonce, err := m.acquireSnapshotLease(ctx, workspace)
	if err != nil {
		return models.WorkspaceSnapshot{}, err
	}
	defer m.releaseSnapshotLease(ctx, workspace.ID, owner, nonce)

	latest, err := m.store.GetWorkspace(ctx, workspace.ID)
	if err == nil && latest.AttachedVM != nil && *latest.AttachedVM > 0 {
		return models.WorkspaceSnapshot{}, ErrWorkspaceSnapshotAttached
	}

	start := m.now().UTC()
	snapshot := models.WorkspaceSnapshot{
		WorkspaceID: workspace.ID,
		Name:        snapshotName,
		BackendRef:  snapshotName,
		CreatedAt:   start,
	}

	if err := m.backend.VolumeSnapshotCreate(ctx, workspace.VolumeID, snapshot.BackendRef); err != nil {
		recordWorkspaceSnapshotEvent(ctx, m.store, "workspace.snapshot.create_failed", snapshot, err)
		return models.WorkspaceSnapshot{}, err
	}

	if err := m.store.CreateWorkspaceSnapshot(ctx, snapshot); err != nil {
		if isUniqueConstraint(err) {
			recordWorkspaceSnapshotEvent(ctx, m.store, "workspace.snapshot.create_failed", snapshot, ErrWorkspaceSnapshotExists)
			return models.WorkspaceSnapshot{}, ErrWorkspaceSnapshotExists
		}
		_ = m.backend.VolumeSnapshotDelete(ctx, workspace.VolumeID, snapshot.BackendRef)
		recordWorkspaceSnapshotEvent(ctx, m.store, "workspace.snapshot.create_failed", snapshot, err)
		return models.WorkspaceSnapshot{}, err
	}
	recordWorkspaceSnapshotEvent(ctx, m.store, "workspace.snapshot.created", snapshot, nil)
	return snapshot, nil
}

// SnapshotList returns snapshots for a workspace.
func (m *WorkspaceManager) SnapshotList(ctx context.Context, idOrName string) ([]models.WorkspaceSnapshot, error) {
	if m == nil || m.store == nil {
		return nil, errors.New("workspace manager unavailable")
	}
	workspace, err := m.Resolve(ctx, idOrName)
	if err != nil {
		return nil, err
	}
	return m.store.ListWorkspaceSnapshots(ctx, workspace.ID)
}

// SnapshotRestore restores a workspace volume to a named snapshot.
func (m *WorkspaceManager) SnapshotRestore(ctx context.Context, idOrName, snapshotName string) (models.WorkspaceSnapshot, error) {
	if m == nil || m.store == nil {
		return models.WorkspaceSnapshot{}, errors.New("workspace manager unavailable")
	}
	if m.backend == nil {
		return models.WorkspaceSnapshot{}, errors.New("workspace backend unavailable")
	}
	snapshotName = strings.TrimSpace(snapshotName)
	if snapshotName == "" {
		return models.WorkspaceSnapshot{}, errors.New("snapshot name is required")
	}
	workspace, err := m.Resolve(ctx, idOrName)
	if err != nil {
		return models.WorkspaceSnapshot{}, err
	}
	if workspace.AttachedVM != nil && *workspace.AttachedVM > 0 {
		return models.WorkspaceSnapshot{}, ErrWorkspaceSnapshotAttached
	}
	snapshot, err := m.store.GetWorkspaceSnapshot(ctx, workspace.ID, snapshotName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.WorkspaceSnapshot{}, ErrWorkspaceSnapshotNotFound
		}
		return models.WorkspaceSnapshot{}, err
	}

	owner, nonce, err := m.acquireSnapshotLease(ctx, workspace)
	if err != nil {
		return models.WorkspaceSnapshot{}, err
	}
	defer m.releaseSnapshotLease(ctx, workspace.ID, owner, nonce)

	latest, err := m.store.GetWorkspace(ctx, workspace.ID)
	if err == nil && latest.AttachedVM != nil && *latest.AttachedVM > 0 {
		return models.WorkspaceSnapshot{}, ErrWorkspaceSnapshotAttached
	}

	if err := m.backend.VolumeSnapshotRestore(ctx, workspace.VolumeID, snapshot.BackendRef); err != nil {
		recordWorkspaceSnapshotEvent(ctx, m.store, "workspace.snapshot.restore_failed", snapshot, err)
		return models.WorkspaceSnapshot{}, err
	}
	recordWorkspaceSnapshotEvent(ctx, m.store, "workspace.snapshot.restored", snapshot, nil)
	return snapshot, nil
}

func (m *WorkspaceManager) acquireSnapshotLease(ctx context.Context, workspace models.Workspace) (string, string, error) {
	if m == nil || m.store == nil {
		return "", "", errors.New("workspace lease store unavailable")
	}
	if strings.TrimSpace(workspace.ID) == "" {
		return "", "", errors.New("workspace id is required")
	}
	owner := fmt.Sprintf("snapshot:%s:%d", workspace.ID, m.now().UTC().UnixNano())
	nonce, err := newWorkspaceLeaseNonce(m.rand)
	if err != nil {
		return "", "", err
	}
	expiresAt := m.now().UTC().Add(workspaceLeaseDefaultTTL)
	acquired, err := m.store.TryAcquireWorkspaceLease(ctx, workspace.ID, owner, nonce, expiresAt)
	if err != nil {
		return "", "", err
	}
	if !acquired {
		return "", "", ErrWorkspaceLeaseHeld
	}
	return owner, nonce, nil
}

func (m *WorkspaceManager) releaseSnapshotLease(ctx context.Context, workspaceID, owner, nonce string) {
	if m == nil || m.store == nil {
		return
	}
	workspaceID = strings.TrimSpace(workspaceID)
	owner = strings.TrimSpace(owner)
	nonce = strings.TrimSpace(nonce)
	if workspaceID == "" || owner == "" || nonce == "" {
		return
	}
	_, _ = m.store.ReleaseWorkspaceLease(ctx, workspaceID, owner, nonce)
}

func recordWorkspaceSnapshotEvent(ctx context.Context, store *db.Store, kind string, snapshot models.WorkspaceSnapshot, err error) {
	if store == nil {
		return
	}
	msg := fmt.Sprintf("workspace_id=%s snapshot=%s", snapshot.WorkspaceID, snapshot.Name)
	payload := struct {
		WorkspaceID string `json:"workspace_id"`
		Name        string `json:"name"`
		BackendRef  string `json:"backend_ref"`
		Error       string `json:"error,omitempty"`
	}{
		WorkspaceID: snapshot.WorkspaceID,
		Name:        snapshot.Name,
		BackendRef:  snapshot.BackendRef,
	}
	if err != nil {
		errMsg := err.Error()
		msg = fmt.Sprintf("workspace_id=%s snapshot=%s error=%s", snapshot.WorkspaceID, snapshot.Name, errMsg)
		payload.Error = errMsg
	}
	var eventKind EventKind
	switch strings.TrimSpace(kind) {
	case string(EventKindWorkspaceSnapshotCreated):
		eventKind = EventKindWorkspaceSnapshotCreated
	case string(EventKindWorkspaceSnapshotFailed):
		eventKind = EventKindWorkspaceSnapshotFailed
	case string(EventKindWorkspaceSnapshotRestored):
		eventKind = EventKindWorkspaceSnapshotRestored
	case string(EventKindWorkspaceSnapshotRestoreFailed):
		eventKind = EventKindWorkspaceSnapshotRestoreFailed
	default:
		return
	}
	_ = emitEvent(ctx, NewStoreEventRecorder(store), eventKind, nil, nil, msg, payload)
}
