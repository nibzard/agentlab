package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/agentlab/agentlab/internal/protocol"
)

func runJobCancel(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("job cancel")
	base.bind(fs)
	help := bindHelpFlag(fs)
	var wait time.Duration
	fs.DurationVar(&wait, "wait", 0, "wait for a verified cancellation result; timeout does not mean failure")
	usage := func() { fmt.Fprintln(os.Stdout, "Usage: agentlab job cancel [--wait DURATION] JOB_ID") }
	if err := parseFlags(fs, args, usage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 1 || wait < 0 {
		return fmt.Errorf("job cancel requires one job ID and a non-negative wait duration")
	}
	client, err := apiClientFromFlags(base)
	if err != nil {
		return err
	}
	caps, err := fetchCapabilities(ctx, client)
	if err != nil {
		return err
	}
	if !caps.Features["job_cancellation"] {
		return &blockedRequirement{Code: "unsupported_feature", Requirement: "job_cancellation", Action: "upgrade the daemon before requesting job cancellation"}
	}
	if !base.jsonOutput {
		fmt.Fprintf(os.Stderr, "Selected server: %s\n", client.target())
	}
	path, err := endpointPath("/v1/jobs", fs.Arg(0), "cancel")
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, "POST", path, nil)
	if err != nil {
		return err
	}
	var record protocol.JobCancellation
	if err := json.Unmarshal(payload, &record); err != nil {
		return err
	}
	timedOut := false
	if wait > 0 {
		waitCtx, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		for record.State != "cancelled" && record.State != "not_needed" && record.State != "blocked" {
			timer := time.NewTimer(time.Second)
			select {
			case <-waitCtx.Done():
				timer.Stop()
				timedOut = true
			case <-timer.C:
			}
			if waitCtx.Err() != nil {
				timedOut = true
			}
			if timedOut {
				break
			}
			payload, err = client.doJSON(waitCtx, "GET", path, nil)
			if err != nil {
				if waitCtx.Err() != nil {
					timedOut = true
					break
				}
				return err
			}
			if err := json.Unmarshal(payload, &record); err != nil {
				return err
			}
		}
	}
	if base.jsonOutput {
		if timedOut {
			var output map[string]any
			data, _ := json.Marshal(record)
			if err := json.Unmarshal(data, &output); err != nil {
				return err
			}
			output["wait_timed_out"] = true
			return json.NewEncoder(os.Stdout).Encode(output)
		}
		if err := json.NewEncoder(os.Stdout).Encode(record); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stdout, "Job %s cancellation: %s\nWork stopped: %t\nCleanup: %s\n", record.JobID, record.State, record.WorkStopped, record.CleanupStatus)
		if timedOut {
			fmt.Fprintln(os.Stdout, "Observation timed out. The server cancellation can continue.")
		}
		if record.Action != "" {
			fmt.Fprintln(os.Stdout, record.Action)
		}
	}
	if record.State == "blocked" {
		if base.jsonOutput {
			return &printedJSONOnlyError{err: errors.New(record.Action)}
		}
		return errors.New(record.Action)
	}
	return nil
}
