// ABOUTME: Remote bootstrap orchestration for provisioning a Proxmox host over SSH.
// ABOUTME: Uploads assets, configures networking, installs agentlabd, and writes local client config.

package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBootstrapSSHPort = 22
)

type bootstrapOptions struct {
	host              string
	sshUser           string
	sshPort           int
	identity          string
	assetsDir         string
	agentlabBin       string
	agentlabdBin      string
	agentlabURL       string
	agentlabdURL      string
	releaseURL        string
	controlPort       int
	controlToken      string
	rotateToken       bool
	tailscaleServe    bool
	noTailscaleServe  bool
	tailscaleAuthKey  string
	tailscaleHostname string
	tailscaleAdmin    *tailscaleAdminConfig
	force             bool
	keepTemp          bool
	verbose           bool
	jsonOutput        bool
	requestTimeout    time.Duration
	acceptNewHostKey  bool
}

type bootstrapBinaries struct {
	mode         string
	agentlab     string
	agentlabd    string
	agentlabURL  string
	agentlabdURL string
}

type bootstrapStep struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type bootstrapOutput struct {
	Host         string             `json:"host"`
	Endpoint     string             `json:"endpoint"`
	ConfigPath   string             `json:"config_path"`
	BackupPath   string             `json:"backup_path,omitempty"`
	Steps        []bootstrapStep    `json:"steps"`
	Warnings     []string           `json:"warnings,omitempty"`
	AssetsDir    string             `json:"assets_dir"`
	RemoteDir    string             `json:"remote_dir"`
	TailnetRoute *tailnetRouteCheck `json:"tailnet_route,omitempty"`
}

func runBootstrapCommand(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("bootstrap")
	opts := base
	opts.bind(fs)
	bootstrap := bootstrapOptions{
		sshUser:          "root",
		sshPort:          defaultBootstrapSSHPort,
		controlPort:      defaultControlPort,
		acceptNewHostKey: true,
		requestTimeout:   opts.timeout,
		jsonOutput:       opts.jsonOutput,
		tailscaleAdmin:   opts.tailscaleAdmin,
	}
	var tailscaleTailnet string
	var tailscaleAPIKey string
	var tailscaleOAuthClientID string
	var tailscaleOAuthClientSecret string
	var tailscaleOAuthScopes string
	var help bool
	fs.StringVar(&bootstrap.host, "host", "", "SSH host (required)")
	fs.StringVar(&bootstrap.sshUser, "ssh-user", bootstrap.sshUser, "SSH username")
	fs.IntVar(&bootstrap.sshPort, "ssh-port", bootstrap.sshPort, "SSH port")
	fs.StringVar(&bootstrap.identity, "identity", "", "SSH identity file")
	fs.StringVar(&bootstrap.assetsDir, "assets", "", "path to agentlab assets (scripts/)")
	fs.StringVar(&bootstrap.agentlabBin, "agentlab-bin", "", "path to agentlab linux binary to upload")
	fs.StringVar(&bootstrap.agentlabdBin, "agentlabd-bin", "", "path to agentlabd linux binary to upload")
	fs.StringVar(&bootstrap.agentlabURL, "agentlab-url", "", "download URL for agentlab linux binary")
	fs.StringVar(&bootstrap.agentlabdURL, "agentlabd-url", "", "download URL for agentlabd linux binary")
	fs.StringVar(&bootstrap.releaseURL, "release-url", "", "base URL for downloading linux binaries")
	fs.IntVar(&bootstrap.controlPort, "control-port", bootstrap.controlPort, "control plane port")
	fs.StringVar(&bootstrap.controlToken, "control-token", "", "control plane auth token (optional)")
	fs.BoolVar(&bootstrap.rotateToken, "rotate-control-token", false, "rotate control auth token")
	fs.BoolVar(&bootstrap.tailscaleServe, "tailscale-serve", false, "force tailscale serve publishing")
	fs.BoolVar(&bootstrap.noTailscaleServe, "no-tailscale-serve", false, "disable tailscale serve publishing")
	fs.StringVar(&bootstrap.tailscaleAuthKey, "tailscale-authkey", "", "tailscale auth key for unattended tailscale up")
	fs.StringVar(&bootstrap.tailscaleHostname, "tailscale-hostname", "", "tailscale hostname (when using authkey)")
	fs.StringVar(&tailscaleTailnet, "tailscale-tailnet", "", "tailscale tailnet name for admin API (optional)")
	fs.StringVar(&tailscaleAPIKey, "tailscale-api-key", "", "tailscale admin API key (optional)")
	fs.StringVar(&tailscaleOAuthClientID, "tailscale-oauth-client-id", "", "tailscale oauth client id (optional)")
	fs.StringVar(&tailscaleOAuthClientSecret, "tailscale-oauth-client-secret", "", "tailscale oauth client secret (optional)")
	fs.StringVar(&tailscaleOAuthScopes, "tailscale-oauth-scopes", "", "tailscale oauth scopes (comma/space separated)")
	fs.BoolVar(&bootstrap.force, "force", false, "force overwrite of managed network files")
	fs.BoolVar(&bootstrap.keepTemp, "keep-temp", false, "keep remote bootstrap bundle directory")
	fs.BoolVar(&bootstrap.verbose, "verbose", false, "show remote command output")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printBootstrapUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	if bootstrap.tailscaleServe && bootstrap.noTailscaleServe {
		return fmt.Errorf("--tailscale-serve and --no-tailscale-serve are mutually exclusive")
	}
	bootstrap.host = strings.TrimSpace(bootstrap.host)
	if bootstrap.host == "" {
		if !opts.jsonOutput {
			printBootstrapUsage()
		}
		return fmt.Errorf("host is required")
	}
	if bootstrap.sshPort <= 0 || bootstrap.sshPort > 65535 {
		return fmt.Errorf("ssh-port must be between 1 and 65535")
	}
	if bootstrap.controlPort <= 0 || bootstrap.controlPort > 65535 {
		return fmt.Errorf("control-port must be between 1 and 65535")
	}
	if cfg, ok := tailscaleAdminConfigFromFlags(tailscaleTailnet, tailscaleAPIKey, tailscaleOAuthClientID, tailscaleOAuthClientSecret, tailscaleOAuthScopes); ok {
		bootstrap.tailscaleAdmin = mergeTailscaleAdminConfig(bootstrap.tailscaleAdmin, cfg)
	}
	if err := validateTailscaleAdminConfig(bootstrap.tailscaleAdmin); err != nil {
		return err
	}

	assetsRoot, err := resolveBootstrapAssets(bootstrap.assetsDir)
	if err != nil {
		return err
	}
	binaries, err := resolveBootstrapBinaries(assetsRoot, bootstrap)
	if err != nil {
		return err
	}
	plan := buildBootstrapPlan(bootstrap, binaries)
	if !opts.jsonOutput {
		printBootstrapPlan(plan)
	}

	sshClient, err := newBootstrapSSHClient(bootstrap)
	if err != nil {
		return err
	}

	bundlePath, err := createBootstrapBundle(assetsRoot, binaries)
	if err != nil {
		return err
	}
	defer os.Remove(bundlePath)

	remoteBase := fmt.Sprintf("/tmp/agentlab-bootstrap-%d", time.Now().Unix())
	remoteBundle := filepath.Join(remoteBase, "bundle.tgz")
	remoteRoot := filepath.Join(remoteBase, "agentlab-bootstrap")
	steps := []bootstrapStep{}
	warnings := []string{}
	var routeCheck *tailnetRouteCheck
	backupPath := ""

	if _, err := sshClient.run(ctx, shellJoin([]string{"mkdir", "-p", remoteBase}), nil); err != nil {
		return err
	}
	steps = append(steps, bootstrapStep{Name: "prepare_remote_dir", Status: "ok", Detail: remoteBase})

	if err := sshClient.upload(ctx, bundlePath, remoteBundle); err != nil {
		return err
	}
	steps = append(steps, bootstrapStep{Name: "upload_bundle", Status: "ok"})

	if _, err := sshClient.run(ctx, shellJoin([]string{"tar", "-xzf", remoteBundle, "-C", remoteBase}), nil); err != nil {
		return err
	}
	steps = append(steps, bootstrapStep{Name: "extract_bundle", Status: "ok"})

	if binaries.mode == "download" {
		if _, err := sshClient.run(ctx, buildRemoteDownloadCommand(remoteRoot, binaries), nil); err != nil {
			return err
		}
		steps = append(steps, bootstrapStep{Name: "download_binaries", Status: "ok"})
	}

	setupArgs := append([]string{filepath.Join(remoteRoot, "scripts/net/setup_vmbr1.sh"), "--apply"}, boolFlag("--force", bootstrap.force)...)
	if _, err := sshClient.run(ctx, sudoCommand(bootstrap, shellJoin(setupArgs)), nil); err != nil {
		return err
	}
	steps = append(steps, bootstrapStep{Name: "configure_vmbr1", Status: "ok"})

	nftArgs := append([]string{filepath.Join(remoteRoot, "scripts/net/apply.sh"), "--apply"}, boolFlag("--force", bootstrap.force)...)
	if _, err := sshClient.run(ctx, sudoCommand(bootstrap, shellJoin(nftArgs)), nil); err != nil {
		return err
	}
	steps = append(steps, bootstrapStep{Name: "configure_nftables", Status: "ok"})

	if bootstrap.tailscaleAuthKey != "" {
		cmdArgs := []string{filepath.Join(remoteRoot, "scripts/net/setup_tailscale_router.sh"), "--apply", "--authkey", bootstrap.tailscaleAuthKey}
		if strings.TrimSpace(bootstrap.tailscaleHostname) != "" {
			cmdArgs = append(cmdArgs, "--hostname", strings.TrimSpace(bootstrap.tailscaleHostname))
		}
		if _, err := sshClient.run(ctx, sudoCommand(bootstrap, shellJoin(cmdArgs)), nil); err != nil {
			return err
		}
		steps = append(steps, bootstrapStep{Name: "configure_tailscale_router", Status: "ok"})
	}

	backupOut, err := sshClient.run(ctx, sudoCommand(bootstrap, buildConfigBackupCommand()), nil)
	if err != nil {
		return err
	}
	if parsed := parseBackupPath(backupOut); parsed != "" {
		backupPath = parsed
		steps = append(steps, bootstrapStep{Name: "backup_config", Status: "ok", Detail: backupPath})
	} else {
		steps = append(steps, bootstrapStep{Name: "backup_config", Status: "skipped", Detail: "no existing config"})
	}

	installArgs := []string{filepath.Join(remoteRoot, "scripts/install_host.sh"), "--enable-remote-control", "--control-port", strconv.Itoa(bootstrap.controlPort)}
	if strings.TrimSpace(bootstrap.controlToken) != "" {
		installArgs = append(installArgs, "--control-token", strings.TrimSpace(bootstrap.controlToken))
	}
	if bootstrap.rotateToken {
		installArgs = append(installArgs, "--rotate-control-token")
	}
	if bootstrap.tailscaleServe {
		installArgs = append(installArgs, "--tailscale-serve")
	}
	if bootstrap.noTailscaleServe {
		installArgs = append(installArgs, "--no-tailscale-serve")
	}
	if _, err := sshClient.run(ctx, sudoCommand(bootstrap, shellJoin(installArgs)), nil); err != nil {
		return err
	}
	steps = append(steps, bootstrapStep{Name: "install_agentlab", Status: "ok"})

	initOut, err := sshClient.run(ctx, sudoCommand(bootstrap, shellJoin([]string{"agentlab", "init", "--json"})), nil)
	if err != nil {
		return err
	}
	endpoint, token, err := parseInitConnect(initOut)
	if err != nil {
		return err
	}

	configPath, err := writeBootstrapClientConfig(endpoint, token)
	if err != nil {
		return err
	}
	steps = append(steps, bootstrapStep{Name: "write_client_config", Status: "ok", Detail: configPath})

	client := newAPIClient(clientOptions{Endpoint: endpoint, Token: token}, bootstrap.requestTimeout)
	hostInfo, err := fetchHostInfo(ctx, client)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("host info unavailable: %v", err))
	}
	agentSubnet := agentSubnetFromHost(hostInfo)
	approvalStep, approvalWarning := maybeApproveTailscaleRoutes(ctx, bootstrap, hostInfo, agentSubnet)
	if approvalStep.Name != "" {
		steps = append(steps, approvalStep)
	}
	if approvalWarning != "" {
		warnings = append(warnings, approvalWarning)
	}
	check := checkTailnetRoute(ctx, agentSubnet)
	routeCheck = &check

	if err := verifyEndpoint(ctx, endpoint, token, bootstrap.requestTimeout); err != nil {
		warnings = append(warnings, fmt.Sprintf("control plane not reachable yet: %v", err))
		steps = append(steps, bootstrapStep{Name: "verify_control_plane", Status: "warn"})
	} else {
		steps = append(steps, bootstrapStep{Name: "verify_control_plane", Status: "ok"})
	}

	if !bootstrap.keepTemp {
		if _, err := sshClient.run(ctx, shellJoin([]string{"rm", "-rf", remoteBase}), nil); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to remove remote temp dir %s", remoteBase))
		} else {
			steps = append(steps, bootstrapStep{Name: "cleanup_remote", Status: "ok"})
		}
	}

	if opts.jsonOutput {
		out := bootstrapOutput{
			Host:         bootstrap.host,
			Endpoint:     endpoint,
			ConfigPath:   configPath,
			BackupPath:   backupPath,
			Steps:        steps,
			Warnings:     warnings,
			AssetsDir:    assetsRoot,
			RemoteDir:    remoteBase,
			TailnetRoute: routeCheck,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(out)
	}

	printBootstrapSummary(endpoint, configPath, backupPath, warnings, routeCheck)
	return nil
}

func buildBootstrapPlan(opts bootstrapOptions, binaries bootstrapBinaries) []string {
	plan := []string{}
	plan = append(plan, "Upload bootstrap bundle over SSH")
	if binaries.mode == "download" {
		plan = append(plan, "Download Linux binaries on the host")
	}
	plan = append(plan, "Configure vmbr1 bridge and enable IP forwarding")
	plan = append(plan, "Install nftables NAT + egress/tailnet blocks")
	if opts.tailscaleAuthKey != "" {
		plan = append(plan, "Configure tailscale subnet routing")
	}
	if opts.tailscaleAdmin != nil && opts.tailscaleAdmin.hasCredentials() {
		plan = append(plan, "Approve tailscale subnet route via admin API")
	}
	plan = append(plan, "Backup /etc/agentlab/config.yaml if present")
	plan = append(plan, "Install agentlabd + agentlab, enable remote control")
	plan = append(plan, "Fetch control endpoint and write local client config")
	return plan
}

func printBootstrapPlan(plan []string) {
	fmt.Fprintln(os.Stdout, "Bootstrap plan:")
	for _, step := range plan {
		fmt.Fprintf(os.Stdout, "- %s\n", step)
	}
}

func printBootstrapSummary(endpoint, configPath, backupPath string, warnings []string, routeCheck *tailnetRouteCheck) {
	fmt.Fprintln(os.Stdout, "Bootstrap complete")
	fmt.Fprintf(os.Stdout, "Endpoint: %s\n", endpoint)
	fmt.Fprintf(os.Stdout, "Client config: %s\n", configPath)
	if backupPath != "" {
		fmt.Fprintf(os.Stdout, "Config backup: %s\n", backupPath)
	}
	for _, warning := range warnings {
		fmt.Fprintf(os.Stdout, "Warning: %s\n", warning)
	}
	if routeCheck != nil {
		fmt.Fprintf(os.Stdout, "Tailnet route: %s\n", formatTailnetRouteCheck(*routeCheck))
	}
}

func resolveBootstrapAssets(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		root, err := filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
		if hasBootstrapAssets(root) {
			return root, nil
		}
		return "", fmt.Errorf("assets not found at %s", root)
	}
	cwd, err := os.Getwd()
	if err == nil {
		if root := findAssetsUpward(cwd); root != "" {
			return root, nil
		}
	}
	if exe, err := os.Executable(); err == nil {
		if root := findAssetsUpward(filepath.Dir(exe)); root != "" {
			return root, nil
		}
	}
	return "", fmt.Errorf("unable to locate agentlab assets; use --assets to specify the repo root")
}

func findAssetsUpward(start string) string {
	dir := start
	for {
		if hasBootstrapAssets(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func hasBootstrapAssets(root string) bool {
	if root == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(root, "scripts", "install_host.sh")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(root, "scripts", "net", "setup_vmbr1.sh")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(root, "scripts", "net", "apply.sh")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "agentlab")); err != nil {
		return false
	}
	return true
}

func resolveBootstrapBinaries(root string, opts bootstrapOptions) (bootstrapBinaries, error) {
	binaries := bootstrapBinaries{}
	if strings.TrimSpace(opts.agentlabBin) != "" {
		path, err := filepath.Abs(strings.TrimSpace(opts.agentlabBin))
		if err != nil {
			return binaries, err
		}
		if _, err := os.Stat(path); err != nil {
			return binaries, fmt.Errorf("agentlab binary not found: %s", path)
		}
		binaries.agentlab = path
	}
	if strings.TrimSpace(opts.agentlabdBin) != "" {
		path, err := filepath.Abs(strings.TrimSpace(opts.agentlabdBin))
		if err != nil {
			return binaries, err
		}
		if _, err := os.Stat(path); err != nil {
			return binaries, fmt.Errorf("agentlabd binary not found: %s", path)
		}
		binaries.agentlabd = path
	}
	if binaries.agentlab == "" && binaries.agentlabd == "" {
		candidate := filepath.Join(root, "dist", "agentlab_linux_amd64")
		if _, err := os.Stat(candidate); err == nil {
			binaries.agentlab = candidate
		}
		candidate = filepath.Join(root, "dist", "agentlabd_linux_amd64")
		if _, err := os.Stat(candidate); err == nil {
			binaries.agentlabd = candidate
		}
	}
	if binaries.agentlab != "" || binaries.agentlabd != "" {
		if binaries.agentlab == "" || binaries.agentlabd == "" {
			return binaries, fmt.Errorf("both agentlab and agentlabd binaries are required for upload")
		}
		binaries.mode = "upload"
		return binaries, nil
	}

	binaries.agentlabURL = strings.TrimSpace(opts.agentlabURL)
	binaries.agentlabdURL = strings.TrimSpace(opts.agentlabdURL)
	if binaries.agentlabURL == "" || binaries.agentlabdURL == "" {
		base := strings.TrimSpace(opts.releaseURL)
		if base != "" {
			base = strings.TrimRight(base, "/")
			if binaries.agentlabURL == "" {
				binaries.agentlabURL = base + "/agentlab_linux_amd64"
			}
			if binaries.agentlabdURL == "" {
				binaries.agentlabdURL = base + "/agentlabd_linux_amd64"
			}
		}
	}
	if binaries.agentlabURL == "" || binaries.agentlabdURL == "" {
		return binaries, fmt.Errorf("linux binaries not found; build dist/ or pass --release-url/--agentlab-url/--agentlabd-url")
	}
	binaries.mode = "download"
	return binaries, nil
}

func createBootstrapBundle(root string, binaries bootstrapBinaries) (string, error) {
	bundle, err := os.CreateTemp("", "agentlab-bootstrap-*.tgz")
	if err != nil {
		return "", err
	}
	defer bundle.Close()

	gz := gzip.NewWriter(bundle)
	defer gz.Close()
	writer := tar.NewWriter(gz)
	defer writer.Close()

	prefix := "agentlab-bootstrap"
	if err := addTarDir(writer, prefix, 0o755); err != nil {
		return "", err
	}
	paths := []string{
		filepath.Join(root, "scripts"),
		filepath.Join(root, "skills", "agentlab"),
	}
	for _, path := range paths {
		if err := addTarTree(writer, root, path, prefix); err != nil {
			return "", err
		}
	}

	if binaries.mode == "upload" {
		destDir := filepath.Join(prefix, "dist")
		if err := addTarDir(writer, destDir, 0o755); err != nil {
			return "", err
		}
		if err := addTarFile(writer, binaries.agentlabd, filepath.Join(destDir, "agentlabd_linux_amd64")); err != nil {
			return "", err
		}
		if err := addTarFile(writer, binaries.agentlab, filepath.Join(destDir, "agentlab_linux_amd64")); err != nil {
			return "", err
		}
	}

	if err := writer.Close(); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	if err := bundle.Close(); err != nil {
		return "", err
	}
	return bundle.Name(), nil
}

func addTarTree(writer *tar.Writer, root, path, prefix string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return filepath.WalkDir(path, func(entry string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, entry)
		if err != nil {
			return err
		}
		tarPath := filepath.ToSlash(filepath.Join(prefix, rel))
		if d.IsDir() {
			return addTarDir(writer, tarPath, 0o755)
		}
		return addTarFile(writer, entry, tarPath)
	})
}

func addTarDir(writer *tar.Writer, name string, mode int64) error {
	name = filepath.ToSlash(name)
	if !strings.HasSuffix(name, "/") {
		name += "/"
	}
	hdr := &tar.Header{
		Name:     name,
		Mode:     mode,
		Typeflag: tar.TypeDir,
		ModTime:  time.Now(),
	}
	return writer.WriteHeader(hdr)
}

func addTarFile(writer *tar.Writer, src, dest string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = filepath.ToSlash(dest)
	if err := writer.WriteHeader(hdr); err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		file, err := os.Open(src)
		if err != nil {
			return err
		}
		defer file.Close()
		if _, err := io.Copy(writer, file); err != nil {
			return err
		}
	}
	return nil
}

type bootstrapSSHClient struct {
	target  string
	args    []string
	verbose bool
}

func newBootstrapSSHClient(opts bootstrapOptions) (*bootstrapSSHClient, error) {
	user, host := splitUserHost(opts.host)
	if strings.TrimSpace(opts.sshUser) != "" {
		user = strings.TrimSpace(opts.sshUser)
	}
	if user == "" {
		user = "root"
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, fmt.Errorf("host is required")
	}
	target := host
	if user != "" {
		target = user + "@" + host
	}
	args := []string{"-o", "BatchMode=yes"}
	if opts.acceptNewHostKey {
		args = append(args, "-o", "StrictHostKeyChecking=accept-new")
	}
	identity := strings.TrimSpace(opts.identity)
	if identity != "" {
		args = append(args, "-i", identity)
	}
	if opts.sshPort != defaultBootstrapSSHPort {
		args = append(args, "-p", strconv.Itoa(opts.sshPort))
	}
	return &bootstrapSSHClient{target: target, args: args, verbose: opts.verbose}, nil
}

func (c *bootstrapSSHClient) run(ctx context.Context, command string, stdin io.Reader) (string, error) {
	if c == nil {
		return "", errors.New("ssh client is nil")
	}
	args := append([]string{}, c.args...)
	args = append(args, c.target, command)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String() + stderr.String(), err
	}
	output := stdout.String() + stderr.String()
	if c.verbose && output != "" {
		fmt.Fprint(os.Stdout, output)
	}
	return output, nil
}

func (c *bootstrapSSHClient) upload(ctx context.Context, localPath, remotePath string) error {
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer file.Close()
	cmd := fmt.Sprintf("cat > %s", shellQuote(remotePath))
	_, err = c.run(ctx, cmd, file)
	return err
}

func splitUserHost(raw string) (string, string) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", ""
	}
	idx := strings.LastIndex(value, "@")
	if idx == -1 {
		return "", value
	}
	return value[:idx], value[idx+1:]
}

func buildRemoteDownloadCommand(remoteRoot string, binaries bootstrapBinaries) string {
	dest := filepath.Join(remoteRoot, "dist")
	script := strings.Join([]string{
		"set -euo pipefail",
		fmt.Sprintf("mkdir -p %s", shellQuote(dest)),
		"if command -v curl >/dev/null 2>&1; then",
		"  dl() { curl -fsSL \"$1\" -o \"$2\"; }",
		"elif command -v wget >/dev/null 2>&1; then",
		"  dl() { wget -qO \"$2\" \"$1\"; }",
		"else",
		"  echo 'curl or wget is required' >&2; exit 1;",
		"fi",
		fmt.Sprintf("dl %s %s", shellQuote(binaries.agentlabdURL), shellQuote(filepath.Join(dest, "agentlabd_linux_amd64"))),
		fmt.Sprintf("dl %s %s", shellQuote(binaries.agentlabURL), shellQuote(filepath.Join(dest, "agentlab_linux_amd64"))),
		fmt.Sprintf("chmod +x %s %s", shellQuote(filepath.Join(dest, "agentlabd_linux_amd64")), shellQuote(filepath.Join(dest, "agentlab_linux_amd64"))),
	}, "\n")
	return "sh -c " + shellQuote(script)
}

func sudoCommand(opts bootstrapOptions, command string) string {
	if !needsSudo(opts) {
		return command
	}
	return "sudo -n " + command
}

func needsSudo(opts bootstrapOptions) bool {
	user, _ := splitUserHost(opts.host)
	if strings.TrimSpace(opts.sshUser) != "" {
		user = strings.TrimSpace(opts.sshUser)
	}
	if user == "" {
		user = "root"
	}
	return user != "root"
}

func buildConfigBackupCommand() string {
	script := strings.Join([]string{
		"set -euo pipefail",
		"cfg=/etc/agentlab/config.yaml",
		"if [ -f \"$cfg\" ]; then",
		"  ts=$(date -u +%Y%m%dT%H%M%SZ)",
		"  backup=\"${cfg}.bak.${ts}\"",
		"  cp -a \"$cfg\" \"$backup\"",
		"  echo \"BACKUP=${backup}\"",
		"fi",
	}, "\n")
	return "sh -c " + shellQuote(script)
}

func parseBackupPath(output string) string {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "BACKUP=") {
			return strings.TrimPrefix(line, "BACKUP=")
		}
	}
	return ""
}

func parseInitConnect(output string) (string, string, error) {
	var report initReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		return "", "", fmt.Errorf("failed to parse init report: %w", err)
	}
	endpoint, token := parseConnectCommand(report.ConnectCommand)
	if endpoint == "" || token == "" {
		return "", "", fmt.Errorf("control endpoint not configured; run agentlab init --apply on host")
	}
	return endpoint, token, nil
}

func parseConnectCommand(command string) (string, string) {
	fields := strings.Fields(command)
	var endpoint string
	var token string
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "--endpoint":
			if i+1 < len(fields) {
				endpoint = fields[i+1]
			}
		case "--token":
			if i+1 < len(fields) {
				token = fields[i+1]
			}
		}
	}
	return strings.TrimSpace(endpoint), strings.TrimSpace(token)
}

func writeBootstrapClientConfig(endpoint, token string) (string, error) {
	endpoint, err := normalizeEndpoint(endpoint)
	if err != nil {
		return "", err
	}
	path, err := clientConfigPath()
	if err != nil {
		return "", err
	}
	cfg := clientConfig{Endpoint: endpoint, Token: token}
	if err := writeClientConfig(path, cfg); err != nil {
		return "", err
	}
	return path, nil
}

func verifyEndpoint(ctx context.Context, endpoint, token string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := newAPIClient(clientOptions{Endpoint: endpoint, Token: token}, timeout)
	_, err := client.doJSON(ctx, "GET", "/v1/status", nil)
	return err
}

func shellJoin(args []string) string {
	if len(args) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func boolFlag(flag string, enabled bool) []string {
	if !enabled {
		return nil
	}
	return []string{flag}
}
