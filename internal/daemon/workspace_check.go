package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

const (
	workspaceCheckSeverityInfo    = "info"
	workspaceCheckSeverityWarning = "warning"
	workspaceCheckSeverityError   = "error"

	workspaceCheckCodeVolumeMissing     = "volume_missing"
	workspaceCheckCodeVolumeCheckFailed = "volume_check_failed"
	workspaceCheckCodeAttachedVMStale   = "attached_vmid_stale"
	workspaceCheckCodeVMissing          = "vm_missing"
	workspaceCheckCodeVMConfigFailed    = "vm_config_failed"
	workspaceCheckCodeSandboxDrift      = "sandbox_record_drift"
	workspaceCheckCodeMultiAttach       = "workspace_multi_attach"
	workspaceCheckCodeVolumeWrongSlot   = "volume_wrong_slot"
	workspaceCheckCodeVolumeNotAttached = "volume_not_attached"
)

// WorkspaceCheckResult captures reconciliation findings between DB and Proxmox.
type WorkspaceCheckResult struct {
	Workspace models.Workspace
	Volume    WorkspaceCheckVolume
	Findings  []WorkspaceCheckFinding
	CheckedAt time.Time
}

// WorkspaceCheckVolume contains workspace volume metadata.
type WorkspaceCheckVolume struct {
	VolumeID string
	Storage  string
	Path     string
	Exists   bool
}

// WorkspaceCheckFinding describes a reconciliation issue with suggested fixes.
type WorkspaceCheckFinding struct {
	Code        string
	Severity    string
	Message     string
	Details     map[string]string
	Remediation []WorkspaceCheckRemediation
}

// WorkspaceCheckRemediation is an actionable suggestion for a finding.
type WorkspaceCheckRemediation struct {
	Action  string
	Command string
	Note    string
}

// Check inspects workspace invariants against DB and Proxmox state.
func (m *WorkspaceManager) Check(ctx context.Context, idOrName string) (WorkspaceCheckResult, error) {
	if m == nil || m.store == nil {
		return WorkspaceCheckResult{}, errors.New("workspace manager unavailable")
	}
	if m.backend == nil {
		return WorkspaceCheckResult{}, errors.New("workspace backend unavailable")
	}
	workspace, err := m.Resolve(ctx, idOrName)
	if err != nil {
		return WorkspaceCheckResult{}, err
	}

	result := WorkspaceCheckResult{
		Workspace: workspace,
		CheckedAt: m.now().UTC(),
		Volume: WorkspaceCheckVolume{
			VolumeID: workspace.VolumeID,
			Storage:  workspace.Storage,
		},
	}

	result.Findings = append(result.Findings, m.checkWorkspaceVolume(ctx, workspace, &result.Volume)...)
	result.Findings = append(result.Findings, m.checkWorkspaceDB(ctx, workspace)...)
	result.Findings = append(result.Findings, m.checkWorkspaceVM(ctx, workspace)...)

	return result, nil
}

func (m *WorkspaceManager) checkWorkspaceVolume(ctx context.Context, workspace models.Workspace, volume *WorkspaceCheckVolume) []WorkspaceCheckFinding {
	var findings []WorkspaceCheckFinding
	volid := strings.TrimSpace(workspace.VolumeID)
	if volid == "" {
		return findings
	}
	info, err := m.backend.VolumeInfo(ctx, volid)
	if err != nil {
		if errors.Is(err, proxmox.ErrVolumeNotFound) {
			volume.Exists = false
			findings = append(findings, WorkspaceCheckFinding{
				Code:     workspaceCheckCodeVolumeMissing,
				Severity: workspaceCheckSeverityError,
				Message:  fmt.Sprintf("workspace volume %s not found in Proxmox storage", volid),
				Details: map[string]string{
					"volid":   volid,
					"storage": strings.TrimSpace(volume.Storage),
				},
				Remediation: []WorkspaceCheckRemediation{
					{
						Action:  "detach-workspace",
						Command: fmt.Sprintf("agentlab workspace detach %s", workspaceReference(workspace)),
						Note:    "Clears the attached_vmid in the database (non-destructive).",
					},
					{
						Action: "restore-volume",
						Note:   "Restore the missing volume in Proxmox or recreate the workspace from backup.",
					},
				},
			})
			return findings
		}
		findings = append(findings, WorkspaceCheckFinding{
			Code:     workspaceCheckCodeVolumeCheckFailed,
			Severity: workspaceCheckSeverityError,
			Message:  fmt.Sprintf("failed to inspect workspace volume %s", volid),
			Details: map[string]string{
				"volid": volid,
				"error": err.Error(),
			},
		})
		return findings
	}
	volume.Exists = true
	if strings.TrimSpace(info.Storage) != "" {
		volume.Storage = strings.TrimSpace(info.Storage)
	}
	volume.Path = strings.TrimSpace(info.Path)
	return findings
}

func (m *WorkspaceManager) checkWorkspaceDB(ctx context.Context, workspace models.Workspace) []WorkspaceCheckFinding {
	var findings []WorkspaceCheckFinding
	vmid := derefVMID(workspace.AttachedVM)

	sandboxes, err := m.store.ListSandboxes(ctx)
	if err != nil {
		return append(findings, WorkspaceCheckFinding{
			Code:     workspaceCheckCodeSandboxDrift,
			Severity: workspaceCheckSeverityError,
			Message:  "failed to load sandboxes for workspace reconciliation",
			Details: map[string]string{
				"error": err.Error(),
			},
		})
	}
	var referencing []models.Sandbox
	for _, sb := range sandboxes {
		if sb.WorkspaceID != nil && *sb.WorkspaceID == workspace.ID {
			referencing = append(referencing, sb)
		}
	}

	if len(referencing) > 1 {
		vmids := make([]string, 0, len(referencing))
		for _, sb := range referencing {
			vmids = append(vmids, strconv.Itoa(sb.VMID))
		}
		sort.Strings(vmids)
		findings = append(findings, WorkspaceCheckFinding{
			Code:     workspaceCheckCodeMultiAttach,
			Severity: workspaceCheckSeverityError,
			Message:  "workspace referenced by multiple sandboxes",
			Details: map[string]string{
				"vmids": strings.Join(vmids, ","),
			},
			Remediation: []WorkspaceCheckRemediation{
				{
					Action:  "detach-workspace",
					Command: fmt.Sprintf("agentlab workspace detach %s", workspaceReference(workspace)),
					Note:    "Detach the workspace and reattach it to the correct sandbox.",
				},
			},
		})
	}

	if vmid > 0 {
		sandbox, err := m.store.GetSandbox(ctx, vmid)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				findings = append(findings, WorkspaceCheckFinding{
					Code:     workspaceCheckCodeAttachedVMStale,
					Severity: workspaceCheckSeverityWarning,
					Message:  fmt.Sprintf("workspace attached_vmid %d has no matching sandbox record", vmid),
					Details: map[string]string{
						"attached_vmid": strconv.Itoa(vmid),
					},
					Remediation: []WorkspaceCheckRemediation{
						{
							Action:  "detach-workspace",
							Command: fmt.Sprintf("agentlab workspace detach %s", workspaceReference(workspace)),
							Note:    "Clears the attached_vmid in the database.",
						},
					},
				})
			} else {
				findings = append(findings, WorkspaceCheckFinding{
					Code:     workspaceCheckCodeSandboxDrift,
					Severity: workspaceCheckSeverityError,
					Message:  "failed to load sandbox for workspace reconciliation",
					Details: map[string]string{
						"error": err.Error(),
					},
				})
			}
		} else if sandbox.WorkspaceID == nil || *sandbox.WorkspaceID != workspace.ID {
			actual := "-"
			if sandbox.WorkspaceID != nil {
				actual = *sandbox.WorkspaceID
			}
			findings = append(findings, WorkspaceCheckFinding{
				Code:     workspaceCheckCodeSandboxDrift,
				Severity: workspaceCheckSeverityWarning,
				Message:  fmt.Sprintf("sandbox %d workspace_id does not match workspace", vmid),
				Details: map[string]string{
					"sandbox_workspace_id": actual,
					"workspace_id":         workspace.ID,
				},
				Remediation: []WorkspaceCheckRemediation{
					{
						Action:  "reattach-workspace",
						Command: fmt.Sprintf("agentlab workspace attach %s %d", workspaceReference(workspace), vmid),
						Note:    "Reattaches the workspace and updates the sandbox record.",
					},
				},
			})
		}
	} else if len(referencing) > 0 {
		vmids := make([]string, 0, len(referencing))
		for _, sb := range referencing {
			vmids = append(vmids, strconv.Itoa(sb.VMID))
		}
		sort.Strings(vmids)
		findings = append(findings, WorkspaceCheckFinding{
			Code:     workspaceCheckCodeSandboxDrift,
			Severity: workspaceCheckSeverityWarning,
			Message:  "workspace is marked detached but sandbox records still reference it",
			Details: map[string]string{
				"vmids": strings.Join(vmids, ","),
			},
			Remediation: []WorkspaceCheckRemediation{
				{
					Action:  "attach-workspace",
					Command: fmt.Sprintf("agentlab workspace attach %s %s", workspaceReference(workspace), vmids[0]),
					Note:    "Reattach the workspace to the intended sandbox.",
				},
				{
					Action:  "detach-workspace",
					Command: fmt.Sprintf("agentlab workspace detach %s", workspaceReference(workspace)),
					Note:    "Clear the workspace attachment if it should be detached.",
				},
			},
		})
	}

	return findings
}

func (m *WorkspaceManager) checkWorkspaceVM(ctx context.Context, workspace models.Workspace) []WorkspaceCheckFinding {
	var findings []WorkspaceCheckFinding
	vmid := derefVMID(workspace.AttachedVM)
	if vmid <= 0 {
		return findings
	}

	cfg, err := m.backend.VMConfig(ctx, proxmox.VMID(vmid))
	if err != nil {
		if errors.Is(err, proxmox.ErrVMNotFound) {
			findings = append(findings, WorkspaceCheckFinding{
				Code:     workspaceCheckCodeVMissing,
				Severity: workspaceCheckSeverityError,
				Message:  fmt.Sprintf("Proxmox VM %d not found", vmid),
				Details: map[string]string{
					"vmid": strconv.Itoa(vmid),
				},
				Remediation: []WorkspaceCheckRemediation{
					{
						Action:  "destroy-sandbox",
						Command: fmt.Sprintf("agentlab sandbox destroy --force %d", vmid),
						Note:    "Removes stale sandbox state if the VM was already deleted.",
					},
					{
						Action:  "detach-workspace",
						Command: fmt.Sprintf("agentlab workspace detach %s", workspaceReference(workspace)),
						Note:    "Clears the workspace attachment in the database.",
					},
				},
			})
			return findings
		}
		findings = append(findings, WorkspaceCheckFinding{
			Code:     workspaceCheckCodeVMConfigFailed,
			Severity: workspaceCheckSeverityError,
			Message:  fmt.Sprintf("failed to read VM %d configuration", vmid),
			Details: map[string]string{
				"vmid":  strconv.Itoa(vmid),
				"error": err.Error(),
			},
		})
		return findings
	}

	slots := parseDiskSlots(cfg)
	volid := strings.TrimSpace(workspace.VolumeID)
	if volid == "" {
		return findings
	}
	if slots[workspaceDiskSlot] == volid {
		return findings
	}
	actualSlot := findVolumeSlot(slots, volid)
	if actualSlot != "" {
		findings = append(findings, WorkspaceCheckFinding{
			Code:     workspaceCheckCodeVolumeWrongSlot,
			Severity: workspaceCheckSeverityWarning,
			Message:  fmt.Sprintf("workspace volume attached at %s instead of %s", actualSlot, workspaceDiskSlot),
			Details: map[string]string{
				"expected_slot": workspaceDiskSlot,
				"actual_slot":   actualSlot,
				"volid":         volid,
			},
			Remediation: []WorkspaceCheckRemediation{
				{
					Action:  "reattach-workspace",
					Command: fmt.Sprintf("agentlab workspace detach %s && agentlab workspace attach %s %d", workspaceReference(workspace), workspaceReference(workspace), vmid),
					Note:    "Reattaches the workspace to the expected slot.",
				},
			},
		})
		return findings
	}

	details := map[string]string{
		"expected_slot": workspaceDiskSlot,
		"volid":         volid,
	}
	if slotValue := strings.TrimSpace(slots[workspaceDiskSlot]); slotValue != "" {
		details["slot_volid"] = slotValue
	}
	findings = append(findings, WorkspaceCheckFinding{
		Code:     workspaceCheckCodeVolumeNotAttached,
		Severity: workspaceCheckSeverityWarning,
		Message:  fmt.Sprintf("workspace volume not attached to VM %d", vmid),
		Details:  details,
		Remediation: []WorkspaceCheckRemediation{
			{
				Action:  "attach-workspace",
				Command: fmt.Sprintf("agentlab workspace attach %s %d", workspaceReference(workspace), vmid),
				Note:    "Attaches the workspace volume to the sandbox.",
			},
		},
	})
	return findings
}

func parseDiskSlots(config map[string]string) map[string]string {
	slots := make(map[string]string)
	for key, value := range config {
		if !isDiskSlotKey(key) {
			continue
		}
		volid := extractVolumeID(value)
		if volid == "" {
			continue
		}
		slots[key] = volid
	}
	return slots
}

func isDiskSlotKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	prefixes := []string{"scsi", "virtio", "sata", "ide"}
	for _, prefix := range prefixes {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if len(key) == len(prefix) {
			return false
		}
		rest := key[len(prefix):]
		for _, r := range rest {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	return false
}

func extractVolumeID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parts := strings.Split(value, ",")
	if len(parts) == 0 {
		return ""
	}
	volid := strings.TrimSpace(parts[0])
	if volid == "" || volid == "none" {
		return ""
	}
	return volid
}

func findVolumeSlot(slots map[string]string, volid string) string {
	volid = strings.TrimSpace(volid)
	if volid == "" {
		return ""
	}
	for slot, candidate := range slots {
		if candidate == volid {
			return slot
		}
	}
	return ""
}

func workspaceReference(workspace models.Workspace) string {
	name := strings.TrimSpace(workspace.Name)
	if name != "" {
		return name
	}
	return workspace.ID
}

func derefVMID(vmid *int) int {
	if vmid == nil {
		return 0
	}
	return *vmid
}
