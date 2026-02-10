package proxmox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type runnerCall struct {
	name string
	args []string
}

type runnerResponse struct {
	stdout string
	err    error
}

type fakeRunner struct {
	calls     []runnerCall
	responses []runnerResponse
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	r.calls = append(r.calls, runnerCall{name: name, args: append([]string(nil), args...)})
	idx := len(r.calls) - 1
	if idx >= len(r.responses) {
		return "", errors.New("unexpected command call")
	}
	resp := r.responses[idx]
	return resp.stdout, resp.err
}

func TestShellBackendClone(t *testing.T) {
	runner := &fakeRunner{responses: []runnerResponse{{}}}
	backend := &ShellBackend{Runner: runner}

	err := backend.Clone(context.Background(), 9000, 101, "sandbox-101")
	if err != nil {
		t.Fatalf("Clone() error = %v", err)
	}

	want := []runnerCall{{
		name: "qm",
		args: []string{"clone", "9000", "101", "--full", "0", "--name", "sandbox-101"},
	}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("Clone() calls = %#v, want %#v", runner.calls, want)
	}
}

func TestShellBackendCloneFull(t *testing.T) {
	runner := &fakeRunner{responses: []runnerResponse{{}}}
	backend := &ShellBackend{Runner: runner, CloneMode: "full"}

	err := backend.Clone(context.Background(), 9000, 101, "sandbox-101")
	if err != nil {
		t.Fatalf("Clone() error = %v", err)
	}

	want := []runnerCall{{
		name: "qm",
		args: []string{"clone", "9000", "101", "--full", "1", "--name", "sandbox-101"},
	}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("Clone() calls = %#v, want %#v", runner.calls, want)
	}
}

func TestShellBackendConfigure(t *testing.T) {
	runner := &fakeRunner{responses: []runnerResponse{{}}}
	backend := &ShellBackend{Runner: runner}

	firewall := true
	cfg := VMConfig{
		Name:          "sandbox-101",
		Cores:         4,
		MemoryMB:      4096,
		Bridge:        "vmbr1",
		NetModel:      "virtio",
		Firewall:      &firewall,
		FirewallGroup: "agent_nat_default",
		CloudInit:     "local:snippets/ci.yaml",
		CPUPinning:    "0-3",
	}

	err := backend.Configure(context.Background(), 101, cfg)
	if err != nil {
		t.Fatalf("Configure() error = %v", err)
	}

	want := []runnerCall{{
		name: "qm",
		args: []string{
			"set", "101",
			"--name", "sandbox-101",
			"--cores", "4",
			"--memory", "4096",
			"--cpulist", "0-3",
			"--net0", "virtio,bridge=vmbr1,firewall=1,fwgroup=agent_nat_default",
			"--cicustom", "user=local:snippets/ci.yaml",
		},
	}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("Configure() calls = %#v, want %#v", runner.calls, want)
	}
}

func TestShellBackendStatus(t *testing.T) {
	runner := &fakeRunner{responses: []runnerResponse{{stdout: "status: running\n"}}}
	backend := &ShellBackend{Runner: runner}

	status, err := backend.Status(context.Background(), 101)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status != StatusRunning {
		t.Fatalf("Status() = %q, want %q", status, StatusRunning)
	}
}

func TestShellBackendSuspendResume(t *testing.T) {
	runner := &fakeRunner{responses: []runnerResponse{{}, {}}}
	backend := &ShellBackend{Runner: runner}

	if err := backend.Suspend(context.Background(), 101); err != nil {
		t.Fatalf("Suspend() error = %v", err)
	}
	if err := backend.Resume(context.Background(), 101); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}

	want := []runnerCall{
		{name: "qm", args: []string{"suspend", "101", "--todisk", "0"}},
		{name: "qm", args: []string{"resume", "101"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("Suspend/Resume calls = %#v, want %#v", runner.calls, want)
	}
}

func TestShellBackendCurrentStats(t *testing.T) {
	runner := &fakeRunner{responses: []runnerResponse{{stdout: `{"cpu":0.07}`}}}
	backend := &ShellBackend{Runner: runner, Node: "pve"}

	stats, err := backend.CurrentStats(context.Background(), 101)
	if err != nil {
		t.Fatalf("CurrentStats() error = %v", err)
	}
	if stats.CPUUsage != 0.07 {
		t.Fatalf("CurrentStats().CPUUsage = %v, want %v", stats.CPUUsage, 0.07)
	}

	want := []runnerCall{{
		name: "pvesh",
		args: []string{"get", "/nodes/pve/qemu/101/status/current", "--output-format", "json"},
	}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("CurrentStats() calls = %#v, want %#v", runner.calls, want)
	}
}

func TestShellBackendGuestIPWithNodeDiscovery(t *testing.T) {
	nodeJSON := `{"data":[{"node":"pve"}]}`
	agentJSON := `{"result":[{"name":"lo","ip-addresses":[{"ip-address":"127.0.0.1","ip-address-type":"ipv4"}]},{"name":"eth0","ip-addresses":[{"ip-address":"10.77.0.5","ip-address-type":"ipv4"}]}]}`
	responses := []runnerResponse{{stdout: nodeJSON}, {stdout: agentJSON}}
	runner := &fakeRunner{responses: responses}
	backend := &ShellBackend{Runner: runner, AgentCIDR: "10.77.0.0/16", GuestIPAttempts: 1}

	ip, err := backend.GuestIP(context.Background(), 101)
	if err != nil {
		t.Fatalf("GuestIP() error = %v", err)
	}
	if ip != "10.77.0.5" {
		t.Fatalf("GuestIP() = %q, want %q", ip, "10.77.0.5")
	}

	wantCalls := []runnerCall{
		{name: "pvesh", args: []string{"get", "/nodes", "--output-format", "json"}},
		{name: "pvesh", args: []string{"get", "/nodes/pve/qemu/101/agent/network-get-interfaces", "--output-format", "json"}},
	}
	if !reflect.DeepEqual(runner.calls, wantCalls) {
		t.Fatalf("GuestIP() calls = %#v, want %#v", runner.calls, wantCalls)
	}
}

func TestShellBackendGuestIPPrefersCIDR(t *testing.T) {
	agentJSON := `{"result":[{"name":"eth0","ip-addresses":[{"ip-address":"192.168.1.10","ip-address-type":"ipv4"},{"ip-address":"10.77.0.9","ip-address-type":"ipv4"}]}]}`
	runner := &fakeRunner{responses: []runnerResponse{{stdout: agentJSON}}}
	backend := &ShellBackend{Runner: runner, Node: "pve", AgentCIDR: "10.77.0.0/16", GuestIPAttempts: 1}

	ip, err := backend.GuestIP(context.Background(), 222)
	if err != nil {
		t.Fatalf("GuestIP() error = %v", err)
	}
	if ip != "10.77.0.9" {
		t.Fatalf("GuestIP() = %q, want %q", ip, "10.77.0.9")
	}
}

func TestShellBackendGuestIPPollsAgent(t *testing.T) {
	nodeJSON := `{"data":[{"node":"pve"}]}`
	agentNoIP := `{"result":[{"name":"lo","ip-addresses":[{"ip-address":"127.0.0.1","ip-address-type":"ipv4"}]}]}`
	agentIP := `{"result":[{"name":"eth0","ip-addresses":[{"ip-address":"10.77.0.42","ip-address-type":"ipv4"}]}]}`
	responses := []runnerResponse{
		{stdout: nodeJSON},
		{stdout: agentNoIP},
		{stdout: agentNoIP},
		{stdout: agentIP},
	}
	runner := &fakeRunner{responses: responses}
	backend := &ShellBackend{
		Runner:             runner,
		AgentCIDR:          "10.77.0.0/16",
		GuestIPAttempts:    3,
		GuestIPInitialWait: 10 * time.Millisecond,
		GuestIPMaxWait:     10 * time.Millisecond,
		Sleep: func(_ context.Context, _ time.Duration) error {
			return nil
		},
	}

	ip, err := backend.GuestIP(context.Background(), 101)
	if err != nil {
		t.Fatalf("GuestIP() error = %v", err)
	}
	if ip != "10.77.0.42" {
		t.Fatalf("GuestIP() = %q, want %q", ip, "10.77.0.42")
	}

	wantCalls := []runnerCall{
		{name: "pvesh", args: []string{"get", "/nodes", "--output-format", "json"}},
		{name: "pvesh", args: []string{"get", "/nodes/pve/qemu/101/agent/network-get-interfaces", "--output-format", "json"}},
		{name: "pvesh", args: []string{"get", "/nodes/pve/qemu/101/agent/network-get-interfaces", "--output-format", "json"}},
		{name: "pvesh", args: []string{"get", "/nodes/pve/qemu/101/agent/network-get-interfaces", "--output-format", "json"}},
	}
	if !reflect.DeepEqual(runner.calls, wantCalls) {
		t.Fatalf("GuestIP() calls = %#v, want %#v", runner.calls, wantCalls)
	}
}

func TestShellBackendGuestIPDHCPFallback(t *testing.T) {
	leaseDir := t.TempDir()
	leasePath := filepath.Join(leaseDir, "dnsmasq.leases")
	leaseContent := "1738159200 52:54:00:aa:bb:cc 10.77.0.55 sandbox *\n"
	if err := os.WriteFile(leasePath, []byte(leaseContent), 0o600); err != nil {
		t.Fatalf("write lease: %v", err)
	}

	responses := []runnerResponse{
		{err: errors.New("guest agent unavailable")},
		{stdout: "net0: virtio=52:54:00:aa:bb:cc,bridge=vmbr1\n"},
	}
	runner := &fakeRunner{responses: responses}
	backend := &ShellBackend{
		Runner:          runner,
		Node:            "pve",
		AgentCIDR:       "10.77.0.0/16",
		GuestIPAttempts: 1,
		DHCPLeasePaths:  []string{leasePath},
	}

	ip, err := backend.GuestIP(context.Background(), 101)
	if err != nil {
		t.Fatalf("GuestIP() error = %v", err)
	}
	if ip != "10.77.0.55" {
		t.Fatalf("GuestIP() = %q, want %q", ip, "10.77.0.55")
	}

	wantCalls := []runnerCall{
		{name: "pvesh", args: []string{"get", "/nodes/pve/qemu/101/agent/network-get-interfaces", "--output-format", "json"}},
		{name: "qm", args: []string{"config", "101"}},
	}
	if !reflect.DeepEqual(runner.calls, wantCalls) {
		t.Fatalf("GuestIP() calls = %#v, want %#v", runner.calls, wantCalls)
	}
}

func TestShellBackendVMConfig(t *testing.T) {
	config := "name: test-vm\nscsi1: local-zfs:vm-101-disk-1,size=10G\nagent: 1\n"
	runner := &fakeRunner{responses: []runnerResponse{{stdout: config}}}
	backend := &ShellBackend{Runner: runner}

	got, err := backend.VMConfig(context.Background(), 101)
	if err != nil {
		t.Fatalf("VMConfig() error = %v", err)
	}
	if got["name"] != "test-vm" {
		t.Fatalf("VMConfig name = %q", got["name"])
	}
	if got["scsi1"] != "local-zfs:vm-101-disk-1,size=10G" {
		t.Fatalf("VMConfig scsi1 = %q", got["scsi1"])
	}

	want := []runnerCall{{
		name: "qm",
		args: []string{"config", "101"},
	}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("VMConfig calls = %#v, want %#v", runner.calls, want)
	}
}

func TestShellBackendVolumeInfo(t *testing.T) {
	runner := &fakeRunner{responses: []runnerResponse{{stdout: "/rpool/data/vm-0-disk-0\n"}}}
	backend := &ShellBackend{Runner: runner}

	info, err := backend.VolumeInfo(context.Background(), "local-zfs:vm-0-disk-0")
	if err != nil {
		t.Fatalf("VolumeInfo() error = %v", err)
	}
	if info.Path != "/rpool/data/vm-0-disk-0" {
		t.Fatalf("VolumeInfo path = %q", info.Path)
	}
	if info.Storage != "local-zfs" {
		t.Fatalf("VolumeInfo storage = %q", info.Storage)
	}

	want := []runnerCall{{
		name: "pvesm",
		args: []string{"path", "local-zfs:vm-0-disk-0"},
	}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("VolumeInfo calls = %#v, want %#v", runner.calls, want)
	}
}

func TestShellBackendCreateVolume(t *testing.T) {
	runner := &fakeRunner{responses: []runnerResponse{{stdout: "local-zfs:vm-0-disk-1\n"}}}
	backend := &ShellBackend{Runner: runner}

	volid, err := backend.CreateVolume(context.Background(), "local-zfs", "workspace-abc", 80)
	if err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}
	if volid != "local-zfs:vm-0-disk-1" {
		t.Fatalf("CreateVolume() = %q, want %q", volid, "local-zfs:vm-0-disk-1")
	}
	want := []runnerCall{{
		name: "pvesm",
		args: []string{"alloc", "local-zfs", "0", "workspace-abc", "80G"},
	}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("CreateVolume() calls = %#v, want %#v", runner.calls, want)
	}
}

func TestShellBackendAttachDetachVolume(t *testing.T) {
	runner := &fakeRunner{responses: []runnerResponse{{}, {}}}
	backend := &ShellBackend{Runner: runner}

	if err := backend.AttachVolume(context.Background(), 101, "local-zfs:vm-0-disk-1", "scsi1"); err != nil {
		t.Fatalf("AttachVolume() error = %v", err)
	}
	if err := backend.DetachVolume(context.Background(), 101, "scsi1"); err != nil {
		t.Fatalf("DetachVolume() error = %v", err)
	}
	want := []runnerCall{
		{name: "qm", args: []string{"set", "101", "--scsi1", "local-zfs:vm-0-disk-1"}},
		{name: "qm", args: []string{"set", "101", "--delete", "scsi1"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("Attach/Detach calls = %#v, want %#v", runner.calls, want)
	}
}

func TestShellBackendDeleteVolume(t *testing.T) {
	runner := &fakeRunner{responses: []runnerResponse{{}}}
	backend := &ShellBackend{Runner: runner}

	if err := backend.DeleteVolume(context.Background(), "local-zfs:vm-0-disk-1"); err != nil {
		t.Fatalf("DeleteVolume() error = %v", err)
	}
	want := []runnerCall{{
		name: "pvesm",
		args: []string{"free", "local-zfs:vm-0-disk-1"},
	}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("DeleteVolume() calls = %#v, want %#v", runner.calls, want)
	}
}

func TestShellBackendVolumeSnapshotCommands(t *testing.T) {
	statusJSON := `[{"storage":"local-zfs","type":"zfspool"}]`
	tests := []struct {
		name     string
		call     func(ctx context.Context, backend *ShellBackend) error
		wantArgs []string
	}{
		{
			name: "create",
			call: func(ctx context.Context, backend *ShellBackend) error {
				return backend.VolumeSnapshotCreate(ctx, "local-zfs:vm-0-disk-1", "snap1")
			},
			wantArgs: []string{"snapshot", "local-zfs:vm-0-disk-1", "snap1"},
		},
		{
			name: "restore",
			call: func(ctx context.Context, backend *ShellBackend) error {
				return backend.VolumeSnapshotRestore(ctx, "local-zfs:vm-0-disk-1", "snap1")
			},
			wantArgs: []string{"rollback", "local-zfs:vm-0-disk-1", "snap1"},
		},
		{
			name: "delete",
			call: func(ctx context.Context, backend *ShellBackend) error {
				return backend.VolumeSnapshotDelete(ctx, "local-zfs:vm-0-disk-1", "snap1")
			},
			wantArgs: []string{"delsnapshot", "local-zfs:vm-0-disk-1", "snap1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeRunner{responses: []runnerResponse{{stdout: statusJSON}, {}}}
			backend := &ShellBackend{Runner: runner}
			if err := tt.call(context.Background(), backend); err != nil {
				t.Fatalf("Volume snapshot %s error = %v", tt.name, err)
			}
			want := []runnerCall{
				{name: "pvesm", args: []string{"status", "--storage", "local-zfs", "--output-format", "json"}},
				{name: "pvesm", args: tt.wantArgs},
			}
			if !reflect.DeepEqual(runner.calls, want) {
				t.Fatalf("Volume snapshot %s calls = %#v, want %#v", tt.name, runner.calls, want)
			}
		})
	}
}

func TestShellBackendVolumeClone(t *testing.T) {
	runner := &fakeRunner{responses: []runnerResponse{{stdout: `[{"storage":"local-zfs","type":"zfspool"}]`}, {}}}
	backend := &ShellBackend{Runner: runner}

	if err := backend.VolumeClone(context.Background(), "local-zfs:vm-0-disk-1", "local-zfs:vm-0-disk-2"); err != nil {
		t.Fatalf("VolumeClone() error = %v", err)
	}
	want := []runnerCall{
		{name: "pvesm", args: []string{"status", "--storage", "local-zfs", "--output-format", "json"}},
		{name: "pvesm", args: []string{"clone", "local-zfs:vm-0-disk-1", "local-zfs:vm-0-disk-2"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("VolumeClone() calls = %#v, want %#v", runner.calls, want)
	}
}

func TestShellBackendVolumeSnapshotUnsupportedStorage(t *testing.T) {
	runner := &fakeRunner{responses: []runnerResponse{{stdout: `[{"storage":"local-lvm","type":"lvm"}]`}}}
	backend := &ShellBackend{Runner: runner}

	err := backend.VolumeSnapshotCreate(context.Background(), "local-lvm:vm-0-disk-1", "snap1")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrStorageUnsupported) {
		t.Fatalf("expected ErrStorageUnsupported, got %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
}

func TestShellBackendVolumeCloneCrossStorage(t *testing.T) {
	backend := &ShellBackend{Runner: &fakeRunner{}}
	err := backend.VolumeClone(context.Background(), "local-zfs:vm-0-disk-1", "local-lvm:vm-0-disk-2")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrStorageUnsupported) {
		t.Fatalf("expected ErrStorageUnsupported, got %v", err)
	}
}

func TestShellBackendSnapshotOps(t *testing.T) {
	runner := &fakeRunner{responses: []runnerResponse{{}, {}, {}, {stdout: `[{"name":"clean","snaptime":1730000000}]`}}}
	backend := &ShellBackend{Runner: runner, Node: "pve"}

	if err := backend.SnapshotCreate(context.Background(), 101, "clean"); err != nil {
		t.Fatalf("SnapshotCreate() error = %v", err)
	}
	if err := backend.SnapshotRollback(context.Background(), 101, "clean"); err != nil {
		t.Fatalf("SnapshotRollback() error = %v", err)
	}
	if err := backend.SnapshotDelete(context.Background(), 101, "clean"); err != nil {
		t.Fatalf("SnapshotDelete() error = %v", err)
	}
	snapshots, err := backend.SnapshotList(context.Background(), 101)
	if err != nil {
		t.Fatalf("SnapshotList() error = %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].Name != "clean" {
		t.Fatalf("SnapshotList() = %#v, want [clean]", snapshots)
	}

	want := []runnerCall{
		{name: "qm", args: []string{"snapshot", "101", "clean"}},
		{name: "qm", args: []string{"rollback", "101", "clean"}},
		{name: "qm", args: []string{"delsnapshot", "101", "clean"}},
		{name: "pvesh", args: []string{"get", "/nodes/pve/qemu/101/snapshot", "--output-format", "json"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("Snapshot calls = %#v, want %#v", runner.calls, want)
	}
}
