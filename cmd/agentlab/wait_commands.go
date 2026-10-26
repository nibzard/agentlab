package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type operationResponse struct {
	ID     string          `json:"id"`
	State  string          `json:"state"`
	VMID   *int            `json:"vmid"`
	Result json.RawMessage `json:"result"`
}

func waitOperation(ctx context.Context, client *apiClient, opts commonFlags, id string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()
	for {
		payload, err := client.doJSON(ctx, http.MethodGet, "/v1/operations/"+url.PathEscape(id), nil)
		if err != nil {
			return nil, withHints(err, "Resume: "+formatShellCommand([]string{"agentlab", "operation", "wait", id})+"; stopping the client does not cancel provisioning")
		}
		var op operationResponse
		if err := json.Unmarshal(payload, &op); err != nil {
			return nil, err
		}
		switch op.State {
		case "succeeded":
			return op.Result, nil
		case "failed", "rejected", "unknown":
			hints := []string{"Inspect the resource before issuing another create request; an unknown outcome must not be retried with a new key"}
			if op.VMID != nil {
				hints = append(hints, "Inspect allocation: "+formatShellCommand([]string{"agentlab", "sandbox", "show", fmt.Sprint(*op.VMID)}))
			}
			if len(op.Result) > 0 {
				var failure struct {
					Error   string `json:"error"`
					Message string `json:"message"`
				}
				if json.Unmarshal(op.Result, &failure) == nil {
					message := failure.Message
					if message == "" {
						message = failure.Error
					}
					if message != "" {
						hints = append(hints, "Daemon reported: "+message)
					}
				}
			}
			return nil, newCLIError("creation operation "+id+" is "+op.State, formatShellCommand([]string{"agentlab", "operation", "show", id}), hints...)
		}
		select {
		case <-ctx.Done():
			return nil, withHints(ctx.Err(), "Resume: "+formatShellCommand([]string{"agentlab", "operation", "wait", id})+"; provisioning may still be running")
		case <-time.After(500 * time.Millisecond):
		}
	}
}
func runOperationCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		fmt.Fprintln(os.Stdout, "Usage: agentlab operation <show|wait> ID [--json] [--timeout DURATION]")
		return errHelp
	}
	if args[0] != "show" && args[0] != "wait" {
		return newUsageError(fmt.Errorf("unknown operation command %q", args[0]), false)
	}
	fs := newFlagSet("operation " + args[0])
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args[1:], func() { fmt.Fprintln(os.Stdout, "Usage: agentlab operation "+args[0]+" ID") }, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return newUsageError(fmt.Errorf("operation ID is required"), false)
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	var payload []byte
	if args[0] == "wait" {
		payload, err = waitOperation(ctx, client, opts, fs.Arg(0))
	} else {
		payload, err = client.doJSON(ctx, http.MethodGet, "/v1/operations/"+url.PathEscape(fs.Arg(0)), nil)
	}
	if err != nil {
		return err
	}
	return prettyPrintJSON(os.Stdout, payload)
}
func runWaitCommand(ctx context.Context, args []string, base commonFlags, kind string) error {
	fs := newFlagSet(kind + " wait")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, func() { fmt.Fprintln(os.Stdout, "Usage: agentlab "+kind+" wait ID [--timeout DURATION] [--json]") }, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return newUsageError(fmt.Errorf("one resource ID is required"), false)
	}
	if kind == "sandbox" {
		if _, err := parseVMID(fs.Arg(0)); err != nil {
			return err
		}
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()
	started := time.Now()
	lastState := ""
	resume := formatShellCommand([]string{"agentlab", kind, "wait", fs.Arg(0)})
	for {
		payload, err := client.doJSON(ctx, http.MethodGet, "/v1/"+kind+"s/"+url.PathEscape(fs.Arg(0)), nil)
		if err != nil {
			return withHints(err, "Stopping observation does not cancel work; resume: "+resume)
		}
		var record struct {
			State  string `json:"state"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(payload, &record); err != nil {
			return err
		}
		state := record.State
		if kind == "job" {
			state = record.Status
		}
		success := kind == "job" && state == "COMPLETED" || kind == "sandbox" && (state == "READY" || state == "RUNNING")
		failed := state == "FAILED" || state == "TIMEOUT" || state == "DESTROYED" || state == "STOPPED"
		if !opts.jsonOutput && state != lastState {
			fmt.Fprintf(os.Stderr, "Observing %s %s: %s (elapsed %s); resume: %s\n", kind, fs.Arg(0), state, time.Since(started).Round(time.Millisecond), resume)
			lastState = state
		}
		if success || failed {
			recovery := waitRecovery(kind, fs.Arg(0), state)
			if opts.jsonOutput {
				observed, err := waitResult(payload, kind, fs.Arg(0), state, success, time.Since(started), recovery)
				if err != nil {
					return err
				}
				if err := prettyPrintJSON(os.Stdout, observed); err != nil {
					return err
				}
			} else {
				fmt.Printf("%s %s: %s\nNext: %s\n", kind, fs.Arg(0), state, strings.Join(recovery, "; "))
			}
			if failed {
				if opts.jsonOutput {
					return newPrintedJSONOnlyError(fmt.Errorf("resource finished with %s", state))
				}
				return fmt.Errorf("resource finished with %s", state)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return withHints(ctx.Err(), "Resume: "+resume+"; work has not been cancelled")
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func waitRecovery(kind, id, state string) []string {
	commands := [][]string{{"agentlab", kind, "show", id}}
	if kind == "job" {
		commands = append(commands, []string{"agentlab", "job", "artifacts", id}, []string{"agentlab", "job", "artifacts", "download", id})
	}
	if state == "FAILED" || state == "TIMEOUT" {
		commands = append(commands, []string{"agentlab", kind, "doctor", id})
	}
	out := make([]string, 0, len(commands))
	for _, command := range commands {
		out = append(out, formatShellCommand(command))
	}
	return out
}
func waitResult(payload []byte, kind, id, state string, success bool, elapsed time.Duration, recovery []string) ([]byte, error) {
	var record map[string]json.RawMessage
	if err := json.Unmarshal(payload, &record); err != nil {
		return nil, err
	}
	if record == nil {
		return nil, fmt.Errorf("wait response must be an object")
	}
	observation := struct {
		ResourceType   string   `json:"resource_type"`
		ID             string   `json:"id"`
		State          string   `json:"state"`
		Successful     bool     `json:"successful"`
		ElapsedSeconds float64  `json:"elapsed_seconds"`
		CancelledWork  bool     `json:"cancelled_work"`
		NextCommands   []string `json:"next_commands"`
	}{kind, id, state, success, elapsed.Seconds(), false, recovery}
	data, err := json.Marshal(observation)
	if err != nil {
		return nil, err
	}
	record["observation"] = data
	return json.Marshal(record)
}
