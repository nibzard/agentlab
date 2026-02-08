package daemon

import (
	"fmt"
	"strings"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
	"gopkg.in/yaml.v3"
)

type profileProvisionSpec struct {
	Network   profileNetworkSpec  `yaml:"network"`
	Resources profileResourceSpec `yaml:"resources"`
}

type profileNetworkSpec struct {
	Bridge        string  `yaml:"bridge"`
	Model         string  `yaml:"model"`
	Firewall      *bool   `yaml:"firewall"`
	FirewallGroup *string `yaml:"firewall_group"`
}

type profileResourceSpec struct {
	Cores      int    `yaml:"cores"`
	MemoryMB   int    `yaml:"memory_mb"`
	CPUPinning string `yaml:"cpulist"`
}

func applyProfileVMConfig(profile models.Profile, cfg proxmox.VMConfig) (proxmox.VMConfig, error) {
	spec, err := parseProfileProvisionSpec(profile.RawYAML)
	if err != nil {
		return cfg, err
	}
	if spec.Resources.Cores > 0 {
		cfg.Cores = spec.Resources.Cores
	}
	if spec.Resources.MemoryMB > 0 {
		cfg.MemoryMB = spec.Resources.MemoryMB
	}
	if strings.TrimSpace(spec.Resources.CPUPinning) != "" {
		cfg.CPUPinning = strings.TrimSpace(spec.Resources.CPUPinning)
	}
	if strings.TrimSpace(spec.Network.Bridge) != "" {
		cfg.Bridge = strings.TrimSpace(spec.Network.Bridge)
	}
	if strings.TrimSpace(spec.Network.Model) != "" {
		cfg.NetModel = strings.TrimSpace(spec.Network.Model)
	}
	if spec.Network.Firewall != nil {
		cfg.Firewall = spec.Network.Firewall
	}
	if spec.Network.FirewallGroup != nil {
		group, err := normalizeFirewallGroup(*spec.Network.FirewallGroup)
		if err != nil {
			return cfg, err
		}
		if spec.Network.Firewall != nil && !*spec.Network.Firewall {
			return cfg, fmt.Errorf("network.firewall_group requires network.firewall=true")
		}
		if spec.Network.Firewall == nil {
			value := true
			cfg.Firewall = &value
		}
		cfg.FirewallGroup = group
	}
	return cfg, nil
}

func parseProfileProvisionSpec(raw string) (profileProvisionSpec, error) {
	var spec profileProvisionSpec
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return spec, nil
	}
	if err := yaml.Unmarshal([]byte(raw), &spec); err != nil {
		return spec, err
	}
	return spec, nil
}

func normalizeFirewallGroup(value string) (string, error) {
	group := strings.TrimSpace(value)
	if group == "" {
		return "", fmt.Errorf("network.firewall_group must not be empty")
	}
	for _, r := range group {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return "", fmt.Errorf("network.firewall_group %q contains invalid characters", group)
		}
	}
	return group, nil
}
