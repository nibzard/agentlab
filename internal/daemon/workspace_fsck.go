package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
)

const (
	workspaceFSCKMethodHost = "host"

	workspaceFSCKModeReadOnly = "read-only"
	workspaceFSCKModeRepair   = "repair"

	workspaceFSCKStatusClean       = "clean"
	workspaceFSCKStatusRepaired    = "repaired"
	workspaceFSCKStatusNeedsRepair = "needs-repair"
	workspaceFSCKStatusFailed      = "failed"
)

type WorkspaceFSCKVolume struct {
	VolumeID string
	Storage  string
	Path     string
}

type WorkspaceFSCKResult struct {
	Workspace      models.Workspace
	Volume         WorkspaceFSCKVolume
	Method         string
	Mode           string
	Status         string
	ExitCode       int
	ExitSummary    string
	NeedsRepair    bool
	RebootRequired bool
	Command        string
	Output         string
	StartedAt      time.Time
	CompletedAt    time.Time
}

type workspaceFSCKRunner func(ctx context.Context, args []string) (string, int, error)

type workspaceFSCKTargetValidator func(path string) error

func defaultWorkspaceFSCKRunner(ctx context.Context, args []string) (string, int, error) {
	cmd := exec.CommandContext(ctx, "fsck", args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	result := strings.TrimSpace(output.String())
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return result, exitErr.ExitCode(), nil
		}
		return result, -1, err
	}
	return result, 0, nil
}

func defaultWorkspaceFSCKTargetValidator(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("workspace volume path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat workspace volume path: %w", err)
	}
	mode := info.Mode()
	if mode&os.ModeDevice == 0 || mode&os.ModeCharDevice != 0 {
		return fmt.Errorf("%w: %s", ErrWorkspaceFSCKUnsupported, path)
	}
	return nil
}

// FSCK runs a filesystem check against a detached workspace volume.
func (m *WorkspaceManager) FSCK(ctx context.Context, idOrName string, repair bool) (WorkspaceFSCKResult, error) {
	if m == nil || m.store == nil {
		return WorkspaceFSCKResult{}, errors.New("workspace manager unavailable")
	}
	if m.backend == nil {
		return WorkspaceFSCKResult{}, errors.New("workspace backend unavailable")
	}
	workspace, err := m.Resolve(ctx, idOrName)
	if err != nil {
		return WorkspaceFSCKResult{}, err
	}
	if workspace.AttachedVM != nil && *workspace.AttachedVM > 0 {
		return WorkspaceFSCKResult{}, ErrWorkspaceFSCKAttached
	}

	owner, nonce, err := m.acquireFSCKLease(ctx, workspace)
	if err != nil {
		return WorkspaceFSCKResult{}, err
	}
	defer m.releaseFSCKLease(ctx, workspace.ID, owner, nonce)

	latest, err := m.store.GetWorkspace(ctx, workspace.ID)
	if err == nil {
		workspace = latest
		if workspace.AttachedVM != nil && *workspace.AttachedVM > 0 {
			return WorkspaceFSCKResult{}, ErrWorkspaceFSCKAttached
		}
	}

	info, err := m.backend.VolumeInfo(ctx, workspace.VolumeID)
	if err != nil {
		return WorkspaceFSCKResult{}, err
	}
	path := strings.TrimSpace(info.Path)
	if path == "" {
		return WorkspaceFSCKResult{}, errors.New("workspace volume path unavailable")
	}
	if validator := m.fsckTargetValidator; validator != nil {
		if err := validator(path); err != nil {
			return WorkspaceFSCKResult{}, err
		}
	}
	if m.fsckRunner == nil {
		return WorkspaceFSCKResult{}, errors.New("fsck runner unavailable")
	}

	mode := workspaceFSCKModeReadOnly
	if repair {
		mode = workspaceFSCKModeRepair
	}
	args := []string{"-f"}
	if repair {
		args = append(args, "-y")
	} else {
		args = append(args, "-n")
	}
	args = append(args, path)

	started := m.now().UTC()
	result := WorkspaceFSCKResult{
		Workspace: workspace,
		Volume: WorkspaceFSCKVolume{
			VolumeID: info.VolumeID,
			Storage:  info.Storage,
			Path:     path,
		},
		Method:    workspaceFSCKMethodHost,
		Mode:      mode,
		Command:   workspaceFSCKCommand(args),
		StartedAt: started,
	}
	recordWorkspaceFSCKEvent(ctx, m.store, "workspace.fsck.started", result, nil)

	output, exitCode, runErr := m.fsckRunner(ctx, args)
	completed := m.now().UTC()
	result.CompletedAt = completed
	result.Output = output
	result.ExitCode = exitCode
	result.ExitSummary = fsckExitSummary(exitCode)
	result.Status, result.NeedsRepair, result.RebootRequired = fsckExitStatus(exitCode, repair)

	if runErr != nil {
		recordWorkspaceFSCKEvent(ctx, m.store, "workspace.fsck.failed", result, runErr)
		return WorkspaceFSCKResult{}, runErr
	}
	recordWorkspaceFSCKEvent(ctx, m.store, "workspace.fsck.completed", result, nil)
	return result, nil
}

func (m *WorkspaceManager) acquireFSCKLease(ctx context.Context, workspace models.Workspace) (string, string, error) {
	if m == nil || m.store == nil {
		return "", "", errors.New("workspace lease store unavailable")
	}
	if strings.TrimSpace(workspace.ID) == "" {
		return "", "", errors.New("workspace id is required")
	}
	owner := fmt.Sprintf("fsck:%s:%d", workspace.ID, m.now().UTC().UnixNano())
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

func (m *WorkspaceManager) releaseFSCKLease(ctx context.Context, workspaceID, owner, nonce string) {
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

func workspaceFSCKCommand(args []string) string {
	if len(args) == 0 {
		return "fsck"
	}
	return "fsck " + strings.Join(args, " ")
}

func fsckExitSummary(code int) string {
	if code == 0 {
		return "no filesystem errors detected"
	}
	parts := make([]string, 0, 4)
	if code&0x01 != 0 {
		parts = append(parts, "filesystem errors corrected")
	}
	if code&0x02 != 0 {
		parts = append(parts, "system should be rebooted")
	}
	if code&0x04 != 0 {
		parts = append(parts, "filesystem errors left uncorrected")
	}
	if code&0x08 != 0 {
		parts = append(parts, "operational error")
	}
	if code&0x10 != 0 {
		parts = append(parts, "usage or syntax error")
	}
	if code&0x20 != 0 {
		parts = append(parts, "fsck canceled by user")
	}
	if code&0x80 != 0 {
		parts = append(parts, "shared library error")
	}
	if len(parts) == 0 {
		return fmt.Sprintf("fsck exited with code %d", code)
	}
	return strings.Join(parts, "; ")
}

func fsckExitStatus(code int, repair bool) (string, bool, bool) {
	reboot := code&0x02 != 0
	failed := code&(0x08|0x10|0x20|0x80) != 0
	if code == 0 {
		return workspaceFSCKStatusClean, false, reboot
	}
	if failed {
		return workspaceFSCKStatusFailed, false, reboot
	}
	if repair {
		if code&0x04 != 0 {
			return workspaceFSCKStatusNeedsRepair, true, reboot
		}
		return workspaceFSCKStatusRepaired, false, reboot
	}
	return workspaceFSCKStatusNeedsRepair, true, reboot
}

func recordWorkspaceFSCKEvent(ctx context.Context, store *db.Store, kind string, result WorkspaceFSCKResult, err error) {
	if store == nil {
		return
	}
	payload := struct {
		WorkspaceID    string `json:"workspace_id"`
		VolumeID       string `json:"volume_id,omitempty"`
		Path           string `json:"path,omitempty"`
		Method         string `json:"method,omitempty"`
		Mode           string `json:"mode,omitempty"`
		Status         string `json:"status,omitempty"`
		ExitCode       int    `json:"exit_code,omitempty"`
		ExitSummary    string `json:"exit_summary,omitempty"`
		NeedsRepair    bool   `json:"needs_repair,omitempty"`
		RebootRequired bool   `json:"reboot_required,omitempty"`
		Command        string `json:"command,omitempty"`
		Error          string `json:"error,omitempty"`
	}{
		WorkspaceID:    result.Workspace.ID,
		VolumeID:       result.Volume.VolumeID,
		Path:           result.Volume.Path,
		Method:         result.Method,
		Mode:           result.Mode,
		Status:         result.Status,
		ExitCode:       result.ExitCode,
		ExitSummary:    result.ExitSummary,
		NeedsRepair:    result.NeedsRepair,
		RebootRequired: result.RebootRequired,
		Command:        result.Command,
	}
	msg := fmt.Sprintf("workspace_id=%s mode=%s status=%s", result.Workspace.ID, result.Mode, result.Status)
	if err != nil {
		errMsg := err.Error()
		payload.Error = errMsg
		msg = fmt.Sprintf("workspace_id=%s mode=%s error=%s", result.Workspace.ID, result.Mode, errMsg)
	}
	eventKind := EventKindWorkspaceFSCKStarted
	switch strings.TrimSpace(kind) {
	case string(EventKindWorkspaceFSCKStarted):
		eventKind = EventKindWorkspaceFSCKStarted
	case string(EventKindWorkspaceFSCKFailed):
		eventKind = EventKindWorkspaceFSCKFailed
	case string(EventKindWorkspaceFSCKDone):
		eventKind = EventKindWorkspaceFSCKDone
	default:
		return
	}
	_ = emitEvent(ctx, NewStoreEventRecorder(store), eventKind, nil, nil, msg, payload)
}
