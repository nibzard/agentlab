// ABOUTME: Main CLI entry point for the agentlab command-line tool.
// ABOUTME: Provides commands for job management, sandbox control, workspace operations, SSH access, and log viewing.

// Package main implements the agentlab CLI for controlling agentlabd.
//
// The agentlab CLI communicates with the agentlabd daemon over a Unix socket
// to manage sandboxes, jobs, workspaces, and view logs.
//
// # Global Flags
//
// The following global flags are available for all commands:
//
//	--endpoint URL  Control plane HTTP endpoint (http(s)://host:port)
//	--token TOKEN   Control plane auth token (Authorization: Bearer)
//	--socket PATH   Path to agentlabd socket (default /run/agentlab/agentlabd.sock)
//	--json          Output JSON instead of formatted text
//	--timeout       Request timeout (e.g., 30s, 2m)
//	--version       Print version and exit
//
// # Usage Examples
//
//	Run a job:
//	  agentlab job run --repo https://github.com/user/repo --task "test all" --profile yolo-ephemeral
//
//	List sandboxes:
//	  agentlab sandbox list
//
//	SSH into a sandbox:
//	  agentlab ssh 1001
//
//	View logs:
//	  agentlab logs 1001 --follow
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/buildinfo"
	"github.com/agentlab/agentlab/internal/clicontract"
	"net/url"
	"strconv"
)

type globalOptions struct {
	socketPath        string
	endpoint          string
	token             string
	jsonOutput        bool
	showVersion       bool
	timeout           time.Duration
	tailscaleAdmin    *tailscaleAdminConfig
	allowInsecureHTTP bool
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "__complete" {
		for _, candidate := range clicontract.Candidates(os.Args[2:]) {
			fmt.Println(candidate)
		}
		return
	}
	opts, args, err := parseGlobal(os.Args[1:])
	if err != nil {
		if errors.Is(err, errHelp) {
			printUsage()
			return
		}
		msg, next, hints := describeError(err)
		if opts.jsonOutput {
			writeJSONError(os.Stdout, err)
		} else {
			printError(os.Stderr, msg, next, hints)
		}
		if errors.Is(err, errUsage) {
			if !opts.jsonOutput {
				if showUsageOnError(err) {
					printUsage()
				}
			}
			os.Exit(2)
		}
		os.Exit(1)
	}
	if opts.showVersion {
		fmt.Println(buildinfo.String())
		return
	}
	if len(args) == 0 {
		printUsage()
		return
	}
	if isHelpToken(args[0]) && args[0] != "help" {
		printUsage()
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	base := commonFlags{
		socketPath:        opts.socketPath,
		endpoint:          opts.endpoint,
		token:             opts.token,
		jsonOutput:        opts.jsonOutput,
		timeout:           opts.timeout,
		tailscaleAdmin:    opts.tailscaleAdmin,
		allowInsecureHTTP: opts.allowInsecureHTTP,
	}
	var commandErr error
	if path, wanted, err := clicontract.HelpPath(args); wanted {
		if err != nil {
			commandErr = newUsageError(err, false)
		} else {
			commandErr = runCatalogHelp(path)
		}
	} else {
		commandErr = dispatch(ctx, args, base)
	}
	if err := commandErr; err != nil {
		if errors.Is(err, errHelp) {
			return
		}
		msg, next, hints := describeError(err)
		if opts.jsonOutput {
			writeJSONError(os.Stdout, err)
		} else {
			printError(os.Stderr, msg, next, hints)
		}
		if errors.Is(err, errUsage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func parseGlobal(args []string) (opts globalOptions, remaining []string, parseErr error) {
	defer func() {
		global, _ := clicontract.ExtractGlobals(args)
		for i := 0; i < len(global); i++ {
			name, value, inline := strings.Cut(strings.TrimLeft(global[i], "-"), "=")
			if kind, ok := clicontract.Globals[name]; ok && kind != "bool" && !inline {
				i++
				continue
			}
			if name == "json" {
				if !inline {
					value = "true"
				}
				if v, err := strconv.ParseBool(value); err == nil {
					opts.jsonOutput = v
				}
			}
		}
	}()
	opts = globalOptions{socketPath: defaultSocketPath, timeout: defaultRequestTimeout}
	// Help and command discovery must remain usable when saved preferences
	// are corrupt. Validate their syntax without opening user configuration.
	globals, commands := clicontract.ExtractGlobals(args)
	if len(commands) > 0 && commands[0] == "help" {
		syntax := append(append([]string{}, globals...), "status")
		if err := clicontract.Validate(syntax); err == nil {
			return opts, commands, nil
		}
	}
	if _, wanted, helpErr := clicontract.HelpPath(commands); wanted && helpErr == nil {
		if err := clicontract.Validate(args); err == nil {
			return opts, commands, nil
		}
	}
	if len(commands) == 0 {
		for _, arg := range globals {
			if arg == "--help" || arg == "-h" {
				if err := clicontract.Validate(args); err == nil {
					return opts, nil, errHelp
				}
			}
		}
	}
	defaults, err := loadDefaults()
	if err != nil {
		return opts, nil, err
	}
	_, commandArgs := clicontract.ExtractGlobals(args)
	editingDefaults := len(commandArgs) > 1 && commandArgs[0] == "defaults" && (commandArgs[1] == "delete" || commandArgs[1] == "write" || commandArgs[1] == "list" || commandArgs[1] == "read")
	for key, value := range defaults {
		if editingDefaults {
			continue
		}
		if err := validateDefault(key, value); err != nil {
			return opts, nil, withHints(fmt.Errorf("invalid saved preference: %w", err), "Remove the unsupported value with agentlab defaults delete "+key)
		}
	}
	if editingDefaults {
		defaults = map[string]string{}
	}
	if value := defaults["default-socket"]; value != "" {
		opts.socketPath = value
	}
	if value := defaults["default-timeout"]; value != "" {
		opts.timeout, _ = time.ParseDuration(value)
	}
	opts.jsonOutput = defaults["output-format"] == "json"

	var tailscaleCfg *tailscaleAdminConfig
	// allowInsecureHTTP accumulates the acknowledgement from any source
	// (saved config, env, then --allow-insecure-http flag below). A trusted
	// bootstrap persists this so subsequent commands keep working over the
	// documented Tailscale tunnel without re-stating the flag (review M8).
	var allowInsecureHTTP bool
	if cfg, ok, err := loadClientConfig(); err != nil {
		return opts, nil, err
	} else if ok {
		if endpoint := strings.TrimSpace(cfg.Endpoint); endpoint != "" {
			opts.endpoint = endpoint
		}
		if token := strings.TrimSpace(cfg.Token); token != "" {
			opts.token = token
		}
		tailscaleCfg = cfg.TailscaleAdmin
		allowInsecureHTTP = cfg.AllowInsecureHTTP
	}
	envCfg := readEnvClientConfig()
	if envCfg.Endpoint != "" {
		opts.endpoint = envCfg.Endpoint
	}
	if envCfg.Token != "" {
		opts.token = envCfg.Token
	}
	tailscaleCfg = mergeTailscaleAdminConfig(tailscaleCfg, envCfg.TailscaleAdmin)
	opts.tailscaleAdmin = tailscaleCfg
	allowInsecureHTTP = allowInsecureHTTP || envCfg.AllowInsecureHTTP
	opts.allowInsecureHTTP = allowInsecureHTTP

	fs := flag.NewFlagSet("agentlab", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var help bool
	fs.StringVar(&opts.endpoint, "endpoint", opts.endpoint, "control plane endpoint (http(s)://host:port)")
	fs.StringVar(&opts.token, "token", opts.token, "control plane auth token")
	fs.StringVar(&opts.socketPath, "socket", opts.socketPath, "path to agentlabd socket")
	fs.BoolVar(&opts.jsonOutput, "json", opts.jsonOutput, jsonFlagDescription)
	fs.DurationVar(&opts.timeout, "timeout", opts.timeout, "request timeout (e.g. 30s, 2m)")
	fs.BoolVar(&opts.allowInsecureHTTP, "allow-insecure-http", opts.allowInsecureHTTP,
		"permit plaintext HTTP to a non-loopback endpoint (only inside a trusted tunnel such as Tailscale)")
	fs.BoolVar(&opts.showVersion, "version", false, "print version and exit")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	globalArgs, commandArgs := clicontract.ExtractGlobals(args)
	if err := fs.Parse(globalArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return opts, nil, errHelp
		}
		return opts, nil, newUsageError(err, true)
	}
	if help {
		return opts, nil, errHelp
	}
	if err := clicontract.ValidateTimeout(opts.timeout); err != nil {
		return opts, nil, newUsageError(err, false)
	}
	if opts.socketPath == "" {
		opts.socketPath = defaultSocketPath
	}
	return opts, commandArgs, nil
}

func dispatch(ctx context.Context, args []string, base commonFlags) error {
	switch args[0] {
	case "help":
		return runCatalogHelp(args[1:])
	case "operation":
		return runOperationCommand(ctx, args[1:], base)
	// Short aliases for the 80/20 common operations.
	// Each delegates to the corresponding sandbox subcommand runner.
	case "new":
		return withDefaultNext(runSandboxNew(ctx, args[1:], base), "agentlab new --help")
	case "ls":
		return withDefaultNext(runSandboxList(ctx, args[1:], base), "agentlab ls --help")
	case "rm":
		return withDefaultNext(runSandboxDestroy(ctx, args[1:], base), "agentlab rm --help")
	case "show":
		return withDefaultNext(runSandboxShow(ctx, args[1:], base), "agentlab show --help")
	case "start":
		return withDefaultNext(runSandboxStart(ctx, args[1:], base), "agentlab start --help")
	case "stop":
		return withDefaultNext(runSandboxStop(ctx, args[1:], base), "agentlab stop --help")
	// Full command tree.
	case "status":
		return withDefaultNext(runStatusCommand(ctx, args[1:], base), "agentlab status --help")
	case "schema":
		return withDefaultNext(runSchemaCommand(ctx, args[1:], base), "agentlab schema --help")
	case "init":
		return withDefaultNext(runInitCommand(ctx, args[1:], base), "agentlab init --help")
	case "bootstrap":
		return withDefaultNext(runBootstrapCommand(ctx, args[1:], base), "agentlab bootstrap --help")
	case "job":
		return withDefaultNext(runJobCommand(ctx, args[1:], base), "agentlab job --help")
	case "sandbox":
		return withDefaultNext(runSandboxCommand(ctx, args[1:], base), "agentlab sandbox --help")
	case "workspace":
		return withDefaultNext(runWorkspaceCommand(ctx, args[1:], base), "agentlab workspace --help")
	case "session":
		return withDefaultNext(runSessionCommand(ctx, args[1:], base), "agentlab session --help")
	case "profile":
		return withDefaultNext(runProfileCommand(ctx, args[1:], base), "agentlab profile --help")
	case "secrets":
		return withDefaultNext(runSecretsCommand(ctx, args[1:], base), "agentlab secrets --help")
	case "msg":
		return withDefaultNext(runMsgCommand(ctx, args[1:], base), "agentlab msg --help")
	case "ssh":
		return withDefaultNext(runSSHCommand(ctx, args[1:], base), "agentlab ssh --help")
	case "logs":
		return withDefaultNext(runLogsCommand(ctx, args[1:], base), "agentlab logs --help")
	case "connect":
		return withDefaultNext(runConnectCommand(ctx, args[1:], base), "agentlab connect --help")
	case "disconnect":
		return withDefaultNext(runDisconnectCommand(ctx, args[1:], base), "agentlab disconnect --help")
	case "token":
		return withDefaultNext(runTokenCommand(ctx, args[1:], base), "agentlab token --help")
	case "integration":
		return withDefaultNext(runIntegrationCommand(ctx, args[1:], base), "agentlab integration --help")
	case "user":
		return withDefaultNext(runUserCommand(ctx, args[1:], base), "agentlab user --help")
	case "team":
		return withDefaultNext(runTeamCommand(ctx, args[1:], base), "agentlab team --help")
	case "defaults":
		return withDefaultNext(runDefaultsDispatch(args[1:], base), "agentlab defaults --help")
	case "version":
		return withDefaultNext(runVersionCommand(args[1:], base), "agentlab version --help")
	case "completion":
		return withDefaultNext(runCompletionCommand(args[1:], base), "agentlab completion --help")
	case "pool":
		return withDefaultNext(runPoolCommand(ctx, args[1:], base), "agentlab pool --help")
	default:
		if !base.jsonOutput {
			printUsage()
		}
		return unknownCommandError(args[0], []string{"new", "ls", "rm", "show", "start", "stop", "status", "schema", "init", "bootstrap", "job", "sandbox", "workspace", "session", "profile", "secrets", "msg", "ssh", "logs", "connect", "disconnect", "token", "integration", "user", "team", "defaults", "version", "completion", "pool"})
	}
}

func printUsage() {
	fmt.Fprintln(os.Stdout, "agentlab is the CLI for agentlabd.")
	fmt.Fprintln(os.Stdout, "\nUsage:\n  agentlab [global flags] <command> [flags]\n  agentlab help <command> [subcommand]")
	for _, candidate := range clicontract.Candidates([]string{""}) {
		if !strings.HasPrefix(candidate, "-") {
			fmt.Fprintln(os.Stdout, "  agentlab "+candidate)
		}
	}
	fmt.Fprintln(os.Stdout, "\nGlobal Flags:\n  --endpoint URL  Control plane endpoint\n  --token TOKEN   Control plane token\n  --socket PATH   Daemon socket (default /run/agentlab/agentlabd.sock)\n  --timeout DURATION Request timeout (default 10m)\n  --json[=false]  JSON output, including errors\n  --allow-insecure-http Permit HTTP inside a trusted tunnel\n  --version      Print version\n\nPreferences: explicit flag, existing environment override, saved preference, built-in default.\nExit codes: 0 success/help, 1 operational failure, 2 invalid arguments.")
}

func errorMessage(err error) string {
	if errors.Is(err, errUsage) {
		if msg, _, ok := usageErrorMessage(err); ok && msg != "" {
			return msg
		}
		prefix := errUsage.Error() + ": "
		return strings.TrimPrefix(err.Error(), prefix)
	}
	return err.Error()
}

func showUsageOnError(err error) bool {
	if _, show, ok := usageErrorMessage(err); ok {
		return show
	}
	return false
}

func writeJSONError(w io.Writer, err error) {
	var skipPrintedJSON *printedJSONOnlyError
	if errors.As(err, &skipPrintedJSON) {
		return
	}
	msg, next, hints := describeError(err)
	payload := map[string]any{
		"error":   "internal error",
		"code":    "operation_failed",
		"message": "internal error",
	}
	if err != nil {
		msg := strings.TrimSpace(err.Error())
		if msg == "" {
			msg = "unknown error"
		}
		payload["error"] = msg
		payload["message"] = msg
		if errors.Is(err, errUsage) {
			payload["code"] = "invalid_arguments"
		}
		var transport *url.Error
		if errors.As(err, &transport) {
			payload["code"] = "transport_error"
		}
		var apiErr apiResponseError
		if errors.As(err, &apiErr) {
			code := strings.TrimSpace(apiErr.Code)
			if code != "" {
				payload["code"] = code
			}
			msg = strings.TrimSpace(apiErr.Message)
			if msg == "" {
				msg = fmt.Sprint(payload["error"])
			}
			payload["message"] = msg
			if details := strings.TrimSpace(apiErr.Details); details != "" {
				payload["details"] = details
			}
		}
	}
	if next != "" {
		payload["next"] = next
	}
	if len(hints) > 0 {
		payload["hints"] = hints
	}
	if msg != "" {
		payload["error"] = msg
	}
	data, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintln(w, `{"error":"internal error"}`)
		return
	}
	_, _ = fmt.Fprintln(w, string(data))
}

func printJobUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab job <run|validate|show|artifacts|doctor> [flags]")
}

func printStatusUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab status")
}

func printSchemaUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab schema")
}

func printInitUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab init [--apply] [--smoke-test] [--assets <path>] [--force] [--control-port <port>] [--control-token <token>] [--rotate-control-token] [--tailscale-serve|--no-tailscale-serve]")
}

func printBootstrapUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab bootstrap --host <ssh_host> [--ssh-user <user>] [--ssh-port <port>] [--identity <path>] [--assets <path>] [--control-port <port>] [--control-token <token>] [--rotate-control-token] [--tailscale-serve|--no-tailscale-serve] [--tailscale-authkey <key>] [--tailscale-hostname <name>] [--tailscale-tailnet <name>] [--tailscale-api-key <key>] [--tailscale-oauth-client-id <id>] [--tailscale-oauth-client-secret <secret>] [--tailscale-oauth-scopes <scopes>] [--release-url <url>] [--agentlab-bin <path>] [--agentlabd-bin <path>] [--agentlab-url <url>] [--agentlabd-url <url>] [--force] [--keep-temp] [--verbose]")
}

func printJobRunUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab job run --repo <url> --task <task> --profile <profile> [--ref <ref>] [--branch <branch>] [--mode <mode>] [--ttl <ttl>] [--keepalive] [--workspace <id|name|new:name>] [--workspace-create <name>] [--workspace-size <size>] [--workspace-storage <storage>] [--workspace-wait <duration>] [--stateful]")
}

func printJobValidateUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab job validate --repo <url> --task <task> --profile <profile> [--ref <ref>] [--branch <branch>] [--mode <mode>] [--ttl <ttl>] [--keepalive] [--workspace <id|name|new:name>] [--workspace-create <name>] [--workspace-size <size>] [--workspace-storage <storage>] [--workspace-wait <duration>] [--stateful]")
	fmt.Fprintln(os.Stdout, "Note: Validation returns plan details and exits with non-zero status when preflight validation fails.")
}

func printJobShowUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab job show <job_id> [--events-tail <n>]")
	fmt.Fprintln(os.Stdout, "Note: --events-tail=0 omits recent events from the response.")
}

func printJobArtifactsUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab job artifacts <job_id>")
}

func printJobArtifactsDownloadUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab job artifacts download <job_id> [--out <path>] [--path <path>] [--name <name>] [--latest] [--bundle]")
	fmt.Fprintln(os.Stdout, "Note: By default, downloads the latest bundle (agentlab-artifacts.tar.gz) when available.")
}

func printJobDoctorUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab job doctor <job_id> [--out <path>]")
	fmt.Fprintln(os.Stdout, "Note: --out may be a directory or file path.")
}

func printSandboxUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox <new|validate|list|inventory|reconcile|show|update|start|stop|pause|resume|revert|snapshot|destroy|lease|prune|expose|exposed|unexpose|doctor>")
}

func printSandboxNewUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox new [--name <name>] [--ttl <ttl>] [--keepalive] [--workspace <id>] [--vmid <vmid>] [--job <id>] [--and-ssh] (--profile <profile> | +mod [+mod...])")
	fmt.Fprintln(os.Stdout, "Note: Modifiers are resolved by sorting and joining with '-' (e.g., +secure +small -> secure-small).")
}

func printSandboxValidateUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox validate [--name <name>] [--ttl <ttl>] [--keepalive] [--workspace <id>] [--vmid <vmid>] [--job <id>] (--profile <profile> | +mod [+mod...])")
	fmt.Fprintln(os.Stdout, "Note: Validation returns plan details and exits with non-zero status when preflight validation fails.")
}

func printSandboxListUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox list [--all|-a]")
	fmt.Fprintln(os.Stdout, "Note: Hides destroyed sandboxes by default. Use --all (-a) to include them.")
	fmt.Fprintln(os.Stdout, "Note: Lists AgentLab database records only. Use sandbox inventory for live Proxmox/Tailscale state.")
}

func printSandboxInventoryUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox inventory")
	fmt.Fprintln(os.Stdout, "Note: Lists live Proxmox VMs and annotates them with AgentLab state and Tailscale metadata when available.")
}

func printSandboxReconcileUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox reconcile [--apply]")
	fmt.Fprintln(os.Stdout, "Note: Without --apply this reports drift only. --apply fixes supported AgentLab/Proxmox mismatches, including restored VMs.")
}

func printSandboxShowUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox show <vmid>")
}

func printSandboxUpdateUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox update [--cores <n>] [--memory <size>] <vmid>")
	fmt.Fprintln(os.Stdout, "Note: Memory accepts MiB or GiB suffixes (for example 8192, 8192MiB, 8GiB).")
}

func printSandboxStartUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox start <vmid>")
}

func printSandboxStopUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox stop <vmid>")
	fmt.Fprintln(os.Stdout, "       agentlab sandbox stop --all [--force]")
	fmt.Fprintln(os.Stdout, "Note: --force skips the confirmation prompt for stop --all.")
}

func printSandboxPauseUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox pause <vmid>")
}

func printSandboxResumeUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox resume <vmid>")
}

func printSandboxRevertUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox revert [--force] [--restart|--no-restart] <vmid>")
	fmt.Fprintln(os.Stdout, "Note: By default, restarts the sandbox only if it was running before the revert.")
	fmt.Fprintln(os.Stdout, "Note: Reverting a running sandbox requires confirmation unless --force is set.")
}

func printSandboxSnapshotUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox snapshot <save|list|restore>")
}

func printSandboxSnapshotSaveUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox snapshot save [--force] <vmid> <name>")
	fmt.Fprintln(os.Stdout, "Note: By default, snapshots require the sandbox to be stopped and workspace detached.")
}

func printSandboxSnapshotListUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox snapshot list <vmid>")
}

func printSandboxSnapshotRestoreUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox snapshot restore [--force] <vmid> <name>")
	fmt.Fprintln(os.Stdout, "Note: By default, restores require the sandbox to be stopped and workspace detached.")
}

func printSandboxDestroyUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox destroy [--force] <vmid>")
	fmt.Fprintln(os.Stdout, "Note: --force is a compatibility alias; both paths stop, detach, destroy and release owned leases")
}

func printSandboxLeaseUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox lease renew --ttl <ttl> <vmid>")
	fmt.Fprintln(os.Stdout, "Note: Flags must come before the vmid argument (e.g., --ttl 120 1009)")
}

func printSandboxLeaseRenewUsage() {
	printSandboxLeaseUsage()
}

func printSandboxPruneUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox prune")
	fmt.Fprintln(os.Stdout, "Note: Removes orphaned sandbox entries (sandboxes in TIMEOUT state that no longer exist in Proxmox)")
}

func printSandboxExposeUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox expose [--force] <vmid> :<port>")
	fmt.Fprintln(os.Stdout, "Note: --force skips the confirmation prompt for expose.")
}

func printSandboxExposedUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox exposed")
}

func printSandboxUnexposeUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox unexpose <name>")
}

func printSandboxDoctorUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox doctor <vmid> [--out <path>]")
	fmt.Fprintln(os.Stdout, "Note: --out may be a directory or file path.")
}

func printWorkspaceUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace <create|list|check|fsck|attach|detach|lease|rebind|fork|snapshot>")
}

func printWorkspaceCreateUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace create --name <name> --size <size> [--storage <storage>]")
}

func printWorkspaceListUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace list")
}

func printWorkspaceCheckUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace check <workspace>")
}

func printWorkspaceFsckUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace fsck <workspace> [--repair]")
	fmt.Fprintln(os.Stdout, "Note: --repair must be set to perform filesystem repairs.")
}

func printWorkspaceAttachUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace attach <workspace> <vmid>")
}

func printWorkspaceDetachUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace detach <workspace>")
}

func printWorkspaceLeaseUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace lease clear <workspace>")
}

func printWorkspaceLeaseClearUsage() {
	printWorkspaceLeaseUsage()
}

func printWorkspaceRebindUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace rebind <workspace> --profile <profile> [--ttl <ttl>] [--keep-old]")
}

func printWorkspaceForkUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace fork <workspace> --name <name> [--from-snapshot <name>]")
}

func printWorkspaceSnapshotUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace snapshot <create|list|restore>")
}

func printWorkspaceSnapshotCreateUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace snapshot create <workspace> <name>")
}

func printWorkspaceSnapshotListUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace snapshot list <workspace>")
}

func printWorkspaceSnapshotRestoreUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace snapshot restore <workspace> <name>")
}

func printSessionUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab session <create|list|show|resume|stop|fork|branch|doctor>")
}

func printSessionCreateUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab session create --name <name> --profile <profile> (--workspace <id|name|new:name> | --workspace-create <name>) [--workspace-size <size>] [--workspace-storage <storage>] [--branch <branch>]")
}

func printSessionListUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab session list")
}

func printSessionShowUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab session show <session>")
}

func printSessionResumeUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab session resume <session>")
}

func printSessionStopUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab session stop <session>")
}

func printSessionForkUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab session fork <session> --name <name> (--workspace <id|name|new:name> | --workspace-create <name>) [--workspace-size <size>] [--workspace-storage <storage>] [--profile <profile>] [--branch <branch>]")
}

func printSessionBranchUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab session branch <branch> --profile <profile> [--workspace <id|name|new:name>] [--workspace-create <name>] [--workspace-size <size>] [--workspace-storage <storage>]")
}

func printSessionDoctorUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab session doctor <session> [--out <path>]")
	fmt.Fprintln(os.Stdout, "Note: --out may be a directory or file path.")
}

func printProfileUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab profile <list>")
}

func printProfileListUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab profile list")
}

func printMsgUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab msg <post|tail>")
}

func printMsgPostUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab msg post (--job <id> | --workspace <id> | --session <id>) [--author <name>] [--kind <kind>] [--text <text>] [--payload <json>] [message...]")
	fmt.Fprintln(os.Stdout, "Note: If --text is omitted, trailing arguments are joined as the message text.")
}

func printMsgTailUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab msg tail (--job <id> | --workspace <id> | --session <id>) [--follow] [--tail <n>]")
	fmt.Fprintln(os.Stdout, "Note: --json outputs one JSON object per line.")
}

func printLogsUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab logs <vmid> [--follow] [--tail <n>]")
	fmt.Fprintln(os.Stdout, "Note: --json outputs one JSON object per line.")
}

func printConnectUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab connect --endpoint <url> --token <token> [--jump-host <host>] [--jump-user <user>]")
}

func printDisconnectUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab disconnect")
}

func printSSHUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab ssh <vmid> [--user <user>] [--port <port>] [--identity <path>] [--jump-host <host>] [--jump-user <user>] [--exec] [--no-start] [--wait] [-- <remote command>...]")
	fmt.Fprintln(os.Stdout, "Note: --exec replaces the CLI with ssh when run in a terminal (unless a remote command is provided).")
	fmt.Fprintln(os.Stdout, "Note: --wait polls for SSH readiness before returning.")
}

func isHelpToken(value string) bool {
	switch strings.TrimSpace(value) {
	case "help", "-h", "--help":
		return true
	default:
		return false
	}
}
