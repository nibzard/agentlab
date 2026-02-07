package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/config"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
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

func TestNewServicePropagatesAgentSubnetToShellBackend(t *testing.T) {
	temp := t.TempDir()
	cfg := config.Config{
		RunDir:                  filepath.Join(temp, "run"),
		SocketPath:              filepath.Join(temp, "run", "agentlabd.sock"),
		DBPath:                  filepath.Join(temp, "agentlab.db"),
		BootstrapListen:         "127.0.0.1:0",
		ArtifactListen:          "127.0.0.1:0",
		AgentSubnet:             "10.77.0.0/16",
		ArtifactDir:             filepath.Join(temp, "artifacts"),
		ArtifactMaxBytes:        1024,
		ArtifactTokenTTLMinutes: 5,
		SecretsDir:              filepath.Join(temp, "secrets"),
		SecretsBundle:           "default",
		SecretsAgeKeyPath:       filepath.Join(temp, "age.key"),
		SecretsSopsPath:         "sops",
		SnippetsDir:             filepath.Join(temp, "snippets"),
		SnippetStorage:          "local",
		ProxmoxCommandTimeout:   time.Second,
		ProvisioningTimeout:     time.Second,
	}
	store, err := db.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	service, err := NewService(cfg, map[string]models.Profile{}, store)
	if err != nil {
		_ = store.Close()
		t.Fatalf("NewService() error = %v", err)
	}
	t.Cleanup(func() {
		if service.unixListener != nil {
			_ = service.unixListener.Close()
		}
		if service.bootstrapListener != nil {
			_ = service.bootstrapListener.Close()
		}
		if service.artifactListener != nil {
			_ = service.artifactListener.Close()
		}
		if service.metricsListener != nil {
			_ = service.metricsListener.Close()
		}
		if service.store != nil {
			_ = service.store.Close()
		}
		_ = os.Remove(cfg.SocketPath)
	})

	backend, ok := service.sandboxManager.backend.(*proxmox.ShellBackend)
	if !ok {
		t.Fatalf("sandbox backend = %T, want *proxmox.ShellBackend", service.sandboxManager.backend)
	}
	if backend.AgentCIDR != cfg.AgentSubnet {
		t.Fatalf("backend.AgentCIDR = %q, want %q", backend.AgentCIDR, cfg.AgentSubnet)
	}

	leaseDir := t.TempDir()
	leasePath := filepath.Join(leaseDir, "dnsmasq.leases")
	if err := os.WriteFile(leasePath, []byte(""), 0o600); err != nil {
		t.Fatalf("write lease: %v", err)
	}

	agentJSON := `{"result":[{"name":"eth0","ip-addresses":[{"ip-address":"192.168.1.10","ip-address-type":"ipv4"},{"ip-address":"10.77.0.9","ip-address-type":"ipv4"}]}]}`
	backend.Runner = &fakeRunner{responses: []runnerResponse{
		{stdout: "net0: virtio=52:54:00:aa:bb:cc,bridge=vmbr1\n"}, // qm config (DHCP fallback)
		{stdout: agentJSON}, // pvesh get .../agent/network-get-interfaces
	}}
	backend.Node = "pve"
	backend.GuestIPAttempts = 1
	backend.DHCPLeasePaths = []string{leasePath}

	ip, err := backend.GuestIP(context.Background(), 123)
	if err != nil {
		t.Fatalf("GuestIP() error = %v", err)
	}
	if ip != "10.77.0.9" {
		t.Fatalf("GuestIP() = %q, want %q", ip, "10.77.0.9")
	}
}
