// ABOUTME: Command implementations for job, sandbox, workspace, and logs operations.
// ABOUTME: Handles flag parsing, API calls, and output formatting for all CLI commands.

// Package main implements all CLI command handlers for agentlab.
//
// # Command Structure
//
// Commands are organized hierarchically:
//
//	job:       Manage jobs (run, show, artifacts)
//	sandbox:   Manage sandboxes (new, list, show, start, stop, revert, destroy, lease, prune)
//	workspace: Manage workspaces (create, list, attach, detach, rebind)
//	ssh:       Generate SSH connection parameters
//	logs:      View sandbox event logs
//
// # Output Formats
//
// By default, commands produce human-readable text output. With --json,
// commands output JSON for programmatic consumption.
//
// # Flag Parsing
//
// This package uses the standard flag package with custom error handling
// to provide consistent error messages and usage output.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

const (
	defaultLogTail            = 50
	eventPollInterval         = 2 * time.Second
	defaultEventLimit         = 200
	maxEventLimit             = 1000
	defaultRequestTimeout     = 10 * time.Minute
	ttlFlagDescription        = "lease ttl in minutes or duration (e.g. 120 or 2h)"
	jsonFlagDescription       = "output json"
	defaultArtifactBundleName = "agentlab-artifacts.tar.gz"
)

var (
	errHelp  = errors.New("help requested")
	errUsage = errors.New("invalid usage")
)

// usageError wraps an error with a flag indicating whether usage should be shown.
type usageError struct {
	err       error
	showUsage bool
}

func (e usageError) Error() string {
	if e.err == nil {
		return errUsage.Error()
	}
	return fmt.Sprintf("%s: %v", errUsage.Error(), e.err)
}

func (e usageError) Unwrap() error {
	return errUsage
}

func newUsageError(err error, showUsage bool) error {
	return usageError{err: err, showUsage: showUsage}
}

func usageErrorMessage(err error) (string, bool, bool) {
	var ue usageError
	if errors.As(err, &ue) {
		if ue.err != nil {
			return ue.err.Error(), ue.showUsage, true
		}
		return errUsage.Error(), ue.showUsage, true
	}
	return "", false, false
}

// commonFlags contains flags shared by all commands.
type commonFlags struct {
	socketPath string
	jsonOutput bool
	timeout    time.Duration
}

func (c *commonFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&c.socketPath, "socket", c.socketPath, "path to agentlabd socket")
	fs.BoolVar(&c.jsonOutput, "json", c.jsonOutput, jsonFlagDescription)
	fs.DurationVar(&c.timeout, "timeout", c.timeout, "request timeout (e.g. 30s, 2m)")
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// optionalBool is a bool flag that can track whether it was explicitly set.
type optionalBool struct {
	value bool
	set   bool
}

func (o *optionalBool) String() string {
	if o == nil || !o.set {
		return ""
	}
	if o.value {
		return "true"
	}
	return "false"
}

func (o *optionalBool) Set(value string) error {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}
	o.value = parsed
	o.set = true
	return nil
}

func (o *optionalBool) IsBoolFlag() bool {
	return true
}

func (o *optionalBool) Ptr() *bool {
	if o == nil || !o.set {
		return nil
	}
	value := o.value
	return &value
}

func parseFlags(fs *flag.FlagSet, args []string, usage func(), help *bool, jsonOutput bool) error {
	fs.Usage = usage
	if err := fs.Parse(args); err != nil {
		if !jsonOutput {
			usage()
		}
		return newUsageError(err, false)
	}
	if help != nil && *help {
		usage()
		return errHelp
	}
	return nil
}

// runJobCommand dispatches job subcommands (run, show, artifacts).
func runJobCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 {
		printJobUsage()
		return nil
	}
	if isHelpToken(args[0]) {
		printJobUsage()
		return errHelp
	}
	switch args[0] {
	case "run":
		return runJobRun(ctx, args[1:], base)
	case "show":
		return runJobShow(ctx, args[1:], base)
	case "artifacts":
		return runJobArtifacts(ctx, args[1:], base)
	default:
		printJobUsage()
		return fmt.Errorf("unknown job command %q", args[0])
	}
}

// runJobRun creates and starts a new job.
func runJobRun(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("job run")
	opts := base
	opts.bind(fs)
	var repo string
	var ref string
	var profile string
	var task string
	var mode string
	var ttl string
	var keepalive optionalBool
	var help bool
	fs.StringVar(&repo, "repo", "", "git repository url")
	fs.StringVar(&ref, "ref", "", "git ref (default main)")
	fs.StringVar(&profile, "profile", "", "profile name")
	fs.StringVar(&task, "task", "", "task description")
	fs.StringVar(&mode, "mode", "", "mode (default dangerous)")
	fs.StringVar(&ttl, "ttl", "", ttlFlagDescription)
	fs.Var(&keepalive, "keepalive", "keep sandbox after job completion")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printJobRunUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	if repo == "" || profile == "" || task == "" {
		printJobRunUsage()
		return fmt.Errorf("repo, profile, and task are required")
	}
	ttlMinutes, err := parseTTLMinutes(ttl)
	if err != nil {
		return err
	}

	client := newAPIClient(opts.socketPath, opts.timeout)
	req := jobCreateRequest{
		RepoURL:    repo,
		Ref:        ref,
		Profile:    profile,
		Task:       task,
		Mode:       mode,
		TTLMinutes: ttlMinutes,
		Keepalive:  keepalive.Ptr(),
	}
	payload, err := client.doJSON(ctx, http.MethodPost, "/v1/jobs", req)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp jobResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printJob(resp)
	return nil
}

// runJobShow displays details of a single job.
func runJobShow(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("job show")
	opts := base
	opts.bind(fs)
	var eventsTail int
	var help bool
	fs.IntVar(&eventsTail, "events-tail", -1, "number of recent events to include (0 to omit)")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printJobShowUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		printJobShowUsage()
		return fmt.Errorf("job_id is required")
	}
	jobID := strings.TrimSpace(fs.Arg(0))
	if jobID == "" {
		return fmt.Errorf("job_id is required")
	}

	client := newAPIClient(opts.socketPath, opts.timeout)
	query := ""
	if eventsTail >= 0 {
		query = fmt.Sprintf("?events_tail=%d", eventsTail)
	}
	payload, err := client.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/jobs/%s%s", jobID, query), nil)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp jobResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printJob(resp)
	if len(resp.Events) > 0 {
		fmt.Println("Events:")
		printEvents(resp.Events, false)
	}
	return nil
}

// runJobArtifacts handles job artifacts subcommands.
func runJobArtifacts(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 {
		printJobArtifactsUsage()
		return nil
	}
	switch args[0] {
	case "download":
		return runJobArtifactsDownload(ctx, args[1:], base)
	default:
		return runJobArtifactsList(ctx, args, base)
	}
}

func runJobArtifactsList(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("job artifacts")
	opts := base
	opts.bind(fs)
	var help bool
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printJobArtifactsUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		printJobArtifactsUsage()
		return fmt.Errorf("job_id is required")
	}
	jobID := strings.TrimSpace(fs.Arg(0))
	if jobID == "" {
		return fmt.Errorf("job_id is required")
	}

	client := newAPIClient(opts.socketPath, opts.timeout)
	payload, err := client.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/jobs/%s/artifacts", jobID), nil)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp artifactsResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printArtifactsList(resp.Artifacts)
	return nil
}

func runJobArtifactsDownload(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("job artifacts download")
	opts := base
	opts.bind(fs)
	var out string
	var path string
	var name string
	var latest bool
	var bundle bool
	var help bool
	fs.StringVar(&out, "out", "", "output file path or directory")
	fs.StringVar(&path, "path", "", "artifact path to download")
	fs.StringVar(&name, "name", "", "artifact name to download")
	fs.BoolVar(&latest, "latest", false, "download latest artifact")
	fs.BoolVar(&bundle, "bundle", false, "download latest bundle (agentlab-artifacts.tar.gz)")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printJobArtifactsDownloadUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		printJobArtifactsDownloadUsage()
		return fmt.Errorf("job_id is required")
	}
	jobID := strings.TrimSpace(fs.Arg(0))
	if jobID == "" {
		return fmt.Errorf("job_id is required")
	}
	path = strings.TrimSpace(path)
	name = strings.TrimSpace(name)
	if path != "" && name != "" {
		return fmt.Errorf("path and name are mutually exclusive")
	}
	if path == "" && name == "" && !latest && !bundle {
		bundle = true
	}

	client := newAPIClient(opts.socketPath, opts.timeout)
	payload, err := client.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/jobs/%s/artifacts", jobID), nil)
	if err != nil {
		return err
	}
	var resp artifactsResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	if len(resp.Artifacts) == 0 {
		return fmt.Errorf("no artifacts found for job %s", jobID)
	}
	artifact, err := selectArtifact(resp.Artifacts, path, name, latest, bundle)
	if err != nil {
		return err
	}
	if strings.TrimSpace(artifact.Path) == "" {
		artifact.Path = strings.TrimSpace(artifact.Name)
	}
	if strings.TrimSpace(artifact.Path) == "" {
		return fmt.Errorf("selected artifact has no path")
	}

	targetName := strings.TrimSpace(artifact.Name)
	if targetName == "" {
		targetName = filepath.Base(strings.TrimSpace(artifact.Path))
	}
	targetPath, err := resolveArtifactOutPath(out, targetName)
	if err != nil {
		return err
	}

	downloadPath := fmt.Sprintf("/v1/jobs/%s/artifacts/download?path=%s", jobID, url.QueryEscape(artifact.Path))
	respBody, err := client.doRequest(ctx, http.MethodGet, downloadPath, nil, nil)
	if err != nil {
		return err
	}
	defer respBody.Body.Close()

	outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, respBody.Body); err != nil {
		return err
	}
	if err := outFile.Sync(); err != nil {
		return err
	}

	if opts.jsonOutput {
		result := map[string]any{
			"job_id":   jobID,
			"artifact": artifact,
			"out":      targetPath,
		}
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, _ = os.Stdout.Write(append(data, '\n'))
		return nil
	}
	fmt.Printf("downloaded %s to %s\n", artifact.Path, targetPath)
	return nil
}

// runSandboxCommand dispatches sandbox subcommands.
func runSandboxCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 {
		printSandboxUsage()
		return nil
	}
	if isHelpToken(args[0]) {
		printSandboxUsage()
		return errHelp
	}
	switch args[0] {
	case "new":
		return runSandboxNew(ctx, args[1:], base)
	case "list":
		return runSandboxList(ctx, args[1:], base)
	case "show":
		return runSandboxShow(ctx, args[1:], base)
	case "start":
		return runSandboxStart(ctx, args[1:], base)
	case "stop":
		return runSandboxStop(ctx, args[1:], base)
	case "revert":
		return runSandboxRevert(ctx, args[1:], base)
	case "destroy":
		return runSandboxDestroy(ctx, args[1:], base)
	case "lease":
		return runSandboxLease(ctx, args[1:], base)
	case "prune":
		return runSandboxPrune(ctx, args[1:], base)
	default:
		printSandboxUsage()
		return fmt.Errorf("unknown sandbox command %q", args[0])
	}
}

// runWorkspaceCommand dispatches workspace subcommands.
func runWorkspaceCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 {
		printWorkspaceUsage()
		return nil
	}
	if isHelpToken(args[0]) {
		printWorkspaceUsage()
		return errHelp
	}
	switch args[0] {
	case "create":
		return runWorkspaceCreate(ctx, args[1:], base)
	case "list":
		return runWorkspaceList(ctx, args[1:], base)
	case "attach":
		return runWorkspaceAttach(ctx, args[1:], base)
	case "detach":
		return runWorkspaceDetach(ctx, args[1:], base)
	case "rebind":
		return runWorkspaceRebind(ctx, args[1:], base)
	default:
		printWorkspaceUsage()
		return fmt.Errorf("unknown workspace command %q", args[0])
	}
}

func runSandboxNew(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox new")
	opts := base
	opts.bind(fs)
	var name string
	var profile string
	var ttl string
	var workspace string
	var vmid int
	var jobID string
	var keepalive optionalBool
	var help bool
	fs.StringVar(&name, "name", "", "sandbox name")
	fs.StringVar(&profile, "profile", "", "profile name")
	fs.StringVar(&ttl, "ttl", "", ttlFlagDescription)
	fs.StringVar(&workspace, "workspace", "", "workspace id or name")
	fs.IntVar(&vmid, "vmid", 0, "vmid override")
	fs.StringVar(&jobID, "job", "", "attach to existing job id")
	fs.Var(&keepalive, "keepalive", "enable keepalive lease for sandbox")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printSandboxNewUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	if profile == "" {
		printSandboxNewUsage()
		return fmt.Errorf("profile is required")
	}
	ttlMinutes, err := parseTTLMinutes(ttl)
	if err != nil {
		return err
	}
	var workspaceID *string
	if strings.TrimSpace(workspace) != "" {
		value := strings.TrimSpace(workspace)
		workspaceID = &value
	}
	var vmidPtr *int
	if vmid > 0 {
		value := vmid
		vmidPtr = &value
	}

	client := newAPIClient(opts.socketPath, opts.timeout)
	req := sandboxCreateRequest{
		Name:       name,
		Profile:    profile,
		Keepalive:  keepalive.Ptr(),
		TTLMinutes: ttlMinutes,
		Workspace:  workspaceID,
		VMID:       vmidPtr,
		JobID:      jobID,
	}
	payload, err := client.doJSON(ctx, http.MethodPost, "/v1/sandboxes", req)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sandboxResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printSandbox(resp)
	return nil
}

func runSandboxList(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox list")
	opts := base
	opts.bind(fs)
	var help bool
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printSandboxListUsage, &help, opts.jsonOutput); err != nil {
		return err
	}

	client := newAPIClient(opts.socketPath, opts.timeout)
	payload, err := client.doJSON(ctx, http.MethodGet, "/v1/sandboxes", nil)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sandboxesResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printSandboxList(resp.Sandboxes)
	return nil
}

func runSandboxShow(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox show")
	opts := base
	opts.bind(fs)
	var help bool
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printSandboxShowUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		printSandboxShowUsage()
		return fmt.Errorf("vmid is required")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}

	client := newAPIClient(opts.socketPath, opts.timeout)
	payload, err := client.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/sandboxes/%d", vmid), nil)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sandboxResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printSandbox(resp)
	return nil
}

func runSandboxStart(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox start")
	opts := base
	opts.bind(fs)
	var help bool
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printSandboxStartUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		printSandboxStartUsage()
		return fmt.Errorf("vmid is required")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}

	client := newAPIClient(opts.socketPath, opts.timeout)
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/sandboxes/%d/start", vmid), nil)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sandboxResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	fmt.Printf("sandbox %d started (state=%s)\n", resp.VMID, resp.State)
	return nil
}

func runSandboxStop(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox stop")
	opts := base
	opts.bind(fs)
	var help bool
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printSandboxStopUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		printSandboxStopUsage()
		return fmt.Errorf("vmid is required")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}

	client := newAPIClient(opts.socketPath, opts.timeout)
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/sandboxes/%d/stop", vmid), nil)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sandboxResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	fmt.Printf("sandbox %d stopped (state=%s)\n", resp.VMID, resp.State)
	return nil
}

func runSandboxRevert(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox revert")
	opts := base
	opts.bind(fs)
	var force bool
	var noRestart bool
	var restart bool
	var help bool
	fs.BoolVar(&force, "force", false, "force revert even if a job is running")
	fs.BoolVar(&noRestart, "no-restart", false, "do not restart the sandbox after revert")
	fs.BoolVar(&restart, "restart", false, "restart the sandbox after revert")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printSandboxRevertUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		printSandboxRevertUsage()
		return fmt.Errorf("vmid is required")
	}
	if noRestart && restart {
		return fmt.Errorf("cannot use --restart and --no-restart together")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}
	var restartPtr *bool
	if noRestart {
		value := false
		restartPtr = &value
	}
	if restart {
		value := true
		restartPtr = &value
	}

	client := newAPIClient(opts.socketPath, opts.timeout)
	req := sandboxRevertRequest{Force: force, Restart: restartPtr}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/sandboxes/%d/revert", vmid), req)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sandboxRevertResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	state := resp.Sandbox.State
	snapshot := resp.Snapshot
	if snapshot == "" {
		snapshot = "clean"
	}
	if resp.Restarted {
		fmt.Printf("sandbox %d reverted to snapshot %s (state=%s, restarted)\n", resp.Sandbox.VMID, snapshot, state)
	} else {
		fmt.Printf("sandbox %d reverted to snapshot %s (state=%s)\n", resp.Sandbox.VMID, snapshot, state)
	}
	return nil
}

func runSandboxDestroy(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox destroy")
	opts := base
	opts.bind(fs)
	var force bool
	var help bool
	fs.BoolVar(&force, "force", false, "force destroy even if in invalid state")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printSandboxDestroyUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		printSandboxDestroyUsage()
		return fmt.Errorf("vmid is required")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}

	client := newAPIClient(opts.socketPath, opts.timeout)
	req := sandboxDestroyRequest{Force: force}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/sandboxes/%d/destroy", vmid), req)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sandboxResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	fmt.Printf("sandbox %d destroyed (state=%s)\n", resp.VMID, resp.State)
	return nil
}

func runSandboxLease(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 {
		printSandboxLeaseUsage()
		return nil
	}
	if isHelpToken(args[0]) {
		printSandboxLeaseUsage()
		return errHelp
	}
	switch args[0] {
	case "renew":
		return runSandboxLeaseRenew(ctx, args[1:], base)
	default:
		printSandboxLeaseUsage()
		return fmt.Errorf("unknown sandbox lease command %q", args[0])
	}
}

func runSandboxLeaseRenew(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox lease renew")
	opts := base
	opts.bind(fs)
	var ttl string
	var help bool
	fs.StringVar(&ttl, "ttl", "", ttlFlagDescription)
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printSandboxLeaseRenewUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		printSandboxLeaseRenewUsage()
		return fmt.Errorf("vmid is required")
	}
	if ttl == "" {
		printSandboxLeaseRenewUsage()
		return fmt.Errorf("ttl is required. Flags must come before vmid (e.g., --ttl 120 1009)")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}
	minutes, err := parseRequiredTTLMinutes(ttl)
	if err != nil {
		return err
	}

	client := newAPIClient(opts.socketPath, opts.timeout)
	req := leaseRenewRequest{TTLMinutes: minutes}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/sandboxes/%d/lease/renew", vmid), req)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp leaseRenewResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	fmt.Printf("sandbox %d lease renewed until %s\n", resp.VMID, resp.LeaseExpires)
	return nil
}

func runSandboxPrune(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox prune")
	opts := base
	opts.bind(fs)
	var help bool
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printSandboxPruneUsage, &help, opts.jsonOutput); err != nil {
		return err
	}

	client := newAPIClient(opts.socketPath, opts.timeout)
	payload, err := client.doJSON(ctx, http.MethodPost, "/v1/sandboxes/prune", nil)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp pruneResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	fmt.Printf("pruned %d sandbox(es)\n", resp.Count)
	return nil
}

func runWorkspaceCreate(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace create")
	opts := base
	opts.bind(fs)
	var name string
	var size string
	var storage string
	var help bool
	fs.StringVar(&name, "name", "", "workspace name")
	fs.StringVar(&size, "size", "", "workspace size (e.g. 80G)")
	fs.StringVar(&storage, "storage", "", "Proxmox storage (default local-zfs)")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printWorkspaceCreateUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	size = strings.TrimSpace(size)
	storage = strings.TrimSpace(storage)
	if name == "" || size == "" {
		printWorkspaceCreateUsage()
		return fmt.Errorf("name and size are required")
	}
	sizeGB, err := parseSizeGB(size)
	if err != nil {
		return err
	}

	client := newAPIClient(opts.socketPath, opts.timeout)
	req := workspaceCreateRequest{
		Name:    name,
		SizeGB:  sizeGB,
		Storage: storage,
	}
	payload, err := client.doJSON(ctx, http.MethodPost, "/v1/workspaces", req)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp workspaceResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printWorkspace(resp)
	return nil
}

func runWorkspaceList(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace list")
	opts := base
	opts.bind(fs)
	var help bool
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printWorkspaceListUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	client := newAPIClient(opts.socketPath, opts.timeout)
	payload, err := client.doJSON(ctx, http.MethodGet, "/v1/workspaces", nil)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp workspacesResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printWorkspaceList(resp.Workspaces)
	return nil
}

func runWorkspaceAttach(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace attach")
	opts := base
	opts.bind(fs)
	var help bool
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printWorkspaceAttachUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		printWorkspaceAttachUsage()
		return fmt.Errorf("workspace and vmid are required")
	}
	workspace := strings.TrimSpace(fs.Arg(0))
	if workspace == "" {
		return fmt.Errorf("workspace is required")
	}
	vmid, err := parseVMID(fs.Arg(1))
	if err != nil {
		return err
	}
	client := newAPIClient(opts.socketPath, opts.timeout)
	req := workspaceAttachRequest{VMID: vmid}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/workspaces/%s/attach", workspace), req)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp workspaceResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printWorkspace(resp)
	return nil
}

func runWorkspaceDetach(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace detach")
	opts := base
	opts.bind(fs)
	var help bool
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printWorkspaceDetachUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		printWorkspaceDetachUsage()
		return fmt.Errorf("workspace is required")
	}
	workspace := strings.TrimSpace(fs.Arg(0))
	if workspace == "" {
		return fmt.Errorf("workspace is required")
	}
	client := newAPIClient(opts.socketPath, opts.timeout)
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/workspaces/%s/detach", workspace), nil)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp workspaceResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printWorkspace(resp)
	return nil
}

func runWorkspaceRebind(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace rebind")
	opts := base
	opts.bind(fs)
	var profile string
	var ttl string
	var keepOld bool
	var help bool
	fs.StringVar(&profile, "profile", "", "profile name")
	fs.StringVar(&ttl, "ttl", "", ttlFlagDescription)
	fs.BoolVar(&keepOld, "keep-old", false, "keep old sandbox running")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printWorkspaceRebindUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		printWorkspaceRebindUsage()
		return fmt.Errorf("workspace is required")
	}
	workspace := strings.TrimSpace(fs.Arg(0))
	if workspace == "" {
		return fmt.Errorf("workspace is required")
	}
	profile = strings.TrimSpace(profile)
	if profile == "" {
		printWorkspaceRebindUsage()
		return fmt.Errorf("profile is required")
	}
	ttlMinutes, err := parseTTLMinutes(ttl)
	if err != nil {
		return err
	}

	client := newAPIClient(opts.socketPath, opts.timeout)
	req := workspaceRebindRequest{
		Profile:    profile,
		TTLMinutes: ttlMinutes,
		KeepOld:    keepOld,
	}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/workspaces/%s/rebind", workspace), req)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp workspaceRebindResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printWorkspaceRebind(resp, keepOld)
	return nil
}

// runLogsCommand displays sandbox event logs, with optional follow mode.
func runLogsCommand(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("logs")
	opts := base
	opts.bind(fs)
	var follow bool
	var tail int
	var help bool
	fs.BoolVar(&follow, "follow", false, "follow new events")
	fs.IntVar(&tail, "tail", defaultLogTail, "show the last N events")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printLogsUsage, &help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		printLogsUsage()
		return fmt.Errorf("vmid is required")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}
	if tail <= 0 {
		tail = defaultLogTail
	}
	if tail > maxEventLimit {
		tail = maxEventLimit
	}

	client := newAPIClient(opts.socketPath, opts.timeout)
	resp, err := fetchEvents(ctx, client, vmid, tail, 0)
	if err != nil {
		return err
	}
	lastID := printEvents(resp.Events, opts.jsonOutput)
	if resp.LastID > lastID {
		lastID = resp.LastID
	}
	if !follow {
		return nil
	}
	if lastID == 0 {
		lastID = resp.LastID
	}

	ticker := time.NewTicker(eventPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			resp, err := fetchEvents(ctx, client, vmid, 0, lastID)
			if err != nil {
				return err
			}
			latest := printEvents(resp.Events, opts.jsonOutput)
			if latest > lastID {
				lastID = latest
			}
			if resp.LastID > lastID {
				lastID = resp.LastID
			}
		}
	}
}

// fetchEvents retrieves sandbox events from the daemon.
// If tail > 0, fetches the last N events. If after > 0, fetches events after the given ID.
func fetchEvents(ctx context.Context, client *apiClient, vmid int, tail int, after int64) (eventsResponse, error) {
	query := ""
	limit := defaultEventLimit
	if limit > maxEventLimit {
		limit = maxEventLimit
	}
	if tail > 0 {
		query = fmt.Sprintf("?tail=%d", tail)
	} else if after > 0 {
		query = fmt.Sprintf("?after=%d&limit=%d", after, limit)
	} else {
		query = fmt.Sprintf("?limit=%d", limit)
	}
	payload, err := client.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/sandboxes/%d/events%s", vmid, query), nil)
	if err != nil {
		return eventsResponse{}, err
	}
	var resp eventsResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return eventsResponse{}, err
	}
	return resp, nil
}

func printSandbox(sb sandboxResponse) {
	fmt.Printf("VMID: %d\n", sb.VMID)
	fmt.Printf("Name: %s\n", sb.Name)
	fmt.Printf("Profile: %s\n", sb.Profile)
	fmt.Printf("State: %s\n", sb.State)
	fmt.Printf("IP: %s\n", orDash(sb.IP))
	fmt.Printf("Workspace: %s\n", orDashPtr(sb.WorkspaceID))
	fmt.Printf("Keepalive: %t\n", sb.Keepalive)
	fmt.Printf("Lease Expires: %s\n", orDashPtr(sb.LeaseExpires))
	fmt.Printf("Created At: %s\n", sb.CreatedAt)
	fmt.Printf("Updated At: %s\n", sb.LastUpdatedAt)
}

func printSandboxList(sandboxes []sandboxResponse) {
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "VMID\tNAME\tPROFILE\tSTATE\tIP\tLEASE")
	for _, sb := range sandboxes {
		lease := orDashPtr(sb.LeaseExpires)
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n", sb.VMID, sb.Name, sb.Profile, sb.State, orDash(sb.IP), lease)
	}
	_ = w.Flush()
}

func printWorkspace(ws workspaceResponse) {
	fmt.Printf("ID: %s\n", ws.ID)
	fmt.Printf("Name: %s\n", ws.Name)
	fmt.Printf("Storage: %s\n", ws.Storage)
	fmt.Printf("Volume ID: %s\n", ws.VolumeID)
	fmt.Printf("Size GB: %d\n", ws.SizeGB)
	fmt.Printf("Attached VMID: %s\n", vmidString(ws.AttachedVMID))
	fmt.Printf("Created At: %s\n", ws.CreatedAt)
	fmt.Printf("Updated At: %s\n", ws.UpdatedAt)
}

func printWorkspaceList(workspaces []workspaceResponse) {
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tSIZE(GB)\tSTORAGE\tATTACHED")
	for _, ws := range workspaces {
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", ws.ID, ws.Name, ws.SizeGB, ws.Storage, vmidString(ws.AttachedVMID))
	}
	_ = w.Flush()
}

func printWorkspaceRebind(resp workspaceRebindResponse, keepOld bool) {
	fmt.Printf("Workspace: %s\n", resp.Workspace.Name)
	fmt.Printf("New VMID: %d\n", resp.Sandbox.VMID)
	fmt.Printf("New IP: %s\n", orDash(resp.Sandbox.IP))
	if resp.OldVMID != nil {
		status := "destroyed"
		if keepOld {
			status = "kept"
		}
		fmt.Printf("Old VMID: %d (%s)\n", *resp.OldVMID, status)
	}
}

func printJob(job jobResponse) {
	fmt.Printf("Job ID: %s\n", job.ID)
	fmt.Printf("Repo: %s\n", job.RepoURL)
	fmt.Printf("Ref: %s\n", job.Ref)
	fmt.Printf("Profile: %s\n", job.Profile)
	fmt.Printf("Task: %s\n", job.Task)
	fmt.Printf("Mode: %s\n", job.Mode)
	fmt.Printf("Status: %s\n", job.Status)
	fmt.Printf("Keepalive: %t\n", job.Keepalive)
	fmt.Printf("TTL Minutes: %s\n", ttlMinutesString(job.TTLMinutes))
	fmt.Printf("Sandbox VMID: %s\n", vmidString(job.SandboxVMID))
	fmt.Printf("Created At: %s\n", job.CreatedAt)
	fmt.Printf("Updated At: %s\n", job.UpdatedAt)
}

func printArtifactsList(artifacts []artifactInfo) {
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPATH\tSIZE(B)\tMIME\tCREATED\tSHA256")
	for _, artifact := range artifacts {
		name := strings.TrimSpace(artifact.Name)
		if name == "" {
			name = filepath.Base(strings.TrimSpace(artifact.Path))
		}
		sha := strings.TrimSpace(artifact.Sha256)
		if len(sha) > 12 {
			sha = sha[:12]
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n",
			orDash(name),
			orDash(artifact.Path),
			artifact.SizeBytes,
			orDash(artifact.MIME),
			orDash(artifact.CreatedAt),
			orDash(sha),
		)
	}
	_ = w.Flush()
}

func printEvents(events []eventResponse, jsonOutput bool) int64 {
	var lastID int64
	for _, ev := range events {
		if ev.ID > lastID {
			lastID = ev.ID
		}
		if jsonOutput {
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			_, _ = os.Stdout.Write(append(data, '\n'))
			continue
		}
		job := "-"
		if strings.TrimSpace(ev.JobID) != "" {
			job = ev.JobID
		}
		msg := strings.TrimSpace(ev.Message)
		if msg == "" {
			msg = "-"
		}
		fmt.Printf("%s\t%s\tjob=%s\t%s\n", ev.Timestamp, ev.Kind, job, msg)
	}
	return lastID
}

// selectArtifact selects an artifact from a list based on the provided criteria.
// Checks path, name, bundle, and latest in that order.
func selectArtifact(artifacts []artifactInfo, path, name string, latest, bundle bool) (artifactInfo, error) {
	if len(artifacts) == 0 {
		return artifactInfo{}, fmt.Errorf("no artifacts found")
	}
	if path != "" {
		for _, artifact := range artifacts {
			if strings.TrimSpace(artifact.Path) == path {
				return artifact, nil
			}
		}
		return artifactInfo{}, fmt.Errorf("artifact path %q not found", path)
	}
	if name != "" {
		if strings.ContainsAny(name, "/\\") {
			return artifactInfo{}, fmt.Errorf("artifact name must not contain path separators")
		}
		var match *artifactInfo
		for i := range artifacts {
			if artifacts[i].Name == name {
				match = &artifacts[i]
			}
		}
		if match == nil {
			return artifactInfo{}, fmt.Errorf("artifact name %q not found", name)
		}
		return *match, nil
	}
	if bundle {
		var match *artifactInfo
		for i := range artifacts {
			if artifacts[i].Name == defaultArtifactBundleName {
				match = &artifacts[i]
			}
		}
		if match != nil {
			return *match, nil
		}
	}
	if latest || bundle {
		return artifacts[len(artifacts)-1], nil
	}
	return artifacts[len(artifacts)-1], nil
}

func resolveArtifactOutPath(out, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "artifact"
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return name, nil
	}
	if strings.HasSuffix(out, string(os.PathSeparator)) {
		if err := os.MkdirAll(out, 0o750); err != nil {
			return "", err
		}
		return filepath.Join(out, name), nil
	}
	if info, err := os.Stat(out); err == nil && info.IsDir() {
		return filepath.Join(out, name), nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	dir := filepath.Dir(out)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return "", err
		}
	}
	return out, nil
}

// parseVMID parses and validates a VM ID from a string.
func parseVMID(value string) (int, error) {
	vmid, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || vmid <= 0 {
		return 0, fmt.Errorf("invalid vmid %q", value)
	}
	return vmid, nil
}

// parseTTLMinutes parses a TTL value, accepting either minutes (int) or a duration string.
// Returns nil if the value is empty.
func parseTTLMinutes(value string) (*int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if minutes, err := strconv.Atoi(value); err == nil {
		if minutes <= 0 {
			return nil, fmt.Errorf("ttl must be positive")
		}
		return &minutes, nil
	}
	dur, err := time.ParseDuration(value)
	if err != nil {
		return nil, fmt.Errorf("invalid ttl %q", value)
	}
	if dur <= 0 {
		return nil, fmt.Errorf("ttl must be positive")
	}
	minutes := int(math.Ceil(dur.Minutes()))
	if minutes <= 0 {
		minutes = 1
	}
	return &minutes, nil
}

// parseRequiredTTLMinutes parses a TTL value that must be provided and positive.
func parseRequiredTTLMinutes(value string) (int, error) {
	minutes, err := parseTTLMinutes(value)
	if err != nil {
		return 0, err
	}
	if minutes == nil || *minutes <= 0 {
		return 0, fmt.Errorf("ttl must be positive")
	}
	return *minutes, nil
}

// parseSizeGB parses a disk size string (e.g., "80G", "80GB") into gigabytes.
func parseSizeGB(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("size is required")
	}
	lower := strings.ToLower(value)
	switch {
	case strings.HasSuffix(lower, "gb"):
		lower = strings.TrimSuffix(lower, "gb")
	case strings.HasSuffix(lower, "g"):
		lower = strings.TrimSuffix(lower, "g")
	}
	lower = strings.TrimSpace(lower)
	size, err := strconv.Atoi(lower)
	if err != nil || size <= 0 {
		return 0, fmt.Errorf("invalid size %q", value)
	}
	return size, nil
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func orDashPtr(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "-"
	}
	return *value
}

func ttlMinutesString(value *int) string {
	if value == nil || *value == 0 {
		return "-"
	}
	return strconv.Itoa(*value)
}

func vmidString(value *int) string {
	if value == nil || *value <= 0 {
		return "-"
	}
	return strconv.Itoa(*value)
}
