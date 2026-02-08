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

func TestShellBackendConfigure(t *testing.T) {
	runner := &fakeRunner{responses: []runnerResponse{{}}}
	backend := &ShellBackend{Runner: runner}

	cfg := VMConfig{
		Name:       "sandbox-101",
		Cores:      4,
		MemoryMB:   4096,
		Bridge:     "vmbr1",
		NetModel:   "virtio",
		CloudInit:  "local:snippets/ci.yaml",
		CPUPinning: "0-3",
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
			"--net0", "virtio,bridge=vmbr1",
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

func TestShellBackendSnapshotOps(t *testing.T) {
	runner := &fakeRunner{responses: []runnerResponse{{}, {}, {}}}
	backend := &ShellBackend{Runner: runner}

	if err := backend.SnapshotCreate(context.Background(), 101, "clean"); err != nil {
		t.Fatalf("SnapshotCreate() error = %v", err)
	}
	if err := backend.SnapshotRollback(context.Background(), 101, "clean"); err != nil {
		t.Fatalf("SnapshotRollback() error = %v", err)
	}
	if err := backend.SnapshotDelete(context.Background(), 101, "clean"); err != nil {
		t.Fatalf("SnapshotDelete() error = %v", err)
	}

	want := []runnerCall{
		{name: "qm", args: []string{"snapshot", "101", "clean"}},
		{name: "qm", args: []string{"rollback", "101", "clean"}},
		{name: "qm", args: []string{"delsnapshot", "101", "clean"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("Snapshot calls = %#v, want %#v", runner.calls, want)
	}
}
