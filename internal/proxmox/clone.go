package proxmox

import "strings"

func shouldRetryFullClone(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "linked clone") {
		return true
	}
	// Common Proxmox failure mode when the target storage doesn't support snapshots.
	if strings.Contains(msg, "does not support snapshots") {
		return true
	}
	if strings.Contains(msg, "snapshot") && strings.Contains(msg, "clone") {
		return true
	}
	return false
}
