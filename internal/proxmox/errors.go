package proxmox

import (
	"context"
	"errors"
)

var (
	ErrVMNotFound      = errors.New("vm not found")
	ErrGuestIPNotFound = errors.New("guest IP not found")
)

type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}
