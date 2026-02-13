package config

import (
	"fmt"
	"os"
	"strings"
)

const (
	permOwnerRead  = 0o400
	permGroupRead  = 0o040
	permGroupWrite = 0o020
	permGroupExec  = 0o010
	permOtherMask  = 0o007
)

// CheckConfigPermissions validates the config file permissions.
//
// It returns a warning when the file is group-readable and an error when the
// file is accessible by others or group-writable/executable.
func CheckConfigPermissions(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("config path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat config %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("config %s must be a regular file", path)
	}
	perms := info.Mode().Perm()
	if perms&permOwnerRead == 0 {
		return "", fmt.Errorf("config %s must be readable by owner (mode %04o)", path, perms)
	}
	if perms&permOtherMask != 0 {
		return "", fmt.Errorf("config %s must not be accessible by others (mode %04o)", path, perms)
	}
	if perms&(permGroupWrite|permGroupExec) != 0 {
		return "", fmt.Errorf("config %s must not be group-writable or executable (mode %04o)", path, perms)
	}
	if perms&permGroupRead != 0 {
		return fmt.Sprintf("config %s is group-readable (mode %04o); consider chmod 0600", path, perms), nil
	}
	return "", nil
}
