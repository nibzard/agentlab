package proxmox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// commandWithGroup stops synchronous storage children when a request ends.
// Proxmox's asynchronous API tasks have their own server-side lifecycle.
func commandWithGroup(ctx context.Context, name string, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, name, args...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	command.WaitDelay = 2 * time.Second
	return command
}
