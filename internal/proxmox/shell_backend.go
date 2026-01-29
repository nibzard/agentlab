package proxmox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
)

var ErrGuestIPNotFound = errors.New("guest IP not found")

// CommandRunner executes external commands and returns stdout.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// ExecRunner runs commands via os/exec.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return "", fmt.Errorf("command %s %s failed: %w: %s", name, strings.Join(args, " "), err, errMsg)
		}
		return "", fmt.Errorf("command %s %s failed: %w", name, strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}

// ShellBackend implements Backend using qm and pvesh commands.
type ShellBackend struct {
	Node      string
	AgentCIDR string
	QmPath    string
	PveShPath string
	Runner    CommandRunner
}

var _ Backend = (*ShellBackend)(nil)

func (b *ShellBackend) Clone(ctx context.Context, template VMID, target VMID, name string) error {
	args := []string{"clone", strconv.Itoa(int(template)), strconv.Itoa(int(target)), "--full", "0"}
	if name != "" {
		args = append(args, "--name", name)
	}
	_, err := b.runner().Run(ctx, b.qmPath(), args...)
	return err
}

func (b *ShellBackend) Configure(ctx context.Context, vmid VMID, cfg VMConfig) error {
	args := []string{"set", strconv.Itoa(int(vmid))}
	if cfg.Name != "" {
		args = append(args, "--name", cfg.Name)
	}
	if cfg.Cores > 0 {
		args = append(args, "--cores", strconv.Itoa(cfg.Cores))
	}
	if cfg.MemoryMB > 0 {
		args = append(args, "--memory", strconv.Itoa(cfg.MemoryMB))
	}
	if cfg.CPUPinning != "" {
		args = append(args, "--cpulist", cfg.CPUPinning)
	}
	if cfg.Bridge != "" || cfg.NetModel != "" {
		net0 := buildNet0(cfg.NetModel, cfg.Bridge)
		args = append(args, "--net0", net0)
	}
	if cfg.CloudInit != "" {
		args = append(args, "--cicustom", formatCICustom(cfg.CloudInit))
	}
	if len(args) == 2 {
		return nil
	}
	_, err := b.runner().Run(ctx, b.qmPath(), args...)
	return err
}

func (b *ShellBackend) Start(ctx context.Context, vmid VMID) error {
	_, err := b.runner().Run(ctx, b.qmPath(), "start", strconv.Itoa(int(vmid)))
	return err
}

func (b *ShellBackend) Stop(ctx context.Context, vmid VMID) error {
	_, err := b.runner().Run(ctx, b.qmPath(), "stop", strconv.Itoa(int(vmid)))
	return err
}

func (b *ShellBackend) Destroy(ctx context.Context, vmid VMID) error {
	_, err := b.runner().Run(ctx, b.qmPath(), "destroy", strconv.Itoa(int(vmid)), "--purge", "1")
	return err
}

func (b *ShellBackend) Status(ctx context.Context, vmid VMID) (Status, error) {
	out, err := b.runner().Run(ctx, b.qmPath(), "status", strconv.Itoa(int(vmid)))
	if err != nil {
		return StatusUnknown, err
	}
	status, err := parseStatus(out)
	if err != nil {
		return StatusUnknown, err
	}
	return status, nil
}

func (b *ShellBackend) GuestIP(ctx context.Context, vmid VMID) (string, error) {
	node, err := b.ensureNode(ctx)
	if err != nil {
		return "", err
	}
	path := fmt.Sprintf("/nodes/%s/qemu/%d/agent/network-get-interfaces", node, vmid)
	out, err := b.runner().Run(ctx, b.pveshPath(), "get", path, "--output-format", "json")
	if err != nil {
		return "", err
	}
	ips, err := parseAgentIPs(out)
	if err != nil {
		return "", err
	}
	if len(ips) == 0 {
		return "", ErrGuestIPNotFound
	}
	if b.AgentCIDR != "" {
		ip, err := selectIPByCIDR(ips, b.AgentCIDR)
		if err != nil {
			return "", err
		}
		if ip != "" {
			return ip, nil
		}
	}
	return ips[0].String(), nil
}

func (b *ShellBackend) runner() CommandRunner {
	if b.Runner != nil {
		return b.Runner
	}
	return ExecRunner{}
}

func (b *ShellBackend) qmPath() string {
	if b.QmPath != "" {
		return b.QmPath
	}
	return "qm"
}

func (b *ShellBackend) pveshPath() string {
	if b.PveShPath != "" {
		return b.PveShPath
	}
	return "pvesh"
}

func (b *ShellBackend) ensureNode(ctx context.Context) (string, error) {
	if b.Node != "" {
		return b.Node, nil
	}
	out, err := b.runner().Run(ctx, b.pveshPath(), "get", "/nodes", "--output-format", "json")
	if err != nil {
		return "", err
	}
	node, err := parseNode(out)
	if err != nil {
		return "", err
	}
	b.Node = node
	return node, nil
}

func buildNet0(model, bridge string) string {
	if model == "" {
		model = "virtio"
	}
	parts := []string{model}
	if bridge != "" && !strings.Contains(model, "bridge=") {
		parts = append(parts, "bridge="+bridge)
	}
	return strings.Join(parts, ",")
}

func formatCICustom(value string) string {
	if strings.Contains(value, "=") {
		return value
	}
	return "user=" + value
}

func parseStatus(output string) (Status, error) {
	out := strings.TrimSpace(output)
	if out == "" {
		return StatusUnknown, errors.New("empty status output")
	}
	if strings.Contains(out, "status:") {
		parts := strings.SplitN(out, "status:", 2)
		out = strings.TrimSpace(parts[1])
		if idx := strings.Index(out, "\n"); idx != -1 {
			out = strings.TrimSpace(out[:idx])
		}
	} else {
		fields := strings.Fields(out)
		if len(fields) > 0 {
			out = fields[0]
		}
	}
	switch out {
	case "running":
		return StatusRunning, nil
	case "stopped":
		return StatusStopped, nil
	default:
		return StatusUnknown, fmt.Errorf("unknown status %q", out)
	}
}

type agentInterface struct {
	Name        string      `json:"name"`
	IPAddresses []agentAddr `json:"ip-addresses"`
}

type agentAddr struct {
	IPAddress     string `json:"ip-address"`
	IPAddressType string `json:"ip-address-type"`
}

type agentNetResp struct {
	Result []agentInterface `json:"result"`
	Data   []agentInterface `json:"data"`
}

func parseAgentIPs(output string) ([]net.IP, error) {
	payload := strings.TrimSpace(output)
	if payload == "" {
		return nil, errors.New("empty agent response")
	}
	var resp agentNetResp
	if err := json.Unmarshal([]byte(payload), &resp); err == nil {
		ifaces := resp.Result
		if len(ifaces) == 0 {
			ifaces = resp.Data
		}
		if len(ifaces) > 0 {
			return collectIPv4(ifaces), nil
		}
	}
	var direct []agentInterface
	if err := json.Unmarshal([]byte(payload), &direct); err == nil {
		return collectIPv4(direct), nil
	}
	return nil, errors.New("unrecognized agent response")
}

func collectIPv4(ifaces []agentInterface) []net.IP {
	var ips []net.IP
	for _, iface := range ifaces {
		for _, addr := range iface.IPAddresses {
			if addr.IPAddressType != "ipv4" {
				continue
			}
			ip := net.ParseIP(addr.IPAddress)
			if ip == nil {
				continue
			}
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			ip = ip.To4()
			if ip == nil {
				continue
			}
			if ip.Equal(net.IPv4zero) {
				continue
			}
			ips = append(ips, ip)
		}
	}
	return ips
}

func selectIPByCIDR(ips []net.IP, cidr string) (string, error) {
	_, netblock, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("invalid agent CIDR %q: %w", cidr, err)
	}
	for _, ip := range ips {
		if netblock.Contains(ip) {
			return ip.String(), nil
		}
	}
	return "", nil
}

type nodeEntry struct {
	Node string `json:"node"`
	Name string `json:"name"`
}

type nodeResp struct {
	Data []nodeEntry `json:"data"`
}

func parseNode(output string) (string, error) {
	payload := strings.TrimSpace(output)
	if payload == "" {
		return "", errors.New("empty node list")
	}
	var resp nodeResp
	if err := json.Unmarshal([]byte(payload), &resp); err == nil && len(resp.Data) > 0 {
		if node := firstNode(resp.Data); node != "" {
			return node, nil
		}
	}
	var nodes []nodeEntry
	if err := json.Unmarshal([]byte(payload), &nodes); err == nil && len(nodes) > 0 {
		if node := firstNode(nodes); node != "" {
			return node, nil
		}
	}
	return "", errors.New("no nodes found")
}

func firstNode(nodes []nodeEntry) string {
	for _, entry := range nodes {
		if entry.Node != "" {
			return entry.Node
		}
		if entry.Name != "" {
			return entry.Name
		}
	}
	return ""
}
