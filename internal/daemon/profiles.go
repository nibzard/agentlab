package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
	Type       string `yaml:"type"`  // "vm" (default) or "lxc"
	Image      string `yaml:"image"` // Container image for LXC (e.g., "ubuntu:22.04")
}

// LoadProfiles reads profile YAML files from dir.
// Supports multi-document YAML files (documents separated by ---).
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

		// Use decoder to handle multi-document YAML files and preserve per-document YAML.
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		docIndex := 0
		for {
			var node yaml.Node
			err := decoder.Decode(&node)
			if err != nil {
				if errors.Is(err, io.EOF) {
					break // End of documents
				}
				return nil, fmt.Errorf("parse profile %s (document %d): %w", path, docIndex, err)
			}
			if node.Kind == 0 || (node.Kind == yaml.DocumentNode && len(node.Content) == 0) {
				return nil, fmt.Errorf("profile %s (document %d) is empty", path, docIndex)
			}
			var spec profileSpec
			if err := node.Decode(&spec); err != nil {
				return nil, fmt.Errorf("parse profile %s (document %d): %w", path, docIndex, err)
			}
			if spec.Name == "" {
				return nil, fmt.Errorf("profile %s (document %d) missing name", path, docIndex)
			}
			// Validate profile type
			sandboxType := models.SandboxTypeVM
			if spec.Type != "" {
				switch models.SandboxType(spec.Type) {
				case models.SandboxTypeVM:
					sandboxType = models.SandboxTypeVM
				case models.SandboxTypeLXC:
					sandboxType = models.SandboxTypeLXC
				default:
					return nil, fmt.Errorf("profile %s (document %d) invalid type %q (must be 'vm' or 'lxc')", path, docIndex, spec.Type)
				}
			}
			// Validate required fields based on type
			if sandboxType == models.SandboxTypeLXC {
				if spec.Image == "" {
					return nil, fmt.Errorf("profile %s (document %d) of type 'lxc' requires 'image' field", path, docIndex)
				}
			} else {
				if spec.TemplateVM <= 0 {
					return nil, fmt.Errorf("profile %s (document %d) missing template_vmid", path, docIndex)
				}
			}
			if _, exists := profiles[spec.Name]; exists {
				return nil, fmt.Errorf("duplicate profile name %q in %s", spec.Name, path)
			}
			modTime := time.Now().UTC()
			if info, err := os.Stat(path); err == nil {
				modTime = info.ModTime().UTC()
			}
			rawYAML, err := renderProfileYAML(&node)
			if err != nil {
				return nil, fmt.Errorf("render profile %s (document %d): %w", path, docIndex, err)
			}
			profiles[spec.Name] = models.Profile{
				Name:       spec.Name,
				TemplateVM: spec.TemplateVM,
				Type:       sandboxType,
				Image:      spec.Image,
				UpdatedAt:  modTime,
				RawYAML:    rawYAML,
			}
			docIndex++
		}
	}
	return profiles, nil
}

func renderProfileYAML(node *yaml.Node) (string, error) {
	if node == nil {
		return "", nil
	}
	target := node
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		target = node.Content[0]
	}
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(target); err != nil {
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func isYAML(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")
}
