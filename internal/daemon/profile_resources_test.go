package daemon

import (
	"testing"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

func TestApplyProfileVMConfigNetworkModeMapping(t *testing.T) {
	profile := models.Profile{
		RawYAML: `
network:
  mode: allowlist
`,
	}
	cfg, err := applyProfileVMConfig(profile, proxmox.VMConfig{})
	if err != nil {
		t.Fatalf("applyProfileVMConfig: %v", err)
	}
	if cfg.FirewallGroup != firewallGroupNatAllowlist {
		t.Fatalf("expected firewall group %q, got %q", firewallGroupNatAllowlist, cfg.FirewallGroup)
	}
	if cfg.Firewall == nil || !*cfg.Firewall {
		t.Fatalf("expected firewall enabled, got %+v", cfg.Firewall)
	}
}

func TestApplyProfileVMConfigCPUOverCommit(t *testing.T) {
	profile := models.Profile{
		RawYAML: `
resources:
  cores: 4
  memory_mb: 8192
  cpu_over_commit: 2.0
`,
	}
	cfg, err := applyProfileVMConfig(profile, proxmox.VMConfig{})
	if err != nil {
		t.Fatalf("applyProfileVMConfig: %v", err)
	}
	if cfg.Cores != 4 {
		t.Errorf("expected 4 cores, got %d", cfg.Cores)
	}
	if cfg.MemoryMB != 8192 {
		t.Errorf("expected 8192 MB, got %d", cfg.MemoryMB)
	}
	if cfg.CPULimit != 2.0 {
		t.Errorf("expected CPULimit 2.0, got %f", cfg.CPULimit)
	}
}

func TestApplyProfileVMConfigNoOverCommit(t *testing.T) {
	profile := models.Profile{
		RawYAML: `
resources:
  cores: 2
  memory_mb: 4096
`,
	}
	cfg, err := applyProfileVMConfig(profile, proxmox.VMConfig{})
	if err != nil {
		t.Fatalf("applyProfileVMConfig: %v", err)
	}
	if cfg.CPULimit != 0 {
		t.Errorf("expected CPULimit 0 when not set, got %f", cfg.CPULimit)
	}
}

func TestApplyProfileVMConfigStaticAddressingAndBalloon(t *testing.T) {
	profile := models.Profile{
		RawYAML: `
network:
  bridge: vmbr1
  model: virtio
  ip_config0: "ip=10.77.0.22/24,gw=10.77.0.1"
  nameserver: "10.77.0.1"
resources:
  cores: 1
  memory_mb: 1024
  balloon_mb: 512
`,
	}
	cfg, err := applyProfileVMConfig(profile, proxmox.VMConfig{})
	if err != nil {
		t.Fatalf("applyProfileVMConfig: %v", err)
	}
	if cfg.IPConfig0 != "ip=10.77.0.22/24,gw=10.77.0.1" {
		t.Errorf("expected ipconfig0 mapping, got %q", cfg.IPConfig0)
	}
	if cfg.Nameserver != "10.77.0.1" {
		t.Errorf("expected nameserver mapping, got %q", cfg.Nameserver)
	}
	if cfg.BalloonMB != 512 {
		t.Errorf("expected balloon 512, got %d", cfg.BalloonMB)
	}
}

func TestApplyProfileVMConfigCloudInitKeysOptional(t *testing.T) {
	profile := models.Profile{
		RawYAML: `
network:
  bridge: vmbr1
resources:
  cores: 1
  memory_mb: 1024
`,
	}
	cfg, err := applyProfileVMConfig(profile, proxmox.VMConfig{})
	if err != nil {
		t.Fatalf("applyProfileVMConfig: %v", err)
	}
	if cfg.IPConfig0 != "" || cfg.Nameserver != "" || cfg.BalloonMB != 0 {
		t.Errorf("expected zero cloud-init extras, got ipconfig0=%q nameserver=%q balloon=%d",
			cfg.IPConfig0, cfg.Nameserver, cfg.BalloonMB)
	}
}
