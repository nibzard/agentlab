package daemon

import (
	"strings"

	"gopkg.in/yaml.v3"
)

type profileInnerSandboxSpec struct {
	Behavior profileInnerSandboxBehavior `yaml:"behavior"`
}

type profileInnerSandboxBehavior struct {
	InnerSandbox     string   `yaml:"inner_sandbox"`
	InnerSandboxArgs []string `yaml:"inner_sandbox_args"`
}

type innerSandboxConfig struct {
	Name string
	Args []string
}

func parseProfileInnerSandbox(raw string) (innerSandboxConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return innerSandboxConfig{}, nil
	}
	var spec profileInnerSandboxSpec
	if err := yaml.Unmarshal([]byte(raw), &spec); err != nil {
		return innerSandboxConfig{}, err
	}

	value := strings.TrimSpace(spec.Behavior.InnerSandbox)
	if value == "" {
		return innerSandboxConfig{}, nil
	}
	value = strings.ToLower(value)
	switch value {
	case "none", "off", "false", "0", "disabled":
		return innerSandboxConfig{}, nil
	case "true", "yes", "1":
		value = "bubblewrap"
	case "bwrap":
		value = "bubblewrap"
	}

	args := make([]string, 0, len(spec.Behavior.InnerSandboxArgs))
	for _, arg := range spec.Behavior.InnerSandboxArgs {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" {
			continue
		}
		args = append(args, trimmed)
	}

	return innerSandboxConfig{Name: value, Args: args}, nil
}
