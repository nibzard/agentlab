// ABOUTME: Host initialization helpers for remote control and Tailscale Serve.
// ABOUTME: Provides `agentlab init` checks and optional apply workflow.

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultControlPort = 8845
	defaultConfigPath  = "/etc/agentlab/config.yaml"
)

type initCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type initReport struct {
	Applied        bool        `json:"applied"`
	Checks         []initCheck `json:"checks"`
	ConnectCommand string      `json:"connect_command,omitempty"`
}

func runInitCommand(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("init")
	opts := base
	opts.bind(fs)
	var apply bool
	var controlPort int
	var controlToken string
	var rotateToken bool
	var tailscaleServe bool
	var noTailscaleServe bool
	var help bool
	fs.BoolVar(&apply, "apply", false, "apply recommended host setup changes")
	fs.IntVar(&controlPort, "control-port", defaultControlPort, "control plane port")
	fs.StringVar(&controlToken, "control-token", "", "control plane auth token (optional)")
	fs.BoolVar(&rotateToken, "rotate-control-token", false, "rotate control auth token")
	fs.BoolVar(&tailscaleServe, "tailscale-serve", false, "force tailscale serve publishing")
	fs.BoolVar(&noTailscaleServe, "no-tailscale-serve", false, "disable tailscale serve publishing")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printInitUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	if tailscaleServe && noTailscaleServe {
		return fmt.Errorf("--tailscale-serve and --no-tailscale-serve are mutually exclusive")
	}
	if controlPort <= 0 || controlPort > 65535 {
		return fmt.Errorf("control-port must be between 1 and 65535")
	}

	if apply {
		if os.Geteuid() != 0 {
			return fmt.Errorf("agentlab init --apply must be run as root")
		}
		mode := "auto"
		if tailscaleServe {
			mode = "on"
		} else if noTailscaleServe {
			mode = "off"
		}
		if err := applyRemoteControl(ctx, defaultConfigPath, controlPort, controlToken, rotateToken, mode); err != nil {
			return err
		}
	}

	report, err := collectInitReport(ctx, defaultConfigPath, controlPort)
	if err != nil {
		return err
	}
	report.Applied = apply
	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(report)
	}
	printInitReport(report)
	return nil
}

func collectInitReport(ctx context.Context, configPath string, fallbackPort int) (initReport, error) {
	lines, _, err := readConfigLines(configPath)
	if err != nil {
		return initReport{}, err
	}
	controlListen := findConfigValue(lines, "control_listen")
	controlToken := findConfigValue(lines, "control_auth_token")
	report := initReport{}

	controlDetail := ""
	controlStatus := "missing"
	if controlListen == "" {
		controlDetail = "control_listen not set"
	} else if controlToken == "" {
		controlDetail = "control_auth_token not set"
	} else {
		controlStatus = "ok"
		controlDetail = fmt.Sprintf("control_listen=%s (token set)", controlListen)
	}
	report.Checks = append(report.Checks, initCheck{
		Name:   "control_plane",
		Status: controlStatus,
		Detail: controlDetail,
	})

	host, port := parseListenHostPort(controlListen, fallbackPort)
	tailscaleCheck, dnsName := checkTailscaleServe(ctx, host, port, controlListen != "")
	report.Checks = append(report.Checks, tailscaleCheck)

	if controlListen != "" && controlToken != "" {
		endpoint := fmt.Sprintf("http://%s", controlListen)
		if dnsName != "" {
			endpoint = fmt.Sprintf("http://%s:%d", dnsName, port)
		}
		report.ConnectCommand = fmt.Sprintf("agentlab connect --endpoint %s --token %s", endpoint, controlToken)
	}
	return report, nil
}

func applyRemoteControl(ctx context.Context, configPath string, port int, token string, rotate bool, tailscaleMode string) error {
	lines, _, err := readConfigLines(configPath)
	if err != nil {
		return err
	}
	controlListen := fmt.Sprintf("127.0.0.1:%d", port)
	existingToken := findConfigValue(lines, "control_auth_token")
	if token == "" {
		if rotate || existingToken == "" {
			token, err = generateToken()
			if err != nil {
				return err
			}
		} else {
			token = existingToken
		}
	}
	lines, _ = upsertConfigLine(lines, "control_listen", controlListen)
	lines, _ = upsertConfigLine(lines, "control_auth_token", token)
	if err := writeConfigLines(configPath, lines); err != nil {
		return err
	}
	if err := enforceRootConfigPermissions(configPath); err != nil {
		return err
	}

	if tailscaleMode != "off" {
		running, err := tailscaleRunning(ctx)
		if err != nil {
			return err
		}
		if running {
			if err := runTailscaleServe(ctx, port); err != nil {
				return err
			}
		} else if tailscaleMode == "on" {
			return fmt.Errorf("tailscale serve requested but tailscale is not running")
		}
	}

	if err := restartAgentlabd(ctx); err != nil {
		return err
	}
	return nil
}

func printInitReport(report initReport) {
	fmt.Println("Init checks:")
	for _, check := range report.Checks {
		status := strings.ToUpper(check.Status)
		if check.Detail == "" {
			fmt.Printf("- %s: %s\n", check.Name, status)
			continue
		}
		fmt.Printf("- %s: %s (%s)\n", check.Name, status, check.Detail)
	}
	if report.ConnectCommand != "" {
		fmt.Printf("Connect: %s\n", report.ConnectCommand)
	}
}

func readConfigLines(path string) ([]string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, false, nil
		}
		return nil, false, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	return lines, true, nil
}

func writeConfigLines(path string, lines []string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	data := strings.Join(lines, "\n")
	if !strings.HasSuffix(data, "\n") {
		data += "\n"
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(data), 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return nil
}

func enforceRootConfigPermissions(path string) error {
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("config must be 0600: %w", err)
	}
	if err := os.Chown(path, 0, 0); err != nil {
		return fmt.Errorf("config must be owned by root: %w", err)
	}
	return nil
}

func findConfigValue(lines []string, key string) string {
	prefix := key + ":"
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
		if idx := strings.Index(value, "#"); idx >= 0 {
			value = strings.TrimSpace(value[:idx])
		}
		return strings.Trim(value, "\"'")
	}
	return ""
}

func upsertConfigLine(lines []string, key, value string) ([]string, bool) {
	line := fmt.Sprintf("%s: %s", key, yamlQuote(value))
	prefix := key + ":"
	updated := false
	for i, current := range lines {
		trimmed := strings.TrimSpace(current)
		if strings.HasPrefix(trimmed, prefix) {
			if current != line {
				lines[i] = line
				updated = true
			}
			return lines, updated
		}
	}
	lines = append(lines, line)
	return lines, true
}

func yamlQuote(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return fmt.Sprintf("\"%s\"", value)
}

func parseListenHostPort(listen string, fallbackPort int) (string, int) {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		return "127.0.0.1", fallbackPort
	}
	host, portStr, err := net.SplitHostPort(listen)
	if err != nil {
		return "127.0.0.1", fallbackPort
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return host, fallbackPort
	}
	return host, port
}

func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func tailscaleRunning(ctx context.Context) (bool, error) {
	if _, err := exec.LookPath("tailscale"); err != nil {
		return false, nil
	}
	cmd := exec.CommandContext(ctx, "tailscale", "status")
	if err := cmd.Run(); err != nil {
		return false, nil
	}
	return true, nil
}

func runTailscaleServe(ctx context.Context, port int) error {
	cmd := exec.CommandContext(ctx, "tailscale", "serve", fmt.Sprintf("--tcp=%d", port), fmt.Sprintf("tcp://127.0.0.1:%d", port))
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("tailscale serve failed: %s", msg)
	}
	return nil
}

func tailscaleDNSName(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "tailscale", "status", "--json")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	var status struct {
		Self struct {
			DNSName  string `json:"DNSName"`
			HostName string `json:"HostName"`
		} `json:"Self"`
		MagicDNSSuffix string `json:"MagicDNSSuffix"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return "", err
	}
	dns := strings.TrimSpace(status.Self.DNSName)
	dns = strings.TrimSuffix(dns, ".")
	if dns == "" {
		host := strings.TrimSpace(status.Self.HostName)
		suffix := strings.TrimSpace(status.MagicDNSSuffix)
		suffix = strings.TrimSuffix(suffix, ".")
		if host != "" && suffix != "" {
			dns = fmt.Sprintf("%s.%s", host, suffix)
		}
	}
	if dns == "" {
		return "", fmt.Errorf("tailscale status missing dns name")
	}
	return dns, nil
}

func checkTailscaleServe(ctx context.Context, host string, port int, controlConfigured bool) (initCheck, string) {
	check := initCheck{Name: "tailscale_serve"}
	running, err := tailscaleRunning(ctx)
	if err != nil {
		check.Status = "error"
		check.Detail = err.Error()
		return check, ""
	}
	if !running {
		check.Status = "skipped"
		check.Detail = "tailscale not running"
		return check, ""
	}
	if !controlConfigured {
		check.Status = "skipped"
		check.Detail = "control_listen not set"
		return check, ""
	}
	dnsName, _ := tailscaleDNSName(ctx)
	cmd := exec.CommandContext(ctx, "tailscale", "serve", "status")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" && serveMissingMessage(msg) {
			check.Status = "missing"
			check.Detail = fmt.Sprintf("no serve rule for tcp/%d", port)
			return check, dnsName
		}
		check.Status = "error"
		if msg == "" {
			check.Detail = err.Error()
		} else {
			check.Detail = msg
		}
		return check, dnsName
	}
	if serveRulePresent(string(out), host, port) {
		check.Status = "ok"
		check.Detail = fmt.Sprintf("tcp://%s:%d", host, port)
		return check, dnsName
	}
	check.Status = "missing"
	check.Detail = fmt.Sprintf("no serve rule for tcp/%d", port)
	return check, dnsName
}

func serveRulePresent(output string, host string, port int) bool {
	needle := fmt.Sprintf("tcp://%s:%d", host, port)
	if strings.Contains(output, needle) {
		return true
	}
	lower := strings.ToLower(output)
	if strings.Contains(lower, fmt.Sprintf("tcp %d", port)) {
		return true
	}
	return false
}

func serveMissingMessage(output string) bool {
	lower := strings.ToLower(output)
	if strings.Contains(lower, "no serve") {
		return true
	}
	if strings.Contains(lower, "not configured") {
		return true
	}
	if strings.Contains(lower, "no listener") {
		return true
	}
	return false
}

func restartAgentlabd(ctx context.Context) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "systemctl", "restart", "agentlabd.service")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("systemctl restart failed: %s", msg)
	}
	return nil
}
