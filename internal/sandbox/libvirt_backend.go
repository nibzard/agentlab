package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// LibvirtConfig holds configuration for the libvirt backend.
type LibvirtConfig struct {
	// URI is the libvirt connection URI (e.g., "qemu:///system", "qemu:///session").
	// Defaults to "qemu:///system".
	URI string
	// Timeout is the command execution timeout for virsh commands.
	Timeout time.Duration
	// Network is the libvirt network for sandbox connectivity.
	// Defaults to "default".
	Network string
	// Pool is the storage pool for sandbox disk images.
	// Defaults to "default".
	Pool string
}

// LibvirtBackend manages sandbox VMs via libvirt using virsh commands.
//
// ABOUTME: This backend creates and manages KVM/QEMU VMs via libvirt for bare-metal
// hosts without Proxmox. It uses the virsh command-line tool, requiring libvirt and
// virsh to be installed on the host.
//
// ABOUTME: Libvirt sandboxes provide full VM isolation with hardware virtualization,
// suitable for production self-hosted deployments on standard Linux servers.
type LibvirtBackend struct {
	uri     string
	network string
	pool    string
	timeout time.Duration
}

// NewLibvirtBackend creates a new libvirt backend.
func NewLibvirtBackend(cfg LibvirtConfig) (*LibvirtBackend, error) {
	uri := cfg.URI
	if uri == "" {
		uri = "qemu:///system"
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	network := cfg.Network
	if network == "" {
		network = "default"
	}
	pool := cfg.Pool
	if pool == "" {
		pool = "default"
	}

	// Verify virsh is available.
	if _, err := exec.LookPath("virsh"); err != nil {
		return nil, fmt.Errorf("virsh not found in PATH (libvirt-cli package required): %w", err)
	}

	return &LibvirtBackend{
		uri:     uri,
		network: network,
		pool:    pool,
		timeout: timeout,
	}, nil
}

func (b *LibvirtBackend) SandboxType() Type { return TypeLibvirt }

func (b *LibvirtBackend) Capabilities() Capabilities {
	return Capabilities{
		Snapshots:      true,  // libvirt supports snapshots
		Suspend:        true,  // suspend/resume
		WorkspaceMount: false, // libvirt disk attachment needs custom handling
		Firewall:       false, // libvirt uses nwfilter, not directly exposed
	}
}

func (b *LibvirtBackend) Create(ctx context.Context, cfg CreateConfig) error {
	if cfg.Image == "" && cfg.TemplateID <= 0 {
		return errors.New("image or template_id is required for libvirt backend")
	}
	domainName := b.domainName(cfg.ID, cfg.Name)

	// If a template/image was provided, clone from it.
	if cfg.TemplateID > 0 {
		templateName := b.domainName(cfg.TemplateID, "")
		cmdCtx, cancel := context.WithTimeout(ctx, b.timeout)
		defer cancel()
		out, err := b.virsh(cmdCtx, "clone", "--original", templateName, "--name", domainName, "--auto-clone")
		if err != nil {
			return fmt.Errorf("clone libvirt domain %s from %s: %w (output: %s)", domainName, templateName, err, string(out))
		}
	} else {
		// Create from a cloud-init image using virt-install.
		// This assumes the image is a qcow2 file available locally.
		diskPath := fmt.Sprintf("/var/lib/libvirt/images/%s.qcow2", domainName)
		args := []string{
			"--connect", b.uri,
			"--import", "--name", domainName,
			"--disk", fmt.Sprintf("path=%s,format=qcow2,bus=virtio", diskPath),
			"--network", fmt.Sprintf("network=%s,model=virtio", b.network),
		}
		if cfg.Cores > 0 {
			args = append(args, "--vcpus", strconv.Itoa(cfg.Cores))
		}
		if cfg.MemoryMB > 0 {
			args = append(args, "--memory", strconv.Itoa(cfg.MemoryMB))
		}
		args = append(args, "--no-autoconsole", "--os-variant", "detect=off")

		cmdCtx, cancel := context.WithTimeout(ctx, b.timeout)
		defer cancel()
		cmd := exec.CommandContext(cmdCtx, "virt-install", args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("virt-install %s: %w (output: %s)", domainName, err, string(out))
		}
	}

	// Set resource limits if specified.
	if cfg.Cores > 0 || cfg.MemoryMB > 0 {
		if cfg.MemoryMB > 0 {
			cmdCtx, cancel := context.WithTimeout(ctx, b.timeout)
			defer cancel()
			_, _ = b.virsh(cmdCtx, "setmaxmem", domainName, fmt.Sprintf("%dM", cfg.MemoryMB), "--config")
		}
		if cfg.Cores > 0 {
			cmdCtx2, cancel2 := context.WithTimeout(ctx, b.timeout)
			defer cancel2()
			_, _ = b.virsh(cmdCtx2, "setvcpus", domainName, strconv.Itoa(cfg.Cores), "--config", "--maximum")
		}
	}
	return nil
}

func (b *LibvirtBackend) Start(ctx context.Context, id int) error {
	name := b.domainNameFromID(ctx, id)
	cmdCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	out, err := b.virsh(cmdCtx, "start", name)
	if err != nil {
		return fmt.Errorf("start libvirt domain %s: %w (output: %s)", name, err, string(out))
	}
	return nil
}

func (b *LibvirtBackend) Stop(ctx context.Context, id int) error {
	name := b.domainNameFromID(ctx, id)
	cmdCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	out, err := b.virsh(cmdCtx, "shutdown", name)
	if err != nil {
		// Try forced shutdown.
		cmdCtx2, cancel2 := context.WithTimeout(ctx, b.timeout)
		defer cancel2()
		out, err = b.virsh(cmdCtx2, "destroy", name)
		if err != nil {
			return fmt.Errorf("stop libvirt domain %s: %w (output: %s)", name, err, string(out))
		}
	}
	return nil
}

func (b *LibvirtBackend) Suspend(ctx context.Context, id int) error {
	name := b.domainNameFromID(ctx, id)
	cmdCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	out, err := b.virsh(cmdCtx, "suspend", name)
	if err != nil {
		return fmt.Errorf("suspend libvirt domain %s: %w (output: %s)", name, err, string(out))
	}
	return nil
}

func (b *LibvirtBackend) Resume(ctx context.Context, id int) error {
	name := b.domainNameFromID(ctx, id)
	cmdCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	out, err := b.virsh(cmdCtx, "resume", name)
	if err != nil {
		return fmt.Errorf("resume libvirt domain %s: %w (output: %s)", name, err, string(out))
	}
	return nil
}

func (b *LibvirtBackend) Destroy(ctx context.Context, id int) error {
	name := b.domainNameFromID(ctx, id)
	// Undefine with storage cleanup.
	cmdCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	out, err := b.virsh(cmdCtx, "undefine", name, "--remove-all-storage", "--snapshots-metadata")
	if err != nil {
		// Try destroy first if it's running.
		cmdCtx2, cancel2 := context.WithTimeout(ctx, b.timeout)
		defer cancel2()
		_, _ = b.virsh(cmdCtx2, "destroy", name)
		cmdCtx3, cancel3 := context.WithTimeout(ctx, b.timeout)
		defer cancel3()
		out, err = b.virsh(cmdCtx3, "undefine", name, "--remove-all-storage", "--snapshots-metadata")
		if err != nil {
			return fmt.Errorf("destroy libvirt domain %s: %w (output: %s)", name, err, string(out))
		}
	}
	return nil
}

func (b *LibvirtBackend) Status(ctx context.Context, id int) (Status, error) {
	name := b.domainNameFromID(ctx, id)
	cmdCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	out, err := b.virsh(cmdCtx, "domstate", name)
	if err != nil {
		if bytes.Contains(out, []byte("not found")) || bytes.Contains(out, []byte("failed to get")) {
			return StatusUnknown, ErrContainerNotFound
		}
		return StatusUnknown, fmt.Errorf("libvirt domstate %s: %w", name, err)
	}
	state := strings.TrimSpace(string(out))
	switch state {
	case "running":
		return StatusRunning, nil
	case "shut off", "shutoff":
		return StatusStopped, nil
	case "paused", "pmsuspended":
		return StatusStopped, nil
	default:
		return StatusUnknown, nil
	}
}

func (b *LibvirtBackend) GuestIP(ctx context.Context, id int) (string, error) {
	name := b.domainNameFromID(ctx, id)
	cmdCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	out, err := b.virsh(cmdCtx, "domifaddr", name, "--source", "lease", "--full")
	if err != nil {
		return "", fmt.Errorf("libvirt domifaddr %s: %w", name, err)
	}
	// Parse output like:
	//  Name       MAC address          Protocol     Address
	//  vnet0      52:54:00:xx:xx:xx    ipv4         192.168.122.100/24
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		proto := fields[2]
		if proto != "ipv4" {
			continue
		}
		addr := fields[3]
		// Strip CIDR suffix.
		if idx := strings.Index(addr, "/"); idx >= 0 {
			addr = addr[:idx]
		}
		if addr != "" && !strings.HasPrefix(addr, "127.") {
			return addr, nil
		}
	}
	return "", ErrGuestIPNotFound
}

func (b *LibvirtBackend) List(ctx context.Context) ([]ContainerSummary, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	out, err := b.virsh(cmdCtx, "list", "--all", "--name")
	if err != nil {
		return nil, fmt.Errorf("libvirt list: %w", err)
	}
	var summaries []ContainerSummary
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, "agentlab-") {
			continue
		}
		id := b.parseIDFromName(name)
		status := StatusStopped
		// Check running state.
		stateCtx, stateCancel := context.WithTimeout(ctx, 10*time.Second)
		stateOut, stateErr := b.virsh(stateCtx, "domstate", name)
		stateCancel()
		if stateErr == nil && strings.TrimSpace(string(stateOut)) == "running" {
			status = StatusRunning
		}
		summaries = append(summaries, ContainerSummary{
			ID:     id,
			Name:   name,
			Status: status,
			Type:   TypeLibvirt,
		})
	}
	return summaries, nil
}

func (b *LibvirtBackend) CurrentStats(ctx context.Context, id int) (ContainerStats, error) {
	name := b.domainNameFromID(ctx, id)
	cmdCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	out, err := b.virsh(cmdCtx, "cpu-stats", name, "--percent")
	if err != nil {
		return ContainerStats{}, fmt.Errorf("libvirt cpu-stats %s: %w", name, err)
	}
	// Parse output looking for CPU utilization percentage.
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		if !strings.Contains(line, "CPU utilization") && !strings.Contains(line, "utilization") {
			continue
		}
		fields := strings.Fields(line)
		for _, f := range fields {
			val, err := strconv.ParseFloat(f, 64)
			if err == nil {
				return ContainerStats{CPUUsage: val / 100.0}, nil
			}
		}
	}
	return ContainerStats{}, nil
}

func (b *LibvirtBackend) ValidateTemplate(ctx context.Context, templateOrImage string) error {
	if templateOrImage == "" {
		return errors.New("image or template_id is required for libvirt backend")
	}
	// Check if the domain exists.
	cmdCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	_, err := b.virsh(cmdCtx, "dominfo", templateOrImage)
	return err
}

func (b *LibvirtBackend) SnapshotCreate(ctx context.Context, id int, name string) error {
	domainName := b.domainNameFromID(ctx, id)
	cmdCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	out, err := b.virsh(cmdCtx, "snapshot-create-as", domainName, name, "AgentLab snapshot")
	if err != nil {
		return fmt.Errorf("create snapshot %s on %s: %w (output: %s)", name, domainName, err, string(out))
	}
	return nil
}

func (b *LibvirtBackend) SnapshotRollback(ctx context.Context, id int, name string) error {
	domainName := b.domainNameFromID(ctx, id)
	cmdCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	out, err := b.virsh(cmdCtx, "snapshot-revert", domainName, name)
	if err != nil {
		return fmt.Errorf("rollback snapshot %s on %s: %w (output: %s)", name, domainName, err, string(out))
	}
	return nil
}

func (b *LibvirtBackend) SnapshotDelete(ctx context.Context, id int, name string) error {
	domainName := b.domainNameFromID(ctx, id)
	cmdCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	out, err := b.virsh(cmdCtx, "snapshot-delete", domainName, name)
	if err != nil {
		return fmt.Errorf("delete snapshot %s on %s: %w (output: %s)", name, domainName, err, string(out))
	}
	return nil
}

func (b *LibvirtBackend) SnapshotList(ctx context.Context, id int) ([]Snapshot, error) {
	domainName := b.domainNameFromID(ctx, id)
	cmdCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	out, err := b.virsh(cmdCtx, "snapshot-list", domainName)
	if err != nil {
		return nil, fmt.Errorf("list snapshots on %s: %w", domainName, err)
	}
	var snaps []Snapshot
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 1 {
			continue
		}
		name := fields[0]
		if name == "Name" || name == "---" || name == "" {
			continue
		}
		var createdAt time.Time
		if len(fields) >= 3 {
			tsStr := fields[1] + " " + fields[2]
			if t, err := time.Parse("2006-01-02 15:04:05", tsStr); err == nil {
				createdAt = t
			}
		}
		snaps = append(snaps, Snapshot{
			Name:      name,
			CreatedAt: createdAt,
		})
	}
	return snaps, nil
}

// HealthCheck verifies the libvirt daemon is reachable.
func (b *LibvirtBackend) HealthCheck(ctx context.Context) error {
	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := b.virsh(cmdCtx, "uri")
	if err != nil {
		return fmt.Errorf("libvirt connection failed: %w", err)
	}
	uri := strings.TrimSpace(string(out))
	if uri != b.uri {
		return fmt.Errorf("libvirt connected to %s, expected %s", uri, b.uri)
	}
	return nil
}

// domainName generates the libvirt domain name for a sandbox.
func (b *LibvirtBackend) domainName(id int, name string) string {
	if name != "" {
		return fmt.Sprintf("agentlab-%s-%d", sanitizeDockerName(name), id)
	}
	return fmt.Sprintf("agentlab-%d", id)
}

// domainNameFromID resolves the libvirt domain name for a sandbox ID.
func (b *LibvirtBackend) domainNameFromID(ctx context.Context, id int) string {
	return fmt.Sprintf("agentlab-%d", id)
}

// parseIDFromName extracts the numeric sandbox ID from a domain name.
func (b *LibvirtBackend) parseIDFromName(name string) int {
	// agentlab-123 or agentlab-name-123
	parts := strings.Split(name, "-")
	if len(parts) < 2 {
		return 0
	}
	id, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return 0
	}
	return id
}

// virsh runs a virsh command with the configured connection URI.
func (b *LibvirtBackend) virsh(ctx context.Context, args ...string) ([]byte, error) {
	fullArgs := append([]string{"--connect", b.uri}, args...)
	cmd := exec.CommandContext(ctx, "virsh", fullArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("virsh %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// Compile-time check that LibvirtBackend implements Backend.
var _ Backend = (*LibvirtBackend)(nil)
