package proxmox

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

var rootDiskCandidates = []string{"scsi0", "virtio0", "sata0", "ide0"}

func detectRootDisk(config map[string]string) string {
	if config == nil {
		return ""
	}
	if v := strings.TrimSpace(config["bootdisk"]); v != "" {
		return v
	}
	if v := strings.TrimSpace(config["boot"]); v != "" {
		if disk := detectBootOrderDisk(v); disk != "" {
			return disk
		}
	}
	for _, candidate := range rootDiskCandidates {
		if _, ok := config[candidate]; ok {
			return candidate
		}
	}
	return ""
}

func detectBootOrderDisk(boot string) string {
	boot = strings.TrimSpace(boot)
	if boot == "" {
		return ""
	}
	idx := strings.Index(boot, "order=")
	if idx == -1 {
		return ""
	}
	order := boot[idx+len("order="):]
	// boot can include additional comma-separated options.
	if comma := strings.Index(order, ","); comma != -1 {
		order = order[:comma]
	}
	order = strings.TrimSpace(order)
	if order == "" {
		return ""
	}
	parts := strings.FieldsFunc(order, func(r rune) bool {
		return r == ';' || r == ',' || r == ' '
	})
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		for _, candidate := range rootDiskCandidates {
			if part == candidate {
				return part
			}
		}
	}
	return ""
}

func extractDiskSizeToken(diskConfig string) string {
	diskConfig = strings.TrimSpace(diskConfig)
	if diskConfig == "" {
		return ""
	}
	idx := strings.Index(diskConfig, "size=")
	if idx == -1 {
		return ""
	}
	rest := diskConfig[idx+len("size="):]
	if end := strings.IndexAny(rest, ", \t\r\n"); end != -1 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

func parseSizeGB(size string) (float64, error) {
	size = strings.TrimSpace(size)
	if size == "" {
		return 0, fmt.Errorf("empty size")
	}
	upper := strings.ToUpper(size)

	// Split numeric part from unit part (allow decimal sizes like 2.8G).
	i := 0
	for i < len(upper) {
		ch := upper[i]
		if (ch >= '0' && ch <= '9') || ch == '.' {
			i++
			continue
		}
		break
	}
	if i == 0 {
		return 0, fmt.Errorf("invalid size %q", size)
	}
	numStr := upper[:i]
	unit := strings.TrimSpace(upper[i:])
	if strings.HasSuffix(unit, "B") {
		unit = strings.TrimSuffix(unit, "B")
	}
	if unit == "" {
		// Proxmox should always include a unit, but default to bytes if missing.
		unit = ""
	}

	value, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", numStr, err)
	}

	switch unit {
	case "":
		// bytes
		return value / (1024.0 * 1024.0 * 1024.0), nil
	case "K":
		return value / (1024.0 * 1024.0), nil
	case "M":
		return value / 1024.0, nil
	case "G":
		return value, nil
	case "T":
		return value * 1024.0, nil
	default:
		return 0, fmt.Errorf("unknown unit %q", unit)
	}
}

func resizeDeltaGB(currentGB float64, targetGB int) int {
	if targetGB <= 0 {
		return 0
	}
	if currentGB < 0 {
		currentGB = 0
	}
	// Ensure we grow to at least targetGB; Proxmox disks can be fractional (e.g. 2.8G).
	currentFloor := int(math.Floor(currentGB))
	delta := targetGB - currentFloor
	if delta < 0 {
		return 0
	}
	return delta
}

func parseQMConfigMap(output string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" {
			continue
		}
		out[key] = value
	}
	return out
}
