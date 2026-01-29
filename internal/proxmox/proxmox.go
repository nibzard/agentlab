package proxmox

import "context"

type VMID int

type Status string

const (
	StatusUnknown Status = "unknown"
	StatusRunning Status = "running"
	StatusStopped Status = "stopped"
)

type VMConfig struct {
	Name       string
	Cores      int
	MemoryMB   int
	Bridge     string
	NetModel   string
	CloudInit  string
	CPUPinning string
}

type Backend interface {
	Clone(ctx context.Context, template VMID, target VMID, name string) error
	Configure(ctx context.Context, vmid VMID, cfg VMConfig) error
	Start(ctx context.Context, vmid VMID) error
	Stop(ctx context.Context, vmid VMID) error
	Destroy(ctx context.Context, vmid VMID) error
	Status(ctx context.Context, vmid VMID) (Status, error)
	GuestIP(ctx context.Context, vmid VMID) (string, error)
}
