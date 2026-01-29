package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"gopkg.in/yaml.v3"
)

type profileSpec struct {
	Name       string `yaml:"name"`
	TemplateVM int    `yaml:"template_vmid"`
}

// LoadProfiles reads profile YAML files from dir.
func LoadProfiles(dir string) (map[string]models.Profile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read profiles dir %s: %w", dir, err)
	}
	profiles := make(map[string]models.Profile)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !isYAML(name) {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read profile %s: %w", path, err)
		}
		var spec profileSpec
		if err := yaml.Unmarshal(data, &spec); err != nil {
			return nil, fmt.Errorf("parse profile %s: %w", path, err)
		}
		if spec.Name == "" {
			return nil, fmt.Errorf("profile %s missing name", path)
		}
		if spec.TemplateVM <= 0 {
			return nil, fmt.Errorf("profile %s missing template_vmid", path)
		}
		if _, exists := profiles[spec.Name]; exists {
			return nil, fmt.Errorf("duplicate profile name %q", spec.Name)
		}
		modTime := time.Now().UTC()
		if info, err := os.Stat(path); err == nil {
			modTime = info.ModTime().UTC()
		}
		profiles[spec.Name] = models.Profile{
			Name:       spec.Name,
			TemplateVM: spec.TemplateVM,
			UpdatedAt:  modTime,
			RawYAML:    string(data),
		}
	}
	return profiles, nil
}

func isYAML(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")
}
