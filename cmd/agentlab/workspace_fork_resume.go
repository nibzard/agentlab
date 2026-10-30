package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
)

func runWorkspaceForkResume(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace fork-resume")
	base.bind(fs)
	help := bindHelpFlag(fs)
	usage := func() { fmt.Fprintln(os.Stdout, "Usage: agentlab workspace fork-resume SOURCE OPERATION_ID [--json]") }
	if err := parseFlags(fs, args, usage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return newUsageError(fmt.Errorf("provide the source workspace and original fork operation ID"), false)
	}
	client, err := apiClientFromFlags(base)
	if err != nil {
		return err
	}
	caps, err := fetchCapabilities(ctx, client)
	if err != nil {
		return err
	}
	if !caps.Features["workspace_fork_recovery"] {
		return &blockedRequirement{Code: "unsupported_feature", Requirement: "workspace_fork_recovery", Action: "use a daemon with native fork recovery; preserve the original operation and its retained volumes"}
	}
	path, err := endpointPath("/v1/workspaces", fs.Arg(0), "forks", fs.Arg(1), "resume")
	if err != nil {
		return err
	}
	if !base.jsonOutput {
		fmt.Fprintf(os.Stderr, "Selected server: %s (%s)\n", client.target(), caps.ServerIdentity)
	}
	data, err := client.doJSON(ctx, http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	return prettyPrintJSON(os.Stdout, data)
}
