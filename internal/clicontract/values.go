package clicontract

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

func ParseTTLMinutes(value string) (*int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if minutes, err := strconv.Atoi(value); err == nil {
		if minutes <= 0 {
			return nil, fmt.Errorf("ttl must be positive")
		}
		return &minutes, nil
	}
	dur, err := time.ParseDuration(value)
	if err != nil {
		return nil, fmt.Errorf("invalid ttl %q", value)
	}
	if dur <= 0 {
		return nil, fmt.Errorf("ttl must be positive")
	}
	minutes := int(math.Ceil(dur.Minutes()))
	if minutes <= 0 {
		minutes = 1
	}
	return &minutes, nil
}

func ParseSizeGB(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("size is required")
	}
	lower := strings.ToLower(value)
	switch {
	case strings.HasSuffix(lower, "gb"):
		lower = strings.TrimSuffix(lower, "gb")
	case strings.HasSuffix(lower, "g"):
		lower = strings.TrimSuffix(lower, "g")
	}
	lower = strings.TrimSpace(lower)
	size, err := strconv.Atoi(lower)
	if err != nil || size <= 0 {
		return 0, fmt.Errorf("invalid size %q", value)
	}
	return size, nil
}

func ParseMemoryMB(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("memory is required")
	}
	lower := strings.ToLower(value)
	multiplier := 1
	switch {
	case strings.HasSuffix(lower, "gib"):
		lower = strings.TrimSuffix(lower, "gib")
		multiplier = 1024
	case strings.HasSuffix(lower, "gb"):
		lower = strings.TrimSuffix(lower, "gb")
		multiplier = 1024
	case strings.HasSuffix(lower, "g"):
		lower = strings.TrimSuffix(lower, "g")
		multiplier = 1024
	case strings.HasSuffix(lower, "mib"):
		lower = strings.TrimSuffix(lower, "mib")
	case strings.HasSuffix(lower, "mb"):
		lower = strings.TrimSuffix(lower, "mb")
	case strings.HasSuffix(lower, "m"):
		lower = strings.TrimSuffix(lower, "m")
	}
	lower = strings.TrimSpace(lower)
	size, err := strconv.Atoi(lower)
	if err != nil || size <= 0 {
		return 0, fmt.Errorf("invalid memory %q", value)
	}
	if size > int(^uint(0)>>1)/multiplier {
		return 0, fmt.Errorf("memory is too large %q", value)
	}
	return size * multiplier, nil
}

func ParseWorkspaceWaitSeconds(value string) (*int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return nil, fmt.Errorf("workspace-wait must be positive")
		}
		return &seconds, nil
	}
	dur, err := time.ParseDuration(value)
	if err != nil {
		return nil, fmt.Errorf("invalid workspace-wait %q", value)
	}
	if dur <= 0 {
		return nil, fmt.Errorf("workspace-wait must be positive")
	}
	seconds := int(math.Ceil(dur.Seconds()))
	if seconds <= 0 {
		seconds = 1
	}
	return &seconds, nil
}

func ParseVMID(value string) (int, error) {
	vmid, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || vmid <= 0 {
		return 0, fmt.Errorf("invalid vmid %q", value)
	}
	return vmid, nil
}

// ParseTokenOptions validates local token claims without loading private keys.
// Zero TTL retains the auth package's one-hour default.
func ParseTokenOptions(ttl, commands, scope string) (time.Duration, []string, []string, error) {
	duration, err := time.ParseDuration(ttl)
	if err != nil || duration < 0 {
		return 0, nil, nil, fmt.Errorf("invalid --ttl %q: use a non-negative duration", ttl)
	}
	split := func(value string) []string {
		var result []string
		for _, item := range strings.Split(value, ",") {
			if item = strings.TrimSpace(item); item != "" {
				result = append(result, item)
			}
		}
		return result
	}
	commandList := split(commands)
	if len(commandList) == 0 {
		return 0, nil, nil, fmt.Errorf("--cmds must contain at least one command")
	}
	scopeList := split(scope)
	if strings.TrimSpace(scope) != "" && len(scopeList) == 0 {
		return 0, nil, nil, fmt.Errorf("--scope must contain at least one sandbox scope")
	}
	for index, item := range scopeList {
		vmid, ok := strings.CutPrefix(item, "sandbox:")
		if !ok {
			return 0, nil, nil, fmt.Errorf("invalid scope %q: use sandbox:<vmid>", item)
		}
		parsed, err := ParseVMID(vmid)
		if err != nil {
			return 0, nil, nil, fmt.Errorf("invalid scope %q: use sandbox:<positive vmid>", item)
		}
		scopeList[index] = "sandbox:" + strconv.Itoa(parsed)
	}
	return duration, commandList, scopeList, nil
}

// ValidateTimeout gives global request and observation limits one contract.
func ValidateTimeout(timeout time.Duration) error {
	if timeout <= 0 {
		return fmt.Errorf("--timeout must be a positive duration")
	}
	return nil
}

func ValidatePreference(key, value string) error {
	switch key {
	case "output-format":
		if value != "json" && value != "text" {
			return fmt.Errorf("output-format must be text or json")
		}
	case "default-timeout":
		d, err := time.ParseDuration(value)
		if err != nil || d <= 0 {
			return fmt.Errorf("default-timeout must be a positive duration")
		}
	case "default-profile", "default-image", "default-socket":
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s cannot be empty", key)
		}
	case "default-backend":
		return fmt.Errorf("default-backend is not supported: configure the backend on the daemon")
	default:
		return fmt.Errorf("unknown preference %q", key)
	}
	return nil
}
