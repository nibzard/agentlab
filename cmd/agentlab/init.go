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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/config"
	"github.com/agentlab/agentlab/internal/daemon"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

const (
	defaultControlPort = 8845
	defaultConfigPath  = "/etc/agentlab/config.yaml"
	defaultAgentBridge = "vmbr1"
)

type initCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type initReport struct {
	Applied        bool        `json:"applied"`
	Checks         []initCheck `json:"checks"`
	ApplySteps     []initCheck `json:"apply_steps,omitempty"`
	SmokeTest      *initCheck  `json:"smoke_test,omitempty"`
	ConnectCommand string      `json:"connect_command,omitempty"`
}

type initState struct {
	Config      config.Config
	Profiles    map[string]models.Profile
	ProfilesErr error
	TemplateIDs []int
}

type skillBundleManifest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func runInitCommand(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("init")
	opts := base
	opts.bind(fs)
	var apply bool
	var smokeTest bool
	var assetsPath string
	var force bool
	var controlPort int
	var controlToken string
	var rotateToken bool
	var tailscaleServe bool
	var noTailscaleServe bool
	var help bool
	fs.BoolVar(&apply, "apply", false, "apply recommended host setup changes")
	fs.BoolVar(&smokeTest, "smoke-test", false, "run end-to-end provisioning smoke test")
	fs.StringVar(&assetsPath, "assets", "", "path to agentlab repo root (for scripts)")
	fs.BoolVar(&force, "force", false, "overwrite managed network files when applying")
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

	report, state, err := collectInitReport(ctx, defaultConfigPath, controlPort)
	if err != nil {
		return err
	}

	assetsRoot := ""
	if apply || smokeTest {
		root, err := resolveInitAssets(assetsPath)
		if err != nil {
			return err
		}
		assetsRoot = root
	}

	var applySteps []initCheck
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
		applySteps, err = applyInit(ctx, state, report, assetsRoot, initApplyOptions{
			controlPort:   controlPort,
			controlToken:  controlToken,
			rotateToken:   rotateToken,
			tailscaleMode: mode,
			force:         force,
			jsonOutput:    opts.jsonOutput,
		})
		if err != nil {
			return err
		}
		report, state, err = collectInitReport(ctx, defaultConfigPath, controlPort)
		if err != nil {
			return err
		}
	}

	report.Applied = apply
	report.ApplySteps = applySteps

	if smokeTest {
		smokeCheck, err := runSmokeTest(ctx, assetsRoot, state, opts.jsonOutput)
		if err != nil {
			return err
		}
		report.SmokeTest = &smokeCheck
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(report)
	}
	printInitReport(report)
	return nil
}

func collectInitReport(ctx context.Context, configPath string, fallbackPort int) (initReport, initState, error) {
	state, err := loadInitState(configPath)
	if err != nil {
		return initReport{}, initState{}, err
	}

	report := initReport{}
	controlListen := state.Config.ControlListen
	controlToken := state.Config.ControlAuthToken

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

	bridgeHost := listenHost(state.Config.BootstrapListen)
	if bridgeHost == "" {
		bridgeHost = listenHost(state.Config.ArtifactListen)
	}
	report.Checks = append(report.Checks, checkBridge(ctx, defaultAgentBridge, bridgeHost, state.Config.AgentSubnet))
	report.Checks = append(report.Checks, checkIPForward())
	report.Checks = append(report.Checks, checkNFTables(ctx))
	report.Checks = append(report.Checks, checkSnippetsDir(state.Config.SnippetsDir, state.Config.SnippetStorage))
	report.Checks = append(report.Checks, checkSkillBundle(state))
	report.Checks = append(report.Checks, checkProfiles(state))
	report.Checks = append(report.Checks, checkTemplates(ctx, state.TemplateIDs))

	if controlListen != "" && controlToken != "" {
		endpoint := fmt.Sprintf("http://%s", controlListen)
	if dnsName != "" {
		endpoint = fmt.Sprintf("http://%s:%d", dnsName, port)
	}
		report.ConnectCommand = fmt.Sprintf("agentlab connect --endpoint %s --token %s", endpoint, controlToken)
	}
	return report, state, nil
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
	if len(report.ApplySteps) > 0 {
		fmt.Println("Apply steps:")
		for _, step := range report.ApplySteps {
			status := strings.ToUpper(step.Status)
			if step.Detail == "" {
				fmt.Printf("- %s: %s\n", step.Name, status)
				continue
			}
			fmt.Printf("- %s: %s (%s)\n", step.Name, status, step.Detail)
		}
	}
	if report.SmokeTest != nil {
		status := strings.ToUpper(report.SmokeTest.Status)
		if report.SmokeTest.Detail == "" {
			fmt.Printf("Smoke test: %s\n", status)
		} else {
			fmt.Printf("Smoke test: %s (%s)\n", status, report.SmokeTest.Detail)
		}
	}
	if report.ConnectCommand != "" {
		fmt.Printf("Connect: %s\n", report.ConnectCommand)
	}
}

type initApplyOptions struct {
	controlPort   int
	controlToken  string
	rotateToken   bool
	tailscaleMode string
	force         bool
	jsonOutput    bool
}

func applyInit(ctx context.Context, state initState, report initReport, assetsRoot string, opts initApplyOptions) ([]initCheck, error) {
	steps := []initCheck{}
	setupScript := filepath.Join(assetsRoot, "scripts", "net", "setup_vmbr1.sh")
	nftScript := filepath.Join(assetsRoot, "scripts", "net", "apply.sh")
	templateScript := filepath.Join(assetsRoot, "scripts", "create_template.sh")
	installSkillScript := filepath.Join(assetsRoot, "scripts", "install_host.sh")

	bridgeStatus := findCheckStatus(report.Checks, "bridge_"+defaultAgentBridge)
	ipForwardStatus := findCheckStatus(report.Checks, "ip_forward")
	if bridgeStatus != "ok" || ipForwardStatus != "ok" {
		args := []string{"--apply"}
		if opts.force {
			args = append(args, "--force")
		}
		if err := runScript(ctx, setupScript, args, opts.jsonOutput); err != nil {
			return steps, fmt.Errorf("setup vmbr1 failed: %w", err)
		}
		steps = append(steps, initCheck{Name: "setup_vmbr1", Status: "ok"})
	} else {
		steps = append(steps, initCheck{Name: "setup_vmbr1", Status: "skipped", Detail: "bridge already configured"})
	}

	if findCheckStatus(report.Checks, "nftables") != "ok" {
		args := []string{"--apply"}
		if opts.force {
			args = append(args, "--force")
		}
		if err := runScript(ctx, nftScript, args, opts.jsonOutput); err != nil {
			return steps, fmt.Errorf("apply nftables failed: %w", err)
		}
		steps = append(steps, initCheck{Name: "apply_nftables", Status: "ok"})
	} else {
		steps = append(steps, initCheck{Name: "apply_nftables", Status: "skipped", Detail: "nftables already configured"})
	}

	templateStep := initCheck{Name: "create_template", Status: "skipped"}
	if len(state.TemplateIDs) == 0 {
		templateStep.Detail = "no profiles loaded"
	} else {
		results, err := validateTemplates(ctx, state.TemplateIDs)
		if err != nil {
			if errors.Is(err, errQmNotFound) {
				return steps, fmt.Errorf("qm not found; cannot create template")
			}
			return steps, err
		}
		var missing []int
		for _, id := range state.TemplateIDs {
			if results[id] == nil {
				continue
			}
			if strings.Contains(results[id].Error(), "does not exist") {
				missing = append(missing, id)
				continue
			}
			return steps, fmt.Errorf("template %d invalid: %w", id, results[id])
		}
		if len(missing) == 0 {
			templateStep.Detail = "template already present"
		} else {
			sort.Ints(missing)
			for _, id := range missing {
				args := []string{"--vmid", strconv.Itoa(id)}
				if err := runScript(ctx, templateScript, args, opts.jsonOutput); err != nil {
					return steps, fmt.Errorf("create template %d failed: %w", id, err)
				}
			}
			templateStep.Status = "ok"
			templateStep.Detail = fmt.Sprintf("created template(s) %s", joinInts(missing))
		}
	}
	steps = append(steps, templateStep)

	skillBundleStatus := findCheckStatus(report.Checks, "skill_bundle")
	if skillBundleStatus == "ok" && !opts.force {
		steps = append(steps, initCheck{Name: "install_skills", Status: "skipped", Detail: "skill bundle already up-to-date"})
	} else {
		if err := installSkillBundle(ctx, installSkillScript, opts.force, opts.jsonOutput); err != nil {
			return steps, fmt.Errorf("install skill bundle failed: %w", err)
		}
		detail := "installed/updated skill bundle"
		if manifest, _, err := readSkillBundleManifestFromRoot(assetsRoot); err == nil {
			detail = fmt.Sprintf("installed/updated %s@%s", manifest.Name, manifest.Version)
		}
		steps = append(steps, initCheck{Name: "install_skills", Status: "ok", Detail: detail})
	}

	if err := applyRemoteControl(ctx, defaultConfigPath, opts.controlPort, opts.controlToken, opts.rotateToken, opts.tailscaleMode); err != nil {
		return steps, err
	}
	steps = append(steps, initCheck{Name: "control_plane", Status: "ok", Detail: fmt.Sprintf("control_listen=127.0.0.1:%d", opts.controlPort)})

	return steps, nil
}

func runSmokeTest(ctx context.Context, assetsRoot string, state initState, jsonOutput bool) (initCheck, error) {
	script := filepath.Join(assetsRoot, "scripts", "tests", "golden_path.sh")
	if _, err := os.Stat(script); err != nil {
		return initCheck{Name: "smoke_test", Status: "error"}, fmt.Errorf("smoke test script not found at %s", script)
	}
	profile := pickSmokeProfile(state)
	args := []string{script, "--profile", profile}
	if _, err := os.Stat(defaultConfigPath); err == nil {
		args = append(args, "--config", defaultConfigPath)
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = prependPathEnv(os.Environ(), executableDir())
	if jsonOutput {
		output, err := cmd.CombinedOutput()
		if err != nil {
			return initCheck{Name: "smoke_test", Status: "error", Detail: fmt.Sprintf("profile=%s", profile)}, formatCommandError("smoke test failed", output, err)
		}
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return initCheck{Name: "smoke_test", Status: "error", Detail: fmt.Sprintf("profile=%s", profile)}, err
		}
	}
	return initCheck{Name: "smoke_test", Status: "ok", Detail: fmt.Sprintf("profile=%s", profile)}, nil
}

func checkSkillBundle(state initState) initCheck {
	check := initCheck{Name: "skill_bundle"}
	installedName := strings.TrimSpace(state.Config.ClaudeSkillBundleName)
	installedVersion := strings.TrimSpace(state.Config.ClaudeSkillBundleVersion)

	manifest, ok, err := readSkillBundleManifest()
	if err != nil || !ok {
		if installedName == "" && installedVersion == "" {
			check.Status = "missing"
			check.Detail = "skill bundle is not installed"
			return check
		}
		check.Status = "ok"
		if installedVersion == "" {
			check.Detail = fmt.Sprintf("installed name=%s (version unknown)", installedName)
			return check
		}
		check.Detail = fmt.Sprintf("installed=%s@%s (source manifest not available)", installedName, installedVersion)
		return check
	}

	if installedName == "" || installedVersion == "" {
		check.Status = "missing"
		check.Detail = fmt.Sprintf("installed none, expected %s@%s", manifest.Name, manifest.Version)
		return check
	}
	if installedName == manifest.Name && installedVersion == manifest.Version {
		check.Status = "ok"
		check.Detail = fmt.Sprintf("installed %s@%s", installedName, installedVersion)
		return check
	}
	check.Status = "upgrade"
	check.Detail = fmt.Sprintf("installed %s@%s, expected %s@%s", installedName, installedVersion, manifest.Name, manifest.Version)
	return check
}

func readSkillBundleManifest() (skillBundleManifest, bool, error) {
	root, err := resolveInitAssetsRoot()
	if err != nil {
		return skillBundleManifest{}, false, err
	}
	if root == "" {
		return skillBundleManifest{}, false, nil
	}
	return readSkillBundleManifestFromRoot(root)
}

func readSkillBundleManifestFromRoot(root string) (skillBundleManifest, bool, error) {
	manifestPath := filepath.Join(root, "skills", "agentlab", "bundle", "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return skillBundleManifest{}, false, err
	}
	var manifest skillBundleManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return skillBundleManifest{}, false, err
	}
	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.Version = strings.TrimSpace(manifest.Version)
	if manifest.Name == "" {
		return skillBundleManifest{}, false, fmt.Errorf("manifest missing name in %s", manifestPath)
	}
	if manifest.Version == "" {
		return skillBundleManifest{}, false, fmt.Errorf("manifest missing version in %s", manifestPath)
	}
	return manifest, true, nil
}

func installSkillBundle(ctx context.Context, installScript string, force bool, jsonOutput bool) error {
	cmd := exec.CommandContext(ctx, installScript, "--install-skills-only")
	cmd.Env = os.Environ()
	if force {
		cmd.Env = setEnv(cmd.Env, "CLAUDE_SKILL_FORCE", "1")
	}
	if jsonOutput {
		output, err := cmd.CombinedOutput()
		if err != nil {
			return formatCommandError("skill bundle install failed", output, err)
		}
		return nil
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func setEnv(env []string, key string, value string) []string {
	prefix := key + "="
	var out []string
	replaced := false
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			out = append(out, prefix+value)
			replaced = true
			continue
		}
		out = append(out, entry)
	}
	if !replaced {
		out = append(out, prefix+value)
	}
	return out
}

func resolveInitAssetsRoot() (string, error) {
	if cwd, err := os.Getwd(); err == nil {
		if root := findInitAssetsUpward(cwd); root != "" {
			return root, nil
		}
	}
	if exe, err := os.Executable(); err == nil {
		if root := findInitAssetsUpward(filepath.Dir(exe)); root != "" {
			return root, nil
		}
	}
	return "", fmt.Errorf("assets root not found")
}

func pickSmokeProfile(state initState) string {
	if len(state.Profiles) == 0 {
		return "yolo-ephemeral"
	}
	names := make([]string, 0, len(state.Profiles))
	for name := range state.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names[0]
}

func loadInitState(configPath string) (initState, error) {
	cfg := config.DefaultConfig()
	if strings.TrimSpace(configPath) != "" {
		cfg.ConfigPath = configPath
	}
	lines, _, err := readConfigLines(cfg.ConfigPath)
	if err != nil {
		return initState{}, err
	}
	applyInitConfigOverrides(&cfg, lines)
	state := initState{Config: cfg}
	profiles, err := daemon.LoadProfiles(cfg.ProfilesDir)
	if err != nil {
		state.ProfilesErr = err
		return state, nil
	}
	state.Profiles = profiles
	seen := make(map[int]struct{})
	for _, profile := range profiles {
		if profile.TemplateVM > 0 {
			seen[profile.TemplateVM] = struct{}{}
		}
	}
	for id := range seen {
		state.TemplateIDs = append(state.TemplateIDs, id)
	}
	sort.Ints(state.TemplateIDs)
	return state, nil
}

func applyInitConfigOverrides(cfg *config.Config, lines []string) {
	if cfg == nil {
		return
	}
	if value := findConfigValue(lines, "profiles_dir"); value != "" {
		cfg.ProfilesDir = value
	}
	if value := findConfigValue(lines, "snippets_dir"); value != "" {
		cfg.SnippetsDir = value
	}
	if value := findConfigValue(lines, "snippet_storage"); value != "" {
		cfg.SnippetStorage = value
	}
	if value := findConfigValue(lines, "control_listen"); value != "" {
		cfg.ControlListen = value
	}
	if value := findConfigValue(lines, "control_auth_token"); value != "" {
		cfg.ControlAuthToken = value
	}
	if value := findConfigValue(lines, "bootstrap_listen"); value != "" {
		cfg.BootstrapListen = value
	}
	if value := findConfigValue(lines, "artifact_listen"); value != "" {
		cfg.ArtifactListen = value
	}
	if value := findConfigValue(lines, "agent_subnet"); value != "" {
		cfg.AgentSubnet = value
	}
	artifactDir := findConfigValue(lines, "artifact_dir")
	if value := findConfigValue(lines, "data_dir"); value != "" {
		cfg.DataDir = value
		if artifactDir == "" {
			cfg.ArtifactDir = filepath.Join(cfg.DataDir, "artifacts")
		}
	}
	if artifactDir != "" {
		cfg.ArtifactDir = artifactDir
	}
	socketPath := findConfigValue(lines, "socket_path")
	if value := findConfigValue(lines, "run_dir"); value != "" {
		cfg.RunDir = value
		if socketPath == "" {
			cfg.SocketPath = filepath.Join(cfg.RunDir, "agentlabd.sock")
		}
	}
	if socketPath != "" {
		cfg.SocketPath = socketPath
	}
}

func checkBridge(ctx context.Context, bridge string, expectedHost string, subnet string) initCheck {
	check := initCheck{Name: "bridge_" + bridge}
	if _, err := exec.LookPath("ip"); err != nil {
		check.Status = "error"
		check.Detail = "ip command not found"
		return check
	}
	cmd := exec.CommandContext(ctx, "ip", "-4", "-o", "addr", "show", "dev", bridge)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		if strings.Contains(msg, "does not exist") {
			check.Status = "missing"
			check.Detail = fmt.Sprintf("%s not found", bridge)
			return check
		}
		check.Status = "error"
		check.Detail = msg
		return check
	}
	addresses := parseIPAddresses(string(out))
	if len(addresses) == 0 {
		check.Status = "missing"
		check.Detail = fmt.Sprintf("%s has no IPv4 address", bridge)
		return check
	}
	if expectedHost != "" {
		found := false
		for _, addr := range addresses {
			host := strings.SplitN(addr, "/", 2)[0]
			if host == expectedHost {
				found = true
				break
			}
		}
		if !found {
			check.Status = "missing"
			check.Detail = fmt.Sprintf("addr=%s (expected %s)", strings.Join(addresses, ", "), expectedHost)
			return check
		}
	}
	if subnet != "" {
		if _, ipnet, err := net.ParseCIDR(subnet); err == nil && ipnet != nil {
			match := false
			for _, addr := range addresses {
				host := strings.SplitN(addr, "/", 2)[0]
				ip := net.ParseIP(host)
				if ip != nil && ipnet.Contains(ip) {
					match = true
					break
				}
			}
			if !match {
				check.Status = "missing"
				check.Detail = fmt.Sprintf("addr=%s (outside %s)", strings.Join(addresses, ", "), subnet)
				return check
			}
		}
	}
	check.Status = "ok"
	check.Detail = fmt.Sprintf("addr=%s", strings.Join(addresses, ", "))
	return check
}

func checkIPForward() initCheck {
	check := initCheck{Name: "ip_forward"}
	data, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		check.Status = "error"
		check.Detail = err.Error()
		return check
	}
	value := strings.TrimSpace(string(data))
	if value == "1" {
		check.Status = "ok"
		check.Detail = "net.ipv4.ip_forward=1"
		return check
	}
	check.Status = "missing"
	check.Detail = fmt.Sprintf("net.ipv4.ip_forward=%s", value)
	return check
}

func checkNFTables(ctx context.Context) initCheck {
	check := initCheck{Name: "nftables"}
	if _, err := exec.LookPath("nft"); err != nil {
		check.Status = "missing"
		check.Detail = "nft not found"
		return check
	}
	if _, err := exec.LookPath("systemctl"); err == nil {
		cmd := exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", "agentlab-nftables.service")
		if err := cmd.Run(); err == nil {
			check.Status = "ok"
			check.Detail = "agentlab-nftables.service active"
			return check
		}
	}
	ok, detail := checkNFTablesTables(ctx)
	check.Status = ok
	check.Detail = detail
	return check
}

func checkNFTablesTables(ctx context.Context) (string, string) {
	if ok, detail := checkNFTTable(ctx, "inet", "agentlab"); !ok {
		if strings.Contains(detail, "permission denied") {
			return "error", detail
		}
		return "missing", detail
	}
	if ok, detail := checkNFTTable(ctx, "ip", "agentlab_nat"); !ok {
		if strings.Contains(detail, "permission denied") {
			return "error", detail
		}
		return "missing", detail
	}
	return "ok", "agentlab nftables tables present"
}

func checkNFTTable(ctx context.Context, family string, table string) (bool, string) {
	cmd := exec.CommandContext(ctx, "nft", "list", "table", family, table)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, ""
	}
	msg := strings.TrimSpace(string(out))
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "permission denied") || strings.Contains(lower, "operation not permitted") {
		return false, "permission denied (run as root)"
	}
	if msg == "" {
		msg = err.Error()
	}
	return false, fmt.Sprintf("missing %s/%s (%s)", family, table, msg)
}

func checkSnippetsDir(dir string, storage string) initCheck {
	check := initCheck{Name: "snippets_dir"}
	if strings.TrimSpace(dir) == "" {
		check.Status = "missing"
		check.Detail = "snippets_dir not set"
		return check
	}
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			check.Status = "missing"
			check.Detail = fmt.Sprintf("%s not found", dir)
			return check
		}
		check.Status = "error"
		check.Detail = err.Error()
		return check
	}
	if !info.IsDir() {
		check.Status = "error"
		check.Detail = fmt.Sprintf("%s is not a directory", dir)
		return check
	}
	check.Status = "ok"
	if storage != "" {
		check.Detail = fmt.Sprintf("dir=%s storage=%s", dir, storage)
	} else {
		check.Detail = fmt.Sprintf("dir=%s", dir)
	}
	return check
}

func checkProfiles(state initState) initCheck {
	check := initCheck{Name: "profiles"}
	if state.ProfilesErr != nil {
		if errors.Is(state.ProfilesErr, os.ErrNotExist) {
			check.Status = "missing"
		} else {
			check.Status = "error"
		}
		check.Detail = state.ProfilesErr.Error()
		return check
	}
	if len(state.Profiles) == 0 {
		check.Status = "missing"
		check.Detail = fmt.Sprintf("no profiles found in %s", state.Config.ProfilesDir)
		return check
	}
	names := make([]string, 0, len(state.Profiles))
	for name := range state.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	check.Status = "ok"
	check.Detail = fmt.Sprintf("%d profiles (%s)", len(names), strings.Join(names, ", "))
	return check
}

var errQmNotFound = errors.New("qm not found")

func validateTemplates(ctx context.Context, templateIDs []int) (map[int]error, error) {
	results := make(map[int]error, len(templateIDs))
	if len(templateIDs) == 0 {
		return results, nil
	}
	if _, err := exec.LookPath("qm"); err != nil {
		return nil, errQmNotFound
	}
	backend := proxmox.ShellBackend{CommandTimeout: 15 * time.Second}
	for _, id := range templateIDs {
		validateCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := backend.ValidateTemplate(validateCtx, proxmox.VMID(id))
		cancel()
		results[id] = err
	}
	return results, nil
}

func checkTemplates(ctx context.Context, templateIDs []int) initCheck {
	check := initCheck{Name: "templates"}
	if len(templateIDs) == 0 {
		check.Status = "skipped"
		check.Detail = "no template_vmid entries"
		return check
	}
	results, err := validateTemplates(ctx, templateIDs)
	if err != nil {
		if errors.Is(err, errQmNotFound) {
			check.Status = "missing"
			check.Detail = "qm not found"
			return check
		}
		check.Status = "error"
		check.Detail = err.Error()
		return check
	}
	var failures []string
	var okIDs []string
	for _, id := range templateIDs {
		if results[id] == nil {
			okIDs = append(okIDs, strconv.Itoa(id))
			continue
		}
		failures = append(failures, fmt.Sprintf("%d: %v", id, results[id]))
	}
	if len(failures) == 0 {
		check.Status = "ok"
		check.Detail = fmt.Sprintf("templates ok (%s)", strings.Join(okIDs, ", "))
		return check
	}
	check.Status = "error"
	check.Detail = strings.Join(failures, "; ")
	return check
}

func listenHost(listen string) string {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return ""
	}
	host = strings.Trim(host, "[]")
	switch host {
	case "", "0.0.0.0", "::":
		return ""
	default:
		return host
	}
}

func findCheckStatus(checks []initCheck, name string) string {
	for _, check := range checks {
		if check.Name == name {
			return check.Status
		}
	}
	return ""
}

func parseIPAddresses(output string) []string {
	lines := strings.Split(output, "\n")
	addresses := []string{}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		if fields[2] != "inet" {
			continue
		}
		addresses = append(addresses, fields[3])
	}
	return addresses
}

func runScript(ctx context.Context, path string, args []string, jsonOutput bool) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	if jsonOutput {
		output, err := cmd.CombinedOutput()
		if err != nil {
			return formatCommandError(fmt.Sprintf("command %s failed", filepath.Base(path)), output, err)
		}
		return nil
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func formatCommandError(prefix string, output []byte, err error) error {
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	if len(trimmed) > 2000 {
		trimmed = trimmed[:2000] + "..."
	}
	return fmt.Errorf("%s: %s", prefix, trimmed)
}

func joinInts(values []int) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ", ")
}

func resolveInitAssets(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		root, err := filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
		if hasInitAssets(root) {
			return root, nil
		}
		return "", fmt.Errorf("assets not found at %s", root)
	}
	if cwd, err := os.Getwd(); err == nil {
		if root := findInitAssetsUpward(cwd); root != "" {
			return root, nil
		}
	}
	if exe, err := os.Executable(); err == nil {
		if root := findInitAssetsUpward(filepath.Dir(exe)); root != "" {
			return root, nil
		}
	}
	return "", fmt.Errorf("unable to locate agentlab assets; use --assets to specify the repo root")
}

func findInitAssetsUpward(start string) string {
	dir := start
	for {
		if hasInitAssets(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func hasInitAssets(root string) bool {
	if root == "" {
		return false
	}
	paths := []string{
		filepath.Join(root, "scripts", "net", "setup_vmbr1.sh"),
		filepath.Join(root, "scripts", "net", "apply.sh"),
		filepath.Join(root, "scripts", "create_template.sh"),
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}

func executableDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

func prependPathEnv(env []string, dir string) []string {
	if dir == "" {
		return env
	}
	var out []string
	found := false
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			found = true
			pathValue := strings.TrimPrefix(entry, "PATH=")
			if !strings.Contains(pathValue, dir) {
				pathValue = dir + string(os.PathListSeparator) + pathValue
			}
			out = append(out, "PATH="+pathValue)
			continue
		}
		out = append(out, entry)
	}
	if !found {
		out = append(out, "PATH="+dir)
	}
	return out
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
	return strings.Contains(lower, fmt.Sprintf("tcp %d", port))
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
