//go:build linux

package daemon

import (
	"fmt"
	"syscall"
)

func templateHeadroom(path string, need int64) error {
	var s syscall.Statfs_t
	if e := syscall.Statfs(path, &s); e != nil {
		return e
	}
	if int64(s.Bavail)*int64(s.Bsize) < need {
		return fmt.Errorf("insufficient disk space")
	}
	return nil
}
