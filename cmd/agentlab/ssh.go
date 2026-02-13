// ABOUTME: SSH command for connecting to sandbox VMs via agent subnet.
// ABOUTME: Provides connection details with route detection for Tailscale networks.

// Package main provides SSH connectivity to AgentLab sandboxes.
//
// The ssh command generates SSH connection parameters for connecting to
// sandbox VMs via the agent subnet. It includes route detection to warn
// when connecting to sandboxes over Tailscale without proper subnet routing.
//
// Usage
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
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
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
	sandboxPollInterval  = 2 * time.Second
	sshProbeInterval     = 1 * time.Second
	sshProbeTimeout      = 750 * time.Millisecond
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

type jumpConfig struct {
	Host string
	User string
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
	noStart := false
	waitSSH := false
	jumpHost := ""
	jumpUser := ""
	help := false
	fs.StringVar(&user, "user", defaultSSHUser, "ssh username")
	fs.StringVar(&user, "u", defaultSSHUser, "ssh username")
	fs.IntVar(&port, "port", defaultSSHPort, "ssh port")
	fs.IntVar(&port, "p", defaultSSHPort, "ssh port")
	fs.StringVar(&identity, "identity", "", "ssh identity file")
	fs.StringVar(&identity, "i", "", "ssh identity file")
	fs.StringVar(&jumpHost, "jump-host", "", "ssh jump host for ProxyJump fallback")
	fs.StringVar(&jumpUser, "jump-user", "", "ssh jump username for ProxyJump fallback")
	fs.BoolVar(&execFlag, "exec", false, "exec ssh instead of printing the command")
	fs.BoolVar(&execFlag, "e", false, "exec ssh instead of printing the command")
	fs.BoolVar(&noStart, "no-start", false, "do not auto-start stopped sandboxes")
	fs.BoolVar(&waitSSH, "wait", false, "wait for ssh readiness before returning")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printSSHUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printSSHUsage()
		}
		return fmt.Errorf("vmid is required")
	}
	vmidArg := fs.Arg(0)
	extraArgs := fs.Args()[1:]
	vmid, err := parseVMID(vmidArg)
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
	waitCtx, cancel := withWaitTimeout(ctx, opts.timeout)
	defer cancel()
	clientOpts, err := opts.clientOptions()
	if err != nil {
		return err
	}
	client := newAPIClient(clientOpts, opts.timeout)
	jumpCfg, err := resolveJumpConfig(clientOpts.Endpoint, jumpHost, jumpUser)
	if err != nil {
		return err
	}
	resp, err := fetchSandbox(waitCtx, client, vmid)
	if err != nil {
		return err
	}
	if strings.EqualFold(resp.State, "STOPPED") {
		if noStart {
			return fmt.Errorf("sandbox %d is stopped; use agentlab sandbox start %d or omit --no-start", vmid, vmid)
		}
		resp, err = startSandbox(waitCtx, client, vmid)
		if err != nil {
			return err
		}
	}
	ip := strings.TrimSpace(resp.IP)
	if ip == "" {
		resp, err = waitForSandboxIP(waitCtx, client, vmid)
		if err != nil {
			return err
		}
		ip = strings.TrimSpace(resp.IP)
	}
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return fmt.Errorf("sandbox %d returned invalid IP %q", vmid, ip)
	}

	identity = strings.TrimSpace(identity)
	target := fmt.Sprintf("%s@%s", user, ip)
	address := net.JoinHostPort(ip, strconv.Itoa(port))
	reachable, err := probeSSH(waitCtx, address)
	if err != nil {
		return err
	}
	useJump := !reachable
	if useJump {
		jumpCfg.Host = strings.TrimSpace(jumpCfg.Host)
		jumpCfg.User = strings.TrimSpace(jumpCfg.User)
		if jumpCfg.Host == "" {
			return newCLIError(
				fmt.Sprintf("cannot reach sandbox %d at %s", vmid, address),
				"",
				fmt.Sprintf("enable the Tailscale subnet route (accept-routes) for %s", defaultAgentSubnet),
				"or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>",
			)
		}
		if jumpCfg.User == "" && !strings.Contains(jumpCfg.Host, "@") {
			return newCLIError(
				fmt.Sprintf("jump host %q requires a user", jumpCfg.Host),
				"",
				"set --jump-user or configure it via agentlab connect --jump-user <user>",
			)
		}
	}
	if waitSSH {
		if useJump {
			if err := waitForSSHViaJump(waitCtx, jumpCfg, target, port, identity); err != nil {
				return err
			}
		} else {
			if err := waitForSSH(waitCtx, ip, port); err != nil {
				return err
			}
		}
	}

	sshArgs := buildSSHArgs(target, port, identity, jumpCfg, useJump)
	if len(extraArgs) > 0 {
		sshArgs = append(sshArgs, extraArgs...)
	}
	fullArgs := append([]string{"ssh"}, sshArgs...)
	warning := ""
	if !useJump {
		warning = tailnetWarning(ctx, parsedIP, defaultAgentSubnet)
	}
	touchSandboxBestEffort(waitCtx, client, vmid)

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
		return execSSHFn(sshArgs)
	}

	fmt.Fprintln(os.Stdout, formatShellCommand(fullArgs))
	return nil
}

func withWaitTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func fetchSandbox(ctx context.Context, client *apiClient, vmid int) (sandboxResponse, error) {
	path, err := endpointPath("/v1/sandboxes", strconv.Itoa(vmid))
	if err != nil {
		return sandboxResponse{}, err
	}
	payload, err := client.doJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return sandboxResponse{}, wrapSandboxNotFound(ctx, client, vmid, err)
	}
	var resp sandboxResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return sandboxResponse{}, err
	}
	return resp, nil
}

func startSandbox(ctx context.Context, client *apiClient, vmid int) (sandboxResponse, error) {
	path, err := endpointPath("/v1/sandboxes", strconv.Itoa(vmid), "start")
	if err != nil {
		return sandboxResponse{}, err
	}
	payload, err := client.doJSON(ctx, http.MethodPost, path, nil)
	if err != nil {
		return sandboxResponse{}, wrapSandboxNotFound(ctx, client, vmid, err)
	}
	var resp sandboxResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return sandboxResponse{}, err
	}
	return resp, nil
}

func waitForSandboxIP(ctx context.Context, client *apiClient, vmid int) (sandboxResponse, error) {
	ticker := time.NewTicker(sandboxPollInterval)
	defer ticker.Stop()
	for {
		resp, err := fetchSandbox(ctx, client, vmid)
		if err == nil {
			ip := strings.TrimSpace(resp.IP)
			if ip != "" {
				return resp, nil
			}
			if terminalState(resp.State) {
				return resp, fmt.Errorf("sandbox %d is %s and has no IP", vmid, strings.ToLower(resp.State))
			}
		}
		if err := ctx.Err(); err != nil {
			return sandboxResponse{}, waitError(err, fmt.Sprintf("sandbox %d IP", vmid))
		}
		select {
		case <-ctx.Done():
			return sandboxResponse{}, waitError(ctx.Err(), fmt.Sprintf("sandbox %d IP", vmid))
		case <-ticker.C:
		}
	}
}

func waitForSSH(ctx context.Context, ip string, port int) error {
	address := net.JoinHostPort(ip, strconv.Itoa(port))
	ticker := time.NewTicker(sshProbeInterval)
	defer ticker.Stop()
	for {
		ok, err := probeSSH(ctx, address)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return waitError(ctx.Err(), fmt.Sprintf("ssh on %s", address))
		case <-ticker.C:
		}
	}
}

func terminalState(state string) bool {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "DESTROYED", "FAILED", "TIMEOUT":
		return true
	default:
		return false
	}
}

func waitError(err error, desc string) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("timed out waiting for %s", desc)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("canceled while waiting for %s", desc)
	}
	return err
}

func waitForSSHViaJump(ctx context.Context, jump jumpConfig, target string, port int, identity string) error {
	jumpSpec := buildJumpSpec(jump)
	if strings.TrimSpace(jumpSpec) == "" {
		return fmt.Errorf("jump host is required for ProxyJump")
	}
	ticker := time.NewTicker(sshProbeInterval)
	defer ticker.Stop()
	for {
		ok, err := probeSSHViaJump(ctx, jumpSpec, target, port, identity)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return waitError(ctx.Err(), fmt.Sprintf("ssh via %s", jumpSpec))
		case <-ticker.C:
		}
	}
}

// buildSSHArgs constructs the SSH argument list for connecting to a target.
func buildSSHArgs(target string, port int, identity string, jump jumpConfig, useJump bool) []string {
	args := []string{}
	if useJump {
		jumpSpec := buildJumpSpec(jump)
		if jumpSpec != "" {
			args = append(args, "-J", jumpSpec)
		}
	}
	if identity != "" {
		args = append(args, "-i", identity)
	}
	if port != defaultSSHPort {
		args = append(args, "-p", strconv.Itoa(port))
	}
	args = append(args, target)
	return args
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

func resolveJumpConfig(endpoint string, flagHost string, flagUser string) (jumpConfig, error) {
	cfg, ok, err := loadClientConfig()
	if err != nil {
		return jumpConfig{}, err
	}
	host := strings.TrimSpace(flagHost)
	userValue := strings.TrimSpace(flagUser)
	if host == "" && ok {
		host = strings.TrimSpace(cfg.JumpHost)
	}
	if userValue == "" && ok {
		userValue = strings.TrimSpace(cfg.JumpUser)
	}
	if host == "" {
		host = strings.TrimSpace(defaultJumpHostFromEndpoint(endpoint))
	}
	if host != "" && userValue == "" {
		userValue = defaultJumpUser()
	}
	return jumpConfig{Host: host, User: userValue}, nil
}

func defaultJumpHostFromEndpoint(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return ""
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return host
}

func defaultJumpUser() string {
	if value := strings.TrimSpace(os.Getenv("USER")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("LOGNAME")); value != "" {
		return value
	}
	if info, err := user.Current(); err == nil {
		return strings.TrimSpace(info.Username)
	}
	return ""
}

func buildJumpSpec(jump jumpConfig) string {
	host := strings.TrimSpace(jump.Host)
	if host == "" {
		return ""
	}
	if strings.Contains(host, "@") {
		return host
	}
	userValue := strings.TrimSpace(jump.User)
	if userValue == "" {
		return host
	}
	return fmt.Sprintf("%s@%s", userValue, host)
}

// tailnetWarning checks if the route to the IP goes through Tailscale.
// Returns a warning message if the route is not via Tailscale.
func tailnetWarning(ctx context.Context, ip net.IP, subnet string) string {
	check := checkTailnetRouteToIP(ctx, ip, subnet)
	if check.Status == "ok" {
		return ""
	}
	detail := strings.TrimSpace(check.Detail)
	if detail == "" {
		detail = fmt.Sprintf("ensure the agent subnet route (%s) is enabled", check.Subnet)
	}
	switch check.Status {
	case "warn":
		return "Warning: " + detail
	default:
		return "Note: " + detail
	}
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
	case ' ', '\t', '\n', '\r', '\v', '\f', '\\', '\'', '"', '$', '`', ';', '&', '|', '<', '>', '(', ')', '*', '?', '!', '#', '[', ']', '{', '}', '~':
		return true
	default:
		return false
	}
}

// isInteractive returns true if both stdin and stdout are terminals.
var isInteractiveFn = func() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
}

func isInteractive() bool {
	return isInteractiveFn()
}

var sshDialFn = func(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}

var sshCommandFn = func(ctx context.Context, args []string) ([]byte, error) {
	path, err := exec.LookPath("ssh")
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	return cmd.CombinedOutput()
}

var execSSHFn = execSSH

func probeSSH(ctx context.Context, address string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, sshProbeTimeout)
	defer cancel()
	conn, err := sshDialFn(probeCtx, "tcp", address)
	if err == nil {
		_ = conn.Close()
		return true, nil
	}
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return false, waitError(ctx.Err(), fmt.Sprintf("ssh on %s", address))
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, waitError(err, fmt.Sprintf("ssh on %s", address))
	}
	return false, nil
}

func probeSSHViaJump(ctx context.Context, jumpSpec string, target string, port int, identity string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, sshProbeTimeout+250*time.Millisecond)
	defer cancel()
	timeoutSeconds := int(sshProbeTimeout.Seconds())
	if timeoutSeconds < 1 {
		timeoutSeconds = 1
	}
	args := []string{
		"-J", jumpSpec,
		"-o", "BatchMode=yes",
		"-o", fmt.Sprintf("ConnectTimeout=%d", timeoutSeconds),
		"-o", "ConnectionAttempts=1",
	}
	if identity != "" {
		args = append(args, "-i", identity)
	}
	if port != defaultSSHPort {
		args = append(args, "-p", strconv.Itoa(port))
	}
	args = append(args, target)
	out, err := sshCommandFn(probeCtx, args)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return false, waitError(ctx.Err(), fmt.Sprintf("ssh via %s", jumpSpec))
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return false, nil
	}
	if isSSHAuthFailure(string(out)) {
		return true, nil
	}
	if err := ctx.Err(); err != nil {
		return false, waitError(err, fmt.Sprintf("ssh via %s", jumpSpec))
	}
	return false, nil
}

func isSSHAuthFailure(output string) bool {
	lower := strings.ToLower(output)
	if strings.Contains(lower, "permission denied") {
		return true
	}
	if strings.Contains(lower, "authentication failed") {
		return true
	}
	if strings.Contains(lower, "no supported authentication methods available") {
		return true
	}
	if strings.Contains(lower, "publickey") {
		return true
	}
	if strings.Contains(lower, "host key verification failed") {
		return true
	}
	return false
}
