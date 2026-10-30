package proxmox

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/agentlab/agentlab/internal/protocol"
)

//go:embed workspace_storage.pl
var workspaceStorageProgram string

var workspaceSourcePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*:vm-[0-9]+-[A-Za-z0-9_.-]+$`)
var workspaceGUIDPattern = regexp.MustCompile(`^[0-9]+$`)
var workspaceSnapshotPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

type WorkspaceClonePlanner interface {
	PlanWorkspaceClone(context.Context, string, string, int) (string, error)
}

type StorageOperation = protocol.StorageOperation

type WorkspaceCopyBackend interface {
	WorkspaceClonePlanner
	CopyWorkspaceVolume(context.Context, string, string, string) (StorageOperation, error)
}

// WorkspaceCopyRecoveryBackend resumes only a retained native copy intent.
type WorkspaceCopyRecoveryBackend interface {
	RecoverWorkspaceVolume(context.Context, StorageOperation) (StorageOperation, error)
}

type StorageOperationError struct {
	Receipt StorageOperation
	Cause   error
}

func (e *StorageOperationError) Error() string {
	return "workspace storage operation requires inspection; preserve the recorded volumes and snapshots"
}
func (e *StorageOperationError) Unwrap() error { return e.Cause }

func validateWorkspaceStorage(operation, source, snapshot, target string) error {
	if !workspaceSourcePattern.MatchString(source) || !workspaceSnapshotPattern.MatchString(snapshot) {
		return errors.New("invalid workspace volume or snapshot name")
	}
	switch operation {
	case "snapshot", "restore", "delete":
		if target != "" {
			return errors.New("unexpected workspace storage target")
		}
	case "copy", "copy-snapshot":
		pair := strings.SplitN(target, ":", 2)
		if len(pair) != 2 || pair[0] != volumeStorage(source) {
			return fmt.Errorf("%w: workspace copy requires the same storage", ErrStorageUnsupported)
		}
		if _, err := volumeAllocationOwner(pair[1]); err != nil {
			return err
		}
		if !strings.Contains(pair[1], "-agentlab-") || target == source {
			return errors.New("workspace copy requires a new reserved target")
		}
	default:
		return errors.New("invalid workspace storage operation")
	}
	return nil
}

func (b *ShellBackend) workspaceStorageReceipt(ctx context.Context, operation, source, snapshot, target string) (StorageOperation, error) {
	return b.workspaceStorageBound(ctx, operation, source, snapshot, target, StorageOperation{})
}
func (b *ShellBackend) workspaceStorageBound(ctx context.Context, operation, source, snapshot, target string, expected StorageOperation) (StorageOperation, error) {
	for _, guid := range []string{expected.SourceGUID, expected.SnapshotGUID, expected.TargetGUID} {
		if guid != "" && !workspaceGUIDPattern.MatchString(guid) {
			return StorageOperation{}, errors.New("invalid expected storage identity")
		}
	}
	if err := validateWorkspaceStorage(operation, source, snapshot, target); err != nil {
		return StorageOperation{}, err
	}
	node, err := b.localStorageNode(ctx)
	if err != nil {
		return StorageOperation{}, err
	}
	receipt := StorageOperation{Version: 1, Operation: operation, Source: source, Snapshot: snapshot, Target: target, State: "unknown", Cleanup: "inspection_required"}
	work, cancel := b.withCommandTimeout(ctx)
	defer cancel()
	// Only this embedded program bypasses the generic control-character check.
	// All variable arguments are separately validated and remain argument values.
	root := b.workspaceCopyStateRoot
	if root == "" {
		root = "/var/lib/agentlab/workspace-copies"
	}
	output, err := b.runner().Run(work, "/usr/bin/perl", "-T", "-e", workspaceStorageProgram, "--", node, operation, source, snapshot, target, root, expected.SourceGUID, expected.SnapshotGUID, expected.TargetGUID)
	if err != nil {
		return receipt, &StorageOperationError{Receipt: receipt, Cause: err}
	}
	var result StorageOperation
	if err := json.Unmarshal([]byte(output), &result); err != nil || result.Version != 1 || result.Operation != operation || result.Source != source || result.Snapshot != snapshot || result.Target != target {
		return receipt, &StorageOperationError{Receipt: receipt, Cause: errors.New("invalid storage operation receipt")}
	}
	if !result.OK || result.State != "verified" {
		cause := errors.New("storage operation was not verified")
		if result.Code == "workspace_storage_unsupported" || result.Code == "workspace_storage_encryption_unsupported" {
			cause = ErrStorageUnsupported
		}
		return result, &StorageOperationError{Receipt: result, Cause: cause}
	}
	if result.SourceGUID == "" || ((operation == "snapshot" || operation == "copy" || operation == "copy-snapshot") && result.SnapshotGUID == "") || ((operation == "copy" || operation == "copy-snapshot") && (result.TargetGUID == "" || result.Cleanup != "verified")) {
		return receipt, &StorageOperationError{Receipt: receipt, Cause: errors.New("incomplete storage operation receipt")}
	}
	return result, nil
}

func (b *ShellBackend) RecoverWorkspaceVolume(ctx context.Context, previous StorageOperation) (StorageOperation, error) {
	operation := previous.Operation
	if operation != "copy" && operation != "copy-snapshot" {
		return StorageOperation{}, errors.New("invalid copy recovery operation")
	}
	return b.workspaceStorageBound(ctx, operation, previous.Source, previous.Snapshot, previous.Target, previous)
}
func (b *APIBackend) RecoverWorkspaceVolume(ctx context.Context, previous StorageOperation) (StorageOperation, error) {
	helper, err := b.workspaceStorageBackend(ctx, previous.Source)
	if err != nil {
		return StorageOperation{}, err
	}
	return helper.RecoverWorkspaceVolume(ctx, previous)
}

func CopySnapshotName(target string) string {
	digest := sha256.Sum256([]byte(target))
	return "agentlab-copy-" + hex.EncodeToString(digest[:16])
}
func temporaryCopySnapshot(target string) string { return CopySnapshotName(target) }
func (b *ShellBackend) workspaceStorage(ctx context.Context, operation, source, snapshot, target string) error {
	_, err := b.workspaceStorageReceipt(ctx, operation, source, snapshot, target)
	return err
}
func (b *ShellBackend) CopyWorkspaceVolume(ctx context.Context, source, snapshot, target string) (StorageOperation, error) {
	operation := "copy-snapshot"
	if snapshot == "" {
		operation = "copy"
		snapshot = CopySnapshotName(target)
	}
	return b.workspaceStorageReceipt(ctx, operation, source, snapshot, target)
}
func (b *APIBackend) CopyWorkspaceVolume(ctx context.Context, source, snapshot, target string) (StorageOperation, error) {
	helper, err := b.workspaceStorageBackend(ctx, source)
	if err != nil {
		return StorageOperation{}, err
	}
	return helper.CopyWorkspaceVolume(ctx, source, snapshot, target)
}

func (b *ShellBackend) PlanWorkspaceClone(ctx context.Context, storage, name string, size int) (string, error) {
	if size <= 0 {
		return "", errors.New("workspace size must be positive")
	}
	kind, err := b.allocationStorageType(ctx, storage)
	if err != nil {
		return "", err
	}
	if kind != "zfspool" {
		return "", unsupportedStorageErr("workspace copy", storage, kind)
	}
	_, _, expected, err := volumeAllocationTarget(storage, kind, name)
	return expected, err
}

func (b *APIBackend) workspaceStorageBackend(ctx context.Context, source string) (*ShellBackend, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	parsed, err := url.Parse(b.BaseURL)
	if err != nil || parsed.User != nil {
		return nil, &WorkspaceStorageRequirement{}
	}
	host := parsed.Hostname()
	local := host == "localhost"
	if ip := net.ParseIP(host); ip != nil {
		local = ip.IsLoopback()
	}
	if !local || !b.AllowShellFallback || b.ShellFallback == nil {
		return nil, &WorkspaceStorageRequirement{}
	}
	storage := volumeStorage(source)
	// An authentication, permission, network, or configuration error stops here.
	// The helper never follows a failed API mutation or an authentication failure.
	kind, err := b.storageType(ctx, storage)
	if err != nil {
		return nil, err
	}
	if kind != "zfspool" {
		return nil, unsupportedStorageErr("workspace storage", storage, kind)
	}
	node, err := b.ensureNode(ctx)
	if err != nil {
		return nil, err
	}
	fallback := *b.ShellFallback
	if fallback.Node != "" && fallback.Node != node {
		return nil, errors.New("local storage helper node does not match the API node")
	}
	fallback.Node = node
	return &fallback, nil
}

func (b *APIBackend) PlanWorkspaceClone(ctx context.Context, storage, name string, size int) (string, error) {
	if size <= 0 {
		return "", errors.New("workspace size must be positive")
	}
	if _, err := volumeAllocationOwner(name); err != nil {
		return "", err
	}
	helper, err := b.workspaceStorageBackend(ctx, storage+":"+name)
	if err != nil {
		return "", err
	}
	_ = helper
	_, _, expected, err := volumeAllocationTarget(storage, "zfspool", name)
	return expected, err
}

// WorkspaceStorageSupported reports static helper availability. Each operation
// still authenticates and verifies the local node and configured storage.
func (b *APIBackend) WorkspaceStorageSupported() bool {
	parsed, err := url.Parse(b.BaseURL)
	if err != nil || parsed.User != nil || !b.AllowShellFallback || b.ShellFallback == nil {
		return false
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
func (b *ShellBackend) WorkspaceStorageSupported() bool { return true }
func (b *FakeBackend) WorkspaceStorageSupported() bool  { return true }

type WorkspaceStorageRequirement struct{}

func (*WorkspaceStorageRequirement) Error() string {
	return "workspace storage requires a verified local Proxmox helper"
}
func (*WorkspaceStorageRequirement) Unwrap() error { return ErrStorageUnsupported }

var localStorageNodePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func (b *ShellBackend) localStorageNode(ctx context.Context) (string, error) {
	node := b.Node
	if node == "" {
		output, err := b.run(ctx, "/usr/bin/perl", "-T", "-MPVE::INotify", "-e", "print PVE::INotify::nodename()")
		if err != nil {
			return "", err
		}
		node = strings.TrimSpace(output)
	}
	if !localStorageNodePattern.MatchString(node) {
		return "", errors.New("invalid local Proxmox storage node")
	}
	return node, nil
}
