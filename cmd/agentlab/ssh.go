package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"
)

const (
	defaultSSHUser       = "agent"
	defaultSSHPort       = 22
	routeCheckTimeout    = 500 * time.Millisecond
	defaultAgentSubnet   = "10.77.0.0/16"
	tailscaleInterfaceID = "tailscale"
)

type sshOutput struct {
	VMID     int      `json:"vmid"`
	IP       string   `json:"ip"`
	User     string   `json:"user"`
	Port     int      `json:"port"`
	Identity string   `json:"identity,omitempty"`
	Args     []string `json:"args"`
	Command  string   `json:"command"`
	Warning  string   `json:"warning,omitempty"`
}

type routeInfo struct {
	Device string
	Via    string
}

func runSSHCommand(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("ssh")
	opts := base
	opts.bind(fs)
	user := defaultSSHUser
	port := defaultSSHPort
	identity := ""
	execFlag := false
	help := false
	fs.StringVar(&user, "user", defaultSSHUser, "ssh username")
	fs.StringVar(&user, "u", defaultSSHUser, "ssh username")
	fs.IntVar(&port, "port", defaultSSHPort, "ssh port")
	fs.IntVar(&port, "p", defaultSSHPort, "ssh port")
	fs.StringVar(&identity, "identity", "", "ssh identity file")
	fs.StringVar(&identity, "i", "", "ssh identity file")
	fs.BoolVar(&execFlag, "exec", false, "exec ssh instead of printing the command")
	fs.BoolVar(&execFlag, "e", false, "exec ssh instead of printing the command")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printSSHUsage, &help); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		printSSHUsage()
		return fmt.Errorf("vmid is required")
	}
	if fs.NArg() > 1 {
		printSSHUsage()
		return fmt.Errorf("unexpected extra arguments")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}
	user = strings.TrimSpace(user)
	if user == "" {
		return fmt.Errorf("user is required")
	}
	if port <= 0 || port > 65535 {
		return fmt.Errorf("invalid port %d", port)
	}
	if opts.jsonOutput && execFlag {
		return fmt.Errorf("cannot use --json with --exec")
	}
	if execFlag && !isInteractive() {
		return fmt.Errorf("--exec requires an interactive terminal")
	}

	client := newAPIClient(opts.socketPath, opts.timeout)
	payload, err := client.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/sandboxes/%d", vmid), nil)
	if err != nil {
		return err
	}
	var resp sandboxResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	ip := strings.TrimSpace(resp.IP)
	if ip == "" {
		return fmt.Errorf("sandbox %d has no IP yet", vmid)
	}
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return fmt.Errorf("sandbox %d returned invalid IP %q", vmid, ip)
	}

	identity = strings.TrimSpace(identity)
	target := fmt.Sprintf("%s@%s", user, ip)
	sshArgs := buildSSHArgs(target, port, identity)
	fullArgs := append([]string{"ssh"}, sshArgs...)
	warning := tailnetWarning(ctx, parsedIP)

	if opts.jsonOutput {
		out := sshOutput{
			VMID:     vmid,
			IP:       ip,
			User:     user,
			Port:     port,
			Identity: identity,
			Args:     fullArgs,
			Command:  formatShellCommand(fullArgs),
			Warning:  warning,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(out)
	}

	if warning != "" {
		fmt.Fprintln(os.Stderr, warning)
	}

	if execFlag {
		return execSSH(sshArgs)
	}

	fmt.Fprintln(os.Stdout, formatShellCommand(fullArgs))
	return nil
}

func buildSSHArgs(target string, port int, identity string) []string {
	args := []string{}
	if identity != "" {
		args = append(args, "-i", identity)
	}
	if port != defaultSSHPort {
		args = append(args, "-p", strconv.Itoa(port))
	}
	args = append(args, target)
	return args
}

func execSSH(args []string) error {
	path, err := exec.LookPath("ssh")
	if err != nil {
		return err
	}
	argv := append([]string{"ssh"}, args...)
	return syscall.Exec(path, argv, os.Environ())
}

func tailnetWarning(ctx context.Context, ip net.IP) string {
	if ip == nil || !ip.IsPrivate() {
		return ""
	}
	if runtime.GOOS != "linux" {
		return fmt.Sprintf("Note: unable to verify tailnet route on %s; ensure the agent subnet route (%s by default) is enabled.", runtime.GOOS, defaultAgentSubnet)
	}
	path, err := exec.LookPath("ip")
	if err != nil {
		return fmt.Sprintf("Note: unable to verify tailnet route (missing ip command); ensure the agent subnet route (%s by default) is enabled.", defaultAgentSubnet)
	}
	args := []string{"-4", "route", "get", ip.String()}
	if ip.To4() == nil {
		args[0] = "-6"
	}
	ctx, cancel := context.WithTimeout(ctx, routeCheckTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, args...).Output()
	if err != nil {
		return fmt.Sprintf("Warning: no route to %s detected. If you're on a tailnet device, enable the agent subnet route (%s by default) in Tailscale.", ip.String(), defaultAgentSubnet)
	}
	info := parseIPRouteGet(string(out))
	if info.Device == "" {
		return fmt.Sprintf("Note: unable to determine route to %s; ensure the agent subnet route (%s by default) is enabled.", ip.String(), defaultAgentSubnet)
	}
	if strings.HasPrefix(info.Device, tailscaleInterfaceID) {
		return ""
	}
	if info.Via != "" {
		return fmt.Sprintf("Warning: route to %s goes via %s on %s, not Tailscale. If you're on a tailnet device, enable the agent subnet route (%s by default).", ip.String(), info.Via, info.Device, defaultAgentSubnet)
	}
	return ""
}

func parseIPRouteGet(output string) routeInfo {
	fields := strings.Fields(output)
	info := routeInfo{}
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "dev":
			if i+1 < len(fields) {
				info.Device = fields[i+1]
			}
		case "via":
			if i+1 < len(fields) {
				info.Via = fields[i+1]
			}
		}
	}
	return info
}

func formatShellCommand(args []string) string {
	if len(args) == 0 {
		return ""
	}
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, isShellSpecial) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func isShellSpecial(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\v', '\f', '\\', '\'', '"', '$', '`':
		return true
	default:
		return false
	}
}

func isInteractive() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
}
