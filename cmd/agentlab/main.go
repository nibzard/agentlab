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
	"github.com/agentlab/agentlab/internal/connection"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/agentlab/agentlab/internal/buildinfo"
	"github.com/agentlab/agentlab/internal/operations"
)

const usageText = `agentlab is the CLI for agentlabd.

Quick commands (aliases for common operations):
  agentlab new [--name <name>] [--prompt <text>] (--profile <profile> | +mod [+mod...])   Create a sandbox (alias: sandbox new)
  agentlab ls [--all|-a]                                                   List current sandboxes (alias: sandbox list)
  agentlab show <vmid>                                                    Show sandbox details (alias: sandbox show)
  agentlab ssh <vmid>                                                     SSH into a sandbox
  agentlab start <vmid>                                                   Start a sandbox (alias: sandbox start)
  agentlab stop <vmid>                                                    Stop a sandbox (alias: sandbox stop)
  agentlab rm [--force] <vmid>                                            Destroy a sandbox (alias: sandbox destroy)
  agentlab logs <vmid> [--follow]                                         View sandbox logs

Usage:
  agentlab template <capabilities|validate|create|list|show|activate|build>
  agentlab --version
  agentlab job cleanup JOB_ID [--json]
  agentlab job cancel [--wait DURATION] JOB_ID
  agentlab repo snapshot [--repo PATH] [--out DIRECTORY] [--json]
  agentlab repo result --repo PATH --base-revision COMMIT --out DIRECTORY [--json]
  agentlab repo upload --manifest PATH [--json]
  agentlab repo <show|remove> ID [--json]
  agentlab context list [--json]
  agentlab context use NAME [--json]
  agentlab context credentials NAME --credential-store auto|os|file [--json]
  agentlab context credentials-pending [--json]
  agentlab context credentials-cleanup [--json]
  agentlab skill show [--json]
  agentlab skill export [--out DIRECTORY] [--json]
  agentlab capabilities [--json]
  agentlab job result JOB_ID [--out DIRECTORY] [--json]
  agentlab setup --host HOST --repo PATH [--plan] [--no-wait] [--request-id KEY]
  agentlab operation <show|resume|cancel> ID
  agentlab [connection options] dashboard [--listen 127.0.0.1:8080]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] status
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] schema
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] init [--apply] [--backend <backend>] [--smoke-test] [--assets <path>] [--force] [--control-port <port>] [--control-token <token>] [--rotate-control-token] [--tailscale-serve|--no-tailscale-serve]
  agentlab [--json] bootstrap --host <ssh_host> [--ssh-user <user>] [--ssh-port <port>] [--identity <path>] [--assets <path>] [--control-port <port>] [--control-token <token>] [--rotate-control-token] [--tailscale-serve|--no-tailscale-serve] [--tailscale-authkey <key>] [--tailscale-hostname <name>] [--tailscale-tailnet <name>] [--tailscale-api-key <key>] [--tailscale-oauth-client-id <id>] [--tailscale-oauth-client-secret <secret>] [--tailscale-oauth-scopes <scopes>] [--release-url <url>] [--agentlab-bin <path>] [--agentlabd-bin <path>] [--agentlab-url <url>] [--agentlabd-url <url>] [--force] [--keep-temp] [--verbose]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] job run [--request-id <key>] --repo <url> --task <task> [--test-command <command>] --profile <profile> [--ref <ref>] [--branch <branch>] [--mode <mode>] [--ttl <ttl>] [--keepalive] [--workspace <id|name|new:name>] [--workspace-create <name>] [--workspace-size <size>] [--workspace-storage <storage>] [--workspace-wait <duration>] [--stateful]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] job validate --repo <url> --task <task> [--test-command <command>] --profile <profile> [--ref <ref>] [--branch <branch>] [--mode <mode>] [--ttl <ttl>] [--keepalive] [--workspace <id|name|new:name>] [--workspace-create <name>] [--workspace-size <size>] [--workspace-storage <storage>] [--workspace-wait <duration>] [--stateful]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] job show <job_id> [--events-tail <n>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] job artifacts <job_id>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] job artifacts download <job_id> [--out <path>] [--path <path>] [--name <name>] [--latest] [--bundle]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] job doctor <job_id> [--out <path>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox new [--name <name>] [--ttl <ttl>] [--keepalive] [--workspace <id>] [--vmid <vmid>] [--job <id>] [--and-ssh] [--type <type>] [--image <image>] [--prompt <text>] [--tag <tag>...] (--profile <profile> | +mod [+mod...])
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox validate [--name <name>] [--ttl <ttl>] [--keepalive] [--workspace <id>] [--vmid <vmid>] [--job <id>] (+mod [+mod...] | --profile <profile>)
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox list [--all|-a]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox inventory
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox reconcile [--apply]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox show <vmid>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox update [--cores <n>] [--memory <size>] [--tag <tag>...] [--clear-tags] <vmid>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox start <vmid>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox stop <vmid>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox stop --all [--force]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox pause <vmid>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox resume <vmid>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox revert [--force] [--restart|--no-restart] <vmid>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox snapshot save [--force] <vmid> <name>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox snapshot list <vmid>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox snapshot restore [--force] <vmid> <name>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox destroy [--force] <vmid>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox lease renew --ttl <ttl> <vmid>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox prune
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox expose [--force] <vmid> :<port>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox exposed
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox unexpose <name>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox doctor <vmid> [--out <path>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace create --name <name> --size <size> [--storage <storage>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace list
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace check <workspace>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace fsck <workspace> [--repair]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace attach <workspace> <vmid>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace detach <workspace>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace lease clear <workspace>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace rebind <workspace> --profile <profile> [--ttl <ttl>] [--keep-old]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace fork <workspace> --name <name> [--from-snapshot <name>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace fork-status <workspace> [operation-id]
  agentlab [connection options] workspace fork-resume <workspace> <operation-id>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace snapshot create <workspace> <name>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace snapshot list <workspace>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace snapshot restore <workspace> <name>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] session create --name <name> --profile <profile> (--workspace <id|name|new:name> | --workspace-create <name>) [--workspace-size <size>] [--workspace-storage <storage>] [--branch <branch>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] session list
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] session show <session>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] session resume <session>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] session stop <session>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] session fork <session> --name <name> (--workspace <id|name|new:name> | --workspace-create <name>) [--workspace-size <size>] [--workspace-storage <storage>] [--profile <profile>] [--branch <branch>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] session branch <branch> --profile <profile> [--workspace <id|name|new:name>] [--workspace-create <name>] [--workspace-size <size>] [--workspace-storage <storage>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] session doctor <session> [--out <path>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] profile list
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] secrets <show|validate|set-env|set-git|add-ssh-key|remove-ssh-key|set-tailscale|clear-tailscale> [...]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] token <enroll|renew|create|list|inspect|revoke|status> [...]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] integration <add|list|rm|status> [...]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] user <add|list|show|rm|key> [...]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] team <add|list|rm|members|member> [...]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] ssh <vmid> [--user <user>] [--port <port>] [--identity <path>] [--jump-host <host>] [--jump-user <user>] [--exec] [--no-start] [--wait] [-- <remote command>...]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] msg post (--job <id> | --workspace <id> | --session <id>) [--author <name>] [--kind <kind>] [--text <text>] [--payload <json>] [message...]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] msg tail (--job <id> | --workspace <id> | --session <id>) [--follow] [--tail <n>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] logs <vmid> [--follow] [--tail <n>]
  agentlab connect --endpoint <url> [--context <name>] [--token-stdin] [--credential-store auto|os|file] [--jump-host <host>] [--jump-user <user>]
  agentlab disconnect
  agentlab defaults write <key> <value>
  agentlab defaults read <key>
  agentlab defaults list
  agentlab defaults delete <key>
  agentlab version [--json]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] pool status
  agentlab completion <bash|zsh|fish>

Global Flags:
  --context NAME  Named server context
  --endpoint URL  Control plane endpoint (http(s)://host:port)
  --token TOKEN   Control plane auth token (Authorization: Bearer)
  --socket PATH   Path to agentlabd socket (default /run/agentlab/agentlabd.sock)
  --json          Output json
  --timeout       Request timeout (e.g. 30s, 2m)

Errors:
  When --json is set, errors are emitted as: {"error":"message","code":"...","message":"...","details":"..."}

Exit codes:
  0: Success or help displayed
  1: Command or request failed
  2: Invalid arguments or usage
`

type globalOptions struct {
	selection         *connection.Selection
	connectionFields  connectionFields
	contextName       string
	socketPath        string
	endpoint          string
	token             string
	jsonOutput        bool
	showVersion       bool
	timeout           time.Duration
	tailscaleAdmin    *tailscaleAdminConfig
	allowInsecureHTTP bool
}

var validateCommandOnly bool
var errCommandValidated = errors.New("command syntax validated")

func main() {
	if len(os.Args) > 1 && os.Args[1] == "__setup-worker" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := runServerSetupCommand(ctx, os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "server setup worker failed; inspect the saved operation")
			os.Exit(1)
		}
		return
	}

	validateCommandOnly = len(os.Args) > 1 && os.Args[1] == "--validate-command"
	if validateCommandOnly {
		os.Args = append(os.Args[:1], os.Args[2:]...)
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
	if isHelpToken(args[0]) {
		printUsage()
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	base := opts.common()
	if err := dispatch(ctx, args, base); err != nil {
		if errors.Is(err, errHelp) || errors.Is(err, errCommandValidated) {
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

func parseGlobal(args []string) (globalOptions, []string, error) {
	opts := globalOptions{socketPath: defaultSocketPath, timeout: defaultRequestTimeout}
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--json" || arg == "--json=true" {
			opts.jsonOutput = true
		}
		if arg == "--json=false" {
			opts.jsonOutput = false
		}
	}
	localOnly := localConnectionCommand(args)
	defaults := map[string]string{}
	var err error
	if !localOnly {
		defaults, err = loadDefaults()
		if err != nil {
			return opts, nil, err
		}
	}
	if value := defaults["default-socket"]; value != "" {
		opts.socketPath = value
	}
	if value := defaults["default-timeout"]; value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return opts, nil, fmt.Errorf("invalid default-timeout: %q", value)
		}
		opts.timeout = duration
	}
	// Explicit output flags take precedence over the saved preference.
	explicitOutput := false
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--json" || strings.HasPrefix(arg, "--json=") {
			explicitOutput = true
		}
	}
	if !explicitOutput {
		opts.jsonOutput = defaults["output-format"] == "json"
	}
	fs := flag.NewFlagSet("agentlab", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var help bool
	fs.StringVar(&opts.contextName, "context", opts.contextName, "named server context")
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
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return opts, nil, errHelp
		}
		return opts, nil, newUsageError(err, true)
	}
	if help {
		return opts, nil, errHelp
	}
	fs.Visit(func(f *flag.Flag) { opts.connectionFields |= connectionField(f.Name) })
	if !localOnly && !opts.showVersion {
		store, err := contextStore()
		if err != nil {
			return opts, nil, err
		}
		selected, err := connection.Prepare(context.Background(), connection.PrepareOptions{
			Root: store.Root, Context: requestedContext(args), Environment: connection.EnvironmentOverrides(), DefaultSocket: opts.socketPath,
			AllowUnselected: requestedContext(args) == "" || globalCommand(args) == "connect" || globalCommand(args) == "setup" || globalCommand(args) == "bootstrap" || globalCommand(args) == "init",
			Legacy: func() (connection.Settings, error) {
				cfg, _, err := loadClientConfig()
				return legacySettings(cfg), err
			},
		})
		if err != nil {
			return opts, nil, err
		}
		opts.selection = selected
		preview, err := selected.Preview(opts.common().connectionOverrides())
		if err != nil {
			return opts, nil, err
		}
		opts.contextName, opts.endpoint, opts.token = preview.ContextName, preview.Endpoint, preview.Token
		opts.socketPath, opts.allowInsecureHTTP = preview.SocketPath, preview.AllowInsecureHTTP
		switch globalCommand(args) {
		case "bootstrap", "init", "setup":
			cfg, _, err := loadClientConfig()
			if err != nil {
				return opts, nil, err
			}
			opts.tailscaleAdmin = mergeTailscaleAdminConfig(cfg.TailscaleAdmin, readEnvClientConfig().TailscaleAdmin)
		}
	}

	if opts.socketPath == "" {
		opts.socketPath = defaultSocketPath
	}
	return opts, fs.Args(), nil
}

func dispatch(ctx context.Context, args []string, base commonFlags) error {
	switch args[0] {
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
	case "repo":
		return runRepoCommand(ctx, args[1:], base)
	case "skill":
		return runSkillCommand(ctx, args[1:], base)
	case "capabilities":
		return runCapabilitiesCommand(ctx, args[1:], base)
	case "schema":
		return withDefaultNext(runSchemaCommand(ctx, args[1:], base), "agentlab schema --help")
	case "init":
		return withDefaultNext(runInitCommand(ctx, args[1:], base), "agentlab init --help")
	case "bootstrap":
		return withDefaultNext(runBootstrapCommand(ctx, args[1:], base), "agentlab bootstrap --help")
	case "template":
		return runTemplateCommand(ctx, args[1:], base)
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
	case "setup":
		return runSetupCommand(ctx, args[1:], base)
	case "operation":
		return runOperationCommandContext(ctx, args[1:], base)
	case "context":
		return runContextCommand(args[1:], base)
	case "dashboard":
		return runDashboardCommand(ctx, args[1:], base)
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
		return unknownCommandError(args[0], []string{"repo", "skill", "capabilities", "setup", "operation", "context", "dashboard", "new", "rm", "show", "start", "stop", "status", "schema", "init", "bootstrap", "job", "sandbox", "workspace", "session", "profile", "secrets", "msg", "ssh", "logs", "connect", "disconnect", "token", "integration", "user", "team", "defaults", "version", "completion", "pool"})
	}
}

func printUsage() {
	_, _ = fmt.Fprint(os.Stdout, usageText)
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
	payload := map[string]string{
		"error":   "internal error",
		"message": "internal error",
	}
	if err != nil {
		msg := strings.TrimSpace(err.Error())
		if msg == "" {
			msg = "unknown error"
		}
		payload["error"] = msg
		payload["message"] = msg
		var apiErr apiResponseError
		if errors.As(err, &apiErr) {
			code := strings.TrimSpace(apiErr.Code)
			if code != "" {
				payload["code"] = code
			}
			msg = strings.TrimSpace(apiErr.Message)
			if msg == "" {
				msg = strings.TrimSpace(payload["error"])
			}
			payload["message"] = msg
			if details := strings.TrimSpace(apiErr.Details); details != "" {
				payload["details"] = details
			}
		}
	}
	switch {
	case errors.Is(err, operations.ErrConflict):
		payload["code"] = "idempotency_conflict"
		payload["state"] = "failed"
		payload["action"] = "reuse the original inputs or choose a new request key"
	case errors.Is(err, operations.ErrExpired):
		payload["code"] = "idempotency_key_expired"
		payload["state"] = "blocked"
		payload["action"] = "inspect retained resources before choosing a new request key"
	case errors.Is(err, operations.ErrBusy):
		payload["code"] = "operation_busy"
		payload["state"] = "running"
		payload["action"] = "observe the existing operation before retrying"
	}
	var operationRequirement *operations.Requirement
	if errors.As(err, &operationRequirement) {
		payload["code"] = operationRequirement.Code
		payload["state"] = "blocked"
		payload["action"] = operationRequirement.Action
	}
	var blocked *blockedRequirement
	if errors.As(err, &blocked) {
		payload["state"] = "blocked"
		payload["code"] = blocked.Code
		payload["requirement"] = blocked.Requirement
		payload["action"] = blocked.Action
	}
	output := map[string]any{}
	for key, value := range payload {
		output[key] = value
	}
	var recovery apiResponseError
	if errors.As(err, &recovery) {
		if recovery.StorageOperation != nil {
			output["storage_operation"] = recovery.StorageOperation
			output["state"] = "blocked"
		}
		if recovery.WorkspaceFork != nil {
			output["workspace_fork"] = recovery.WorkspaceFork
			output["state"] = "blocked"
		}
		if recovery.WorkspaceAllocation != nil {
			output["workspace_allocation"] = recovery.WorkspaceAllocation
			output["state"] = "blocked"
		}
		if recovery.Action != "" {
			output["action"] = recovery.Action
		}
		if recovery.Retryable != nil {
			output["retryable"] = *recovery.Retryable
		}
	}
	var metadata interface{ ErrorMetadata() map[string]any }
	if errors.As(err, &metadata) {
		for key, value := range metadata.ErrorMetadata() {
			output[key] = value
		}
	}
	data, err := json.Marshal(output)
	if err != nil {
		fmt.Fprintln(w, `{"error":"internal error"}`)
		return
	}
	_, _ = fmt.Fprintln(w, string(data))
}

func printJobUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab job <run|validate|show|artifacts|doctor|cancel|result|cleanup> [flags]")
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
	fmt.Fprintln(os.Stdout, "Usage: agentlab job run [--request-id <key>] --repo <url> --task <task> [--test-command <command>] --profile <profile> [--ref <ref>] [--branch <branch>] [--mode <mode>] [--ttl <ttl>] [--keepalive] [--workspace <id|name|new:name>] [--workspace-create <name>] [--workspace-size <size>] [--workspace-storage <storage>] [--workspace-wait <duration>] [--stateful]")
}

func printJobValidateUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab job validate --repo <url> --task <task> [--test-command <command>] --profile <profile> [--ref <ref>] [--branch <branch>] [--mode <mode>] [--ttl <ttl>] [--keepalive] [--workspace <id|name|new:name>] [--workspace-create <name>] [--workspace-size <size>] [--workspace-storage <storage>] [--workspace-wait <duration>] [--stateful]")
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
	fmt.Fprintln(os.Stdout, "Note: --force bypasses state restrictions and destroys sandbox in any state")
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
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace <create|list|check|fsck|attach|detach|lease|rebind|fork|fork-status|fork-resume|snapshot>")
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
	fmt.Fprintln(os.Stdout, "Usage: agentlab connect --endpoint <url> [--context <name>] [--token-stdin] [--credential-store auto|os|file] [--jump-host <host>] [--jump-user <user>]")
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
