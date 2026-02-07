// ABOUTME: SSH command for connecting to sandbox VMs via agent subnet.
// ABOUTME: Provides connection details with route detection for Tailscale networks.

// Package main provides SSH connectivity to AgentLab sandboxes.
//
// The ssh command generates SSH connection parameters for connecting to
// sandbox VMs via the agent subnet. It includes route detection to warn
// when connecting to sandboxes over Tailscale without proper subnet routing.
//
// # Usage
//
//	# Print SSH command (default)
//	agentlab ssh 1001
//
//	# Execute SSH directly (replaces CLI process)
//	agentlab ssh 1001 --exec
//
//	# Custom user and port
//	agentlab ssh 1001 --user ubuntu --port 2222
//
//	# Output JSON for scripting
//	agentlab ssh 1001 --json
//
// # Route Detection
//
// On Linux, the command uses "ip route get" to verify that the route to
// the sandbox IP goes through the Tailscale interface. If not, it warns
// that the agent subnet route may need to be enabled in the Tailscale admin
// console.
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
	defaultIdentityPath  = "/etc/agentlab/keys/agentlab_id_ed25519"
)

// sshOutput contains the SSH connection details for a sandbox.
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

// routeInfo contains information about the network route to an IP address.
type routeInfo struct {
	Device string
	Via    string
}

// runSSHCommand handles the 'agentlab ssh' command.
// It fetches sandbox details, validates the IP, and either prints the SSH command
// or executes SSH directly with --exec.
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

	preArgs, explicitRemoteCmd, hasExplicitRemoteCmd := splitDoubleDash(args)
	vmidToken, parseArgs := extractVMIDArg(preArgs)
	if vmidToken == "" {
		printSSHUsage()
		return fmt.Errorf("vmid is required")
	}
	// Parse flags from the remaining args (supports flags appearing before or after <vmid>).
	if err := parseFlags(fs, parseArgs, printSSHUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	vmid, err := parseVMID(vmidToken)
	if err != nil {
		return err
	}

	remoteCmd := []string{}
	if hasExplicitRemoteCmd {
		remoteCmd = explicitRemoteCmd
	} else {
		remoteCmd = fs.Args()
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
	if execFlag && len(remoteCmd) == 0 && !isInteractive() {
		return fmt.Errorf("--exec requires an interactive terminal (or pass a remote command after <vmid> with --)")
	}

	client := newAPIClient(opts.socketPath, opts.timeout)

	waitCtx := ctx
	cancel := func() {}
	if opts.timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, opts.timeout)
	}
	defer cancel()

	var resp sandboxResponse
	var ip string
	wait := 250 * time.Millisecond
	maxWait := 2 * time.Second
	for {
		payload, err := client.doJSON(waitCtx, http.MethodGet, fmt.Sprintf("/v1/sandboxes/%d", vmid), nil)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(payload, &resp); err != nil {
			return err
		}
		ip = strings.TrimSpace(resp.IP)
		if ip != "" {
			break
		}
		if waitCtx.Err() != nil {
			return fmt.Errorf("sandbox %d has no IP yet", vmid)
		}
		timer := time.NewTimer(wait)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return fmt.Errorf("sandbox %d has no IP yet", vmid)
		case <-timer.C:
		}
		wait *= 2
		if wait > maxWait {
			wait = maxWait
		}
	}

	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return fmt.Errorf("sandbox %d returned invalid IP %q", vmid, ip)
	}

	identity = strings.TrimSpace(identity)
	if identity == "" {
		if env := strings.TrimSpace(os.Getenv("AGENTLAB_SSH_IDENTITY")); env != "" {
			identity = env
		} else if isReadableFile(defaultIdentityPath) {
			identity = defaultIdentityPath
		}
	}
	target := fmt.Sprintf("%s@%s", user, ip)
	sshArgs := buildSSHArgs(target, port, identity)
	if len(remoteCmd) > 0 {
		sshArgs = append(sshArgs, remoteCmd...)
	}
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

// buildSSHArgs constructs the SSH argument list for connecting to a target.
func buildSSHArgs(target string, port int, identity string) []string {
	args := []string{
		// Avoid prompts so AI agents can run reliably in non-interactive contexts.
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
	}
	if identity != "" {
		args = append(args, "-o", "IdentitiesOnly=yes")
		args = append(args, "-i", identity)
	}
	if port != defaultSSHPort {
		args = append(args, "-p", strconv.Itoa(port))
	}
	args = append(args, target)
	return args
}

func splitDoubleDash(args []string) (before []string, after []string, ok bool) {
	for i, v := range args {
		if v == "--" {
			return args[:i], args[i+1:], true
		}
	}
	return args, nil, false
}

func extractVMIDArg(args []string) (vmid string, rest []string) {
	for i, v := range args {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "-") {
			continue
		}
		vmid = trimmed
		rest = append([]string{}, args[:i]...)
		rest = append(rest, args[i+1:]...)
		return vmid, rest
	}
	return "", args
}

func isReadableFile(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// execSSH replaces the current process with SSH.
// This provides a seamless SSH experience by exec'ing the ssh binary.
func execSSH(args []string) error {
	path, err := exec.LookPath("ssh")
	if err != nil {
		return err
	}
	argv := append([]string{"ssh"}, args...)
	return syscall.Exec(path, argv, os.Environ())
}

// tailnetWarning checks if the route to the IP goes through Tailscale.
// Returns a warning message if the route is not via Tailscale.
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

// parseIPRouteGet parses the output of 'ip route get' to extract route information.
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

// formatShellCommand formats command arguments as a shell-escaped string.
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

// shellQuote quotes a string for safe use in a shell command.
// It uses single quotes and escapes any embedded single quotes.
func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, isShellSpecial) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// isShellSpecial returns true if the rune is a shell special character.
func isShellSpecial(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\v', '\f', '\\', '\'', '"', '$', '`':
		return true
	default:
		return false
	}
}

// isInteractive returns true if both stdin and stdout are terminals.
func isInteractive() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
}
