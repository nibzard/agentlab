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
)

const usageText = `agentlab is the CLI for agentlabd.

Usage:
  agentlab --version
  agentlab [--socket PATH] [--json] [--timeout DURATION] status
  agentlab [--socket PATH] [--json] [--timeout DURATION] job run --repo <url> --task <task> --profile <profile> [--ref <ref>] [--mode <mode>] [--ttl <ttl>] [--keepalive]
  agentlab [--socket PATH] [--json] [--timeout DURATION] job show <job_id> [--events-tail <n>]
  agentlab [--socket PATH] [--json] [--timeout DURATION] job artifacts <job_id>
  agentlab [--socket PATH] [--json] [--timeout DURATION] job artifacts download <job_id> [--out <path>] [--path <path>] [--name <name>] [--latest] [--bundle]
  agentlab [--socket PATH] [--json] [--timeout DURATION] sandbox new --profile <profile> [--name <name>] [--ttl <ttl>] [--keepalive] [--workspace <id>] [--vmid <vmid>] [--job <id>]
  agentlab [--socket PATH] [--json] [--timeout DURATION] sandbox list
  agentlab [--socket PATH] [--json] [--timeout DURATION] sandbox show <vmid>
  agentlab [--socket PATH] [--json] [--timeout DURATION] sandbox start <vmid>
  agentlab [--socket PATH] [--json] [--timeout DURATION] sandbox stop <vmid>
  agentlab [--socket PATH] [--json] [--timeout DURATION] sandbox revert [--force] [--restart|--no-restart] <vmid>
  agentlab [--socket PATH] [--json] [--timeout DURATION] sandbox destroy [--force] <vmid>
  agentlab [--socket PATH] [--json] [--timeout DURATION] sandbox lease renew --ttl <ttl> <vmid>
  agentlab [--socket PATH] [--json] [--timeout DURATION] sandbox prune
  agentlab [--socket PATH] [--json] [--timeout DURATION] workspace create --name <name> --size <size> [--storage <storage>]
  agentlab [--socket PATH] [--json] [--timeout DURATION] workspace list
  agentlab [--socket PATH] [--json] [--timeout DURATION] workspace attach <workspace> <vmid>
  agentlab [--socket PATH] [--json] [--timeout DURATION] workspace detach <workspace>
  agentlab [--socket PATH] [--json] [--timeout DURATION] workspace rebind <workspace> --profile <profile> [--ttl <ttl>] [--keep-old]
  agentlab [--socket PATH] [--json] [--timeout DURATION] profile list
  agentlab [--socket PATH] [--json] [--timeout DURATION] ssh <vmid> [--user <user>] [--port <port>] [--identity <path>] [--exec] [-- <remote command>...]
  agentlab [--socket PATH] [--json] [--timeout DURATION] logs <vmid> [--follow] [--tail <n>]

Global Flags:
  --socket PATH   Path to agentlabd socket (default /run/agentlab/agentlabd.sock)
  --json          Output json
  --timeout       Request timeout (e.g. 30s, 2m)

Errors:
  When --json is set, errors are emitted as: {"error":"message"}

Exit codes:
  0: Success or help displayed
  1: Command or request failed
  2: Invalid arguments or usage
`

type globalOptions struct {
	socketPath  string
	jsonOutput  bool
	showVersion bool
	timeout     time.Duration
}

func main() {
	opts, args, err := parseGlobal(os.Args[1:])
	if err != nil {
		if errors.Is(err, errHelp) {
			printUsage()
			return
		}
		msg := errorMessage(err)
		if opts.jsonOutput {
			writeJSONError(os.Stdout, msg)
		} else {
			fmt.Fprintln(os.Stderr, msg)
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

	base := commonFlags{socketPath: opts.socketPath, jsonOutput: opts.jsonOutput, timeout: opts.timeout}
	if err := dispatch(ctx, args, base); err != nil {
		if errors.Is(err, errHelp) {
			return
		}
		msg := errorMessage(err)
		if opts.jsonOutput {
			writeJSONError(os.Stdout, msg)
		} else {
			fmt.Fprintln(os.Stderr, msg)
		}
		if errors.Is(err, errUsage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func parseGlobal(args []string) (globalOptions, []string, error) {
	opts := globalOptions{socketPath: defaultSocketPath}
	fs := flag.NewFlagSet("agentlab", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var help bool
	fs.StringVar(&opts.socketPath, "socket", defaultSocketPath, "path to agentlabd socket")
	fs.BoolVar(&opts.jsonOutput, "json", false, jsonFlagDescription)
	fs.DurationVar(&opts.timeout, "timeout", defaultRequestTimeout, "request timeout (e.g. 30s, 2m)")
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
	if opts.socketPath == "" {
		opts.socketPath = defaultSocketPath
	}
	return opts, fs.Args(), nil
}

func dispatch(ctx context.Context, args []string, base commonFlags) error {
	switch args[0] {
	case "status":
		return runStatusCommand(ctx, args[1:], base)
	case "job":
		return runJobCommand(ctx, args[1:], base)
	case "sandbox":
		return runSandboxCommand(ctx, args[1:], base)
	case "workspace":
		return runWorkspaceCommand(ctx, args[1:], base)
	case "profile":
		return runProfileCommand(ctx, args[1:], base)
	case "ssh":
		return runSSHCommand(ctx, args[1:], base)
	case "logs":
		return runLogsCommand(ctx, args[1:], base)
	default:
		printUsage()
		return fmt.Errorf("unknown command %q", args[0])
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

func writeJSONError(w io.Writer, message string) {
	payload := map[string]string{"error": message}
	data, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintln(w, `{"error":"internal error"}`)
		return
	}
	_, _ = fmt.Fprintln(w, string(data))
}

func printJobUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab job <run|show|artifacts> [flags]")
}

func printStatusUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab status")
}

func printJobRunUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab job run --repo <url> --task <task> --profile <profile> [--ref <ref>] [--mode <mode>] [--ttl <ttl>] [--keepalive]")
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

func printSandboxUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox <new|list|show|start|stop|revert|destroy|lease|prune>")
}

func printSandboxNewUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox new --profile <profile> [--name <name>] [--ttl <ttl>] [--keepalive] [--workspace <id>] [--vmid <vmid>] [--job <id>]")
}

func printSandboxListUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox list")
}

func printSandboxShowUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox show <vmid>")
}

func printSandboxStartUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox start <vmid>")
}

func printSandboxStopUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox stop <vmid>")
}

func printSandboxRevertUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox revert [--force] [--restart|--no-restart] <vmid>")
	fmt.Fprintln(os.Stdout, "Note: By default, restarts the sandbox only if it was running before the revert.")
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

func printWorkspaceUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace <create|list|attach|detach|rebind>")
}

func printWorkspaceCreateUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace create --name <name> --size <size> [--storage <storage>]")
}

func printWorkspaceListUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace list")
}

func printWorkspaceAttachUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace attach <workspace> <vmid>")
}

func printWorkspaceDetachUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace detach <workspace>")
}

func printWorkspaceRebindUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab workspace rebind <workspace> --profile <profile> [--ttl <ttl>] [--keep-old]")
}

func printProfileUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab profile <list>")
}

func printProfileListUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab profile list")
}

func printLogsUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab logs <vmid> [--follow] [--tail <n>]")
	fmt.Fprintln(os.Stdout, "Note: --json outputs one JSON object per line.")
}

func printSSHUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab ssh <vmid> [--user <user>] [--port <port>] [--identity <path>] [--exec] [-- <remote command...>]")
	fmt.Fprintln(os.Stdout, "Usage: agentlab ssh [flags] <vmid> [-- <remote command...>]")
	fmt.Fprintln(os.Stdout, "Note: --exec replaces the CLI with ssh when run in a terminal (unless a remote command is provided).")
}

func isHelpToken(value string) bool {
	switch strings.TrimSpace(value) {
	case "help", "-h", "--help":
		return true
	default:
		return false
	}
}
