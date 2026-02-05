// ABOUTME: This file provides error definitions and the CommandRunner interface used by
// the ShellBackend implementation for executing Proxmox CLI commands.
package proxmox

import (
	"context"
	"errors"
)

var (
	// ErrVMNotFound is returned when a VM does not exist in Proxmox.
	// ABOUTME: This error is returned by Stop, Destroy, and DetachVolume operations
	// when the target VMID cannot be found.
	ErrVMNotFound = errors.New("vm not found")

	// ErrGuestIPNotFound is returned when the guest IP address cannot be determined.
	// ABOUTME: This occurs when both guest agent queries and DHCP lease lookups fail
	// to find an IP address for the VM.
	ErrGuestIPNotFound = errors.New("guest IP not found")
)

// CommandRunner defines the interface for executing shell commands.
// ABOUTME: This abstraction allows the ShellBackend to use different execution strategies
// (direct exec vs bash wrapper) and enables testing with mock implementations.
type CommandRunner interface {
	// Run executes a command with the given name and arguments.
	// ABOUTME: Returns the combined stdout output or an error if the command fails.
	Run(ctx context.Context, name string, args ...string) (string, error)
}
