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

// canRetryAsFull reports whether a failed clone should be retried as a full
// clone. The original request must have been a linked clone (retrying an
// already-full clone with full=1 is a no-op and masks the real error), and the
// failure must look like a storage/snapshot incompatibility (review M2).
func canRetryAsFull(wasFull bool, err error) bool {
	return !wasFull && shouldRetryFullClone(err)
}
