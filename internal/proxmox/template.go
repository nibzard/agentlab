package proxmox

import (
	"fmt"
	"strings"
)

func hasCloudInitDrive(config map[string]string) bool {
	for _, v := range config {
		if strings.Contains(strings.ToLower(v), "cloudinit") {
			return true
		}
	}
	return false
}

func agentConfigEnabled(value any) (bool, error) {
	switch v := value.(type) {
	case float64:
		return v != 0, nil
	case int:
		return v != 0, nil
	case string:
		return parseAgentConfigString(v)
	default:
		return false, fmt.Errorf("unknown agent config type %T", value)
	}
}

func parseAgentConfigString(value string) (bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, fmt.Errorf("empty agent config")
	}
	// Common forms: "1", "0", "enabled=1", "enabled=1,fstrim_cloned_disks=0".
	if value == "1" {
		return true, nil
	}
	if value == "0" {
		return false, nil
	}

	parts := strings.Split(value, ",")
	if len(parts) > 0 {
		head := strings.TrimSpace(parts[0])
		if head == "1" {
			return true, nil
		}
		if head == "0" {
			return false, nil
		}
	}

	enabledSeen := false
	enabled := false
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		switch key {
		case "enabled":
			enabledSeen = true
			enabled = val == "1"
		case "disabled":
			if val == "1" {
				return false, nil
			}
		}
	}
	if enabledSeen {
		return enabled, nil
	}
	if strings.Contains(value, "disabled=1") {
		return false, nil
	}

	// If the field is present but doesn't specify enabled/disabled, assume enabled.
	return true, nil
}
