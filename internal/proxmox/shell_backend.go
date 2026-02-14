// ABOUTME: This file implements the Backend interface using Proxmox CLI commands (qm, pvesh, pvesm).
// The shell backend is provided as a fallback for environments where the API is unavailable.
// Note: The ShellBackend may encounter Proxmox IPC issues; BashRunner can help work around these.
package proxmox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BashRunner wraps commands in bash to provide interactive shell context.
// ABOUTME: Uses an argument-safe bash exec pattern to avoid shell injection.
type BashRunner struct{}

func (br BashRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	fullCmd := formatCommand(append([]string{name}, args...))
	cmdArgs := append([]string{"-c", "exec \"$@\"", "bash", name}, args...)
	cmd := exec.CommandContext(ctx, "bash", cmdArgs...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return "", fmt.Errorf("command %s failed: %w: %s", fullCmd, err, errMsg)
		}
		return "", fmt.Errorf("command %s failed: %w", fullCmd, err)
	}
	return stdout.String(), nil
}

// ExecRunner runs commands via os/exec.
// ABOUTME: This is the default command runner for the ShellBackend.
type ExecRunner struct{}

func (er ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		fullCmd := formatCommand(append([]string{name}, args...))
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return "", fmt.Errorf("command %s failed: %w: %s", fullCmd, err, errMsg)
		}
		return "", fmt.Errorf("command %s failed: %w", fullCmd, err)
	}
	return stdout.String(), nil
}

// ShellBackend implements Backend using qm and pvesh commands.
// ABOUTME: This backend uses Proxmox CLI tools instead of the REST API. It may encounter
// IPC issues on some Proxmox configurations. Consider using BashRunner for better compatibility.
type ShellBackend struct {
	Node      string        // Proxmox node name (empty for auto-detection)
	AgentCIDR string        // CIDR block for selecting guest IPs (e.g., "10.77.0.0/16")
	QmPath    string        // Path to qm command (defaults to "qm")
	PveShPath string        // Path to pvesh command (defaults to "pvesh")
	Runner    CommandRunner // Command execution strategy (defaults to ExecRunner)
	CloneMode string        // "linked" or "full" clone mode (default: linked)

	// Use BashRunner to work around Proxmox IPC issues
	BashRunner         BashRunner                                       // Bash runner for working around IPC issues
	CommandTimeout     time.Duration                                    // Timeout for command execution
	GuestIPAttempts    int                                              // Number of attempts to query guest IP (defaults to 30)
	GuestIPInitialWait time.Duration                                    // Initial wait between guest IP attempts (defaults to 500ms)
	GuestIPMaxWait     time.Duration                                    // Maximum wait between guest IP attempts (defaults to 10s)
	DHCPLeasePaths     []string                                         // Paths to DHCP lease files for fallback IP discovery
	Sleep              func(ctx context.Context, d time.Duration) error // Custom sleep for testing
}

var _ Backend = (*ShellBackend)(nil)

func (b *ShellBackend) Clone(ctx context.Context, template VMID, target VMID, name string) error {
	full := "0"
	if strings.EqualFold(strings.TrimSpace(b.CloneMode), "full") {
		full = "1"
	}
	args := []string{"clone", strconv.Itoa(int(template)), strconv.Itoa(int(target)), "--full", full}
	if name != "" {
		args = append(args, "--name", name)
	}
	_, err := b.run(ctx, b.qmPath(), args...)
	if err == nil {
		return nil
	}
	if !shouldRetryFullClone(err) {
		return err
	}
	fullArgs := []string{"clone", strconv.Itoa(int(template)), strconv.Itoa(int(target)), "--full", "1"}
	if name != "" {
		fullArgs = append(fullArgs, "--name", name)
	}
	_, retryErr := b.run(ctx, b.qmPath(), fullArgs...)
	if retryErr != nil {
		return fmt.Errorf("linked clone failed: %w; full clone retry failed: %v", err, retryErr)
	}
	return nil
}

func (b *ShellBackend) Configure(ctx context.Context, vmid VMID, cfg VMConfig) error {
	buildConfigureArgs := func(firewallGroup string) []string {
		args := []string{"set", strconv.Itoa(int(vmid))}
		if cfg.Name != "" {
			args = append(args, "--name", cfg.Name)
		}
		if cfg.Cores > 0 {
			args = append(args, "--cores", strconv.Itoa(cfg.Cores))
		}
		if cfg.MemoryMB > 0 {
			args = append(args, "--memory", strconv.Itoa(cfg.MemoryMB))
		}
		if cfg.SCSIHW != "" {
			args = append(args, "--scsihw", cfg.SCSIHW)
		}
		if cfg.CPUPinning != "" {
			args = append(args, "--cpulist", cfg.CPUPinning)
		}
		if cfg.Bridge != "" || cfg.NetModel != "" || cfg.Firewall != nil || firewallGroup != "" {
			net0 := buildNet0(cfg.NetModel, cfg.Bridge, cfg.Firewall, firewallGroup)
			args = append(args, "--net0", net0)
		}
		if cfg.CloudInit != "" {
			args = append(args, "--cicustom", formatCICustom(cfg.CloudInit))
		}
		return args
	}

	args := buildConfigureArgs(cfg.FirewallGroup)
	if len(args) == 2 {
		if cfg.RootDiskGB > 0 {
			return b.ensureRootDiskSize(ctx, vmid, cfg.RootDisk, cfg.RootDiskGB)
		}
		return nil
	}
	if _, err := b.run(ctx, b.qmPath(), args...); err != nil {
		if cfg.FirewallGroup == "" || !isUnsupportedFWGroupError(err) {
			return err
		}
		fallbackArgs := buildConfigureArgs("")
		if _, retryErr := b.run(ctx, b.qmPath(), fallbackArgs...); retryErr != nil {
			return fmt.Errorf("%w; retry without fwgroup failed: %v", err, retryErr)
		}
	}
	if cfg.RootDiskGB > 0 {
		if err := b.ensureRootDiskSize(ctx, vmid, cfg.RootDisk, cfg.RootDiskGB); err != nil {
			return err
		}
	}
	return nil
}

func (b *ShellBackend) ensureRootDiskSize(ctx context.Context, vmid VMID, disk string, targetGB int) error {
	if targetGB <= 0 {
		return nil
	}
	// Query config to determine current disk size and boot disk.
	out, err := b.run(ctx, b.qmPath(), "config", strconv.Itoa(int(vmid)))
	if err != nil {
		return err
	}
	cfg := parseQMConfigMap(out)
	disk = strings.TrimSpace(disk)
	if disk == "" {
		disk = detectRootDisk(cfg)
	}
	if disk == "" {
		return fmt.Errorf("unable to determine root disk for vm %d", vmid)
	}
	diskCfg, ok := cfg[disk]
	if !ok {
		return fmt.Errorf("root disk %q missing from vm %d config", disk, vmid)
	}
	token := extractDiskSizeToken(diskCfg)
	if token == "" {
		return fmt.Errorf("unable to determine current size for vm %d disk %q", vmid, disk)
	}
	currentGB, err := parseSizeGB(token)
	if err != nil {
		return fmt.Errorf("parse vm %d disk %q size %q: %w", vmid, disk, token, err)
	}
	delta := resizeDeltaGB(currentGB, targetGB)
	if delta <= 0 {
		return nil
	}
	_, err = b.run(ctx, b.qmPath(), "resize", strconv.Itoa(int(vmid)), disk, fmt.Sprintf("+%dG", delta))
	return err
}

func (b *ShellBackend) Start(ctx context.Context, vmid VMID) error {
	_, err := b.run(ctx, b.qmPath(), "start", strconv.Itoa(int(vmid)))
	return err
}

func (b *ShellBackend) Stop(ctx context.Context, vmid VMID) error {
	_, err := b.run(ctx, b.qmPath(), "stop", strconv.Itoa(int(vmid)))
	if err != nil {
		if isMissingVMError(err) {
			return fmt.Errorf("%w: %v", ErrVMNotFound, err)
		}
		return err
	}
	return nil
}

func (b *ShellBackend) Suspend(ctx context.Context, vmid VMID) error {
	_, err := b.run(ctx, b.qmPath(), "suspend", strconv.Itoa(int(vmid)), "--todisk", "0")
	if err != nil {
		if isMissingVMError(err) {
			return fmt.Errorf("%w: %v", ErrVMNotFound, err)
		}
		return err
	}
	return nil
}

func (b *ShellBackend) Resume(ctx context.Context, vmid VMID) error {
	_, err := b.run(ctx, b.qmPath(), "resume", strconv.Itoa(int(vmid)))
	if err != nil {
		if isMissingVMError(err) {
			return fmt.Errorf("%w: %v", ErrVMNotFound, err)
		}
		return err
	}
	return nil
}

func (b *ShellBackend) Destroy(ctx context.Context, vmid VMID) error {
	_, err := b.run(ctx, b.qmPath(), "destroy", strconv.Itoa(int(vmid)), "--purge", "1")
	if err != nil {
		if isMissingVMError(err) {
			return fmt.Errorf("%w: %v", ErrVMNotFound, err)
		}
		return err
	}
	return nil
}

// SnapshotCreate creates a disk-only snapshot of a VM.
// ABOUTME: The snapshot excludes VM memory state (no vmstate).
func (b *ShellBackend) SnapshotCreate(ctx context.Context, vmid VMID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("snapshot name is required")
	}
	_, err := b.run(ctx, b.qmPath(), "snapshot", strconv.Itoa(int(vmid)), name)
	return err
}

// SnapshotRollback reverts a VM to the named snapshot.
// ABOUTME: The VM should be stopped before rollback; no vmstate snapshots are used.
func (b *ShellBackend) SnapshotRollback(ctx context.Context, vmid VMID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("snapshot name is required")
	}
	_, err := b.run(ctx, b.qmPath(), "rollback", strconv.Itoa(int(vmid)), name)
	return err
}

// SnapshotDelete removes a snapshot from a VM.
func (b *ShellBackend) SnapshotDelete(ctx context.Context, vmid VMID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("snapshot name is required")
	}
	_, err := b.run(ctx, b.qmPath(), "delsnapshot", strconv.Itoa(int(vmid)), name)
	return err
}

// SnapshotList lists snapshots for a VM via pvesh.
func (b *ShellBackend) SnapshotList(ctx context.Context, vmid VMID) ([]Snapshot, error) {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/nodes/%s/qemu/%d/snapshot", node, vmid)
	out, err := b.run(ctx, b.pveshPath(), "get", path, "--output-format", "json")
	if err != nil {
		if isMissingVMError(err) {
			return nil, fmt.Errorf("%w: %v", ErrVMNotFound, err)
		}
		return nil, err
	}
	var raw []struct {
		Name        string  `json:"name"`
		SnapTime    float64 `json:"snaptime"`
		Description string  `json:"description"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parse snapshot list: %w", err)
	}

	snapshots := make([]Snapshot, 0, len(raw))
	for _, entry := range raw {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			continue
		}
		var created time.Time
		if entry.SnapTime > 0 {
			created = time.Unix(int64(entry.SnapTime), 0).UTC()
		}
		snapshots = append(snapshots, Snapshot{
			Name:        name,
			Description: strings.TrimSpace(entry.Description),
			CreatedAt:   created,
		})
	}
	return snapshots, nil
}

func (b *ShellBackend) Status(ctx context.Context, vmid VMID) (Status, error) {
	out, err := b.run(ctx, b.qmPath(), "status", strconv.Itoa(int(vmid)))
	if err != nil {
		return StatusUnknown, err
	}
	status, err := parseStatus(out)
	if err != nil {
		return StatusUnknown, err
	}
	return status, nil
}

// CurrentStats retrieves VM runtime statistics via pvesh.
// ABOUTME: CPUUsage is reported by Proxmox status/current.
func (b *ShellBackend) CurrentStats(ctx context.Context, vmid VMID) (VMStats, error) {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return VMStats{}, err
	}
	path := fmt.Sprintf("/nodes/%s/qemu/%d/status/current", node, vmid)
	out, err := b.run(ctx, b.pveshPath(), "get", path, "--output-format", "json")
	if err != nil {
		return VMStats{}, err
	}
	var result struct {
		CPU float64 `json:"cpu"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return VMStats{}, fmt.Errorf("parse current stats: %w", err)
	}
	return VMStats{CPUUsage: result.CPU}, nil
}

func (b *ShellBackend) GuestIP(ctx context.Context, vmid VMID) (string, error) {
	var netblock *net.IPNet
	if b.AgentCIDR != "" {
		_, parsed, err := net.ParseCIDR(b.AgentCIDR)
		if err != nil {
			return "", fmt.Errorf("invalid agent CIDR %q: %w", b.AgentCIDR, err)
		}
		netblock = parsed
	}

	dhcpErr := ErrGuestIPNotFound
	macs := []string{}
	leaseFiles := b.leasePaths()
	if len(leaseFiles) == 0 {
		dhcpErr = fmt.Errorf("%w: no DHCP lease files configured", ErrGuestIPNotFound)
	} else {
		out, err := b.run(ctx, b.qmPath(), "config", strconv.Itoa(int(vmid)))
		if err != nil {
			dhcpErr = err
		} else {
			macs = parseNetMACs(out)
			if len(macs) == 0 {
				dhcpErr = fmt.Errorf("%w: no MAC addresses found", ErrGuestIPNotFound)
			}
		}
	}

	attempts := 0
	if _, ok := ctx.Deadline(); !ok {
		// Avoid infinite polling with a background context.
		attempts = b.guestIPAttempts()
	}
	wait := b.guestIPInitialWait()
	maxWait := b.guestIPMaxWait()

	qgaErr := ErrGuestIPNotFound
	node := ""
	for i := 0; ; i++ {
		if len(macs) > 0 && len(leaseFiles) > 0 {
			var readErr error
			for _, path := range leaseFiles {
				content, err := os.ReadFile(path)
				if err != nil {
					if os.IsNotExist(err) {
						continue
					}
					readErr = err
					continue
				}
				if ip := findLeaseIP(content, macs, netblock); ip != "" {
					return ip, nil
				}
			}
			if readErr != nil {
				dhcpErr = readErr
			} else {
				dhcpErr = ErrGuestIPNotFound
			}
		}

		if node == "" {
			resolved, err := b.ensureNode(ctx)
			if err != nil {
				qgaErr = err
			} else {
				node = resolved
			}
		}
		if node != "" {
			ip, err := b.guestAgentIP(ctx, node, vmid)
			if err == nil && strings.TrimSpace(ip) != "" {
				return strings.TrimSpace(ip), nil
			}
			if err != nil {
				qgaErr = err
			} else {
				qgaErr = ErrGuestIPNotFound
			}
		}

		if attempts > 0 && i >= attempts-1 {
			break
		}
		if err := b.sleep(ctx, wait); err != nil {
			return "", err
		}
		wait = nextBackoff(wait, maxWait)
	}

	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return "", fmt.Errorf("%w: dhcp=%v qemu-guest-agent=%v", ErrGuestIPNotFound, dhcpErr, qgaErr)
}

func (b *ShellBackend) VMConfig(ctx context.Context, vmid VMID) (map[string]string, error) {
	out, err := b.run(ctx, b.qmPath(), "config", strconv.Itoa(int(vmid)))
	if err != nil {
		if isMissingVMError(err) {
			return nil, fmt.Errorf("%w: %v", ErrVMNotFound, err)
		}
		return nil, err
	}
	return parseQMConfig(out), nil
}

func (b *ShellBackend) runner() CommandRunner {
	if b.Runner != nil {
		return b.Runner
	}
	return ExecRunner{}
}

func (b *ShellBackend) qmPath() string {
	if b.QmPath != "" {
		return b.QmPath
	}
	return "qm"
}

func (b *ShellBackend) pveshPath() string {
	if b.PveShPath != "" {
		return b.PveShPath
	}
	return "pvesh"
}

func (b *ShellBackend) pvesmPath() string {
	return "pvesm"
}

// NewShellBackendWithBashRunner creates a ShellBackend that uses BashRunner instead of ExecRunner.
// ABOUTME: This works around Proxmox IPC issues by running qm commands via bash shell.
// Recommended for production use with the shell backend.
func NewShellBackendWithBashRunner(node string, agentCIDR string, qmPath string, pveShPath string, timeout time.Duration) *ShellBackend {
	return &ShellBackend{
		Node:           node,
		AgentCIDR:      agentCIDR,
		QmPath:         qmPath,
		PveShPath:      pveShPath,
		Runner:         BashRunner{},
		CommandTimeout: timeout,
	}
}

func (b *ShellBackend) pollGuestAgentIP(ctx context.Context, node string, vmid VMID) (string, error) {
	attempts := 0
	if _, ok := ctx.Deadline(); !ok {
		// Avoid infinite polling with a background context.
		attempts = b.guestIPAttempts()
	}
	wait := b.guestIPInitialWait()
	maxWait := b.guestIPMaxWait()
	var lastErr error
	for i := 0; ; i++ {
		ip, err := b.guestAgentIP(ctx, node, vmid)
		if err == nil && ip != "" {
			return ip, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = ErrGuestIPNotFound
		}
		if attempts > 0 && i >= attempts-1 {
			break
		}
		if err := b.sleep(ctx, wait); err != nil {
			return "", err
		}
		wait = nextBackoff(wait, maxWait)
	}
	if lastErr == nil {
		lastErr = ErrGuestIPNotFound
	}
	return "", lastErr
}

func (b *ShellBackend) guestAgentIP(ctx context.Context, node string, vmid VMID) (string, error) {
	path := fmt.Sprintf("/nodes/%s/qemu/%d/agent/network-get-interfaces", node, vmid)
	out, err := b.run(ctx, b.pveshPath(), "get", path, "--output-format", "json")
	if err != nil {
		if isGuestAgentNotRunningError(err) {
			return "", ErrGuestIPNotFound
		}
		return "", err
	}
	ips, err := parseAgentIPs(out)
	if err != nil {
		return "", err
	}
	return b.selectIP(ips)
}

func (b *ShellBackend) selectIP(ips []net.IP) (string, error) {
	if len(ips) == 0 {
		return "", ErrGuestIPNotFound
	}
	if b.AgentCIDR != "" {
		ip, err := selectIPByCIDR(ips, b.AgentCIDR)
		if err != nil {
			return "", err
		}
		if ip != "" {
			return ip, nil
		}
	}
	for _, ip := range ips {
		if ip != nil && ip.IsPrivate() {
			return ip.String(), nil
		}
	}
	return ips[0].String(), nil
}

func (b *ShellBackend) dhcpLeaseIP(ctx context.Context, vmid VMID) (string, error) {
	var netblock *net.IPNet
	if b.AgentCIDR != "" {
		_, parsed, err := net.ParseCIDR(b.AgentCIDR)
		if err != nil {
			return "", fmt.Errorf("invalid agent CIDR %q: %w", b.AgentCIDR, err)
		}
		netblock = parsed
	}
	out, err := b.run(ctx, b.qmPath(), "config", strconv.Itoa(int(vmid)))
	if err != nil {
		return "", err
	}
	macs := parseNetMACs(out)
	if len(macs) == 0 {
		return "", fmt.Errorf("%w: no MAC addresses found", ErrGuestIPNotFound)
	}
	leaseFiles := b.leasePaths()
	if len(leaseFiles) == 0 {
		return "", fmt.Errorf("%w: no DHCP lease files configured", ErrGuestIPNotFound)
	}
	var readErr error
	for _, path := range leaseFiles {
		content, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			readErr = err
			continue
		}
		if ip := findLeaseIP(content, macs, netblock); ip != "" {
			return ip, nil
		}
	}
	if readErr != nil {
		return "", readErr
	}
	return "", ErrGuestIPNotFound
}

func (b *ShellBackend) CreateVolume(ctx context.Context, storage, name string, sizeGB int) (string, error) {
	storage = strings.TrimSpace(storage)
	if storage == "" {
		return "", errors.New("storage is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("volume name is required")
	}
	if sizeGB <= 0 {
		return "", errors.New("size_gb must be positive")
	}
	sizeArg := fmt.Sprintf("%dG", sizeGB)
	out, err := b.run(ctx, b.pvesmPath(), "alloc", storage, "0", name, sizeArg)
	if err != nil {
		return "", err
	}
	volid := strings.TrimSpace(out)
	if volid == "" {
		return "", errors.New("empty volume id")
	}
	return volid, nil
}

func (b *ShellBackend) AttachVolume(ctx context.Context, vmid VMID, volumeID, slot string) error {
	volumeID = strings.TrimSpace(volumeID)
	if volumeID == "" {
		return errors.New("volume id is required")
	}
	slot = strings.TrimSpace(slot)
	if slot == "" {
		return errors.New("slot is required")
	}
	_, err := b.run(ctx, b.qmPath(), "set", strconv.Itoa(int(vmid)), "--"+slot, volumeID)
	return err
}

func (b *ShellBackend) DetachVolume(ctx context.Context, vmid VMID, slot string) error {
	slot = strings.TrimSpace(slot)
	if slot == "" {
		return errors.New("slot is required")
	}
	_, err := b.run(ctx, b.qmPath(), "set", strconv.Itoa(int(vmid)), "--delete", slot)
	if err != nil {
		if isMissingVMError(err) {
			return fmt.Errorf("%w: %v", ErrVMNotFound, err)
		}
		return err
	}
	return nil
}

func (b *ShellBackend) DeleteVolume(ctx context.Context, volumeID string) error {
	volumeID = strings.TrimSpace(volumeID)
	if volumeID == "" {
		return errors.New("volume id is required")
	}
	_, err := b.run(ctx, b.pvesmPath(), "free", volumeID)
	return err
}

func (b *ShellBackend) VolumeInfo(ctx context.Context, volumeID string) (VolumeInfo, error) {
	volumeID = strings.TrimSpace(volumeID)
	if volumeID == "" {
		return VolumeInfo{}, errors.New("volume id is required")
	}
	out, err := b.run(ctx, b.pvesmPath(), "path", volumeID)
	if err != nil {
		if isMissingVolumeError(err) {
			return VolumeInfo{}, fmt.Errorf("%w: %v", ErrVolumeNotFound, err)
		}
		return VolumeInfo{}, err
	}
	info := VolumeInfo{
		VolumeID: volumeID,
		Storage:  volumeStorage(volumeID),
		Path:     strings.TrimSpace(out),
	}
	return info, nil
}

// VolumeSnapshotCreate creates a snapshot for a workspace volume.
// ABOUTME: Callers should detach the volume before snapshotting for consistency.
func (b *ShellBackend) VolumeSnapshotCreate(ctx context.Context, volumeID, name string) error {
	volumeID = strings.TrimSpace(volumeID)
	if volumeID == "" {
		return errors.New("volume id is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("snapshot name is required")
	}
	if err := b.ensureZFSVolume(ctx, volumeID, "volume snapshot"); err != nil {
		return err
	}
	_, err := b.run(ctx, b.pvesmPath(), "snapshot", volumeID, name)
	return err
}

// VolumeSnapshotRestore restores a workspace volume to a snapshot.
// ABOUTME: Callers should detach the volume before restoring for consistency.
func (b *ShellBackend) VolumeSnapshotRestore(ctx context.Context, volumeID, name string) error {
	volumeID = strings.TrimSpace(volumeID)
	if volumeID == "" {
		return errors.New("volume id is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("snapshot name is required")
	}
	if err := b.ensureZFSVolume(ctx, volumeID, "volume restore"); err != nil {
		return err
	}
	_, err := b.run(ctx, b.pvesmPath(), "rollback", volumeID, name)
	return err
}

// VolumeSnapshotDelete removes a snapshot from a workspace volume.
func (b *ShellBackend) VolumeSnapshotDelete(ctx context.Context, volumeID, name string) error {
	volumeID = strings.TrimSpace(volumeID)
	if volumeID == "" {
		return errors.New("volume id is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("snapshot name is required")
	}
	if err := b.ensureZFSVolume(ctx, volumeID, "volume snapshot delete"); err != nil {
		return err
	}
	_, err := b.run(ctx, b.pvesmPath(), "delsnapshot", volumeID, name)
	return err
}

// VolumeClone clones a workspace volume into a new volume ID.
// ABOUTME: Callers should detach the source volume before cloning for consistency.
func (b *ShellBackend) VolumeClone(ctx context.Context, sourceVolumeID, targetVolumeID string) error {
	sourceVolumeID = strings.TrimSpace(sourceVolumeID)
	if sourceVolumeID == "" {
		return errors.New("source volume id is required")
	}
	targetVolumeID = strings.TrimSpace(targetVolumeID)
	if targetVolumeID == "" {
		return errors.New("target volume id is required")
	}
	sourceStorage := volumeStorage(sourceVolumeID)
	if sourceStorage == "" {
		return fmt.Errorf("invalid source volume id format: %s", sourceVolumeID)
	}
	targetStorage := volumeStorage(targetVolumeID)
	if targetStorage == "" {
		return fmt.Errorf("invalid target volume id format: %s", targetVolumeID)
	}
	if sourceStorage != targetStorage {
		return fmt.Errorf("%w: volume clone requires same storage (source=%s target=%s)", ErrStorageUnsupported, sourceStorage, targetStorage)
	}
	if err := b.ensureZFSStorage(ctx, sourceStorage, "volume clone"); err != nil {
		return err
	}
	_, err := b.run(ctx, b.pvesmPath(), "clone", sourceVolumeID, targetVolumeID)
	return err
}

// VolumeCloneFromSnapshot clones a workspace volume snapshot into a new volume ID.
// ABOUTME: Callers should detach the source volume before cloning for consistency.
func (b *ShellBackend) VolumeCloneFromSnapshot(ctx context.Context, sourceVolumeID, snapshotName, targetVolumeID string) error {
	sourceVolumeID = strings.TrimSpace(sourceVolumeID)
	if sourceVolumeID == "" {
		return errors.New("source volume id is required")
	}
	snapshotName = strings.TrimSpace(snapshotName)
	if snapshotName == "" {
		return errors.New("snapshot name is required")
	}
	targetVolumeID = strings.TrimSpace(targetVolumeID)
	if targetVolumeID == "" {
		return errors.New("target volume id is required")
	}
	sourceStorage := volumeStorage(sourceVolumeID)
	if sourceStorage == "" {
		return fmt.Errorf("invalid source volume id format: %s", sourceVolumeID)
	}
	targetStorage := volumeStorage(targetVolumeID)
	if targetStorage == "" {
		return fmt.Errorf("invalid target volume id format: %s", targetVolumeID)
	}
	if sourceStorage != targetStorage {
		return fmt.Errorf("%w: volume clone requires same storage (source=%s target=%s)", ErrStorageUnsupported, sourceStorage, targetStorage)
	}
	if err := b.ensureZFSStorage(ctx, sourceStorage, "volume clone"); err != nil {
		return err
	}
	_, err := b.run(ctx, b.pvesmPath(), "clone", sourceVolumeID, targetVolumeID, "--snapname", snapshotName)
	return err
}

func (b *ShellBackend) ValidateTemplate(ctx context.Context, template VMID) error {
	// Check if VM exists
	out, err := b.run(ctx, b.qmPath(), "config", strconv.Itoa(int(template)))
	if err != nil {
		// Check if error indicates VM doesn't exist
		if isMissingVMError(err) {
			return fmt.Errorf("template VM %d does not exist", template)
		}
		return fmt.Errorf("failed to query template VM %d: %w", template, err)
	}

	// Check if agent is enabled in the config
	// The config output should contain "agent: 1" or "agent: enabled=1"
	if !strings.Contains(out, "agent:") {
		return fmt.Errorf("template VM %d does not have qemu-guest-agent enabled (missing 'agent:' config)", template)
	}
	// Check for explicit agent disabled
	if strings.Contains(out, "agent: 0") || strings.Contains(out, "agent: disabled=1") {
		return fmt.Errorf("template VM %d has qemu-guest-agent explicitly disabled", template)
	}
	if !hasCloudInitDrive(parseQMConfigMap(out)) {
		return fmt.Errorf("template VM %d does not have a cloud-init drive configured", template)
	}
	return nil
}

func (b *ShellBackend) guestIPAttempts() int {
	if b.GuestIPAttempts > 0 {
		return b.GuestIPAttempts
	}
	return 30
}

func (b *ShellBackend) guestIPInitialWait() time.Duration {
	if b.GuestIPInitialWait > 0 {
		return b.GuestIPInitialWait
	}
	return 500 * time.Millisecond
}

func (b *ShellBackend) guestIPMaxWait() time.Duration {
	if b.GuestIPMaxWait > 0 {
		return b.GuestIPMaxWait
	}
	return 10 * time.Second
}

func (b *ShellBackend) sleep(ctx context.Context, d time.Duration) error {
	if b.Sleep != nil {
		return b.Sleep(ctx, d)
	}
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (b *ShellBackend) run(ctx context.Context, name string, args ...string) (string, error) {
	if err := validateCommandArgs(name, args); err != nil {
		return "", err
	}
	ctx, cancel := b.withCommandTimeout(ctx)
	defer cancel()
	return b.runner().Run(ctx, name, args...)
}

func (b *ShellBackend) withCommandTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if b.CommandTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, b.CommandTimeout)
}

func nextBackoff(current, max time.Duration) time.Duration {
	if current <= 0 {
		return max
	}
	next := current * 2
	if next > max {
		return max
	}
	return next
}

func validateCommandArgs(name string, args []string) error {
	if err := validateCommandToken("command name", name, false); err != nil {
		return err
	}
	for _, arg := range args {
		if err := validateCommandToken("command argument", arg, true); err != nil {
			return err
		}
	}
	return nil
}

func validateCommandToken(label, value string, allowEmpty bool) error {
	if !allowEmpty && strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", label)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s contains control characters", label)
		}
	}
	return nil
}

func formatCommand(args []string) string {
	if len(args) == 0 {
		return ""
	}
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, isShellSpecial) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func isShellSpecial(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\v', '\f', '\\', '\'', '"', '$', '`', ';', '&', '|', '<', '>', '(', ')', '*', '?', '!', '#', '[', ']', '{', '}', '~':
		return true
	default:
		return false
	}
}

func (b *ShellBackend) leasePaths() []string {
	paths := b.DHCPLeasePaths
	if len(paths) == 0 {
		paths = []string{
			"/var/lib/misc/dnsmasq.leases",
			"/var/lib/dnsmasq/dnsmasq.leases",
			"/var/lib/misc/dnsmasq.*.leases",
			"/var/lib/misc/dnsmasq*.leases",
			"/var/lib/dhcp/dhcpd.leases",
			"/var/lib/dhcp/dhcpd.leases~",
			"/var/lib/dhcp3/dhcpd.leases",
			"/var/lib/pve-firewall/dhcpd.leases",
		}
	}
	expanded := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		if hasGlob(path) {
			matches, err := filepath.Glob(path)
			if err != nil {
				continue
			}
			for _, match := range matches {
				if _, ok := seen[match]; ok {
					continue
				}
				seen[match] = struct{}{}
				expanded = append(expanded, match)
			}
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		expanded = append(expanded, path)
	}
	sort.Strings(expanded)
	return expanded
}

func hasGlob(path string) bool {
	return strings.ContainsAny(path, "*?[")
}

func (b *ShellBackend) ensureZFSVolume(ctx context.Context, volumeID, op string) error {
	storage := volumeStorage(volumeID)
	if storage == "" {
		return fmt.Errorf("invalid volume id format: %s", volumeID)
	}
	return b.ensureZFSStorage(ctx, storage, op)
}

func (b *ShellBackend) ensureZFSStorage(ctx context.Context, storage, op string) error {
	storageType, err := b.storageType(ctx, storage)
	if err != nil {
		return err
	}
	if !isZFSStorageType(storageType) {
		return unsupportedStorageErr(op, storage, storageType)
	}
	return nil
}

func (b *ShellBackend) storageType(ctx context.Context, storage string) (string, error) {
	storage = strings.TrimSpace(storage)
	if storage == "" {
		return "", errors.New("storage is required")
	}
	out, err := b.run(ctx, b.pvesmPath(), "status", "--storage", storage, "--output-format", "json")
	if err != nil {
		return "", err
	}
	if storageType := parsePvesmStatusJSON(out, storage); storageType != "" {
		return storageType, nil
	}
	if storageType := parsePvesmStatusTable(out, storage); storageType != "" {
		return storageType, nil
	}
	return "", fmt.Errorf("storage %s not found in pvesm status output", storage)
}

type pvesmStatusEntry struct {
	Storage string `json:"storage"`
	Type    string `json:"type"`
}

func parsePvesmStatusJSON(out string, storage string) string {
	var entries []pvesmStatusEntry
	if err := json.Unmarshal([]byte(out), &entries); err == nil {
		for _, entry := range entries {
			if entry.Storage == storage {
				return entry.Type
			}
		}
	}
	var wrapper struct {
		Data []pvesmStatusEntry `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &wrapper); err == nil {
		for _, entry := range wrapper.Data {
			if entry.Storage == storage {
				return entry.Type
			}
		}
	}
	var single pvesmStatusEntry
	if err := json.Unmarshal([]byte(out), &single); err == nil && single.Storage != "" {
		if single.Storage == storage {
			return single.Type
		}
	}
	return ""
}

func parsePvesmStatusTable(out string, storage string) string {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[0] == "Name" {
			continue
		}
		if fields[0] == storage {
			return fields[1]
		}
	}
	return ""
}

func parseQMConfig(config string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(config, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(parts[1])
	}
	return out
}

func parseNetMACs(config string) []string {
	var macs []string
	for _, line := range strings.Split(config, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "net") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		fields := strings.Split(strings.TrimSpace(parts[1]), ",")
		for _, field := range fields {
			kv := strings.SplitN(strings.TrimSpace(field), "=", 2)
			if len(kv) != 2 {
				continue
			}
			mac := strings.TrimSpace(kv[1])
			if isMAC(mac) {
				macs = append(macs, normalizeMAC(mac))
			}
		}
	}
	return uniqueStrings(macs)
}

func findLeaseIP(content []byte, macs []string, netblock *net.IPNet) string {
	if len(macs) == 0 || len(content) == 0 {
		return ""
	}
	macset := make(map[string]struct{}, len(macs))
	for _, mac := range macs {
		macset[normalizeMAC(mac)] = struct{}{}
	}
	if ip := findDNSMasqLease(content, macset, netblock); ip != "" {
		return ip
	}
	if ip := findDHCPDLease(content, macset, netblock); ip != "" {
		return ip
	}
	return ""
}

func findDNSMasqLease(content []byte, macset map[string]struct{}, netblock *net.IPNet) string {
	var found string
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		mac := normalizeMAC(fields[1])
		if _, ok := macset[mac]; !ok {
			continue
		}
		ip := net.ParseIP(fields[2])
		if ip == nil {
			continue
		}
		ip = ip.To4()
		if ip == nil {
			continue
		}
		if netblock != nil && !netblock.Contains(ip) {
			continue
		}
		found = ip.String()
	}
	return found
}

func findDHCPDLease(content []byte, macset map[string]struct{}, netblock *net.IPNet) string {
	var found string
	var currentIP string
	var currentMAC string
	inLease := false
	active := true
	bindingSeen := false
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "lease ") && strings.Contains(line, "{") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				currentIP = fields[1]
				currentMAC = ""
				active = true
				bindingSeen = false
				inLease = true
			}
			continue
		}
		if !inLease {
			continue
		}
		if strings.HasPrefix(line, "hardware ethernet ") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				currentMAC = normalizeMAC(strings.TrimSuffix(fields[2], ";"))
			}
			continue
		}
		if strings.HasPrefix(line, "binding state ") {
			bindingSeen = true
			active = strings.Contains(line, "active")
			continue
		}
		if line == "}" {
			if currentIP != "" && currentMAC != "" {
				if _, ok := macset[currentMAC]; ok && (!bindingSeen || active) {
					ip := net.ParseIP(currentIP)
					if ip != nil {
						ip = ip.To4()
						if ip != nil && (netblock == nil || netblock.Contains(ip)) {
							found = ip.String()
						}
					}
				}
			}
			inLease = false
		}
	}
	return found
}

func normalizeMAC(mac string) string {
	return strings.ToLower(strings.TrimSpace(mac))
}

func isMAC(value string) bool {
	_, err := net.ParseMAC(value)
	return err == nil
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (b *ShellBackend) ensureNode(ctx context.Context) (string, error) {
	if b.Node != "" {
		return b.Node, nil
	}
	out, err := b.run(ctx, b.pveshPath(), "get", "/nodes", "--output-format", "json")
	if err != nil {
		return "", err
	}
	node, err := parseNode(out)
	if err != nil {
		return "", err
	}
	b.Node = node
	return node, nil
}

func buildNet0(model, bridge string, firewall *bool, firewallGroup string) string {
	if model == "" {
		model = "virtio"
	}
	parts := []string{model}
	if bridge != "" && !strings.Contains(model, "bridge=") {
		parts = append(parts, "bridge="+bridge)
	}
	if firewall != nil && !strings.Contains(model, "firewall=") {
		if *firewall {
			parts = append(parts, "firewall=1")
		} else {
			parts = append(parts, "firewall=0")
		}
	}
	if firewallGroup != "" && !strings.Contains(model, "fwgroup=") {
		parts = append(parts, "fwgroup="+firewallGroup)
	}
	return strings.Join(parts, ",")
}

func formatCICustom(value string) string {
	if strings.Contains(value, "=") {
		return value
	}
	return "user=" + value
}

func isMissingVMError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	indicators := []string{
		"does not exist",
		"no such vm",
		"no such qemu",
		"no such vmid",
		"vmid does not exist",
	}
	for _, indicator := range indicators {
		if strings.Contains(msg, indicator) {
			return true
		}
	}
	return strings.Contains(msg, "not found") && strings.Contains(msg, "vm")
}

func parseStatus(output string) (Status, error) {
	out := strings.TrimSpace(output)
	if out == "" {
		return StatusUnknown, errors.New("empty status output")
	}
	if strings.Contains(out, "status:") {
		parts := strings.SplitN(out, "status:", 2)
		out = strings.TrimSpace(parts[1])
		if idx := strings.Index(out, "\n"); idx != -1 {
			out = strings.TrimSpace(out[:idx])
		}
	} else {
		fields := strings.Fields(out)
		if len(fields) > 0 {
			out = fields[0]
		}
	}
	switch out {
	case "running":
		return StatusRunning, nil
	case "stopped":
		return StatusStopped, nil
	default:
		return StatusUnknown, fmt.Errorf("unknown status %q", out)
	}
}

type agentInterface struct {
	Name        string      `json:"name"`
	IPAddresses []agentAddr `json:"ip-addresses"`
}

type agentAddr struct {
	IPAddress     string `json:"ip-address"`
	IPAddressType string `json:"ip-address-type"`
}

type agentNetResp struct {
	Result []agentInterface `json:"result"`
	Data   []agentInterface `json:"data"`
}

func parseAgentIPs(output string) ([]net.IP, error) {
	payload := strings.TrimSpace(output)
	if payload == "" {
		return nil, errors.New("empty agent response")
	}
	var resp agentNetResp
	if err := json.Unmarshal([]byte(payload), &resp); err == nil {
		ifaces := resp.Result
		if len(ifaces) == 0 {
			ifaces = resp.Data
		}
		if len(ifaces) > 0 {
			return collectIPv4(ifaces), nil
		}
	}
	var direct []agentInterface
	if err := json.Unmarshal([]byte(payload), &direct); err == nil {
		return collectIPv4(direct), nil
	}
	return nil, errors.New("unrecognized agent response")
}

func collectIPv4(ifaces []agentInterface) []net.IP {
	var ips []net.IP
	for _, iface := range ifaces {
		for _, addr := range iface.IPAddresses {
			if addr.IPAddressType != "ipv4" {
				continue
			}
			ip := net.ParseIP(addr.IPAddress)
			if ip == nil {
				continue
			}
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			ip = ip.To4()
			if ip == nil {
				continue
			}
			if ip.Equal(net.IPv4zero) {
				continue
			}
			ips = append(ips, ip)
		}
	}
	return ips
}

func selectIPByCIDR(ips []net.IP, cidr string) (string, error) {
	_, netblock, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("invalid agent CIDR %q: %w", cidr, err)
	}
	for _, ip := range ips {
		if netblock.Contains(ip) {
			return ip.String(), nil
		}
	}
	return "", nil
}

type nodeEntry struct {
	Node string `json:"node"`
	Name string `json:"name"`
}

type nodeResp struct {
	Data []nodeEntry `json:"data"`
}

func parseNode(output string) (string, error) {
	payload := strings.TrimSpace(output)
	if payload == "" {
		return "", errors.New("empty node list")
	}
	var resp nodeResp
	if err := json.Unmarshal([]byte(payload), &resp); err == nil && len(resp.Data) > 0 {
		if node := firstNode(resp.Data); node != "" {
			return node, nil
		}
	}
	var nodes []nodeEntry
	if err := json.Unmarshal([]byte(payload), &nodes); err == nil && len(nodes) > 0 {
		if node := firstNode(nodes); node != "" {
			return node, nil
		}
	}
	return "", errors.New("no nodes found")
}

func firstNode(nodes []nodeEntry) string {
	for _, entry := range nodes {
		if entry.Node != "" {
			return entry.Node
		}
		if entry.Name != "" {
			return entry.Name
		}
	}
	return ""
}
