package proxmox

import "strings"

func isMissingVolumeError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	indicators := []string{
		"volume does not exist",
		"no such volume",
		"volume not found",
		"cannot find volume",
		"can't find volume",
	}
	for _, indicator := range indicators {
		if strings.Contains(msg, indicator) {
			return true
		}
	}
	return strings.Contains(msg, "not found") && strings.Contains(msg, "volume")
}

func volumeStorage(volumeID string) string {
	parts := strings.SplitN(strings.TrimSpace(volumeID), ":", 2)
	if len(parts) != 2 {
		return ""
	}
	return parts[0]
}
