package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

func runJobCleanup(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("job cleanup")
	base.bind(fs)
	help := bindHelpFlag(fs)
	usage := func() { fmt.Fprintln(os.Stdout, "Usage: agentlab job cleanup JOB_ID [--json]") }
	if err := parseFlags(fs, args, usage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("job cleanup requires one job ID")
	}
	client, err := apiClientFromFlags(base)
	if err != nil {
		return err
	}
	caps, err := fetchCapabilities(ctx, client)
	if err != nil {
		return err
	}
	if !caps.Features["repository_result_cleanup"] {
		return &blockedRequirement{Code: "unsupported_feature", Requirement: "repository_result_cleanup", Action: "use a daemon with verified result cleanup support"}
	}
	if !base.jsonOutput {
		fmt.Fprintf(os.Stderr, "Selected server: %s (%s)\n", client.target(), caps.ServerIdentity)
	}
	path, err := endpointPath("/v1/jobs", fs.Arg(0), "result", "cleanup")
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	var result map[string]any
	if err := json.Unmarshal(payload, &result); err != nil {
		return err
	}
	if result["job_id"] != fs.Arg(0) {
		return fmt.Errorf("cleanup response identifies another job")
	}
	result["server_identity"] = caps.ServerIdentity
	result["next"] = "agentlab job show " + fs.Arg(0)
	if base.jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	fmt.Fprintf(os.Stdout, "Cleanup request saved for job %s.\nObserve: agentlab job show %s\n", fs.Arg(0), fs.Arg(0))
	return nil
}
