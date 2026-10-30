package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/agentlab/agentlab/internal/operations"
	"os"
	"time"
)

func runOperationCommandContext(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("operation")
	help := bindHelpFlag(fs)
	fs.BoolVar(&base.jsonOutput, "json", base.jsonOutput, jsonFlagDescription)
	usage := func() { fmt.Fprintln(os.Stdout, "Usage: agentlab operation <show|resume|cancel> ID") }
	if err := parseFlags(fs, args, usage, help, base.jsonOutput); err != nil {
		return err
	}
	args = fs.Args()
	if len(args) != 2 {
		return fmt.Errorf("operation requires an action and an operation ID")
	}
	store, err := operationStore()
	if err != nil {
		return err
	}
	reference, referenceErr := readSetupReference(store, args[1])
	if referenceErr == nil {
		return runRemoteOperationCommand(ctx, store, reference, args[0], base)
	}
	if !errors.Is(referenceErr, os.ErrNotExist) {
		return referenceErr
	}
	record, err := store.Read(args[1])
	if err != nil {
		return err
	}
	switch args[0] {
	case "show":
		return printSetupRecord(record, base.jsonOutput)
	case "cancel":
		if record.State == "succeeded" || record.State == "cancelled" {
			if base.jsonOutput {
				return json.NewEncoder(os.Stdout).Encode(map[string]any{"operation_id": record.ID, "state": record.State, "work_stopped": true, "cleanup_status": record.Cleanup})
			}
			fmt.Fprintf(os.Stdout, "Operation %s has already stopped (%s). Cleanup: %s\n", record.ID, record.State, record.Cleanup)
			return nil
		}
		if err := store.Cancel(record.ID); err != nil {
			return err
		}
		if base.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"operation_id": record.ID, "state": "cancellation_requested", "work_stopped": false, "cleanup_status": record.Cleanup})
		}
		fmt.Fprintf(os.Stdout, "Cancellation requested for %s. Work can remain active until the current step stops.\n", record.ID)
		return nil
	default:
		return fmt.Errorf("unknown operation action %q", args[0])
	}
}

func runRemoteOperationCommand(ctx context.Context, store operations.Store, request serverSetupRequest, action string, base commonFlags) error {
	ssh, options, err := setupReferenceSSH(request)
	if err != nil {
		return err
	}
	if action == "resume" {
		record, err := executeRemoteSetup(ctx, store, request, "", false, time.Duration(request.Input.WaitSeconds)*time.Second)
		if err != nil {
			return err
		}
		return printSetupRecord(record, base.jsonOutput)
	}
	if action == "cancel" {
		if err := store.Cancel(request.OperationID); err != nil {
			return err
		}
	}
	command := action
	if command != "show" && command != "cancel" && command != "start" {
		return fmt.Errorf("unknown operation action %q", action)
	}
	record, err := remoteSetupCall(ctx, ssh, options, request, command)
	if err != nil {
		if action == "cancel" {
			local, _ := store.Read(request.OperationID)
			local.Worker = nil
			return &setupRemoteError{Record: local, Code: "setup_cancellation_pending", Action: "cancellation is saved locally; inspect the server operation and retained staging files"}
		}
		return err
	}
	if err := saveRemoteSetupObservation(store, &record); err != nil {
		return err
	}

	if action == "cancel" {
		stopped := record.Worker != nil && !record.Worker.Active
		state := "cancellation_requested"
		if stopped && (record.State == operations.Succeeded || record.State == operations.Cancelled) {
			state = string(record.State)
		}
		if base.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"operation_id": record.ID, "state": state, "worker_stopped": stopped, "work_stopped": stopped && record.State == operations.Succeeded && record.Cleanup == "verified", "worker": record.Worker, "cleanup_status": record.Cleanup, "operation": record})
		}
		fmt.Fprintf(os.Stdout, "Operation %s: %s. Worker stopped: %t. Cleanup: %s\n", record.ID, state, stopped, record.Cleanup)
		return nil
	}
	return printSetupRecord(record, base.jsonOutput)
}
