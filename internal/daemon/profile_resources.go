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
	Storage   profileStorageSpec  `yaml:"storage"`
}

type profileNetworkSpec struct {
	Bridge        string  `yaml:"bridge"`
	Model         string  `yaml:"model"`
	Mode          *string `yaml:"mode"`
	Firewall      *bool   `yaml:"firewall"`
	FirewallGroup *string `yaml:"firewall_group"`
}

type profileResourceSpec struct {
	Cores      int    `yaml:"cores"`
	MemoryMB   int    `yaml:"memory_mb"`
	CPUPinning string `yaml:"cpulist"`
}

type profileStorageSpec struct {
	RootSizeGB int    `yaml:"root_size_gb"`
	SCSIHW     string `yaml:"scsihw"`
}

func applyProfileVMConfig(profile models.Profile, cfg proxmox.VMConfig) (proxmox.VMConfig, error) {
	raw := strings.TrimSpace(profile.RawYAML)
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
	if spec.Storage.RootSizeGB > 0 {
		cfg.RootDiskGB = spec.Storage.RootSizeGB
	}
	if strings.TrimSpace(spec.Storage.SCSIHW) != "" {
		cfg.SCSIHW = strings.TrimSpace(spec.Storage.SCSIHW)
	}
	if strings.TrimSpace(cfg.SCSIHW) == "" {
		// Many cloud images do not include legacy LSI SCSI drivers in the initramfs.
		// Default to virtio-scsi so cloned sandboxes reliably boot.
		cfg.SCSIHW = "virtio-scsi-pci"
	}
	if spec.Network.Firewall != nil {
		cfg.Firewall = spec.Network.Firewall
	}
	group, err := resolveFirewallGroup(spec.Network)
	if err != nil {
		return cfg, err
	}
	if group != "" {
		if spec.Network.Firewall != nil && !*spec.Network.Firewall {
			if spec.Network.Mode != nil {
				mode, _ := normalizeNetworkMode(*spec.Network.Mode)
				if mode != "" {
					return cfg, fmt.Errorf("network.mode %q requires network.firewall=true", mode)
				}
			}
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
