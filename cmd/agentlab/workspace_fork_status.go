package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
)

func runWorkspaceForkStatus(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace fork-status")
	base.bind(fs)
	help := bindHelpFlag(fs)
	usage := func() {
		fmt.Fprintln(os.Stdout, "Usage: agentlab workspace fork-status SOURCE [OPERATION_ID] [--json]")
	}
	if err := parseFlags(fs, args, usage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		return newUsageError(fmt.Errorf("provide a source workspace and an optional fork operation ID"), false)
	}
	client, err := apiClientFromFlags(base)
	if err != nil {
		return err
	}
	parts := []string{"/v1/workspaces", fs.Arg(0), "forks"}
	if fs.NArg() == 2 {
		parts = append(parts, fs.Arg(1))
	}
	path, err := endpointPath(parts[0], parts[1:]...)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	return prettyPrintJSON(os.Stdout, payload)
}
