package proxmox

import "strings"

func isUnsupportedFWGroupError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "fwgroup") {
		return true
	}
	if strings.Contains(msg, "property is not defined in schema") && strings.Contains(msg, "additional properties") {
		return true
	}
	if strings.Contains(msg, "schema does not allow additional properties") {
		return true
	}
	if strings.Contains(msg, "parameter verification failed") && strings.Contains(msg, "additional properties") {
		return true
	}
	if strings.Contains(msg, "invalid format") && strings.Contains(msg, "fwgroup") {
		return true
	}
	return false
}
