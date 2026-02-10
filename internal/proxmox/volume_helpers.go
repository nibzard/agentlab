package proxmox

import (
	"fmt"
	"strings"
)

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

func isZFSStorageType(storageType string) bool {
	switch strings.ToLower(strings.TrimSpace(storageType)) {
	case "zfspool", "zfs":
		return true
	default:
		return false
	}
}

func unsupportedStorageErr(op, storage, storageType string) error {
	storage = strings.TrimSpace(storage)
	storageType = strings.TrimSpace(storageType)
	if storageType == "" {
		storageType = "unknown"
	}
	return fmt.Errorf("%w: %s requires zfs storage (storage=%s type=%s)", ErrStorageUnsupported, op, storage, storageType)
}
