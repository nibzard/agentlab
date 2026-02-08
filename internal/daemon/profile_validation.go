package daemon

import (
	"fmt"
	"sort"
	"strings"

	"github.com/agentlab/agentlab/internal/models"
	"gopkg.in/yaml.v3"
)

func validateProfileForProvisioning(profile models.Profile) error {
	paths, err := profileHostMountPaths(profile.RawYAML)
	if err != nil {
		return fmt.Errorf("parse profile %q: %w", profile.Name, err)
	}
	if len(paths) > 0 {
		return fmt.Errorf("profile %q requests host mounts at %s; host bind mounts are not allowed (use workspace disks instead)", profile.Name, strings.Join(paths, ", "))
	}
	if err := validateProfileInnerSandbox(profile); err != nil {
		return err
	}
	if err := validateProfileFirewallGroup(profile); err != nil {
		return err
	}
	return nil
}

func validateProfileInnerSandbox(profile models.Profile) error {
	cfg, err := parseProfileInnerSandbox(profile.RawYAML)
	if err != nil {
		return fmt.Errorf("parse profile %q: %w", profile.Name, err)
	}
	if cfg.Name == "" {
		return nil
	}
	if cfg.Name != "bubblewrap" {
		return fmt.Errorf("profile %q has unsupported behavior.inner_sandbox %q", profile.Name, cfg.Name)
	}
	return nil
}

func validateProfileFirewallGroup(profile models.Profile) error {
	spec, err := parseProfileProvisionSpec(profile.RawYAML)
	if err != nil {
		return fmt.Errorf("parse profile %q: %w", profile.Name, err)
	}
	if spec.Network.FirewallGroup == nil {
		return nil
	}
	group, err := normalizeFirewallGroup(*spec.Network.FirewallGroup)
	if err != nil {
		return fmt.Errorf("profile %q: %w", profile.Name, err)
	}
	if spec.Network.Firewall != nil && !*spec.Network.Firewall {
		return fmt.Errorf("profile %q sets network.firewall=false with firewall_group %q", profile.Name, group)
	}
	return nil
}

func profileHostMountPaths(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &node); err != nil {
		return nil, err
	}
	matches := make(map[string]struct{})
	walkProfileNode(&node, nil, matches)
	if len(matches) == 0 {
		return nil, nil
	}
	paths := make([]string, 0, len(matches))
	for path := range matches {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func walkProfileNode(node *yaml.Node, path []string, matches map[string]struct{}) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			walkProfileNode(child, path, matches)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valueNode := node.Content[i+1]
			key := strings.TrimSpace(keyNode.Value)
			if isHostMountKey(key) {
				matches[joinProfilePath(path, key)] = struct{}{}
			}
			nextPath := append(append([]string{}, path...), key)
			walkProfileNode(valueNode, nextPath, matches)
		}
	case yaml.SequenceNode:
		for idx, child := range node.Content {
			nextPath := append(append([]string{}, path...), fmt.Sprintf("[%d]", idx))
			walkProfileNode(child, nextPath, matches)
		}
	}
}

func isHostMountKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "host_mount", "host_mounts", "host_path", "host_paths", "bind_mount", "bind_mounts", "binds", "mounts", "mount_points", "mountpoints", "virtiofs", "virtio_fs":
		return true
	}
	if strings.Contains(normalized, "host") && (strings.Contains(normalized, "mount") || strings.Contains(normalized, "path") || strings.Contains(normalized, "bind")) {
		return true
	}
	if strings.Contains(normalized, "bind") && strings.Contains(normalized, "mount") {
		return true
	}
	return false
}

func joinProfilePath(path []string, key string) string {
	segments := append(append([]string{}, path...), key)
	var b strings.Builder
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		if strings.HasPrefix(seg, "[") {
			b.WriteString(seg)
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(seg)
	}
	return b.String()
}
