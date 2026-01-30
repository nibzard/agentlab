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

	"github.com/agentlab/agentlab/internal/buildinfo"
)

const usageText = `agentlab is the CLI for agentlabd.

Usage:
  agentlab --version
  agentlab [--socket PATH] [--json] job run --repo <url> --task <task> --profile <profile> [--ref <ref>] [--mode <mode>] [--ttl <ttl>] [--keepalive]
  agentlab [--socket PATH] [--json] job artifacts <job_id>
  agentlab [--socket PATH] [--json] job artifacts download <job_id> [--out <path>] [--path <path>] [--name <name>] [--latest] [--bundle]
  agentlab [--socket PATH] [--json] sandbox new --profile <profile> [--name <name>] [--ttl <ttl>] [--keepalive] [--workspace <id>] [--vmid <vmid>] [--job <id>]
  agentlab [--socket PATH] [--json] sandbox list
  agentlab [--socket PATH] [--json] sandbox show <vmid>
  agentlab [--socket PATH] [--json] sandbox destroy <vmid>
  agentlab [--socket PATH] [--json] sandbox lease renew <vmid> --ttl <ttl>
  agentlab [--socket PATH] [--json] workspace create --name <name> --size <size> [--storage <storage>]
  agentlab [--socket PATH] [--json] workspace list
  agentlab [--socket PATH] [--json] workspace attach <workspace> <vmid>
  agentlab [--socket PATH] [--json] workspace detach <workspace>
  agentlab [--socket PATH] [--json] workspace rebind <workspace> --profile <profile> [--ttl <ttl>] [--keep-old]
  agentlab [--socket PATH] [--json] ssh <vmid> [--user <user>] [--port <port>] [--identity <path>] [--exec]
  agentlab [--socket PATH] [--json] logs <vmid> [--follow] [--tail <n>]

Global Flags:
  --socket PATH   Path to agentlabd socket (default /run/agentlab/agentlabd.sock)
  --json          Output json
`

type globalOptions struct {
	socketPath  string
	jsonOutput  bool
	showVersion bool
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

	base := commonFlags{socketPath: opts.socketPath, jsonOutput: opts.jsonOutput}
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
	fmt.Fprintln(os.Stdout, "Usage: agentlab job <run|artifacts> [flags]")
}

func printJobRunUsage() {
	fmt.Fprintln(os.Stdout, "Usage: agentlab job run --repo <url> --task <task> --profile <profile> [--ref <ref>] [--mode <mode>] [--ttl <ttl>] [--keepalive]")
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
