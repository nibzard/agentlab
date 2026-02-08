package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/proxmox"
)

func TestLoadProfilesPerDocumentYAML(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: alpha
template_vmid: 9000
network:
  mode: off
behavior:
  keepalive_default: false
  ttl_minutes_default: 10
  inner_sandbox: kata
artifacts:
  retention_minutes: 5
---
name: beta
template_vmid: 9001
network:
  mode: allowlist
resources:
  cores: 6
  memory_mb: 4096
behavior:
  keepalive_default: true
  ttl_minutes_default: 99
  inner_sandbox: bubblewrap
artifacts:
  retention_hours: 2
`
	if err := os.WriteFile(filepath.Join(dir, "profiles.yaml"), []byte(content), 0o600); err != nil {
		t.Fatalf("write profiles: %v", err)
	}

	profiles, err := LoadProfiles(dir)
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	alpha, ok := profiles["alpha"]
	if !ok {
		t.Fatalf("expected alpha profile loaded")
	}
	beta, ok := profiles["beta"]
	if !ok {
		t.Fatalf("expected beta profile loaded")
	}

	if strings.Contains(alpha.RawYAML, "beta") {
		t.Fatalf("alpha RawYAML contains beta document")
	}
	if strings.Contains(beta.RawYAML, "alpha") {
		t.Fatalf("beta RawYAML contains alpha document")
	}

	if err := validateProfileInnerSandbox(alpha); err == nil {
		t.Fatalf("expected alpha inner_sandbox validation error")
	}
	if err := validateProfileInnerSandbox(beta); err != nil {
		t.Fatalf("expected beta inner_sandbox valid, got %v", err)
	}

	alphaTTL, alphaKeepalive, err := applyProfileBehaviorDefaults(alpha, nil, nil)
	if err != nil {
		t.Fatalf("alpha behavior defaults: %v", err)
	}
	if alphaTTL != 10 {
		t.Fatalf("expected alpha ttl 10, got %d", alphaTTL)
	}
	if alphaKeepalive {
		t.Fatalf("expected alpha keepalive false")
	}

	betaTTL, betaKeepalive, err := applyProfileBehaviorDefaults(beta, nil, nil)
	if err != nil {
		t.Fatalf("beta behavior defaults: %v", err)
	}
	if betaTTL != 99 {
		t.Fatalf("expected beta ttl 99, got %d", betaTTL)
	}
	if !betaKeepalive {
		t.Fatalf("expected beta keepalive true")
	}

	cfgAlpha, err := applyProfileVMConfig(alpha, proxmox.VMConfig{})
	if err != nil {
		t.Fatalf("applyProfileVMConfig alpha: %v", err)
	}
	if cfgAlpha.FirewallGroup != firewallGroupNetOff {
		t.Fatalf("expected alpha firewall group %q, got %q", firewallGroupNetOff, cfgAlpha.FirewallGroup)
	}

	cfgBeta, err := applyProfileVMConfig(beta, proxmox.VMConfig{})
	if err != nil {
		t.Fatalf("applyProfileVMConfig beta: %v", err)
	}
	if cfgBeta.FirewallGroup != firewallGroupNatAllowlist {
		t.Fatalf("expected beta firewall group %q, got %q", firewallGroupNatAllowlist, cfgBeta.FirewallGroup)
	}
	if cfgBeta.Cores != 6 {
		t.Fatalf("expected beta cores 6, got %d", cfgBeta.Cores)
	}
	if cfgBeta.MemoryMB != 4096 {
		t.Fatalf("expected beta memory 4096, got %d", cfgBeta.MemoryMB)
	}

	alphaRetention, alphaConfigured, err := parseArtifactRetention(alpha.RawYAML)
	if err != nil {
		t.Fatalf("alpha retention: %v", err)
	}
	if !alphaConfigured {
		t.Fatalf("expected alpha retention configured")
	}
	if alphaRetention != 5*time.Minute {
		t.Fatalf("expected alpha retention 5m, got %s", alphaRetention)
	}

	betaRetention, betaConfigured, err := parseArtifactRetention(beta.RawYAML)
	if err != nil {
		t.Fatalf("beta retention: %v", err)
	}
	if !betaConfigured {
		t.Fatalf("expected beta retention configured")
	}
	if betaRetention != 2*time.Hour {
		t.Fatalf("expected beta retention 2h, got %s", betaRetention)
	}
}
