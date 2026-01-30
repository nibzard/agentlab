package main

import (
	"context"
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
  agentlab [--socket PATH] [--json] [--timeout DURATION] job run --repo <url> --task <task> --profile <profile> [--ref <ref>] [--mode <mode>] [--ttl <ttl>] [--keepalive]
  agentlab [--socket PATH] [--json] [--timeout DURATION] job show <job_id> [--events-tail <n>]
  agentlab [--socket PATH] [--json] [--timeout DURATION] job artifacts <job_id>
  agentlab [--socket PATH] [--json] [--timeout DURATION] job artifacts download <job_id> [--out <path>] [--path <path>] [--name <name>] [--latest] [--bundle]
  agentlab [--socket PATH] [--json] [--timeout DURATION] sandbox new --profile <profile> [--name <name>] [--ttl <ttl>] [--keepalive] [--workspace <id>] [--vmid <vmid>] [--job <id>]
  agentlab [--socket PATH] [--json] [--timeout DURATION] sandbox list
  agentlab [--socket PATH] [--json] [--timeout DURATION] sandbox show <vmid>
  agentlab [--socket PATH] [--json] [--timeout DURATION] sandbox destroy <vmid>
  agentlab [--socket PATH] [--json] [--timeout DURATION] sandbox lease renew <vmid> --ttl <ttl>
  agentlab [--socket PATH] [--json] [--timeout DURATION] workspace create --name <name> --size <size> [--storage <storage>]
  agentlab [--socket PATH] [--json] [--timeout DURATION] workspace list
  agentlab [--socket PATH] [--json] [--timeout DURATION] workspace attach <workspace> <vmid>
  agentlab [--socket PATH] [--json] [--timeout DURATION] workspace detach <workspace>
  agentlab [--socket PATH] [--json] [--timeout DURATION] workspace rebind <workspace> --profile <profile> [--ttl <ttl>] [--keep-old]
  agentlab [--socket PATH] [--json] [--timeout DURATION] ssh <vmid> [--user <user>] [--port <port>] [--identity <path>] [--exec]
  agentlab [--socket PATH] [--json] [--timeout DURATION] logs <vmid> [--follow] [--tail <n>]

Global Flags:
  --socket PATH   Path to agentlabd socket (default /run/agentlab/agentlabd.sock)
  --json          Output json
  --timeout       Request timeout (e.g. 30s, 2m)
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
		fmt.Fprintln(os.Stderr, err)
		printUsage()
		os.Exit(2)
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
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseGlobal(args []string) (globalOptions, []string, error) {
	opts := globalOptions{socketPath: defaultSocketPath}
	fs := flag.NewFlagSet("agentlab", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.socketPath, "socket", defaultSocketPath, "path to agentlabd socket")
	fs.BoolVar(&opts.jsonOutput, "json", false, jsonFlagDescription)
	fs.DurationVar(&opts.timeout, "timeout", defaultRequestTimeout, "request timeout (e.g. 30s, 2m)")
	fs.BoolVar(&opts.showVersion, "version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return opts, nil, err
	}
	if opts.socketPath == "" {
		opts.socketPath = defaultSocketPath
	}
	return opts, fs.Args(), nil
}

func dispatch(ctx context.Context, args []string, base commonFlags) error {
	switch args[0] {
	case "job":
		return runJobCommand(ctx, args[1:], base)
	case "sandbox":
		return runSandboxCommand(ctx, args[1:], base)
	case "workspace":
		return runWorkspaceCommand(ctx, args[1:], base)
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

func printJobUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab job <run|show|artifacts> [flags]")
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
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox <new|list|show|destroy|lease>")
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

func printSandboxDestroyUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox destroy <vmid>")
}

func printSandboxLeaseUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab sandbox lease renew <vmid> --ttl <ttl>")
}

func printSandboxLeaseRenewUsage() {
	printSandboxLeaseUsage()
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

func printLogsUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab logs <vmid> [--follow] [--tail <n>]")
	fmt.Fprintln(os.Stdout, "Note: --json outputs one JSON object per line.")
}

func printSSHUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab ssh <vmid> [--user <user>] [--port <port>] [--identity <path>] [--exec]")
	fmt.Fprintln(os.Stdout, "Note: --exec replaces the CLI with ssh when run in a terminal.")
}

func isHelpToken(value string) bool {
	switch strings.TrimSpace(value) {
	case "help", "-h", "--help":
		return true
	default:
		return false
	}
}
