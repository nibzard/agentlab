package daemon

import (
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
	Bridge string `yaml:"bridge"`
	Model  string `yaml:"model"`
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
	if raw == "" {
		if strings.TrimSpace(cfg.SCSIHW) == "" {
			// Many cloud images do not include legacy LSI SCSI drivers in the initramfs.
			// Default to virtio-scsi so cloned sandboxes reliably boot.
			cfg.SCSIHW = "virtio-scsi-pci"
		}
		return cfg, nil
	}
	var spec profileProvisionSpec
	if err := yaml.Unmarshal([]byte(raw), &spec); err != nil {
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
	return cfg, nil
}
