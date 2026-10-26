package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"text/tabwriter"
)

func runJobList(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("job list")
	opts := base
	opts.bind(fs)
	var state, profile, workspace, after string
	var limit int
	fs.StringVar(&state, "state", "", "job state")
	fs.StringVar(&profile, "profile", "", "profile filter")
	fs.StringVar(&workspace, "workspace", "", "workspace ID filter")
	fs.StringVar(&after, "after", "", "opaque pagination cursor")
	fs.IntVar(&limit, "limit", 50, "page size (1-200)")
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, func() {
		fmt.Fprintln(os.Stdout, "Usage: agentlab job list [--state STATE] [--profile PROFILE] [--workspace ID] [--limit N] [--after CURSOR]")
	}, help, opts.jsonOutput); err != nil {
		return err
	}
	if limit < 1 || limit > 200 {
		return newUsageError(fmt.Errorf("limit must be between 1 and 200"), false)
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	q := url.Values{"state": {state}, "profile": {profile}, "workspace": {workspace}, "after": {after}, "limit": {strconv.Itoa(limit)}}
	payload, err := client.doJSON(ctx, http.MethodGet, "/v1/jobs?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var result struct {
		Jobs       []jobResponse `json:"jobs"`
		NextCursor string        `json:"next_cursor"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATE\tPROFILE\tWORKSPACE\tSANDBOX")
	for _, job := range result.Jobs {
		workspace := "-"
		if job.WorkspaceID != nil {
			workspace = *job.WorkspaceID
		}
		vmid := "-"
		if job.SandboxVMID != nil {
			vmid = strconv.Itoa(*job.SandboxVMID)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", job.ID, job.Status, job.Profile, workspace, vmid)
	}
	w.Flush()
	if result.NextCursor != "" {
		args := []string{"agentlab", "job", "list", "--after", result.NextCursor, "--limit", strconv.Itoa(limit)}
		for _, filter := range [][2]string{{"state", state}, {"profile", profile}, {"workspace", workspace}} {
			if filter[1] != "" {
				args = append(args, "--"+filter[0], filter[1])
			}
		}
		fmt.Printf("Next page: %s\n", formatShellCommand(args))
	}
	return nil
}
