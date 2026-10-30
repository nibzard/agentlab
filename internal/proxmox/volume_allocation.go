package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// VolumeOwnerInventory includes virtual machines and containers on every node.
// Workspace storage owners must not collide with either resource type.
type VolumeOwnerInventory interface {
	VolumeOwnerVMIDs(context.Context) ([]VMID, error)
}

var allocationStoragePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
var allocationNamePattern = regexp.MustCompile(`^vm-([1-9][0-9]*)-([A-Za-z0-9][A-Za-z0-9_-]{0,127})$`)

func volumeAllocationOwner(name string) (int, error) {
	match := allocationNamePattern.FindStringSubmatch(name)
	if match == nil {
		return 0, errors.New("volume allocation requires an owner-prefixed disk name")
	}
	owner, err := strconv.Atoi(match[1])
	if err != nil || owner < 100 || owner > 999999999 {
		return 0, errors.New("volume owner must be between 100 and 999999999")
	}
	return owner, nil
}

func volumeAllocationTarget(storage, storageType, name string) (int, string, string, error) {
	owner, err := volumeAllocationOwner(name)
	if err != nil {
		return 0, "", "", err
	}
	if !allocationStoragePattern.MatchString(storage) {
		return 0, "", "", errors.New("invalid volume storage identifier")
	}
	switch strings.ToLower(strings.TrimSpace(storageType)) {
	case "zfspool", "zfs", "lvm", "lvmthin", "rbd":
		return owner, name, storage + ":" + name, nil
	case "dir", "nfs", "cifs", "btrfs", "cephfs":
		filename := name + ".raw"
		return owner, filename, fmt.Sprintf("%s:%d/%s", storage, owner, filename), nil
	default:
		return 0, "", "", fmt.Errorf("%w: allocation storage type %s", ErrStorageUnsupported, storageType)
	}
}

func parseVolumeAllocationReceipt(data []byte, expected string) (string, error) {
	var volume string
	if err := json.Unmarshal(data, &volume); err != nil || volume != expected {
		return "", errors.New("volume allocation returned an invalid or mismatched receipt")
	}
	return volume, nil
}

func parseVolumeOwnerInventory(data []byte) ([]VMID, error) {
	var entries []struct {
		VMID int    `json:"vmid"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse cluster volume owners: %w", err)
	}
	if entries == nil {
		return nil, errors.New("cluster volume owner inventory is missing")
	}
	result := make([]VMID, 0, len(entries))
	for _, entry := range entries {
		if entry.Type != "qemu" && entry.Type != "lxc" {
			return nil, errors.New("unexpected cluster resource type in volume owner inventory")
		}
		if entry.VMID < 100 || entry.VMID > 999999999 {
			return nil, errors.New("invalid VM ID in volume owner inventory")
		}
		result = append(result, VMID(entry.VMID))
	}
	return result, nil
}

func (b *APIBackend) VolumeOwnerVMIDs(ctx context.Context) ([]VMID, error) {
	data, err := b.doGet(ctx, "/cluster/resources?type=vm")
	if err != nil {
		return nil, err
	}
	return parseVolumeOwnerInventory(data)
}
func (b *ShellBackend) VolumeOwnerVMIDs(ctx context.Context) ([]VMID, error) {
	output, err := b.run(ctx, b.pveshPath(), "get", "/cluster/resources", "--type", "vm", "--output-format", "json")
	if err != nil {
		return nil, err
	}
	return parseVolumeOwnerInventory([]byte(output))
}

// allocationStorageType uses the API serializer. The pvesm status command has
// a human table printer and does not provide this JSON contract.
func (b *ShellBackend) allocationStorageType(ctx context.Context, storage string) (string, error) {
	node, err := b.localStorageNode(ctx)
	if err != nil {
		return "", err
	}
	output, err := b.run(ctx, b.pveshPath(), "get", fmt.Sprintf("/nodes/%s/storage/%s/status", node, storage), "--output-format", "json")
	if err != nil {
		return "", err
	}
	var status struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(output), &status); err != nil || status.Type == "" {
		return "", errors.New("invalid allocation storage status")
	}
	return status.Type, nil
}

// VolumePreflightError proves that allocation did not reach a mutating call.
// Backend response errors must never be wrapped with this type.
type VolumePreflightError struct{ Cause error }

func (e *VolumePreflightError) Error() string { return e.Cause.Error() }
func (e *VolumePreflightError) Unwrap() error { return e.Cause }
func volumePreflight(err error) error         { return &VolumePreflightError{Cause: err} }

// WorkspaceVolumeOwnerFromID decodes names created by AgentLab. Legacy IDs are
// left unchanged and do not acquire a new owner reservation during a read.
func WorkspaceVolumeOwnerFromID(volumeID string) (int, string, bool) {
	pair := strings.SplitN(volumeID, ":", 2)
	if len(pair) != 2 || !allocationStoragePattern.MatchString(pair[0]) {
		return 0, "", false
	}
	parts := strings.Split(pair[1], "/")
	if len(parts) > 2 {
		return 0, "", false
	}
	name := strings.TrimSuffix(parts[len(parts)-1], ".raw")
	owner, err := volumeAllocationOwner(name)
	if err != nil || !strings.HasPrefix(name, fmt.Sprintf("vm-%d-agentlab-workspace-", owner)) {
		return 0, "", false
	}
	if len(parts) == 2 && parts[0] != strconv.Itoa(owner) {
		return 0, "", false
	}
	return owner, name, true
}
