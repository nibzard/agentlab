package proxmox

import (
	"context"
	"errors"
	"reflect"
	"testing"
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

func TestShellBackendGuestIPWithNodeDiscovery(t *testing.T) {
	nodeJSON := `{"data":[{"node":"pve"}]}`
	agentJSON := `{"result":[{"name":"lo","ip-addresses":[{"ip-address":"127.0.0.1","ip-address-type":"ipv4"}]},{"name":"eth0","ip-addresses":[{"ip-address":"10.77.0.5","ip-address-type":"ipv4"}]}]}`
	responses := []runnerResponse{{stdout: nodeJSON}, {stdout: agentJSON}}
	runner := &fakeRunner{responses: responses}
	backend := &ShellBackend{Runner: runner, AgentCIDR: "10.77.0.0/16"}

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
	backend := &ShellBackend{Runner: runner, Node: "pve", AgentCIDR: "10.77.0.0/16"}

	ip, err := backend.GuestIP(context.Background(), 222)
	if err != nil {
		t.Fatalf("GuestIP() error = %v", err)
	}
	if ip != "10.77.0.9" {
		t.Fatalf("GuestIP() = %q, want %q", ip, "10.77.0.9")
	}
}
