package proxmox

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// GuestReadiness proves that a running guest answers its agent and exposes SSH.
// GuestIP alone may come from a stale DHCP lease and is not a readiness signal.
type GuestReadiness interface {
	ProbeGuest(context.Context, VMID) (string, error)
}

func (b *ShellBackend) ProbeGuest(ctx context.Context, vmid VMID) (string, error) {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return "", err
	}
	ip, err := b.guestAgentIP(ctx, node, vmid)
	if err != nil {
		return "", fmt.Errorf("guest_agent_unavailable: %w", err)
	}
	return ip, ProbeSSH(ctx, ip)
}
func (b *APIBackend) ProbeGuest(ctx context.Context, vmid VMID) (string, error) {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return "", err
	}
	ip, err := b.guestAgentIP(ctx, node, vmid)
	if err != nil {
		return "", fmt.Errorf("guest_agent_unavailable: %w", err)
	}
	return ip, ProbeSSH(ctx, ip)
}

// ProbeSSH requires an SSH protocol banner, not merely an open TCP port.
func ProbeSSH(ctx context.Context, ip string) error {
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("guest_network_unavailable: no valid guest IP")
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, "22"))
	if err != nil {
		return fmt.Errorf("guest_ssh_unavailable: %w", err)
	}
	defer c.Close()
	deadline := time.Now().Add(2 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	_ = c.SetReadDeadline(deadline)
	line, err := bufio.NewReaderSize(c, 512).ReadSlice('\n')
	if err != nil || !strings.HasPrefix(string(line), "SSH-2.0-") {
		return fmt.Errorf("guest_ssh_unavailable: valid SSH banner not received")
	}
	return nil
}
