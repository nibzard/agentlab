package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentlab/agentlab/internal/proxmox"
)

func TestLoadProfilesMultiDocumentStoresPerDocumentYAML(t *testing.T) {
	dir := t.TempDir()
	data := strings.TrimSpace(`
---
name: small
template_vmid: 9000
resources:
  cores: 1
  memory_mb: 1024
---
name: large
template_vmid: 9001
resources:
  cores: 4
  memory_mb: 4096
`) + "\n"
	if err := writeFile(filepath.Join(dir, "profiles.yaml"), []byte(data)); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	profiles, err := LoadProfiles(dir)
	if err != nil {
		t.Fatalf("LoadProfiles() error = %v", err)
	}

	if len(profiles) != 2 {
		t.Fatalf("profiles = %d, want %d", len(profiles), 2)
	}

	small, ok := profiles["small"]
	if !ok {
		t.Fatalf("missing profile small")
	}
	if small.TemplateVM != 9000 {
		t.Fatalf("small.TemplateVM = %d, want %d", small.TemplateVM, 9000)
	}
	if !strings.Contains(small.RawYAML, "name: small") || strings.Contains(small.RawYAML, "name: large") {
		t.Fatalf("small.RawYAML should contain only small document, got:\n%s", small.RawYAML)
	}
	cfg, err := applyProfileVMConfig(small, proxmox.VMConfig{})
	if err != nil {
		t.Fatalf("applyProfileVMConfig(small) error = %v", err)
	}
	if cfg.Cores != 1 || cfg.MemoryMB != 1024 {
		t.Fatalf("small cfg = %+v, want cores=1 memory=1024", cfg)
	}

	large, ok := profiles["large"]
	if !ok {
		t.Fatalf("missing profile large")
	}
	if large.TemplateVM != 9001 {
		t.Fatalf("large.TemplateVM = %d, want %d", large.TemplateVM, 9001)
	}
	if !strings.Contains(large.RawYAML, "name: large") || strings.Contains(large.RawYAML, "name: small") {
		t.Fatalf("large.RawYAML should contain only large document, got:\n%s", large.RawYAML)
	}
	cfg, err = applyProfileVMConfig(large, proxmox.VMConfig{})
	if err != nil {
		t.Fatalf("applyProfileVMConfig(large) error = %v", err)
	}
	if cfg.Cores != 4 || cfg.MemoryMB != 4096 {
		t.Fatalf("large cfg = %+v, want cores=4 memory=4096", cfg)
	}
}

func writeFile(path string, data []byte) error {
	// 0600 so tests don't depend on umask.
	return os.WriteFile(path, data, 0o600)
}
