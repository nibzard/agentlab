//go:build !linux

package templatebuild

import (
	"context"
	"fmt"
	"os/exec"
)

func isolate(cmd *exec.Cmd) {}
func lockCache(ctx context.Context, path string) (func(), error) {
	return nil, fmt.Errorf("template_build_unsupported: Linux Proxmox host required")
}
