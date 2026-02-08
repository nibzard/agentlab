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
